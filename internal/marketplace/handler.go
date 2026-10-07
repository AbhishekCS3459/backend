package marketplace

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/httputil"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// DebugHeader must be "1", on top of the handler's debug flag, for debug fields.
const DebugHeader = "X-Debug"

type Handler struct {
	svc   Service
	debug bool
}

// NewHandler serves the marketplace. debug must already be false in production.
func NewHandler(svc Service, debug bool) *Handler {
	return &Handler{svc: svc, debug: debug}
}

func (h *Handler) debugRequested(r *http.Request) bool {
	return h.debug && r.Header.Get(DebugHeader) == "1"
}

// Search finds products near a location and the stores selling them.
// @Summary Search products near a location
// @Description Matches products by name, brand or category (typos tolerated: "cocacola" finds "Coca-Cola"), then lists up to 10 nearby stores selling each, with the store's price, distance and availability bucket. Stock not confirmed within the staleness window shows as CONFIRM_WITH_STORE. Exact quantities are never returned: max_order_quantity is how many a customer can order, at most 10.
// @Tags Marketplace
// @Produce json
// @Param q query string true "Search text, 2 to 100 characters"
// @Param lat query number true "Latitude, -90 to 90"
// @Param lng query number true "Longitude, -180 to 180"
// @Param radius_m query int false "Search radius in metres (default 5000, max 20000)"
// @Param limit query int false "Products to return (default 20, max 50)"
// @Param sort query string false "Store order within each product" Enums(nearest, cheapest, availability)
// @Success 200 {object} SearchResult
// @Failure 400 {object} httputil.ErrorResponse
// @Failure 429 {string} string "Too many requests"
// @Router /api/marketplace/search [get]
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	a, ok := areaParams(w, query)
	if !ok {
		return
	}
	limit, ok := intParam(w, query, "limit")
	if !ok {
		return
	}
	result, err := h.svc.Search(r.Context(), SearchParams{
		Query:   query.Get("q"),
		Lat:     a.lat,
		Lng:     a.lng,
		RadiusM: a.radiusM,
		Limit:   limit,
		Sort:    a.sort,
	}, h.debugRequested(r))
	if err != nil {
		writeError(w, r, err, "marketplace search failed")
		return
	}
	writeJSON(w, result)
}

// Nearby lists products sold near a location, grouped by category.
// @Summary Browse products near a location by category
// @Description Products sold by stores within the radius, grouped by top-level category (largest first, at most 20). In each category, products stocked by more nearby stores come first. With category, lists every product in that category by name, one page at a time: pass next_cursor as cursor for the next page. Each product lists up to 10 nearby stores ordered by sort. Exact quantities are never returned: max_order_quantity is how many a customer can order, at most 10.
// @Tags Marketplace
// @Produce json
// @Param lat query number true "Latitude, -90 to 90"
// @Param lng query number true "Longitude, -180 to 180"
// @Param radius_m query int false "Radius in metres (default 2000, max 20000)"
// @Param sort query string false "Store order within each product" Enums(nearest, cheapest, availability)
// @Param category query string false "Only this top-level category, paged by name"
// @Param per_category query int false "Products per category, or per page with category (default 10, max 50)"
// @Param cursor query string false "next_cursor from the previous page; requires category"
// @Success 200 {object} NearbyResult
// @Failure 400 {object} httputil.ErrorResponse
// @Router /api/marketplace/nearby [get]
func (h *Handler) Nearby(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	a, ok := areaParams(w, query)
	if !ok {
		return
	}
	perCategory, ok := intParam(w, query, "per_category")
	if !ok {
		return
	}
	result, err := h.svc.Nearby(r.Context(), NearbyParams{
		Lat:         a.lat,
		Lng:         a.lng,
		RadiusM:     a.radiusM,
		Sort:        a.sort,
		Category:    query.Get("category"),
		PerCategory: perCategory,
		Cursor:      query.Get("cursor"),
	}, h.debugRequested(r))
	if err != nil {
		writeError(w, r, err, "marketplace nearby failed")
		return
	}
	writeJSON(w, result)
}

