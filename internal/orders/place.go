package orders

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/availability"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const maxCodeAttempts = 5

// Place creates an order and reserves its stock in one transaction: either
// every line is held, or nothing is and the error lists each problem line.
// The bool reports whether the response is a replay of an earlier request
// with the same Idempotency-Key.
func (s *Service) Place(
	ctx context.Context, customerID uuid.UUID, key string, req *PlaceRequest,
) (*Order, bool, error) {
	seen := make(map[string]bool, len(req.Items))
	for i := range req.Items {
		req.Items[i].CatalogKey = strings.TrimSpace(req.Items[i].CatalogKey)
		if seen[req.Items[i].CatalogKey] {
			return nil, false, &ValidationError{Message: "each product can appear only once"}
		}
		seen[req.Items[i].CatalogKey] = true
		if req.Items[i].Quantity > availability.MaxOrderQuantity {
			return nil, false, &ValidationError{
				Message: fmt.Sprintf("quantity must be at most %d", availability.MaxOrderQuantity),
			}
		}
	}
	scope, err := newScope(customerID, key, req)
	if err != nil {
		return nil, false, err
	}
	return runIdempotent(ctx, s.db, scope, func(tx *gorm.DB) (*Order, error) {
		id, err := s.place(tx, customerID, req)
		if err != nil {
			return nil, err
		}
		return s.load(tx, id, viewCustomer)
	})
}

type orderLine struct {
	InventoryID uuid.UUID
	VariantID   uuid.UUID
	CatalogKey  string
	Name        string
	Unit        string
	Quantity    int
	PricePaise  int64
}

func (s *Service) place(tx *gorm.DB, customerID uuid.UUID, req *PlaceRequest) (uuid.UUID, error) {
	// One customer's orders are placed one at a time, so the open-order cap holds under concurrent requests.
	if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`,
		"orders:customer:"+customerID.String()).Error; err != nil {
		return uuid.Nil, fmt.Errorf("lock customer orders: %w", err)
	}
	var open int64
	if err := tx.Raw(`SELECT COUNT(*) FROM orders WHERE customer_id = ? AND status IN (`+openStatuses+`)`,
		customerID).Scan(&open).Error; err != nil {
		return uuid.Nil, fmt.Errorf("count open orders: %w", err)
	}
	if open >= int64(s.cfg.MaxOpenOrders) {
		return uuid.Nil, ErrTooManyOpen
	}
	if err := checkStoreOpen(tx, req.StoreID); err != nil {
		return uuid.Nil, err
	}
	lines, err := s.lockLines(tx, req)
	if err != nil {
		return uuid.Nil, err
	}

	now := s.now()
	o := &orderRow{
		ID: uuid.New(), CustomerID: customerID, StoreID: req.StoreID,
		PaymentMode: req.PaymentMode, PaymentStatus: PaymentUnpaid,
	}
	var expires time.Time
	if req.PaymentMode == PaymentOnline {
		o.Status, expires = StatusPendingPayment, now.Add(s.cfg.PaymentHold)
	} else {
		o.Status, expires = StatusPlaced, now.Add(s.cfg.AcceptTimeout)
	}
	o.ExpiresAt = &expires
	for _, l := range lines {
		o.TotalPaise += l.PricePaise * int64(l.Quantity)
	}
	if err := insertOrder(tx, o, now); err != nil {
		return uuid.Nil, err
	}
	for _, l := range lines {
		err := tx.Exec(`
			INSERT INTO order_item (order_id, product_variant_id, inventory_id, catalog_key, name, unit,
				quantity, unit_price_paise, line_total_paise, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			o.ID, l.VariantID, l.InventoryID, l.CatalogKey, l.Name, l.Unit,
			l.Quantity, l.PricePaise, l.PricePaise*int64(l.Quantity), now).Error
		if err != nil {
			return uuid.Nil, fmt.Errorf("insert order item: %w", err)
		}
	}
	if err := s.reserveAll(tx, o, &customerID); err != nil {
		return uuid.Nil, err
	}
	err = s.recordStatus(tx, o, nil, move{to: o.Status, actor: ActorCustomer, by: &customerID, expiresAt: o.ExpiresAt})
	return o.ID, err
}

// checkStoreOpen fails unless customers can order from the store right now.
// FOR SHARE makes a concurrent close wait for this order to commit.
func checkStoreOpen(tx *gorm.DB, storeID uuid.UUID) error {
	var stores []struct {
		Status           string
		IsOpen           bool
		OnboardingStatus string
		Deleted          bool
	}
	err := tx.Raw(`SELECT status, is_open, onboarding_status, deleted_at IS NOT NULL AS deleted
		FROM store WHERE id = ? FOR SHARE`, storeID).Scan(&stores).Error
	if err != nil {
		return fmt.Errorf("load store: %w", err)
	}
	if len(stores) == 0 {
		return ErrStoreUnavailable
	}
	st := stores[0]
	if !availability.StoreSearchable(st.Status, st.IsOpen, st.OnboardingStatus, st.Deleted) {
		return ErrStoreUnavailable
	}
	return nil
}

