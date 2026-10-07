package orders

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/availability"
	"github.com/AbhishekCS3459/find-me-backend/internal/inventory"
	"github.com/AbhishekCS3459/find-me-backend/internal/payment"
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

func (a allowAll) Require(
	ctx context.Context, userID, storeID uuid.UUID, _ storeaccess.Permission,
) (*storeaccess.Access, error) {
	return a.Store(ctx, userID, storeID)
}

var testSecret = []byte("orders-test-secret")

type fixture struct {
	db       *gorm.DB
	svc      *Service
	ledger   *inventory.Ledger
	ownerID  uuid.UUID
	storeID  uuid.UUID
	keys     []string
	variants []uuid.UUID
	suffix   string
	users    []uuid.UUID
}

// newFixture creates a retailer with one open store selling one product per
// entry of stock (at ₹10, with that many units), against the database in
// TEST_DATABASE_URL or DATABASE_URL. Everything it creates is removed when the test ends.
func newFixture(t *testing.T, cfg Config, stock ...int) *fixture {
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
	require.NoError(t, db.Raw(`SELECT to_regclass('inventory_reservation')::text`).Scan(&table).Error)
	if table == nil {
		t.Skip("migration 000052 (orders) is not applied")
	}

	cfg.PickupSecret = testSecret
	f := &fixture{db: db, ledger: inventory.NewLedger(), ownerID: uuid.New(), storeID: uuid.New()}
	f.svc = NewService(db, f.ledger, allowAll{}, payment.NewDummy(), cfg)
	f.suffix = f.ownerID.String()[:8]
	retailerID, categoryID, brandID := uuid.New(), uuid.New(), uuid.New()

	exec := func(sql string, args ...any) {
		t.Helper()
		require.NoError(t, db.Exec(sql, args...).Error)
	}
	exec(`INSERT INTO users (id, phone, email, password_hash, full_name, user_type)
		VALUES (?, ?, ?, 'x', 'Test Owner', 'RETAILER')`, f.ownerID, "+91999"+f.suffix, f.suffix+"@orders.test")
	exec(`INSERT INTO retailers (id, user_id, legal_name, owner_name) VALUES (?, ?, 'Test Retail', 'Test Owner')`,
		retailerID, f.ownerID)
	exec(`INSERT INTO category (id, name) VALUES (?, ?)`, categoryID, "orders-test-"+f.suffix)
	exec(`INSERT INTO brand (id, name) VALUES (?, ?)`, brandID, "orders-test-"+f.suffix)
	exec(`INSERT INTO store (id, retailer_id, category_id, name, description) VALUES (?, ?, ?, 'Test Store', '')`,
		f.storeID, retailerID, categoryID)

	t.Cleanup(func() {
		orders := `(SELECT id FROM orders WHERE store_id = ?)`
		for _, sql := range []string{
			`DELETE FROM outbox_event WHERE aggregate_type = 'order' AND aggregate_id IN ` + orders,
			`DELETE FROM payment_webhook_event WHERE payload->>'provider_payment_id' IN
				(SELECT provider_payment_id FROM payment WHERE order_id IN ` + orders + `)`,
			`DELETE FROM inventory_reservation WHERE store_id = ?`,
			`DELETE FROM payment WHERE order_id IN ` + orders,
			`DELETE FROM order_status_history WHERE order_id IN ` + orders,
			`DELETE FROM inventory_transaction WHERE store_id = ?`,
			`DELETE FROM order_item WHERE order_id IN ` + orders,
			`DELETE FROM orders WHERE store_id = ?`,
			`DELETE FROM inventory WHERE store_id = ?`,
		} {
			_ = db.Exec(sql, f.storeID).Error
		}
		for _, id := range f.users {
			_ = db.Exec(`DELETE FROM order_idempotency WHERE customer_id = ?`, id).Error
			_ = db.Exec(`DELETE FROM users WHERE id = ?`, id).Error
		}
		_ = db.Exec(`DELETE FROM product_variant WHERE retailer_id = ?`, retailerID).Error
		_ = db.Exec(`DELETE FROM product WHERE retailer_id = ?`, retailerID).Error
		_ = db.Exec(`DELETE FROM store WHERE id = ?`, f.storeID).Error
		_ = db.Exec(`DELETE FROM brand WHERE id = ?`, brandID).Error
		_ = db.Exec(`DELETE FROM category WHERE id = ?`, categoryID).Error
		_ = db.Exec(`DELETE FROM retailers WHERE id = ?`, retailerID).Error
		_ = db.Exec(`DELETE FROM users WHERE id = ?`, f.ownerID).Error
	})

	for i, units := range stock {
		productID, variantID := uuid.New(), uuid.New()
		exec(`INSERT INTO product (id, retailer_id, category_id, brand_id, name, description, attributes, status)
			VALUES (?, ?, ?, ?, ?, '', '{}', 'ACTIVE')`,
			productID, retailerID, categoryID, brandID, fmt.Sprintf("Product %d", i))
		exec(`INSERT INTO product_variant (id, product_id, retailer_id, variant_label, sku, price)
			VALUES (?, ?, ?, 'Default', ?, 10)`, variantID, productID, retailerID, fmt.Sprintf("ORD-%s-%d", f.suffix, i))
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			listing := inventory.ListInput{StoreID: f.storeID, VariantID: variantID, IsAvailable: true}
			if _, err := f.ledger.List(tx, listing); err != nil {
				return err
			}
			if units == 0 {
				return nil
			}
			_, err := f.ledger.Receive(tx, inventory.ReceiveInput{StoreID: f.storeID, VariantID: variantID, Quantity: units})
			return err
		}))
		f.variants = append(f.variants, variantID)
		f.keys = append(f.keys, "variant:"+variantID.String())
	}
	return f
}

