package health

import "github.com/go-chi/chi/v5"

// Routes registers health endpoints.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/health", h.Health)
	return r
}
