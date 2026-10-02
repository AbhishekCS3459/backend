package productcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// asJSON renders a BSON value as relaxed extended JSON for readable assertions.
func asJSON(t *testing.T, v any) string {
	t.Helper()
	out, err := bson.MarshalExtJSON(bson.D{{Key: "v", Value: v}}, false, false)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(out)
}

func TestSearchStage(t *testing.T) {
	stage := asJSON(t, searchStage("stiker book", Filters{Brand: "Maple Press", InStock: true, MaxPrice: 99}))
	for _, want := range []string{
		`"index":"product_search"`,
		`"phrase":{"query":"stiker book","path":"name"`,
		`"matchCriteria":"all"`,
		`"fuzzy":{"maxEdits":1,"prefixLength":1,"maxExpansions":50}`,
		`"minimumShouldMatch":1`,
		`{"equals":{"path":"brand","value":"Maple Press"}}`,
		`{"equals":{"path":"inStock","value":true}}`,
		`{"range":{"path":"price","lte":99.0}}`,
		`"count":{"type":"total"}`,
	} {
		if !strings.Contains(stage, want) {
			t.Errorf("search stage missing %s\n%s", want, stage)
		}
	}
}

func TestSearchStageWithoutFilters(t *testing.T) {
	if stage := asJSON(t, searchStage("maggi", Filters{})); strings.Contains(stage, `"filter"`) {
		t.Errorf("unfiltered search has a filter clause: %s", stage)
	}
}

func TestAutocompleteStageFuzzyOnlyForLongerQueries(t *testing.T) {
	if stage := asJSON(t, autocompleteStage("sti", Filters{})); strings.Contains(stage, `"fuzzy"`) {
		t.Errorf("3-letter autocomplete is fuzzy: %s", stage)
	}
	stage := asJSON(t, autocompleteStage("stik", Filters{Brand: "Maple Press"}))
	for _, want := range []string{
		`"tokenOrder":"sequential"`,
		`"path":"brand"`,
		`"fuzzy"`,
		`{"equals":{"path":"brand","value":"Maple Press"}}`,
	} {
		if !strings.Contains(stage, want) {
			t.Errorf("autocomplete stage missing %s\n%s", want, stage)
		}
	}
	if strings.Contains(stage, `"count"`) {
		t.Errorf("autocomplete counts matches: %s", stage)
	}
	if strings.Contains(stage, `"must"`) {
		t.Errorf("single-word autocomplete requires words: %s", stage)
	}
}

func TestAutocompleteStageRequiresFinishedWords(t *testing.T) {
	stage := asJSON(t, autocompleteStage("amul gold m", Filters{}))
	want := `"must":[{"text":{"query":"amul gold","path":["name","brand"],"matchCriteria":"all"`
	if !strings.Contains(stage, want) {
		t.Errorf("autocomplete stage missing %s\n%s", want, stage)
	}
}

func TestSearchFilters(t *testing.T) {
	got := asJSON(t, searchFilters(Filters{
		Group:      "Snacks & Drinks",
		Collection: "Chips & Namkeen",
		Category:   "Chips & Crisps",
		MinPrice:   10,
		MaxPrice:   50,
	}))
	want := `{"v":[` +
		`{"equals":{"path":"categories.group","value":"Snacks & Drinks"}},` +
		`{"equals":{"path":"categories.collection","value":"Chips & Namkeen"}},` +
		`{"equals":{"path":"categories.name","value":"Chips & Crisps"}},` +
		`{"range":{"path":"price","gte":10.0,"lte":50.0}}]}`
	if got != want {
		t.Errorf("searchFilters\n got %s\nwant %s", got, want)
	}
}

func TestSearchPipelineReturnsPageAndTotal(t *testing.T) {
	pipeline := asJSON(t, searchPipeline(SearchParams{Query: "amul milk", Offset: 24, Limit: 24}))
	for _, want := range []string{
		`{"$skip":24}`,
		`{"$limit":24}`,
		`"score":{"$meta":"searchScore"}`,
		`{"$replaceWith":"$$SEARCH_META"}`,
	} {
		if !strings.Contains(pipeline, want) {
			t.Errorf("pipeline missing %s\n%s", want, pipeline)
		}
	}
}

func TestSearchIndexDefinition(t *testing.T) {
	def := asJSON(t, SearchIndexDefinition())
	for _, want := range []string{
		`"dynamic":false`,
		`"type":"autocomplete"`,
		`"tokenization":"edgeGram"`,
		`{"type":"porterStemming"}`,
		`"type":"token"`,
	} {
		if !strings.Contains(def, want) {
			t.Errorf("index definition missing %s", want)
		}
	}
}

