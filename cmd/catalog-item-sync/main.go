// Command catalog-item-sync fills catalog_item, the canonical product customer
// search shows, for every catalog_key in use:
//
//   - todayz:<productId> from the MongoDB catalogue (one row however many
//     localities the catalogue stores the product in);
//   - variant:<id> from the retailer's own product, for products made by hand.
//
// Without -apply it runs the same writes in a transaction it rolls back, so the
// counts are exactly what -apply would change, and exits 1 if anything would.
// Re-run it after a catalogue upload to refresh names, units and images. It
// never writes mrp and never touches prices or stock. Safe while the API serves.
//
//	go run ./cmd/catalog-item-sync          # report only
//	go run ./cmd/catalog-item-sync -apply   # write
//
// It reads DATABASE_URL and, for catalogue products, DATABASE_URL_MONGODB_PROD
// from the same .env files as the API, chosen by ENVIRONMENT (default
// development). Without the catalogue, only hand-made products are synced.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	"github.com/AbhishekCS3459/find-me-backend/internal/catalogitem"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/database"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/mongodb"
	"github.com/AbhishekCS3459/find-me-backend/internal/productcatalog"
)

// errDryRun rolls back a page's writes when -apply isn't set.
var errDryRun = errors.New("dry run")

type report struct {
	catalogue, manual, changed, unknown, orphans int
	skippedCatalogue                             bool
}

func main() {
	apply := flag.Bool("apply", false, "write catalog_item rows (default: report only)")
	batch := flag.Int("batch", 500, "keys per catalogue lookup and transaction")
	flag.Parse()

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	loadEnvFiles()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal().Msg("DATABASE_URL is not set")
	}
	os.Exit(run(dbURL, os.Getenv("DATABASE_URL_MONGODB_PROD"), *apply, min(max(*batch, 1), 1000)))
}

// run returns the exit code, so its deferred cleanup runs before the process exits.
func run(dbURL, mongoURL string, apply bool, batch int) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	db, err := database.NewDB(ctx, dbURL)
	if err != nil {
		log.Error().Err(err).Msg("connect to database")
		return 1
	}
	defer db.Close()

	var catalogue productcatalog.Repository
	if mongoURL != "" {
		client, err := mongodb.Connect(ctx, mongoURL)
		if err != nil {
			log.Error().Err(err).Msg("connect to catalogue")
			return 1
		}
		defer func() { _ = client.Close(context.Background()) }()
		catalogue = productcatalog.NewRepository(client.Database(envOr("MONGODB_CATALOG_DATABASE", "catalog")).
			Collection(envOr("MONGODB_CATALOG_COLLECTION", "products")))
	}

	start := time.Now()
	var rep report
	if catalogue == nil {
		rep.skippedCatalogue = true
		log.Warn().Msg("DATABASE_URL_MONGODB_PROD is not set; catalogue products are skipped")
	} else {
		err = syncCatalogue(ctx, db.Gorm, catalogue, apply, batch, &rep)
	}
	if err == nil {
		err = syncManual(ctx, db.Gorm, apply, batch, &rep)
	}
	if err == nil {
		err = countOrphans(ctx, db.Gorm, &rep)
	}
	log.Info().
		Bool("apply", apply).
		Int("catalogue_keys", rep.catalogue).
		Int("manual_keys", rep.manual).
		Int("changed", rep.changed).
		Int("not_in_catalogue", rep.unknown).
		Int("unused_rows", rep.orphans).
		Bool("catalogue_skipped", rep.skippedCatalogue).
		Dur("took", time.Since(start)).
		Msg("catalog item sync finished")
	if err != nil {
		log.Error().Err(err).Msg("catalog item sync failed")
		return 1
	}
	if !apply && rep.changed > 0 {
		log.Warn().Msg("catalog items are missing or out of date; run with -apply to write them")
		return 1
	}
	return 0
}

// inTx runs fn in a transaction that is committed only when apply is set.
func inTx(ctx context.Context, db *gorm.DB, apply bool, fn func(tx *gorm.DB) error) error {
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := fn(tx); err != nil {
			return err
		}
		if !apply {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		return nil
	}
	return err
}

