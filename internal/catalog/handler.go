package catalog

import (
	"net/http"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/httputil"
	"github.com/rs/zerolog/log"
)

type Handler struct {
	svc Service
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.List(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("failed to list categories")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to load categories")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, rows)
}