func (f *fixture) customer(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	short := id.String()[:8]
	require.NoError(t, f.db.Exec(`INSERT INTO users (id, phone, email, password_hash, full_name, user_type)
		VALUES (?, ?, ?, 'x', 'Test Customer', 'USER')`, id, "+91888"+short, short+"@customer.test").Error)
	f.users = append(f.users, id)
	return id
}

type stock struct{ OnHand, Reserved int }

func (f *fixture) stock(t *testing.T, i int) stock {
	t.Helper()
	var s stock
	require.NoError(t, f.db.Raw(`SELECT on_hand_quantity AS on_hand, reserved_quantity AS reserved
		FROM inventory WHERE store_id = ? AND product_variant_id = ?`, f.storeID, f.variants[i]).Scan(&s).Error)
	return s
}

func (f *fixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, f.db.Raw(sql, args...).Scan(&n).Error)
	return n
}

func (f *fixture) place(t *testing.T, customerID uuid.UUID, mode PaymentMode, quantities ...int) (*Order, error) {
	t.Helper()
	req := &PlaceRequest{StoreID: f.storeID, PaymentMode: mode}
	for i, q := range quantities {
		if q > 0 {
			req.Items = append(req.Items, PlaceItem{CatalogKey: f.keys[i], Quantity: q})
		}
	}
	order, _, err := f.svc.Place(context.Background(), customerID, uuid.NewString(), req)
	return order, err
}

// overdue moves the order's deadline into the past, as if its time ran out.
func (f *fixture) overdue(t *testing.T, orderID uuid.UUID) {
	t.Helper()
	require.NoError(t, f.db.Exec(`UPDATE orders SET expires_at = now() - interval '1 second' WHERE id = ?`, orderID).Error)
}

func (f *fixture) sweep(t *testing.T) {
	t.Helper()
	_, err := f.svc.ExpireDue(context.Background(), sweepBatch)
	require.NoError(t, err)
}

func (f *fixture) get(t *testing.T, customerID, orderID uuid.UUID) *Order {
	t.Helper()
	o, err := f.svc.Get(context.Background(), customerID, orderID)
	require.NoError(t, err)
	return o
}

