package productcatalog

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	queryTimeout = 8 * time.Second

	// Filtered counts stop at countCap so a broad search cannot scan the whole
	// collection; the UI shows "10,000+" instead. They are cached per filter
	// set and catalogue size, so loading a scrape that adds products recounts
	// at once; the TTL covers uploads that only change existing products.
	countCap          = 10_000
	countTimeout      = 3 * time.Second
	countCacheTTL     = 10 * time.Minute
	countCacheEntries = 1_000

	// Filter options are exact counts over the matching documents, cached the
	// same way as filtered counts.
	facetTimeout      = 20 * time.Second
	facetCacheTTL     = 10 * time.Minute
	facetCacheEntries = 200
	maxBrandOptions   = 300
)

// Repository reads the catalogue collection.
type Repository interface {
	List(ctx context.Context, params ListParams) (Page, error)
	Get(ctx context.Context, id string) (Product, error)
	FilterOptions(ctx context.Context, filters Filters) (FilterOptions, error)
	Search(ctx context.Context, params SearchParams) (SearchResult, error)
	Autocomplete(ctx context.Context, query string, limit int, filters Filters) ([]Suggestion, error)
	ExistingProductIDs(ctx context.Context, ids []string) (map[string]bool, error)
	CanonicalProducts(ctx context.Context, ids []string) (map[string]CanonicalProduct, error)
}

type repository struct {
	col *mongo.Collection

	mu     sync.Mutex
	counts map[string]countEntry

	facetsMu sync.Mutex // serialises facet runs so concurrent requests share one result
	facets   map[Filters]facetEntry
}

type countEntry struct {
	n      int64
	capped bool
	exact  bool
	at     time.Time
	size   int64 // catalogue size when counted
}

type facetEntry struct {
	options FilterOptions
	at      time.Time
	size    int64
}

// NewRepository returns a Repository backed by col, or nil when col is nil.
func NewRepository(col *mongo.Collection) Repository {
	if col == nil {
		return nil
	}
	return &repository{col: col, counts: map[string]countEntry{}, facets: map[Filters]facetEntry{}}
}

var projection = bson.D{
	{Key: "productId", Value: 1},
	{Key: "name", Value: 1},
	{Key: "brand", Value: 1},
	{Key: "unit", Value: 1},
	{Key: "price", Value: 1},
	{Key: "mrp", Value: 1},
	{Key: "discountPercent", Value: 1},
	{Key: "inStock", Value: 1},
	{Key: "imageUrl", Value: 1},
	{Key: "categories", Value: 1},
}

func (r *repository) List(ctx context.Context, params ListParams) (Page, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	match := matchFilter(params.Query, params.Filters)
	totals := make(chan countEntry, 1)
	go func() { totals <- r.matchingTotal(ctx, params, match) }()

	filter := match
	if params.After != "" {
		filter = append(bson.D{{Key: "_id", Value: bson.D{{Key: "$gt", Value: params.After}}}}, match...)
	}

	findOptions := options.Find().
		SetSort(bson.D{{Key: "_id", Value: 1}}).
		SetLimit(int64(params.Limit + 1)).
		SetProjection(projection)
	if params.Offset > 0 {
		findOptions.SetSkip(int64(params.Offset))
	}
	cur, err := r.col.Find(ctx, filter, findOptions)
	if err != nil {
		return Page{}, classify(err)
	}
	defer func() { _ = cur.Close(ctx) }()

	page := Page{Items: make([]Product, 0, params.Limit)}
	scanned, lastID := 0, ""
	for cur.Next(ctx) {
		if scanned == params.Limit {
			page.HasMore = true
			break
		}
		scanned++
		lastID, _ = cur.Current.Lookup("_id").StringValueOK()
		var p Product
		if err := cur.Decode(&p); err != nil {
			// One malformed document must not fail the page; the cursor still advances past it.
			log.Warn().Err(err).Str("id", lastID).Msg("skipping undecodable catalogue product")
			continue
		}
		p.finish()
		page.Items = append(page.Items, p)
	}
	if err := cur.Err(); err != nil {
		return Page{}, classify(err)
	}
	if page.HasMore {
		page.NextCursor = lastID
	}
	total := <-totals
	page.TotalEstimate, page.TotalCapped, page.TotalExact = total.n, total.capped, total.exact
	return page, nil
}

// matchFilter builds the search and filter conditions for query and f.
func matchFilter(query string, f Filters) bson.D {
	filter := bson.D{}
	if search := searchFilter(query); search != nil {
		filter = append(filter, search...)
	}
	if f.Brand != "" {
		filter = append(filter, bson.E{Key: "brand", Value: f.Brand})
	}
	// $elemMatch keeps group, collection and name on the same category entry;
	// separate conditions could each match a different entry of the array.
	if category := categoryMatch(f); len(category) > 0 {
		filter = append(filter, bson.E{Key: "categories", Value: bson.D{{Key: "$elemMatch", Value: category}}})
	}
	if f.InStock {
		filter = append(filter, bson.E{Key: "inStock", Value: true})
	}
	if price := priceRange(f); price != nil {
		filter = append(filter, bson.E{Key: "price", Value: price})
	}
	sort.Slice(filter, func(i, j int) bool { return filter[i].Key < filter[j].Key })
	return filter
}

