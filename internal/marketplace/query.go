package marketplace

import (
	"strings"
	"unicode"
)

// maxQueryWords bounds the words a query is matched on.
const maxQueryWords = 8

// apostrophes are dropped rather than treated as separators, so "Lay's" is one word.
var apostrophes = strings.NewReplacer("'", "", "’", "")

// textQuery is a search query normalised for each way it is matched. Every
// form holds only letters, digits and the operators added here, so it is safe
// to pass to to_tsquery and LIKE.
type textQuery struct {
	// text is the words separated by spaces, for trigram word similarity.
	text string
	// compact is the words run together, matched against name and brand with
	// punctuation removed: "coca cola", "coca-cola" and "cocacola" are equal.
	compact string
	// tsquery requires every word, each as a prefix: "coca:* & cola:*".
	tsquery string
}

// parseTextQuery returns nil when q has no letters or digits.
func parseTextQuery(q string) *textQuery {
	words := strings.FieldsFunc(apostrophes.Replace(strings.ToLower(q)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := make(map[string]bool, len(words))
	kept := make([]string, 0, min(len(words), maxQueryWords))
	for _, w := range words {
		if seen[w] {
			continue
		}
		seen[w] = true
		kept = append(kept, w)
		if len(kept) == maxQueryWords {
			break
		}
	}
	if len(kept) == 0 {
		return nil
	}
	prefixes := make([]string, len(kept))
	for i, w := range kept {
		prefixes[i] = w + ":*"
	}
	return &textQuery{
		text:    strings.Join(kept, " "),
		compact: strings.Join(kept, ""),
		tsquery: strings.Join(prefixes, " & "),
	}
}

// like matches the run-together words anywhere in search_compact.
func (q textQuery) like() string { return "%" + q.compact + "%" }
