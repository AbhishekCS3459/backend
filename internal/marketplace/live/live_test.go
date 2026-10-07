package live

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AbhishekCS3459/find-me-backend/internal/availability"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/outbox"
)

func inventoryChanged(t *testing.T, row availability.Row) outbox.Message {
	t.Helper()
	payload, err := json.Marshal(struct {
		SchemaVersion int `json:"schema_version"`
		availability.Row
	}{availability.InventoryChangedSchemaVersion, row})
	require.NoError(t, err)
	return outbox.Message{
		ID:            uuid.New(),
		AggregateType: availability.AggregateType,
		AggregateID:   row.InventoryID,
		EventType:     availability.EventInventoryChanged,
		Payload:       payload,
	}
}

func testRow(key string, store uuid.UUID, version int64) availability.Row {
	return availability.Row{
		InventoryID:       uuid.New(),
		StoreID:           store,
		CatalogKey:        key,
		Price:             "42.50",
		AvailableQty:      17,
		Bucket:            availability.InStock,
		Searchable:        true,
		LastStockUpdateAt: time.Now().UTC().Truncate(time.Second),
		Version:           version,
	}
}

func TestUpdateFromLeavesOutQuantity(t *testing.T) {
	store := uuid.New()
	u, ok := updateFrom(inventoryChanged(t, testRow("todayz:1", store, 3)))
	require.True(t, ok)
	assert.Equal(t, Update{
		CatalogKey: "todayz:1", StoreID: store, Price: 42.5, AvailabilityBucket: "IN_STOCK",
		MaxOrderQuantity: availability.MaxOrderQuantity,
		Searchable:       true, LastStockUpdateAt: u.LastStockUpdateAt, Version: 3,
	}, u)
	data, err := json.Marshal(u)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "available_qty")

	few := testRow("todayz:1", store, 4)
	few.AvailableQty = 3
	u, ok = updateFrom(inventoryChanged(t, few))
	require.True(t, ok)
	assert.Equal(t, 3, u.MaxOrderQuantity, "below the cap customers see how many they can order")
}

func TestUpdateFromSkipsUnusableEvents(t *testing.T) {
	other := inventoryChanged(t, testRow("todayz:1", uuid.New(), 1))
	other.EventType = "StoreRenamed"
	_, ok := updateFrom(other)
	assert.False(t, ok)

	noKey := inventoryChanged(t, testRow("", uuid.New(), 1))
	_, ok = updateFrom(noKey)
	assert.False(t, ok)

	badPrice := testRow("todayz:1", uuid.New(), 1)
	badPrice.Price = "n/a"
	_, ok = updateFrom(inventoryChanged(t, badPrice))
	assert.False(t, ok)
}

func TestVisibleChange(t *testing.T) {
	base := Update{AvailabilityBucket: "IN_STOCK", Price: 10, Searchable: true, LastStockUpdateAt: time.Now()}
	same := base
	same.LastStockUpdateAt = base.LastStockUpdateAt.Add(10 * time.Second)
	assert.False(t, visibleChange(base, same), "a few seconds' newer stock time alone")

	for name, change := range map[string]func(*Update){
		"bucket":     func(u *Update) { u.AvailabilityBucket = "OUT" },
		"orderable":  func(u *Update) { u.MaxOrderQuantity = 2 },
		"price":      func(u *Update) { u.Price = 11 },
		"searchable": func(u *Update) { u.Searchable = false },
		"stock time": func(u *Update) { u.LastStockUpdateAt = u.LastStockUpdateAt.Add(minTimestampStep) },
	} {
		next := base
		change(&next)
		assert.True(t, visibleChange(base, next), name)
	}
}

