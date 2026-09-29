package stores

import "github.com/go-chi/chi/v5"

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Post("/", h.Create)
	r.Get("/onboarding", h.NewOnboarding)
	r.Get("/{storeID}", h.Get)
	r.Patch("/{storeID}", h.Update)
	r.Delete("/{storeID}", h.Delete)
	r.Get("/{storeID}/onboarding", h.Onboarding)
	r.Put("/{storeID}/onboarding", h.SaveOnboarding)
	r.Put("/{storeID}/bank", h.SaveBank)
	return r
}
