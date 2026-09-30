package inventory

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/availability"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/realtime"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// Ledger applies inventory changes inside a transaction owned by the caller,
// so other modules (e.g. adding a product with opening stock) can combine
// their own writes with stock changes atomically. Every write also refreshes
// the product's customer search row and announces the change to the store's
// live screens, both in that transaction.
type Ledger struct{}

func NewLedger() *Ledger { return &Ledger{} }

// ChangesChannel is the PostgreSQL NOTIFY channel for inventory changes; the
// payload is the store ID.
const ChangesChannel = "inventory_changed"

// changed runs after every write to an inventory row, inside its transaction.
func changed(tx *gorm.DB, row *inventoryRow) error {
	if err := availability.RefreshInventory(tx, row.ID); err != nil {
		return err
	}
	return realtime.Notify(tx, ChangesChannel, row.StoreID.String())
}

type inventoryRow struct {
	ID                uuid.UUID
	StoreID           uuid.UUID
	ProductVariantID  uuid.UUID
	OnHandQuantity    int
	ReservedQuantity  int
	LowStockThreshold int
	IsAvailable       bool
	UnlistedAt        *time.Time
	UpdatedAt         time.Time
}

func (r inventoryRow) stock() Stock {
	return Stock{OnHand: r.OnHandQuantity, Reserved: r.ReservedQuantity}
}

func (r inventoryRow) item() Item {
	listed := r.UnlistedAt == nil
	return Item{
		InventoryID:       r.ID,
		StoreID:           r.StoreID,
		VariantID:         r.ProductVariantID,
		OnHand:            r.OnHandQuantity,
		Reserved:          r.ReservedQuantity,
		Available:         r.stock().Available(),
		LowStockThreshold: r.LowStockThreshold,
		IsAvailable:       r.IsAvailable,
		Listed:            listed,
		StockStatus:       StockStatus(r.stock(), r.LowStockThreshold, r.IsAvailable, listed),
		UpdatedAt:         r.UpdatedAt,
	}
}

const rowColumns = `id, store_id, product_variant_id, on_hand_quantity, reserved_quantity,
	low_stock_threshold, is_available, unlisted_at, updated_at`

// setPrice sets the price when one is given (args: price, price, now). Giving
// the current price again still confirms it, so price_updated_at moves too.
const setPrice = `price = COALESCE(?::numeric, price),
	price_updated_at = CASE WHEN ?::numeric IS NULL THEN price_updated_at ELSE ?::timestamptz END`

