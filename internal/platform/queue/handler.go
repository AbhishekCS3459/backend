package queue

import (
	"net/http"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/httputil"
	"github.com/go-chi/chi/v5"
)

// Handler handles queue monitoring and management endpoints
type Handler struct {
	queue Queue
}

// NewHandler creates a new queue handler
func NewHandler(q Queue) *Handler {
	return &Handler{queue: q}
}

// ListQueues returns all queue names and their stats
// @Summary List all queues
// @Description Get statistics for all queues (admin only)
// @Tags queue
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/admin/queues [get]
func (h *Handler) ListQueues(w http.ResponseWriter, r *http.Request) {
	statsProvider, ok := h.queue.(StatsProvider)
	if !ok {
		httputil.WriteError(w, http.StatusNotImplemented, "queue does not support statistics")
		return
	}

	queueNames, err := statsProvider.ListQueues(r.Context())
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to list queues: "+err.Error())
		return
	}

	queues := make(map[string]*QueueStats)
	for _, name := range queueNames {
		stats, err := statsProvider.GetStats(r.Context(), name)
		if err != nil {
			continue
		}
		queues[name] = stats
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"queues": queues,
		"count":  len(queues),
	})
}

// GetQueueStats returns statistics for a specific queue
// @Summary Get queue statistics
// @Description Get statistics for a specific queue (admin only)
// @Tags queue
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param queueName path string true "Queue name"
// @Param peek query bool false "Peek at jobs (default: false)"
// @Success 200 {object} QueueInfo
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/admin/queues/{queueName} [get]
func (h *Handler) GetQueueStats(w http.ResponseWriter, r *http.Request) {
	queueName := chi.URLParam(r, "queueName")
	if queueName == "" {
		httputil.WriteError(w, http.StatusBadRequest, "queue name required")
		return
	}

	statsProvider, ok := h.queue.(StatsProvider)
	if !ok {
		httputil.WriteError(w, http.StatusNotImplemented, "queue does not support statistics")
		return
	}

	peek := r.URL.Query().Get("peek") == "true"

	if infoProvider, ok := h.queue.(QueueInfoProvider); ok {
		info, err := infoProvider.GetQueueInfo(r.Context(), queueName, peek)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "failed to get queue info: "+err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, info)
		return
	}

	stats, err := statsProvider.GetStats(r.Context(), queueName)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "queue not found: "+queueName)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"stats":  stats,
		"peeked": false,
	})
}
