package middleware

import (
	"net/http"
	"time"

	chiMiddleware "github.com/go-chi/chi/v5/middleware"
)

// TimeoutExcept applies chi's request timeout to every request except those
// exempt reports true for, such as event streams that end on their own.
func TimeoutExcept(timeout time.Duration, exempt func(*http.Request) bool) func(http.Handler) http.Handler {
	limit := chiMiddleware.Timeout(timeout)
	return func(next http.Handler) http.Handler {
		limited := limit(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if exempt(r) {
				next.ServeHTTP(w, r)
				return
			}
			limited.ServeHTTP(w, r)
		})
	}
}
