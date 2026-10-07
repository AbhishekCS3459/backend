// Package orders takes customer orders for pickup at a store. Placing an order
// reserves its units through the inventory ledger in the same transaction, so
// two customers can never be promised the same unit, and every later status
// change either keeps, releases or hands over those units in its own
// transaction. Every change is guarded by the order's current status, so
// racing requests (a payment and an expiry, two cancels) can't both win.
package orders

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	// StatusPendingPayment holds the stock while the customer pays online.
	StatusPendingPayment Status = "PENDING_PAYMENT"
	// StatusPlaced waits for the store to accept: paid online, or to be paid at pickup.
	StatusPlaced    Status = "PLACED"
	StatusAccepted  Status = "ACCEPTED"
	StatusReady     Status = "READY"
	StatusCompleted Status = "COMPLETED"
	StatusCancelled Status = "CANCELLED"
	StatusRejected  Status = "REJECTED"
	// StatusExpired: the customer didn't pay within the hold.
	StatusExpired Status = "EXPIRED"
	// StatusNoShow: the customer didn't collect a ready order in time.
	StatusNoShow Status = "NO_SHOW"
)

// Open reports whether the order still holds stock.
func (s Status) Open() bool {
	switch s {
	case StatusPendingPayment, StatusPlaced, StatusAccepted, StatusReady:
		return true
	}
	return false
}

// openStatuses lists, as SQL, the statuses that hold stock.
const openStatuses = `'PENDING_PAYMENT', 'PLACED', 'ACCEPTED', 'READY'`

type PaymentMode string

const (
	PaymentOnline     PaymentMode = "ONLINE"
	PaymentPayAtStore PaymentMode = "PAY_AT_STORE"
)

type PaymentStatus string

const (
	PaymentUnpaid        PaymentStatus = "UNPAID"
	PaymentPaid          PaymentStatus = "PAID"
	PaymentRefundPending PaymentStatus = "REFUND_PENDING"
	PaymentRefunded      PaymentStatus = "REFUNDED"
)

// Actor is who changed an order's status.
type Actor string

const (
	ActorCustomer Actor = "CUSTOMER"
	ActorRetailer Actor = "RETAILER"
	// ActorSystem is a deadline passing.
	ActorSystem Actor = "SYSTEM"
	// ActorPayment is the payment provider reporting a payment.
	ActorPayment Actor = "PAYMENT"
)

// Payment record statuses.
const (
	paymentCreated       = "CREATED"
	paymentSucceeded     = "SUCCEEDED"
	paymentFailed        = "FAILED"
	paymentRefundPending = "REFUND_PENDING"
	paymentRefunded      = "REFUNDED"
)

const currencyINR = "INR"

