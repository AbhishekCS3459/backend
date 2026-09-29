package stores

import (
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/availability"
	"github.com/AbhishekCS3459/find-me-backend/internal/identity/progress"
	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	OnboardingDraft     = "DRAFT"
	OnboardingCompleted = "COMPLETED"
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
	// Onboarding columns are exposed through OnboardingSummary / progress.View instead.
	OnboardingStatus      string         `json:"-" gorm:"column:onboarding_status"`
	OnboardingData        progress.JSONB `json:"-" gorm:"column:onboarding;type:jsonb"`
	OnboardingCompletedAt *time.Time     `json:"-" gorm:"column:onboarding_completed_at"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
	// Soft delete: gorm skips deleted stores in every query on this model.
	DeletedAt gorm.DeletedAt `json:"-"`
}

func (Store) TableName() string { return "store" }

func (s *Store) searchable() bool {
	return availability.StoreSearchable(s.Status, s.IsOpen, s.OnboardingStatus, s.DeletedAt.Valid)
}

// Location is where the store is. Customer search finds nearby stores by its
// point (store_location.geog, which PostgreSQL computes from lat and lng).
type Location struct {
	StoreID             uuid.UUID `json:"store_id" gorm:"type:uuid;primaryKey"`
	AddressLine         string    `json:"address_line"`
	City                string    `json:"city"`
	Pincode             string    `json:"pincode"`
	Lat                 float64   `json:"lat"`
	Lng                 float64   `json:"lng"`
	ServiceAreaRadiusKm int       `json:"service_area_radius_km" gorm:"column:service_area_radius_km"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func (Location) TableName() string { return "store_location" }

func (l *Location) draft(label string) progress.Shipping {
	lat, lng := l.Lat, l.Lng
	return progress.Shipping{Label: label, Address: l.AddressLine, City: l.City, Pin: l.Pincode, Lat: &lat, Lng: &lng}
}

type Media struct {
	ID        uuid.UUID `json:"id" gorm:"type:uuid;primaryKey"`
	StoreID   uuid.UUID `json:"store_id" gorm:"type:uuid"`
	MediaURL  string    `json:"media_url"`
	Type      string    `json:"type"`
	SortOrder int       `json:"sort_order"`
	IsCover   bool      `json:"is_cover"`
}

func (Media) TableName() string { return "store_media" }

// Bank is where a store's payouts go. Each store has its own account or QR.
type Bank struct {
	StoreID           uuid.UUID `gorm:"type:uuid;primaryKey"`
	Method            string
	AccountHolderName string
	AccountNumber     string
	IFSC              string `gorm:"column:ifsc"`
	BankName          string
	QRURL             string `gorm:"column:qr_url"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (Bank) TableName() string { return "store_bank_details" }

func (b *Bank) draft() progress.Bank {
	if b == nil {
		return progress.Bank{}
	}
	return progress.Bank{
		Method: b.Method,
		Holder: b.AccountHolderName,
		Number: b.AccountNumber,
		IFSC:   b.IFSC,
		Bank:   b.BankName,
		QRURL:  b.QRURL,
	}
}

// OnboardingSummary is the store's setup state as judged by the server.
type OnboardingSummary struct {
	Status         string     `json:"status"` // progress.StatusDraft or progress.StatusCompleted
	CurrentStep    string     `json:"current_step"`
	CompletedSteps int        `json:"completed_steps"`
	TotalSteps     int        `json:"total_steps"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

type Summary struct {
	Store
	Images         []string          `json:"images" gorm:"-"`
	ProductCount   int               `json:"product_count" gorm:"-"`
	TotalInventory int               `json:"total_inventory" gorm:"-"`
	LowStockCount  int               `json:"low_stock_count" gorm:"-"`
	Onboarding     OnboardingSummary `json:"onboarding" gorm:"-"`
	// Access is the caller's role and permissions in this store.
	Access *storeaccess.Access `json:"access,omitempty" gorm:"-"`
}

// CreateRequest starts a new store in DRAFT. Onboarding is optional form data
// captured before the store existed; the server re-evaluates it. Name may be
// blank while setup has just started.
type CreateRequest struct {
	Name        string          `json:"name" validate:"max=255"`
	Description string          `json:"description" validate:"max=2000"`
	Images      []string        `json:"images" validate:"max=20,dive,required,max=2048"`
	Onboarding  *progress.Draft `json:"onboarding"`
}

type UpdateRequest struct {
	Name        *string   `json:"name" validate:"omitnil,min=2,max=255"`
	Description *string   `json:"description" validate:"omitnil,max=2000"`
	Images      *[]string `json:"images" validate:"omitnil,max=20,dive,required,max=2048"`
}

type SaveOnboardingRequest struct {
	Data progress.Draft `json:"data"`
}

// SaveLocationRequest sets the store's address and map pin. Lat and Lng are
// pointers so a missing value isn't mistaken for 0.
type SaveLocationRequest struct {
	AddressLine string   `json:"address_line" validate:"required,max=255"`
	City        string   `json:"city" validate:"required,max=255"`
	Pincode     string   `json:"pincode" validate:"required,len=6,number"`
	Lat         *float64 `json:"lat" validate:"required,min=-90,max=90"`
	Lng         *float64 `json:"lng" validate:"required,min=-180,max=180"`
	// ServiceAreaRadiusKm keeps its current value (or the default) when omitted.
	ServiceAreaRadiusKm int `json:"service_area_radius_km" validate:"omitempty,min=1,max=50"`
}

// SaveBankRequest uses the same field names as the onboarding draft's bank section.
type SaveBankRequest struct {
	Method string `json:"method" validate:"required,oneof=bank qr"`
	Holder string `json:"holder" validate:"max=255"`
	Number string `json:"number" validate:"max=255"`
	IFSC   string `json:"ifsc" validate:"max=255"`
	Bank   string `json:"bank" validate:"max=255"`
	QRURL  string `json:"qr_url" validate:"omitempty,url,max=2048"`
}
