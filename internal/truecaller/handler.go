package truecaller

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

// Start registers a Truecaller request nonce so status polling can resolve it.
// @Summary Start Truecaller login
// @Tags Truecaller
// @Accept json
// @Produce json
// @Param request body StartRequest true "Request nonce"
// @Success 202 {object} map[string]string
// @Failure 400 {object} httputil.ErrorResponse
// @Router /api/truecaller/start [post]
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	var req StartRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		log.Error().Err(err).Str("step", "http_start").Msg("truecaller: invalid start body")
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := httputil.ValidateStruct(&req); err != nil {
		log.Error().Err(err).Str("step", "http_start").Msg("truecaller: start validation failed")
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.svc.Start(r.Context(), req.RequestID); err != nil {
		log.Error().Err(err).Str("step", "http_start").Str("request_id", req.RequestID).Msg("truecaller: start failed")
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusAccepted, map[string]string{"status": StatusPending})
}

// Callback receives the Truecaller server webhook and acknowledges immediately.
// @Summary Truecaller callback
// @Tags Truecaller
// @Accept json
// @Produce json
// @Param request body CallbackRequest true "Truecaller callback"
// @Success 200 {object} map[string]string
// @Failure 400 {object} httputil.ErrorResponse
// @Router /api/truecaller/callback [post]
func (h *Handler) Callback(w http.ResponseWriter, r *http.Request) {
	log.Info().
		Str("step", "http_callback").
		Str("remote_addr", r.RemoteAddr).
		Str("user_agent", r.UserAgent()).
		Msg("truecaller: incoming callback")

	var req CallbackRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		log.Error().Err(err).Str("step", "http_callback").Msg("truecaller: invalid callback body")
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.svc.HandleCallback(r.Context(), &req); err != nil {
		log.Error().Err(err).Str("step", "http_callback").Str("request_id", req.RequestID).Msg("truecaller: callback rejected")
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Status is polled by the browser until verification completes.
// @Summary Truecaller login status
// @Tags Truecaller
// @Produce json
// @Param requestId query string true "Request nonce"
// @Success 200 {object} StatusResponse
// @Failure 400 {object} httputil.ErrorResponse
// @Router /api/truecaller/status [get]
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	requestID := r.URL.Query().Get("requestId")
	if requestID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "requestId is required")
		return
	}

	result, err := h.svc.Status(r.Context(), requestID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to read verification status")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, result)
}
