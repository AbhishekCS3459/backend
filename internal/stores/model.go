package stores

import (
	"time"

	"github.com/google/uuid"
)

type Store struct {
	ID          uuid.UUID `json:"id" gorm:"type:uuid;primaryKey"`
	RetailerID  uuid.UUID `json:"retailer_id" gorm:"type:uuid"`
	CategoryID  uuid.UUID `json:"category_id" gorm:"type:uuid"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	IsOpen      bool      `json:"is_open"`
	KYBStatus   string    `json:"kyb_status" gorm:"column:kyb_status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (Store) TableName() string { return "store" }

type Media struct {
	ID        uuid.UUID `json:"id" gorm:"type:uuid;primaryKey"`
	StoreID   uuid.UUID `json:"store_id" gorm:"type:uuid"`
	MediaURL  string    `json:"media_url"`
	Type      string    `json:"type"`
	SortOrder int       `json:"sort_order"`
	IsCover   bool      `json:"is_cover"`
}

func (Media) TableName() string { return "store_media" }

type Summary struct {
	Store
	Images         []string `json:"images" gorm:"-"`
	ProductCount   int      `json:"product_count" gorm:"-"`
	TotalInventory int      `json:"total_inventory" gorm:"-"`
	LowStockCount  int      `json:"low_stock_count" gorm:"-"`
}

type CreateRequest struct {
	Name        string   `json:"name" validate:"required,min=2,max=255"`
	Description string   `json:"description"`
	Images      []string `json:"images"`
}
