package listproducts

import "github.com/go-chi/chi/v5"

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Get("/catalog", h.Catalog)
	r.Post("/", h.Add)
	r.Post("/create", h.Create)
	r.Patch("/", h.BulkUpdate)
	r.Patch("/{variantID}", h.Update)
	r.Delete("/{variantID}", h.Remove)
	return r
}
