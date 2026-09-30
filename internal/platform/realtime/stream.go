package realtime

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// StreamOptions shapes a server-sent events stream.
type StreamOptions struct {
	// Heartbeat keeps proxies from closing an idle stream.
	Heartbeat time.Duration
	// MaxLifetime ends the stream so the client reconnects, and so its access
	// is checked again; access is only checked when a stream opens.
	MaxLifetime time.Duration
	// MinInterval is the shortest gap between two change events; changes in
	// between are merged into the next one.
	MinInterval time.Duration
	// Retry is how long the client should wait before reconnecting.
	Retry time.Duration
}

func DefaultStreamOptions() StreamOptions {
	return StreamOptions{
		// Under the idle timeouts of common proxies, including the 30s of Next.js rewrites.
		Heartbeat:   15 * time.Second,
		MaxLifetime: 5 * time.Minute,
		MinInterval: 500 * time.Millisecond,
		Retry:       3 * time.Second,
	}
}

// writeTimeout bounds each write, so a client that stopped reading can't hold the stream open.
const writeTimeout = 10 * time.Second

// Stream sends a "change" event after each signal on updates until the client
// leaves, the hub closes or MaxLifetime passes. It starts with a "ready" event
// once the stream is open. Call it only after authorising the request.
func Stream(w http.ResponseWriter, r *http.Request, hub *Hub, updates <-chan struct{}, opts StreamOptions) {
	rc := http.NewResponseController(w)
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	// no-transform stops proxies and compressors from buffering the stream.
	header.Set("Cache-Control", "no-cache, no-transform")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(event string) bool {
		// The server's WriteTimeout covers a whole response; a stream sets its own per write.
		if err := rc.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return false
		}
		if _, err := fmt.Fprint(w, event); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	if !send(fmt.Sprintf("retry: %d\nevent: ready\ndata: {}\n\n", opts.Retry.Milliseconds())) {
		log.Debug().Str("path", r.URL.Path).Msg("event stream could not start")
		return
	}

	heartbeat := time.NewTicker(opts.Heartbeat)
	defer heartbeat.Stop()
	lifetime := time.NewTimer(opts.MaxLifetime)
	defer lifetime.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-hub.Done():
			return
		case <-lifetime.C:
			return
		case <-heartbeat.C:
			if !send(": ping\n\n") {
				return
			}
		case <-updates:
			if !send("event: change\ndata: {}\n\n") {
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-hub.Done():
				return
			case <-time.After(opts.MinInterval):
			}
		}
	}
}
