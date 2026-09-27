// Command catalog-cleanup prepares catalogue products for search: it removes
// scraper fields the catalogue does not use (platform, url, city, locality,
// address and scrapedAt by default) and stores a normalized
// copy of the searchable text under "search" (see
// productcatalog.BuildSearchFields). It is idempotent and only touches
// products that need it, so run it after every catalogue import
// (make catalog-prepare). Without -apply it only reports what it would change.
//
//	go run ./cmd/catalog-cleanup            # dry run: counts and a sample
//	go run ./cmd/catalog-cleanup -apply
//	go run ./cmd/catalog-cleanup -apply -drop platform,url,city,locality,address,scrapedAt,merchantId
//
// -drop replaces the default list, so repeat the defaults when adding a field.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
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

// requiredFields are read by the catalogue API or by this command and must
// never be dropped.
var requiredFields = []string{
	"_id", "productId", "name", "brand", "unit", "price", "mrp",
	"discountPercent", "inStock", "imageUrl", "categories", "search",
}

const sampleSize = 3

type stats struct {
	Scanned, Unchanged, NeedUpdate, SearchChanged, Updated int
	Dropped                                                map[string]int
}

const defaultDrop = "platform,url,city,locality,address,scrapedAt"

func main() {
	apply := flag.Bool("apply", false, "write the changes (default: dry run)")
	drop := flag.String("drop", defaultDrop, "comma-separated top-level fields to remove from every product")
	batch := flag.Int("batch", 500, "updates per bulk write")
	flag.Parse()

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	_ = godotenv.Load(".env")

	dropFields, err := parseDrop(*drop)
	if err != nil {
		log.Fatal().Err(err).Send()
	}
	uri := os.Getenv("DATABASE_URL_MONGODB_PROD")
	if uri == "" {
		log.Fatal().Msg("DATABASE_URL_MONGODB_PROD is not set")
	}
	if err := run(uri, dropFields, *apply, max(*batch, 1)); err != nil {
		log.Fatal().Err(err).Msg("catalogue cleanup failed")
	}
}

func run(uri string, dropFields []string, apply bool, batchSize int) error {
	dbName := envOr("MONGODB_CATALOG_DATABASE", "catalog")
	colName := envOr("MONGODB_CATALOG_COLLECTION", "products")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()

	client, err := mongodb.Connect(ctx, uri)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = client.Close(context.Background()) }()
	col := client.Database(dbName).Collection(colName)

	log.Info().
		Str("collection", dbName+"."+colName).
		Strs("drop", dropFields).
		Bool("apply", apply).
		Msg("starting catalogue cleanup")

	start := time.Now()
	s, err := cleanup(ctx, col, dropFields, apply, batchSize)
	logStats(s, time.Since(start), err)
	if err != nil {
		return err
	}
	if err := dropIndexes(ctx, col, dropFields, apply); err != nil {
		return fmt.Errorf("drop indexes: %w", err)
	}
	if !apply && s.NeedUpdate > 0 {
		log.Info().Msg("dry run: nothing was written; re-run with -apply to make these changes")
	}
	return nil
}

func parseDrop(value string) ([]string, error) {
	var fields []string
	for _, f := range strings.Split(value, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if strings.ContainsAny(f, ".$") {
			return nil, fmt.Errorf("-drop takes top-level field names, got %q", f)
		}
		if slices.Contains(requiredFields, f) {
			return nil, fmt.Errorf("-drop: %q is used by the catalogue and cannot be removed", f)
		}
		if !slices.Contains(fields, f) {
			fields = append(fields, f)
		}
	}
	return fields, nil
}

type product struct {
	ID         string                    `bson:"_id"`
	Name       string                    `bson:"name"`
	Brand      string                    `bson:"brand"`
	Categories []productcatalog.Category `bson:"categories"`
	Search     bson.RawValue             `bson:"search"`
}

// sameSearch reports whether the stored search field is exactly want, byte for
// byte, so stale extra keys (e.g. a removed field) also count as a change.
func sameSearch(stored bson.RawValue, want productcatalog.SearchFields) bool {
	encoded, err := bson.Marshal(want)
	return err == nil && stored.Type == bson.TypeEmbeddedDocument && bytes.Equal(stored.Value, encoded)
}

