package analytics

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

// Routes is mounted at /api/stores/{storeID}/analytics.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.Report)
	return r
}

// Report returns the store's analytics for ?period= (today, 7d, 14d, 30d,
// 90d; default 7d) in the store's time zone ?tz= (default Asia/Kolkata).
func (h *Handler) Report(w http.ResponseWriter, r *http.Request) {
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
	period := Period(r.URL.Query().Get("period"))
	if period == "" {
		period = Period7Days
	}
	report, err := h.svc.Report(r.Context(), userID, storeID, period, r.URL.Query().Get("tz"))
	switch {
	case err == nil:
		httputil.WriteJSONWithETag(w, r, report)
	case errors.Is(err, ErrInvalidPeriod), errors.Is(err, ErrInvalidTimezone):
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, storeaccess.ErrNotFound):
		httputil.WriteError(w, http.StatusNotFound, "store not found")
	case errors.Is(err, storeaccess.ErrForbidden):
		httputil.WriteError(w, http.StatusForbidden, err.Error())
	default:
		log.Error().Err(err).Str("store_id", storeID.String()).Msg("failed to build analytics report")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to load analytics")
	}
}
