package listproducts

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/inventory"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/database"
	"github.com/AbhishekCS3459/find-me-backend/internal/productcatalog"
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
	}, nil)
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

func TestACatalogueProductIsOwnedOncePerRetailer(t *testing.T) {
	repo, db, userID, retailerID, storeID := newTestRepo(t)
	other, _, otherUser, otherRetailer, otherStore := newTestRepo(t)
	ctx := context.Background()
	suffix := userID.String()[:8]
	key := fmt.Sprintf("todayz:%d", time.Now().UnixNano())
	create := func(r Repository, user, retailer, store uuid.UUID, sku string, catalogKey *string) (*Listing, error) {
		var source *productcatalog.CanonicalProduct
		if catalogKey != nil {
			id, ok := productcatalog.CatalogKeyProductID(*catalogKey)
			require.True(t, ok)
			source = &productcatalog.CanonicalProduct{ProductID: id, Name: "Cola 500 ml"}
		}
		return r.CreateAndList(ctx, user, retailer, store, &CreateProductRequest{
			Name: "Cola 500 ml", Brand: "lp-brand-" + suffix, Category: "lp-test-" + suffix, SKU: sku, Price: 40,
		}, source)
	}
	storedKey := func(variantID uuid.UUID) *string {
		var rows []struct{ CatalogKey *string }
		require.NoError(t, db.Raw(`SELECT catalog_key FROM product_variant WHERE id = ?`, variantID).Scan(&rows).Error)
		require.Len(t, rows, 1)
		return rows[0].CatalogKey
	}

	created, err := create(repo, userID, retailerID, storeID, "LP-C-"+suffix, &key)
	require.NoError(t, err)
	require.NotNil(t, storedKey(created.VariantID))
	assert.Equal(t, key, *storedKey(created.VariantID))

	_, err = create(repo, userID, retailerID, storeID, "LP-C2-"+suffix, &key)
	assert.ErrorIs(t, err, ErrCatalogProductTaken, "a different SKU doesn't make it a different product")

	theirs, err := create(other, otherUser, otherRetailer, otherStore, "LP-C-"+suffix, &key)
	require.NoError(t, err, "another retailer can stock the same catalogue product")
	assert.Equal(t, key, *storedKey(theirs.VariantID))

	handMade, err := create(repo, userID, retailerID, storeID, "LP-H-"+suffix, nil)
	require.NoError(t, err)
	assert.Nil(t, storedKey(handMade.VariantID), "a product made by hand has no key")

	reserved := "variant:" + uuid.NewString()
	err = db.Exec(`UPDATE product_variant SET catalog_key = ? WHERE id = ?`, reserved, handMade.VariantID).Error
	require.Error(t, err)
	assert.Contains(t, err.Error(), "product_variant_catalog_key_format", "variant: keys belong to the search table")
}

func TestEachStoreSetsItsOwnPrice(t *testing.T) {
	repo, db, userID, retailerID, storeA := newTestRepo(t)
	ctx := context.Background()
	suffix := userID.String()[:8]
	storeB := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO store (id, retailer_id, category_id, name, description)
		SELECT ?, ?, id, 'Second Store', '' FROM category WHERE name = ?`, storeB, retailerID, "lp-test-"+suffix).Error)
	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM inventory_transaction WHERE store_id = ?`, storeB).Error
		_ = db.Exec(`DELETE FROM inventory WHERE store_id = ?`, storeB).Error
		_ = db.Exec(`DELETE FROM store WHERE id = ?`, storeB).Error
	})
	price := func(v float64) *float64 { return &v }

	created, err := repo.CreateAndList(ctx, userID, retailerID, storeA, &CreateProductRequest{
		Name: "Price Soap", Brand: "lp-brand-" + suffix, Category: "lp-test-" + suffix,
		SKU: "LP-P-" + suffix, Price: 25,
	}, nil)
	require.NoError(t, err)
	assert.InDelta(t, 25, created.Price, 0.001)
	variant := created.VariantID

	inB, err := repo.AddListing(ctx, userID, retailerID, storeB, &AddRequest{VariantID: variant}, true)
	require.NoError(t, err)
	assert.InDelta(t, 25, inB.Price, 0.001, "a new listing starts at the product's default price")
	listedAt := inB.PriceUpdatedAt
	assert.False(t, listedAt.IsZero())

	threshold := 4
	inB, err = repo.UpdateListing(ctx, storeB, variant, &UpdateRequest{LowStockThreshold: &threshold})
	require.NoError(t, err)
	assert.True(t, inB.PriceUpdatedAt.Equal(listedAt), "other settings leave the price date alone")

	inB, err = repo.UpdateListing(ctx, storeB, variant, &UpdateRequest{Price: price(22.5)})
	require.NoError(t, err)
	assert.InDelta(t, 22.5, inB.Price, 0.001)
	assert.True(t, inB.PriceUpdatedAt.After(listedAt))
	pricedAt := inB.PriceUpdatedAt

	inB, err = repo.UpdateListing(ctx, storeB, variant, &UpdateRequest{Price: price(22.5)})
	require.NoError(t, err)
	assert.True(t, inB.PriceUpdatedAt.After(pricedAt), "sending the same price again confirms it")
	pricedAt = inB.PriceUpdatedAt
	inA, err := repo.FindListing(ctx, storeA, variant)
	require.NoError(t, err)
	assert.InDelta(t, 25, inA.Price, 0.001, "another store's price change leaves this one alone")

	catalog, err := repo.ListCatalog(ctx, retailerID, storeB, "Price Soap")
	require.NoError(t, err)
	require.Len(t, catalog, 1)
	assert.InDelta(t, 25, catalog[0].Price, 0.001, "the default price is unchanged")
	require.NotNil(t, catalog[0].StorePrice)
	assert.InDelta(t, 22.5, *catalog[0].StorePrice, 0.001)

	require.NoError(t, repo.RemoveListing(ctx, storeB, variant))
	catalog, err = repo.ListCatalog(ctx, retailerID, storeB, "Price Soap")
	require.NoError(t, err)
	require.Len(t, catalog, 1)
	assert.False(t, catalog[0].Listed)
	require.NotNil(t, catalog[0].StorePrice, "a removed listing remembers its price")
	relisted, err := repo.AddListing(ctx, userID, retailerID, storeB, &AddRequest{VariantID: variant}, true)
	require.NoError(t, err)
	assert.InDelta(t, 22.5, relisted.Price, 0.001, "re-adding keeps the store's last price")
	assert.True(t, relisted.PriceUpdatedAt.Equal(pricedAt), "and when it was set, so an old price still looks old")

	var invalid *inventory.ValidationError
	_, err = repo.UpdateListing(ctx, storeB, variant, &UpdateRequest{Price: price(0)})
	assert.ErrorAs(t, err, &invalid)
	_, err = repo.UpdateListing(ctx, storeB, variant, &UpdateRequest{Price: price(1e10)})
	assert.ErrorAs(t, err, &invalid)
}
