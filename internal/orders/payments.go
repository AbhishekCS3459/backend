package orders

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/AbhishekCS3459/find-me-backend/internal/inventory"
	"github.com/AbhishekCS3459/find-me-backend/internal/payment"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// StartPayment opens a payment for an online order waiting to be paid, or
// returns the one already open. The gateway is called outside any
// transaction; if the order stops waiting meanwhile, nothing is recorded.
func (s *Service) StartPayment(ctx context.Context, customerID, orderID uuid.UUID) (*Payment, error) {
	db := s.db.WithContext(ctx)
	var rows []orderRow
	if err := db.Raw(`SELECT `+orderRowColumns+` FROM orders WHERE id = ? AND customer_id = ?`,
		orderID, customerID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load order: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	o := rows[0]
	if o.PaymentMode != PaymentOnline {
		return nil, ErrNotOnlinePayment
	}
	if o.Status != StatusPendingPayment {
		return nil, &StateError{Status: o.Status, Action: "pay for"}
	}
	if p, err := openPayment(db, orderID); err != nil || p != nil {
		return s.viewOf(p), err
	}

	paymentID := uuid.New()
	providerPaymentID, err := s.provider.CreatePayment(ctx, payment.CreateRequest{
		PaymentID: paymentID.String(), OrderID: orderID.String(), OrderCode: o.Code,
		AmountPaise: o.TotalPaise, Currency: currencyINR,
	})
	if err != nil {
		log.Error().Err(err).Str("order_id", orderID.String()).Msg("payment provider create failed")
		return nil, ErrProvider
	}

	var p *paymentRecord
	err = db.Transaction(func(tx *gorm.DB) error {
		locked, err := lockOrder(tx, orderID)
		if err != nil {
			return err
		}
		if locked.Status != StatusPendingPayment {
			return &StateError{Status: locked.Status, Action: "pay for"}
		}
		res := tx.Exec(`
			INSERT INTO payment (id, order_id, provider, provider_payment_id, status, amount_paise, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'CREATED', ?, ?, ?)
			ON CONFLICT (order_id) WHERE status = 'CREATED' DO NOTHING`,
			paymentID, orderID, s.provider.Name(), providerPaymentID, locked.TotalPaise, s.now(), s.now())
		if res.Error != nil {
			return fmt.Errorf("record payment: %w", res.Error)
		}
		p, err = openPayment(tx, orderID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.viewOf(p), nil
}

func (s *Service) viewOf(p *paymentRecord) *Payment {
	if p == nil {
		return nil
	}
	v := s.paymentView(p)
	return &v
}

func openPayment(db *gorm.DB, orderID uuid.UUID) (*paymentRecord, error) {
	var rows []paymentRecord
	if err := db.Raw(`SELECT `+paymentColumns+` FROM payment WHERE order_id = ? AND status = 'CREATED'`,
		orderID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load open payment: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// Simulate completes a dummy payment as the gateway would: it builds the
// gateway's event and processes it exactly like a webhook.
func (s *Service) Simulate(
	ctx context.Context, customerID, orderID, paymentID uuid.UUID, outcome payment.Outcome,
) (*Order, error) {
	sim, ok := s.provider.(payment.Simulator)
	if !ok {
		return nil, ErrNotSimulated
	}
	db := s.db.WithContext(ctx)
	if err := ownedBy(db, customerID, orderID); err != nil {
		return nil, err
	}
	var rows []paymentRecord
	if err := db.Raw(`SELECT `+paymentColumns+` FROM payment WHERE id = ? AND order_id = ?`,
		paymentID, orderID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load payment: %w", err)
	}
	if len(rows) == 0 || rows[0].Provider != s.provider.Name() {
		return nil, ErrPaymentNotFound
	}
	if err := s.ProcessEvent(ctx, sim.Simulate(rows[0].ProviderPaymentID, rows[0].AmountPaise, outcome)); err != nil {
		return nil, err
	}
	return s.load(db, orderID, viewCustomer)
}

// HandleWebhook verifies and processes a gateway webhook delivery.
func (s *Service) HandleWebhook(ctx context.Context, provider string, header http.Header, body []byte) error {
	if provider != s.provider.Name() {
		return payment.ErrWebhooksUnsupported
	}
	ev, err := s.provider.ParseWebhook(header, body)
	if err != nil {
		return err
	}
	return s.ProcessEvent(ctx, ev)
}

// ProcessEvent applies a gateway event once, however often it is delivered.
//
//   - Success while the order waits: the order is placed for the store to accept.
//   - Success after the hold expired: the stock is reserved again if it is
//     still there; otherwise the money goes back.
//   - Success for an order that is cancelled or already paid: refunded.
//   - Failure: the attempt closes; the customer may try again until the hold ends.
func (s *Service) ProcessEvent(ctx context.Context, ev payment.Event) error {
	if ev.ID == "" || ev.ProviderPaymentID == "" {
		return payment.ErrInvalidWebhook
	}
	var refunds []uuid.UUID
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(`INSERT INTO payment_webhook_event (provider, event_id, payload) VALUES (?, ?, ?::jsonb)
			ON CONFLICT DO NOTHING`, s.provider.Name(), ev.ID, string(rawOrEmpty(ev.Raw)))
		if res.Error != nil {
			return fmt.Errorf("record webhook event: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return nil
		}
		var found []paymentRecord
		if err := tx.Raw(`SELECT `+paymentColumns+` FROM payment WHERE provider = ? AND provider_payment_id = ?`,
			s.provider.Name(), ev.ProviderPaymentID).Scan(&found).Error; err != nil {
			return fmt.Errorf("find payment: %w", err)
		}
		if len(found) == 0 {
			log.Warn().Str("provider_payment_id", ev.ProviderPaymentID).Msg("payment event for an unknown payment ignored")
			return nil
		}
		// Order first, then payment: the lock order every path uses.
		o, err := lockOrder(tx, found[0].OrderID)
		if err != nil {
			return err
		}
		var p paymentRecord
		err = tx.Raw(`SELECT `+paymentColumns+` FROM payment WHERE id = ? FOR UPDATE`, found[0].ID).Scan(&p).Error
		if err != nil {
			return fmt.Errorf("lock payment: %w", err)
		}
		if p.Status != paymentCreated {
			return nil
		}
		if ev.Outcome == payment.Failed {
			return tx.Exec(`UPDATE payment SET status = 'FAILED', updated_at = ? WHERE id = ?`, s.now(), p.ID).Error
		}
		refunds, err = s.paymentSucceeded(tx, o, &p, ev)
		return err
	})
	if err != nil {
		return err
	}
	s.refundNow(ctx, refunds)
	return nil
}

func (s *Service) paymentSucceeded(tx *gorm.DB, o *orderRow, p *paymentRecord, ev payment.Event) ([]uuid.UUID, error) {
	err := tx.Exec(`UPDATE payment SET status = 'SUCCEEDED', updated_at = ? WHERE id = ?`, s.now(), p.ID).Error
	if err != nil {
		return nil, fmt.Errorf("record payment success: %w", err)
	}
	refund := func(why string) ([]uuid.UUID, error) {
		log.Warn().Str("order_id", o.ID.String()).Str("payment_id", p.ID.String()).Msg("refunding payment: " + why)
		if o.PaymentStatus == PaymentUnpaid {
			if err := tx.Exec(`UPDATE orders SET payment_status = 'REFUND_PENDING', updated_at = ? WHERE id = ?`,
				s.now(), o.ID).Error; err != nil {
				return nil, err
			}
		}
		return s.queueRefunds(tx, o.ID, `id = ?`, p.ID)
	}
	if ev.AmountPaise != 0 && ev.AmountPaise != o.TotalPaise {
		return refund("amount differs from the order total")
	}
	paid := PaymentPaid
	deadline := s.now().Add(s.cfg.AcceptTimeout)
	placed := move{to: StatusPlaced, actor: ActorPayment, expiresAt: &deadline, payment: &paid}
	switch {
	case o.PaymentStatus != PaymentUnpaid:
		return refund("order already paid")
	case o.Status == StatusPendingPayment:
		return nil, s.transition(tx, o, placed, "pay for")
	case o.Status == StatusExpired:
		// Reserve again inside a savepoint: if the stock is gone, only the attempt rolls back.
		err := tx.Transaction(func(sp *gorm.DB) error {
			if err := checkStoreOpen(sp, o.StoreID); err != nil {
				return err
			}
			return s.reserveAll(sp, o, nil)
		})
		if err != nil {
			if !isStockProblem(err) {
				return nil, err
			}
			return refund("paid after the hold expired and the items or store are no longer available")
		}
		placed.reason = "Paid after the hold expired; the items were still available"
		return nil, s.transition(tx, o, placed, "pay for")
	default:
		return refund("order is " + string(o.Status))
	}
}

// isStockProblem reports whether err means the items can't be reserved right
// now, as opposed to a failure worth retrying.
func isStockProblem(err error) bool {
	var se *inventory.StockError
	return errors.As(err, &se) || errors.Is(err, inventory.ErrUnlisted) || errors.Is(err, inventory.ErrNotForSale) ||
		errors.Is(err, inventory.ErrNotFound) || errors.Is(err, ErrStoreUnavailable)
}

func rawOrEmpty(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}
