package truecaller

import "github.com/go-chi/chi/v5"

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/start", h.Start)
	r.Post("/callback", h.Callback)
	r.Get("/status", h.Status)
	return r
}
