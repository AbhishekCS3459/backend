package orders

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Get returns one of the customer's orders. Other customers' orders are "not found".
func (s *Service) Get(ctx context.Context, customerID, orderID uuid.UUID) (*Order, error) {
	db := s.db.WithContext(ctx)
	if err := ownedBy(db, customerID, orderID); err != nil {
		return nil, err
	}
	return s.load(db, orderID, viewCustomer)
}

// List returns the customer's orders, newest first.
func (s *Service) List(ctx context.Context, customerID uuid.UUID, cursor string, limit int) (*Page, error) {
	return s.list(s.db.WithContext(ctx), `o.customer_id = ?`, []any{customerID}, cursor, limit, viewCustomer)
}

// Cancel ends the customer's order and releases its stock, refunding a paid
// order. It is allowed until the order is ready for pickup. Cancelling an
// already cancelled order changes nothing and returns it.
func (s *Service) Cancel(ctx context.Context, customerID, orderID uuid.UUID, req *CancelRequest) (*Order, error) {
	var refunds []uuid.UUID
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		o, err := lockOrder(tx, orderID)
		if err != nil {
			return err
		}
		if o.CustomerID != customerID {
			return ErrNotFound
		}
		if o.Status == StatusCancelled {
			return nil
		}
		reason := req.Reason
		if reason == "" {
			reason = "Cancelled by the customer"
		}
		m := move{to: StatusCancelled, actor: ActorCustomer, by: &customerID, reason: reason}
		refunds, err = s.closeOrder(tx, o, m, "cancel")
		return err
	})
	if err != nil {
		return nil, err
	}
	s.refundNow(ctx, refunds)
	return s.load(s.db.WithContext(ctx), orderID, viewCustomer)
}

func ownedBy(db *gorm.DB, customerID, orderID uuid.UUID) error {
	var n int64
	err := db.Raw(`SELECT COUNT(*) FROM orders WHERE id = ? AND customer_id = ?`, orderID, customerID).Scan(&n).Error
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
