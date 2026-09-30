package availability_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/availability"
	"github.com/AbhishekCS3459/find-me-backend/internal/catalogitem"
	"github.com/AbhishekCS3459/find-me-backend/internal/inventory"
	"github.com/AbhishekCS3459/find-me-backend/internal/listproducts"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/database"
	"github.com/AbhishekCS3459/find-me-backend/internal/productcatalog"
	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/AbhishekCS3459/find-me-backend/internal/stores"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type allowAll struct{}

func (allowAll) Store(_ context.Context, _, storeID uuid.UUID) (*storeaccess.Access, error) {
	return &storeaccess.Access{StoreID: storeID, Role: storeaccess.RoleOwner}, nil
}

func (allowAll) Stores(context.Context, uuid.UUID) ([]storeaccess.Access, error) { return nil, nil }

func (a allowAll) Require(
	ctx context.Context, userID, storeID uuid.UUID, _ storeaccess.Permission,
) (*storeaccess.Access, error) {
	return a.Store(ctx, userID, storeID)
}

type fixture struct {
	db         *gorm.DB
	inventory  inventory.Service
	listings   listproducts.Repository
	stores     stores.Repository
	userID     uuid.UUID
	retailerID uuid.UUID
	suffix     string
	storeIDs   []uuid.UUID
	sku        int
}

// newFixture creates a retailer with no stores against TEST_DATABASE_URL or
// DATABASE_URL. Everything it (and addStore) creates is removed afterwards.
func newFixture(t *testing.T) *fixture {
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
	db := conn.Gorm

	var table *string
	require.NoError(t, db.Raw(`SELECT to_regclass('store_product_search')::text`).Scan(&table).Error)
	if table == nil {
		t.Skip("migration 000043 (availability search rules) is not applied")
	}

	ledger := inventory.NewLedger()
	f := &fixture{
		db:         db,
		inventory:  inventory.NewService(db, allowAll{}, ledger),
		listings:   listproducts.NewRepository(db, ledger),
		stores:     stores.NewRepository(db),
		userID:     uuid.New(),
		retailerID: uuid.New(),
	}
	f.suffix = f.userID.String()[:8]
	f.exec(t, `INSERT INTO users (id, phone, email, password_hash, full_name, user_type)
		VALUES (?, ?, ?, 'x', 'Test Owner', 'RETAILER')`, f.userID, "+91996"+f.suffix, f.suffix+"@availability.test")
	f.exec(t, `INSERT INTO retailers (id, user_id, legal_name, owner_name) VALUES (?, ?, 'Test Retail', 'Test Owner')`,
		f.retailerID, f.userID)
	f.exec(t, `INSERT INTO category (id, name) VALUES (?, ?)`, uuid.New(), f.category())

	t.Cleanup(func() {
		ids := f.storeIDs
		if len(ids) > 0 {
			for _, sql := range []string{
				`DELETE FROM outbox_event WHERE aggregate_id IN (SELECT id FROM inventory WHERE store_id IN ?)`,
				`DELETE FROM inventory_idempotency WHERE store_id IN ?`,
				`DELETE FROM inventory_transaction WHERE store_id IN ?`,
				`DELETE FROM inventory WHERE store_id IN ?`,
				`DELETE FROM store_location WHERE store_id IN ?`,
			} {
				_ = db.Exec(sql, ids).Error
			}
		}
		_ = db.Exec(`DELETE FROM product_variant WHERE retailer_id = ?`, f.retailerID).Error
		_ = db.Exec(`DELETE FROM product WHERE retailer_id = ?`, f.retailerID).Error
		_ = db.Exec(`DELETE FROM store WHERE retailer_id = ?`, f.retailerID).Error
		_ = db.Exec(`DELETE FROM category WHERE name = ?`, f.category()).Error
		_ = db.Exec(`DELETE FROM brand WHERE name = ?`, f.brand()).Error
		_ = db.Exec(`DELETE FROM retailers WHERE id = ?`, f.retailerID).Error
		_ = db.Exec(`DELETE FROM users WHERE id = ?`, f.userID).Error
	})
	return f
}

