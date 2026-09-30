package availability

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type SyncOptions struct {
	// Apply writes missing and stale rows; otherwise Sync only reports them.
	Apply bool
	// StoreID limits the run to one store.
	StoreID   *uuid.UUID
	BatchSize int
}

// Drift is a row that does not match its sources.
type Drift struct {
	InventoryID uuid.UUID `json:"inventory_id"`
	// Fields lists the columns that differ, or is ["missing"] when there is no row.
	Fields []string `json:"fields"`
}

type SyncReport struct {
	Checked int
	Missing int
	Stale   int
	Written int
	// Drift holds the first maxReportedDrift differences.
	Drift []Drift
}

func (r SyncReport) Clean() bool { return r.Missing == 0 && r.Stale == 0 }

const maxReportedDrift = 50

// Sync compares every row with what its sources say it should be, page by
// page. It is the backfill (with Apply) and the reconciliation check (without).
func Sync(ctx context.Context, db *gorm.DB, opts SyncOptions) (SyncReport, error) {
	batch := opts.BatchSize
	if batch <= 0 {
		batch = refreshBatch
	}
	batch = min(batch, refreshBatch)
	var report SyncReport
	after := uuid.Nil
	for {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		ids, err := nextPage(ctx, db, after, opts.StoreID, batch)
		if err != nil {
			return report, err
		}
		if len(ids) == 0 {
			return report, nil
		}
		after = ids[len(ids)-1]
		report.Checked += len(ids)

		var drift []Drift
		if opts.Apply {
			err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec(`SELECT id FROM inventory WHERE id IN ? ORDER BY product_variant_id, store_id FOR UPDATE`,
					ids).Error; err != nil {
					return fmt.Errorf("lock inventory for availability: %w", err)
				}
				if drift, err = diff(tx, ids); err != nil {
					return err
				}
				changed, err := apply(tx, ids)
				report.Written += len(changed)
				return err
			})
		} else {
			// One snapshot for both reads, so a change committing in between
			// doesn't show up as drift.
			err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				drift, err = diff(tx, ids)
				return err
			}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		}
		if err != nil {
			return report, err
		}
		for _, d := range drift {
			if d.Fields[0] == "missing" {
				report.Missing++
			} else {
				report.Stale++
			}
			if len(report.Drift) < maxReportedDrift {
				report.Drift = append(report.Drift, d)
			}
		}
	}
}

func nextPage(ctx context.Context, db *gorm.DB, after uuid.UUID, storeID *uuid.UUID, limit int) ([]uuid.UUID, error) {
	query := `SELECT id FROM inventory WHERE id > ?`
	args := []any{after}
	if storeID != nil {
		query += ` AND store_id = ?`
		args = append(args, *storeID)
	}
	query += ` ORDER BY id LIMIT ?`
	args = append(args, limit)
	var page []struct{ ID uuid.UUID }
	if err := db.WithContext(ctx).Raw(query, args...).Scan(&page).Error; err != nil {
		return nil, fmt.Errorf("list inventory: %w", err)
	}
	ids := make([]uuid.UUID, len(page))
	for i, p := range page {
		ids[i] = p.ID
	}
	return ids, nil
}

func diff(db *gorm.DB, ids []uuid.UUID) ([]Drift, error) {
	expected, err := expectedRows(db, ids)
	if err != nil {
		return nil, err
	}
	stored, err := storedRows(db, ids)
	if err != nil {
		return nil, err
	}
	var out []Drift
	for _, want := range expected {
		got, ok := stored[want.InventoryID]
		if !ok {
			out = append(out, Drift{InventoryID: want.InventoryID, Fields: []string{"missing"}})
			continue
		}
		if fields := differingFields(want, got); len(fields) > 0 {
			out = append(out, Drift{InventoryID: want.InventoryID, Fields: fields})
		}
	}
	return out, nil
}

func differingFields(want, got Row) []string {
	var fields []string
	check := func(name string, same bool) {
		if !same {
			fields = append(fields, name)
		}
	}
	check("store_id", want.StoreID == got.StoreID)
	check("product_variant_id", want.VariantID == got.VariantID)
	check("catalog_key", want.CatalogKey == got.CatalogKey)
	check("location", equalPtr(want.Location, got.Location))
	check("price", want.Price == got.Price)
	check("price_updated_at", want.PriceUpdatedAt.Equal(got.PriceUpdatedAt))
	check("available_qty", want.AvailableQty == got.AvailableQty)
	check("availability_bucket", want.Bucket == got.Bucket)
	check("searchable", want.Searchable == got.Searchable)
	check("last_stock_update_at", want.LastStockUpdateAt.Equal(got.LastStockUpdateAt))
	return fields
}

func equalPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
