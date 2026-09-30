// Package productcatalog serves the global product catalogue: quick-commerce
package productcatalog

import (
	"errors"
	"regexp"
	"strings"
	"unicode"

	"go.mongodb.org/mongo-driver/v2/bson"
)

var (
	ErrUnavailable   = errors.New("product catalogue is unavailable")
	ErrTimeout       = errors.New("catalogue query timed out")
	ErrNotFound      = errors.New("product not found")
	ErrInvalidFilter = errors.New("invalid catalogue filter")
	ErrInvalidQuery  = errors.New("invalid search query")
)

const (
	DefaultLimit   = 24
	MaxLimit       = 60
	maxKeywords    = 5
	maxFilterValue = 150
	skuPrefix      = "TDZ-"

	MaxQueryLength = 200
	// MaxSearchOffset bounds offset paging: $skip still reads every skipped result.
	MaxSearchOffset    = 1_000
	DefaultSuggestions = 8
	MaxSuggestions     = 15
	// MinAutocompleteLength matches the shortest indexed edge n-gram.
	MinAutocompleteLength = 2
)

// Product is the catalogue item returned to clients.
type Product struct {
	ID              string     `json:"id" bson:"_id"`
	SKU             string     `json:"sku" bson:"-"`
	ProductID       string     `json:"product_id" bson:"productId"`
	Name            string     `json:"name" bson:"name"`
	Brand           string     `json:"brand" bson:"brand"`
	Unit            string     `json:"unit" bson:"unit"`
	Price           float64    `json:"price" bson:"price"`
	MRP             float64    `json:"mrp" bson:"mrp"`
	DiscountPercent float64    `json:"discount_percent" bson:"discountPercent"`
	InStock         bool       `json:"in_stock" bson:"inStock"`
	ImageURL        string     `json:"image_url" bson:"imageUrl"`
	Category        string     `json:"category" bson:"-"`
	Categories      []Category `json:"categories" bson:"categories"`
}

// Category is one place a product is listed, from the source's own taxonomy:
// Group › Collection › Name, e.g. "Snacks & Drinks › Chips & Namkeen › Chips & Crisps".
type Category struct {
	ID         int    `json:"id" bson:"id"`
	Name       string `json:"name" bson:"name"`
	Collection string `json:"collection" bson:"collection"`
	Group      string `json:"group" bson:"group"`
}

// finish fills the fields derived from the stored document.
func (p *Product) finish() {
	if p.Categories == nil {
		p.Categories = []Category{}
	}
	if len(p.Categories) > 0 {
		p.Category = p.Categories[0].Name
	}
	p.SKU = productSKU(p.ProductID, p.ID)
}

// productSKU is the Todayz SKU for a catalogue product. It depends only on the
// product id, so the same item scraped in two localities resolves to one
// inventory SKU, and it never names the source platform.
func productSKU(productID, id string) string {
	if productID == "" {
		productID = id[strings.LastIndex(id, ":")+1:]
	}
	return skuPrefix + productID
}

// CatalogKeySource namespaces catalogue keys. Like the Todayz SKU, the key never
// names the platform a product was scraped from.
const CatalogKeySource = "todayz"

// CatalogKey is product_variant.catalog_key for a catalogue product id: the
// key that groups every retailer's variant of the product in search.
func CatalogKey(productID string) string {
	return CatalogKeySource + ":" + productID
}

// CatalogKeyProductID returns the product id in a key made by CatalogKey.
func CatalogKeyProductID(key string) (string, bool) {
	id, ok := strings.CutPrefix(key, CatalogKeySource+":")
	return id, ok && ValidProductID(id)
}

// ValidProductID reports whether id has the shape of a catalogue product id.
func ValidProductID(id string) bool {
	return len(id) <= maxProductIDLen && allDigits(id)
}

// skuProductID returns the product id inside a Todayz SKU such as "TDZ-776963".
func skuProductID(query string) (string, bool) {
	if len(query) <= len(skuPrefix) || !strings.EqualFold(query[:len(skuPrefix)], skuPrefix) {
		return "", false
	}
	id := query[len(skuPrefix):]
	return id, ValidProductID(id)
}

// ListParams selects one page of the catalogue, ordered by _id.
type ListParams struct {
	Query   string
	After   string
	Limit   int
	Filters Filters
}