func syncCatalogue(
	ctx context.Context, db *gorm.DB, catalogue productcatalog.Repository, apply bool, batch int, rep *report,
) error {
	after := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var keys []string
		err := db.WithContext(ctx).Raw(`
			SELECT DISTINCT catalog_key FROM product_variant
			WHERE catalog_key LIKE ? AND catalog_key > ?
			ORDER BY catalog_key LIMIT ?`, productcatalog.CatalogKeySource+":%", after, batch).Scan(&keys).Error
		if err != nil {
			return fmt.Errorf("list catalogue keys: %w", err)
		}
		if len(keys) == 0 {
			return nil
		}
		after = keys[len(keys)-1]
		rep.catalogue += len(keys)

		ids := make([]string, 0, len(keys))
		for _, key := range keys {
			if id, ok := productcatalog.CatalogKeyProductID(key); ok {
				ids = append(ids, id)
			}
		}
		products, err := catalogue.CanonicalProducts(ctx, ids)
		if err != nil {
			return fmt.Errorf("look up catalogue products: %w", err)
		}
		err = inTx(ctx, db, apply, func(tx *gorm.DB) error {
			for _, key := range keys {
				id, _ := productcatalog.CatalogKeyProductID(key)
				product, ok := products[id]
				if !ok || product.Name == "" {
					rep.unknown++
					log.Warn().Str("catalog_key", key).Msg("not in the catalogue; run make catalog-key-check")
					continue
				}
				changed, err := catalogitem.Upsert(tx, catalogitem.FromCatalogue(product))
				if err != nil {
					return err
				}
				if changed {
					rep.changed++
					log.Info().Str("catalog_key", key).Str("name", product.Name).Msg("catalog item written")
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
}

func syncManual(ctx context.Context, db *gorm.DB, apply bool, batch int, rep *report) error {
	after := uuid.Nil
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var page []struct{ ID uuid.UUID }
		err := db.WithContext(ctx).Raw(`
			SELECT id FROM product_variant
			WHERE catalog_key IS NULL AND id > ?
			ORDER BY id LIMIT ?`, after, batch).Scan(&page).Error
		if err != nil {
			return fmt.Errorf("list hand-made products: %w", err)
		}
		if len(page) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, len(page))
		for i, p := range page {
			ids[i] = p.ID
		}
		after = ids[len(ids)-1]
		rep.manual += len(ids)

		err = inTx(ctx, db, apply, func(tx *gorm.DB) error {
			items, err := catalogitem.ManualItems(tx, ids)
			if err != nil {
				return err
			}
			for _, item := range items {
				changed, err := catalogitem.Upsert(tx, item)
				if err != nil {
					return err
				}
				if changed {
					rep.changed++
					log.Info().Str("catalog_key", item.CatalogKey).Str("name", item.Name).Msg("catalog item written")
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
}

// countOrphans reports rows no variant uses any more (a key cleared by
// catalog-key-check, say). They are harmless, since search starts from store
// rows, and are kept in case the key comes back.
func countOrphans(ctx context.Context, db *gorm.DB, rep *report) error {
	var n int64
	err := db.WithContext(ctx).Raw(`
		SELECT COUNT(*) FROM catalog_item c
		WHERE NOT EXISTS (
			SELECT 1 FROM product_variant v
			WHERE COALESCE(v.catalog_key, 'variant:' || v.id::text) = c.catalog_key
		)`).Scan(&n).Error
	if err != nil {
		return fmt.Errorf("count unused catalog items: %w", err)
	}
	rep.orphans = int(n)
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// loadEnvFiles mirrors the API: the first file to define a key wins and the
// process environment is never overridden.
func loadEnvFiles() {
	env := os.Getenv("ENVIRONMENT")
	if env == "" {
		env = "development"
	}
	files := []string{".env." + env + ".local"}
	if env != "test" {
		files = append(files, ".env.local")
	}
	files = append(files, ".env."+env, ".env")
	for _, file := range files {
		if err := godotenv.Load(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Warn().Err(err).Str("file", file).Msg("failed to load env file")
		}
	}
}
