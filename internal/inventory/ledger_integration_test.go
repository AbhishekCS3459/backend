package inventory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/database"
	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
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

func (a allowAll) Require(ctx context.Context, userID, storeID uuid.UUID, _ storeaccess.Permission) (*storeaccess.Access, error) {
	return a.Store(ctx, userID, storeID)
}

type fixture struct {
	db       *gorm.DB
	svc      Service
	ledger   *Ledger
	userID   uuid.UUID
	storeID  uuid.UUID
	variants []uuid.UUID
}

// newFixture creates a retailer with one store and n listed products, each
// holding the given opening stock, against the database in TEST_DATABASE_URL
// or DATABASE_URL. Everything it creates is removed when the test ends.
func newFixture(t *testing.T, n, opening int) *fixture {
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

	var ledgerTable *string
	require.NoError(t, db.Raw(`SELECT to_regclass('inventory_transaction')::text`).Scan(&ledgerTable).Error)
	if ledgerTable == nil {
		t.Skip("migration 000037 (inventory ledger) is not applied")
	}

	f := &fixture{db: db, ledger: NewLedger(), userID: uuid.New(), storeID: uuid.New()}
	f.svc = NewService(db, allowAll{}, f.ledger)
	suffix := f.userID.String()[:8]
	retailerID, categoryID, brandID := uuid.New(), uuid.New(), uuid.New()

	exec := func(sql string, args ...any) {
		t.Helper()
		require.NoError(t, db.Exec(sql, args...).Error)
	}
	exec(`INSERT INTO users (id, phone, email, password_hash, full_name, user_type)
		VALUES (?, ?, ?, 'x', 'Test Owner', 'RETAILER')`, f.userID, "+91999"+suffix, suffix+"@inventory.test")
	exec(`INSERT INTO retailers (id, user_id, legal_name, owner_name) VALUES (?, ?, 'Test Retail', 'Test Owner')`,
		retailerID, f.userID)
	exec(`INSERT INTO category (id, name) VALUES (?, ?)`, categoryID, "inv-test-"+suffix)
	exec(`INSERT INTO brand (id, name) VALUES (?, ?)`, brandID, "inv-test-"+suffix)
	exec(`INSERT INTO store (id, retailer_id, category_id, name, description) VALUES (?, ?, ?, 'Test Store', '')`,
		f.storeID, retailerID, categoryID)

	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM inventory_idempotency WHERE store_id = ?`,
			`DELETE FROM inventory_transaction WHERE store_id = ?`,
			`DELETE FROM inventory WHERE store_id = ?`,
		} {
			_ = db.Exec(sql, f.storeID).Error
		}
		_ = db.Exec(`DELETE FROM product_variant WHERE retailer_id = ?`, retailerID).Error
		_ = db.Exec(`DELETE FROM product WHERE retailer_id = ?`, retailerID).Error
		_ = db.Exec(`DELETE FROM store WHERE id = ?`, f.storeID).Error
		_ = db.Exec(`DELETE FROM brand WHERE id = ?`, brandID).Error
		_ = db.Exec(`DELETE FROM category WHERE id = ?`, categoryID).Error
		_ = db.Exec(`DELETE FROM retailers WHERE id = ?`, retailerID).Error
		_ = db.Exec(`DELETE FROM users WHERE id = ?`, f.userID).Error
	})

	for i := range n {
		productID, variantID := uuid.New(), uuid.New()
		exec(`INSERT INTO product (id, retailer_id, category_id, brand_id, name, description, attributes, status)
			VALUES (?, ?, ?, ?, ?, '', '{}', 'ACTIVE')`, productID, retailerID, categoryID, brandID, fmt.Sprintf("Product %d", i))
		exec(`INSERT INTO product_variant (id, product_id, retailer_id, variant_label, sku, price)
			VALUES (?, ?, ?, 'Default', ?, 10)`, variantID, productID, retailerID, fmt.Sprintf("INV-%s-%d", suffix, i))
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			if _, err := f.ledger.List(tx, ListInput{StoreID: f.storeID, VariantID: variantID, IsAvailable: true}); err != nil {
				return err
			}
			if opening == 0 {
				return nil
			}
			_, err := f.ledger.Receive(tx, ReceiveInput{StoreID: f.storeID, VariantID: variantID, Quantity: opening})
			return err
		}))
		f.variants = append(f.variants, variantID)
	}
	return f
}

func (f *fixture) stock(t *testing.T, variantID uuid.UUID) Stock {
	t.Helper()
	row, err := findRow(f.db, f.storeID, variantID, false)
	require.NoError(t, err)
	return row.stock()
}

func (f *fixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, f.db.Raw(sql, args...).Scan(&n).Error)
	return n
}

func key() string { return uuid.NewString() }

func TestConcurrentAdjustmentsNeverOversell(t *testing.T) {
	f := newFixture(t, 1, 20)
	v := f.variants[0]
	ctx := context.Background()

	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded, rejected := 0, 0
	for range 25 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := f.svc.Adjust(ctx, f.userID, f.storeID, v, key(),
				&AdjustRequest{Mode: ModeChange, Quantity: intp(-1), Reason: ReasonDamaged})
			mu.Lock()
			defer mu.Unlock()
			var se *StockError
			switch {
			case err == nil:
				succeeded++
			case errors.As(err, &se):
				rejected++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 20, succeeded)
	assert.Equal(t, 5, rejected)
	assert.Equal(t, Stock{}, f.stock(t, v))
	assert.Equal(t, 20, f.count(t, `SELECT COUNT(*) FROM inventory_transaction WHERE store_id = ? AND type = 'ADJUSTMENT'`, f.storeID))
	assert.Equal(t, 0, f.count(t, `SELECT COALESCE(SUM(quantity), 0) FROM inventory_transaction WHERE store_id = ?`, f.storeID),
		"history must add up to the current stock")
}

func TestIdempotencyKey(t *testing.T) {
	f := newFixture(t, 1, 0)
	v := f.variants[0]
	ctx := context.Background()
	k := key()
	req := &ReceiveRequest{Quantity: 12, Reference: "PO-1"}

	first, replayed, err := f.svc.Receive(ctx, f.userID, f.storeID, v, k, req)
	require.NoError(t, err)
	assert.False(t, replayed)
	require.NotNil(t, first.Transaction.CreatedBy)
	assert.Equal(t, "Test Owner", first.Transaction.CreatedBy.Name)

	again, replayed, err := f.svc.Receive(ctx, f.userID, f.storeID, v, k, req)
	require.NoError(t, err)
	assert.True(t, replayed)
	assert.Equal(t, first.Transaction.ID, again.Transaction.ID)
	assert.Equal(t, 12, f.stock(t, v).OnHand, "a retried request must not add stock twice")

	_, _, err = f.svc.Receive(ctx, f.userID, f.storeID, v, k, &ReceiveRequest{Quantity: 13})
	assert.ErrorIs(t, err, ErrKeyReused)

	_, _, err = f.svc.Receive(ctx, f.userID, f.storeID, v, "short", req)
	var ve *ValidationError
	assert.True(t, errors.As(err, &ve))
}

func TestIdempotencyKeyUnderConcurrentRetries(t *testing.T) {
	f := newFixture(t, 1, 0)
	v := f.variants[0]
	k := key()

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := f.svc.Receive(context.Background(), f.userID, f.storeID, v, k, &ReceiveRequest{Quantity: 5})
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	assert.Equal(t, 5, f.stock(t, v).OnHand)
	assert.Equal(t, 1, f.count(t, `SELECT COUNT(*) FROM inventory_transaction WHERE store_id = ?`, f.storeID))
}

func TestReceiveBatchIsAllOrNothing(t *testing.T) {
	f := newFixture(t, 2, 3)
	ctx := context.Background()
	before := f.count(t, `SELECT COUNT(*) FROM inventory_transaction WHERE store_id = ?`, f.storeID)

	_, _, err := f.svc.ReceiveBatch(ctx, f.userID, f.storeID, key(), &ReceiveBatchRequest{
		Reference: "INV-9",
		Items: []ReceiveBatchItem{
			{VariantID: f.variants[0], Quantity: 10},
			{VariantID: f.variants[1], Quantity: 10},
			{VariantID: uuid.New(), Quantity: 10},
		},
	})
	var lineErr *LineError
	require.True(t, errors.As(err, &lineErr), "got %v", err)
	assert.Equal(t, 2, lineErr.Index)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Equal(t, 3, f.stock(t, f.variants[0]).OnHand)
	assert.Equal(t, 3, f.stock(t, f.variants[1]).OnHand)
	assert.Equal(t, before, f.count(t, `SELECT COUNT(*) FROM inventory_transaction WHERE store_id = ?`, f.storeID))

	res, _, err := f.svc.ReceiveBatch(ctx, f.userID, f.storeID, key(), &ReceiveBatchRequest{
		Reference: "INV-9",
		Items: []ReceiveBatchItem{
			{VariantID: f.variants[0], Quantity: 10},
			{VariantID: f.variants[1], Quantity: 4},
		},
	})
	require.NoError(t, err)
	require.Len(t, res.Items, 2)
	assert.Equal(t, 13, res.Items[0].Inventory.OnHand)
	assert.Equal(t, 7, res.Items[1].Inventory.OnHand)
	assert.Equal(t, 2, f.count(t, `SELECT COUNT(*) FROM inventory_transaction WHERE batch_id = ?`, res.BatchID))

	_, _, err = f.svc.ReceiveBatch(ctx, f.userID, f.storeID, key(), &ReceiveBatchRequest{
		Items: []ReceiveBatchItem{{VariantID: f.variants[0], Quantity: 1}, {VariantID: f.variants[0], Quantity: 1}},
	})
	var ve *ValidationError
	assert.True(t, errors.As(err, &ve), "duplicate products in one delivery are rejected")
}

func TestStockCountRecordsDifference(t *testing.T) {
	f := newFixture(t, 1, 20)
	v := f.variants[0]
	ctx := context.Background()

	res, _, err := f.svc.Adjust(ctx, f.userID, f.storeID, v, key(), &AdjustRequest{Mode: ModeCount, CountedQuantity: intp(17)})
	require.NoError(t, err)
	assert.Equal(t, -3, res.Transaction.Quantity)
	assert.Equal(t, 17, res.Inventory.OnHand)
	require.NotNil(t, res.Transaction.CountedQuantity)
	assert.Equal(t, 17, *res.Transaction.CountedQuantity)

	res, _, err = f.svc.Adjust(ctx, f.userID, f.storeID, v, key(), &AdjustRequest{Mode: ModeCount, CountedQuantity: intp(17)})
	require.NoError(t, err)
	assert.Equal(t, 0, res.Transaction.Quantity, "a matching count is still recorded")

	page, err := f.svc.History(ctx, f.userID, f.storeID, v, "", 2)
	require.NoError(t, err)
	require.Len(t, page.Transactions, 2)
	assert.NotEmpty(t, page.NextCursor)
	assert.Equal(t, 0, page.Transactions[0].Quantity, "newest first")

	older, err := f.svc.History(ctx, f.userID, f.storeID, v, page.NextCursor, 2)
	require.NoError(t, err)
	require.Len(t, older.Transactions, 1)
	assert.Equal(t, TypeStockReceived, older.Transactions[0].Type)
	assert.Empty(t, older.NextCursor)
}

func TestCannotReduceBelowReserved(t *testing.T) {
	f := newFixture(t, 1, 10)
	v := f.variants[0]
	require.NoError(t, f.db.Exec(`UPDATE inventory SET reserved_quantity = 7 WHERE store_id = ?`, f.storeID).Error)

	_, _, err := f.svc.Adjust(context.Background(), f.userID, f.storeID, v, key(),
		&AdjustRequest{Mode: ModeChange, Quantity: intp(-5), Reason: ReasonLost})
	var se *StockError
	require.True(t, errors.As(err, &se), "got %v", err)
	assert.Equal(t, "Cannot reduce stock by 5 because 7 units are currently reserved.", se.Message)
	assert.Equal(t, Stock{OnHand: 10, Reserved: 7}, f.stock(t, v))

	err = f.db.Exec(`UPDATE inventory SET on_hand_quantity = 6 WHERE store_id = ?`, f.storeID).Error
	assert.Error(t, err, "the database refuses reserved > on hand even outside the service")
}

func TestLegacyColumnsStayInSync(t *testing.T) {
	f := newFixture(t, 1, 10)
	v := f.variants[0]

	require.NoError(t, f.db.Exec(`UPDATE inventory SET quantity_available = 33 WHERE store_id = ?`, f.storeID).Error)
	assert.Equal(t, 33, f.stock(t, v).OnHand, "a write from the old backend reaches the new column")

	_, _, err := f.svc.Receive(context.Background(), f.userID, f.storeID, v, key(), &ReceiveRequest{Quantity: 7})
	require.NoError(t, err)
	var legacy int
	require.NoError(t, f.db.Raw(`SELECT quantity_available FROM inventory WHERE store_id = ?`, f.storeID).Scan(&legacy).Error)
	assert.Equal(t, 40, legacy, "the old backend keeps reading correct stock")

	productVariant := uuid.New()
	require.NoError(t, f.db.Exec(`
		INSERT INTO product_variant (id, product_id, retailer_id, variant_label, sku, price)
		SELECT ?, product_id, retailer_id, 'Legacy', ?, 10 FROM product_variant WHERE id = ?`,
		productVariant, "LEGACY-"+productVariant.String()[:8], v).Error)
	require.NoError(t, f.db.Exec(`
		INSERT INTO inventory (id, store_id, product_variant_id, quantity_available, quantity_reserved)
		VALUES (?, ?, ?, 5, 0)`, uuid.New(), f.storeID, productVariant).Error)
	assert.Equal(t, Stock{OnHand: 5}, f.stock(t, productVariant), "an insert from the old backend fills the new columns")
}

func TestUnlistedProductsKeepStockButRejectChanges(t *testing.T) {
	f := newFixture(t, 1, 8)
	v := f.variants[0]
	ctx := context.Background()

	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.ledger.Unlist(tx, f.storeID, v) }))

	_, _, err := f.svc.Receive(ctx, f.userID, f.storeID, v, key(), &ReceiveRequest{Quantity: 1})
	assert.ErrorIs(t, err, ErrUnlisted)
	_, _, err = f.svc.Adjust(ctx, f.userID, f.storeID, v, key(), &AdjustRequest{Mode: ModeCount, CountedQuantity: intp(0)})
	assert.ErrorIs(t, err, ErrUnlisted)

	err = f.db.Transaction(func(tx *gorm.DB) error {
		item, err := f.ledger.List(tx, ListInput{StoreID: f.storeID, VariantID: v, IsAvailable: true})
		if err == nil {
			assert.Equal(t, 8, item.OnHand, "relisting restores the product with its stock")
			assert.True(t, item.Listed)
		}
		return err
	})
	require.NoError(t, err)

	err = f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.ledger.List(tx, ListInput{StoreID: f.storeID, VariantID: v, IsAvailable: true})
		return err
	})
	assert.ErrorIs(t, err, ErrAlreadyListed)
}
