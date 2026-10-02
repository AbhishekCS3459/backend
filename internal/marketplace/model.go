// Package marketplace serves customer discovery: search for a product near a
// location, then see which nearby stores sell it, at what price and how
// available. It only reads: products from catalog_item, stores' prices and
// stock from store_product_availability, which every inventory write keeps
// current in the same transaction. Customers never see an exact quantity.
package marketplace

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrInvalidCursor = errors.New("invalid cursor")
	ErrTimeout       = errors.New("marketplace query timed out")
)

// ValidationError is a request parameter the client got wrong.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func invalid(message string) error { return &ValidationError{Message: message} }

const (
	MinQueryLength = 2
	MaxQueryLength = 100

	DefaultRadiusM = 5000
	MaxRadiusM     = 20000

	DefaultSearchLimit = 20
	// MaxSearchLimit caps both the products matched by text and those returned.
	MaxSearchLimit   = 50
	StoresPerProduct = 10

	DefaultStoreProductsLimit = 20
	MaxStoreProductsLimit     = 50

	// Browsing nearby products by category, without a query.
	DefaultNearbyRadiusM = 2000
	DefaultPerCategory   = 10
	MaxPerCategory       = 50
	MaxNearbyCategories  = 20
	// Listing every nearby product, one page at a time.
	DefaultNearbyPageLimit = 20
	MaxNearbyPageLimit     = 50
	// Listing the stores near a point, nearest first.
	DefaultNearbyStoresLimit = 50
	MaxNearbyStoresLimit     = 100
	// OtherCategory holds products whose category path is empty.
	OtherCategory = "Other"

	// DefaultStaleAfter is how long stock may go unconfirmed before customers
	// are told to confirm with the store.
	DefaultStaleAfter = 14 * 24 * time.Hour
)

// Sort orders the stores listed under each product.
type Sort string

const (
	// SortNearest: stores with stock (or unconfirmed) first, then by distance.
	SortNearest Sort = "nearest"
	// SortCheapest: stores with stock first, then by price, then distance.
	SortCheapest Sort = "cheapest"
	// SortAvailability: IN_STOCK, LOW, CONFIRM_WITH_STORE, OUT, then distance.
	SortAvailability Sort = "availability"
)

// Config tunes the marketplace.
type Config struct {
	// StaleAfter turns a bucket into CONFIRM_WITH_STORE when stock hasn't been
	// confirmed for this long. The stored bucket is never changed.
	StaleAfter time.Duration
}

// SearchParams is a validated search request.
type SearchParams struct {
	Query   string
	Lat     float64
	Lng     float64
	RadiusM int
	Limit   int
	Sort    Sort
}

// Debug holds internal fields, returned only in debug mode.
type Debug struct {
	AvailableQty int   `json:"available_qty"`
	Version      int64 `json:"version"`
}

// StoreOffer is one store selling a product: its price, distance and how
// available the product is there.
type StoreOffer struct {
	StoreID            uuid.UUID `json:"store_id"`
	StoreName          string    `json:"store_name"`
	DistanceM          int       `json:"distance_m"`
	Lat                float64   `json:"lat"`
	Lng                float64   `json:"lng"`
	Price              float64   `json:"price"`
	AvailabilityBucket string    `json:"availability_bucket"`
	LastStockUpdateAt  time.Time `json:"last_stock_update_at"`
	Debug              *Debug    `json:"debug,omitempty"`
}

// Product is the canonical product customers see, from catalog_item.
type Product struct {
	CatalogKey   string `json:"catalog_key"`
	Name         string `json:"name"`
	Brand        string `json:"brand"`
	Unit         string `json:"unit"`
	CategoryPath string `json:"category_path"`
	ImageURL     string `json:"image_url"`
}

// SearchProduct is a product with the nearby stores selling it.
type SearchProduct struct {
	Product
	Stores []StoreOffer `json:"stores"`
}

type SearchResult struct {
	Query    string          `json:"query"`
	Lat      float64         `json:"lat"`
	Lng      float64         `json:"lng"`
	RadiusM  int             `json:"radius_m"`
	Sort     Sort            `json:"sort"`
	Products []SearchProduct `json:"products"`
}

// Store is what customers see about a store. Owner and business details are
// never included.
type Store struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	AddressLine   string    `json:"address_line"`
	City          string    `json:"city"`
	Pincode       string    `json:"pincode"`
	Lat           float64   `json:"lat"`
	Lng           float64   `json:"lng"`
	IsOpen        bool      `json:"is_open"`
	VacationUntil *string   `json:"vacation_until,omitempty"`
}

