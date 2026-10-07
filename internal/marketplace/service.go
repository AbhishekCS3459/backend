package marketplace

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/AbhishekCS3459/find-me-backend/internal/availability"
)

type Service interface {
	Search(ctx context.Context, p SearchParams, debug bool) (*SearchResult, error)
	Nearby(ctx context.Context, p NearbyParams, debug bool) (*NearbyResult, error)
	NearbyProducts(ctx context.Context, p NearbyPageParams, debug bool) (*NearbyProductsPage, error)
	Product(ctx context.Context, p ProductParams, debug bool) (*ProductNearby, error)
	Store(ctx context.Context, storeID uuid.UUID) (*Store, error)
	NearbyStores(ctx context.Context, p NearbyStoresParams) (*NearbyStoresResult, error)
	StoreProducts(ctx context.Context, storeID uuid.UUID, query, cursor string, limit int, debug bool) (*StoreProductsPage, error)
	StoreProduct(ctx context.Context, storeID uuid.UUID, catalogKey string, debug bool) (*StoreProduct, error)
}

type service struct {
	repo       Repository
	staleAfter time.Duration
	now        func() time.Time
}

func NewService(repo Repository, cfg Config) Service {
	staleAfter := cfg.StaleAfter
	if staleAfter <= 0 {
		staleAfter = DefaultStaleAfter
	}
	return &service{repo: repo, staleAfter: staleAfter, now: time.Now}
}

// staleBefore is the cutoff: stock last confirmed earlier is CONFIRM_WITH_STORE.
func (s *service) staleBefore() time.Time { return s.now().Add(-s.staleAfter) }

// Search finds products matching the query and, for each, up to
// StoresPerProduct nearby stores selling it. Products come in match order
// (best first); a product no nearby store sells is left out.
func (s *service) Search(ctx context.Context, p SearchParams, debug bool) (*SearchResult, error) {
	p, q, err := validateSearch(p)
	if err != nil {
		return nil, err
	}
	result := &SearchResult{
		Query: p.Query, Lat: p.Lat, Lng: p.Lng, RadiusM: p.RadiusM, Sort: p.Sort, Products: []SearchProduct{},
	}
	matches, err := s.repo.MatchProducts(ctx, *q, MaxSearchLimit)
	if err != nil || len(matches) == 0 {
		return result, err
	}
	keys := make([]string, len(matches))
	for i, m := range matches {
		keys[i] = m.CatalogKey
	}
	offers, err := s.repo.StoreOffers(ctx, keys, p, s.staleBefore())
	if err != nil {
		return nil, err
	}
	byKey := groupOffers(offers, debug)
	for _, m := range matches {
		stores := byKey[m.CatalogKey]
		if len(stores) == 0 {
			continue
		}
		result.Products = append(result.Products, SearchProduct{Product: m, Stores: stores})
		if len(result.Products) == p.Limit {
			break
		}
	}
	return result, nil
}

func validateSearch(p SearchParams) (SearchParams, *textQuery, error) {
	p.Query = strings.TrimSpace(p.Query)
	if n := utf8.RuneCountInString(p.Query); n < MinQueryLength || n > MaxQueryLength {
		return p, nil, invalid(fmt.Sprintf("q must be %d to %d characters", MinQueryLength, MaxQueryLength))
	}
	q := parseTextQuery(p.Query)
	if q == nil {
		return p, nil, invalid("q must contain letters or digits")
	}
	if err := validateArea(&p.Lat, &p.Lng, &p.RadiusM, &p.Sort, DefaultRadiusM); err != nil {
		return p, nil, err
	}
	switch {
	case p.Limit == 0:
		p.Limit = DefaultSearchLimit
	case p.Limit < 0 || p.Limit > MaxSearchLimit:
		return p, nil, invalid(fmt.Sprintf("limit must be between 1 and %d", MaxSearchLimit))
	}
	return p, q, nil
}

// validateArea checks the point, radius and sort shared by every nearby
// query, filling in defaults for a zero radius and an empty sort.
func validateArea(lat, lng *float64, radiusM *int, sort *Sort, defaultRadiusM int) error {
	if !finite(*lat) || *lat < -90 || *lat > 90 {
		return invalid("lat must be between -90 and 90")
	}
	if !finite(*lng) || *lng < -180 || *lng > 180 {
		return invalid("lng must be between -180 and 180")
	}
	switch {
	case *radiusM == 0:
		*radiusM = defaultRadiusM
	case *radiusM < 0 || *radiusM > MaxRadiusM:
		return invalid(fmt.Sprintf("radius_m must be between 1 and %d", MaxRadiusM))
	}
	switch *sort {
	case "":
		*sort = SortNearest
	case SortNearest, SortCheapest, SortAvailability:
	default:
		return invalid("sort must be nearest, cheapest or availability")
	}
	return nil
}

