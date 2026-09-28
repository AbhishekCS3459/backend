package stores

import (
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/identity/progress"
	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/google/uuid"
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
// captured before the store existed; the server re-evaluates it.
type CreateRequest struct {
	Name        string          `json:"name" validate:"required,min=2,max=255"`
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