func findRow(db *gorm.DB, storeID, variantID uuid.UUID, lock bool) (*inventoryRow, error) {
	sql := `SELECT ` + rowColumns + ` FROM inventory WHERE store_id = ? AND product_variant_id = ?`
	if lock {
		sql += ` FOR UPDATE`
	}
	var rows []inventoryRow
	if err := db.Raw(sql, storeID, variantID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("find inventory: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

// ResolveActor loads the display name recorded with history entries.
func ResolveActor(db *gorm.DB, userID uuid.UUID) (Actor, error) {
	var name string
	err := db.Raw(`SELECT COALESCE(NULLIF(full_name, ''), phone) FROM users WHERE id = ?`, userID).Scan(&name).Error
	if err != nil {
		return Actor{}, fmt.Errorf("resolve actor: %w", err)
	}
	return Actor{ID: userID, Name: name}, nil
}

// List adds a product to a store with zero stock, or relists a removed one
// keeping its stock and history.
func (Ledger) List(tx *gorm.DB, in ListInput) (Item, error) {
	if in.LowStockThreshold < 0 {
		return Item{}, &ValidationError{Message: "low stock threshold cannot be negative"}
	}
	if err := validPrice(in.Price); err != nil {
		return Item{}, err
	}
	now := time.Now().UTC()
	row, err := findRow(tx, in.StoreID, in.VariantID, true)
	switch {
	case errors.Is(err, ErrNotFound):
		row = &inventoryRow{
			ID:                uuid.New(),
			StoreID:           in.StoreID,
			ProductVariantID:  in.VariantID,
			LowStockThreshold: in.LowStockThreshold,
			IsAvailable:       in.IsAvailable,
			UpdatedAt:         now,
		}
		err = tx.Exec(`
			INSERT INTO inventory (id, store_id, product_variant_id, on_hand_quantity, reserved_quantity,
				low_stock_threshold, is_available, price, price_updated_at, listed_at, updated_at)
			VALUES (?, ?, ?, 0, 0, ?, ?,
				COALESCE(?::numeric, (SELECT price FROM product_variant WHERE id = ?)), ?, ?, ?)`,
			row.ID, row.StoreID, row.ProductVariantID, row.LowStockThreshold, row.IsAvailable,
			in.Price, row.ProductVariantID, now, now, now).Error
		if isUniqueViolation(err, "inventory_store_variant_key") {
			return Item{}, ErrAlreadyListed
		}
		if err != nil {
			return Item{}, fmt.Errorf("list product: %w", err)
		}
		if err := changed(tx, row); err != nil {
			return Item{}, err
		}
		return row.item(), nil
	case err != nil:
		return Item{}, err
	case row.UnlistedAt == nil:
		return Item{}, ErrAlreadyListed
	}
	err = tx.Exec(`
		UPDATE inventory
		SET unlisted_at = NULL, listed_at = ?, low_stock_threshold = ?, is_available = ?,
			`+setPrice+`, updated_at = ?
		WHERE id = ?`, now, in.LowStockThreshold, in.IsAvailable, in.Price, in.Price, now, now, row.ID).Error
	if err != nil {
		return Item{}, fmt.Errorf("relist product: %w", err)
	}
	if err := changed(tx, row); err != nil {
		return Item{}, err
	}
	row.UnlistedAt, row.LowStockThreshold, row.IsAvailable, row.UpdatedAt = nil, in.LowStockThreshold, in.IsAvailable, now
	return row.item(), nil
}

// Unlist removes a product from the store without deleting its stock or
// history. Units reserved for orders must be released first.
func (Ledger) Unlist(tx *gorm.DB, storeID, variantID uuid.UUID) error {
	row, err := findRow(tx, storeID, variantID, true)
	if err != nil {
		return err
	}
	if row.UnlistedAt != nil {
		return ErrNotFound
	}
	if row.ReservedQuantity > 0 {
		return &StockError{Message: fmt.Sprintf(
			"Cannot remove this product because %d %s reserved for orders.",
			row.ReservedQuantity, unitsAre(row.ReservedQuantity))}
	}
	now := time.Now().UTC()
	if err := tx.Exec(`UPDATE inventory SET unlisted_at = ?, updated_at = ? WHERE id = ?`, now, now, row.ID).Error; err != nil {
		return fmt.Errorf("unlist product: %w", err)
	}
	return changed(tx, row)
}

// UpdateSettings changes listing options; it never touches stock.
func (Ledger) UpdateSettings(tx *gorm.DB, storeID, variantID uuid.UUID, s Settings) (Item, error) {
	if s.LowStockThreshold != nil && *s.LowStockThreshold < 0 {
		return Item{}, &ValidationError{Message: "low stock threshold cannot be negative"}
	}
	if err := validPrice(s.Price); err != nil {
		return Item{}, err
	}
	row, err := findRow(tx, storeID, variantID, true)
	if err != nil {
		return Item{}, err
	}
	if row.UnlistedAt != nil {
		return Item{}, ErrUnlisted
	}
	if s.LowStockThreshold != nil {
		row.LowStockThreshold = *s.LowStockThreshold
	}
	if s.IsAvailable != nil {
		row.IsAvailable = *s.IsAvailable
	}
	row.UpdatedAt = time.Now().UTC()
	err = tx.Exec(`
		UPDATE inventory
		SET low_stock_threshold = ?, is_available = ?, `+setPrice+`, updated_at = ?
		WHERE id = ?`, row.LowStockThreshold, row.IsAvailable, s.Price, s.Price, row.UpdatedAt, row.UpdatedAt,
		row.ID).Error
	if err != nil {
		return Item{}, fmt.Errorf("update listing settings: %w", err)
	}
	if err := changed(tx, row); err != nil {
		return Item{}, err
	}
	return row.item(), nil
}

// ReceiveInput describes stock physically arriving at the store.
type ReceiveInput struct {
	StoreID   uuid.UUID
	VariantID uuid.UUID
	Quantity  int
	Reference string
	Note      string
	BatchID   *uuid.UUID
	Actor     Actor
}

func (l Ledger) Receive(tx *gorm.DB, in ReceiveInput) (Result, error) {
	c, err := receiveChange(in.Quantity)
	if err != nil {
		return Result{}, err
	}
	row, err := lockListed(tx, in.StoreID, in.VariantID)
	if err != nil {
		return Result{}, err
	}
	return l.record(tx, row, c, entryMeta{reference: in.Reference, note: in.Note, batchID: in.BatchID, actor: in.Actor})
}

// SaleInput describes units sold over the counter, outside online orders.
type SaleInput struct {
	StoreID   uuid.UUID
	VariantID uuid.UUID
	Quantity  int
	Reference string
	Note      string
	BatchID   *uuid.UUID
	Actor     Actor
}

// Sell takes sold units out of stock. Units reserved for online orders are
// never sold, so on_hand cannot drop below reserved.
func (l Ledger) Sell(tx *gorm.DB, in SaleInput) (Result, error) {
	c, err := saleChange(in.Quantity)
	if err != nil {
		return Result{}, err
	}
	row, err := lockListed(tx, in.StoreID, in.VariantID)
	if err != nil {
		return Result{}, err
	}
	return l.record(tx, row, c, entryMeta{reference: in.Reference, note: in.Note, batchID: in.BatchID, actor: in.Actor})
}

func (l Ledger) Adjust(tx *gorm.DB, storeID, variantID uuid.UUID, req AdjustRequest, actor Actor) (Result, error) {
	row, err := lockListed(tx, storeID, variantID)
	if err != nil {
		return Result{}, err
	}
	c, err := adjustChange(row.stock(), req)
	if err != nil {
		return Result{}, err
	}
	return l.record(tx, row, c, entryMeta{note: req.Note, actor: actor})
}

func lockListed(tx *gorm.DB, storeID, variantID uuid.UUID) (*inventoryRow, error) {
	row, err := findRow(tx, storeID, variantID, true)
	if err != nil {
		return nil, err
	}
	if row.UnlistedAt != nil {
		return nil, ErrUnlisted
	}
	return row, nil
}

type entryMeta struct {
	reference string
	note      string
	batchID   *uuid.UUID
	actor     Actor
}

// record applies a change to a row that the caller has locked FOR UPDATE and
// writes the matching history entry.
func (Ledger) record(tx *gorm.DB, row *inventoryRow, c change, meta entryMeta) (Result, error) {
	before := row.stock()
	after, err := apply(before, c)
	if err != nil {
		return Result{}, err
	}
	now := time.Now().UTC()
	err = tx.Exec(`UPDATE inventory SET on_hand_quantity = ?, reserved_quantity = ?, updated_at = ? WHERE id = ?`,
		after.OnHand, after.Reserved, now, row.ID).Error
	if err != nil {
		return Result{}, fmt.Errorf("update stock: %w", err)
	}

	entry := Transaction{
		ID:              uuid.New(),
		Type:            c.Type,
		Reason:          c.Reason,
		Quantity:        c.Delta,
		BeforeOnHand:    before.OnHand,
		AfterOnHand:     after.OnHand,
		BeforeReserved:  before.Reserved,
		AfterReserved:   after.Reserved,
		CountedQuantity: c.Counted,
		Reference:       optional(meta.reference),
		Note:            optional(meta.note),
		BatchID:         meta.batchID,
		CreatedAt:       now,
	}
	var createdBy *uuid.UUID
	if meta.actor.ID != uuid.Nil {
		actor := meta.actor
		entry.CreatedBy = &actor
		createdBy = &actor.ID
	}
	// A sale keeps the store's price at that moment, so sales reports don't change with later prices.
	err = tx.Exec(`
		INSERT INTO inventory_transaction (
			id, inventory_id, store_id, product_variant_id, type, reason, quantity,
			before_on_hand, after_on_hand, before_reserved, after_reserved, counted_quantity,
			reference, note, batch_id, created_by, created_at, unit_price
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			(SELECT price FROM inventory WHERE id = ? AND ?::text = 'OFFLINE_SALE'))`,
		entry.ID, row.ID, row.StoreID, row.ProductVariantID, entry.Type, entry.Reason, entry.Quantity,
		entry.BeforeOnHand, entry.AfterOnHand, entry.BeforeReserved, entry.AfterReserved, entry.CountedQuantity,
		entry.Reference, entry.Note, entry.BatchID, createdBy, entry.CreatedAt, row.ID, entry.Type).Error
	if err != nil {
		return Result{}, fmt.Errorf("record inventory transaction: %w", err)
	}
	if err := changed(tx, row); err != nil {
		return Result{}, err
	}

	row.OnHandQuantity, row.ReservedQuantity, row.UpdatedAt = after.OnHand, after.Reserved, now
	return Result{Inventory: row.item(), Transaction: entry}, nil
}

func optional(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && (constraint == "" || pgErr.ConstraintName == constraint)
}
