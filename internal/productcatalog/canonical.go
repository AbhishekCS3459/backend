package productcatalog

import (
	"context"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// categoryPathSeparator joins the levels of a category path.
const categoryPathSeparator = " › "

// CanonicalProduct is one catalogue product as every retailer and customer
// sees it. The catalogue stores a product once per locality; those documents
// share a productId and become one CanonicalProduct.
type CanonicalProduct struct {
	ProductID string
	Name      string
	Brand     string
	Unit      string
	ImageURL  string
	// Category is the leaf category name; CategoryPath is Group › Collection › Name.
	Category     string
	CategoryPath string
	// MRP is the highest MRP across localities, 0 when none is recorded.
	MRP float64
}

// CatalogKey is the product's catalog_key.
func (p CanonicalProduct) CatalogKey() string { return CatalogKey(p.ProductID) }

// SKU is the product's Todayz SKU.
func (p CanonicalProduct) SKU() string { return SKU(p.ProductID) }

type canonicalDoc struct {
	ProductID  string     `bson:"_id"`
	Name       string     `bson:"name"`
	Brand      string     `bson:"brand"`
	Unit       string     `bson:"unit"`
	ImageURL   string     `bson:"imageUrl"`
	Categories []Category `bson:"categories"`
	MRP        float64    `bson:"mrp"`
}

// CanonicalProducts returns the canonical record for each id the catalogue has.
// Descriptive fields come from the product's first document in _id order, so
// the choice is stable across runs without depending on the _id format.
func (r *repository) CanonicalProducts(ctx context.Context, ids []string) (map[string]CanonicalProduct, error) {
	out := make(map[string]CanonicalProduct, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	first := func(field string) bson.D { return bson.D{{Key: "$first", Value: "$" + field}} }
	cur, err := r.col.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.D{{Key: "productId", Value: bson.D{{Key: "$in", Value: ids}}}}}},
		{{Key: "$sort", Value: bson.D{{Key: "_id", Value: 1}}}},
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$productId"},
			{Key: "name", Value: first("name")},
			{Key: "brand", Value: first("brand")},
			{Key: "unit", Value: first("unit")},
			{Key: "imageUrl", Value: first("imageUrl")},
			{Key: "categories", Value: first("categories")},
			{Key: "mrp", Value: bson.D{{Key: "$max", Value: "$mrp"}}},
		}}},
	})
	if err != nil {
		return nil, classify(err)
	}
	defer func() { _ = cur.Close(ctx) }()
	for cur.Next(ctx) {
		var doc canonicalDoc
		if err := cur.Decode(&doc); err != nil {
			return nil, fmt.Errorf("decode catalogue product: %w", err)
		}
		out[doc.ProductID] = doc.canonical()
	}
	if err := cur.Err(); err != nil {
		return nil, classify(err)
	}
	return out, nil
}

func (d canonicalDoc) canonical() CanonicalProduct {
	p := CanonicalProduct{
		ProductID: d.ProductID,
		Name:      strings.TrimSpace(d.Name),
		Brand:     strings.TrimSpace(d.Brand),
		Unit:      strings.TrimSpace(d.Unit),
		ImageURL:  strings.TrimSpace(d.ImageURL),
		MRP:       d.MRP,
	}
	if len(d.Categories) > 0 {
		c := d.Categories[0]
		p.Category = strings.TrimSpace(c.Name)
		levels := make([]string, 0, 3)
		for _, level := range []string{c.Group, c.Collection, c.Name} {
			if level = strings.TrimSpace(level); level != "" {
				levels = append(levels, level)
			}
		}
		p.CategoryPath = strings.Join(levels, categoryPathSeparator)
	}
	return p
}
