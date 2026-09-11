package middleware

import (
	"net/http"
	"strings"
)

const (
	apiCSP     = "default-src 'none'; frame-ancestors 'none'"
	swaggerCSP = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'"
)

// SecurityHeaders adds security headers to responses
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Swagger UI needs inline script/style. Keep API responses locked down.
		if strings.HasPrefix(r.URL.Path, "/swagger") {
			w.Header().Set("Content-Security-Policy", swaggerCSP)
		} else {
			w.Header().Set("Content-Security-Policy", apiCSP)
		}

		next.ServeHTTP(w, r)
	})
}
