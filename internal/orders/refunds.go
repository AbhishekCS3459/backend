package orders

import (
	"context"
	"fmt"

	"github.com/AbhishekCS3459/find-me-backend/internal/payment"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/realtime"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// maxRefundAttempts failed refunds park a payment for someone to look at.
const maxRefundAttempts = 10

// refundNow tries the refunds a request just queued. Failures stay queued for
// the sweeper, so the request itself never fails because of the gateway.
func (s *Service) refundNow(ctx context.Context, paymentIDs []uuid.UUID) {
	for _, id := range paymentIDs {
		if _, err := s.refundOne(context.WithoutCancel(ctx), &id); err != nil {
			log.Warn().Err(err).Str("payment_id", id.String()).Msg("refund failed; it will be retried")
		}
	}
}

// RefundDue retries queued refunds that are due, up to limit, and returns how many it attempted.
func (s *Service) RefundDue(ctx context.Context, limit int) (int, error) {
	for n := range limit {
		claimed, err := s.refundOne(ctx, nil)
		if err != nil {
			log.Warn().Err(err).Msg("refund failed; it will be retried")
		}
		if !claimed {
			return n, nil
		}
	}
	return limit, nil
}

type refundClaim struct {
	ID                uuid.UUID
	OrderID           uuid.UUID
	Provider          string
	ProviderPaymentID string
	AmountPaise       int64
	RefundAttempts    int
}

// refundOne claims a due refund (the given one, or the oldest due) and asks
// the gateway for it. Claiming pushes the next attempt back first, so a
// crash mid-call is retried later, never lost; the gateway is called outside
// any transaction. It reports whether it claimed anything.
func (s *Service) refundOne(ctx context.Context, only *uuid.UUID) (bool, error) {
	now := s.now()
	filter, args := ``, []any{now, now, now}
	if only != nil {
		filter, args = ` AND id = ?`, append(args, *only)
	}
	var claims []refundClaim
	err := s.db.WithContext(ctx).Raw(`
		UPDATE payment SET refund_attempts = refund_attempts + 1, updated_at = ?,
			next_refund_at = ?::timestamptz + make_interval(secs => LEAST(600, 30 * power(2, refund_attempts)))
		WHERE id = (
			SELECT id FROM payment
			WHERE status = 'REFUND_PENDING' AND next_refund_at <= ?`+filter+`
			ORDER BY next_refund_at
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, order_id, provider, provider_payment_id, amount_paise, refund_attempts`, args...).
		Scan(&claims).Error
	if err != nil {
		return false, fmt.Errorf("claim refund: %w", err)
	}
	if len(claims) == 0 {
		return false, nil
	}
	c := claims[0]
	if c.Provider != s.provider.Name() {
		return true, s.refundFailed(ctx, c, fmt.Errorf("payment was taken by %q, not the configured provider", c.Provider))
	}
	err = s.provider.Refund(ctx, payment.RefundRequest{
		PaymentID: c.ID.String(), ProviderPaymentID: c.ProviderPaymentID, AmountPaise: c.AmountPaise,
	})
	if err != nil {
		return true, s.refundFailed(ctx, c, err)
	}
	return true, s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(`UPDATE payment SET status = 'REFUNDED', next_refund_at = NULL, last_error = NULL, updated_at = ?
			WHERE id = ? AND status = 'REFUND_PENDING'`, s.now(), c.ID)
		if res.Error != nil || res.RowsAffected == 0 {
			return res.Error
		}
		var stores []struct{ StoreID uuid.UUID }
		err := tx.Raw(`
			UPDATE orders SET payment_status = 'REFUNDED', updated_at = ?
			WHERE id = ? AND payment_status = 'REFUND_PENDING'
				AND NOT EXISTS (SELECT 1 FROM payment WHERE order_id = ? AND status = 'REFUND_PENDING')
			RETURNING store_id`, s.now(), c.OrderID, c.OrderID).Scan(&stores).Error
		if err != nil || len(stores) == 0 {
			return err
		}
		return realtime.Notify(tx, ChangesChannel, stores[0].StoreID.String())
	})
}

func (s *Service) refundFailed(ctx context.Context, c refundClaim, cause error) error {
	parked := c.RefundAttempts >= maxRefundAttempts
	err := s.db.WithContext(ctx).Exec(`
		UPDATE payment SET last_error = ?, updated_at = ?,
			next_refund_at = CASE WHEN ? THEN NULL ELSE next_refund_at END
		WHERE id = ?`, truncate(cause.Error(), 1000), s.now(), parked, c.ID).Error
	if parked {
		log.Error().Err(cause).Str("payment_id", c.ID.String()).Int("attempts", c.RefundAttempts).
			Msg("refund parked after too many failures; refund it by hand")
	}
	if err != nil {
		return err
	}
	return cause
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
