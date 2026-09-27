package productcatalog

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	// autocompleteTimeout is short: a suggestion that arrives late is useless.
	autocompleteTimeout = 3 * time.Second
	// autocompleteCandidates are fetched before collapsing duplicate names; the
	// same product can be listed more than once.
	autocompleteCandidates = 50
	// fuzzyAutocompleteMinLen is the shortest query that also matches with a
	// typo. Below it one edit matches too many unrelated words.
	fuzzyAutocompleteMinLen = 4
)

// Search result modes.
const (
	ModeSearch  = "search"  // ranked by Atlas Search relevance
	ModeKeyword = "keyword" // regex word match ordered by id: SKU lookups, or no Atlas Search match
)

// fuzzy tolerates one typo per word ("stiker" → "sticker"). The first letter
// must match, which keeps the expansion small and the matches plausible.
var fuzzy = bson.D{
	{Key: "maxEdits", Value: 1},
	{Key: "prefixLength", Value: 1},
	{Key: "maxExpansions", Value: 50},
}

var categoryPaths = bson.A{"categories.name", "categories.collection", "categories.group"}

func boost(value float64) bson.D {
	return bson.D{{Key: "boost", Value: bson.D{{Key: "value", Value: value}}}}
}

// searchStage is the $search stage for a product search. Each clause adds to
// the score of the products it matches, so the exact phrase in the name ranks
// first, then all the words in the name, then brand and category matches, and
// typo matches last.
func searchStage(query string, f Filters) bson.D {
	text := func(path any, score float64, extra ...bson.E) bson.D {
		op := bson.D{{Key: "query", Value: query}, {Key: "path", Value: path}}
		op = append(op, extra...)
		op = append(op, bson.E{Key: "score", Value: boost(score)})
		return bson.D{{Key: "text", Value: op}}
	}
	should := bson.A{
		bson.D{{Key: "phrase", Value: bson.D{
			{Key: "query", Value: query},
			{Key: "path", Value: "name"},
			{Key: "score", Value: boost(6)},
		}}},
		text("name", 4, bson.E{Key: "matchCriteria", Value: "all"}),
		text("name", 2),
		text("brand", 3),
		text(categoryPaths, 1.5),
		text(bson.A{"name", "brand", "categories.name"}, 1, bson.E{Key: "fuzzy", Value: fuzzy}),
	}
	return bson.D{{Key: "$search", Value: bson.D{
		{Key: "index", Value: SearchIndexName},
		{Key: "compound", Value: compound(should, f)},
		{Key: "count", Value: bson.D{{Key: "type", Value: "total"}}},
	}}}
}

// autocompleteStage is the $search stage for suggestions while typing. Name
// matches in the typed word order rank above matches in any order.
func autocompleteStage(query string, f Filters) bson.D {
	autocomplete := func(path string, score float64, extra ...bson.E) bson.D {
		op := bson.D{{Key: "query", Value: query}, {Key: "path", Value: path}}
		op = append(op, extra...)
		op = append(op, bson.E{Key: "score", Value: boost(score)})
		return bson.D{{Key: "autocomplete", Value: op}}
	}
	should := bson.A{
		autocomplete("name", 6, bson.E{Key: "tokenOrder", Value: "sequential"}),
		autocomplete("name", 3),
		autocomplete("brand", 2),
	}
	if utf8.RuneCountInString(query) >= fuzzyAutocompleteMinLen {
		should = append(should, autocomplete("name", 1, bson.E{Key: "fuzzy", Value: fuzzy}))
	}
	c := compound(should, f)
	// Words before the last are finished: require them all, so "amul m"
	// suggests Amul products rather than anything with a word starting "m".
	if words := strings.Fields(query); len(words) > 1 {
		c = append(c, bson.E{Key: "must", Value: bson.A{bson.D{{Key: "text", Value: bson.D{
			{Key: "query", Value: strings.Join(words[:len(words)-1], " ")},
			{Key: "path", Value: bson.A{"name", "brand"}},
			{Key: "matchCriteria", Value: "all"},
			{Key: "fuzzy", Value: fuzzy},
		}}}}})
	}
	return bson.D{{Key: "$search", Value: bson.D{
		{Key: "index", Value: SearchIndexName},
		{Key: "compound", Value: c},
	}}}
}

