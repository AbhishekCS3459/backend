package inventory

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/httputil"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/middleware"
	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

const (
	IdempotencyKeyHeader     = "Idempotency-Key"
	IdempotentReplayedHeader = "Idempotent-Replayed"
)

type Handler struct {
	svc Service
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, storeID, variantID, ok := variantIDs(w, r)
	if !ok {
		return
	}
	item, err := h.svc.Get(r.Context(), userID, storeID, variantID)
	if err != nil {
		writeErr(w, err, userID, "failed to load inventory")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, item)
}

func (h *Handler) Receive(w http.ResponseWriter, r *http.Request) {
	userID, storeID, variantID, ok := variantIDs(w, r)
	if !ok {
		return
	}
	var req ReceiveRequest
	if !decode(w, r, &req) {
		return
	}
	res, replayed, err := h.svc.Receive(r.Context(), userID, storeID, variantID, r.Header.Get(IdempotencyKeyHeader), &req)
	if err != nil {
		writeErr(w, err, userID, "failed to receive stock")
		return
	}
	writeResult(w, res, replayed)
}

func (h *Handler) ReceiveBatch(w http.ResponseWriter, r *http.Request) {
	userID, storeID, ok := storeIDs(w, r)
	if !ok {
		return
	}
	var req BatchRequest
	if !decode(w, r, &req) {
		return
	}
	res, replayed, err := h.svc.ReceiveBatch(r.Context(), userID, storeID, r.Header.Get(IdempotencyKeyHeader), &req)
	if err != nil {
		writeErr(w, err, userID, "failed to receive delivery")
		return
	}
	writeResult(w, res, replayed)
}

func (h *Handler) Sell(w http.ResponseWriter, r *http.Request) {
	userID, storeID, variantID, ok := variantIDs(w, r)
	if !ok {
		return
	}
	var req SellRequest
	if !decode(w, r, &req) {
		return
	}
	res, replayed, err := h.svc.Sell(r.Context(), userID, storeID, variantID, r.Header.Get(IdempotencyKeyHeader), &req)
	if err != nil {
		writeErr(w, err, userID, "failed to record sale")
		return
	}
	writeResult(w, res, replayed)
}

func (h *Handler) SellBatch(w http.ResponseWriter, r *http.Request) {
	userID, storeID, ok := storeIDs(w, r)
	if !ok {
		return
	}
	var req BatchRequest
	if !decode(w, r, &req) {
		return
	}
	res, replayed, err := h.svc.SellBatch(r.Context(), userID, storeID, r.Header.Get(IdempotencyKeyHeader), &req)
	if err != nil {
		writeErr(w, err, userID, "failed to record sale")
		return
	}
	writeResult(w, res, replayed)
}

func (h *Handler) Count(w http.ResponseWriter, r *http.Request) {
	userID, storeID, variantID, ok := variantIDs(w, r)
	if !ok {
		return
	}
	var req CountRequest
	if !decode(w, r, &req) {
		return
	}
	res, replayed, err := h.svc.Count(r.Context(), userID, storeID, variantID, r.Header.Get(IdempotencyKeyHeader), &req)
	if err != nil {
		writeErr(w, err, userID, "failed to save stock count")
		return
	}
	writeResult(w, res, replayed)
}

func (h *Handler) Adjust(w http.ResponseWriter, r *http.Request) {
	userID, storeID, variantID, ok := variantIDs(w, r)
	if !ok {
		return
	}
	var req AdjustRequest
	if !decode(w, r, &req) {
		return
	}
	res, replayed, err := h.svc.Adjust(r.Context(), userID, storeID, variantID, r.Header.Get(IdempotencyKeyHeader), &req)
	if err != nil {
		writeErr(w, err, userID, "failed to adjust stock")
		return
	}
	writeResult(w, res, replayed)
}

func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	userID, storeID, variantID, ok := variantIDs(w, r)
	if !ok {
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			httputil.WriteError(w, http.StatusBadRequest, "limit must be a positive number")
			return
		}
		limit = n
	}
	page, err := h.svc.History(r.Context(), userID, storeID, variantID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeErr(w, err, userID, "failed to load inventory history")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, page)
}

func writeResult(w http.ResponseWriter, body any, replayed bool) {
	if replayed {
		w.Header().Set(IdempotentReplayedHeader, "true")
	}
	httputil.WriteJSON(w, http.StatusOK, body)
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

func storeIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
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

func variantIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, uuid.UUID, bool) {
	userID, storeID, ok := storeIDs(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	variantID, err := uuid.Parse(chi.URLParam(r, "variantID"))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid variant id")
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	return userID, storeID, variantID, true
}

// MapError converts inventory errors to an HTTP status and message.
func MapError(err error) (int, string) {
	var ve *ValidationError
	var se *StockError
	var le *LineError
	switch {
	case errors.Is(err, storeaccess.ErrNotFound):
		return http.StatusNotFound, "store not found"
	case errors.Is(err, storeaccess.ErrForbidden):
		return http.StatusForbidden, err.Error()
	case errors.As(err, &ve):
		return http.StatusBadRequest, ve.Message
	case errors.As(err, &se):
		return http.StatusConflict, se.Message
	case errors.Is(err, ErrKeyReused):
		return http.StatusUnprocessableEntity, err.Error()
	case errors.As(err, &le) && errors.Is(le.Err, ErrNotFound):
		return http.StatusNotFound, le.Error()
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound, err.Error()
	case errors.As(err, &le):
		return http.StatusConflict, le.Error()
	case errors.Is(err, ErrUnlisted), errors.Is(err, ErrAlreadyListed):
		return http.StatusConflict, err.Error()
	default:
		return http.StatusInternalServerError, "request failed"
	}
}

func writeErr(w http.ResponseWriter, err error, userID uuid.UUID, msg string) {
	status, message := MapError(err)
	if status >= http.StatusInternalServerError {
		log.Error().Err(err).Str("user_id", userID.String()).Msg(msg)
		message = msg
	}
	httputil.WriteError(w, status, message)
}
