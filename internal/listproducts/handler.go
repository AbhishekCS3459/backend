package listproducts

import (
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
	userID, storeID, ok := h.ids(w, r)
	if !ok {
		return
	}
	rows, stats, err := h.svc.List(r.Context(), userID, storeID, r.URL.Query().Get("q"), r.URL.Query().Get("status"))
	if err != nil {
		h.writeErr(w, err, userID.String(), "failed to list store products")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"products": rows,
		"summary":  stats,
	})
}

func (h *Handler) Catalog(w http.ResponseWriter, r *http.Request) {
	userID, storeID, ok := h.ids(w, r)
	if !ok {
		return
	}
	rows, err := h.svc.Catalog(r.Context(), userID, storeID, r.URL.Query().Get("q"))
	if err != nil {
		h.writeErr(w, err, userID.String(), "failed to list catalog")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, rows)
}

func (h *Handler) Add(w http.ResponseWriter, r *http.Request) {
	userID, storeID, ok := h.ids(w, r)
	if !ok {
		return
	}
	var req AddRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := httputil.ValidateStruct(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	row, err := h.svc.Add(r.Context(), userID, storeID, &req)
	if err != nil {
		h.writeErr(w, err, userID.String(), "failed to add product")
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, row)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, storeID, ok := h.ids(w, r)
	if !ok {
		return
	}
	var req CreateProductRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := httputil.ValidateStruct(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	row, err := h.svc.Create(r.Context(), userID, storeID, &req)
	if err != nil {
		h.writeErr(w, err, userID.String(), "failed to create product")
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, row)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	userID, storeID, ok := h.ids(w, r)
	if !ok {
		return
	}
	variantID, err := uuid.Parse(chi.URLParam(r, "variantID"))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid variant id")
		return
	}
	var req UpdateRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	row, err := h.svc.Update(r.Context(), userID, storeID, variantID, &req)
	if err != nil {
		h.writeErr(w, err, userID.String(), "failed to update inventory")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, row)
}

func (h *Handler) BulkUpdate(w http.ResponseWriter, r *http.Request) {
	userID, storeID, ok := h.ids(w, r)
	if !ok {
		return
	}
	var req BulkUpdateRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := httputil.ValidateStruct(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := h.svc.BulkUpdate(r.Context(), userID, storeID, &req)
	if err != nil {
		h.writeErr(w, err, userID.String(), "failed to update inventory")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]int{"updated": updated})
}

func (h *Handler) Remove(w http.ResponseWriter, r *http.Request) {
	userID, storeID, ok := h.ids(w, r)
	if !ok {
		return
	}
	variantID, err := uuid.Parse(chi.URLParam(r, "variantID"))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid variant id")
		return
	}
	if err := h.svc.Remove(r.Context(), userID, storeID, variantID); err != nil {
		h.writeErr(w, err, userID.String(), "failed to remove product")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ids(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	userID, ok := middleware.GetUserID(r.Context())
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

func (h *Handler) writeErr(w http.ResponseWriter, err error, userID, msg string) {
	status, message := MapError(err)
	if status >= 500 {
		log.Error().Err(err).Str("user_id", userID).Msg(msg)
		message = msg
	}
	httputil.WriteError(w, status, message)
}