func (f *fixture) category() string { return "av-cat-" + f.suffix }
func (f *fixture) brand() string    { return "av-brand-" + f.suffix }

func (f *fixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	require.NoError(t, f.db.Exec(sql, args...).Error)
}

// addStore creates an open, active, located store with the given onboarding status.
func (f *fixture) addStore(t *testing.T, onboarding string) uuid.UUID {
	t.Helper()
	id := f.addUnlocatedStore(t, onboarding)
	f.saveLocation(t, id)
	return id
}

// addUnlocatedStore is addStore without a saved location.
func (f *fixture) addUnlocatedStore(t *testing.T, onboarding string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	f.exec(t, `INSERT INTO store (id, retailer_id, category_id, name, description, onboarding_status)
		SELECT ?, ?, id, 'Test Store', '', ? FROM category WHERE name = ?`, id, f.retailerID, onboarding, f.category())
	f.storeIDs = append(f.storeIDs, id)
	return id
}

func (f *fixture) saveLocation(t *testing.T, storeID uuid.UUID) {
	t.Helper()
	_, err := f.stores.SaveLocation(context.Background(), &stores.Location{
		StoreID: storeID, AddressLine: "12 Market Road", City: "Bengaluru", Pincode: "560001",
		Lat: 12.9716, Lng: 77.5946, ServiceAreaRadiusKm: 5,
	})
	require.NoError(t, err)
}

func (f *fixture) createProduct(
	t *testing.T, storeID uuid.UUID, price float64, opening, threshold int,
) *listproducts.Listing {
	t.Helper()
	return f.createKeyedProduct(t, storeID, nil, price, opening, threshold)
}

// createKeyedProduct is createProduct for a product added from the catalogue under catalogKey.
func (f *fixture) createKeyedProduct(
	t *testing.T, storeID uuid.UUID, catalogKey *string, price float64, opening, threshold int,
) *listproducts.Listing {
	t.Helper()
	f.sku++
	var source *productcatalog.CanonicalProduct
	if catalogKey != nil {
		id, ok := productcatalog.CatalogKeyProductID(*catalogKey)
		require.True(t, ok)
		source = &productcatalog.CanonicalProduct{ProductID: id, Name: "Availability Soap"}
	}
	listing, err := f.listings.CreateAndList(context.Background(), f.userID, f.retailerID, storeID,
		&listproducts.CreateProductRequest{
			Name: "Availability Soap", Brand: f.brand(), Category: f.category(),
			SKU: fmt.Sprintf("AV-%s-%d", f.suffix, f.sku), Price: price,
			OpeningQuantity: opening, LowStockThreshold: threshold,
		}, source)
	require.NoError(t, err)
	return listing
}

func (f *fixture) row(t *testing.T, inventoryID uuid.UUID) availability.Row {
	t.Helper()
	var rows []availability.Row
	require.NoError(t, f.db.Raw(`
		SELECT inventory_id, store_id, product_variant_id, catalog_key, location::text AS location, price::text AS price,
			price_updated_at, available_qty, availability_bucket, searchable, last_stock_update_at, version, updated_at
		FROM store_product_availability WHERE inventory_id = ?`, inventoryID).Scan(&rows).Error)
	require.Len(t, rows, 1, "every store product has a search row")
	return rows[0]
}

func (f *fixture) inventoryID(t *testing.T, storeID, variantID uuid.UUID) uuid.UUID {
	t.Helper()
	var row struct{ ID uuid.UUID }
	require.NoError(t, f.db.Raw(`SELECT id FROM inventory WHERE store_id = ? AND product_variant_id = ?`,
		storeID, variantID).Scan(&row).Error)
	return row.ID
}

