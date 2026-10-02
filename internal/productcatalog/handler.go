package productcatalog

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/httputil"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
)

type Handler struct {
	svc Service
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

// List returns one page of catalogue products ordered by id.
// Query params: q (words in the name or brand, or a product id), after (cursor
// from next_cursor) or offset (matches to skip, for jumping to a page), limit,
// the filters group, collection, category, brand (values from /filters), in_stock=true, and
// min_price / max_price in rupees.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filters, err := filtersFrom(query)
	if err != nil {
		writeError(w, r, err, "")
		return
	}
	offset, _ := strconv.Atoi(query.Get("offset"))
	limit, _ := strconv.Atoi(query.Get("limit"))

	page, err := h.svc.List(r.Context(), ListParams{
		Query:   query.Get("q"),
		After:   query.Get("after"),
		Offset:  offset,
		Limit:   limit,
		Filters: filters,
	})
	if err != nil {
		writeError(w, r, err, "failed to list catalogue products")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, page)
}

// Search returns products ranked by relevance to q, tolerating typos ("stiker
// book" finds "Sticker Book"). Query params: q (required), offset, limit, and
// the List filters.
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filters, err := filtersFrom(query)
	if err != nil {
		writeError(w, r, err, "")
		return
	}
	offset, _ := strconv.Atoi(query.Get("offset"))
	limit, _ := strconv.Atoi(query.Get("limit"))

	result, err := h.svc.Search(r.Context(), SearchParams{
		Query:   query.Get("q"),
		Offset:  offset,
		Limit:   limit,
		Filters: filters,
	})
	if err != nil {
		writeError(w, r, err, "failed to search catalogue products")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, result)
}

// Autocomplete suggests product names for a partly typed q ("sti" suggests
// "Sticker Book"). Query params: q, limit, and the List filters.
func (h *Handler) Autocomplete(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filters, err := filtersFrom(query)
	if err != nil {
		writeError(w, r, err, "")
		return
	}
	limit, _ := strconv.Atoi(query.Get("limit"))

	suggestions, err := h.svc.Autocomplete(r.Context(), query.Get("q"), limit, filters)
	if err != nil {
		writeError(w, r, err, "failed to load catalogue suggestions")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string][]Suggestion{"suggestions": suggestions})
}

// Filters returns the values accepted by the List filters. Each filter's values
// are narrowed by the other filters passed in the query string.
func (h *Handler) Filters(w http.ResponseWriter, r *http.Request) {
	filters, err := filtersFrom(r.URL.Query())
	if err != nil {
		writeError(w, r, err, "")
		return
	}
	options, err := h.svc.FilterOptions(r.Context(), filters)
	if err != nil {
		writeError(w, r, err, "failed to load catalogue filters")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, options)
}

func filtersFrom(query url.Values) (Filters, error) {
	f := Filters{
		Group:      query.Get("group"),
		Collection: query.Get("collection"),
		Category:   query.Get("category"),
		Brand:      query.Get("brand"),
	}
	if v := query.Get("in_stock"); v != "" {
		inStock, err := strconv.ParseBool(v)
		if err != nil {
			return Filters{}, ErrInvalidFilter
		}
		f.InStock = inStock
	}
	for _, bound := range []struct {
		key string
		dst *float64
	}{{"min_price", &f.MinPrice}, {"max_price", &f.MaxPrice}} {
		v := query.Get(bound.key)
		if v == "" {
			continue
		}
		price, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(price) || math.IsInf(price, 0) {
			return Filters{}, ErrInvalidFilter
		}
		*bound.dst = price
	}
	return f, nil
}

// productIDParam returns the {id} path segment decoded. chi routes on the raw
// (still percent-encoded) path when the request has one, so an id such as
// "blinkit:bengaluru:koramangala:776963" arrives as "blinkit%3Abengaluru…".
func productIDParam(r *http.Request) string {
	raw := chi.URLParam(r, "id")
	id, err := url.PathUnescape(raw)
	if err != nil {
		return raw
	}
	return id
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	product, err := h.svc.Get(r.Context(), productIDParam(r))
	if err != nil {
		writeError(w, r, err, "failed to load catalogue product")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, product)
}

func writeError(w http.ResponseWriter, r *http.Request, err error, logMsg string) {
	switch {
	case errors.Is(r.Context().Err(), context.Canceled):
		// The client went away (e.g. a superseded search); nobody reads the response.
		return
	case errors.Is(err, ErrNotFound):
		httputil.WriteError(w, http.StatusNotFound, "product not found")
	case errors.Is(err, ErrInvalidFilter):
		httputil.WriteError(w, http.StatusBadRequest, "invalid catalogue filter")
	case errors.Is(err, ErrInvalidQuery):
		httputil.WriteError(w, http.StatusBadRequest, "search query must be 1 to 200 characters")
	case errors.Is(err, ErrUnavailable):
		httputil.WriteError(w, http.StatusServiceUnavailable, "product catalogue is not configured")
	case errors.Is(err, ErrTimeout):
		httputil.WriteError(w, http.StatusGatewayTimeout, "search took too long; try more specific words or add a filter")
	default:
		log.Error().Err(err).Msg(logMsg)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to load catalogue")
	}
}
