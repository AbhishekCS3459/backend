package listproducts

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/AbhishekCS3459/find-me-backend/internal/productcatalog"
)

var (
	// ErrReservedSKU: TDZ- SKUs name catalogue products, so a product made by
	// hand can't use one and pass itself off as a catalogue product.
	ErrReservedSKU = errors.New("SKUs starting with TDZ- are reserved for products added from the product catalogue; choose another SKU")
	// ErrInvalidProduct is a hand-made product missing a required field.
	ErrInvalidProduct = errors.New("invalid product")
)

// invalidProduct is an ErrInvalidProduct with the reason shown to the user.
type invalidProduct struct{ reason string }

func (e invalidProduct) Error() string { return e.reason }
func (e invalidProduct) Is(target error) bool {
	return target == ErrInvalidProduct
}

// cleanSKU drops invisible formatting characters (zero-width spaces, BOMs,
// bidi marks) anywhere in the SKU and trims surrounding whitespace, so what is
// stored is what the retailer sees.
func cleanSKU(sku string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, sku))
}

// reservedSKU reports whether a cleaned SKU uses the catalogue prefix, in any case.
func reservedSKU(sku string) bool {
	prefix := productcatalog.SKUPrefix
	return len(sku) >= len(prefix) && strings.EqualFold(sku[:len(prefix)], prefix)
}

// validateManual checks a product made by hand and cleans its SKU in place.
func validateManual(req *CreateProductRequest) error {
	req.SKU = cleanSKU(req.SKU)
	fields := []struct {
		name, value string
		min         int
	}{
		{"name", req.Name, 2},
		{"brand", req.Brand, 1},
		{"category", req.Category, 1},
		{"sku", req.SKU, 2},
	}
	for _, f := range fields {
		if utf8.RuneCountInString(strings.TrimSpace(f.value)) < f.min {
			if f.min == 1 {
				return invalidProduct{f.name + " is required"}
			}
			return invalidProduct{f.name + " must be at least 2 characters"}
		}
	}
	if reservedSKU(req.SKU) {
		return ErrReservedSKU
	}
	return nil
}
