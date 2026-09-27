package team

import (
	"errors"
	"net/http"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/httputil"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/middleware"
	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
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

func (h *Handler) Permissions(w http.ResponseWriter, _ *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, storeaccess.GetCatalogue())
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	callerID, ok := caller(w, r)
	if !ok {
		return
	}
	var storeID *uuid.UUID
	if raw := r.URL.Query().Get("store_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "invalid store id")
			return
		}
		storeID = &id
	}
	members, err := h.svc.List(r.Context(), callerID, storeID)
	if err != nil {
		writeErr(w, err, callerID, "failed to load team")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, members)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	callerID, ok := caller(w, r)
	if !ok {
		return
	}
	var req CreateMemberRequest
	if !decode(w, r, &req) {
		return
	}
	res, err := h.svc.Create(r.Context(), callerID, &req)
	if err != nil {
		writeErr(w, err, callerID, "failed to add team member")
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	callerID, memberID, ok := callerAndMember(w, r)
	if !ok {
		return
	}
	var req UpdateMemberRequest
	if !decode(w, r, &req) {
		return
	}
	member, err := h.svc.Update(r.Context(), callerID, memberID, &req)
	if err != nil {
		writeErr(w, err, callerID, "failed to update team member")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, member)
}

func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	callerID, memberID, ok := callerAndMember(w, r)
	if !ok {
		return
	}
	var req ResetPasswordRequest
	if !decode(w, r, &req) {
		return
	}
	if err := h.svc.ResetPassword(r.Context(), callerID, memberID, req.TemporaryPassword); err != nil {
		writeErr(w, err, callerID, "failed to reset password")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Remove(w http.ResponseWriter, r *http.Request) {
	callerID, memberID, ok := callerAndMember(w, r)
	if !ok {
		return
	}
	if err := h.svc.Remove(r.Context(), callerID, memberID); err != nil {
		writeErr(w, err, callerID, "failed to remove team member")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func caller(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := middleware.GetUserID(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "authentication required")
	}
	return id, ok
}

func callerAndMember(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	callerID, ok := caller(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	memberID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid team member id")
		return uuid.Nil, uuid.Nil, false
	}
	return callerID, memberID, true
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := httputil.DecodeJSON(r, dst); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	if err := httputil.ValidateStruct(dst); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

func writeErr(w http.ResponseWriter, err error, callerID uuid.UUID, msg string) {
	var validation *ValidationError
	switch {
	case errors.As(err, &validation):
		httputil.WriteError(w, http.StatusBadRequest, validation.Message)
	case errors.Is(err, ErrNotFound):
		httputil.WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrNoTeamAccess), errors.Is(err, ErrSelf):
		httputil.WriteError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrAccountInUse), errors.Is(err, ErrAlreadyMember):
		httputil.WriteError(w, http.StatusConflict, err.Error())
	default:
		log.Error().Err(err).Str("user_id", callerID.String()).Msg(msg)
		httputil.WriteError(w, http.StatusInternalServerError, msg)
	}
}