// newRedis connects to REDIS_URL, or a local Redis, and skips the test when
// neither answers.
func newRedis(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("REDIS_URL")
	if url == "" {
		url = "redis://127.0.0.1:6379"
	}
	opts, err := redis.ParseURL(url)
	require.NoError(t, err)
	rdb := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		t.Skipf("redis not available at %s: %v", url, err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func newGateway(t *testing.T, rdb *redis.Client) *Gateway {
	t.Helper()
	g := NewGateway(rdb, GatewayConfig{StaleAfter: 14 * 24 * time.Hour, MaxSubscriptions: 3})
	t.Cleanup(g.Close)
	return g
}

func uniqueKey() string { return "test:" + strings.ReplaceAll(uuid.NewString(), "-", "") }

func waitReady(t *testing.T, s *Subscription) {
	t.Helper()
	select {
	case <-s.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("subscription never became ready")
	}
}

// take waits for the subscription's next batch.
func take(t *testing.T, s *Subscription) ([]Update, bool) {
	t.Helper()
	select {
	case <-s.Notify():
		return s.Take()
	case <-time.After(3 * time.Second):
		t.Fatal("no update arrived")
		return nil, false
	}
}

func assertQuiet(t *testing.T, s *Subscription) {
	t.Helper()
	select {
	case <-s.Notify():
		updates, resync := s.Take()
		t.Fatalf("unexpected delivery: %+v resync=%v", updates, resync)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestSinkToGateway(t *testing.T) {
	rdb := newRedis(t)
	g := newGateway(t, rdb)
	sink := NewSink(rdb)
	ctx := context.Background()
	key := uniqueKey()
	storeA, storeB := uuid.New(), uuid.New()

	all, err := g.Subscribe(key, uuid.Nil)
	require.NoError(t, err)
	defer all.Close()
	onlyB, err := g.Subscribe(key, storeB)
	require.NoError(t, err)
	defer onlyB.Close()
	waitReady(t, all)
	waitReady(t, onlyB)

	rowA := testRow(key, storeA, 5)
	require.Empty(t, sink.Publish(ctx, []outbox.Message{inventoryChanged(t, rowA)}))
	updates, resync := take(t, all)
	assert.False(t, resync)
	require.Len(t, updates, 1)
	assert.Equal(t, storeA, updates[0].StoreID)
	assert.Equal(t, int64(5), updates[0].Version)
	assertQuiet(t, onlyB)

	t.Run("drops older versions and changes customers can't see", func(t *testing.T) {
		older := rowA
		older.Version = 4
		older.Bucket = availability.Out
		quantityOnly := rowA
		quantityOnly.Version = 6
		quantityOnly.AvailableQty = 16
		require.Empty(t, sink.Publish(ctx, []outbox.Message{
			inventoryChanged(t, older), inventoryChanged(t, quantityOnly),
		}))
		assertQuiet(t, all)
	})

	t.Run("coalesces per store, newest wins", func(t *testing.T) {
		low := rowA
		low.Version = 7
		low.Bucket = availability.Low
		out := rowA
		out.Version = 8
		out.Bucket = availability.Out
		require.Empty(t, sink.Publish(ctx, []outbox.Message{inventoryChanged(t, low), inventoryChanged(t, out)}))
		time.Sleep(200 * time.Millisecond)
		updates, _ := take(t, all)
		require.Len(t, updates, 1)
		assert.Equal(t, "OUT", updates[0].AvailabilityBucket)
		assert.Equal(t, int64(8), updates[0].Version)
	})

	t.Run("filters by store and applies staleness", func(t *testing.T) {
		stale := testRow(key, storeB, 2)
		stale.LastStockUpdateAt = time.Now().Add(-30 * 24 * time.Hour)
		require.Empty(t, sink.Publish(ctx, []outbox.Message{inventoryChanged(t, stale)}))
		updates, _ := take(t, onlyB)
		require.Len(t, updates, 1)
		assert.Equal(t, "CONFIRM_WITH_STORE", updates[0].AvailabilityBucket)
		updates, _ = take(t, all)
		require.Len(t, updates, 1)
		assert.Equal(t, storeB, updates[0].StoreID)
	})
}

func TestGatewayResyncsAfterReconnect(t *testing.T) {
	rdb := newRedis(t)
	g := newGateway(t, rdb)
	s, err := g.Subscribe(uniqueKey(), uuid.Nil)
	require.NoError(t, err)
	defer s.Close()
	waitReady(t, s)

	require.NoError(t, rdb.ClientKillByFilter(context.Background(), "TYPE", "pubsub").Err())
	updates, resync := take(t, s)
	assert.True(t, resync)
	assert.Empty(t, updates)
}

func TestGatewayLimitsAndClose(t *testing.T) {
	rdb := newRedis(t)
	g := newGateway(t, rdb)
	key := uniqueKey()
	var subs []*Subscription
	for range 3 {
		s, err := g.Subscribe(key, uuid.Nil)
		require.NoError(t, err)
		subs = append(subs, s)
	}
	_, err := g.Subscribe(key, uuid.Nil)
	assert.ErrorIs(t, err, ErrBusy)

	subs[0].Close()
	subs[0].Close()
	s, err := g.Subscribe(uniqueKey(), uuid.Nil)
	require.NoError(t, err, "closing frees a slot, once")
	_, err = g.Subscribe(key, uuid.Nil)
	assert.ErrorIs(t, err, ErrBusy)

	g.Close()
	select {
	case <-g.Done():
	default:
		t.Fatal("Done not closed")
	}
	_, err = g.Subscribe(key, uuid.Nil)
	assert.ErrorIs(t, err, ErrClosed)
	s.Close()
	subs[1].Close()
}