// pay starts an online payment and reports the given outcome through the dummy gateway.
func (f *fixture) pay(t *testing.T, customerID, orderID uuid.UUID, outcome payment.Outcome) *Order {
	t.Helper()
	ctx := context.Background()
	p, err := f.svc.StartPayment(ctx, customerID, orderID)
	require.NoError(t, err)
	require.True(t, p.Simulated)
	o, err := f.svc.Simulate(ctx, customerID, orderID, p.ID, outcome)
	require.NoError(t, err)
	return o
}

// assertBooksBalance checks this store's reserved units against its active reservations.
func (f *fixture) assertBooksBalance(t *testing.T) {
	t.Helper()
	report, err := f.svc.Reconcile(context.Background())
	require.NoError(t, err)
	var ours []uuid.UUID
	require.NoError(t, f.db.Raw(`SELECT id FROM inventory WHERE store_id = ?`, f.storeID).Scan(&ours).Error)
	for _, d := range report.ReservedDrift {
		assert.NotContains(t, ours, d.InventoryID, "reserved units must match active reservations")
	}
}

func itemsError(t *testing.T, err error) *ItemsError {
	t.Helper()
	var ie *ItemsError
	require.True(t, errors.As(err, &ie), "want ItemsError, got %v", err)
	return ie
}