// NearbyStoresParams is a validated request for the stores near a point.
type NearbyStoresParams struct {
	Lat     float64
	Lng     float64
	RadiusM int
	Limit   int
}

// NearbyStore is a store near the customer: what they see about it, how far
// it is and how much they can find there. A closed store lists nothing.
type NearbyStore struct {
	Store
	// Category is the store's kind, e.g. "Pharmacy"; empty when unknown.
	Category      string `json:"category"`
	DistanceM     int    `json:"distance_m"`
	CoverImageURL string `json:"cover_image_url,omitempty"`
	// ProductCount counts the products customers can find at the store;
	// AvailableCount those of them not out of stock.
	ProductCount   int `json:"product_count"`
	AvailableCount int `json:"available_count"`
}

type NearbyStoresResult struct {
	Lat     float64       `json:"lat"`
	Lng     float64       `json:"lng"`
	RadiusM int           `json:"radius_m"`
	Stores  []NearbyStore `json:"stores"`
	// HasMore is set when more than Limit stores are within the radius.
	HasMore bool `json:"has_more"`
}

// StoreProduct is a product at one store, with its price and availability.
type StoreProduct struct {
	Product
	StoreID            uuid.UUID `json:"store_id"`
	Price              float64   `json:"price"`
	AvailabilityBucket string    `json:"availability_bucket"`
	LastStockUpdateAt  time.Time `json:"last_stock_update_at"`
	Debug              *Debug    `json:"debug,omitempty"`
}

// NearbyParams is a validated request for products sold near a point.
type NearbyParams struct {
	Lat     float64
	Lng     float64
	RadiusM int
	Sort    Sort
	// Category limits the result to one top-level category, listed by name
	// one page at a time instead of best-stocked first.
	Category    string
	PerCategory int
	// Cursor is the client's next_cursor; the service decodes it into After,
	// the last product of the previous category page.
	Cursor string
	After  *categoryCursor
}

// NearbyCategory is a top-level category (the first part of category_path)
// with some of its nearby products. ProductCount counts all of them.
type NearbyCategory struct {
	Name         string          `json:"name"`
	ProductCount int             `json:"product_count"`
	Products     []SearchProduct `json:"products"`
}

type NearbyResult struct {
	Lat        float64          `json:"lat"`
	Lng        float64          `json:"lng"`
	RadiusM    int              `json:"radius_m"`
	Sort       Sort             `json:"sort"`
	Categories []NearbyCategory `json:"categories"`
	// Set only when browsing one category and more products follow.
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// NearbyPageParams is a validated request for one page of the products sold
// near a point, listed by name.
type NearbyPageParams struct {
	Lat     float64
	Lng     float64
	RadiusM int
	Sort    Sort
	// Category limits the listing to one top-level category; empty lists all.
	Category string
	Limit    int
	// Cursor is the client's next_cursor; the service decodes it into After.
	Cursor string
	After  *categoryCursor
}

// CategoryCount is a top-level category and how many nearby products it has.
type CategoryCount struct {
	Name         string `json:"name"`
	ProductCount int    `json:"product_count"`
}

// NearbyProductsPage is one page of the products sold near a point.
type NearbyProductsPage struct {
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
	RadiusM  int     `json:"radius_m"`
	Sort     Sort    `json:"sort"`
	Category string  `json:"category,omitempty"`
	// ProductCount counts every product in the listing, across all pages.
	ProductCount int `json:"product_count"`
	// Categories are all top-level categories nearby, whatever Category is.
	// Returned on the first page only.
	Categories []CategoryCount `json:"categories,omitempty"`
	Products   []SearchProduct `json:"products"`
	NextCursor string          `json:"next_cursor,omitempty"`
	HasMore    bool            `json:"has_more"`
}

// ProductParams is a validated request for one product's nearby stores.
type ProductParams struct {
	CatalogKey string
	Lat        float64
	Lng        float64
	RadiusM    int
	Sort       Sort
}

// ProductNearby is one product with the nearby stores selling it.
type ProductNearby struct {
	SearchProduct
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	RadiusM int     `json:"radius_m"`
	Sort    Sort    `json:"sort"`
}

// StoreProductsParams is a validated store products request.
type StoreProductsParams struct {
	StoreID uuid.UUID
	Limit   int
	After   *cursor
}

type StoreProductsPage struct {
	Products   []StoreProduct `json:"products"`
	NextCursor string         `json:"next_cursor,omitempty"`
	HasMore    bool           `json:"has_more"`
}