func cleanup(
	ctx context.Context, col *mongo.Collection, dropFields []string, apply bool, batchSize int,
) (stats, error) {
	s := stats{Dropped: map[string]int{}}
	projection := bson.D{
		{Key: "name", Value: 1},
		{Key: "brand", Value: 1},
		{Key: "categories", Value: 1},
		{Key: "search", Value: 1},
	}
	for _, f := range dropFields {
		projection = append(projection, bson.E{Key: f, Value: 1})
	}
	cur, err := col.Find(ctx, bson.D{}, options.Find().
		SetSort(bson.D{{Key: "_id", Value: 1}}).
		SetProjection(projection))
	if err != nil {
		return s, err
	}
	defer func() { _ = cur.Close(ctx) }()

	unset := bson.D{}
	for _, f := range dropFields {
		unset = append(unset, bson.E{Key: f, Value: ""})
	}
	var writes []mongo.WriteModel
	flush := func() error {
		if len(writes) == 0 {
			return nil
		}
		res, err := col.BulkWrite(ctx, writes, options.BulkWrite().SetOrdered(false))
		if res != nil {
			s.Updated += int(res.MatchedCount)
		}
		writes = writes[:0]
		if err != nil {
			return fmt.Errorf("bulk write: %w", err)
		}
		log.Info().Int("updated", s.Updated).Int("scanned", s.Scanned).Msg("progress")
		return nil
	}

	samples := 0
	for cur.Next(ctx) {
		var p product
		if err := cur.Decode(&p); err != nil {
			id, _ := cur.Current.Lookup("_id").StringValueOK()
			return s, fmt.Errorf("decode product %q: %w", id, err)
		}
		s.Scanned++

		search := productcatalog.BuildSearchFields(productcatalog.SearchInput{
			Name:       p.Name,
			Brand:      p.Brand,
			Categories: p.Categories,
		})
		searchChanged := !sameSearch(p.Search, search)
		var present []string
		for _, f := range dropFields {
			if _, err := cur.Current.LookupErr(f); err == nil {
				present = append(present, f)
				s.Dropped[f]++
			}
		}
		if !searchChanged && len(present) == 0 {
			s.Unchanged++
			continue
		}
		s.NeedUpdate++
		if searchChanged {
			s.SearchChanged++
		}
		if samples < sampleSize {
			samples++
			logSample(p, search, present)
		}
		if !apply {
			continue
		}

		update := bson.D{{Key: "$set", Value: bson.D{{Key: "search", Value: search}}}}
		if len(present) > 0 {
			update = append(update, bson.E{Key: "$unset", Value: unset})
		}
		writes = append(writes, mongo.NewUpdateOneModel().
			SetFilter(bson.D{{Key: "_id", Value: p.ID}}).
			SetUpdate(update))
		if len(writes) == batchSize {
			if err := flush(); err != nil {
				return s, err
			}
		}
	}
	if err := cur.Err(); err != nil {
		return s, err
	}
	return s, flush()
}

// dropIndexes removes indexes that use a dropped field; they would only index
// missing values from now on.
func dropIndexes(ctx context.Context, col *mongo.Collection, dropFields []string, apply bool) error {
	if len(dropFields) == 0 {
		return nil
	}
	cur, err := col.Indexes().List(ctx)
	if err != nil {
		return err
	}
	var indexes []struct {
		Name string `bson:"name"`
		Key  bson.D `bson:"key"`
	}
	if err := cur.All(ctx, &indexes); err != nil {
		return err
	}
	for _, index := range indexes {
		uses := false
		for _, k := range index.Key {
			root, _, _ := strings.Cut(k.Key, ".")
			uses = uses || slices.Contains(dropFields, root)
		}
		if !uses {
			continue
		}
		if !apply {
			log.Info().Str("index", index.Name).Msg("would drop index on a removed field")
			continue
		}
		if err := col.Indexes().DropOne(ctx, index.Name); err != nil {
			return fmt.Errorf("drop index %s: %w", index.Name, err)
		}
		log.Info().Str("index", index.Name).Msg("dropped index on a removed field")
	}
	return nil
}

func logSample(p product, search productcatalog.SearchFields, dropped []string) {
	var out strings.Builder
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	change := map[string]any{"search": search}
	if len(dropped) > 0 {
		change["remove"] = dropped
	}
	_ = enc.Encode(change)
	log.Info().Str("id", p.ID).Str("name", p.Name).Msg("sample change:\n" + out.String())
}

func logStats(s stats, took time.Duration, err error) {
	event := log.Info()
	if err != nil {
		event = log.Error().Err(err)
	}
	for f, n := range s.Dropped {
		event = event.Int("with_"+f, n)
	}
	event.
		Int("scanned", s.Scanned).
		Int("unchanged", s.Unchanged).
		Int("need_update", s.NeedUpdate).
		Int("search_changed", s.SearchChanged).
		Int("updated", s.Updated).
		Dur("took", took).
		Msg("cleanup finished")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
