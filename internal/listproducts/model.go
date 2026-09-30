package listproducts

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// AttributesJSON is JSONB stored on product.attributes (NOT NULL).
type AttributesJSON json.RawMessage

func (a AttributesJSON) Value() (driver.Value, error) {
	if len(a) == 0 {
		return "{}", nil
	}
	return string(a), nil
}

func (a *AttributesJSON) Scan(value interface{}) error {
	if value == nil {
		*a = AttributesJSON([]byte("{}"))
		return nil
	}
	switch typed := value.(type) {
	case []byte:
		copied := make([]byte, len(typed))
		copy(copied, typed)
		*a = AttributesJSON(copied)
		return nil
	case string:
		*a = AttributesJSON([]byte(typed))
		return nil
	default:
		return fmt.Errorf("unsupported attributes type %T", value)
	}
}

type Product struct {
	ID          uuid.UUID      `json:"id" gorm:"type:uuid;primaryKey"`
	RetailerID  *uuid.UUID     `json:"retailer_id,omitempty" gorm:"type:uuid"`
	StoreID     *uuid.UUID     `json:"store_id,omitempty" gorm:"type:uuid"`
	CategoryID  uuid.UUID      `json:"category_id" gorm:"type:uuid"`
	BrandID     uuid.UUID      `json:"brand_id" gorm:"type:uuid"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Attributes  AttributesJSON `json:"attributes" gorm:"type:jsonb;not null"`
	Status      string         `json:"status"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

func (Product) TableName() string { return "product" }

type Variant struct {
	ID           uuid.UUID `json:"id" gorm:"type:uuid;primaryKey"`
	ProductID    uuid.UUID `json:"product_id" gorm:"type:uuid"`
	RetailerID   uuid.UUID `json:"retailer_id" gorm:"type:uuid"`
	VariantLabel string    `json:"variant_label"`
	SKU          string    `json:"sku"`
	Price        float64   `json:"price"`
	CatalogKey   *string   `json:"catalog_key,omitempty"`
}

func (Variant) TableName() string { return "product_variant" }

type Image struct {
	ID        uuid.UUID `json:"id" gorm:"type:uuid;primaryKey"`
	ProductID uuid.UUID `json:"product_id" gorm:"type:uuid"`
	ImageURL  string    `json:"image_url"`
	SortOrder int       `json:"sort_order"`
}

func (Image) TableName() string { return "product_image" }

type Brand struct {
	ID   uuid.UUID `json:"id" gorm:"type:uuid;primaryKey"`
	Name string    `json:"name"`
}

func (Brand) TableName() string { return "brand" }

type Category struct {
	ID   uuid.UUID `json:"id" gorm:"type:uuid;primaryKey"`
	Name string    `json:"name"`
}

func (Category) TableName() string { return "category" }

// MaxListVariantIDs matches the most lines a counter bill can have.
const MaxListVariantIDs = 200

// ListFilter narrows a store's product list. VariantIDs keeps only those
// products, so a counter can recheck the items in an open order; a product
// removed from the store is simply missing from the result.
type ListFilter struct {
	Query      string
	Status     string
	VariantIDs []uuid.UUID
}

type Listing struct {
	InventoryID       uuid.UUID `json:"inventory_id"`
	ProductID         uuid.UUID `json:"product_id"`
	VariantID         uuid.UUID `json:"variant_id"`
	Name              string    `json:"name"`
	Brand             string    `json:"brand"`
	SKU               string    `json:"sku"`
	Category          string    `json:"category"`
	ImageURL          string    `json:"image_url"`
	Price             float64   `json:"price"`            // this store's selling price
	PriceUpdatedAt    time.Time `json:"price_updated_at"` // when the store last set or confirmed it
	OnHand            int       `json:"on_hand"`
	Reserved          int       `json:"reserved"`
	Available         int       `json:"available"`
	LowStockThreshold int       `json:"low_stock_threshold"`
	IsAvailable       bool      `json:"is_available"`
	StockStatus       string    `json:"stock_status"`
}

type StoreSummary struct {
	ProductCount int `json:"product_count"`
	// TotalInventory is the units on hand across the store's listed products.
	TotalInventory  int `json:"total_inventory"`
	LowStockCount   int `json:"low_stock_count"`
	OutOfStockCount int `json:"out_of_stock_count"`
}

type CatalogItem struct {
	ProductID uuid.UUID `json:"product_id"`
	VariantID uuid.UUID `json:"variant_id"`
	Name      string    `json:"name"`
	Brand     string    `json:"brand"`
	SKU       string    `json:"sku"`
	Category  string    `json:"category"`
	ImageURL  string    `json:"image_url"`
	// Price is the retailer's default, the price a new listing starts at.
	Price float64 `json:"price"`
	// StorePrice is this store's price, set whenever the store has listed the
	// product, including one it has since removed (re-adding keeps the price).
	StorePrice *float64 `json:"store_price"`
	Listed     bool     `json:"listed"`
}

type AddRequest struct {
	VariantID uuid.UUID `json:"variant_id" validate:"required"`
	// Price, when set, is this store's selling price; other stores keep theirs.
	// Otherwise a new listing starts at the product's default price and a
	// re-added one keeps the price it had.
	Price *float64 `json:"price" validate:"omitnil,gt=0"`
	// OpeningQuantity, when above zero, is recorded as a STOCK_RECEIVED
	// inventory transaction in the same database transaction as the listing.
	OpeningQuantity   int   `json:"opening_quantity" validate:"min=0,max=1000000"`
	LowStockThreshold int   `json:"low_stock_threshold" validate:"min=0"`
	IsAvailable       *bool `json:"is_available"`
}

// CreateProductRequest creates a product and lists it. For a catalogue product
// the server takes name, brand, category, image and SKU from the catalogue and
// ignores the client's values; a product made by hand needs all but the image.
type CreateProductRequest struct {
	Name              string  `json:"name" validate:"max=255"`
	Brand             string  `json:"brand" validate:"max=255"`
	Category          string  `json:"category" validate:"max=255"`
	SKU               string  `json:"sku" validate:"max=255"`
	Price             float64 `json:"price" validate:"required,gt=0"` // the default and this store's price
	ImageURL          string  `json:"image_url" validate:"max=2048"`
	OpeningQuantity   int     `json:"opening_quantity" validate:"min=0,max=1000000"`
	LowStockThreshold int     `json:"low_stock_threshold" validate:"min=0"`
	IsAvailable       *bool   `json:"is_available"`
	// CatalogProductID is the catalogue product this was added from (its
	// product_id). It must exist in the catalogue, and it groups this product
	// with other retailers' in customer search. Empty for a product made by hand.
	CatalogProductID string `json:"catalog_product_id" validate:"omitempty,max=20"`
}

// UpdateRequest changes listing settings only. Stock changes go through the
// inventory receive and adjust endpoints so every change is recorded.
type UpdateRequest struct {
	LowStockThreshold *int  `json:"low_stock_threshold" validate:"omitnil,min=0"`
	IsAvailable       *bool `json:"is_available"`
	// Price is this store's selling price; other stores keep theirs.
	Price *float64 `json:"price" validate:"omitnil,gt=0"`
	// QuantityAvailable is accepted only to reject it with a pointer to the
	// inventory API instead of silently ignoring it.
	QuantityAvailable *int `json:"quantity_available"`
}
