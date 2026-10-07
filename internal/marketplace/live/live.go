// Package live pushes changes to a product's availability to the customers
// looking at it. Committed InventoryChanged events leave the outbox through
// Sink to a Redis channel per product; each API instance's Gateway subscribes
// to the channels its open streams need and forwards what changed.
//
// Delivery is best effort: a stream starts with "ready", after which the
// client fetches a fresh snapshot, and gets "resync" when updates may have
// been lost. Inventory writes never wait on Redis.
package live

import (
	"time"

	"github.com/google/uuid"
)

// Update is one store's availability of a product as customers see it. It
// never carries the exact quantity.
type Update struct {
	CatalogKey         string    `json:"catalog_key"`
	StoreID            uuid.UUID `json:"store_id"`
	Price              float64   `json:"price"`
	AvailabilityBucket string    `json:"availability_bucket"`
	// MaxOrderQuantity is how many a customer can order, at most availability.MaxOrderQuantity.
	MaxOrderQuantity int `json:"max_order_quantity"`
	// Searchable is false once customers can no longer find the product at the store.
	Searchable        bool      `json:"searchable"`
	LastStockUpdateAt time.Time `json:"last_stock_update_at"`
	// Version orders the updates of one store's row: apply only a higher one.
	Version int64 `json:"version"`
}

// Channel is the Redis channel carrying a product's updates.
func Channel(catalogKey string) string { return "avail:" + catalogKey }