func compound(should bson.A, f Filters) bson.D {
	c := bson.D{{Key: "should", Value: should}, {Key: "minimumShouldMatch", Value: 1}}
	if filter := searchFilters(f); len(filter) > 0 {
		c = append(c, bson.E{Key: "filter", Value: filter})
	}
	return c
}

// searchFilters are the Atlas Search equivalents of matchFilter's field
// conditions. They filter without affecting the score. Unlike matchFilter's
// $elemMatch, the category levels may match different entries of the
// categories array; a product listed under two categories can match a mix.
func searchFilters(f Filters) bson.A {
	equals := func(path string, value any) bson.D {
		return bson.D{{Key: "equals", Value: bson.D{{Key: "path", Value: path}, {Key: "value", Value: value}}}}
	}
	filter := bson.A{}
	for _, c := range []struct{ path, value string }{
		{"brand", f.Brand},
		{"categories.group", f.Group},
		{"categories.collection", f.Collection},
		{"categories.name", f.Category},
	} {
		if c.value != "" {
			filter = append(filter, equals(c.path, c.value))
		}
	}
	if f.InStock {
		filter = append(filter, equals("inStock", true))
	}
	if f.MinPrice > 0 || f.MaxPrice > 0 {
		bounds := bson.D{{Key: "path", Value: "price"}}
		if f.MinPrice > 0 {
			bounds = append(bounds, bson.E{Key: "gte", Value: f.MinPrice})
		}
		if f.MaxPrice > 0 {
			bounds = append(bounds, bson.E{Key: "lte", Value: f.MaxPrice})
		}
		filter = append(filter, bson.D{{Key: "range", Value: bounds}})
	}
	return filter
}

// searchPipeline returns one page of ranked results plus the total match
// count, which $search computes alongside the results ($$SEARCH_META).
func searchPipeline(params SearchParams) mongo.Pipeline {
	fields := append(bson.D{}, projection...)
	fields = append(fields, bson.E{Key: "score", Value: bson.D{{Key: "$meta", Value: "searchScore"}}})
	return mongo.Pipeline{
		searchStage(params.Query, params.Filters),
		{{Key: "$skip", Value: params.Offset}},
		{{Key: "$limit", Value: params.Limit}},
		{{Key: "$project", Value: fields}},
		{{Key: "$facet", Value: bson.D{
			{Key: "items", Value: bson.A{}},
			{Key: "meta", Value: bson.A{
				bson.D{{Key: "$replaceWith", Value: "$$SEARCH_META"}},
				bson.D{{Key: "$limit", Value: 1}},
			}},
		}}},
	}
}

var suggestionProjection = bson.D{
	{Key: "productId", Value: 1},
	{Key: "name", Value: 1},
	{Key: "brand", Value: 1},
	{Key: "imageUrl", Value: 1},
	{Key: "categories", Value: 1},
}

func autocompletePipeline(query string, f Filters) mongo.Pipeline {
	return mongo.Pipeline{
		autocompleteStage(query, f),
		{{Key: "$limit", Value: autocompleteCandidates}},
		{{Key: "$project", Value: suggestionProjection}},
	}
}

func (r *repository) Search(ctx context.Context, params SearchParams) (SearchResult, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	// A SKU or product id names one product; relevance ranking adds nothing.
	if _, sku := skuProductID(params.Query); sku || isProductID(params.Query) {
		return r.keywordSearch(ctx, params)
	}

	result, err := r.atlasSearch(ctx, params)
	switch {
	case ctx.Err() != nil:
		return SearchResult{}, classify(ctx.Err())
	case err != nil:
		// Keep search working if the index is missing or still building.
		log.Warn().Err(err).Msg("atlas search failed; using keyword search")
		return r.keywordSearch(ctx, params)
	case result.Total == 0 && params.Offset == 0:
		// Atlas Search drops some tokens a word-prefix regex still matches, such
		// as the start of a word ("stick" for "sticker").
		return r.keywordSearch(ctx, params)
	}
	return result, nil
}

