// Package catalogitem writes catalog_item, the canonical description of each
// product customers can find: one row per catalog_key, shared by every
// retailer that stocks it. It never holds a store's price or stock.
package catalogitem

import (
	"fmt"

	"github.com/AbhishekCS3459/find-me-backend/internal/productcatalog"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ManualSource is the source of a product a retailer made by hand; its key is
// variant:<variant id>, as in store_product_availability.
const ManualSource = "variant"

// Item is one catalog_item row, without its timestamps.
type Item struct {
	CatalogKey      string
	Source          string
	SourceProductID string
	Name            string
	Brand           string
	Unit            string
	CategoryPath    string
	ImageURL        string
}

// ManualKey is the catalog_key of a product made by hand.
func ManualKey(variantID uuid.UUID) string {
	return ManualSource + ":" + variantID.String()
}

// FromCatalogue is the row for a catalogue product.
func FromCatalogue(p productcatalog.CanonicalProduct) Item {
	return Item{
		CatalogKey:      p.CatalogKey(),
		Source:          productcatalog.CatalogKeySource,
		SourceProductID: p.ProductID,
		Name:            p.Name,
		Brand:           p.Brand,
		Unit:            p.Unit,
		CategoryPath:    p.CategoryPath,
		ImageURL:        p.ImageURL,
	}
}

// Manual is the row for a product made by hand.
func Manual(variantID uuid.UUID, name, brand, category, imageURL string) Item {
	return Item{
		CatalogKey:      ManualKey(variantID),
		Source:          ManualSource,
		SourceProductID: variantID.String(),
		Name:            name,
		Brand:           brand,
		CategoryPath:    category,
		ImageURL:        imageURL,
	}
}

// ManualItems reads the rows of the given variants that have no catalogue
// key, from their products. Variants that still have a key are skipped.
func ManualItems(tx *gorm.DB, variantIDs []uuid.UUID) ([]Item, error) {
	if len(variantIDs) == 0 {
		return nil, nil
	}
	var rows []struct {
		VariantID uuid.UUID
		Name      string
		Brand     string
		Category  string
		ImageURL  string
	}
	err := tx.Raw(`
		SELECT v.id AS variant_id, p.name, COALESCE(b.name, '') AS brand, COALESCE(c.name, '') AS category,
			COALESCE((
				SELECT pi.image_url FROM product_image pi
				WHERE pi.product_id = p.id
				ORDER BY pi.sort_order
				LIMIT 1
			), '') AS image_url
		FROM product_variant v
		JOIN product p ON p.id = v.product_id
		LEFT JOIN brand b ON b.id = p.brand_id
		LEFT JOIN category c ON c.id = p.category_id
		WHERE v.catalog_key IS NULL AND v.id IN ?
		ORDER BY v.id`, variantIDs).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read hand-made products: %w", err)
	}
	items := make([]Item, len(rows))
	for i, r := range rows {
		items[i] = Manual(r.VariantID, r.Name, r.Brand, r.Category, r.ImageURL)
	}
	return items, nil
}

// UpsertManual writes the rows of the given variants that have no catalogue
// key. Call it in the transaction that clears a variant's key, so the
// variant:<id> key its search rows switch to always has a product to show.
func UpsertManual(tx *gorm.DB, variantIDs []uuid.UUID) error {
	items, err := ManualItems(tx, variantIDs)
	if err != nil {
		return err
	}
	for _, item := range items {
		if _, err := Upsert(tx, item); err != nil {
			return err
		}
	}
	return nil
}

// Upsert inserts or updates the row and reports whether it changed. Safe when
// two retailers add the same catalogue product at once: the second waits for
// the first's row and then updates it to the same values. mrp is never written.
func Upsert(tx *gorm.DB, item Item) (bool, error) {
	res := tx.Exec(`
		INSERT INTO catalog_item AS c (catalog_key, source, source_product_id, name, brand, unit, category_path, image_url)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (catalog_key) DO UPDATE SET
			name = EXCLUDED.name,
			brand = EXCLUDED.brand,
			unit = EXCLUDED.unit,
			category_path = EXCLUDED.category_path,
			image_url = EXCLUDED.image_url,
			updated_at = NOW()
		WHERE (c.name, c.brand, c.unit, c.category_path, c.image_url)
			IS DISTINCT FROM (EXCLUDED.name, EXCLUDED.brand, EXCLUDED.unit, EXCLUDED.category_path, EXCLUDED.image_url)`,
		item.CatalogKey, item.Source, item.SourceProductID, item.Name, item.Brand, item.Unit, item.CategoryPath, item.ImageURL)
	if res.Error != nil {
		return false, fmt.Errorf("upsert catalog item %s: %w", item.CatalogKey, res.Error)
	}
	return res.RowsAffected > 0, nil
}
