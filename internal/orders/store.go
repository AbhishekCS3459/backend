package orders

import (
	"context"
	"strings"

	"github.com/AbhishekCS3459/find-me-backend/internal/inventory"
	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// CanWatch returns nil if the user may follow the store's order changes.
func (s *Service) CanWatch(ctx context.Context, userID, storeID uuid.UUID) error {
	_, err := s.access.Require(ctx, userID, storeID, storeaccess.OrdersView)
	return err
}

// StoreList returns the store's orders, newest first, optionally only those in statuses.
func (s *Service) StoreList(
	ctx context.Context, userID, storeID uuid.UUID, statuses []Status, cursor string, limit int,
) (*Page, error) {
	if _, err := s.access.Require(ctx, userID, storeID, storeaccess.OrdersView); err != nil {
		return nil, err
	}
	where, args := `o.store_id = ?`, []any{storeID}
	if len(statuses) > 0 {
		for _, st := range statuses {
			if !st.Open() && !isTerminal(st) {
				return nil, &ValidationError{Message: "unknown status " + string(st)}
			}
		}
		where += ` AND o.status IN ?`
		args = append(args, statuses)
	}
	return s.list(s.db.WithContext(ctx), where, args, cursor, limit, viewStore)
}

func isTerminal(st Status) bool {
	switch st {
	case StatusCompleted, StatusCancelled, StatusRejected, StatusExpired, StatusNoShow:
		return true
	}
	return false
}

func (s *Service) StoreGet(ctx context.Context, userID, storeID, orderID uuid.UUID) (*Order, error) {
	if _, err := s.access.Require(ctx, userID, storeID, storeaccess.OrdersView); err != nil {
		return nil, err
	}
	db := s.db.WithContext(ctx)
	if err := inStore(db, storeID, orderID); err != nil {
		return nil, err
	}
	return s.load(db, orderID, viewStore)
}

// Accept commits the store to preparing a placed order.
func (s *Service) Accept(ctx context.Context, userID, storeID, orderID uuid.UUID) (*Order, error) {
	return s.storeChange(ctx, userID, storeID, orderID, storeaccess.OrdersAccept,
		func(tx *gorm.DB, o *orderRow) ([]uuid.UUID, error) {
			return nil, s.transition(tx, o, move{to: StatusAccepted, actor: ActorRetailer, by: &userID}, "accept")
		})
}

// Reject turns an order down before it is ready, releasing its stock and refunding a paid order.
func (s *Service) Reject(ctx context.Context, userID, storeID, orderID uuid.UUID, req *RejectRequest) (*Order, error) {
	return s.storeChange(ctx, userID, storeID, orderID, storeaccess.OrdersAccept,
		func(tx *gorm.DB, o *orderRow) ([]uuid.UUID, error) {
			return s.closeOrder(tx, o, move{
				to: StatusRejected, actor: ActorRetailer, by: &userID, reason: strings.TrimSpace(req.Reason),
			}, "reject")
		})
}

// Ready tells the customer to come; the pickup window starts now.
func (s *Service) Ready(ctx context.Context, userID, storeID, orderID uuid.UUID) (*Order, error) {
	return s.storeChange(ctx, userID, storeID, orderID, storeaccess.OrdersFulfil,
		func(tx *gorm.DB, o *orderRow) ([]uuid.UUID, error) {
			deadline := s.now().Add(s.cfg.PickupWindow)
			return nil, s.transition(tx, o, move{
				to: StatusReady, actor: ActorRetailer, by: &userID, expiresAt: &deadline,
			}, "mark ready")
		})
}

// Complete hands a ready order to the customer: its reserved units leave
// stock as a sale at the order's prices. The customer's pickup code proves
// who is collecting; without it the store gives a reason, kept in the history.
func (s *Service) Complete(
	ctx context.Context, userID, storeID, orderID uuid.UUID, req *CompleteRequest,
) (*Order, error) {
	code, override := req.PickupCode, strings.TrimSpace(req.OverrideReason)
	if (code == "") == (override == "") {
		return nil, &ValidationError{Message: "give either the customer's pickup_code or an override_reason"}
	}
	var wrongCode *PickupCodeError
	order, err := s.storeChange(ctx, userID, storeID, orderID, storeaccess.OrdersFulfil,
		func(tx *gorm.DB, o *orderRow) ([]uuid.UUID, error) {
			if o.Status != StatusReady {
				return nil, &StateError{Status: o.Status, Action: "hand over"}
			}
			reason := ""
			if code != "" {
				if o.PickupAttempts >= s.cfg.MaxPickupAttempts {
					return nil, ErrPickupLocked
				}
				if !pickupCodeMatches(s.cfg.PickupSecret, o.ID, code) {
					// Counted even though nothing else changes, so guessing is limited.
					if err := tx.Exec(`UPDATE orders SET pickup_attempts = pickup_attempts + 1 WHERE id = ?`, o.ID).Error; err != nil {
						return nil, err
					}
					wrongCode = &PickupCodeError{AttemptsLeft: s.cfg.MaxPickupAttempts - o.PickupAttempts - 1}
					return nil, nil
				}
			} else {
				reason = "Handed over without the pickup code: " + override
			}
			if err := s.handOver(tx, o, &userID); err != nil {
				return nil, err
			}
			m := move{to: StatusCompleted, actor: ActorRetailer, by: &userID, reason: reason}
			if o.PaymentMode == PaymentPayAtStore {
				paid := PaymentPaid
				m.payment = &paid
			}
			return nil, s.transition(tx, o, m, "hand over")
		})
	if err != nil {
		return nil, err
	}
	if wrongCode != nil {
		return nil, wrongCode
	}
	return order, nil
}

// handOver turns every reservation of o into a sale.
func (s *Service) handOver(tx *gorm.DB, o *orderRow, by *uuid.UUID) error {
	held, err := lockReservations(tx, o.ID)
	if err != nil {
		return err
	}
	for _, r := range held {
		closed, err := s.closeReservation(tx, r.ID, "CONSUMED")
		if err != nil {
			return err
		}
		if !closed {
			continue
		}
		price := float64(r.UnitPricePaise) / 100
		if _, err := s.ledger.Pickup(tx, inventory.OrderInput{
			StoreID: o.StoreID, VariantID: r.VariantID, OrderID: o.ID, Quantity: r.Quantity,
			Reference: o.Code, UnitPrice: &price, Actor: ledgerActor(by),
		}); err != nil {
			return err
		}
	}
	return nil
}

// storeChange runs change on a locked order of the store, after checking the
// user's permission, then refunds what it queued and returns the store's view.
func (s *Service) storeChange(
	ctx context.Context, userID, storeID, orderID uuid.UUID, perm storeaccess.Permission,
	change func(tx *gorm.DB, o *orderRow) ([]uuid.UUID, error),
) (*Order, error) {
	if _, err := s.access.Require(ctx, userID, storeID, perm); err != nil {
		return nil, err
	}
	var refunds []uuid.UUID
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		o, err := lockOrder(tx, orderID)
		if err != nil {
			return err
		}
		if o.StoreID != storeID {
			return ErrNotFound
		}
		refunds, err = change(tx, o)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.refundNow(ctx, refunds)
	return s.load(s.db.WithContext(ctx), orderID, viewStore)
}

func inStore(db *gorm.DB, storeID, orderID uuid.UUID) error {
	var n int64
	err := db.Raw(`SELECT COUNT(*) FROM orders WHERE id = ? AND store_id = ?`, orderID, storeID).Scan(&n).Error
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
