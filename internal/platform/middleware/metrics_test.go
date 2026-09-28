package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestMetricsLabelsByRoutePattern(t *testing.T) {
	r := chi.NewRouter()
	r.Use(Metrics)
	r.Route("/api/stores/{storeID}", func(r chi.Router) {
		r.Get("/products", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	})

	for _, url := range []string{"/api/stores/a1/products", "/api/stores/b2/products", "/nope/123"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, url, nil))
	}

	scrape := httptest.NewRecorder()
	promhttp.Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := scrape.Body.String()

	for _, want := range []string{
		`http_requests_total{method="GET",path="/api/stores/{storeID}/products",status="200"} 2`,
		`http_requests_total{method="GET",path="unmatched",status="404"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q", want)
		}
	}
	if strings.Contains(body, `path="/api/stores/a1/products"`) {
		t.Error("raw URL was used as a metric label")
	}
}