// Filters narrow the catalogue by exact field values. Zero values match everything.
type Filters struct {
	Group      string  // categories.group, e.g. "Snacks & Drinks"
	Collection string  // categories.collection, e.g. "Chips & Namkeen"
	Category   string  // categories.name, e.g. "Chips & Crisps"
	Brand      string  // e.g. "Lay's"
	InStock    bool    // only products in stock at the source
	MinPrice   float64 // market price bounds in rupees; 0 means no bound
	MaxPrice   float64
}

// Page is a keyset-paginated slice of the catalogue.
type Page struct {
	Items      []Product `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
	HasMore    bool      `json:"has_more"`
	// TotalEstimate is the approximate number of matches, omitted when counting
	// took too long. TotalCapped means there are at least that many.
	TotalEstimate int64 `json:"total_estimate,omitempty"`
	TotalCapped   bool  `json:"total_capped,omitempty"`
}

// SearchParams selects one page of relevance-ranked search results.
type SearchParams struct {
	Query   string
	Offset  int
	Limit   int
	Filters Filters
}

// SearchHit is a product with its relevance score (0 in keyword mode).
type SearchHit struct {
	Product `bson:",inline"`
	Score   float64 `json:"score" bson:"score"`
}

// SearchResult is one page of search results. Total counts every match;
// TotalCapped means there are at least that many (keyword mode only).
type SearchResult struct {
	Items       []SearchHit `json:"items"`
	Total       int64       `json:"total"`
	TotalCapped bool        `json:"total_capped,omitempty"`
	Mode        string      `json:"mode"`
}

// Suggestion is one autocomplete entry: a distinct product name.
type Suggestion struct {
	ID       string `json:"id"`
	SKU      string `json:"sku"`
	Name     string `json:"name"`
	Brand    string `json:"brand"`
	Category string `json:"category"`
	ImageURL string `json:"image_url"`
}

// FilterOption is one selectable value of a catalogue filter.
type FilterOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// FilterOptions lists the values clients can pass to the List filters.
type FilterOptions struct {
	Groups      []FilterOption `json:"groups"`
	Collections []FilterOption `json:"collections"`
	Categories  []FilterOption `json:"categories"`
	Brands      []FilterOption `json:"brands"`
}

// normalizeFilters validates client-supplied filters. Values are only ever used
// as equality operands, so validation is about rejecting junk early, not injection.
func normalizeFilters(f Filters) (Filters, error) {
	for _, value := range []*string{&f.Group, &f.Collection, &f.Category, &f.Brand} {
		*value = strings.TrimSpace(*value)
		if len(*value) > maxFilterValue {
			return Filters{}, ErrInvalidFilter
		}
	}
	if f.MinPrice < 0 || f.MaxPrice < 0 || (f.MaxPrice > 0 && f.MinPrice > f.MaxPrice) {
		return Filters{}, ErrInvalidFilter
	}
	return f, nil
}

// priceRange is the price condition for f, or nil when f has no price bounds.
func priceRange(f Filters) bson.D {
	bounds := bson.D{}
	if f.MinPrice > 0 {
		bounds = append(bounds, bson.E{Key: "$gte", Value: f.MinPrice})
	}
	if f.MaxPrice > 0 {
		bounds = append(bounds, bson.E{Key: "$lte", Value: f.MaxPrice})
	}
	if len(bounds) == 0 {
		return nil
	}
	return bounds
}

// apostrophes are dropped rather than treated as separators, so "Lay's" is one word.
var apostrophes = strings.NewReplacer("'", "", "’", "")

// keywords splits free text into lowercase words of letters and digits.
func keywords(query string) []string {
	fields := strings.FieldsFunc(apostrophes.Replace(strings.ToLower(query)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, word := range fields {
		if len([]rune(word)) < 2 || seen[word] {
			continue
		}
		seen[word] = true
		out = append(out, word)
		if len(out) == maxKeywords {
			break
		}
	}
	return out
}

// wordPattern matches word at the start of a word, tolerating an apostrophe
// between letters: "lays" matches "Lay's Chips" but not "Himalaya".
func wordPattern(word string) string {
	letters := make([]string, 0, len(word))
	for _, r := range word {
		letters = append(letters, regexp.QuoteMeta(string(r)))
	}
	return `\b` + strings.Join(letters, `['’]?`)
}

const maxProductIDLen = 20

// isProductID reports whether a bare search query looks like a platform
// product id. Short numbers are left to the text search; a "TDZ-" prefix makes
// the intent explicit instead (see skuProductID).
func isProductID(query string) bool {
	return len(query) >= 3 && len(query) <= maxProductIDLen && allDigits(query)
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
