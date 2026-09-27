package productcatalog

import (
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// indexedFields are the fields List filters on. Each index pairs the field with
// _id so an equality match can seek past the cursor and return rows already in
// id order, instead of scanning and sorting the collection.
var indexedFields = []string{
	"brand",
	"categories.group",
	"categories.collection",
	"categories.name",
	"productId",
}

// NamedIndex is an index definition with its name.
type NamedIndex struct {
	Name  string
	Model mongo.IndexModel
}

// Indexes returns the indexes the catalogue queries rely on.
func Indexes() []NamedIndex {
	out := make([]NamedIndex, 0, len(indexedFields))
	for _, field := range indexedFields {
		name := "catalog_" + strings.ReplaceAll(field, ".", "_") + "_id"
		out = append(out, NamedIndex{
			Name: name,
			Model: mongo.IndexModel{
				Keys:    bson.D{{Key: field, Value: 1}, {Key: "_id", Value: 1}},
				Options: options.Index().SetName(name),
			},
		})
	}
	return out
}
