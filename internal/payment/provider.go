// Package payment talks to payment gateways. A Provider creates a payment for
// an order, reads the gateway's webhooks and issues refunds; it never touches
// the database. The orders package owns payment records and decides what a
// payment means for an order.
package payment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// ErrWebhooksUnsupported means the provider takes no webhooks from the internet.
var ErrWebhooksUnsupported = errors.New("payment provider does not accept webhooks")

// ErrInvalidWebhook means a webhook failed signature or format checks.
var ErrInvalidWebhook = errors.New("invalid payment webhook")

// Outcome is what a gateway reports about a payment.
type Outcome string

const (
	Succeeded Outcome = "SUCCEEDED"
	Failed    Outcome = "FAILED"
)

// CreateRequest asks the gateway to collect an order's total.
type CreateRequest struct {
	// PaymentID is our payment record's ID; gateways use it as their
	// idempotency key / receipt so a retried create doesn't charge twice.
	PaymentID   string
	OrderID     string
	OrderCode   string
	AmountPaise int64
	Currency    string
}

// CheckoutRequest identifies a created payment for building the client's checkout.
type CheckoutRequest struct {
	PaymentID         string
	ProviderPaymentID string
	AmountPaise       int64
	Currency          string
}

// RefundRequest returns a succeeded payment's amount to the customer.
type RefundRequest struct {
	// PaymentID doubles as the refund's idempotency key: retrying a refund
	// that already went through must not refund twice.
	PaymentID         string
	ProviderPaymentID string
	AmountPaise       int64
}

// Event is one gateway notification about a payment. Gateways deliver events
// at least once, so ID must identify the event, not the delivery.
type Event struct {
	ID                string
	ProviderPaymentID string
	Outcome           Outcome
	AmountPaise       int64
	Raw               json.RawMessage
}

// Provider is one payment gateway.
type Provider interface {
	Name() string
	// CreatePayment registers a payment with the gateway and returns its ID there.
	CreatePayment(ctx context.Context, req CreateRequest) (providerPaymentID string, err error)
	// Checkout is what the client needs to let the customer pay (keys, gateway order ID, ...).
	Checkout(req CheckoutRequest) map[string]any
	// ParseWebhook verifies a webhook delivery and reads its event.
	ParseWebhook(header http.Header, body []byte) (Event, error)
	Refund(ctx context.Context, req RefundRequest) error
}

// Simulator is implemented by providers that only pretend to take money. It
// produces the event a real gateway would send, so a simulated payment runs
// the same code as a real webhook.
type Simulator interface {
	Simulate(providerPaymentID string, amountPaise int64, outcome Outcome) Event
}