func (r *repository) atlasSearch(ctx context.Context, params SearchParams) (SearchResult, error) {
	cur, err := r.col.Aggregate(ctx, searchPipeline(params))
	if err != nil {
		return SearchResult{}, err
	}
	defer func() { _ = cur.Close(ctx) }()

	var out struct {
		Items []bson.Raw `bson:"items"`
		Meta  []struct {
			Count struct {
				Total int64 `bson:"total"`
			} `bson:"count"`
		} `bson:"meta"`
	}
	if !cur.Next(ctx) {
		if err := cur.Err(); err != nil {
			return SearchResult{}, err
		}
		return SearchResult{}, errors.New("search aggregation returned no result")
	}
	if err := cur.Decode(&out); err != nil {
		return SearchResult{}, err
	}

	result := SearchResult{Items: make([]SearchHit, 0, len(out.Items)), Mode: ModeSearch}
	if len(out.Meta) > 0 {
		result.Total = out.Meta[0].Count.Total
	}
	for _, raw := range out.Items {
		var hit SearchHit
		if err := bson.Unmarshal(raw, &hit); err != nil {
			id, _ := raw.Lookup("_id").StringValueOK()
			log.Warn().Err(err).Str("id", id).Msg("skipping undecodable catalogue product")
			continue
		}
		hit.finish()
		result.Items = append(result.Items, hit)
	}
	return result, nil
}

// keywordSearch is the regex word match List uses, paged by offset.
func (r *repository) keywordSearch(ctx context.Context, params SearchParams) (SearchResult, error) {
	match := matchFilter(params.Query, params.Filters)
	totals := make(chan countEntry, 1)
	go func() {
		totals <- r.matchingTotal(ctx, ListParams{Query: params.Query, Filters: params.Filters}, match)
	}()

	cur, err := r.col.Find(ctx, match, options.Find().
		SetSort(bson.D{{Key: "_id", Value: 1}}).
		SetSkip(int64(params.Offset)).
		SetLimit(int64(params.Limit)).
		SetProjection(projection))
	if err != nil {
		return SearchResult{}, classify(err)
	}
	defer func() { _ = cur.Close(ctx) }()

	result := SearchResult{Items: make([]SearchHit, 0, params.Limit), Mode: ModeKeyword}
	for cur.Next(ctx) {
		var hit SearchHit
		if err := cur.Decode(&hit); err != nil {
			id, _ := cur.Current.Lookup("_id").StringValueOK()
			log.Warn().Err(err).Str("id", id).Msg("skipping undecodable catalogue product")
			continue
		}
		hit.finish()
		result.Items = append(result.Items, hit)
	}
	if err := cur.Err(); err != nil {
		return SearchResult{}, classify(err)
	}
	total := <-totals
	result.Total, result.TotalCapped = total.n, total.capped
	return result, nil
}

func (r *repository) Autocomplete(ctx context.Context, query string, limit int, f Filters) ([]Suggestion, error) {
	ctx, cancel := context.WithTimeout(ctx, autocompleteTimeout)
	defer cancel()

	suggestions, err := r.suggest(ctx, limit, func() (*mongo.Cursor, error) {
		return r.col.Aggregate(ctx, autocompletePipeline(query, f))
	})
	switch {
	case ctx.Err() != nil:
		return nil, classify(ctx.Err())
	case err != nil:
		log.Warn().Err(err).Msg("atlas autocomplete failed; using keyword suggestions")
	case len(suggestions) > 0:
		return suggestions, nil
	}
	suggestions, err = r.suggest(ctx, limit, func() (*mongo.Cursor, error) {
		return r.col.Find(ctx, matchFilter(query, f), options.Find().
			SetLimit(autocompleteCandidates).
			SetProjection(suggestionProjection))
	})
	if err != nil {
		return nil, classify(err)
	}
	return suggestions, nil
}

// suggest reads products from the cursor open returns and keeps the first
// limit distinct names, in cursor order.
func (r *repository) suggest(ctx context.Context, limit int, open func() (*mongo.Cursor, error)) ([]Suggestion, error) {
	cur, err := open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = cur.Close(ctx) }()

	out := make([]Suggestion, 0, limit)
	seen := make(map[string]bool, limit)
	for len(out) < limit && cur.Next(ctx) {
		var p Product
		if err := cur.Decode(&p); err != nil {
			continue
		}
		key := NormalizeText(p.Name)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		p.finish()
		out = append(out, Suggestion{
			ID:       p.ID,
			SKU:      p.SKU,
			Name:     p.Name,
			Brand:    p.Brand,
			Category: p.Category,
			ImageURL: p.ImageURL,
		})
	}
	return out, cur.Err()
}
