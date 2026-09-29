package health

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/database"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/httputil"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/mongodb"
)

// dependencyTimeout bounds each dependency ping so an unreachable backend cannot
// stall the endpoint (the MongoDB client otherwise waits for server selection).
const dependencyTimeout = 2 * time.Second

// Handler handles health check requests
type Handler struct {
	db          *database.DB
	mongo       *mongodb.Client
	environment string
}

// NewHandler creates a new health handler. mongo may be nil when the product
// catalogue is disabled.
func NewHandler(db *database.DB, mongo *mongodb.Client, environment string) *Handler {
	return &Handler{db: db, mongo: mongo, environment: environment}
}

// HealthResponse represents the health check response
type HealthResponse struct {
	Status      string          `json:"status"`
	Service     string          `json:"service"`
	Environment string          `json:"environment,omitempty"`
	Timestamp   string          `json:"timestamp"`
	Database    *DatabaseHealth `json:"database,omitempty"`
	MongoDB     *DatabaseHealth `json:"mongodb,omitempty"`
	Version     string          `json:"version,omitempty"`
	Uptime      string          `json:"uptime,omitempty"`
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
// @Description Check if the API is running and its databases are accessible. PostgreSQL is required; MongoDB (product catalogue) is reported but does not affect the status code.
// @Tags Health
// @Accept json
// @Produce json
// @Success 200 {object} HealthResponse
// @Failure 503 {object} HealthResponse "Service unavailable if PostgreSQL is down"
// @Router /api/health [get]
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	response := HealthResponse{
		Status:      "ok",
		Service:     "find-me-backend",
		Environment: h.environment,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		Uptime:      time.Since(startTime).String(),
		Version:     "new version",
	}

	ctx := r.Context()
	var wg sync.WaitGroup
	if h.db != nil {
		wg.Go(func() {
			dbHealth := checkDependency(ctx, h.db.Health)
			response.Database = &dbHealth
		})
	}
	if h.mongo != nil {
		wg.Go(func() {
			mongoHealth := checkDependency(ctx, h.mongo.Health)
			response.MongoDB = &mongoHealth
		})
	} else {
		response.MongoDB = &DatabaseHealth{Status: "disabled"}
	}
	wg.Wait()

	if response.Database != nil && response.Database.Status != "ok" {
		response.Status = "degraded"
		httputil.WriteJSON(w, http.StatusServiceUnavailable, response)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, response)
}

func checkDependency(ctx context.Context, ping func(context.Context) error) DatabaseHealth {
	ctx, cancel := context.WithTimeout(ctx, dependencyTimeout)
	defer cancel()

	start := time.Now()
	err := ping(ctx)
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