func TestSearchHitJSON(t *testing.T) {
	hit := SearchHit{Product: Product{ID: "x", Name: "Sticker Book"}, Score: 2.5}
	out, err := json.Marshal(hit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"name":"Sticker Book"`) || !strings.Contains(string(out), `"score":2.5`) {
		t.Errorf("SearchHit JSON = %s", out)
	}
}

func TestSearchHitDecodesInlineProduct(t *testing.T) {
	raw, err := bson.Marshal(bson.D{{Key: "_id", Value: "x"}, {Key: "name", Value: "Maggi"}, {Key: "score", Value: 3.0}})
	if err != nil {
		t.Fatal(err)
	}
	var hit SearchHit
	if err := bson.Unmarshal(raw, &hit); err != nil {
		t.Fatal(err)
	}
	if hit.ID != "x" || hit.Name != "Maggi" || hit.Score != 3 {
		t.Errorf("decoded %+v", hit)
	}
}

type fakeRepository struct {
	Repository
	list         ListParams
	search       SearchParams
	suggestQuery string
	suggestLimit int
}

func (f *fakeRepository) List(_ context.Context, params ListParams) (Page, error) {
	f.list = params
	return Page{}, nil
}

func (f *fakeRepository) Search(_ context.Context, params SearchParams) (SearchResult, error) {
	f.search = params
	return SearchResult{}, nil
}

func (f *fakeRepository) Autocomplete(_ context.Context, query string, limit int, _ Filters) ([]Suggestion, error) {
	f.suggestQuery, f.suggestLimit = query, limit
	return []Suggestion{{Name: "Sticker Book"}}, nil
}

func TestServiceSearchValidation(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo)
	ctx := context.Background()

	for _, q := range []string{"", "   ", strings.Repeat("a", MaxQueryLength+1)} {
		if _, err := svc.Search(ctx, SearchParams{Query: q}); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("Search(%q) error = %v, want ErrInvalidQuery", q, err)
		}
	}

	if _, err := svc.Search(ctx, SearchParams{Query: "  sticker book ", Offset: -5, Limit: 500}); err != nil {
		t.Fatal(err)
	}
	if got := repo.search; got.Query != "sticker book" || got.Offset != 0 || got.Limit != MaxLimit {
		t.Errorf("normalized params = %+v", got)
	}
	if _, err := svc.Search(ctx, SearchParams{Query: "x", Offset: 1 << 30}); err != nil {
		t.Fatal(err)
	}
	if repo.search.Offset != MaxSearchOffset {
		t.Errorf("offset = %d, want %d", repo.search.Offset, MaxSearchOffset)
	}
}

func TestServiceListOffset(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo)
	ctx := context.Background()

	for _, tc := range []struct {
		name  string
		in    ListParams
		after string
		want  int
	}{
		{"negative", ListParams{Offset: -24}, "", 0},
		{"page jump", ListParams{Offset: 480}, "", 480},
		{"too deep", ListParams{Offset: 1 << 30}, "", MaxListOffset},
		{"cursor wins", ListParams{Offset: 480, After: " blinkit:x:y:1 "}, "blinkit:x:y:1", 0},
	} {
		if _, err := svc.List(ctx, tc.in); err != nil {
			t.Fatal(err)
		}
		if repo.list.Offset != tc.want || repo.list.After != tc.after {
			t.Errorf("%s: offset = %d, after = %q; want %d, %q", tc.name, repo.list.Offset, repo.list.After, tc.want, tc.after)
		}
	}
}

func TestServiceAutocomplete(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo)
	ctx := context.Background()

	got, err := svc.Autocomplete(ctx, " s ", 0, Filters{})
	if err != nil || got == nil || len(got) != 0 || repo.suggestQuery != "" {
		t.Errorf("short query: got %v, %v; repo called with %q", got, err, repo.suggestQuery)
	}

	if _, err := svc.Autocomplete(ctx, " sti ", 100, Filters{}); err != nil {
		t.Fatal(err)
	}
	if repo.suggestQuery != "sti" || repo.suggestLimit != MaxSuggestions {
		t.Errorf("repo called with %q, %d", repo.suggestQuery, repo.suggestLimit)
	}

	if _, err := svc.Autocomplete(ctx, "x", 0, Filters{Brand: strings.Repeat("b", 200)}); !errors.Is(err, ErrInvalidFilter) {
		t.Errorf("bad filter error = %v", err)
	}
}
