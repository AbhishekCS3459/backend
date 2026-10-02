package marketplace

import "github.com/go-chi/chi/v5"

// Routes are public: customers search without an account.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/search", h.Search)
	r.Get("/nearby", h.Nearby)
	r.Get("/nearby/products", h.NearbyProducts)
	r.Get("/nearby/stores", h.NearbyStores)
	r.Get("/products/{catalogKey}", h.Product)
	r.Get("/stores/{id}", h.Store)
	r.Get("/stores/{id}/products", h.StoreProducts)
	r.Get("/stores/{id}/products/{catalogKey}", h.StoreProduct)
	return r
}
