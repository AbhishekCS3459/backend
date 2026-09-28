package listproducts

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/inventory"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newTestRepo creates a retailer with one store against TEST_DATABASE_URL or
// DATABASE_URL and removes everything it created when the test ends.
func newTestRepo(t *testing.T) (repo Repository, db *gorm.DB, userID, retailerID, storeID uuid.UUID) {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		t.Skip("DATABASE_URL / TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := database.NewDB(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(conn.Close)
	db = conn.Gorm

	var ledgerTable *string
	require.NoError(t, db.Raw(`SELECT to_regclass('inventory_transaction')::text`).Scan(&ledgerTable).Error)
	if ledgerTable == nil {
		t.Skip("migration 000037 (inventory ledger) is not applied")
	}

	userID, retailerID, storeID = uuid.New(), uuid.New(), uuid.New()
	categoryID := uuid.New()
	suffix := userID.String()[:8]
	exec := func(sql string, args ...any) {
		t.Helper()
		require.NoError(t, db.Exec(sql, args...).Error)
	}
	exec(`INSERT INTO users (id, phone, email, password_hash, full_name, user_type)
		VALUES (?, ?, ?, 'x', 'Test Owner', 'RETAILER')`, userID, "+91998"+suffix, suffix+"@listproducts.test")
	exec(`INSERT INTO retailers (id, user_id, legal_name, owner_name) VALUES (?, ?, 'Test Retail', 'Test Owner')`,
		retailerID, userID)
	exec(`INSERT INTO category (id, name) VALUES (?, ?)`, categoryID, "lp-test-"+suffix)
	exec(`INSERT INTO store (id, retailer_id, category_id, name, description) VALUES (?, ?, ?, 'Test Store', '')`,
		storeID, retailerID, categoryID)

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM inventory_transaction WHERE store_id = ?`, storeID).Error
		_ = db.Exec(`DELETE FROM inventory WHERE store_id = ?`, storeID).Error
		_ = db.Exec(`DELETE FROM product_image WHERE product_id IN (SELECT id FROM product WHERE retailer_id = ?)`, retailerID).Error
		_ = db.Exec(`DELETE FROM product_variant WHERE retailer_id = ?`, retailerID).Error
		_ = db.Exec(`DELETE FROM product WHERE retailer_id = ?`, retailerID).Error
		_ = db.Exec(`DELETE FROM store WHERE id = ?`, storeID).Error
		_ = db.Exec(`DELETE FROM category WHERE name = ?`, "lp-test-"+suffix).Error
		_ = db.Exec(`DELETE FROM brand WHERE name = ?`, "lp-brand-"+suffix).Error
		_ = db.Exec(`DELETE FROM retailers WHERE id = ?`, retailerID).Error
		_ = db.Exec(`DELETE FROM users WHERE id = ?`, userID).Error
	})
	return NewRepository(db, inventory.NewLedger()), db, userID, retailerID, storeID
}

func TestListingLifecycleGoesThroughLedger(t *testing.T) {
	repo, db, userID, retailerID, storeID := newTestRepo(t)
	ctx := context.Background()
	suffix := userID.String()[:8]

	created, err := repo.CreateAndList(ctx, userID, retailerID, storeID, &CreateProductRequest{
		Name: "Ledger Soap", Brand: "lp-brand-" + suffix, Category: "lp-test-" + suffix,
		SKU: "LP-" + suffix, Price: 25, OpeningQuantity: 12, LowStockThreshold: 3,
	})
	require.NoError(t, err)
	assert.Equal(t, 12, created.OnHand)
	assert.Equal(t, 0, created.Reserved)
	assert.Equal(t, 12, created.Available)
	assert.Equal(t, "in_stock", created.StockStatus)

	history := func() []struct {
		Type     string
		Quantity int
		Note     *string
	} {
		var rows []struct {
			Type     string
			Quantity int
			Note     *string
		}
		require.NoError(t, db.Raw(`SELECT type, quantity, note FROM inventory_transaction
			WHERE store_id = ? AND product_variant_id = ? ORDER BY seq`, storeID, created.VariantID).Scan(&rows).Error)
		return rows
	}
	opening := history()
	require.Len(t, opening, 1)
	assert.Equal(t, "STOCK_RECEIVED", opening[0].Type)
	assert.Equal(t, 12, opening[0].Quantity)
	require.NotNil(t, opening[0].Note)
	assert.Equal(t, openingStockNote, *opening[0].Note)

	_, err = repo.AddListing(ctx, userID, retailerID, storeID, &AddRequest{VariantID: created.VariantID}, true)
	assert.ErrorIs(t, err, inventory.ErrAlreadyListed)

	require.NoError(t, repo.RemoveListing(ctx, storeID, created.VariantID))
	listed, err := repo.ListStoreProducts(ctx, storeID, "", "")
	require.NoError(t, err)
	assert.Empty(t, listed, "removed products are hidden from the store list")
	stats, err := repo.StoreStats(ctx, storeID)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.ProductCount)
	catalog, err := repo.ListCatalog(ctx, retailerID, storeID, "Ledger Soap")
	require.NoError(t, err)
	require.Len(t, catalog, 1)
	assert.False(t, catalog[0].Listed)
	assert.Len(t, history(), 1, "removing keeps history")

	_, err = repo.UpdateListing(ctx, storeID, created.VariantID, &UpdateRequest{IsAvailable: new(bool)})
	assert.ErrorIs(t, err, inventory.ErrUnlisted)
	assert.ErrorIs(t, repo.RemoveListing(ctx, storeID, created.VariantID), inventory.ErrNotFound)

	relisted, err := repo.AddListing(ctx, userID, retailerID, storeID,
		&AddRequest{VariantID: created.VariantID, OpeningQuantity: 3, LowStockThreshold: 20}, true)
	require.NoError(t, err)
	assert.Equal(t, 15, relisted.OnHand, "re-adding restores kept stock plus the new opening quantity")
	assert.Equal(t, "low", relisted.StockStatus)
	assert.Len(t, history(), 2)

	off := false
	updated, err := repo.UpdateListing(ctx, storeID, created.VariantID, &UpdateRequest{IsAvailable: &off})
	require.NoError(t, err)
	assert.Equal(t, 15, updated.OnHand, "settings never touch stock")
	assert.Equal(t, "unavailable", updated.StockStatus)
}
