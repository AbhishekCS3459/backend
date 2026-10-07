package orders

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/AbhishekCS3459/find-me-backend/internal/payment"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/httputil"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/middleware"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/realtime"
	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

const (
	IdempotencyKeyHeader     = "Idempotency-Key"
	IdempotentReplayedHeader = "Idempotent-Replayed"
	maxWebhookBytes          = 64 << 10
)

type Handler struct {
	svc *Service
	hub *realtime.Hub
}

// NewHandler serves order endpoints; hub carries the store's live order events.
func NewHandler(svc *Service, hub *realtime.Hub) *Handler {
	return &Handler{svc: svc, hub: hub}
}

// ErrorResponse is an order error. Code names the error for clients; Items
// lists the lines that can't be ordered; AttemptsLeft follows a wrong pickup code.
type ErrorResponse struct {
	Error        string        `json:"error"`
	Code         string        `json:"code,omitempty"`
	Items        []ItemProblem `json:"items,omitempty"`
	AttemptsLeft *int          `json:"attempts_left,omitempty"`
}

// Customer endpoints.

func (h *Handler) Place(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFrom(w, r)
	if !ok {
		return
	}
	var req PlaceRequest
	if !decode(w, r, &req, false) {
		return
	}
	order, replayed, err := h.svc.Place(r.Context(), userID, r.Header.Get(IdempotencyKeyHeader), &req)
	if err != nil {
		writeErr(w, err, userID, "failed to place order")
		return
	}
	if replayed {
		w.Header().Set(IdempotentReplayedHeader, "true")
		httputil.WriteJSON(w, http.StatusOK, order)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, order)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFrom(w, r)
	if !ok {
		return
	}
	limit, ok := limitFrom(w, r)
	if !ok {
		return
	}
	page, err := h.svc.List(r.Context(), userID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeErr(w, err, userID, "failed to load orders")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, orderID, ok := customerIDs(w, r)
	if !ok {
		return
	}
	order, err := h.svc.Get(r.Context(), userID, orderID)
	if err != nil {
		writeErr(w, err, userID, "failed to load order")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, order)
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	userID, orderID, ok := customerIDs(w, r)
	if !ok {
		return
	}
	var req CancelRequest
	if !decode(w, r, &req, true) {
		return
	}
	order, err := h.svc.Cancel(r.Context(), userID, orderID, &req)
	if err != nil {
		writeErr(w, err, userID, "failed to cancel order")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, order)
}

func (h *Handler) StartPayment(w http.ResponseWriter, r *http.Request) {
	userID, orderID, ok := customerIDs(w, r)
	if !ok {
		return
	}
	p, err := h.svc.StartPayment(r.Context(), userID, orderID)
	if err != nil {
		writeErr(w, err, userID, "failed to start payment")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, p)
}

// Simulate completes a dummy payment; real gateways report through webhooks.
func (h *Handler) Simulate(w http.ResponseWriter, r *http.Request) {
	userID, orderID, ok := customerIDs(w, r)
	if !ok {
		return
	}
	paymentID, err := uuid.Parse(chi.URLParam(r, "paymentID"))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid payment id")
		return
	}
	var req SimulateRequest
	if !decode(w, r, &req, false) {
		return
	}
	order, err := h.svc.Simulate(r.Context(), userID, orderID, paymentID, payment.Outcome(req.Outcome))
	if err != nil {
		writeErr(w, err, userID, "failed to simulate payment")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, order)
}

