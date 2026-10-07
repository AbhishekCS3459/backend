package orders

import (
	"fmt"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/inventory"
	"github.com/AbhishekCS3459/find-me-backend/internal/payment"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/outbox"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/realtime"
	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ChangesChannel is the PostgreSQL NOTIFY channel for order changes; the
// payload is the store ID.
const ChangesChannel = "orders_changed"

const (
	AggregateType          = "order"
	EventStatusChanged     = "OrderStatusChanged"
	statusChangedSchemaVer = 1
)

type Config struct {
	// PaymentHold is how long an online order holds its stock for payment.
	PaymentHold time.Duration
	// AcceptTimeout is how long the store has to accept a placed order before
	// it is rejected and its stock released.
	AcceptTimeout time.Duration
	// PickupWindow is how long a ready order waits for the customer.
	PickupWindow time.Duration
	// PickupSecret derives pickup codes; changing it changes every open order's code.
	PickupSecret []byte
	// MaxOpenOrders caps one customer's orders holding stock, so nobody can
	// tie up a small shop's last units.
	MaxOpenOrders int
	// MaxPickupAttempts wrong pickup codes lock the code for that order.
	MaxPickupAttempts int
}

func (c Config) withDefaults() Config {
	if c.PaymentHold <= 0 {
		c.PaymentHold = 10 * time.Minute
	}
	if c.AcceptTimeout <= 0 {
		c.AcceptTimeout = 15 * time.Minute
	}
	if c.PickupWindow <= 0 {
		c.PickupWindow = 2 * time.Hour
	}
	if c.MaxOpenOrders <= 0 {
		c.MaxOpenOrders = 5
	}
	if c.MaxPickupAttempts <= 0 {
		c.MaxPickupAttempts = 5
	}
	return c
}

type Service struct {
	db       *gorm.DB
	ledger   *inventory.Ledger
	access   storeaccess.Resolver
	provider payment.Provider
	cfg      Config
	now      func() time.Time
}

func NewService(db *gorm.DB, ledger *inventory.Ledger, access storeaccess.Resolver,
	provider payment.Provider, cfg Config) *Service {
	if len(cfg.PickupSecret) == 0 {
		panic("orders: PickupSecret is required")
	}
	return &Service{db: db, ledger: ledger, access: access, provider: provider, cfg: cfg.withDefaults(),
		now: func() time.Time { return time.Now().UTC() }}
}

// orderRow is an order's state as the write paths need it.
type orderRow struct {
	ID             uuid.UUID
	Code           string
	CustomerID     uuid.UUID
	StoreID        uuid.UUID
	Status         Status
	PaymentMode    PaymentMode
	PaymentStatus  PaymentStatus
	TotalPaise     int64
	ExpiresAt      *time.Time
	PickupAttempts int
	Version        int
}

const orderRowColumns = `id, code, customer_id, store_id, status, payment_mode, payment_status,
	total_paise, expires_at, pickup_attempts, version`

// lockOrder loads an order FOR UPDATE. Every change to an order takes this
// lock first, so changes to one order happen one at a time.
func lockOrder(tx *gorm.DB, id uuid.UUID) (*orderRow, error) {
	var rows []orderRow
	if err := tx.Raw(`SELECT `+orderRowColumns+` FROM orders WHERE id = ? FOR UPDATE`, id).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("lock order: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

// move is one status change and what goes with it.
type move struct {
	to     Status
	actor  Actor
	by     *uuid.UUID
	reason string
	// expiresAt is the new status's deadline; nil for statuses without one.
	expiresAt *time.Time
	// payment is the new payment status, when it changes.
	payment *PaymentStatus
}

// transition moves o to m.to if that is allowed from its current status. The
// update is also conditional on the status and version it read, so it can
// never overwrite a change it didn't see.
func (s *Service) transition(tx *gorm.DB, o *orderRow, m move, action string) error {
	if !canMove(o.Status, m.to) {
		return &StateError{Status: o.Status, Action: action}
	}
	now := s.now()
	pay := o.PaymentStatus
	if m.payment != nil {
		pay = *m.payment
	}
	var acceptedAt, readyAt, completedAt *time.Time
	switch m.to {
	case StatusAccepted:
		acceptedAt = &now
	case StatusReady:
		readyAt = &now
	case StatusCompleted:
		completedAt = &now
	}
	var closedReason *string
	if !m.to.Open() && m.reason != "" {
		closedReason = &m.reason
	}
	res := tx.Exec(`
		UPDATE orders SET status = ?, payment_status = ?, expires_at = ?,
			accepted_at = COALESCE(?::timestamptz, accepted_at),
			ready_at = COALESCE(?::timestamptz, ready_at),
			completed_at = COALESCE(?::timestamptz, completed_at),
			closed_reason = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND status = ? AND version = ?`,
		m.to, pay, m.expiresAt, acceptedAt, readyAt, completedAt, closedReason, now,
		o.ID, o.Status, o.Version)
	if res.Error != nil {
		return fmt.Errorf("update order status: %w", res.Error)
	}
	if res.RowsAffected != 1 {
		return fmt.Errorf("order %s changed concurrently", o.ID)
	}
	from := o.Status
	o.Status, o.PaymentStatus, o.ExpiresAt, o.Version = m.to, pay, m.expiresAt, o.Version+1
	return s.recordStatus(tx, o, &from, m)
}

type statusChanged struct {
	SchemaVersion int        `json:"schema_version"`
	OrderID       uuid.UUID  `json:"order_id"`
	Code          string     `json:"code"`
	StoreID       uuid.UUID  `json:"store_id"`
	CustomerID    uuid.UUID  `json:"customer_id"`
	From          *Status    `json:"from"`
	To            Status     `json:"to"`
	PaymentStatus string     `json:"payment_status"`
	Actor         Actor      `json:"actor"`
	Version       int        `json:"version"`
	OccurredAt    time.Time  `json:"occurred_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
}

// recordStatus writes the history entry, the outbox event and the store's
// live-screen signal for a status o just took, all inside tx.
func (s *Service) recordStatus(tx *gorm.DB, o *orderRow, from *Status, m move) error {
	now := s.now()
	var reason *string
	if m.reason != "" {
		reason = &m.reason
	}
	if err := tx.Exec(`
		INSERT INTO order_status_history (order_id, from_status, to_status, actor, changed_by, reason, changed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, o.ID, from, m.to, m.actor, m.by, reason, now).Error; err != nil {
		return fmt.Errorf("record order history: %w", err)
	}
	err := outbox.Enqueue(tx, outbox.Event{
		AggregateType: AggregateType, AggregateID: o.ID, Type: EventStatusChanged,
		Payload: statusChanged{
			SchemaVersion: statusChangedSchemaVer, OrderID: o.ID, Code: o.Code, StoreID: o.StoreID,
			CustomerID: o.CustomerID, From: from, To: m.to, PaymentStatus: string(o.PaymentStatus),
			Actor: m.actor, Version: o.Version, OccurredAt: now, ExpiresAt: o.ExpiresAt,
		},
	})
	if err != nil {
		return err
	}
	return realtime.Notify(tx, ChangesChannel, o.StoreID.String())
}

func ledgerActor(by *uuid.UUID) inventory.Actor {
	if by == nil {
		return inventory.Actor{}
	}
	return inventory.Actor{ID: *by}
}

type reservedLine struct {
	ItemID         uuid.UUID
	VariantID      uuid.UUID
	Quantity       int
	UnitPricePaise int64
}

// reserveAll holds every line of o through the ledger, in (variant, store)
// order like every other multi-row stock change, and records the reservations.
// Any line failing fails the whole call; the caller's transaction rolls back.
func (s *Service) reserveAll(tx *gorm.DB, o *orderRow, by *uuid.UUID) error {
	var lines []reservedLine
	err := tx.Raw(`SELECT id AS item_id, product_variant_id AS variant_id, quantity, unit_price_paise
		FROM order_item WHERE order_id = ? ORDER BY product_variant_id`, o.ID).Scan(&lines).Error
	if err != nil {
		return fmt.Errorf("load order items: %w", err)
	}
	for _, l := range lines {
		res, err := s.ledger.Reserve(tx, inventory.OrderInput{
			StoreID: o.StoreID, VariantID: l.VariantID, OrderID: o.ID, Quantity: l.Quantity,
			Reference: o.Code, Actor: ledgerActor(by),
		})
		if err != nil {
			return err
		}
		err = tx.Exec(`
			INSERT INTO inventory_reservation
				(order_id, order_item_id, inventory_id, store_id, product_variant_id, quantity, status, created_at)
			VALUES (?, ?, ?, ?, ?, ?, 'ACTIVE', ?)`,
			o.ID, l.ItemID, res.Inventory.InventoryID, o.StoreID, l.VariantID, l.Quantity, s.now()).Error
		if err != nil {
			return fmt.Errorf("record reservation: %w", err)
		}
	}
	return nil
}

type activeReservation struct {
	ID             uuid.UUID
	VariantID      uuid.UUID
	Quantity       int
	UnitPricePaise int64
}

// lockReservations locks o's active reservations in (variant, store) order.
func lockReservations(tx *gorm.DB, orderID uuid.UUID) ([]activeReservation, error) {
	var rows []activeReservation
	err := tx.Raw(`
		SELECT r.id, r.product_variant_id AS variant_id, r.quantity, i.unit_price_paise
		FROM inventory_reservation r
		JOIN order_item i ON i.id = r.order_item_id
		WHERE r.order_id = ? AND r.status = 'ACTIVE'
		ORDER BY r.product_variant_id, r.store_id
		FOR UPDATE OF r`, orderID).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("lock reservations: %w", err)
	}
	return rows, nil
}

// closeReservation ends one reservation; it reports false if it was already closed.
func (s *Service) closeReservation(tx *gorm.DB, id uuid.UUID, status string) (bool, error) {
	res := tx.Exec(`UPDATE inventory_reservation SET status = ?, closed_at = ? WHERE id = ? AND status = 'ACTIVE'`,
		status, s.now(), id)
	if res.Error != nil {
		return false, fmt.Errorf("close reservation: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// releaseAll gives back every unit o still holds.
func (s *Service) releaseAll(tx *gorm.DB, o *orderRow, by *uuid.UUID) error {
	held, err := lockReservations(tx, o.ID)
	if err != nil {
		return err
	}
	for _, r := range held {
		closed, err := s.closeReservation(tx, r.ID, "RELEASED")
		if err != nil {
			return err
		}
		if !closed {
			continue
		}
		if _, err := s.ledger.Release(tx, inventory.OrderInput{
			StoreID: o.StoreID, VariantID: r.VariantID, OrderID: o.ID, Quantity: r.Quantity,
			Reference: o.Code, Actor: ledgerActor(by),
		}); err != nil {
			return err
		}
	}
	return nil
}

// closeOrder ends o with a terminal status: its stock is released and, if it
// was paid, the payment is queued for a refund. It returns the payments to refund.
func (s *Service) closeOrder(tx *gorm.DB, o *orderRow, m move, action string) ([]uuid.UUID, error) {
	paid := o.PaymentStatus == PaymentPaid
	if paid {
		pending := PaymentRefundPending
		m.payment = &pending
	}
	if err := s.transition(tx, o, m, action); err != nil {
		return nil, err
	}
	if err := s.releaseAll(tx, o, m.by); err != nil {
		return nil, err
	}
	if !paid {
		return nil, nil
	}
	return s.queueRefunds(tx, o.ID, `status = 'SUCCEEDED'`)
}

// queueRefunds marks the order's payments matching where for refund.
func (s *Service) queueRefunds(tx *gorm.DB, orderID uuid.UUID, where string, args ...any) ([]uuid.UUID, error) {
	var rows []struct{ ID uuid.UUID }
	now := s.now()
	err := tx.Raw(`UPDATE payment SET status = 'REFUND_PENDING', next_refund_at = ?, updated_at = ?
		WHERE order_id = ? AND `+where+` RETURNING id`, append([]any{now, now, orderID}, args...)...).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("queue refund: %w", err)
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids, nil
}
