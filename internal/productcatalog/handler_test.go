package productcatalog

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestProductIDParamDecodesEscapedIDs(t *testing.T) {
	const id = "blinkit:bengaluru:koramangala:776963"
	for _, path := range []string{
		"/products/" + id,
		"/products/blinkit%3Abengaluru%3Akoramangala%3A776963",
	} {
		var got string
		r := chi.NewRouter()
		r.Get("/products/{id}", func(_ http.ResponseWriter, req *http.Request) { got = productIDParam(req) })
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
		if got != id {
			t.Errorf("%s: id = %q, want %q", path, got, id)
		}
	}
}
