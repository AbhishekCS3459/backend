package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// HTTP request metrics
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path", "status"},
	)

	httpRequestSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_size_bytes",
			Help:    "HTTP request size in bytes",
			Buckets: []float64{100, 500, 1000, 5000, 10000, 50000, 100000, 500000, 1000000},
		},
		[]string{"method", "path"},
	)

	httpResponseSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_response_size_bytes",
			Help:    "HTTP response size in bytes",
			Buckets: []float64{100, 500, 1000, 5000, 10000, 50000, 100000, 500000, 1000000},
		},
		[]string{"method", "path"},
	)
)

// unmatchedRoute labels requests that hit no route, so scanners probing random
// URLs cannot create unbounded label values.
const unmatchedRoute = "unmatched"

// Metrics middleware records Prometheus metrics for HTTP requests
func Metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status code and size
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		// Process request
		next.ServeHTTP(ww, r)

		duration := time.Since(start).Seconds()
		status := strconv.Itoa(ww.Status())
		path := routePattern(r)

		httpRequestsTotal.WithLabelValues(r.Method, path, status).Inc()
		httpRequestDuration.WithLabelValues(r.Method, path, status).Observe(duration)
		httpResponseSize.WithLabelValues(r.Method, path).Observe(float64(ww.BytesWritten()))
		if r.ContentLength > 0 {
			httpRequestSize.WithLabelValues(r.Method, path).Observe(float64(r.ContentLength))
		}
	})
}

// routePattern returns the matched chi route (e.g. /api/stores/{storeID}) rather
// than the raw URL, keeping label cardinality bounded. It is only complete after
// the next handler has run.
func routePattern(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return unmatchedRoute
	}
	if pattern := rctx.RoutePattern(); pattern != "" {
		return pattern
	}
	return unmatchedRoute
}