// groupOffers turns offer rows into each product's store list, keeping their order.
func groupOffers(offers []offerRow, debug bool) map[string][]StoreOffer {
	byKey := make(map[string][]StoreOffer)
	for _, o := range offers {
		byKey[o.CatalogKey] = append(byKey[o.CatalogKey], StoreOffer{
			StoreID:            o.StoreID,
			StoreName:          o.StoreName,
			DistanceM:          int(math.Round(o.DistanceM)),
			Lat:                o.Lat,
			Lng:                o.Lng,
			Price:              o.Price,
			AvailabilityBucket: o.AvailabilityBucket,
			MaxOrderQuantity:   availability.OrderableQuantity(o.AvailableQty),
			LastStockUpdateAt:  o.LastStockUpdateAt,
			Version:            o.Version,
			Debug:              debugFields(debug, o.AvailableQty, o.Version),
		})
	}
	return byKey
}

// Nearby lists products sold near the point, grouped by top-level category,
// each with up to StoresPerProduct nearby stores ordered by p.Sort. With a
// category, it pages through all of that category's products by name.
func (s *service) Nearby(ctx context.Context, p NearbyParams, debug bool) (*NearbyResult, error) {
	if err := validateArea(&p.Lat, &p.Lng, &p.RadiusM, &p.Sort, DefaultNearbyRadiusM); err != nil {
		return nil, err
	}
	p.Category = strings.TrimSpace(p.Category)
	if utf8.RuneCountInString(p.Category) > MaxQueryLength {
		return nil, invalid(fmt.Sprintf("category must be at most %d characters", MaxQueryLength))
	}
	switch {
	case p.PerCategory == 0:
		p.PerCategory = DefaultPerCategory
	case p.PerCategory < 0 || p.PerCategory > MaxPerCategory:
		return nil, invalid(fmt.Sprintf("per_category must be between 1 and %d", MaxPerCategory))
	}
	if p.Cursor != "" && p.Category == "" {
		return nil, invalid("cursor requires category")
	}
	after, err := decodeCategoryCursor(p.Cursor)
	if err != nil {
		return nil, err
	}
	p.After = after
	result := &NearbyResult{Lat: p.Lat, Lng: p.Lng, RadiusM: p.RadiusM, Sort: p.Sort, Categories: []NearbyCategory{}}
	rows, err := s.repo.NearbyProducts(ctx, p, s.staleBefore())
	if err != nil || len(rows) == 0 {
		return result, err
	}
	if p.Category != "" && len(rows) > p.PerCategory {
		rows = rows[:p.PerCategory]
		last := rows[len(rows)-1]
		result.HasMore = true
		result.NextCursor = categoryCursor{Name: last.SortName, CatalogKey: last.CatalogKey}.encode()
	}
	keys := make([]string, len(rows))
	for i, row := range rows {
		keys[i] = row.CatalogKey
	}
	offers, err := s.repo.StoreOffers(ctx, keys, SearchParams{Lat: p.Lat, Lng: p.Lng, RadiusM: p.RadiusM, Sort: p.Sort}, s.staleBefore())
	if err != nil {
		return nil, err
	}
	byKey := groupOffers(offers, debug)
	for _, row := range rows {
		stores := byKey[row.CatalogKey]
		if len(stores) == 0 {
			// Unlisted between the two queries.
			continue
		}
		if n := len(result.Categories); n == 0 || result.Categories[n-1].Name != row.Category {
			result.Categories = append(result.Categories, NearbyCategory{Name: row.Category, ProductCount: row.CategorySize})
		}
		last := &result.Categories[len(result.Categories)-1]
		last.Products = append(last.Products, SearchProduct{Product: row.Product, Stores: stores})
	}
	return result, nil
}

