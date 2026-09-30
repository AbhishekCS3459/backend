package httputil

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
)

// WriteJSONWithETag sends a 200 JSON response tagged with a hash of its body.
// The browser keeps its copy but revalidates it on every request ("private,
// no-cache"); when nothing changed the answer is a 304 with no body, which
// keeps frequent refreshes cheap. Call it only after the request is
// authorized, so a 304 never confirms data to someone who may not read it.
func WriteJSONWithETag(w http.ResponseWriter, r *http.Request, data interface{}) {
	body, err := json.Marshal(data)
	if err != nil {
		log.Error().Err(err).Msg("failed to encode JSON response")
		WriteError(w, http.StatusInternalServerError, "failed to encode response")
		return
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`

	header := w.Header()
	header.Set("ETag", etag)
	header.Set("Cache-Control", "private, no-cache")
	header.Add("Vary", "Authorization")
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	header.Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(append(body, '\n')); err != nil {
		log.Error().Err(err).Msg("failed to write JSON response")
	}
}

// etagMatches applies If-None-Match's weak comparison to a list of tags.
func etagMatches(ifNoneMatch, etag string) bool {
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == "*" || candidate == etag {
			return true
		}
	}
	return false
}
