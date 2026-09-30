// Command catalog-key-check confirms that every catalogue key on a product
// variant (todayz:<productId>) names a product in the MongoDB catalogue.
// Migration 000046 set keys from TDZ-<productId> SKUs, and a retailer can type
// such a SKU by hand, so a key can name a product that doesn't exist. Without
// -apply it only reports those keys and exits 1 if there are any; with -apply
// it clears them, so the product forms its own search group, and refreshes the
// search rows in the same transaction. It is safe to run while the API is serving.
//
//	go run ./cmd/catalog-key-check          # report only
//	go run ./cmd/catalog-key-check -apply   # clear unknown keys
//
// It reads DATABASE_URL and DATABASE_URL_MONGODB_PROD from the same .env files
// as the API, chosen by ENVIRONMENT (default development).
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

	"github.com/AbhishekCS3459/find-me-backend/internal/availability"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/database"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/mongodb"
	"github.com/AbhishekCS3459/find-me-backend/internal/productcatalog"
)

func main() {
	apply := flag.Bool("apply", false, "clear keys whose product isn't in the catalogue (default: report only)")
	batch := flag.Int("batch", 500, "variants per catalogue lookup and transaction")
	flag.Parse()

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	loadEnvFiles()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal().Msg("DATABASE_URL is not set")
	}
	mongoURL := os.Getenv("DATABASE_URL_MONGODB_PROD")
	if mongoURL == "" {
		log.Fatal().Msg("DATABASE_URL_MONGODB_PROD is not set; the catalogue is needed to check keys")
	}
	os.Exit(run(dbURL, mongoURL, *apply, min(max(*batch, 1), 1000)))
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
	client, err := mongodb.Connect(ctx, mongoURL)
	if err != nil {
		log.Error().Err(err).Msg("connect to catalogue")
		return 1
	}
	defer func() { _ = client.Close(context.Background()) }()
	catalogue := productcatalog.NewRepository(client.Database(envOr("MONGODB_CATALOG_DATABASE", "catalog")).
		Collection(envOr("MONGODB_CATALOG_COLLECTION", "products")))

	start := time.Now()
	checked, unknown, cleared, err := check(ctx, db.Gorm, catalogue, apply, batch)
	log.Info().
		Bool("apply", apply).
		Int("checked", checked).
		Int("unknown", unknown).
		Int("cleared", cleared).
		Dur("took", time.Since(start)).
		Msg("catalog key check finished")
	if err != nil {
		log.Error().Err(err).Msg("catalog key check failed")
		return 1
	}
	if !apply && unknown > 0 {
		log.Warn().Msg("keys name products that aren't in the catalogue; run with -apply to clear them")
		return 1
	}
	return 0
}

type keyedVariant struct {
	ID         uuid.UUID
	RetailerID uuid.UUID
	SKU        string
	CatalogKey string
}

// check pages through variants with a catalogue key and looks their products
// up in the catalogue, clearing the unknown ones when apply is set.
func check(
	ctx context.Context, db *gorm.DB, catalogue productcatalog.Repository, apply bool, batch int,
) (checked, unknown, cleared int, err error) {
	after := uuid.Nil
	for {
		if err := ctx.Err(); err != nil {
			return checked, unknown, cleared, err
		}
		var page []keyedVariant
		err := db.WithContext(ctx).Raw(`
			SELECT id, retailer_id, sku, catalog_key FROM product_variant
			WHERE catalog_key LIKE ? AND id > ?
			ORDER BY id LIMIT ?`, productcatalog.CatalogKeySource+":%", after, batch).Scan(&page).Error
		if err != nil {
			return checked, unknown, cleared, fmt.Errorf("list keyed variants: %w", err)
		}
		if len(page) == 0 {
			return checked, unknown, cleared, nil
		}
		after = page[len(page)-1].ID
		checked += len(page)

		missing, err := unknownKeys(ctx, catalogue, page)
		if err != nil {
			return checked, unknown, cleared, err
		}
		unknown += len(missing)
		for _, v := range missing {
			log.Warn().Str("variant_id", v.ID.String()).Str("retailer_id", v.RetailerID.String()).
				Str("sku", v.SKU).Str("catalog_key", v.CatalogKey).Msg("product not in the catalogue")
		}
		if !apply || len(missing) == 0 {
			continue
		}
		n, err := clearKeys(ctx, db, missing)
		cleared += n
		if err != nil {
			return checked, unknown, cleared, err
		}
	}
}

func unknownKeys(
	ctx context.Context, catalogue productcatalog.Repository, page []keyedVariant,
) ([]keyedVariant, error) {
	ids := make([]string, 0, len(page))
	for _, v := range page {
		if id, ok := productcatalog.CatalogKeyProductID(v.CatalogKey); ok {
			ids = append(ids, id)
		}
	}
	found, err := catalogue.ExistingProductIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("look up catalogue products: %w", err)
	}
	var missing []keyedVariant
	for _, v := range page {
		if id, ok := productcatalog.CatalogKeyProductID(v.CatalogKey); !ok || !found[id] {
			missing = append(missing, v)
		}
	}
	return missing, nil
}

// clearKeys removes the keys and rebuilds the variants' search rows in one
// transaction. A key that changed since it was read is left alone.
func clearKeys(ctx context.Context, db *gorm.DB, variants []keyedVariant) (int, error) {
	cleared := 0
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ids := make([]uuid.UUID, 0, len(variants))
		for _, v := range variants {
			res := tx.Exec(`UPDATE product_variant SET catalog_key = NULL, updated_at = NOW()
				WHERE id = ? AND catalog_key = ?`, v.ID, v.CatalogKey)
			if res.Error != nil {
				return fmt.Errorf("clear catalog key: %w", res.Error)
			}
			if res.RowsAffected > 0 {
				ids = append(ids, v.ID)
			}
		}
		cleared = len(ids)
		return availability.RefreshVariants(tx, ids)
	})
	if err != nil {
		return 0, err
	}
	return cleared, nil
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