func TestLastUnitGoesToExactlyOneCustomer(t *testing.T) {
	f := newFixture(t, Config{}, 1)
	customers := make([]uuid.UUID, 20)
	for i := range customers {
		customers[i] = f.customer(t)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	won, outOfStock := 0, 0
	for _, c := range customers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.place(t, c, PaymentPayAtStore, 1)
			mu.Lock()
			defer mu.Unlock()
			var ie *ItemsError
			switch {
			case err == nil:
				won++
			case errors.As(err, &ie) && len(ie.Items) == 1 && ie.Items[0].Reason == ProblemOutOfStock:
				outOfStock++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, won)
	assert.Equal(t, 19, outOfStock)
	assert.Equal(t, stock{OnHand: 1, Reserved: 1}, f.stock(t, 0))
	assert.Equal(t, 1, f.count(t,
		`SELECT COUNT(*) FROM inventory_reservation WHERE store_id = ? AND status = 'ACTIVE'`, f.storeID))
	f.assertBooksBalance(t)
}

func TestPlaceIsIdempotent(t *testing.T) {
	f := newFixture(t, Config{}, 5)
	c := f.customer(t)
	ctx := context.Background()
	key := uuid.NewString()
	req := func(q int) *PlaceRequest {
		return &PlaceRequest{StoreID: f.storeID, PaymentMode: PaymentPayAtStore,
			Items: []PlaceItem{{CatalogKey: f.keys[0], Quantity: q}}}
	}

	first, replayed, err := f.svc.Place(ctx, c, key, req(2))
	require.NoError(t, err)
	assert.False(t, replayed)
	assert.Equal(t, StatusPlaced, first.Status)
	assert.Equal(t, int64(2000), first.TotalPaise)
	assert.NotEmpty(t, first.PickupCode)

	again, replayed, err := f.svc.Place(ctx, c, key, req(2))
	require.NoError(t, err)
	assert.True(t, replayed)
	assert.Equal(t, first.ID, again.ID)
	assert.Equal(t, 2, f.stock(t, 0).Reserved, "a retried order must not reserve twice")

	_, _, err = f.svc.Place(ctx, c, key, req(3))
	assert.ErrorIs(t, err, ErrKeyReused)

	_, _, err = f.svc.Place(ctx, c, "", req(1))
	var ve *ValidationError
	assert.True(t, errors.As(err, &ve), "an order needs an Idempotency-Key")
}

func TestOrderIsAllOrNothing(t *testing.T) {
	f := newFixture(t, Config{}, 5, 1)
	c := f.customer(t)

	_, err := f.place(t, c, PaymentPayAtStore, 2, 2)
	ie := itemsError(t, err)
	assert.Equal(t, "ITEMS_UNAVAILABLE", ie.Code)
	require.Len(t, ie.Items, 1)
	assert.Equal(t, f.keys[1], ie.Items[0].CatalogKey)
	assert.Equal(t, ProblemOutOfStock, ie.Items[0].Reason)
	require.NotNil(t, ie.Items[0].MaxOrderQuantity)
	assert.Equal(t, 1, *ie.Items[0].MaxOrderQuantity, "tells the customer how many they can still order")
	assert.Equal(t, 0, f.stock(t, 0).Reserved, "nothing is held when any line fails")
	assert.Equal(t, 0, f.count(t, `SELECT COUNT(*) FROM orders WHERE store_id = ?`, f.storeID))

	seen := 9.5
	_, _, err = f.svc.Place(context.Background(), c, uuid.NewString(), &PlaceRequest{
		StoreID: f.storeID, PaymentMode: PaymentPayAtStore,
		Items: []PlaceItem{{CatalogKey: f.keys[0], Quantity: 1, ExpectedUnitPrice: &seen}},
	})
	ie = itemsError(t, err)
	assert.Equal(t, ProblemPriceChanged, ie.Code)
	require.Len(t, ie.Items, 1)
	require.NotNil(t, ie.Items[0].CurrentUnitPrice)
	assert.InDelta(t, 10.0, *ie.Items[0].CurrentUnitPrice, 0.001)

	_, err = f.place(t, c, PaymentPayAtStore, 5, 1)
	require.NoError(t, err)
	assert.Equal(t, stock{OnHand: 5, Reserved: 5}, f.stock(t, 0))
	assert.Equal(t, stock{OnHand: 1, Reserved: 1}, f.stock(t, 1))
	f.assertBooksBalance(t)
}

func TestConcurrentCancelsReleaseOnce(t *testing.T) {
	f := newFixture(t, Config{}, 3)
	c := f.customer(t)
	order, err := f.place(t, c, PaymentPayAtStore, 3)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, err := f.svc.Cancel(context.Background(), c, order.ID, &CancelRequest{})
			if assert.NoError(t, err) {
				assert.Equal(t, StatusCancelled, o.Status)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, stock{OnHand: 3}, f.stock(t, 0))
	assert.Equal(t, 1, f.count(t,
		`SELECT COUNT(*) FROM inventory_transaction WHERE order_id = ? AND type = 'ORDER_RELEASED'`, order.ID))
	assert.Equal(t, 1, f.count(t,
		`SELECT COUNT(*) FROM order_status_history WHERE order_id = ? AND to_status = 'CANCELLED'`, order.ID))
	assert.Empty(t, f.get(t, c, order.ID).PickupCode, "a closed order has no pickup code")

	other := f.customer(t)
	_, err = f.svc.Cancel(context.Background(), other, order.ID, &CancelRequest{})
	assert.ErrorIs(t, err, ErrNotFound, "customers can't see each other's orders")
	f.assertBooksBalance(t)
}

func TestOnlineOrderPaidInTime(t *testing.T) {
	f := newFixture(t, Config{}, 2)
	c := f.customer(t)
	order, err := f.place(t, c, PaymentOnline, 2)
	require.NoError(t, err)
	assert.Equal(t, StatusPendingPayment, order.Status)
	assert.Empty(t, order.PickupCode, "no pickup code before payment")

	// A failed attempt leaves the order waiting; the customer can try again.
	o := f.pay(t, c, order.ID, payment.Failed)
	assert.Equal(t, StatusPendingPayment, o.Status)
	assert.Equal(t, PaymentUnpaid, o.PaymentStatus)

	o = f.pay(t, c, order.ID, payment.Succeeded)
	assert.Equal(t, StatusPlaced, o.Status)
	assert.Equal(t, PaymentPaid, o.PaymentStatus)
	require.NotNil(t, o.ExpiresAt, "the store's time to accept starts")
	assert.NotEmpty(t, o.PickupCode)
	assert.Equal(t, 2, f.stock(t, 0).Reserved)

	_, err = f.svc.StartPayment(context.Background(), c, order.ID)
	var se *StateError
	assert.True(t, errors.As(err, &se), "a paid order can't be paid again")
}

func TestDuplicateWebhookAppliesOnce(t *testing.T) {
	f := newFixture(t, Config{}, 1)
	c := f.customer(t)
	ctx := context.Background()
	order, err := f.place(t, c, PaymentOnline, 1)
	require.NoError(t, err)
	p, err := f.svc.StartPayment(ctx, c, order.ID)
	require.NoError(t, err)
	again, err := f.svc.StartPayment(ctx, c, order.ID)
	require.NoError(t, err)
	assert.Equal(t, p.ID, again.ID, "starting twice returns the open payment")

	var rec paymentRecord
	require.NoError(t, f.db.Raw(`SELECT `+paymentColumns+` FROM payment WHERE id = ?`, p.ID).Scan(&rec).Error)
	ev := payment.NewDummy().Simulate(rec.ProviderPaymentID, rec.AmountPaise, payment.Succeeded)
	for range 3 {
		require.NoError(t, f.svc.ProcessEvent(ctx, ev))
	}

	o := f.get(t, c, order.ID)
	assert.Equal(t, StatusPlaced, o.Status)
	assert.Equal(t, 1, f.count(t,
		`SELECT COUNT(*) FROM order_status_history WHERE order_id = ? AND to_status = 'PLACED'`, order.ID))
	assert.Equal(t, 0, f.count(t, `SELECT COUNT(*) FROM payment WHERE order_id = ? AND status <> 'SUCCEEDED'`, order.ID))
}

func TestPaymentAfterExpiryReservesAgainWhenStockRemains(t *testing.T) {
	f := newFixture(t, Config{}, 2)
	c := f.customer(t)
	ctx := context.Background()
	order, err := f.place(t, c, PaymentOnline, 2)
	require.NoError(t, err)
	p, err := f.svc.StartPayment(ctx, c, order.ID)
	require.NoError(t, err)

	f.overdue(t, order.ID)
	f.sweep(t)
	assert.Equal(t, StatusExpired, f.get(t, c, order.ID).Status)
	assert.Equal(t, stock{OnHand: 2}, f.stock(t, 0), "an expired hold frees the stock")

	o, err := f.svc.Simulate(ctx, c, order.ID, p.ID, payment.Succeeded)
	require.NoError(t, err)
	assert.Equal(t, StatusPlaced, o.Status)
	assert.Equal(t, PaymentPaid, o.PaymentStatus)
	assert.Equal(t, stock{OnHand: 2, Reserved: 2}, f.stock(t, 0))
	f.assertBooksBalance(t)
}

func TestPaymentAfterExpiryIsRefundedWhenStockIsGone(t *testing.T) {
	f := newFixture(t, Config{}, 1)
	c := f.customer(t)
	ctx := context.Background()
	order, err := f.place(t, c, PaymentOnline, 1)
	require.NoError(t, err)
	p, err := f.svc.StartPayment(ctx, c, order.ID)
	require.NoError(t, err)

	f.overdue(t, order.ID)
	f.sweep(t)
	_, err = f.place(t, f.customer(t), PaymentPayAtStore, 1)
	require.NoError(t, err, "the freed unit goes to the next customer")

	o, err := f.svc.Simulate(ctx, c, order.ID, p.ID, payment.Succeeded)
	require.NoError(t, err)
	assert.Equal(t, StatusExpired, o.Status)
	assert.Equal(t, PaymentRefunded, o.PaymentStatus)
	require.NotNil(t, o.Payment)
	assert.Equal(t, paymentRefunded, o.Payment.Status)
	assert.Equal(t, stock{OnHand: 1, Reserved: 1}, f.stock(t, 0))
	f.assertBooksBalance(t)
}

func TestOfflineSaleCannotTakeReservedUnits(t *testing.T) {
	f := newFixture(t, Config{}, 3)
	_, err := f.place(t, f.customer(t), PaymentPayAtStore, 2)
	require.NoError(t, err)

	sell := func(q int) error {
		return f.db.Transaction(func(tx *gorm.DB) error {
			_, err := f.ledger.Sell(tx, inventory.SaleInput{StoreID: f.storeID, VariantID: f.variants[0], Quantity: q})
			return err
		})
	}
	var se *inventory.StockError
	assert.True(t, errors.As(sell(2), &se), "only one unit is free to sell")
	require.NoError(t, sell(1))
	assert.Equal(t, stock{OnHand: 2, Reserved: 2}, f.stock(t, 0))
}

func TestPayAtStoreOrderToPickup(t *testing.T) {
	f := newFixture(t, Config{}, 5)
	c := f.customer(t)
	ctx := context.Background()
	order, err := f.place(t, c, PaymentPayAtStore, 3)
	require.NoError(t, err)
	assert.Equal(t, StatusPlaced, order.Status)
	assert.Equal(t, pickupCode(testSecret, order.ID), order.PickupCode)

	o, err := f.svc.Accept(ctx, f.ownerID, f.storeID, order.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusAccepted, o.Status)
	assert.Nil(t, o.ExpiresAt, "no deadline while the store prepares the order")
	assert.Empty(t, o.PickupCode, "the store never sees the pickup code")
	require.NotNil(t, o.Customer)

	_, err = f.svc.Complete(ctx, f.ownerID, f.storeID, order.ID, &CompleteRequest{PickupCode: order.PickupCode})
	var se *StateError
	assert.True(t, errors.As(err, &se), "can't hand over before it is ready")

	o, err = f.svc.Ready(ctx, f.ownerID, f.storeID, order.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusReady, o.Status)
	require.NotNil(t, o.ExpiresAt, "the pickup window starts")

	_, err = f.svc.Cancel(ctx, c, order.ID, &CancelRequest{})
	assert.True(t, errors.As(err, &se), "a ready order can't be cancelled")

	o, err = f.svc.Complete(ctx, f.ownerID, f.storeID, order.ID, &CompleteRequest{PickupCode: order.PickupCode})
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, o.Status)
	assert.Equal(t, PaymentPaid, o.PaymentStatus)
	assert.Equal(t, stock{OnHand: 2}, f.stock(t, 0))

	var sale struct {
		Quantity  int
		UnitPrice float64
	}
	require.NoError(t, f.db.Raw(`SELECT quantity, unit_price FROM inventory_transaction
		WHERE order_id = ? AND type = 'ORDER_PICKUP'`, order.ID).Scan(&sale).Error)
	assert.Equal(t, -3, sale.Quantity)
	assert.InDelta(t, 10.0, sale.UnitPrice, 0.001, "the sale is recorded at the order's price")
	assert.Equal(t, 1, f.count(t,
		`SELECT COUNT(*) FROM inventory_reservation WHERE order_id = ? AND status = 'CONSUMED'`, order.ID))
	f.assertBooksBalance(t)
}

