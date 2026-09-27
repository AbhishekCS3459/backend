package productcatalog

import (
	"context"
	"strings"
	"unicode/utf8"
)

type Service interface {
	List(ctx context.Context, params ListParams) (Page, error)
	Get(ctx context.Context, id string) (Product, error)
	FilterOptions(ctx context.Context, filters Filters) (FilterOptions, error)
	Search(ctx context.Context, params SearchParams) (SearchResult, error)
	Autocomplete(ctx context.Context, query string, limit int, filters Filters) ([]Suggestion, error)
}

type service struct {
	repo Repository
}

// NewService returns a Service; a nil repo makes every call return ErrUnavailable.
func NewService(repo Repository) Service {
	return &service{repo: repo}
}

func (s *service) List(ctx context.Context, params ListParams) (Page, error) {
	if s.repo == nil {
		return Page{}, ErrUnavailable
	}
	params.Query = strings.TrimSpace(params.Query)
	params.After = strings.TrimSpace(params.After)
	params.Limit = clampLimit(params.Limit, DefaultLimit, MaxLimit)
	filters, err := normalizeFilters(params.Filters)
	if err != nil {
		return Page{}, err
	}
	params.Filters = filters
	return s.repo.List(ctx, params)
}

func (s *service) FilterOptions(ctx context.Context, filters Filters) (FilterOptions, error) {
	if s.repo == nil {
		return FilterOptions{}, ErrUnavailable
	}
	filters, err := normalizeFilters(filters)
	if err != nil {
		return FilterOptions{}, err
	}
	return s.repo.FilterOptions(ctx, filters)
}

func (s *service) Get(ctx context.Context, id string) (Product, error) {
	if s.repo == nil {
		return Product{}, ErrUnavailable
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Product{}, ErrNotFound
	}
	return s.repo.Get(ctx, id)
}

func (s *service) Search(ctx context.Context, params SearchParams) (SearchResult, error) {
	if s.repo == nil {
		return SearchResult{}, ErrUnavailable
	}
	params.Query = strings.TrimSpace(params.Query)
	if params.Query == "" || utf8.RuneCountInString(params.Query) > MaxQueryLength {
		return SearchResult{}, ErrInvalidQuery
	}
	params.Limit = clampLimit(params.Limit, DefaultLimit, MaxLimit)
	params.Offset = min(max(params.Offset, 0), MaxSearchOffset)
	filters, err := normalizeFilters(params.Filters)
	if err != nil {
		return SearchResult{}, err
	}
	params.Filters = filters
	return s.repo.Search(ctx, params)
}

// Autocomplete returns up to limit distinct product names for a partly typed
// query, or none when the query is shorter than MinAutocompleteLength.
func (s *service) Autocomplete(ctx context.Context, query string, limit int, filters Filters) ([]Suggestion, error) {
	if s.repo == nil {
		return nil, ErrUnavailable
	}
	query = strings.TrimSpace(query)
	n := utf8.RuneCountInString(query)
	if n > MaxQueryLength {
		return nil, ErrInvalidQuery
	}
	filters, err := normalizeFilters(filters)
	if err != nil {
		return nil, err
	}
	if n < MinAutocompleteLength {
		return []Suggestion{}, nil
	}
	return s.repo.Autocomplete(ctx, query, clampLimit(limit, DefaultSuggestions, MaxSuggestions), filters)
}

func clampLimit(limit, fallback, max int) int {
	if limit <= 0 {
		return fallback
	}
	return min(limit, max)
}
