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
	userID, storeID, ok := storeRequest(w, r)
	if !ok {
		return
	}
	row, err := h.svc.Get(r.Context(), userID, storeID)
	if err != nil {
		writeServiceError(w, err, userID, "failed to load store")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, row)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	userID, storeID, ok := storeRequest(w, r)
	if !ok {
		return
	}
	var req UpdateRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := httputil.ValidateStruct(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	row, err := h.svc.Update(r.Context(), userID, storeID, &req)
	if err != nil {
		writeServiceError(w, err, userID, "failed to update store")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, row)
}

func (h *Handler) NewOnboarding(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	view, err := h.svc.NewOnboarding(r.Context(), userID)
	if err != nil {
		writeServiceError(w, err, userID, "failed to load store setup")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, view)
}

func (h *Handler) Onboarding(w http.ResponseWriter, r *http.Request) {
	userID, storeID, ok := storeRequest(w, r)
	if !ok {
		return
	}
	view, err := h.svc.Onboarding(r.Context(), userID, storeID)
	if err != nil {
		writeServiceError(w, err, userID, "failed to load store setup")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, view)
}

func (h *Handler) SaveOnboarding(w http.ResponseWriter, r *http.Request) {
	userID, storeID, ok := storeRequest(w, r)
	if !ok {
		return
	}
	var req SaveOnboardingRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	view, err := h.svc.SaveOnboarding(r.Context(), userID, storeID, req.Data)
	if err != nil {
		writeServiceError(w, err, userID, "failed to save store setup")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, view)
}

func (h *Handler) SaveBank(w http.ResponseWriter, r *http.Request) {
	userID, storeID, ok := storeRequest(w, r)
	if !ok {
		return
	}
	var req SaveBankRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := httputil.ValidateStruct(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	view, err := h.svc.SaveBank(r.Context(), userID, storeID, &req)
	if err != nil {
		writeServiceError(w, err, userID, "failed to save store bank details")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, view)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, storeID, ok := storeRequest(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), userID, storeID); err != nil {
		writeServiceError(w, err, userID, "failed to delete store")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func storeRequest(w http.ResponseWriter, r *http.Request) (userID, storeID uuid.UUID, ok bool) {
	userID, ok = middleware.GetUserID(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "authentication required")
		return uuid.Nil, uuid.Nil, false
	}
	storeID, err := uuid.Parse(chi.URLParam(r, "storeID"))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid store id")
		return uuid.Nil, uuid.Nil, false
	}
	return userID, storeID, true
}

func writeServiceError(w http.ResponseWriter, err error, userID uuid.UUID, fallback string) {
	var incomplete *IncompleteError
	switch {
	case errors.Is(err, ErrNotFound):
		httputil.WriteError(w, http.StatusNotFound, "store not found")
	case errors.Is(err, ErrNotOwner):
		httputil.WriteError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrOnboardingComplete):
		httputil.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrNameTooLong), errors.Is(err, ErrBankIncomplete):
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.As(err, &incomplete):
		httputil.WriteError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		log.Error().Err(err).Str("user_id", userID.String()).Msg(fallback)
		httputil.WriteError(w, http.StatusInternalServerError, fallback)
	}
}