// assertInSync checks every fixture store with the reconciliation check.
func (f *fixture) assertInSync(t *testing.T) {
	t.Helper()
	for _, id := range f.storeIDs {
		report, err := availability.Sync(context.Background(), f.db, availability.SyncOptions{StoreID: &id})
		require.NoError(t, err)
		assert.Truef(t, report.Clean(), "store %s drifted: %+v, keys without a catalog item: %v",
			id, report.Drift, report.MissingCatalogKeys)
	}
}

func (f *fixture) eventVersions(t *testing.T, inventoryID uuid.UUID) []int64 {
	t.Helper()
	var versions []int64
	require.NoError(t, f.db.Raw(`
		SELECT (payload->>'version')::bigint FROM outbox_event
		WHERE aggregate_type = ? AND aggregate_id = ? AND event_type = ?
		ORDER BY seq`, availability.AggregateType, inventoryID, availability.EventInventoryChanged).
		Scan(&versions).Error)
	return versions
}

// key returns a fresh idempotency key.
func key() string { return uuid.NewString() }

func intPtr(n int) *int           { return &n }
func boolPtr(b bool) *bool        { return &b }
func floatPtr(v float64) *float64 { return &v }

func TestEveryWritePathUpdatesTheSearchRow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	storeA := f.addUnlocatedStore(t, stores.OnboardingCompleted)
	storeB := f.addStore(t, stores.OnboardingCompleted)

	created := f.createProduct(t, storeA, 25, 12, 3)
	id := created.InventoryID
	row := f.row(t, id)
	assert.Equal(t, 12, row.AvailableQty)
	assert.Equal(t, availability.InStock, row.Bucket)
	assert.Equal(t, "25.00", row.Price)
	assert.Nil(t, row.Location, "no location saved yet")
	assert.False(t, row.Searchable, "a store without a location can't be found by distance")
	f.assertInSync(t)

	f.saveLocation(t, storeA)
	row = f.row(t, id)
	assert.NotNil(t, row.Location, "saving the location copies the point to the store's rows")
	assert.True(t, row.Searchable)
	f.assertInSync(t)

	variant := created.VariantID
	_, _, err := f.inventory.Sell(ctx, f.userID, storeA, variant, key(), &inventory.SellRequest{Quantity: 9})
	require.NoError(t, err)
	row = f.row(t, id)
	assert.Equal(t, 3, row.AvailableQty)
	assert.Equal(t, availability.Low, row.Bucket, "3 left with a threshold of 3 is low")

	_, _, err = f.inventory.Adjust(ctx, f.userID, storeA, variant, key(), &inventory.AdjustRequest{
		Mode: inventory.ModeChange, Quantity: intPtr(-3), Reason: inventory.ReasonDamaged,
	})
	require.NoError(t, err)
	assert.Equal(t, availability.Out, f.row(t, id).Bucket)

	_, _, err = f.inventory.Receive(ctx, f.userID, storeA, variant, key(), &inventory.ReceiveRequest{Quantity: 10})
	require.NoError(t, err)
	before := f.row(t, id)
	assert.Equal(t, availability.InStock, before.Bucket)

	count := &inventory.CountRequest{CountedQuantity: intPtr(10)}
	_, _, err = f.inventory.Count(ctx, f.userID, storeA, variant, key(), count)
	require.NoError(t, err)
	after := f.row(t, id)
	assert.Equal(t, 10, after.AvailableQty)
	assert.True(t, after.LastStockUpdateAt.After(before.LastStockUpdateAt),
		"a count that matches still confirms the stock")
	f.assertInSync(t)

	_, err = f.listings.UpdateListing(ctx, storeA, variant, &listproducts.UpdateRequest{IsAvailable: boolPtr(false)})
	require.NoError(t, err)
	assert.False(t, f.row(t, id).Searchable, "switched off for orders")
	_, err = f.listings.UpdateListing(ctx, storeA, variant, &listproducts.UpdateRequest{IsAvailable: boolPtr(true)})
	require.NoError(t, err)
	assert.True(t, f.row(t, id).Searchable)

	// Each store sets its own price.
	_, err = f.listings.AddListing(ctx, f.userID, f.retailerID, storeB, &listproducts.AddRequest{
		VariantID: created.VariantID, Price: floatPtr(30), OpeningQuantity: 4,
	}, true)
	require.NoError(t, err)
	idB := f.inventoryID(t, storeB, created.VariantID)
	assert.Equal(t, "30.00", f.row(t, idB).Price)
	unpriced := f.row(t, id)
	assert.Equal(t, "25.00", unpriced.Price, "listing elsewhere at another price leaves this store's alone")
	assert.True(t, unpriced.PriceUpdatedAt.Equal(created.PriceUpdatedAt), "stock changes leave the price date alone")
	_, err = f.listings.UpdateListing(ctx, storeA, variant, &listproducts.UpdateRequest{Price: floatPtr(27.5)})
	require.NoError(t, err)
	repriced := f.row(t, id)
	assert.Equal(t, "27.50", repriced.Price)
	assert.True(t, repriced.PriceUpdatedAt.After(unpriced.PriceUpdatedAt), "a new price records when it was set")
	assert.Equal(t, "30.00", f.row(t, idB).Price)
	f.assertInSync(t)

	_, err = f.stores.UpdateLocked(ctx, storeA, func(s *stores.Store) error {
		s.IsOpen = false
		return nil
	}, "is_open")
	require.NoError(t, err)
	assert.False(t, f.row(t, id).Searchable, "a closed store's products are hidden")
	_, err = f.stores.UpdateLocked(ctx, storeA, func(s *stores.Store) error {
		s.IsOpen = true
		return nil
	}, "is_open")
	require.NoError(t, err)
	assert.True(t, f.row(t, id).Searchable)

	require.NoError(t, f.listings.RemoveListing(ctx, storeA, created.VariantID))
	assert.False(t, f.row(t, id).Searchable, "an unlisted product is hidden")

	require.NoError(t, f.stores.Delete(ctx, storeB))
	assert.False(t, f.row(t, idB).Searchable, "a deleted store's products are hidden")
	f.assertInSync(t)

	final := f.row(t, id)
	versions := f.eventVersions(t, id)
	require.NotEmpty(t, versions)
	for i, v := range versions {
		assert.Equal(t, int64(i+1), v, "one event per change, versions without gaps")
	}
	assert.Equal(t, final.Version, versions[len(versions)-1], "the last event carries the row's current version")

	var payload struct {
		SchemaVersion int
		OccurredAt    *time.Time
	}
	require.NoError(t, f.db.Raw(`
		SELECT (payload->>'schema_version')::int AS schema_version,
			(payload->>'occurred_at')::timestamptz AS occurred_at
		FROM outbox_event WHERE aggregate_id = ? ORDER BY seq DESC LIMIT 1`, id).Scan(&payload).Error)
	assert.Equal(t, availability.InventoryChangedSchemaVersion, payload.SchemaVersion)
	require.NotNil(t, payload.OccurredAt)
	assert.WithinDuration(t, final.UpdatedAt, *payload.OccurredAt, time.Millisecond)
}