// NearbyProducts lists every product sold near the point by name, one page at
// a time, optionally in one top-level category. Each product has up to
// StoresPerProduct nearby stores ordered by p.Sort. The first page also names
// every nearby category with its product count.
func (s *service) NearbyProducts(ctx context.Context, p NearbyPageParams, debug bool) (*NearbyProductsPage, error) {
	if err := validateArea(&p.Lat, &p.Lng, &p.RadiusM, &p.Sort, DefaultNearbyRadiusM); err != nil {
		return nil, err
	}
	p.Category = strings.TrimSpace(p.Category)
	if utf8.RuneCountInString(p.Category) > MaxQueryLength {
		return nil, invalid(fmt.Sprintf("category must be at most %d characters", MaxQueryLength))
	}
	switch {
	case p.Limit == 0:
		p.Limit = DefaultNearbyPageLimit
	case p.Limit < 0 || p.Limit > MaxNearbyPageLimit:
		return nil, invalid(fmt.Sprintf("limit must be between 1 and %d", MaxNearbyPageLimit))
	}
	after, err := decodeCategoryCursor(p.Cursor)
	if err != nil {
		return nil, err
	}
	p.After = after

	page := &NearbyProductsPage{
		Lat: p.Lat, Lng: p.Lng, RadiusM: p.RadiusM, Sort: p.Sort, Category: p.Category, Products: []SearchProduct{},
	}
	staleBefore := s.staleBefore()
	if p.After == nil {
		if page.Categories, err = s.repo.NearbyCategories(ctx, p, staleBefore); err != nil {
			return nil, err
		}
	}
	rows, err := s.repo.NearbyPage(ctx, p, staleBefore)
	if err != nil || len(rows) == 0 {
		return page, err
	}
	page.ProductCount = rows[0].CategorySize
	if len(rows) > p.Limit {
		rows = rows[:p.Limit]
		last := rows[len(rows)-1]
		page.HasMore = true
		page.NextCursor = categoryCursor{Name: last.SortName, CatalogKey: last.CatalogKey}.encode()
	}
	keys := make([]string, len(rows))
	for i, row := range rows {
		keys[i] = row.CatalogKey
	}
	offers, err := s.repo.StoreOffers(ctx, keys, SearchParams{Lat: p.Lat, Lng: p.Lng, RadiusM: p.RadiusM, Sort: p.Sort}, staleBefore)
	if err != nil {
		return nil, err
	}
	byKey := groupOffers(offers, debug)
	for _, row := range rows {
		// A product unlisted between the two queries has no stores left.
		if stores := byKey[row.CatalogKey]; len(stores) > 0 {
			page.Products = append(page.Products, SearchProduct{Product: row.Product, Stores: stores})
		}
	}
	return page, nil
}