// NearbyProducts lists every product sold near a location, one page at a time.
// @Summary List all products near a location
// @Description Every product sold by stores within the radius, by name, one page at a time: pass next_cursor as cursor for the next page. With category, only that top-level category. The first page also lists every nearby category with its product count, whatever category is. Each product lists up to 10 nearby stores ordered by sort. Exact quantities are never returned: max_order_quantity is how many a customer can order, at most 10.
// @Tags Marketplace
// @Produce json
// @Param lat query number true "Latitude, -90 to 90"
// @Param lng query number true "Longitude, -180 to 180"
// @Param radius_m query int false "Radius in metres (default 2000, max 20000)"
// @Param sort query string false "Store order within each product" Enums(nearest, cheapest, availability)
// @Param category query string false "Only this top-level category"
// @Param limit query int false "Products per page (default 20, max 50)"
// @Param cursor query string false "next_cursor from the previous page"
// @Success 200 {object} NearbyProductsPage
// @Failure 400 {object} httputil.ErrorResponse
// @Router /api/marketplace/nearby/products [get]
func (h *Handler) NearbyProducts(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	a, ok := areaParams(w, query)
	if !ok {
		return
	}
	limit, ok := intParam(w, query, "limit")
	if !ok {
		return
	}
	page, err := h.svc.NearbyProducts(r.Context(), NearbyPageParams{
		Lat:      a.lat,
		Lng:      a.lng,
		RadiusM:  a.radiusM,
		Sort:     a.sort,
		Category: query.Get("category"),
		Limit:    limit,
		Cursor:   query.Get("cursor"),
	}, h.debugRequested(r))
	if err != nil {
		writeError(w, r, err, "marketplace nearby products failed")
		return
	}
	writeJSON(w, page)
}

// NearbyStores lists the stores near a location, nearest first.
// @Summary List stores near a location
// @Description Stores customers can see within the radius, nearest first, each with its distance, cover photo and how many products it lists (and how many of those aren't out of stock). Closed stores are included with is_open=false and list nothing. has_more is set when more than limit stores are within the radius.
// @Tags Marketplace
// @Produce json
// @Param lat query number true "Latitude, -90 to 90"
// @Param lng query number true "Longitude, -180 to 180"
// @Param radius_m query int false "Radius in metres (default 5000, max 20000)"
// @Param limit query int false "Stores to return (default 50, max 100)"
// @Success 200 {object} NearbyStoresResult
// @Failure 400 {object} httputil.ErrorResponse
// @Failure 429 {string} string "Too many requests"
// @Router /api/marketplace/nearby/stores [get]
func (h *Handler) NearbyStores(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	a, ok := areaParams(w, query)
	if !ok {
		return
	}
	limit, ok := intParam(w, query, "limit")
	if !ok {
		return
	}
	result, err := h.svc.NearbyStores(r.Context(), NearbyStoresParams{
		Lat: a.lat, Lng: a.lng, RadiusM: a.radiusM, Limit: limit,
	})
	if err != nil {
		writeError(w, r, err, "marketplace nearby stores failed")
		return
	}
	writeJSON(w, result)
}

// Product returns one product with the nearby stores selling it.
// @Summary Compare a product's nearby stores
// @Description The canonical product with up to 10 stores within the radius, each with its price, distance and availability, ordered by sort. A product no store lists is 404; one sold only further away has no stores.
// @Tags Marketplace
// @Produce json
// @Param catalogKey path string true "The product's catalog_key, URL-encoded (todayz%3A776963)"
// @Param lat query number true "Latitude, -90 to 90"
// @Param lng query number true "Longitude, -180 to 180"
// @Param radius_m query int false "Radius in metres (default 2000, max 20000)"
// @Param sort query string false "Store order" Enums(nearest, cheapest, availability)
// @Success 200 {object} ProductNearby
// @Failure 400 {object} httputil.ErrorResponse
// @Failure 404 {object} httputil.ErrorResponse
// @Router /api/marketplace/products/{catalogKey} [get]
func (h *Handler) Product(w http.ResponseWriter, r *http.Request) {
	key, err := url.PathUnescape(chi.URLParam(r, "catalogKey"))
	if err != nil {
		writeError(w, r, ErrNotFound, "")
		return
	}
	a, ok := areaParams(w, r.URL.Query())
	if !ok {
		return
	}
	product, err := h.svc.Product(r.Context(), ProductParams{
		CatalogKey: key, Lat: a.lat, Lng: a.lng, RadiusM: a.radiusM, Sort: a.sort,
	}, h.debugRequested(r))
	if err != nil {
		writeError(w, r, err, "failed to load marketplace product")
		return
	}
	writeJSON(w, product)
}

type area struct {
	lat, lng float64
	radiusM  int
	sort     Sort
}

// areaParams reads lat, lng, radius_m and sort; the service checks their ranges.
func areaParams(w http.ResponseWriter, query url.Values) (area, bool) {
	if query.Get("lat") == "" || query.Get("lng") == "" {
		httputil.WriteError(w, http.StatusBadRequest, "lat and lng are required")
		return area{}, false
	}
	lat, errLat := strconv.ParseFloat(query.Get("lat"), 64)
	lng, errLng := strconv.ParseFloat(query.Get("lng"), 64)
	if errLat != nil || errLng != nil {
		httputil.WriteError(w, http.StatusBadRequest, "lat and lng must be numbers")
		return area{}, false
	}
	radius, ok := intParam(w, query, "radius_m")
	if !ok {
		return area{}, false
	}
	return area{lat: lat, lng: lng, radiusM: radius, sort: Sort(query.Get("sort"))}, true
}