func TestCustomersSeeOnlyTheBucketAndStaleStockNeedsConfirming(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	storeID := f.addStore(t, stores.OnboardingCompleted)
	created := f.createProduct(t, storeID, 10, 5, 0)
	id := created.InventoryID

	var columns []string
	require.NoError(t, f.db.Raw(`
		SELECT column_name::text FROM information_schema.columns
		WHERE table_name = 'store_product_search'`).Scan(&columns).Error)
	assert.Contains(t, columns, "availability_bucket")
	assert.Contains(t, columns, "price_updated_at")
	assert.NotContains(t, columns, "available_qty", "customers never see the exact count")

	bucket := func() string {
		t.Helper()
		var buckets []string
		require.NoError(t, f.db.Raw(`SELECT availability_bucket FROM store_product_search WHERE inventory_id = ?`, id).
			Scan(&buckets).Error)
		if len(buckets) == 0 {
			return ""
		}
		return buckets[0]
	}
	assert.Equal(t, string(availability.InStock), bucket())

	f.exec(t, `UPDATE store_product_availability SET last_stock_update_at = NOW() - INTERVAL '15 days'
		WHERE inventory_id = ?`, id)
	assert.Equal(t, string(availability.ConfirmWithStore), bucket(), "nobody has confirmed the stock for 14 days")
	assert.Equal(t, availability.InStock, f.row(t, id).Bucket, "the stored bucket is left alone")

	count := &inventory.CountRequest{CountedQuantity: intPtr(5)}
	_, _, err := f.inventory.Count(ctx, f.userID, storeID, created.VariantID, key(), count)
	require.NoError(t, err)
	assert.Equal(t, string(availability.InStock), bucket(), "a count confirms the stock again")

	require.NoError(t, f.listings.RemoveListing(ctx, storeID, created.VariantID))
	assert.Empty(t, bucket(), "hidden rows aren't in the view")
}