// lockLines finds each requested product in the store and locks its stock
// row, in (variant, store) order. It checks every line against the locked
// state and returns all problems at once, so the customer can fix the basket
// in one go. The ledger checks stock again when reserving.
func (s *Service) lockLines(tx *gorm.DB, req *PlaceRequest) ([]orderLine, error) {
	keys := make([]string, len(req.Items))
	for i, it := range req.Items {
		keys[i] = it.CatalogKey
	}
	var found []struct {
		InventoryID uuid.UUID
		VariantID   uuid.UUID
		CatalogKey  string
		Name        string
		Unit        string
		PricePaise  int64
		Listed      bool
		IsAvailable bool
		Available   int
	}
	err := tx.Raw(`
		SELECT i.id AS inventory_id, i.product_variant_id AS variant_id,
			COALESCE(v.catalog_key, 'variant:' || v.id::text) AS catalog_key,
			COALESCE(NULLIF(c.name, ''), p.name) AS name,
			COALESCE(NULLIF(c.unit, ''), v.variant_label, '') AS unit,
			ROUND(i.price * 100)::bigint AS price_paise,
			i.unlisted_at IS NULL AS listed, i.is_available,
			i.on_hand_quantity - i.reserved_quantity AS available
		FROM inventory i
		JOIN product_variant v ON v.id = i.product_variant_id
		JOIN product p ON p.id = v.product_id
		LEFT JOIN catalog_item c ON c.catalog_key = COALESCE(v.catalog_key, 'variant:' || v.id::text)
		WHERE i.store_id = ? AND COALESCE(v.catalog_key, 'variant:' || v.id::text) IN ?
		ORDER BY i.product_variant_id, i.store_id
		FOR UPDATE OF i`, req.StoreID, keys).Scan(&found).Error
	if err != nil {
		return nil, fmt.Errorf("lock order lines: %w", err)
	}
	byKey := make(map[string]int, len(found))
	for i, f := range found {
		byKey[f.CatalogKey] = i
	}

	var unavailable, repriced []ItemProblem
	lines := make([]orderLine, 0, len(req.Items))
	for _, it := range req.Items {
		i, ok := byKey[it.CatalogKey]
		if !ok || !found[i].Listed {
			unavailable = append(unavailable, ItemProblem{CatalogKey: it.CatalogKey, Reason: ProblemNotSoldHere})
			continue
		}
		f := found[i]
		switch {
		case !f.IsAvailable:
			unavailable = append(unavailable, ItemProblem{CatalogKey: it.CatalogKey, Reason: ProblemUnavailable})
			continue
		case f.Available < it.Quantity:
			orderable := availability.OrderableQuantity(f.Available)
			unavailable = append(unavailable, ItemProblem{
				CatalogKey: it.CatalogKey, Reason: ProblemOutOfStock, MaxOrderQuantity: &orderable,
			})
			continue
		}
		if it.ExpectedUnitPrice != nil && toPaise(*it.ExpectedUnitPrice) != f.PricePaise {
			current := float64(f.PricePaise) / 100
			repriced = append(repriced, ItemProblem{
				CatalogKey: it.CatalogKey, Reason: ProblemPriceChanged, CurrentUnitPrice: &current,
			})
		}
		lines = append(lines, orderLine{
			InventoryID: f.InventoryID, VariantID: f.VariantID, CatalogKey: f.CatalogKey,
			Name: f.Name, Unit: f.Unit, Quantity: it.Quantity, PricePaise: f.PricePaise,
		})
	}
	switch {
	case len(unavailable) > 0:
		return nil, &ItemsError{
			Code:    "ITEMS_UNAVAILABLE",
			Message: "Some items can't be ordered from this store right now.",
			Items:   append(unavailable, repriced...),
		}
	case len(repriced) > 0:
		return nil, &ItemsError{
			Code:    ProblemPriceChanged,
			Message: "Some prices changed. Check the new prices and place the order again.",
			Items:   repriced,
		}
	}
	return lines, nil
}

func toPaise(rupees float64) int64 { return int64(math.Round(rupees * 100)) }

func insertOrder(tx *gorm.DB, o *orderRow, now time.Time) error {
	for range maxCodeAttempts {
		o.Code = newOrderCode()
		res := tx.Exec(`
			INSERT INTO orders (id, customer_id, store_id, status, code, payment_mode, payment_status,
				total_paise, expires_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (code) DO NOTHING`,
			o.ID, o.CustomerID, o.StoreID, o.Status, o.Code, o.PaymentMode, o.PaymentStatus,
			o.TotalPaise, o.ExpiresAt, now, now)
		if res.Error != nil {
			return fmt.Errorf("insert order: %w", res.Error)
		}
		if res.RowsAffected == 1 {
			o.Version = 1
			return nil
		}
	}
	return fmt.Errorf("insert order: no free order code after %d attempts", maxCodeAttempts)
}
