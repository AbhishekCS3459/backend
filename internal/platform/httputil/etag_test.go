package httputil

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteJSONWithETag(t *testing.T) {
	send := func(ifNoneMatch string, data interface{}) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		rec := httptest.NewRecorder()
		WriteJSONWithETag(rec, req, data)
		return rec
	}

	first := send("", map[string]int{"available": 3})
	require.Equal(t, http.StatusOK, first.Code)
	etag := first.Header().Get("ETag")
	require.NotEmpty(t, etag)
	assert.Equal(t, "private, no-cache", first.Header().Get("Cache-Control"))
	assert.JSONEq(t, `{"available":3}`, first.Body.String())

	unchanged := send(etag, map[string]int{"available": 3})
	assert.Equal(t, http.StatusNotModified, unchanged.Code)
	assert.Empty(t, unchanged.Body.String())
	assert.Equal(t, etag, unchanged.Header().Get("ETag"))

	assert.Equal(t, http.StatusNotModified, send(`"other", W/`+etag, map[string]int{"available": 3}).Code)

	changed := send(etag, map[string]int{"available": 2})
	assert.Equal(t, http.StatusOK, changed.Code)
	assert.NotEqual(t, etag, changed.Header().Get("ETag"))
	assert.JSONEq(t, `{"available":2}`, changed.Body.String())
}