// Store returns a store's public details.
// @Summary Get a store
// @Description Public details of a store customers can see. A closed store is returned with is_open=false. Unknown, deleted, deactivated or not yet onboarded stores are 404.
// @Tags Marketplace
// @Produce json
// @Param id path string true "Store ID"
// @Success 200 {object} Store
// @Failure 404 {object} httputil.ErrorResponse
// @Router /api/marketplace/stores/{id} [get]
func (h *Handler) Store(w http.ResponseWriter, r *http.Request) {
	storeID, ok := storeIDParam(w, r)
	if !ok {
		return
	}
	store, err := h.svc.Store(r.Context(), storeID)
	if err != nil {
		writeError(w, r, err, "failed to load marketplace store")
		return
	}
	writeJSON(w, store)
}

// StoreProducts lists the products customers can find at a store.
// @Summary List a store's products
// @Description Products customers can find at the store, ordered by name, one page at a time. Pass next_cursor as cursor for the next page. A closed store has none.
// @Tags Marketplace
// @Produce json
// @Param id path string true "Store ID"
// @Param q query string false "Filter by name, brand or category, 2 to 100 characters"
// @Param limit query int false "Products per page (default 20, max 50)"
// @Param cursor query string false "next_cursor from the previous page"
// @Success 200 {object} StoreProductsPage
// @Failure 400 {object} httputil.ErrorResponse
// @Failure 404 {object} httputil.ErrorResponse
// @Router /api/marketplace/stores/{id}/products [get]
func (h *Handler) StoreProducts(w http.ResponseWriter, r *http.Request) {
	storeID, ok := storeIDParam(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	limit, ok := intParam(w, query, "limit")
	if !ok {
		return
	}
	page, err := h.svc.StoreProducts(r.Context(), storeID, query.Get("q"), query.Get("cursor"), limit, h.debugRequested(r))
	if err != nil {
		writeError(w, r, err, "failed to list marketplace store products")
		return
	}
	writeJSON(w, page)
}

// StoreProduct returns one product at one store, as of the last stock change.
// @Summary Get a product at a store
// @Description The product's details with this store's price and availability, read from the primary database and never cached. Unknown, deleted, closed and unlisted all return the same 404.
// @Tags Marketplace
// @Produce json
// @Param id path string true "Store ID"
// @Param catalogKey path string true "The product's catalog_key, URL-encoded (todayz%3A776963)"
// @Success 200 {object} StoreProduct
// @Failure 404 {object} httputil.ErrorResponse
// @Router /api/marketplace/stores/{id}/products/{catalogKey} [get]
func (h *Handler) StoreProduct(w http.ResponseWriter, r *http.Request) {
	storeID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, ErrNotFound, "")
		return
	}
	key, err := url.PathUnescape(chi.URLParam(r, "catalogKey"))
	if err != nil {
		writeError(w, r, ErrNotFound, "")
		return
	}
	product, err := h.svc.StoreProduct(r.Context(), storeID, key, h.debugRequested(r))
	if err != nil {
		writeError(w, r, err, "failed to load marketplace store product")
		return
	}
	writeJSON(w, product)
}

// writeJSON writes a marketplace response. Prices and stock change with every
// sale, so nothing is cached by browsers or proxies.
func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Cache-Control", "no-store")
	httputil.WriteJSON(w, http.StatusOK, body)
}

func intParam(w http.ResponseWriter, query url.Values, name string) (int, bool) {
	raw := query.Get(name)
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		httputil.WriteError(w, http.StatusBadRequest, name+" must be a positive whole number")
		return 0, false
	}
	return n, true
}

// storeIDParam returns the {id} path segment; a malformed id is a store that doesn't exist.
func storeIDParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "not found")
		return uuid.Nil, false
	}
	return id, true
}

func writeError(w http.ResponseWriter, r *http.Request, err error, logMsg string) {
	var validation *ValidationError
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case errors.Is(r.Context().Err(), context.Canceled):
		// The client went away (e.g. a superseded search); nobody reads the response.
		return
	case errors.As(err, &validation):
		httputil.WriteError(w, http.StatusBadRequest, validation.Message)
	case errors.Is(err, ErrInvalidCursor):
		httputil.WriteError(w, http.StatusBadRequest, "invalid cursor")
	case errors.Is(err, ErrNotFound):
		httputil.WriteError(w, http.StatusNotFound, "not found")
	case errors.Is(err, ErrTimeout):
		log.Warn().Err(err).Msg(logMsg)
		httputil.WriteError(w, http.StatusServiceUnavailable, "search is busy right now; try again shortly")
	default:
		log.Error().Err(err).Msg(logMsg)
		httputil.WriteError(w, http.StatusInternalServerError, "something went wrong; try again")
	}
}
