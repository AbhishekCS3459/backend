package orders

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/payment"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	defaultPageSize = 20
	maxPageSize     = 50
)

// view decides what an order shows: the customer sees their pickup code, the
// store sees who the customer is.
type view int

const (
	viewCustomer view = iota
	viewStore
)

type orderRecord struct {
	ID            uuid.UUID
	Code          string
	CustomerID    uuid.UUID
	StoreID       uuid.UUID
	StoreName     string
	Status        Status
	PaymentMode   PaymentMode
	PaymentStatus PaymentStatus
	TotalPaise    int64
	ExpiresAt     *time.Time
	AcceptedAt    *time.Time
	ReadyAt       *time.Time
	CompletedAt   *time.Time
	ClosedReason  *string
	Version       int
	CreatedAt     time.Time
	UpdatedAt     time.Time
	CustomerName  string
	CustomerPhone string
}

const recordSelect = `
	SELECT o.id, o.code, o.customer_id, o.store_id, s.name AS store_name, o.status, o.payment_mode,
		o.payment_status, o.total_paise, o.expires_at, o.accepted_at, o.ready_at, o.completed_at,
		o.closed_reason, o.version, o.created_at, o.updated_at,
		COALESCE(NULLIF(u.full_name, ''), '') AS customer_name, u.phone AS customer_phone
	FROM orders o
	JOIN store s ON s.id = o.store_id
	JOIN users u ON u.id = o.customer_id`

// load returns one order in full, history included.
func (s *Service) load(db *gorm.DB, id uuid.UUID, v view) (*Order, error) {
	var recs []orderRecord
	if err := db.Raw(recordSelect+` WHERE o.id = ?`, id).Scan(&recs).Error; err != nil {
		return nil, fmt.Errorf("load order: %w", err)
	}
	if len(recs) == 0 {
		return nil, ErrNotFound
	}
	out, err := s.hydrate(db, recs, v)
	if err != nil {
		return nil, err
	}
	o := &out[0]
	var history []struct {
		FromStatus *Status
		ToStatus   Status
		Actor      Actor
		Reason     *string
		ChangedAt  time.Time
	}
	err = db.Raw(`SELECT from_status, to_status, actor, reason, changed_at FROM order_status_history
		WHERE order_id = ? ORDER BY seq`, id).Scan(&history).Error
	if err != nil {
		return nil, fmt.Errorf("load order history: %w", err)
	}
	o.History = make([]Event, len(history))
	for i, h := range history {
		o.History[i] = Event{From: h.FromStatus, To: h.ToStatus, Actor: h.Actor, Reason: h.Reason, At: h.ChangedAt}
	}
	return o, nil
}

// hydrate adds items, the latest payment and the per-view fields to records.
func (s *Service) hydrate(db *gorm.DB, recs []orderRecord, v view) ([]Order, error) {
	out := make([]Order, len(recs))
	if len(recs) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, len(recs))
	index := make(map[uuid.UUID]int, len(recs))
	for i, r := range recs {
		ids[i] = r.ID
		index[r.ID] = i
		out[i] = Order{
			ID: r.ID, Code: r.Code, StoreID: r.StoreID, StoreName: r.StoreName, Status: r.Status,
			PaymentMode: r.PaymentMode, PaymentStatus: r.PaymentStatus, TotalPaise: r.TotalPaise,
			ExpiresAt: r.ExpiresAt, AcceptedAt: r.AcceptedAt, ReadyAt: r.ReadyAt, CompletedAt: r.CompletedAt,
			ClosedReason: r.ClosedReason, Version: r.Version, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			Items: []Item{},
		}
		switch v {
		case viewCustomer:
			if r.Status == StatusPlaced || r.Status == StatusAccepted || r.Status == StatusReady {
				out[i].PickupCode = pickupCode(s.cfg.PickupSecret, r.ID)
			}
		case viewStore:
			out[i].Customer = &Customer{Name: r.CustomerName, Phone: r.CustomerPhone}
		}
	}

	var items []struct {
		OrderID uuid.UUID
		Item
	}
	err := db.Raw(`SELECT order_id, id, product_variant_id AS variant_id, catalog_key, name, unit, quantity,
			unit_price_paise, line_total_paise
		FROM order_item WHERE order_id IN ? ORDER BY name, id`, ids).Scan(&items).Error
	if err != nil {
		return nil, fmt.Errorf("load order items: %w", err)
	}
	for _, it := range items {
		o := &out[index[it.OrderID]]
		o.Items = append(o.Items, it.Item)
	}

	var payments []paymentRecord
	err = db.Raw(`SELECT DISTINCT ON (order_id) `+paymentColumns+` FROM payment
		WHERE order_id IN ? ORDER BY order_id, created_at DESC, id`, ids).Scan(&payments).Error
	if err != nil {
		return nil, fmt.Errorf("load payments: %w", err)
	}
	for i := range payments {
		p := s.paymentView(&payments[i])
		out[index[payments[i].OrderID]].Payment = &p
	}
	return out, nil
}

type paymentRecord struct {
	ID                uuid.UUID
	OrderID           uuid.UUID
	Provider          string
	ProviderPaymentID string
	Status            string
	AmountPaise       int64
	CreatedAt         time.Time
}

const paymentColumns = `id, order_id, provider, provider_payment_id, status, amount_paise, created_at`

func (s *Service) paymentView(p *paymentRecord) Payment {
	out := Payment{ID: p.ID, Provider: p.Provider, Status: p.Status, AmountPaise: p.AmountPaise, CreatedAt: p.CreatedAt}
	if p.Provider != s.provider.Name() {
		return out
	}
	_, out.Simulated = s.provider.(payment.Simulator)
	if p.Status == paymentCreated {
		out.Checkout = s.provider.Checkout(payment.CheckoutRequest{
			PaymentID: p.ID.String(), ProviderPaymentID: p.ProviderPaymentID,
			AmountPaise: p.AmountPaise, Currency: currencyINR,
		})
	}
	return out
}

// list returns a page of orders matching where, newest first.
func (s *Service) list(db *gorm.DB, where string, args []any, cursor string, limit int, v view) (*Page, error) {
	if limit <= 0 {
		limit = defaultPageSize
	}
	limit = min(limit, maxPageSize)
	query := recordSelect + ` WHERE ` + where
	if cursor != "" {
		at, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, err
		}
		query += ` AND (o.created_at, o.id) < (?, ?)`
		args = append(args, at, id)
	}
	query += ` ORDER BY o.created_at DESC, o.id DESC LIMIT ?`
	args = append(args, limit+1)
	var recs []orderRecord
	if err := db.Raw(query, args...).Scan(&recs).Error; err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	page := &Page{}
	if len(recs) > limit {
		last := recs[limit-1]
		page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
		recs = recs[:limit]
	}
	orders, err := s.hydrate(db, recs, v)
	if err != nil {
		return nil, err
	}
	page.Orders = orders
	return page, nil
}

func encodeCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at.UnixMicro(), 10) + "_" + id.String()))
}

func decodeCursor(cursor string) (time.Time, uuid.UUID, error) {
	invalid := &ValidationError{Message: "invalid cursor"}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, uuid.Nil, invalid
	}
	micros, idText, ok := strings.Cut(string(raw), "_")
	if !ok {
		return time.Time{}, uuid.Nil, invalid
	}
	n, err := strconv.ParseInt(micros, 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, invalid
	}
	id, err := uuid.Parse(idText)
	if err != nil {
		return time.Time{}, uuid.Nil, invalid
	}
	return time.UnixMicro(n).UTC(), id, nil
}
