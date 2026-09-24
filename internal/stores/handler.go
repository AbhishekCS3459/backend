package stores

import (
	"errors"
	"net/http"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/httputil"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type Handler struct {
	svc Service
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rows, err := h.svc.ListMine(r.Context(), userID)
	if err != nil {
		log.Error().Err(err).Str("user_id", userID.String()).Msg("failed to list stores")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to load stores")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, rows)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req CreateRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := httputil.ValidateStruct(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	row, err := h.svc.Create(r.Context(), userID, &req)
	if err != nil {
		if errors.Is(err, ErrNoRetailer) {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		log.Error().Err(err).Str("user_id", userID.String()).Msg("failed to create store")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create store")
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, row)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	storeID, err := uuid.Parse(chi.URLParam(r, "storeID"))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid store id")
		return
	}
	row, err := h.svc.GetOwned(r.Context(), userID, storeID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httputil.WriteError(w, http.StatusNotFound, "store not found")
			return
		}
		log.Error().Err(err).Str("user_id", userID.String()).Msg("failed to get store")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to load store")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, row)
}
