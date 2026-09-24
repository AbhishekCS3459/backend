package retailer

import (
	"errors"
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

func (h *Handler) SaveProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req UpsertProfileRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := httputil.ValidateStruct(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	profile, err := h.svc.SaveProfile(r.Context(), userID, &req)
	if err != nil {
		h.writeError(w, err, userID.String(), "failed to save retailer profile")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, profile)
}

func (h *Handler) SaveKYC(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req UpsertKYCRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := httputil.ValidateStruct(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	profile, err := h.svc.SaveKYC(r.Context(), userID, &req)
	if err != nil {
		h.writeError(w, err, userID.String(), "failed to save retailer kyc")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, profile)
}

func (h *Handler) SaveBank(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req UpsertBankRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := httputil.ValidateStruct(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	profile, err := h.svc.SaveBank(r.Context(), userID, &req)
	if err != nil {
		h.writeError(w, err, userID.String(), "failed to save bank details")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, profile)
}

func (h *Handler) writeError(w http.ResponseWriter, err error, userID, fallback string) {
	if errors.Is(err, ErrNotFound) {
		httputil.WriteError(w, http.StatusNotFound, ErrNotFound.Error())
		return
	}
	if errors.Is(err, ErrInvalidPayout) {
		httputil.WriteError(w, http.StatusBadRequest, ErrInvalidPayout.Error())
		return
	}
	log.Error().Err(err).Str("user_id", userID).Msg(fallback)
	httputil.WriteError(w, http.StatusInternalServerError, fallback)
}