func TestWrongPickupCodesLockTheCode(t *testing.T) {
	f := newFixture(t, Config{MaxPickupAttempts: 2}, 1)
	ctx := context.Background()
	order, err := f.place(t, f.customer(t), PaymentPayAtStore, 1)
	require.NoError(t, err)
	_, err = f.svc.Accept(ctx, f.ownerID, f.storeID, order.ID)
	require.NoError(t, err)
	_, err = f.svc.Ready(ctx, f.ownerID, f.storeID, order.ID)
	require.NoError(t, err)

	wrong := "000000"
	if order.PickupCode == wrong {
		wrong = "111111"
	}
	for left := 1; left >= 0; left-- {
		_, err = f.svc.Complete(ctx, f.ownerID, f.storeID, order.ID, &CompleteRequest{PickupCode: wrong})
		var pe *PickupCodeError
		require.True(t, errors.As(err, &pe), "got %v", err)
		assert.Equal(t, left, pe.AttemptsLeft)
	}
	_, err = f.svc.Complete(ctx, f.ownerID, f.storeID, order.ID, &CompleteRequest{PickupCode: order.PickupCode})
	assert.ErrorIs(t, err, ErrPickupLocked, "even the right code is refused once locked")

	override := &CompleteRequest{OverrideReason: "Checked the customer's ID"}
	o, err := f.svc.Complete(ctx, f.ownerID, f.storeID, order.ID, override)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, o.Status)
	require.NotEmpty(t, o.History)
	last := o.History[len(o.History)-1]
	require.NotNil(t, last.Reason)
	assert.Contains(t, *last.Reason, "Checked the customer's ID")
}

