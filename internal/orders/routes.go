package orders

import "github.com/go-chi/chi/v5"

// CustomerRoutes is mounted at /api/orders.
func (h *Handler) CustomerRoutes() chi.Router {
	r := chi.NewRouter()
	r.Post("/", h.Place)
	r.Get("/", h.List)
	r.Get("/{orderID}", h.Get)
	r.Post("/{orderID}/cancel", h.Cancel)
	r.Post("/{orderID}/payments", h.StartPayment)
	r.Post("/{orderID}/payments/{paymentID}/simulate", h.Simulate)
	return r
}

// StoreRoutes is mounted at /api/stores/{storeID}/orders.
func (h *Handler) StoreRoutes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.StoreList)
	r.Get("/events", h.Events)
	r.Get("/{orderID}", h.StoreGet)
	r.Post("/{orderID}/accept", h.Accept)
	r.Post("/{orderID}/reject", h.Reject)
	r.Post("/{orderID}/ready", h.Ready)
	r.Post("/{orderID}/complete", h.Complete)
	return r
}

// WebhookRoutes is mounted at /api/payments/webhooks; it needs no login.
func (h *Handler) WebhookRoutes() chi.Router {
	r := chi.NewRouter()
	r.Post("/{provider}", h.Webhook)
	return r
}