// Webhook receives payment gateway events. It is public: the provider
// verifies each delivery's signature.
func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBytes))
	if err != nil {
		httputil.WriteError(w, http.StatusRequestEntityTooLarge, "webhook body too large")
		return
	}
	provider := chi.URLParam(r, "provider")
	if err := h.svc.HandleWebhook(r.Context(), provider, r.Header, body); err != nil {
		writeErr(w, err, uuid.Nil, "failed to process payment webhook")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Store endpoints.

// Events streams a "change" event whenever one of the store's orders
// changes, so the orders screen reloads instead of polling.
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	userID, storeID, ok := storeIDs(w, r)
	if !ok {
		return
	}
	if err := h.svc.CanWatch(r.Context(), userID, storeID); err != nil {
		writeErr(w, err, userID, "failed to open order events")
		return
	}
	updates, unsubscribe, err := h.hub.Subscribe(storeID.String())
	if err != nil {
		w.Header().Set("Retry-After", "30")
		httputil.WriteError(w, http.StatusServiceUnavailable, "live updates are busy; try again shortly")
		return
	}
	defer unsubscribe()
	realtime.Stream(w, r, h.hub, updates, realtime.DefaultStreamOptions())
}

// IsEventsRequest reports whether r opens a store's order event stream,
// which outlives the usual request timeout.
func IsEventsRequest(r *http.Request) bool {
	return r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/stores/") &&
		strings.HasSuffix(r.URL.Path, "/orders/events")
}

func (h *Handler) StoreList(w http.ResponseWriter, r *http.Request) {
	userID, storeID, ok := storeIDs(w, r)
	if !ok {
		return
	}
	limit, ok := limitFrom(w, r)
	if !ok {
		return
	}
	var statuses []Status
	if raw := r.URL.Query().Get("status"); raw != "" {
		for _, s := range strings.Split(raw, ",") {
			statuses = append(statuses, Status(strings.ToUpper(strings.TrimSpace(s))))
		}
	}
	page, err := h.svc.StoreList(r.Context(), userID, storeID, statuses, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeErr(w, err, userID, "failed to load orders")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) StoreGet(w http.ResponseWriter, r *http.Request) {
	userID, storeID, orderID, ok := storeOrderIDs(w, r)
	if !ok {
		return
	}
	order, err := h.svc.StoreGet(r.Context(), userID, storeID, orderID)
	if err != nil {
		writeErr(w, err, userID, "failed to load order")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, order)
}

func (h *Handler) Accept(w http.ResponseWriter, r *http.Request) {
	userID, storeID, orderID, ok := storeOrderIDs(w, r)
	if !ok {
		return
	}
	order, err := h.svc.Accept(r.Context(), userID, storeID, orderID)
	if err != nil {
		writeErr(w, err, userID, "failed to accept order")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, order)
}

func (h *Handler) Reject(w http.ResponseWriter, r *http.Request) {
	userID, storeID, orderID, ok := storeOrderIDs(w, r)
	if !ok {
		return
	}
	var req RejectRequest
	if !decode(w, r, &req, false) {
		return
	}
	order, err := h.svc.Reject(r.Context(), userID, storeID, orderID, &req)
	if err != nil {
		writeErr(w, err, userID, "failed to reject order")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, order)
}

func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	userID, storeID, orderID, ok := storeOrderIDs(w, r)
	if !ok {
		return
	}
	order, err := h.svc.Ready(r.Context(), userID, storeID, orderID)
	if err != nil {
		writeErr(w, err, userID, "failed to mark order ready")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, order)
}

func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	userID, storeID, orderID, ok := storeOrderIDs(w, r)
	if !ok {
		return
	}
	var req CompleteRequest
	if !decode(w, r, &req, false) {
		return
	}
	order, err := h.svc.Complete(r.Context(), userID, storeID, orderID, &req)
	if err != nil {
		writeErr(w, err, userID, "failed to complete order")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, order)
}

// decode reads and validates the JSON body; optional bodies may be empty.
func decode(w http.ResponseWriter, r *http.Request, dst any, optional bool) bool {
	if err := httputil.DecodeJSON(r, dst); err != nil && (!optional || !errors.Is(err, io.EOF)) {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	if err := httputil.ValidateStruct(dst); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

func limitFrom(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		httputil.WriteError(w, http.StatusBadRequest, "limit must be a positive number")
		return 0, false
	}
	return n, true
}

func userIDFrom(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "authentication required")
	}
	return userID, ok
}

