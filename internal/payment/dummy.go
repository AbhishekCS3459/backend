package payment

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
)

// DummyName is the dummy provider's name in payment records.
const DummyName = "dummy"

// Dummy takes no money. Payments succeed or fail when the customer's client
// asks for it through Simulate; refunds always succeed. It accepts no webhooks
// from the internet, since anyone could forge them.
type Dummy struct{}

func NewDummy() *Dummy { return &Dummy{} }

func (*Dummy) Name() string { return DummyName }

func (*Dummy) CreatePayment(context.Context, CreateRequest) (string, error) {
	return "dummy_" + uuid.NewString(), nil
}

func (*Dummy) Checkout(req CheckoutRequest) map[string]any {
	return map[string]any{
		"provider":            DummyName,
		"provider_payment_id": req.ProviderPaymentID,
		"amount_paise":        req.AmountPaise,
		"currency":            req.Currency,
		"simulated":           true,
	}
}

func (*Dummy) ParseWebhook(http.Header, []byte) (Event, error) {
	return Event{}, ErrWebhooksUnsupported
}

func (*Dummy) Refund(context.Context, RefundRequest) error { return nil }

func (*Dummy) Simulate(providerPaymentID string, amountPaise int64, outcome Outcome) Event {
	id := "dummy_evt_" + uuid.NewString()
	raw, _ := json.Marshal(map[string]any{ //nolint:errchkjson // plain strings and numbers always encode
		"id": id, "provider_payment_id": providerPaymentID, "outcome": outcome, "amount_paise": amountPaise,
	})
	return Event{ID: id, ProviderPaymentID: providerPaymentID, Outcome: outcome, AmountPaise: amountPaise, Raw: raw}
}