func TestDeadlinesCloseOrders(t *testing.T) {
	f := newFixture(t, Config{}, 3)
	ctx := context.Background()

	// The store never answers a paid order: rejected, stock freed, money back.
	c := f.customer(t)
	paid, err := f.place(t, c, PaymentOnline, 1)
	require.NoError(t, err)
	f.pay(t, c, paid.ID, payment.Succeeded)
	f.overdue(t, paid.ID)

	// The customer never collects a ready order.
	c2 := f.customer(t)
	ready, err := f.place(t, c2, PaymentPayAtStore, 1)
	require.NoError(t, err)
	_, err = f.svc.Accept(ctx, f.ownerID, f.storeID, ready.ID)
	require.NoError(t, err)
	_, err = f.svc.Ready(ctx, f.ownerID, f.storeID, ready.ID)
	require.NoError(t, err)
	f.overdue(t, ready.ID)

	f.sweep(t)

	o := f.get(t, c, paid.ID)
	assert.Equal(t, StatusRejected, o.Status)
	assert.Equal(t, PaymentRefunded, o.PaymentStatus)
	require.NotNil(t, o.ClosedReason)
	assert.Equal(t, "The store didn't respond in time", *o.ClosedReason)
	assert.Equal(t, StatusNoShow, f.get(t, c2, ready.ID).Status)
	assert.Equal(t, stock{OnHand: 3}, f.stock(t, 0))
	f.assertBooksBalance(t)
}

