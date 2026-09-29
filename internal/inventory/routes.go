package inventory

import "github.com/go-chi/chi/v5"

// Routes is mounted at /api/stores/{storeID}/inventory.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/receive", h.ReceiveBatch)
	r.Post("/sell", h.SellBatch)
	r.Get("/{variantID}", h.Get)
	r.Post("/{variantID}/receive", h.Receive)
	r.Post("/{variantID}/sell", h.Sell)
	r.Post("/{variantID}/count", h.Count)
	r.Post("/{variantID}/adjust", h.Adjust)
	r.Get("/{variantID}/history", h.History)
	return r
}