// Product returns one product with its nearby stores ordered by p.Sort. A
// product no store lists is ErrNotFound; one listed only further away is
// returned with no stores.
func (s *service) Product(ctx context.Context, p ProductParams, debug bool) (*ProductNearby, error) {
	if !catalogKeyPattern.MatchString(p.CatalogKey) {
		return nil, ErrNotFound
	}
	if err := validateArea(&p.Lat, &p.Lng, &p.RadiusM, &p.Sort, DefaultNearbyRadiusM); err != nil {
		return nil, err
	}
	product, err := s.repo.FindProduct(ctx, p.CatalogKey)
	if err != nil {
		return nil, err
	}
	offers, err := s.repo.StoreOffers(ctx, []string{p.CatalogKey},
		SearchParams{Lat: p.Lat, Lng: p.Lng, RadiusM: p.RadiusM, Sort: p.Sort}, s.staleBefore())
	if err != nil {
		return nil, err
	}
	stores := groupOffers(offers, debug)[p.CatalogKey]
	if stores == nil {
		stores = []StoreOffer{}
	}
	return &ProductNearby{
		SearchProduct: SearchProduct{Product: *product, Stores: stores},
		Lat:           p.Lat, Lng: p.Lng, RadiusM: p.RadiusM, Sort: p.Sort,
	}, nil
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// Store returns what customers see about a store. A closed store is returned
// with IsOpen false; one customers can't see at all is ErrNotFound.
func (s *service) Store(ctx context.Context, storeID uuid.UUID) (*Store, error) {
	row, err := s.repo.FindStore(ctx, storeID)
	if err != nil {
		return nil, err
	}
	store := toStore(*row)
	return &store, nil
}

func toStore(row storeRow) Store {
	store := Store{
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		AddressLine: row.AddressLine,
		City:        row.City,
		Pincode:     row.Pincode,
		Lat:         row.Lat,
		Lng:         row.Lng,
		IsOpen:      row.Status == "ACTIVE" && row.IsOpen,
	}
	if row.Status == "VACATION" {
		store.VacationUntil = row.VacationUntil
	}
	return store
}

// NearbyStores lists the stores customers may see within the radius, nearest
// first, closed ones included with IsOpen false.
func (s *service) NearbyStores(ctx context.Context, p NearbyStoresParams) (*NearbyStoresResult, error) {
	// Stores are always nearest first; validateArea only needs somewhere to put a sort.
	var sort Sort
	if err := validateArea(&p.Lat, &p.Lng, &p.RadiusM, &sort, DefaultRadiusM); err != nil {
		return nil, err
	}
	switch {
	case p.Limit == 0:
		p.Limit = DefaultNearbyStoresLimit
	case p.Limit < 0 || p.Limit > MaxNearbyStoresLimit:
		return nil, invalid(fmt.Sprintf("limit must be between 1 and %d", MaxNearbyStoresLimit))
	}
	rows, err := s.repo.NearbyStores(ctx, p, s.staleBefore())
	if err != nil {
		return nil, err
	}
	result := &NearbyStoresResult{Lat: p.Lat, Lng: p.Lng, RadiusM: p.RadiusM, Stores: []NearbyStore{}}
	if len(rows) > p.Limit {
		rows = rows[:p.Limit]
		result.HasMore = true
	}
	for _, row := range rows {
		result.Stores = append(result.Stores, NearbyStore{
			Store:          toStore(row.Store),
			Category:       row.Category,
			DistanceM:      int(math.Round(row.DistanceM)),
			CoverImageURL:  row.CoverImageURL,
			ProductCount:   row.ProductCount,
			AvailableCount: row.AvailableCount,
		})
	}
	return result, nil
}

// StoreProducts pages through the products customers can find at the store.
// A closed store has none.
func (s *service) StoreProducts(
	ctx context.Context, storeID uuid.UUID, query, rawCursor string, limit int, debug bool,
) (*StoreProductsPage, error) {
	switch {
	case limit == 0:
		limit = DefaultStoreProductsLimit
	case limit < 0 || limit > MaxStoreProductsLimit:
		return nil, invalid(fmt.Sprintf("limit must be between 1 and %d", MaxStoreProductsLimit))
	}
	after, err := decodeCursor(rawCursor)
	if err != nil {
		return nil, err
	}
	var q *textQuery
	if query = strings.TrimSpace(query); query != "" {
		if n := utf8.RuneCountInString(query); n < MinQueryLength || n > MaxQueryLength {
			return nil, invalid(fmt.Sprintf("q must be %d to %d characters", MinQueryLength, MaxQueryLength))
		}
		if q = parseTextQuery(query); q == nil {
			return nil, invalid("q must contain letters or digits")
		}
	}
	store, err := s.Store(ctx, storeID)
	if err != nil {
		return nil, err
	}
	page := &StoreProductsPage{Products: []StoreProduct{}}
	if !store.IsOpen {
		return page, nil
	}
	rows, err := s.repo.StoreProducts(ctx, StoreProductsParams{StoreID: storeID, Limit: limit, After: after}, q, s.staleBefore())
	if err != nil {
		return nil, err
	}
	if len(rows) > limit {
		rows = rows[:limit]
		page.HasMore = true
		last := rows[len(rows)-1]
		page.NextCursor = cursor{Name: last.SortName, InventoryID: last.InventoryID}.encode()
	}
	for _, row := range rows {
		page.Products = append(page.Products, toStoreProduct(row, debug))
	}
	return page, nil
}

// catalogKeyPattern matches the keys store_product_availability can hold.
var catalogKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*:[A-Za-z0-9._-]{1,64}$`)

// ValidCatalogKey reports whether key has the shape of a catalogue key.
func ValidCatalogKey(key string) bool { return catalogKeyPattern.MatchString(key) }

// StoreProduct returns one product at one store. Unknown, deleted, closed and
// unlisted all look the same: ErrNotFound.
func (s *service) StoreProduct(ctx context.Context, storeID uuid.UUID, catalogKey string, debug bool) (*StoreProduct, error) {
	if !catalogKeyPattern.MatchString(catalogKey) {
		return nil, ErrNotFound
	}
	row, err := s.repo.StoreProduct(ctx, storeID, catalogKey, s.staleBefore())
	if err != nil {
		return nil, err
	}
	product := toStoreProduct(*row, debug)
	return &product, nil
}

func toStoreProduct(row storeProductRow, debug bool) StoreProduct {
	return StoreProduct{
		Product:            row.Product,
		StoreID:            row.StoreID,
		Price:              row.Price,
		AvailabilityBucket: row.AvailabilityBucket,
		MaxOrderQuantity:   availability.OrderableQuantity(row.AvailableQty),
		LastStockUpdateAt:  row.LastStockUpdateAt,
		Version:            row.Version,
		Debug:              debugFields(debug, row.AvailableQty, row.Version),
	}
}

func debugFields(debug bool, qty int, version int64) *Debug {
	if !debug {
		return nil
	}
	return &Debug{AvailableQty: qty, Version: version}
}