func TestCancellingAPaidOrderRefundsIt(t *testing.T) {
	f := newFixture(t, Config{}, 1)
	c := f.customer(t)
	order, err := f.place(t, c, PaymentOnline, 1)
	require.NoError(t, err)
	f.pay(t, c, order.ID, payment.Succeeded)

	o, err := f.svc.Cancel(context.Background(), c, order.ID, &CancelRequest{Reason: "Changed my mind"})
	require.NoError(t, err)
	assert.Equal(t, StatusCancelled, o.Status)
	assert.Equal(t, PaymentRefunded, o.PaymentStatus)
	assert.Equal(t, stock{OnHand: 1}, f.stock(t, 0))
}

func TestOrderLimits(t *testing.T) {
	f := newFixture(t, Config{MaxOpenOrders: 2}, 10)
	c := f.customer(t)
	for range 2 {
		_, err := f.place(t, c, PaymentPayAtStore, 1)
		require.NoError(t, err)
	}
	_, err := f.place(t, c, PaymentPayAtStore, 1)
	assert.ErrorIs(t, err, ErrTooManyOpen)

	_, err = f.place(t, f.customer(t), PaymentPayAtStore, availability.MaxOrderQuantity+1)
	var ve *ValidationError
	assert.True(t, errors.As(err, &ve), "a line can't exceed the order quantity cap")

	require.NoError(t, f.db.Exec(`UPDATE store SET is_open = false WHERE id = ?`, f.storeID).Error)
	_, err = f.place(t, f.customer(t), PaymentPayAtStore, 1)
	assert.ErrorIs(t, err, ErrStoreUnavailable)
}