func categoryMatch(f Filters) bson.D {
	match := bson.D{}
	for _, c := range []struct{ key, value string }{
		{"group", f.Group},
		{"collection", f.Collection},
		{"name", f.Category},
	} {
		if c.value != "" {
			match = append(match, bson.E{Key: c.key, Value: c.value})
		}
	}
	return match
}

// searchFilter requires every word to start a word in the name or brand. A
// numeric query or a Todayz SKU also matches a product id exactly.
func searchFilter(query string) bson.D {
	query = strings.TrimSpace(query)
	if id, ok := skuProductID(query); ok {
		return bson.D{{Key: "productId", Value: id}}
	}
	words := keywords(query)
	clauses := make(bson.A, 0, len(words))
	for _, word := range words {
		re := bson.Regex{Pattern: wordPattern(word), Options: "i"}
		clauses = append(clauses, bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: "name", Value: re}},
			bson.D{{Key: "brand", Value: re}},
		}}})
	}
	var text bson.D
	switch len(clauses) {
	case 0:
	case 1:
		text = clauses[0].(bson.D)
	default:
		text = bson.D{{Key: "$and", Value: clauses}}
	}

	if !isProductID(query) {
		return text
	}
	alternatives := bson.A{bson.D{{Key: "productId", Value: query}}}
	if text != nil {
		alternatives = append(alternatives, text)
	}
	return bson.D{{Key: "$or", Value: alternatives}}
}

// matchingTotal counts matches for the pagination summary. It never fails the
// page: a slow or failed count just leaves the total unknown.
func (r *repository) matchingTotal(ctx context.Context, params ListParams, match bson.D) countEntry {
	size, ok := r.size(ctx)
	if len(match) == 0 {
		return countEntry{n: size, exact: ok}
	}

	key := fmt.Sprintf("%q|%+v", params.Query, params.Filters)
	r.mu.Lock()
	cached, cachedOK := r.counts[key]
	r.mu.Unlock()
	if ok && cachedOK && cached.size == size && time.Since(cached.at) < countCacheTTL {
		return cached
	}

	countCtx, cancel := context.WithTimeout(ctx, countTimeout)
	defer cancel()
	n, err := r.col.CountDocuments(countCtx, match, options.Count().SetLimit(countCap))

	entry := countEntry{n: n, capped: n >= countCap, exact: n < countCap, at: time.Now(), size: size}
	if err != nil {
		if ctx.Err() != nil {
			return countEntry{} // the request itself ended; the count says nothing
		}
		// Remember "unknown" too, so paging through a slow search pays the
		// count timeout once rather than on every page.
		log.Debug().Err(err).Str("filter", key).Msg("catalogue count skipped")
		entry = countEntry{at: time.Now(), size: size}
	}
	if !ok {
		return entry // without the size, the entry could never be validated
	}
	r.mu.Lock()
	if len(r.counts) >= countCacheEntries {
		clear(r.counts)
	}
	r.counts[key] = entry
	r.mu.Unlock()
	return entry
}

func (r *repository) Get(ctx context.Context, id string) (Product, error) {
	var p Product
	err := r.col.FindOne(ctx, bson.D{{Key: "_id", Value: id}}, options.FindOne().SetProjection(projection)).Decode(&p)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Product{}, ErrNotFound
	}
	if err != nil {
		return Product{}, classify(err)
	}
	p.finish()
	return p, nil
}

// ExistingProductIDs returns which of ids are in the catalogue. A product is
// stored once per locality, so it is matched by productId, not _id.
func (r *repository) ExistingProductIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	found := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return found, nil
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	var existing []string
	err := r.col.Distinct(ctx, "productId", bson.D{{Key: "productId", Value: bson.D{{Key: "$in", Value: ids}}}}).
		Decode(&existing)
	if err != nil {
		return nil, classify(err)
	}
	for _, id := range existing {
		found[id] = true
	}
	return found, nil
}

// size returns the number of products in the catalogue, read from collection
// metadata: one cheap round trip that stays exact on a replica set, so it is
// read on every request instead of cached. ok is false when it failed.
func (r *repository) size(ctx context.Context) (n int64, ok bool) {
	n, err := r.col.EstimatedDocumentCount(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Warn().Err(err).Msg("catalogue size unavailable")
		}
		return 0, false
	}
	return n, true
}

// FilterOptions returns the values of each filter among products matching the
// other selected filters, so picking a group narrows the collections offered.
func (r *repository) FilterOptions(ctx context.Context, filters Filters) (FilterOptions, error) {
	size, sized := r.size(ctx)
	if options, ok := r.cachedFacets(filters, size, sized); ok {
		return options, nil
	}

	r.facetsMu.Lock()
	defer r.facetsMu.Unlock()
	if options, ok := r.cachedFacets(filters, size, sized); ok {
		return options, nil
	}

	// Detached from the request: the result is shared, so one impatient client
	// cancelling must not waste a run everyone else is waiting on.
	facetCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), facetTimeout)
	defer cancel()

	options, err := r.computeFacets(facetCtx, filters)
	if err != nil {
		return FilterOptions{}, classify(err)
	}
	if !sized {
		return options, nil
	}
	r.mu.Lock()
	if len(r.facets) >= facetCacheEntries {
		clear(r.facets)
	}
	r.facets[filters] = facetEntry{options: options, at: time.Now(), size: size}
	r.mu.Unlock()
	return options, nil
}

