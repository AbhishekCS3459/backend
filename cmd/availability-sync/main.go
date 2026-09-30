// Command availability-sync compares store_product_availability with the
// inventory, store and price data it is built from. Without -apply it only
// reports drift and exits 1 if there is any; with -apply it writes missing and
// stale rows (the backfill) and records an InventoryChanged event for each.
// It is safe to run while the API is serving.
//
//	go run ./cmd/availability-sync                 # check every store
//	go run ./cmd/availability-sync -apply          # backfill / repair
//	go run ./cmd/availability-sync -store <uuid>   # one store only
//
// It reads DATABASE_URL from the same .env files as the API, chosen by
// ENVIRONMENT (default development).
package main

import (
	"context"
	"errors"
	"flag"
	"io/fs"
	"os"
	"os/signal"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/AbhishekCS3459/find-me-backend/internal/availability"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/database"
)

func main() {
	apply := flag.Bool("apply", false, "write missing and stale rows (default: report only)")
	store := flag.String("store", "", "limit to one store ID")
	batch := flag.Int("batch", 500, "inventory rows per transaction (max 500)")
	flag.Parse()

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	loadEnvFiles()

	opts := availability.SyncOptions{Apply: *apply, BatchSize: *batch}
	if *store != "" {
		id, err := uuid.Parse(*store)
		if err != nil {
			log.Fatal().Err(err).Msg("invalid -store")
		}
		opts.StoreID = &id
	}
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal().Msg("DATABASE_URL is not set")
	}
	os.Exit(run(dbURL, opts))
}

// run returns the exit code, so its deferred cleanup runs before the process exits.
func run(dbURL string, opts availability.SyncOptions) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	db, err := database.NewDB(ctx, dbURL)
	if err != nil {
		log.Error().Err(err).Msg("connect to database")
		return 1
	}
	defer db.Close()

	start := time.Now()
	report, err := availability.Sync(ctx, db.Gorm, opts)
	for _, d := range report.Drift {
		log.Warn().Str("inventory_id", d.InventoryID.String()).Strs("fields", d.Fields).Msg("drift")
	}
	for _, key := range report.MissingCatalogKeys {
		log.Warn().Str("catalog_key", key).Msg("search key has no catalog_item")
	}
	log.Info().
		Bool("apply", opts.Apply).
		Int("checked", report.Checked).
		Int("missing", report.Missing).
		Int("stale", report.Stale).
		Int("written", report.Written).
		Int("missing_catalog_items", report.MissingCatalogItems).
		Dur("took", time.Since(start)).
		Msg("availability sync finished")
	if err != nil {
		log.Error().Err(err).Msg("availability sync failed")
		return 1
	}
	if report.MissingCatalogItems > 0 {
		log.Warn().Msg("catalog items are missing; run make catalog-item-sync-apply")
		if opts.Apply {
			return 1
		}
	}
	if !opts.Apply && !report.Clean() {
		log.Warn().Msg("drift found; run with -apply to repair it")
		return 1
	}
	return 0
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
