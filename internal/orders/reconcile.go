package orders

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	// overdueGrace is how late the sweeper may be before an order counts as stuck.
	overdueGrace = 5 * time.Minute
	// acceptedTooLong flags orders the store accepted but never marked ready.
	acceptedTooLong = 24 * time.Hour
	maxReported     = 50
)

// ReservedDrift is an inventory row whose reserved units don't match its
// active reservations.
type ReservedDrift struct {
	InventoryID uuid.UUID `json:"inventory_id"`
	Reserved    int       `json:"reserved"`
	Held        int       `json:"held"`
}

// Report is what Reconcile found. Each list holds at most the first 50.
type Report struct {
	ReservedDrift []ReservedDrift `json:"reserved_drift"`
	// Overdue orders are past their deadline but the sweeper hasn't closed them.
	Overdue []uuid.UUID `json:"overdue"`
	// StaleAccepted orders were accepted over a day ago and still hold stock.
	StaleAccepted []uuid.UUID `json:"stale_accepted"`
	// ParkedRefunds stopped retrying and need a manual refund.
	ParkedRefunds []uuid.UUID `json:"parked_refunds"`
	// UnappliedPayments succeeded but left their order unpaid.
	UnappliedPayments []uuid.UUID `json:"unapplied_payments"`
}

func (r Report) Clean() bool {
	return len(r.ReservedDrift) == 0 && len(r.Overdue) == 0 && len(r.StaleAccepted) == 0 &&
		len(r.ParkedRefunds) == 0 && len(r.UnappliedPayments) == 0
}

func (s *Service) Reconcile(ctx context.Context) (Report, error) {
	return Reconcile(ctx, s.db, s.now())
}

// Reconcile checks orders against stock and payments as of now. It only reads.
func Reconcile(ctx context.Context, db *gorm.DB, now time.Time) (Report, error) {
	db = db.WithContext(ctx)
	var r Report
	err := db.Raw(`
		SELECT i.id AS inventory_id, i.reserved_quantity AS reserved, COALESCE(h.held, 0) AS held
		FROM inventory i
		LEFT JOIN (
			SELECT inventory_id, SUM(quantity)::int AS held FROM inventory_reservation
			WHERE status = 'ACTIVE' GROUP BY inventory_id
		) h ON h.inventory_id = i.id
		WHERE i.reserved_quantity <> COALESCE(h.held, 0)
		ORDER BY i.id
		LIMIT ?`, maxReported).Scan(&r.ReservedDrift).Error
	if err != nil {
		return r, fmt.Errorf("check reserved stock: %w", err)
	}
	checks := []struct {
		dst   *[]uuid.UUID
		query string
		args  []any
	}{
		{&r.Overdue, `SELECT id FROM orders WHERE expires_at < ? ORDER BY expires_at LIMIT ?`,
			[]any{now.Add(-overdueGrace), maxReported}},
		{&r.StaleAccepted, `SELECT id FROM orders WHERE status = 'ACCEPTED' AND accepted_at < ? ORDER BY accepted_at LIMIT ?`,
			[]any{now.Add(-acceptedTooLong), maxReported}},
		{&r.ParkedRefunds, `SELECT id FROM payment WHERE status = 'REFUND_PENDING' AND next_refund_at IS NULL
			ORDER BY updated_at LIMIT ?`, []any{maxReported}},
		{&r.UnappliedPayments, `SELECT p.id FROM payment p JOIN orders o ON o.id = p.order_id
			WHERE p.status = 'SUCCEEDED' AND o.payment_status = 'UNPAID' ORDER BY p.updated_at LIMIT ?`, []any{maxReported}},
	}
	for _, c := range checks {
		var rows []struct{ ID uuid.UUID }
		if err := db.Raw(c.query, c.args...).Scan(&rows).Error; err != nil {
			return r, fmt.Errorf("reconcile orders: %w", err)
		}
		for _, row := range rows {
			*c.dst = append(*c.dst, row.ID)
		}
	}
	return r, nil
}