func (r *repository) cachedFacets(filters Filters, size int64, sized bool) (FilterOptions, bool) {
	if !sized {
		return FilterOptions{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.facets[filters]
	return entry.options, ok && entry.size == size && time.Since(entry.at) < facetCacheTTL
}

type valueCount struct {
	Value any `bson:"_id"`
	N     int `bson:"n"`
}

func (r *repository) computeFacets(ctx context.Context, f Filters) (FilterOptions, error) {
	// field facets count documents per value of a scalar field.
	field := func(name string, without Filters, limit int) bson.A {
		stages := bson.A{
			bson.D{{Key: "$match", Value: matchFilter("", without)}},
			bson.D{{Key: "$group", Value: bson.D{{Key: "_id", Value: "$" + name}, {Key: "n", Value: bson.D{{Key: "$sum", Value: 1}}}}}},
			bson.D{{Key: "$sort", Value: bson.D{{Key: "n", Value: -1}, {Key: "_id", Value: 1}}}},
		}
		if limit > 0 {
			stages = append(stages, bson.D{{Key: "$limit", Value: limit}})
		}
		return stages
	}
	// category facets count documents per value of one level of the category
	// tree, among entries that also match the other selected levels.
	category := func(level string, without Filters) bson.A {
		stages := bson.A{
			bson.D{{Key: "$match", Value: matchFilter("", without)}},
			bson.D{{Key: "$unwind", Value: "$categories"}},
		}
		if sibling := categoryMatch(without); len(sibling) > 0 {
			prefixed := make(bson.D, 0, len(sibling))
			for _, e := range sibling {
				prefixed = append(prefixed, bson.E{Key: "categories." + e.Key, Value: e.Value})
			}
			stages = append(stages, bson.D{{Key: "$match", Value: prefixed}})
		}
		return append(stages,
			// A product listed twice under the same value counts once.
			bson.D{{Key: "$group", Value: bson.D{{Key: "_id", Value: bson.D{
				{Key: "v", Value: "$categories." + level},
				{Key: "d", Value: "$_id"},
			}}}}},
			bson.D{{Key: "$group", Value: bson.D{{Key: "_id", Value: "$_id.v"}, {Key: "n", Value: bson.D{{Key: "$sum", Value: 1}}}}}},
		)
	}

	without := func(unset func(*Filters)) Filters {
		others := f
		unset(&others)
		return others
	}

	cur, err := r.col.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$project", Value: bson.D{
			{Key: "brand", Value: 1},
			{Key: "categories", Value: 1},
			{Key: "inStock", Value: 1},
			{Key: "price", Value: 1},
		}}},
		{{Key: "$facet", Value: bson.D{
			{Key: "brands", Value: field("brand", without(func(c *Filters) { c.Brand = "" }), maxBrandOptions)},
			{Key: "groups", Value: category("group", without(func(c *Filters) { c.Group = "" }))},
			{Key: "collections", Value: category("collection", without(func(c *Filters) { c.Collection = "" }))},
			{Key: "categories", Value: category("name", without(func(c *Filters) { c.Category = "" }))},
		}}},
	})
	if err != nil {
		return FilterOptions{}, err
	}
	defer func() { _ = cur.Close(ctx) }()

	var result struct {
		Brands      []valueCount `bson:"brands"`
		Groups      []valueCount `bson:"groups"`
		Collections []valueCount `bson:"collections"`
		Categories  []valueCount `bson:"categories"`
	}
	if !cur.Next(ctx) {
		if err := cur.Err(); err != nil {
			return FilterOptions{}, err
		}
		return FilterOptions{}, errors.New("facet aggregation returned no result")
	}
	if err := cur.Decode(&result); err != nil {
		return FilterOptions{}, fmt.Errorf("decode facets: %w", err)
	}

	return FilterOptions{
		Groups:      facetOptions(result.Groups),
		Collections: facetOptions(result.Collections),
		Categories:  facetOptions(result.Categories),
		Brands:      facetOptions(result.Brands),
	}, nil
}

// facetOptions turns value counts into options sorted by label.
func facetOptions(counts []valueCount) []FilterOption {
	out := make([]FilterOption, 0, len(counts))
	for _, c := range counts {
		value, ok := c.Value.(string)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		out = append(out, FilterOption{Value: value, Label: value, Count: c.N})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Label) < strings.ToLower(out[j].Label) })
	return out
}

func classify(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || mongo.IsTimeout(err) {
		return ErrTimeout
	}
	return fmt.Errorf("query catalogue: %w", err)
}
