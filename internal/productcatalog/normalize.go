package productcatalog

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// SearchFields is the normalized copy of a product's searchable text, stored
// on each document under "search" by cmd/catalog-cleanup. Products that differ
// only in case, spacing, accents or punctuation ("Maggi 2-Minute Noodles",
// "MAGGI 2 MINUTE NOODLES") get identical values.
type SearchFields struct {
	Name  string `json:"name" bson:"name"`
	Brand string `json:"brand" bson:"brand"`
	// Category, Collection and Group are the product's primary (first) category.
	Category   string `json:"category" bson:"category"`
	Collection string `json:"collection" bson:"collection"`
	Group      string `json:"group" bson:"group"`
	// Text is the name, brand and every category level (all categories, not
	// only the primary one) in one string: the input for full-text search and,
	// later, for embeddings.
	Text string `json:"text" bson:"text"`
}

// SearchInput is the product data BuildSearchFields reads.
type SearchInput struct {
	Name, Brand string
	Categories  []Category
}

// BuildSearchFields normalizes a product's searchable fields.
func BuildSearchFields(in SearchInput) SearchFields {
	name, brand, categories := in.Name, in.Brand, in.Categories
	s := SearchFields{
		Name:  NormalizeText(name),
		Brand: NormalizeText(brand),
	}
	if len(categories) > 0 {
		s.Category = NormalizeText(categories[0].Name)
		s.Collection = NormalizeText(categories[0].Collection)
		s.Group = NormalizeText(categories[0].Group)
	}

	parts := []string{s.Name, s.Brand}
	for _, c := range categories {
		parts = append(parts, NormalizeText(c.Name), NormalizeText(c.Collection), NormalizeText(c.Group))
	}
	text := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		if part != "" && !seen[part] {
			seen[part] = true
			text = append(text, part)
		}
	}
	s.Text = strings.Join(text, " ")
	return s
}

// NormalizeText lowercases s, strips accents, and reduces punctuation to
// single spaces, keeping only characters that carry meaning in product names:
// "+" (1000+), "&" (stationery & games), "%" (70% dark), an apostrophe between
// letters (children's), and "." between digits (1.5 l).
func NormalizeText(s string) string {
	s, _, err := transform.String(transform.Chain(norm.NFKD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), s)
	if err != nil {
		return ""
	}
	in := []rune(strings.ToLower(s))
	var b strings.Builder
	b.Grow(len(in))
	pendingSpace := false
	write := func(r rune) {
		if pendingSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		pendingSpace = false
		b.WriteRune(r)
	}
	between := func(i int, is func(rune) bool) bool {
		return i > 0 && i < len(in)-1 && is(in[i-1]) && is(in[i+1])
	}
	for i, r := range in {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '+' || r == '&' || r == '%':
			write(r)
		case isApostrophe(r) && between(i, unicode.IsLetter):
			write('\'')
		case r == '.' && between(i, unicode.IsDigit):
			write('.')
		case r == ',' && between(i, unicode.IsDigit):
			// Thousands separator: "1,000" is "1000".
		default:
			pendingSpace = true
		}
	}
	return b.String()
}

func isApostrophe(r rune) bool {
	switch r {
	case '\'', '’', '‘', '`', '´', 'ʼ':
		return true
	}
	return false
}
