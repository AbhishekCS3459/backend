package team

import "github.com/go-chi/chi/v5"

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/permissions", h.Permissions)
	r.Get("/members", h.List)
	r.Post("/members", h.Create)
	r.Put("/members/{userID}", h.Update)
	r.Post("/members/{userID}/reset-password", h.ResetPassword)
	r.Delete("/members/{userID}", h.Remove)
	return r
}
