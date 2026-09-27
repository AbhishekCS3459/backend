// Command catalog-indexes creates the MongoDB indexes the product catalogue
// needs, including the product_search Atlas Search index. It is idempotent:
// existing indexes with the same definition are left alone, and a changed
// search index definition is updated in place.
//
//	go run ./cmd/catalog-indexes
//	go run ./cmd/catalog-indexes -only catalog_categories_name_id
//	go run ./cmd/catalog-indexes -only product_search
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/mongodb"
	"github.com/AbhishekCS3459/find-me-backend/internal/productcatalog"
)

const searchIndexPoll = 5 * time.Second

func main() {
	only := flag.String("only", "", "comma-separated index names to create (default: all)")
	flag.Parse()

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	_ = godotenv.Load(".env")

	uri := os.Getenv("DATABASE_URL_MONGODB_PROD")
	if uri == "" {
		log.Fatal().Msg("DATABASE_URL_MONGODB_PROD is not set")
	}
	dbName := envOr("MONGODB_CATALOG_DATABASE", "catalog")
	colName := envOr("MONGODB_CATALOG_COLLECTION", "products")

	// Building indexes over the full collection can take a long time.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
	defer cancel()

	client, err := mongodb.Connect(ctx, uri)
	if err != nil {
		log.Fatal().Err(err).Msg("connect")
	}
	defer func() { _ = client.Close(context.Background()) }()

	col := client.Database(dbName).Collection(colName)
	var selected []string
	if *only != "" {
		selected = strings.Split(*only, ",")
	}
	wanted := func(name string) bool { return selected == nil || slices.Contains(selected, name) }

	for _, index := range productcatalog.Indexes() {
		if !wanted(index.Name) {
			continue
		}
		start := time.Now()
		log.Info().Str("index", index.Name).Msg("creating index")
		if _, err := col.Indexes().CreateOne(ctx, index.Model); err != nil {
			log.Fatal().Err(err).Str("index", index.Name).Msg("create index failed")
		}
		log.Info().Str("index", index.Name).Dur("took", time.Since(start)).Msg("index ready")
	}

	if wanted(productcatalog.SearchIndexName) {
		start := time.Now()
		if err := ensureSearchIndex(ctx, col.SearchIndexes()); err != nil {
			log.Fatal().Err(err).Str("index", productcatalog.SearchIndexName).Msg("search index failed")
		}
		log.Info().Str("index", productcatalog.SearchIndexName).Dur("took", time.Since(start)).Msg("search index ready")
	}
	log.Info().Msg("all catalogue indexes are ready")
}

// searchIndexStatus is the part of a $listSearchIndexes entry this command reads.
type searchIndexStatus struct {
	Name             string   `bson:"name"`
	Status           string   `bson:"status"`
	Queryable        bool     `bson:"queryable"`
	LatestDefinition bson.Raw `bson:"latestDefinition"`
}

// ensureSearchIndex creates the product search index, or updates it when its
// definition changed, then waits until it serves queries with the latest
// definition.
func ensureSearchIndex(ctx context.Context, view mongo.SearchIndexView) error {
	name := productcatalog.SearchIndexName
	want := productcatalog.SearchIndexDefinition()

	current, found, err := searchIndex(ctx, view, name)
	if err != nil {
		return err
	}
	switch {
	case !found:
		log.Info().Str("index", name).Msg("creating search index")
		if _, err := view.CreateOne(ctx, productcatalog.SearchIndex()); err != nil {
			return fmt.Errorf("create: %w", err)
		}
	default:
		same, err := definitionApplied(want, current.LatestDefinition)
		if err != nil {
			return err
		}
		if same {
			log.Info().Str("index", name).Str("status", current.Status).Msg("search index definition is up to date")
			break
		}
		log.Info().Str("index", name).Msg("updating search index definition")
		if err := view.UpdateOne(ctx, name, want); err != nil {
			return fmt.Errorf("update: %w", err)
		}
	}
	return waitReady(ctx, view, name)
}

func searchIndex(ctx context.Context, view mongo.SearchIndexView, name string) (searchIndexStatus, bool, error) {
	cur, err := view.List(ctx, options.SearchIndexes().SetName(name))
	if err != nil {
		return searchIndexStatus{}, false, fmt.Errorf("list search indexes: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()
	var indexes []searchIndexStatus
	if err := cur.All(ctx, &indexes); err != nil {
		return searchIndexStatus{}, false, fmt.Errorf("list search indexes: %w", err)
	}
	if len(indexes) == 0 {
		return searchIndexStatus{}, false, nil
	}
	return indexes[0], true, nil
}

// waitReady polls until the index is READY: queryable with its latest
// definition. An index that is queryable but still rebuilding after an update
// keeps serving the previous definition.
func waitReady(ctx context.Context, view mongo.SearchIndexView, name string) error {
	for {
		index, found, err := searchIndex(ctx, view, name)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("search index disappeared while building")
		}
		switch index.Status {
		case "READY":
			if index.Queryable {
				return nil
			}
		case "FAILED":
			return errors.New("search index build failed; see the Atlas UI for the reason")
		}
		log.Info().Str("index", name).Str("status", index.Status).Bool("queryable", index.Queryable).Msg("waiting for search index")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(searchIndexPoll):
		}
	}
}

// definitionApplied reports whether every setting in want is present in the
// stored definition and the indexed fields are the same. Atlas adds default
// settings to the stored copy, so an exact comparison would always report a
// change, but it never adds fields.
func definitionApplied(want bson.D, stored bson.Raw) (bool, error) {
	if len(stored) == 0 {
		return false, nil
	}
	w, err := generic(want)
	if err != nil {
		return false, err
	}
	s, err := generic(stored)
	if err != nil {
		return false, err
	}
	return contains(s, w, false), nil
}

// generic converts a BSON document to plain maps, slices and float64s.
func generic(doc any) (any, error) {
	ext, err := bson.MarshalExtJSON(doc, false, false)
	if err != nil {
		return nil, fmt.Errorf("encode search index definition: %w", err)
	}
	var out any
	if err := json.Unmarshal(ext, &out); err != nil {
		return nil, fmt.Errorf("decode search index definition: %w", err)
	}
	return out, nil
}

// contains reports whether have includes everything in want. exactKeys
// requires the same keys too; it applies to "fields" mappings, whose keys are
// the indexed field names.
func contains(have, want any, exactKeys bool) bool {
	switch w := want.(type) {
	case map[string]any:
		h, ok := have.(map[string]any)
		if !ok || (exactKeys && len(h) != len(w)) {
			return false
		}
		for key, value := range w {
			if !contains(h[key], value, key == "fields") {
				return false
			}
		}
		return true
	case []any:
		h, ok := have.([]any)
		if !ok || len(h) != len(w) {
			return false
		}
		for i := range w {
			if !contains(h[i], w[i], false) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(have, want)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
