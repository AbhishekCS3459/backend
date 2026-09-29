// Package inventory owns store stock. It is the only code that writes the
// inventory table, and every stock change it makes is recorded as an
// inventory_transaction in the same database transaction.
package inventory

import (
	"errors"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/availability"
	"github.com/google/uuid"
)

type TxType string

const (
	TypeOpeningBalance TxType = "OPENING_BALANCE"
	TypeStockReceived  TxType = "STOCK_RECEIVED"
	TypeAdjustment     TxType = "ADJUSTMENT"
	TypeOfflineSale    TxType = "OFFLINE_SALE"
)

type Reason string

const (
	ReasonDamaged          Reason = "DAMAGED"
	ReasonExpired          Reason = "EXPIRED"
	ReasonLost             Reason = "LOST"
	ReasonStockCount       Reason = "STOCK_COUNT"
	ReasonReturnToSupplier Reason = "RETURN_TO_SUPPLIER"
	ReasonOther            Reason = "OTHER"
)

// changeReasons are the reasons a CHANGE adjustment may use. STOCK_COUNT is
// reserved for COUNT adjustments so the counted quantity is always recorded.
var changeReasons = map[Reason]bool{
	ReasonDamaged:          true,
	ReasonExpired:          true,
	ReasonLost:             true,
	ReasonReturnToSupplier: true,
	ReasonOther:            true,
}

type AdjustMode string

const (
	ModeChange AdjustMode = "CHANGE"
	ModeCount  AdjustMode = "COUNT"
)

const (
	// MaxChange bounds a single receive or adjustment.
	MaxChange = 1_000_000
	// maxOnHand keeps on_hand_quantity well inside PostgreSQL INTEGER.
	maxOnHand = 1_000_000_000
)

var (
	ErrNotFound      = errors.New("product is not in this store")
	ErrUnlisted      = errors.New("this product was removed from the store; add it back before changing its stock")
	ErrAlreadyListed = errors.New("product is already listed in this store")
	ErrKeyReused     = errors.New("this idempotency key was already used for a different request")
)

// ValidationError is a malformed request (400).
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// StockError is a well-formed request that the current stock does not allow (409).
type StockError struct{ Message string }

func (e *StockError) Error() string { return e.Message }

// Stock is the quantity state of one inventory row.
type Stock struct {
	OnHand   int
	Reserved int
}

func (s Stock) Available() int { return s.OnHand - s.Reserved }

type Item struct {
	InventoryID       uuid.UUID `json:"inventory_id"`
	StoreID           uuid.UUID `json:"store_id"`
	VariantID         uuid.UUID `json:"variant_id"`
	OnHand            int       `json:"on_hand"`
	Reserved          int       `json:"reserved"`
	Available         int       `json:"available"`
	LowStockThreshold int       `json:"low_stock_threshold"`
	IsAvailable       bool      `json:"is_available"`
	Listed            bool      `json:"listed"`
	StockStatus       string    `json:"stock_status"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Actor struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type Transaction struct {
	ID              uuid.UUID  `json:"id"`
	Type            TxType     `json:"type"`
	Reason          *Reason    `json:"reason"`
	Quantity        int        `json:"quantity"`
	BeforeOnHand    int        `json:"before_on_hand"`
	AfterOnHand     int        `json:"after_on_hand"`
	BeforeReserved  int        `json:"before_reserved"`
	AfterReserved   int        `json:"after_reserved"`
	CountedQuantity *int       `json:"counted_quantity"`
	Reference       *string    `json:"reference"`
	Note            *string    `json:"note"`
	BatchID         *uuid.UUID `json:"batch_id"`
	CreatedBy       *Actor     `json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
}

type Result struct {
	Inventory   Item        `json:"inventory"`
	Transaction Transaction `json:"transaction"`
}

type BatchResult struct {
	BatchID   uuid.UUID `json:"batch_id"`
	Reference string    `json:"reference,omitempty"`
	Items     []Result  `json:"items"`
}

type HistoryPage struct {
	Transactions []Transaction `json:"transactions"`
	NextCursor   string        `json:"next_cursor,omitempty"`
}

type ReceiveRequest struct {
	Quantity  int    `json:"quantity" validate:"required,gt=0,max=1000000"`
	Reference string `json:"reference" validate:"max=100"`
	Note      string `json:"note" validate:"max=500"`
}

// SellRequest records units sold over the counter.
type SellRequest struct {
	Quantity  int    `json:"quantity" validate:"required,gt=0,max=1000000"`
	Reference string `json:"reference" validate:"max=100"`
	Note      string `json:"note" validate:"max=500"`
}

// CountRequest sets stock to the physically counted quantity.
type CountRequest struct {
	CountedQuantity *int   `json:"counted_quantity"`
	Note            string `json:"note" validate:"max=500"`
}

type BatchItem struct {
	VariantID uuid.UUID `json:"variant_id" validate:"required"`
	Quantity  int       `json:"quantity" validate:"required,gt=0,max=1000000"`
}

// BatchRequest is a delivery (receive) or a counter bill (sell) covering
// several products.
type BatchRequest struct {
	Reference string      `json:"reference" validate:"max=100"`
	Note      string      `json:"note" validate:"max=500"`
	Items     []BatchItem `json:"items" validate:"required,min=1,max=200,dive"`
}

// AdjustRequest is either a CHANGE (signed quantity with a reason) or a COUNT
// (the physically counted quantity; the server works out the difference).
type AdjustRequest struct {
	Mode            AdjustMode `json:"mode" validate:"required,oneof=CHANGE COUNT"`
	Quantity        *int       `json:"quantity"`
	Reason          Reason     `json:"reason"`
	CountedQuantity *int       `json:"counted_quantity"`
	Note            string     `json:"note" validate:"max=500"`
}

// ListInput adds a product to a store, or relists one that was removed.
type ListInput struct {
	StoreID           uuid.UUID
	VariantID         uuid.UUID
	LowStockThreshold int
	IsAvailable       bool
}

// Settings are listing options that do not change stock.
type Settings struct {
	LowStockThreshold *int
	IsAvailable       *bool
}

// StockStatus is shared with listings so every screen classifies stock the same way.
func StockStatus(stock Stock, threshold int, isAvailable, listed bool) string {
	switch {
	case !listed:
		return "unlisted"
	case !isAvailable:
		return "unavailable"
	}
	switch availability.BucketFor(stock.Available(), threshold) {
	case availability.Out:
		return "out"
	case availability.Low:
		return "low"
	default:
		return "in_stock"
	}
}