func TestASearchableRowMustHaveALocation(t *testing.T) {
	f := newFixture(t)
	storeID := f.addStore(t, stores.OnboardingCompleted)
	created := f.createProduct(t, storeID, 10, 5, 0)
	require.True(t, f.row(t, created.InventoryID).Searchable)

	err := f.db.Exec(`UPDATE store_product_availability SET location = NULL WHERE inventory_id = ?`,
		created.InventoryID).Error
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store_product_availability_searchable_location")
}

func TestDraftStoreBecomesSearchableWhenSetupCompletes(t *testing.T) {
	f := newFixture(t)
	storeID := f.addStore(t, stores.OnboardingDraft)
	created := f.createProduct(t, storeID, 10, 5, 0)
	assert.False(t, f.row(t, created.InventoryID).Searchable, "draft stores are not in search")

	_, err := f.stores.UpdateLocked(context.Background(), storeID, func(s *stores.Store) error {
		s.OnboardingStatus = stores.OnboardingCompleted
		return nil
	}, "onboarding_status")
	require.NoError(t, err)
	assert.True(t, f.row(t, created.InventoryID).Searchable)
	f.assertInSync(t)
}

// Sales, price changes and store open/close touch the same rows at once. None
// may deadlock, and afterwards every row must match its sources.
func TestConcurrentWritersLeaveNoDrift(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	storeA := f.addStore(t, stores.OnboardingCompleted)
	storeB := f.addStore(t, stores.OnboardingCompleted)
	first := f.createProduct(t, storeA, 10, 100, 5)
	second := f.createProduct(t, storeA, 20, 100, 5)
	_, err := f.listings.AddListing(ctx, f.userID, f.retailerID, storeB, &listproducts.AddRequest{
		VariantID: first.VariantID, OpeningQuantity: 100,
	}, true)
	require.NoError(t, err)

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	run := func(n int, fn func(i int) error) {
		wg.Go(func() {
			for i := range n {
				if err := fn(i); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	run(10, func(int) error {
		_, _, err := f.inventory.SellBatch(ctx, f.userID, storeA, key(), &inventory.BatchRequest{
			Items: []inventory.BatchItem{{VariantID: second.VariantID, Quantity: 1}, {VariantID: first.VariantID, Quantity: 1}},
		})
		return err
	})
	run(10, func(int) error {
		_, _, err := f.inventory.Sell(ctx, f.userID, storeB, first.VariantID, key(), &inventory.SellRequest{Quantity: 1})
		return err
	})
	run(10, func(i int) error {
		price := float64(10 + i)
		_, err := f.listings.UpdateListing(ctx, storeA, first.VariantID, &listproducts.UpdateRequest{Price: &price})
		return err
	})
	run(10, func(i int) error {
		_, err := f.stores.UpdateLocked(ctx, storeA, func(s *stores.Store) error {
			s.IsOpen = i%2 == 1
			return nil
		}, "is_open")
		return err
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	f.assertInSync(t)
	inA, inB := f.row(t, first.InventoryID), f.row(t, f.inventoryID(t, storeB, first.VariantID))
	assert.Equal(t, 90, inA.AvailableQty)
	assert.Equal(t, 90, inB.AvailableQty)
	assert.Equal(t, "19.00", inA.Price, "the last price change wins")
	assert.Equal(t, "10.00", inB.Price, "store A's prices never reach store B")
	assert.True(t, inA.Searchable, "the last toggle reopened the store")
}

func TestSearchRowsCarryTheCatalogKey(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	storeID := f.addStore(t, stores.OnboardingCompleted)
	key := fmt.Sprintf("todayz:%d", time.Now().UnixNano())

	keyed := f.createKeyedProduct(t, storeID, &key, 40, 5, 0)
	handMade := f.createProduct(t, storeID, 10, 5, 0)
	assert.Equal(t, key, f.row(t, keyed.InventoryID).CatalogKey)
	assert.Equal(t, "variant:"+handMade.VariantID.String(), f.row(t, handMade.InventoryID).CatalogKey,
		"a product made by hand is a group of its own")

	var keys []string
	require.NoError(t, f.db.Raw(`SELECT catalog_key FROM store_product_search WHERE inventory_id = ?`,
		keyed.InventoryID).Scan(&keys).Error)
	assert.Equal(t, []string{key}, keys, "customer search can group by the key")

	before := f.row(t, keyed.InventoryID)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`UPDATE product_variant SET catalog_key = NULL WHERE id = ?`, keyed.VariantID).Error; err != nil {
			return err
		}
		if err := catalogitem.UpsertManual(tx, []uuid.UUID{keyed.VariantID}); err != nil {
			return err
		}
		return availability.RefreshVariants(tx, []uuid.UUID{keyed.VariantID})
	}))
	after := f.row(t, keyed.InventoryID)
	assert.Equal(t, "variant:"+keyed.VariantID.String(), after.CatalogKey)
	assert.Equal(t, before.Version+1, after.Version)
	var eventKey string
	require.NoError(t, f.db.Raw(`SELECT payload->>'catalog_key' FROM outbox_event
		WHERE aggregate_id = ? ORDER BY seq DESC LIMIT 1`, keyed.InventoryID).Scan(&eventKey).Error)
	assert.Equal(t, after.CatalogKey, eventKey, "the event carries the new key")
	f.assertInSync(t)

	f.exec(t, `UPDATE store_product_availability SET catalog_key = 'todayz:1' WHERE inventory_id = ?`, handMade.InventoryID)
	report, err := availability.Sync(ctx, f.db, availability.SyncOptions{StoreID: &storeID})
	require.NoError(t, err)
	require.Len(t, report.Drift, 1)
	assert.Equal(t, []string{"catalog_key"}, report.Drift[0].Fields)
}

func TestSyncReportsAndRepairsDrift(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	storeID := f.addStore(t, stores.OnboardingCompleted)
	created := f.createProduct(t, storeID, 10, 5, 0)
	id := created.InventoryID
	check := func(apply bool) availability.SyncReport {
		t.Helper()
		report, err := availability.Sync(ctx, f.db, availability.SyncOptions{StoreID: &storeID, Apply: apply})
		require.NoError(t, err)
		return report
	}

	f.exec(t, `UPDATE store_product_availability SET available_qty = 999, searchable = FALSE WHERE inventory_id = ?`, id)
	report := check(false)
	assert.Equal(t, 1, report.Stale)
	require.Len(t, report.Drift, 1)
	assert.Equal(t, []string{"available_qty", "searchable"}, report.Drift[0].Fields)
	assert.Equal(t, 999, f.row(t, id).AvailableQty, "the check never writes")

	f.exec(t, `DELETE FROM store_product_availability WHERE inventory_id = ?`, id)
	assert.Equal(t, 1, check(false).Missing)

	repaired := check(true)
	assert.Equal(t, 1, repaired.Missing)
	assert.Equal(t, 1, repaired.Written)
	assert.Equal(t, 5, f.row(t, id).AvailableQty)
	assert.True(t, check(false).Clean())
	assert.Equal(t, 0, check(true).Written, "a clean store needs no writes")
}