func parseID(w http.ResponseWriter, r *http.Request, param, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid "+name+" id")
		return uuid.Nil, false
	}
	return id, true
}

func customerIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	userID, ok := userIDFrom(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	orderID, ok := parseID(w, r, "orderID", "order")
	return userID, orderID, ok
}

func storeIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	userID, ok := userIDFrom(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	storeID, ok := parseID(w, r, "storeID", "store")
	return userID, storeID, ok
}

func storeOrderIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, uuid.UUID, bool) {
	userID, storeID, ok := storeIDs(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	orderID, ok := parseID(w, r, "orderID", "order")
	return userID, storeID, orderID, ok
}

// MapError converts order errors to an HTTP status and response body.
func MapError(err error) (int, ErrorResponse) {
	var ve *ValidationError
	var st *StateError
	var ie *ItemsError
	var pe *PickupCodeError
	switch {
	case errors.Is(err, storeaccess.ErrNotFound):
		return http.StatusNotFound, ErrorResponse{Error: "store not found"}
	case errors.Is(err, storeaccess.ErrForbidden):
		return http.StatusForbidden, ErrorResponse{Error: err.Error()}
	case errors.As(err, &ve):
		return http.StatusBadRequest, ErrorResponse{Error: ve.Message, Code: "INVALID_REQUEST"}
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrPaymentNotFound):
		return http.StatusNotFound, ErrorResponse{Error: err.Error(), Code: "NOT_FOUND"}
	case errors.Is(err, ErrKeyReused):
		return http.StatusUnprocessableEntity, ErrorResponse{Error: err.Error(), Code: "IDEMPOTENCY_KEY_REUSED"}
	case errors.As(err, &pe):
		left := pe.AttemptsLeft
		return http.StatusUnprocessableEntity,
			ErrorResponse{Error: pe.Error(), Code: "WRONG_PICKUP_CODE", AttemptsLeft: &left}
	case errors.As(err, &st):
		return http.StatusConflict, ErrorResponse{Error: st.Error(), Code: "INVALID_STATE"}
	case errors.As(err, &ie):
		return http.StatusConflict, ErrorResponse{Error: ie.Message, Code: ie.Code, Items: ie.Items}
	case errors.Is(err, ErrStoreUnavailable):
		return http.StatusConflict, ErrorResponse{Error: err.Error(), Code: "STORE_UNAVAILABLE"}
	case errors.Is(err, ErrTooManyOpen):
		return http.StatusConflict, ErrorResponse{Error: err.Error(), Code: "TOO_MANY_OPEN_ORDERS"}
	case errors.Is(err, ErrNotOnlinePayment):
		return http.StatusConflict, ErrorResponse{Error: err.Error(), Code: "NOT_ONLINE_PAYMENT"}
	case errors.Is(err, ErrNotSimulated):
		return http.StatusConflict, ErrorResponse{Error: err.Error(), Code: "NOT_SIMULATED"}
	case errors.Is(err, ErrPickupLocked):
		return http.StatusConflict, ErrorResponse{Error: err.Error(), Code: "PICKUP_LOCKED"}
	case errors.Is(err, ErrProvider):
		return http.StatusBadGateway, ErrorResponse{Error: err.Error(), Code: "PAYMENT_PROVIDER_ERROR"}
	case errors.Is(err, payment.ErrWebhooksUnsupported):
		return http.StatusNotFound, ErrorResponse{Error: err.Error()}
	case errors.Is(err, payment.ErrInvalidWebhook):
		return http.StatusBadRequest, ErrorResponse{Error: err.Error()}
	default:
		return http.StatusInternalServerError, ErrorResponse{Error: "request failed"}
	}
}

func writeErr(w http.ResponseWriter, err error, userID uuid.UUID, msg string) {
	status, body := MapError(err)
	if status >= http.StatusInternalServerError && status != http.StatusBadGateway {
		log.Error().Err(err).Str("user_id", userID.String()).Msg(msg)
		body.Error = msg
	}
	httputil.WriteJSON(w, status, body)
}
