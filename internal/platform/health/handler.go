package health

import (
	"context"
	"net/http"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/database"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/httputil"
)

// Handler handles health check requests
type Handler struct {
	db *database.DB
}

// NewHandler creates a new health handler
func NewHandler(db *database.DB) *Handler {
	return &Handler{db: db}
}

// HealthResponse represents the health check response
type HealthResponse struct {
	Status    string          `json:"status"`
	Service   string          `json:"service"`
	Timestamp string          `json:"timestamp"`
	Database  *DatabaseHealth `json:"database,omitempty"`
	Version   string          `json:"version,omitempty"`
	Uptime    string          `json:"uptime,omitempty"`
}

// DatabaseHealth represents database health status
type DatabaseHealth struct {
	Status       string `json:"status"`
	ResponseTime string `json:"response_time,omitempty"`
	Error        string `json:"error,omitempty"`
}

var startTime = time.Now()

// Health returns the health status of the API
// @Summary Health check
// @Description Check if the API is running and database is accessible
// @Tags Health
// @Accept json
// @Produce json
// @Success 200 {object} HealthResponse
// @Failure 503 {object} HealthResponse "Service unavailable if database is down"
// @Router /api/health [get]
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	response := HealthResponse{
		Status:    "ok",
		Service:   "find-me-backend",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Uptime:    time.Since(startTime).String(),
		Version: "1.1.1",
	}

	// Check database health if database is available
	if h.db != nil {
		dbHealth := h.checkDatabaseHealth(r.Context())
		response.Database = &dbHealth

		// If database is unhealthy, return 503
		if dbHealth.Status != "ok" {
			response.Status = "degraded"
			httputil.WriteJSON(w, http.StatusServiceUnavailable, response)
			return
		}
	}

	httputil.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) checkDatabaseHealth(ctx context.Context) DatabaseHealth {
	start := time.Now()
	err := h.db.Health(ctx)
	duration := time.Since(start)

	if err != nil {
		return DatabaseHealth{
			Status:       "error",
			ResponseTime: duration.String(),
			Error:        err.Error(),
		}
	}

	return DatabaseHealth{
		Status:       "ok",
		ResponseTime: duration.String(),
	}
}
