// Command catalog-dedupe keeps one catalogue document per productId. The
// scraper stores a product once per locality, so the same product appears
// several times in the catalogue list. Everything outside MongoDB refers to a
// product by productId (TDZ-<productId> SKUs, todayz:<productId> keys), so the
// extra copies can go. The kept copy is the first in _id order, the same one
// productcatalog.CanonicalProducts already reads, so retailer and search data
// do not change. Without -apply it only reports what it would delete.
//
//	go run ./cmd/catalog-dedupe            # dry run: counts and a sample
//	go run ./cmd/catalog-dedupe -apply
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/mongodb"
)

const sampleSize = 5

type stats struct {
	Documents, Products, Duplicated, Extra, Deleted int
}

type group struct {
	ProductID string   `bson:"_id"`
	Name      string   `bson:"name"`
	IDs       []string `bson:"ids"`
}

func main() {
	apply := flag.Bool("apply", false, "delete the duplicates (default: dry run)")
	batch := flag.Int("batch", 500, "documents per delete")
	flag.Parse()

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	_ = godotenv.Load(".env")

	uri := os.Getenv("DATABASE_URL_MONGODB_PROD")
	if uri == "" {
		log.Fatal().Msg("DATABASE_URL_MONGODB_PROD is not set")
	}
	if err := run(uri, *apply, max(*batch, 1)); err != nil {
		log.Fatal().Err(err).Msg("catalogue dedupe failed")
	}
}

func run(uri string, apply bool, batchSize int) error {
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

	log.Info().Str("collection", dbName+"."+colName).Bool("apply", apply).Msg("starting catalogue dedupe")

	start := time.Now()
	s, err := dedupe(ctx, col, apply, batchSize)
	logStats(s, time.Since(start), err)
	if err != nil {
		return err
	}
	if !apply && s.Extra > 0 {
		log.Info().Msg("dry run: nothing was deleted; re-run with -apply to delete these documents")
	}
	return nil
}

func dedupe(ctx context.Context, col *mongo.Collection, apply bool, batchSize int) (stats, error) {
	var s stats
	total, err := col.CountDocuments(ctx, bson.D{})
	if err != nil {
		return s, fmt.Errorf("count: %w", err)
	}
	s.Documents = int(total)

	// Documents without a productId cannot be matched to anything, so they are
	// left alone rather than all treated as one product.
	hasProductID := bson.D{{Key: "$type", Value: "string"}, {Key: "$ne", Value: ""}}
	cur, err := col.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.D{{Key: "productId", Value: hasProductID}}}},
		{{Key: "$sort", Value: bson.D{{Key: "_id", Value: 1}}}},
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$productId"},
			{Key: "name", Value: bson.D{{Key: "$first", Value: "$name"}}},
			{Key: "ids", Value: bson.D{{Key: "$push", Value: "$_id"}}},
		}}},
		{{Key: "$sort", Value: bson.D{{Key: "_id", Value: 1}}}},
	}, options.Aggregate().SetAllowDiskUse(true))
	if err != nil {
		return s, fmt.Errorf("group by productId: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()

	var pending []string
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		res, err := col.DeleteMany(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: pending}}}})
		if res != nil {
			s.Deleted += int(res.DeletedCount)
		}
		pending = pending[:0]
		if err != nil {
			return fmt.Errorf("delete: %w", err)
		}
		log.Info().Int("deleted", s.Deleted).Int("of", s.Extra).Msg("progress")
		return nil
	}

	samples := 0
	for cur.Next(ctx) {
		var g group
		if err := cur.Decode(&g); err != nil {
			return s, fmt.Errorf("decode group: %w", err)
		}
		s.Products++
		if len(g.IDs) < 2 {
			continue
		}
		keep, extra := g.IDs[0], g.IDs[1:]
		s.Duplicated++
		s.Extra += len(extra)
		if samples < sampleSize {
			samples++
			log.Info().Str("product_id", g.ProductID).Str("name", g.Name).Str("keep", keep).Strs("delete", extra).Msg("sample")
		}
		if !apply {
			continue
		}
		pending = append(pending, extra...)
		if len(pending) >= batchSize {
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

func logStats(s stats, took time.Duration, err error) {
	event := log.Info()
	if err != nil {
		event = log.Error().Err(err)
	}
	event.
		Int("documents", s.Documents).
		Int("products", s.Products).
		Int("duplicated_products", s.Duplicated).
		Int("extra_documents", s.Extra).
		Int("deleted", s.Deleted).
		Dur("took", took).
		Msg("dedupe finished")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
