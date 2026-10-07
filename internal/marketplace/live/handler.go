package live

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/AbhishekCS3459/find-me-backend/internal/marketplace"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/httputil"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/realtime"
)

const (
	// readyTimeout is how long a stream waits for Redis to confirm its
	// subscription before telling the client to fall back to polling.
	readyTimeout = 3 * time.Second
	// unavailableRetryAfter is the Retry-After sent when live updates are off.
	unavailableRetryAfter = 30 * time.Second
)

// HandlerConfig tunes a Handler.
type HandlerConfig struct {
	// MaxPerIP caps the streams one client address holds open.
	MaxPerIP int
	Stream   realtime.StreamOptions
}

// Handler serves live availability streams. A nil gateway means Redis is not
// configured: every stream is refused and clients poll instead.
type Handler struct {
	gateway *Gateway
	opts    realtime.StreamOptions
	perIP   *ipLimit
}

func NewHandler(gateway *Gateway, cfg HandlerConfig) *Handler {
	if cfg.MaxPerIP <= 0 {
		cfg.MaxPerIP = 30
	}
	if cfg.Stream == (realtime.StreamOptions{}) {
		cfg.Stream = realtime.DefaultStreamOptions()
	}
	return &Handler{gateway: gateway, opts: cfg.Stream, perIP: newIPLimit(cfg.MaxPerIP)}
}

// IsStreamRequest reports whether r opens a live stream, which must not be
// cut short by the request timeout.
func IsStreamRequest(r *http.Request) bool {
	return r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/marketplace/products/") &&
		strings.HasSuffix(r.URL.Path, "/live")
}

// Stream sends a product's availability changes as they happen.
// @Summary Live availability of a product
// @Description Server-sent events for one product. "ready" (data {}) once the stream is live: fetch the product now, so no change falls between the snapshot and the stream. "offer" carries one store's new price, availability bucket and max_order_quantity (at most 10), newest first per store; apply it only if its version is higher than the one you have, and drop the store when searchable is false. "resync" (data {}) means changes may have been missed: fetch the product again. The stream ends after a few minutes and the browser reconnects; each "ready" calls for a fresh fetch. 503 means live updates are off: poll instead. Exact quantities are never sent.
// @Tags Marketplace
// @Produce text/event-stream
// @Param catalogKey path string true "The product's catalog_key, URL-encoded (todayz%3A776963)"
// @Param store_id query string false "Only this store's changes"
// @Success 200 {object} Update "offer event data"
// @Failure 400 {object} httputil.ErrorResponse
// @Failure 404 {object} httputil.ErrorResponse
// @Failure 429 {object} httputil.ErrorResponse
// @Failure 503 {object} httputil.ErrorResponse
// @Router /api/marketplace/products/{catalogKey}/live [get]
func (h *Handler) Stream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	key, err := url.PathUnescape(chi.URLParam(r, "catalogKey"))
	if err != nil || !marketplace.ValidCatalogKey(key) {
		httputil.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	var store uuid.UUID
	if raw := r.URL.Query().Get("store_id"); raw != "" {
		if store, err = uuid.Parse(raw); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "store_id must be a UUID")
			return
		}
	}
	if h.gateway == nil {
		unavailable(w, "live updates are off")
		return
	}
	ip := clientIP(r)
	if !h.perIP.acquire(ip) {
		httputil.WriteError(w, http.StatusTooManyRequests, "too many live streams from this address")
		return
	}
	defer h.perIP.release(ip)

	sub, err := h.gateway.Subscribe(key, store)
	if err != nil {
		if !errors.Is(err, ErrClosed) {
			log.Warn().Err(err).Msg("live stream refused")
		}
		unavailable(w, "live updates are busy")
		return
	}
	defer sub.Close()

	wait := time.NewTimer(readyTimeout)
	defer wait.Stop()
	select {
	case <-sub.Ready():
	case <-wait.C:
		unavailable(w, "live updates are unavailable")
		return
	case <-h.gateway.Done():
		unavailable(w, "live updates are restarting")
		return
	case <-r.Context().Done():
		return
	}

	h.serve(w, r, sub)
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request, sub *Subscription) {
	send := realtime.OpenEvents(w).Send
	// Jitter spreads out the reconnects after an instance restarts.
	retry := h.opts.Retry + rand.N(h.opts.Retry+1)
	if !send(fmt.Sprintf("retry: %d\nevent: ready\ndata: {}\n\n", retry.Milliseconds())) {
		return
	}

	heartbeat := time.NewTicker(h.opts.Heartbeat)
	defer heartbeat.Stop()
	lifetime := time.NewTimer(h.opts.MaxLifetime)
	defer lifetime.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-h.gateway.Done():
			return
		case <-lifetime.C:
			return
		case <-heartbeat.C:
			if !send(": ping\n\n") {
				return
			}
		case <-sub.Notify():
			updates, resync := sub.Take()
			if chunk := events(updates, resync); chunk != "" && !send(chunk) {
				return
			}
			// Changes during the pause go out together in the next batch.
			select {
			case <-r.Context().Done():
				return
			case <-h.gateway.Done():
				return
			case <-time.After(h.opts.MinInterval):
			}
		}
	}
}

func events(updates []Update, resync bool) string {
	var b strings.Builder
	if resync {
		b.WriteString("event: resync\ndata: {}\n\n")
	}
	for _, u := range updates {
		data, err := json.Marshal(u)
		if err != nil {
			continue
		}
		b.WriteString("event: offer\ndata: ")
		b.Write(data)
		b.WriteString("\n\n")
	}
	return b.String()
}

func unavailable(w http.ResponseWriter, message string) {
	w.Header().Set("Retry-After", strconv.Itoa(int(unavailableRetryAfter.Seconds())))
	httputil.WriteError(w, http.StatusServiceUnavailable, message)
}

// clientIP is the address chi's RealIP middleware left in RemoteAddr.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// ipLimit counts open streams per client address.
type ipLimit struct {
	max  int
	mu   sync.Mutex
	open map[string]int
}

func newIPLimit(max int) *ipLimit { return &ipLimit{max: max, open: make(map[string]int)} }

func (l *ipLimit) acquire(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.open[ip] >= l.max {
		return false
	}
	l.open[ip]++
	return true
}

func (l *ipLimit) release(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.open[ip]--; l.open[ip] <= 0 {
		delete(l.open, ip)
	}
}
