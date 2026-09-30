package analytics

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Repository reads one store's ledger. Times are instants; callers pass the
// store's time zone only where rows are grouped by local hour or day.
type Repository interface {
	Sales(ctx context.Context, storeID uuid.UUID, from, to time.Time) (Sales, error)
	Discarded(ctx context.Context, storeID uuid.UUID, from, to time.Time) (Discarded, error)
	// StockAt is the units on hand across the store's products at an instant.
	StockAt(ctx context.Context, storeID uuid.UUID, at time.Time) (int, error)
	Received(ctx context.Context, storeID uuid.UUID, from, to time.Time) (int, error)
	// SalesBy groups sales by local hour or day, keyed by the local wall-clock start.
	SalesBy(ctx context.Context, storeID uuid.UUID, from, to time.Time, unit string, tz string) ([]Point, error)
	TopSellers(ctx context.Context, storeID uuid.UUID, from, to time.Time, limit int) ([]Seller, error)
	SlowMovers(ctx context.Context, storeID uuid.UUID, from, to time.Time, limit int) ([]SlowMover, error)
	Missing(ctx context.Context, storeID uuid.UUID, from, to time.Time) (units int, value float64, err error)
	Shortfalls(ctx context.Context, storeID uuid.UUID, from, to time.Time, limit int) ([]Shortfall, error)
	// Stale lists products shown online with stock whose record hasn't changed since before.
	Stale(ctx context.Context, storeID uuid.UUID, before time.Time) ([]StaleProduct, error)
	Activity(ctx context.Context, storeID uuid.UUID) (Activity, error)
}

// Activity is what the store's record looks like right now.
type Activity struct {
	ListedWithStock int
	LastSaleAt      *time.Time
	FirstListedAt   *time.Time
}

type repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) Repository {
	return &repository{db: db}
}

// saleValue values a sale at its recorded price, or today's price for sales recorded before prices were kept.
const saleValue = `-t.quantity * COALESCE(t.unit_price, i.price)`

func (r *repository) Sales(ctx context.Context, storeID uuid.UUID, from, to time.Time) (Sales, error) {
	var row struct {
		Units     int
		Revenue   float64
		Estimated bool
	}
	err := r.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(-t.quantity), 0) AS units,
			COALESCE(SUM(`+saleValue+`), 0)::float8 AS revenue,
			COALESCE(BOOL_OR(t.unit_price IS NULL), FALSE) AS estimated
		FROM inventory_transaction t
		JOIN inventory i ON i.id = t.inventory_id
		WHERE t.store_id = ? AND t.type = 'OFFLINE_SALE' AND t.created_at >= ? AND t.created_at < ?`,
		storeID, from, to).Scan(&row).Error
	if err != nil {
		return Sales{}, fmt.Errorf("load sales: %w", err)
	}
	return Sales{Units: row.Units, Revenue: row.Revenue, Estimated: row.Estimated}, nil
}

func (r *repository) Discarded(ctx context.Context, storeID uuid.UUID, from, to time.Time) (Discarded, error) {
	var d Discarded
	err := r.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(-t.quantity), 0) AS units,
			COALESCE(SUM(-t.quantity) FILTER (WHERE t.reason = 'EXPIRED'), 0) AS expired_units,
			COALESCE(SUM(-t.quantity) FILTER (WHERE t.reason = 'DAMAGED'), 0) AS damaged_units,
			COALESCE(SUM(-t.quantity * i.price), 0)::float8 AS value
		FROM inventory_transaction t
		JOIN inventory i ON i.id = t.inventory_id
		WHERE t.store_id = ? AND t.type = 'ADJUSTMENT' AND t.reason IN ('EXPIRED', 'DAMAGED')
			AND t.quantity < 0 AND t.created_at >= ? AND t.created_at < ?`,
		storeID, from, to).Scan(&d).Error
	if err != nil {
		return Discarded{}, fmt.Errorf("load discarded stock: %w", err)
	}
	return d, nil
}

