package productcatalog

import "github.com/go-chi/chi/v5"

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/filters", h.Filters)
	r.Get("/search", h.Search)
	r.Get("/autocomplete", h.Autocomplete)
	r.Get("/products", h.List)
	r.Get("/products/{id}", h.Get)
	return r
}
