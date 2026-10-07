package orders

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

const (
	sweepBatch        = 100
	reconcileInterval = 10 * time.Minute
)

// ExpireDue closes orders whose deadline passed, up to limit, one transaction
// each, and returns how many it closed. Several instances can run it at once:
// each order is claimed with SKIP LOCKED, and the status guard means a
// payment or cancel committing first simply wins.
//
//   - PENDING_PAYMENT: not paid in time, EXPIRED.
//   - PLACED: the store didn't respond, REJECTED (refunded if paid).
//   - READY: not collected, NO_SHOW (refunded if paid).
func (s *Service) ExpireDue(ctx context.Context, limit int) (int, error) {
	for n := range limit {
		closed, err := s.expireOne(ctx)
		if err != nil || !closed {
			return n, err
		}
	}
	return limit, nil
}

func (s *Service) expireOne(ctx context.Context) (bool, error) {
	var refunds []uuid.UUID
	closed := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var due []orderRow
		err := tx.Raw(`SELECT `+orderRowColumns+` FROM orders
			WHERE expires_at <= ? ORDER BY expires_at LIMIT 1 FOR UPDATE SKIP LOCKED`, s.now()).Scan(&due).Error
		if err != nil {
			return fmt.Errorf("claim due order: %w", err)
		}
		if len(due) == 0 {
			return nil
		}
		o := &due[0]
		m := move{actor: ActorSystem}
		switch o.Status {
		case StatusPendingPayment:
			m.to, m.reason = StatusExpired, "Not paid in time"
		case StatusPlaced:
			m.to, m.reason = StatusRejected, "The store didn't respond in time"
		case StatusReady:
			m.to, m.reason = StatusNoShow, "Not collected in time"
		default:
			return fmt.Errorf("order %s is %s but has a deadline", o.ID, o.Status)
		}
		refunds, err = s.closeOrder(tx, o, m, "expire")
		closed = err == nil
		return err
	})
	if err != nil {
		return false, err
	}
	s.refundNow(ctx, refunds)
	return closed, nil
}

// RunSweeper closes overdue orders and retries refunds every interval, and
// checks the books every few minutes, until ctx ends.
func (s *Service) RunSweeper(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastCheck := time.Now()
	for {
		if n, err := s.ExpireDue(ctx, sweepBatch); err != nil && ctx.Err() == nil {
			log.Error().Err(err).Msg("order sweeper: closing overdue orders failed")
		} else if n > 0 {
			log.Info().Int("orders", n).Msg("order sweeper: closed overdue orders")
		}
		if _, err := s.RefundDue(ctx, sweepBatch); err != nil && ctx.Err() == nil {
			log.Error().Err(err).Msg("order sweeper: refunds failed")
		}
		if time.Since(lastCheck) >= reconcileInterval {
			lastCheck = time.Now()
			if report, err := s.Reconcile(ctx); err != nil && ctx.Err() == nil {
				log.Error().Err(err).Msg("order reconciliation failed")
			} else if err == nil && !report.Clean() {
				log.Error().Interface("report", report).Msg("order reconciliation found problems")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
