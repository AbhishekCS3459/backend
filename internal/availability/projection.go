package availability

import (
	"fmt"
	"strings"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/outbox"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	AggregateType         = "store_product"
	EventInventoryChanged = "InventoryChanged"
	// InventoryChangedSchemaVersion changes when the payload changes shape.
	InventoryChangedSchemaVersion = 1
)

// Row is one row of store_product_availability and the body of its
// InventoryChanged event. It carries the exact count, so it is internal only:
// customers read the store_product_search view, which exposes just the bucket.
type Row struct {
	InventoryID uuid.UUID `json:"inventory_id"`
	StoreID     uuid.UUID `json:"store_id"`
	VariantID   uuid.UUID `json:"product_variant_id" gorm:"column:product_variant_id"`
	// Location is the geography as PostgreSQL prints it (hex EWKB).
	Location          *string   `json:"-"`
	Price             string    `json:"price"`
	AvailableQty      int       `json:"available_qty"`
	Bucket            Bucket    `json:"availability_bucket" gorm:"column:availability_bucket"`
	Searchable        bool      `json:"searchable"`
	LastStockUpdateAt time.Time `json:"last_stock_update_at"`
	Version           int64     `json:"version"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// inventoryChanged is the InventoryChanged payload: the full row, not a delta,
// so a consumer can apply any event whose version is newer than what it has.
type inventoryChanged struct {
	SchemaVersion int       `json:"schema_version"`
	OccurredAt    time.Time `json:"occurred_at"`
	Row
}

// RefreshInventory rebuilds the row for one inventory row.
func RefreshInventory(tx *gorm.DB, inventoryID uuid.UUID) error {
	return refresh(tx, "id = ?", inventoryID)
}

// RefreshStore rebuilds every row of a store, after its visibility or location changes.
func RefreshStore(tx *gorm.DB, storeID uuid.UUID) error {
	return refresh(tx, "store_id = ?", storeID)
}

// RefreshVariant rebuilds the variant's row in every store, after its price changes.
func RefreshVariant(tx *gorm.DB, variantID uuid.UUID) error {
	return refresh(tx, "product_variant_id = ?", variantID)
}

// refreshBatch keeps each upsert well under PostgreSQL's 65535 parameter limit.
const refreshBatch = 500

// refresh locks the matching inventory rows, then reads the current state in
// a new statement. Reading after the lock means a concurrent writer to the
// same rows has committed, so this transaction sees its changes and can't
// overwrite a newer row with older data. Rows are locked in (variant, store)
// order, the order batch stock changes use, so they can't deadlock.
func refresh(tx *gorm.DB, filter string, arg any) error {
	var locked []struct{ ID uuid.UUID }
	err := tx.Raw(`SELECT id FROM inventory WHERE `+filter+
		` ORDER BY product_variant_id, store_id FOR UPDATE`, arg).Scan(&locked).Error
	if err != nil {
		return fmt.Errorf("lock inventory for availability: %w", err)
	}
	ids := make([]uuid.UUID, len(locked))
	for i, l := range locked {
		ids[i] = l.ID
	}
	for start := 0; start < len(ids); start += refreshBatch {
		if _, err := apply(tx, ids[start:min(start+refreshBatch, len(ids))]); err != nil {
			return err
		}
	}
	return nil
}

// apply writes the expected rows for ids and records an event for each row
// that changed. It returns the changed rows.
func apply(tx *gorm.DB, ids []uuid.UUID) ([]Row, error) {
	expected, err := expectedRows(tx, ids)
	if err != nil {
		return nil, err
	}
	changed, err := upsert(tx, expected)
	if err != nil {
		return nil, err
	}
	if len(changed) == 0 {
		return nil, nil
	}
	events := make([]outbox.Event, len(changed))
	for i, row := range changed {
		events[i] = outbox.Event{
			AggregateType: AggregateType,
			AggregateID:   row.InventoryID,
			Type:          EventInventoryChanged,
			Payload: inventoryChanged{
				SchemaVersion: InventoryChangedSchemaVersion,
				OccurredAt:    row.UpdatedAt,
				Row:           row,
			},
		}
	}
	if err := outbox.Enqueue(tx, events...); err != nil {
		return nil, err
	}
	return changed, nil
}

// source is everything a row is derived from.
type source struct {
	InventoryID       uuid.UUID
	StoreID           uuid.UUID
	ProductVariantID  uuid.UUID
	OnHandQuantity    int
	ReservedQuantity  int
	LowStockThreshold int
	IsAvailable       bool
	Listed            bool
	StoreStatus       string
	StoreIsOpen       bool
	OnboardingStatus  string
	StoreDeleted      bool
	Location          *string
	Price             string
	LastStockUpdateAt time.Time
}

func (s source) row() Row {
	available := max(s.OnHandQuantity-s.ReservedQuantity, 0)
	return Row{
		InventoryID:  s.InventoryID,
		StoreID:      s.StoreID,
		VariantID:    s.ProductVariantID,
		Location:     s.Location,
		Price:        s.Price,
		AvailableQty: available,
		Bucket:       BucketFor(available, s.LowStockThreshold),
		// Search is by distance, so a store without a saved location can't be found.
		Searchable: s.Listed && s.IsAvailable && s.Location != nil &&
			StoreSearchable(s.StoreStatus, s.StoreIsOpen, s.OnboardingStatus, s.StoreDeleted),
		LastStockUpdateAt: s.LastStockUpdateAt,
	}
}

// expectedRows derives rows from the source tables, in (variant, store) order.
func expectedRows(db *gorm.DB, ids []uuid.UUID) ([]Row, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var sources []source
	err := db.Raw(`
		SELECT i.id AS inventory_id, i.store_id, i.product_variant_id,
			i.on_hand_quantity, i.reserved_quantity, i.low_stock_threshold, i.is_available,
			i.unlisted_at IS NULL AS listed,
			s.status AS store_status, s.is_open AS store_is_open, s.onboarding_status,
			s.deleted_at IS NOT NULL AS store_deleted,
			l.geog::text AS location,
			v.price::text AS price,
			COALESCE(t.created_at, i.listed_at) AS last_stock_update_at
		FROM inventory i
		JOIN store s ON s.id = i.store_id
		JOIN product_variant v ON v.id = i.product_variant_id
		LEFT JOIN store_location l ON l.store_id = i.store_id
		LEFT JOIN LATERAL (
			SELECT created_at FROM inventory_transaction
			WHERE inventory_id = i.id
			ORDER BY seq DESC
			LIMIT 1
		) t ON TRUE
		WHERE i.id IN ?
		ORDER BY i.product_variant_id, i.store_id`, ids).Scan(&sources).Error
	if err != nil {
		return nil, fmt.Errorf("load availability sources: %w", err)
	}
	rows := make([]Row, len(sources))
	for i, s := range sources {
		rows[i] = s.row()
	}
	return rows, nil
}

const rowColumns = `inventory_id, store_id, product_variant_id, location::text AS location,
	price::text AS price, available_qty, availability_bucket, searchable,
	last_stock_update_at, version, updated_at`

func storedRows(db *gorm.DB, ids []uuid.UUID) (map[uuid.UUID]Row, error) {
	var rows []Row
	err := db.Raw(`SELECT `+rowColumns+` FROM store_product_availability WHERE inventory_id IN ?`, ids).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load availability rows: %w", err)
	}
	out := make(map[uuid.UUID]Row, len(rows))
	for _, r := range rows {
		out[r.InventoryID] = r
	}
	return out, nil
}

// upsert inserts or updates rows and returns those that were written. A row
// that already matches is left alone, so its version only moves on real changes.
func upsert(tx *gorm.DB, rows []Row) ([]Row, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	values := make([]string, len(rows))
	args := make([]any, 0, len(rows)*9)
	for i, r := range rows {
		values[i] = "(?, ?, ?, ?::geography, ?::numeric, ?, ?, ?, ?)"
		args = append(args, r.InventoryID, r.StoreID, r.VariantID, r.Location, r.Price,
			r.AvailableQty, string(r.Bucket), r.Searchable, r.LastStockUpdateAt)
	}
	var changed []Row
	err := tx.Raw(`
		INSERT INTO store_product_availability AS a (
			inventory_id, store_id, product_variant_id, location, price, available_qty,
			availability_bucket, searchable, last_stock_update_at
		) VALUES `+strings.Join(values, ", ")+`
		ON CONFLICT (inventory_id) DO UPDATE SET
			store_id = EXCLUDED.store_id,
			product_variant_id = EXCLUDED.product_variant_id,
			location = EXCLUDED.location,
			price = EXCLUDED.price,
			available_qty = EXCLUDED.available_qty,
			availability_bucket = EXCLUDED.availability_bucket,
			searchable = EXCLUDED.searchable,
			last_stock_update_at = EXCLUDED.last_stock_update_at,
			version = a.version + 1,
			updated_at = NOW()
		WHERE a.store_id IS DISTINCT FROM EXCLUDED.store_id
			OR a.product_variant_id IS DISTINCT FROM EXCLUDED.product_variant_id
			OR a.location::text IS DISTINCT FROM EXCLUDED.location::text
			OR a.price IS DISTINCT FROM EXCLUDED.price
			OR a.available_qty IS DISTINCT FROM EXCLUDED.available_qty
			OR a.availability_bucket IS DISTINCT FROM EXCLUDED.availability_bucket
			OR a.searchable IS DISTINCT FROM EXCLUDED.searchable
			OR a.last_stock_update_at IS DISTINCT FROM EXCLUDED.last_stock_update_at
		RETURNING `+rowColumns, args...).Scan(&changed).Error
	if err != nil {
		return nil, fmt.Errorf("upsert availability: %w", err)
	}
	return changed, nil
}
