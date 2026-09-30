package analytics

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/inventory"
	"github.com/AbhishekCS3459/find-me-backend/internal/listproducts"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/database"
	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReportFromLedger(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		t.Skip("DATABASE_URL / TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	conn, err := database.NewDB(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(conn.Close)
	db := conn.Gorm

	userID, retailerID, storeID, categoryID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	suffix := userID.String()[:8]
	exec := func(sql string, args ...any) {
		t.Helper()
		require.NoError(t, db.Exec(sql, args...).Error)
	}
	exec(`INSERT INTO users (id, phone, email, password_hash, full_name, user_type)
		VALUES (?, ?, ?, 'x', 'Test Owner', 'RETAILER')`, userID, "+91997"+suffix, suffix+"@analytics.test")
	exec(`INSERT INTO retailers (id, user_id, legal_name, owner_name) VALUES (?, ?, 'Test Retail', 'Test Owner')`, retailerID, userID)
	exec(`INSERT INTO category (id, name) VALUES (?, ?)`, categoryID, "an-test-"+suffix)
	exec(`INSERT INTO store (id, retailer_id, category_id, name, description) VALUES (?, ?, ?, 'Test Store', '')`,
		storeID, retailerID, categoryID)
	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM inventory_transaction WHERE store_id = ?`, storeID).Error
		_ = db.Exec(`DELETE FROM store_product_availability WHERE store_id = ?`, storeID).Error
		_ = db.Exec(`DELETE FROM outbox_event WHERE aggregate_id IN (SELECT id FROM inventory WHERE store_id = ?)`, storeID).Error
		_ = db.Exec(`DELETE FROM inventory WHERE store_id = ?`, storeID).Error
		_ = db.Exec(`DELETE FROM product_variant WHERE retailer_id = ?`, retailerID).Error
		_ = db.Exec(`DELETE FROM product WHERE retailer_id = ?`, retailerID).Error
		_ = db.Exec(`DELETE FROM store WHERE id = ?`, storeID).Error
		_ = db.Exec(`DELETE FROM category WHERE id = ?`, categoryID).Error
		_ = db.Exec(`DELETE FROM brand WHERE name = ?`, "an-brand-"+suffix).Error
		_ = db.Exec(`DELETE FROM retailers WHERE id = ?`, retailerID).Error
		_ = db.Exec(`DELETE FROM users WHERE id = ?`, userID).Error
	})

	svc := NewService(NewRepository(db), storeaccess.NewResolver(db), 14*24*time.Hour)
	empty, err := svc.Report(ctx, userID, storeID, PeriodToday, "Asia/Kolkata")
	require.NoError(t, err)
	body, err := json.Marshal(empty)
	require.NoError(t, err)
	for _, list := range []string{`"top_sellers":[]`, `"slow_movers":[]`, `"shortfalls":[]`, `"stale":[]`} {
		assert.Contains(t, string(body), list, "an empty store still reports lists")
	}
	assert.Equal(t, SyncOK, empty.StockSync.Status)

	ledger := inventory.NewLedger()
	products := listproducts.NewRepository(db, ledger)
	create := func(name string, price float64, quantity int) uuid.UUID {
		t.Helper()
		listing, err := products.CreateAndList(ctx, userID, retailerID, storeID, &listproducts.CreateProductRequest{
			Name: name, Brand: "an-brand-" + suffix, Category: "an-test-" + suffix,
			SKU: "AN-" + name + "-" + suffix, Price: price, OpeningQuantity: quantity,
		}, nil)
		require.NoError(t, err)
		return listing.VariantID
	}
	change := func(fn func(tx *gorm.DB) error) {
		t.Helper()
		require.NoError(t, db.Transaction(fn))
	}

	// Rice sells 3 at ₹20, then its price goes up: the sale keeps ₹20.
	rice := create("Rice", 20, 10)
	change(func(tx *gorm.DB) error {
		_, err := ledger.Sell(tx, inventory.SaleInput{StoreID: storeID, VariantID: rice, Quantity: 3})
		return err
	})
	newPrice := 30.0
	change(func(tx *gorm.DB) error {
		_, err := ledger.UpdateSettings(tx, storeID, rice, inventory.Settings{Price: &newPrice})
		return err
	})

	// A count finds 3 soaps fewer than recorded, and one is thrown away as expired.
	soap := create("Soap", 50, 8)
	counted, expired := 5, -1
	change(func(tx *gorm.DB) error {
		_, err := ledger.Adjust(tx, storeID, soap, inventory.AdjustRequest{Mode: inventory.ModeCount, CountedQuantity: &counted}, inventory.Actor{})
		return err
	})
	change(func(tx *gorm.DB) error {
		_, err := ledger.Adjust(tx, storeID, soap, inventory.AdjustRequest{Mode: inventory.ModeChange, Quantity: &expired, Reason: inventory.ReasonExpired}, inventory.Actor{})
		return err
	})

	// Tea was listed three weeks ago and its stock never touched since.
	tea := create("Tea", 10, 4)
	old := time.Now().AddDate(0, 0, -21)
	exec(`UPDATE inventory SET listed_at = ? WHERE store_id = ? AND product_variant_id = ?`, old, storeID, tea)
	exec(`UPDATE inventory_transaction SET created_at = ? WHERE store_id = ? AND product_variant_id = ?`, old, storeID, tea)

	report, err := svc.Report(ctx, userID, storeID, Period7Days, "Asia/Kolkata")
	require.NoError(t, err)

	assert.Equal(t, 3, report.Sales.Units)
	assert.Equal(t, 60.0, report.Sales.Revenue, "sales keep the price they were sold at")
	assert.False(t, report.Sales.Estimated)
	assert.Equal(t, 0.0, report.Sales.PreviousRevenue)

	assert.Equal(t, 1, report.Discarded.Units)
	assert.Equal(t, 1, report.Discarded.ExpiredUnits)
	assert.Equal(t, 50.0, report.Discarded.Value)

	// Tea's 4 units were in stock when the week began; rice and soap arrived during it.
	assert.Equal(t, 22, report.SellThrough.Available)
	require.NotNil(t, report.SellThrough.Rate)
	assert.InDelta(t, 3.0/22, *report.SellThrough.Rate, 1e-9)

	require.Len(t, report.Series, 7)
	assert.Equal(t, 60.0, report.Series[6].Revenue, "today's sales land in the last day")

	require.Len(t, report.TopSellers, 1)
	assert.Equal(t, rice, report.TopSellers[0].VariantID)
	assert.Equal(t, 3, report.TopSellers[0].Units)

	require.Len(t, report.SlowMovers, 1, "products listed during the period aren't slow movers yet")
	assert.Equal(t, tea, report.SlowMovers[0].VariantID)
	assert.Equal(t, 0, report.SlowMovers[0].UnitsSold)

	sync := report.StockSync
	assert.Equal(t, 3, sync.MissingUnits)
	assert.Equal(t, 150.0, sync.MissingValue)
	require.NotNil(t, sync.UnrecordedRate)
	assert.InDelta(t, 0.5, *sync.UnrecordedRate, 1e-9, "3 missing of 6 that left the shop")
	require.Len(t, sync.Shortfalls, 1)
	assert.Equal(t, soap, sync.Shortfalls[0].VariantID)
	assert.Equal(t, 1, sync.StaleProducts)
	assert.Equal(t, tea, sync.Stale[0].VariantID)
	assert.Equal(t, 3, sync.ListedWithStock)
	require.NotNil(t, sync.DaysWithoutSales)
	assert.Equal(t, 0, *sync.DaysWithoutSales)
	assert.Equal(t, SyncWarning, sync.Status)

	_, err = svc.Report(ctx, uuid.New(), storeID, Period7Days, "")
	assert.ErrorIs(t, err, storeaccess.ErrNotFound, "other people's stores are not visible")
	_, err = svc.Report(ctx, userID, storeID, Period7Days, "Mars/Olympus")
	assert.ErrorIs(t, err, ErrInvalidTimezone)
}
