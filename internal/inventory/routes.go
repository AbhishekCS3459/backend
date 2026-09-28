package inventory

import "github.com/go-chi/chi/v5"

// Routes is mounted at /api/stores/{storeID}/inventory.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/receive", h.ReceiveBatch)
	r.Get("/{variantID}", h.Get)
	r.Post("/{variantID}/receive", h.Receive)
	r.Post("/{variantID}/adjust", h.Adjust)
	r.Get("/{variantID}/history", h.History)
	return r
}