func (r *repository) StockAt(ctx context.Context, storeID uuid.UUID, at time.Time) (int, error) {
	var units int
	err := r.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(last.after_on_hand), 0)
		FROM inventory i
		JOIN LATERAL (
			SELECT after_on_hand FROM inventory_transaction
			WHERE inventory_id = i.id AND created_at < ?
			ORDER BY seq DESC
			LIMIT 1
		) last ON TRUE
		WHERE i.store_id = ?`, at, storeID).Scan(&units).Error
	if err != nil {
		return 0, fmt.Errorf("load opening stock: %w", err)
	}
	return units, nil
}

func (r *repository) Received(ctx context.Context, storeID uuid.UUID, from, to time.Time) (int, error) {
	var units int
	err := r.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(quantity), 0) FROM inventory_transaction
		WHERE store_id = ? AND type IN ('STOCK_RECEIVED', 'OPENING_BALANCE')
			AND created_at >= ? AND created_at < ?`, storeID, from, to).Scan(&units).Error
	if err != nil {
		return 0, fmt.Errorf("load received stock: %w", err)
	}
	return units, nil
}

func (r *repository) SalesBy(ctx context.Context, storeID uuid.UUID, from, to time.Time, unit, tz string) ([]Point, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, err
	}
	// to_char keeps the local wall-clock time; scanning a timestamp without zone would mislabel it as UTC.
	rows, err := r.db.WithContext(ctx).Raw(`
		SELECT to_char(date_trunc(?, t.created_at AT TIME ZONE ?), 'YYYY-MM-DD"T"HH24:MI:SS') AS local_start,
			SUM(-t.quantity) AS units,
			SUM(`+saleValue+`)::float8 AS revenue
		FROM inventory_transaction t
		JOIN inventory i ON i.id = t.inventory_id
		WHERE t.store_id = ? AND t.type = 'OFFLINE_SALE' AND t.created_at >= ? AND t.created_at < ?
		GROUP BY 1
		ORDER BY 1`, unit, tz, storeID, from, to).Rows()
	if err != nil {
		return nil, fmt.Errorf("load sales series: %w", err)
	}
	defer rows.Close()
	var points []Point
	for rows.Next() {
		var raw string
		var p Point
		if err := rows.Scan(&raw, &p.Units, &p.Revenue); err != nil {
			return nil, fmt.Errorf("scan sales series: %w", err)
		}
		if p.Start, err = time.ParseInLocation("2006-01-02T15:04:05", raw, loc); err != nil {
			return nil, fmt.Errorf("parse sales series: %w", err)
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

const productName = `JOIN product_variant v ON v.id = i.product_variant_id
		JOIN product p ON p.id = v.product_id`

func (r *repository) TopSellers(ctx context.Context, storeID uuid.UUID, from, to time.Time, limit int) ([]Seller, error) {
	sellers := []Seller{}
	err := r.db.WithContext(ctx).Raw(`
		SELECT i.product_variant_id AS variant_id, p.name,
			SUM(-t.quantity) AS units,
			SUM(`+saleValue+`)::float8 AS revenue
		FROM inventory_transaction t
		JOIN inventory i ON i.id = t.inventory_id
		`+productName+`
		WHERE t.store_id = ? AND t.type = 'OFFLINE_SALE' AND t.created_at >= ? AND t.created_at < ?
		GROUP BY i.product_variant_id, p.name
		ORDER BY units DESC, revenue DESC, p.name
		LIMIT ?`, storeID, from, to, limit).Scan(&sellers).Error
	if err != nil {
		return nil, fmt.Errorf("load top sellers: %w", err)
	}
	return sellers, nil
}

func (r *repository) SlowMovers(ctx context.Context, storeID uuid.UUID, from, to time.Time, limit int) ([]SlowMover, error) {
	movers := []SlowMover{}
	// Only products listed before the period: one added today hasn't had a chance to sell.
	err := r.db.WithContext(ctx).Raw(`
		SELECT i.product_variant_id AS variant_id, p.name, i.on_hand_quantity AS on_hand,
			COALESCE(sold.units, 0) AS units_sold, last_sale.at AS last_sale_at
		FROM inventory i
		`+productName+`
		LEFT JOIN (
			SELECT inventory_id, SUM(-quantity) AS units FROM inventory_transaction
			WHERE store_id = ? AND type = 'OFFLINE_SALE' AND created_at >= ? AND created_at < ?
			GROUP BY inventory_id
		) sold ON sold.inventory_id = i.id
		LEFT JOIN LATERAL (
			SELECT created_at AS at FROM inventory_transaction
			WHERE inventory_id = i.id AND type = 'OFFLINE_SALE'
			ORDER BY seq DESC
			LIMIT 1
		) last_sale ON TRUE
		WHERE i.store_id = ? AND i.unlisted_at IS NULL AND i.on_hand_quantity > 0 AND i.listed_at < ?
			AND COALESCE(sold.units, 0) < i.on_hand_quantity
		ORDER BY COALESCE(sold.units, 0)::float8 / i.on_hand_quantity, i.on_hand_quantity DESC, p.name
		LIMIT ?`, storeID, from, to, storeID, from, limit).Scan(&movers).Error
	if err != nil {
		return nil, fmt.Errorf("load slow movers: %w", err)
	}
	return movers, nil
}

// missingUnits are units that left without a sale: counts that found fewer than recorded, and stock marked lost.
const missingUnits = `t.type = 'ADJUSTMENT' AND t.reason IN ('STOCK_COUNT', 'LOST') AND t.quantity < 0`

func (r *repository) Missing(ctx context.Context, storeID uuid.UUID, from, to time.Time) (int, float64, error) {
	var row struct {
		Units int
		Value float64
	}
	err := r.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(-t.quantity), 0) AS units, COALESCE(SUM(-t.quantity * i.price), 0)::float8 AS value
		FROM inventory_transaction t
		JOIN inventory i ON i.id = t.inventory_id
		WHERE t.store_id = ? AND `+missingUnits+` AND t.created_at >= ? AND t.created_at < ?`,
		storeID, from, to).Scan(&row).Error
	if err != nil {
		return 0, 0, fmt.Errorf("load missing stock: %w", err)
	}
	return row.Units, row.Value, nil
}

func (r *repository) Shortfalls(ctx context.Context, storeID uuid.UUID, from, to time.Time, limit int) ([]Shortfall, error) {
	shortfalls := []Shortfall{}
	err := r.db.WithContext(ctx).Raw(`
		SELECT i.product_variant_id AS variant_id, p.name,
			SUM(-t.quantity) AS missing_units, MAX(t.created_at) AS last_found_at
		FROM inventory_transaction t
		JOIN inventory i ON i.id = t.inventory_id
		`+productName+`
		WHERE t.store_id = ? AND `+missingUnits+` AND t.created_at >= ? AND t.created_at < ?
		GROUP BY i.product_variant_id, p.name
		ORDER BY missing_units DESC, p.name
		LIMIT ?`, storeID, from, to, limit).Scan(&shortfalls).Error
	if err != nil {
		return nil, fmt.Errorf("load stock shortfalls: %w", err)
	}
	return shortfalls, nil
}

func (r *repository) Stale(ctx context.Context, storeID uuid.UUID, before time.Time) ([]StaleProduct, error) {
	stale := []StaleProduct{}
	// The same "last update" the customer search uses to show "confirm with store".
	err := r.db.WithContext(ctx).Raw(`
		SELECT i.product_variant_id AS variant_id, p.name,
			i.on_hand_quantity - i.reserved_quantity AS available,
			COALESCE(last.created_at, i.listed_at) AS last_update_at
		FROM inventory i
		`+productName+`
		LEFT JOIN LATERAL (
			SELECT created_at FROM inventory_transaction
			WHERE inventory_id = i.id
			ORDER BY seq DESC
			LIMIT 1
		) last ON TRUE
		WHERE i.store_id = ? AND i.unlisted_at IS NULL AND i.is_available
			AND i.on_hand_quantity - i.reserved_quantity > 0
			AND COALESCE(last.created_at, i.listed_at) < ?
		ORDER BY last_update_at, p.name`, storeID, before).Scan(&stale).Error
	if err != nil {
		return nil, fmt.Errorf("load stale stock: %w", err)
	}
	return stale, nil
}

func (r *repository) Activity(ctx context.Context, storeID uuid.UUID) (Activity, error) {
	var a Activity
	err := r.db.WithContext(ctx).Raw(`
		SELECT
			(SELECT COUNT(*) FROM inventory
				WHERE store_id = ? AND unlisted_at IS NULL AND is_available
					AND on_hand_quantity - reserved_quantity > 0) AS listed_with_stock,
			(SELECT MAX(created_at) FROM inventory_transaction
				WHERE store_id = ? AND type = 'OFFLINE_SALE') AS last_sale_at,
			(SELECT MIN(listed_at) FROM inventory
				WHERE store_id = ? AND unlisted_at IS NULL) AS first_listed_at`,
		storeID, storeID, storeID).Scan(&a).Error
	if err != nil {
		return Activity{}, fmt.Errorf("load store activity: %w", err)
	}
	return a, nil
}