type Order struct {
	ID            uuid.UUID     `json:"id"`
	Code          string        `json:"code"`
	StoreID       uuid.UUID     `json:"store_id"`
	StoreName     string        `json:"store_name"`
	Status        Status        `json:"status"`
	PaymentMode   PaymentMode   `json:"payment_mode"`
	PaymentStatus PaymentStatus `json:"payment_status"`
	TotalPaise    int64         `json:"total_paise"`
	// ExpiresAt is the deadline of the current status: paying, the store
	// accepting, or collecting. Nil while the store prepares the order.
	ExpiresAt    *time.Time `json:"expires_at"`
	AcceptedAt   *time.Time `json:"accepted_at"`
	ReadyAt      *time.Time `json:"ready_at"`
	CompletedAt  *time.Time `json:"completed_at"`
	ClosedReason *string    `json:"closed_reason"`
	Version      int        `json:"version"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	Items        []Item     `json:"items"`
	// PickupCode is shown only to the customer, while the order can still be collected.
	PickupCode string `json:"pickup_code,omitempty"`
	// Customer is shown only to the store.
	Customer *Customer `json:"customer,omitempty"`
	// Payment is the latest online payment attempt.
	Payment *Payment `json:"payment,omitempty"`
	History []Event  `json:"history,omitempty"`
}

type Item struct {
	ID             uuid.UUID `json:"id"`
	VariantID      uuid.UUID `json:"variant_id"`
	CatalogKey     string    `json:"catalog_key"`
	Name           string    `json:"name"`
	Unit           string    `json:"unit"`
	Quantity       int       `json:"quantity"`
	UnitPricePaise int64     `json:"unit_price_paise"`
	LineTotalPaise int64     `json:"line_total_paise"`
}

type Customer struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

type Event struct {
	From   *Status   `json:"from"`
	To     Status    `json:"to"`
	Actor  Actor     `json:"actor"`
	Reason *string   `json:"reason"`
	At     time.Time `json:"at"`
}

type Payment struct {
	ID          uuid.UUID `json:"id"`
	Provider    string    `json:"provider"`
	Status      string    `json:"status"`
	AmountPaise int64     `json:"amount_paise"`
	CreatedAt   time.Time `json:"created_at"`
	// Checkout is what the client needs to let the customer pay; only while the payment is open.
	Checkout map[string]any `json:"checkout,omitempty"`
	// Simulated payments are completed with the simulate endpoint instead of a gateway.
	Simulated bool `json:"simulated"`
}

type Page struct {
	Orders     []Order `json:"orders"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

// PlaceRequest is a customer's order at one store.
type PlaceRequest struct {
	StoreID     uuid.UUID   `json:"store_id" validate:"required"`
	PaymentMode PaymentMode `json:"payment_mode" validate:"required,oneof=ONLINE PAY_AT_STORE"`
	Items       []PlaceItem `json:"items" validate:"required,min=1,max=50,dive"`
}

type PlaceItem struct {
	CatalogKey string `json:"catalog_key" validate:"required,max=200"`
	// Quantity is at most availability.MaxOrderQuantity.
	Quantity int `json:"quantity" validate:"required,gt=0"`
	// ExpectedUnitPrice is the price the customer saw, in rupees. When set and
	// the store's price differs, nothing is reserved and PRICE_CHANGED returns the new price.
	ExpectedUnitPrice *float64 `json:"expected_unit_price" validate:"omitempty,gt=0"`
}

type CancelRequest struct {
	Reason string `json:"reason" validate:"max=500"`
}

type RejectRequest struct {
	Reason string `json:"reason" validate:"required,min=3,max=500"`
}

// CompleteRequest hands an order over: with the customer's pickup code, or
// without it and a reason, which is kept in the order's history.
type CompleteRequest struct {
	PickupCode     string `json:"pickup_code" validate:"omitempty,len=6,numeric"`
	OverrideReason string `json:"override_reason" validate:"omitempty,min=5,max=500"`
}

type SimulateRequest struct {
	Outcome string `json:"outcome" validate:"required,oneof=SUCCEEDED FAILED"`
}

var (
	ErrNotFound  = errors.New("order not found")
	ErrKeyReused = errors.New("this idempotency key was already used for a different request")
	// ErrStoreUnavailable: the store is closed, paused or not visible to customers.
	ErrStoreUnavailable = errors.New("this store isn't taking orders right now")
	ErrTooManyOpen      = errors.New("you have too many open orders; collect or cancel one first")
	ErrNotOnlinePayment = errors.New("this order is paid at the store")
	ErrPaymentNotFound  = errors.New("payment not found")
	ErrNotSimulated     = errors.New("payments here go through a real gateway")
	// ErrPickupLocked: too many wrong pickup codes; the store hands over with a reason instead.
	ErrPickupLocked = errors.New("too many wrong pickup codes; hand over with a reason instead")
	// ErrProvider is a payment gateway failure (502).
	ErrProvider = errors.New("the payment provider could not be reached; try again")
)

// ValidationError is a malformed request (400).
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// StateError means the order's status doesn't allow the action (409).
type StateError struct {
	Status Status
	Action string
}

func (e *StateError) Error() string {
	return fmt.Sprintf("can't %s an order that is %s", e.Action, e.Status)
}

// Item problem reasons.
const (
	ProblemNotSoldHere  = "NOT_SOLD_HERE"
	ProblemUnavailable  = "UNAVAILABLE"
	ProblemOutOfStock   = "OUT_OF_STOCK"
	ProblemPriceChanged = "PRICE_CHANGED"
)

// ItemProblem is why one line can't be ordered. Like the marketplace, it never
// reveals more stock than availability.MaxOrderQuantity.
type ItemProblem struct {
	CatalogKey       string   `json:"catalog_key"`
	Reason           string   `json:"reason"`
	CurrentUnitPrice *float64 `json:"current_unit_price,omitempty"`
	// MaxOrderQuantity is set with OUT_OF_STOCK: how many can be ordered now, possibly 0.
	MaxOrderQuantity *int `json:"max_order_quantity,omitempty"`
}

// ItemsError lists every line that can't be ordered as asked (409); nothing was reserved.
type ItemsError struct {
	Code    string
	Message string
	Items   []ItemProblem
}

func (e *ItemsError) Error() string { return e.Message }

// PickupCodeError is a wrong pickup code (422).
type PickupCodeError struct{ AttemptsLeft int }

func (e *PickupCodeError) Error() string {
	return fmt.Sprintf("wrong pickup code; %d attempts left", e.AttemptsLeft)
}
