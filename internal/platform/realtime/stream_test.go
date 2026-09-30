package realtime

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/require"
)

// readEvent returns the next event's name, or "ping" for a heartbeat.
func readEvent(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	name := ""
	for {
		line, err := r.ReadString('\n')
		require.NoError(t, err)
		line = strings.TrimRight(line, "\n")
		switch {
		case line == "" && name != "":
			return name
		case strings.HasPrefix(line, ": ping"):
			name = "ping"
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		}
	}
}

func TestStreamOutlivesWriteTimeoutAndDeliversChanges(t *testing.T) {
	hub := NewHub(0)
	router := chi.NewRouter()
	// The same wrappers the API uses, which must pass flushing and deadlines through.
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(chiMiddleware.NewWrapResponseWriter(w, r.ProtoMajor), r)
		})
	})
	router.Use(chiMiddleware.Compress(5))
	router.Get("/events", func(w http.ResponseWriter, r *http.Request) {
		updates, stop, err := hub.Subscribe("store")
		if err != nil {
			t.Error(err)
			return
		}
		defer stop()
		Stream(w, r, hub, updates, StreamOptions{
			Heartbeat:   100 * time.Millisecond,
			MaxLifetime: time.Second,
			MinInterval: 10 * time.Millisecond,
			Retry:       time.Second,
		})
	})
	server := httptest.NewUnstartedServer(router)
	server.Config.WriteTimeout = 200 * time.Millisecond
	server.Start()
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/events", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, "text/event-stream", res.Header.Get("Content-Type"))
	require.Empty(t, res.Header.Get("Content-Encoding"))
	events := bufio.NewReader(res.Body)
	require.Equal(t, "ready", readEvent(t, events))

	// Past the server's WriteTimeout, heartbeats and changes still arrive.
	time.Sleep(400 * time.Millisecond)
	hub.Publish("store")
	for name := readEvent(t, events); name != "change"; name = readEvent(t, events) {
		require.Equal(t, "ping", name)
	}

	// MaxLifetime ends the stream so the client reconnects.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, err := events.ReadString('\n'); err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not end after MaxLifetime")
	}
	require.Eventually(t, func() bool { return hub.Subscribers() == 0 }, time.Second, 10*time.Millisecond)
}

func TestStreamEndsWhenHubCloses(t *testing.T) {
	hub := NewHub(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		updates, stop, err := hub.Subscribe("store")
		if err != nil {
			t.Error(err)
			return
		}
		defer stop()
		Stream(w, r, hub, updates, DefaultStreamOptions())
	}))
	defer server.Close()

	res, err := http.Get(server.URL)
	require.NoError(t, err)
	defer res.Body.Close()
	events := bufio.NewReader(res.Body)
	require.Equal(t, "ready", readEvent(t, events))

	hub.Close()
	_, err = events.ReadString('\n')
	for err == nil {
		_, err = events.ReadString('\n')
	}
	require.Eventually(t, func() bool { return hub.Subscribers() == 0 }, time.Second, 10*time.Millisecond)
}
