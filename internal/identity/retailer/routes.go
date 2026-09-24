package retailer

import "github.com/go-chi/chi/v5"

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Put("/me", h.SaveProfile)
	r.Put("/me/kyc", h.SaveKYC)
	r.Put("/me/bank", h.SaveBank)
	return r
}
