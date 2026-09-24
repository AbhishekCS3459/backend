package progress

import (
	"net/http"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/httputil"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/middleware"
	"github.com/rs/zerolog/log"
)

type Handler struct {
	svc Service
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	view, err := h.svc.Get(r.Context(), userID)
	if err != nil {
		log.Error().Err(err).Str("user_id", userID.String()).Msg("failed to load user progress")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to load progress")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, view)
}

func (h *Handler) Save(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req SaveRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	view, err := h.svc.Save(r.Context(), userID, req.Data)
	if err != nil {
		log.Error().Err(err).Str("user_id", userID.String()).Msg("failed to save user progress")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to save progress")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, view)
}
