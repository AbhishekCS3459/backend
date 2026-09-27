package productcatalog

import (
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// SearchIndexName is the Atlas Search index behind Search and Autocomplete.
const SearchIndexName = "product_search"

const (
	textAnalyzer         = "productText"
	autocompleteAnalyzer = "productAutocomplete"
)

// apostrophes are removed before tokenizing, so "Lay's" is indexed as "lays"
// and matches a search for "lays", like keywords() does for regex search.
var apostropheMapping = bson.D{
	{Key: "type", Value: "mapping"},
	{Key: "mappings", Value: bson.D{{Key: "'", Value: ""}, {Key: "’", Value: ""}, {Key: "‘", Value: ""}}},
}

// SearchIndexDefinition is the definition of SearchIndexName.
//
// Text fields use productText: apostrophes removed, standard tokenizer
// (splits "2-Minute" into "2" and "minute"), lowercase, accents folded, and
// Porter stemming, so "books" matches "book" and the typo "noodls" matches
// "noodles" (both stem to "noodl"). Name and brand are also indexed for
// autocomplete with edge n-grams ("sti" matches "Sticker"). Token fields are
// for exact filters with the values the /filters endpoint returns.
func SearchIndexDefinition() bson.D {
	text := bson.D{{Key: "type", Value: "string"}, {Key: "analyzer", Value: textAnalyzer}}
	token := bson.D{{Key: "type", Value: "token"}}
	autocomplete := bson.D{
		{Key: "type", Value: "autocomplete"},
		{Key: "analyzer", Value: autocompleteAnalyzer},
		{Key: "tokenization", Value: "edgeGram"},
		{Key: "minGrams", Value: 2},
		{Key: "maxGrams", Value: 15},
		{Key: "foldDiacritics", Value: true},
	}
	return bson.D{
		{Key: "mappings", Value: bson.D{
			{Key: "dynamic", Value: false},
			{Key: "fields", Value: bson.D{
				{Key: "name", Value: bson.A{text, autocomplete}},
				{Key: "brand", Value: bson.A{text, autocomplete, token}},
				{Key: "categories", Value: bson.D{
					{Key: "type", Value: "document"},
					{Key: "dynamic", Value: false},
					{Key: "fields", Value: bson.D{
						{Key: "name", Value: bson.A{text, token}},
						{Key: "collection", Value: bson.A{text, token}},
						{Key: "group", Value: bson.A{text, token}},
					}},
				}},
				{Key: "inStock", Value: bson.D{{Key: "type", Value: "boolean"}}},
				{Key: "price", Value: bson.D{{Key: "type", Value: "number"}}},
			}},
		}},
		{Key: "analyzers", Value: bson.A{
			bson.D{
				{Key: "name", Value: textAnalyzer},
				{Key: "charFilters", Value: bson.A{apostropheMapping}},
				{Key: "tokenizer", Value: bson.D{{Key: "type", Value: "standard"}}},
				{Key: "tokenFilters", Value: bson.A{
					bson.D{{Key: "type", Value: "lowercase"}},
					bson.D{{Key: "type", Value: "icuFolding"}},
					bson.D{{Key: "type", Value: "porterStemming"}},
				}},
			},
			bson.D{
				{Key: "name", Value: autocompleteAnalyzer},
				{Key: "charFilters", Value: bson.A{apostropheMapping}},
				{Key: "tokenizer", Value: bson.D{{Key: "type", Value: "standard"}}},
				{Key: "tokenFilters", Value: bson.A{
					bson.D{{Key: "type", Value: "lowercase"}},
					bson.D{{Key: "type", Value: "icuFolding"}},
				}},
			},
		}},
	}
}

// SearchIndex returns the model that creates SearchIndexName.
func SearchIndex() mongo.SearchIndexModel {
	return mongo.SearchIndexModel{
		Definition: SearchIndexDefinition(),
		Options:    options.SearchIndexes().SetName(SearchIndexName).SetType("search"),
	}
}
