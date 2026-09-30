// Command catalog-key-check audits catalogue identity on product variants.
//
// TDZ-<productId> SKUs and todayz:<productId> keys belong to catalogue
// products only. Migration 000046 inferred keys from SKUs, and until 000047 a
// retailer could type such a SKU by hand, so it reports:
//
//   - unknown: a key naming a product that isn't in the MongoDB catalogue;
//   - name mismatch: a key whose variant's name is very different from the
//     catalogue's, likely a hand-typed SKU that happened to match a product;
//   - reserved SKU: a variant without a key whose SKU starts with TDZ-
//     (including the case-only duplicates 000046 skipped).
//
// Without -apply it only reports and exits 1 if anything is found. With -apply
// it clears unknown keys (and, with -include-name-mismatches, mismatched ones)
// and renames the reserved SKUs of products without a key to MANUAL-<sku>, so
// they form their own search group and migration 000048 can validate. Search
// rows are refreshed in the same transaction. Safe to run while the API serves.
//
//	go run ./cmd/catalog-key-check                                   # report only
//	go run ./cmd/catalog-key-check -apply                            # repair unknown keys and reserved SKUs
//	go run ./cmd/catalog-key-check -apply -include-name-mismatches   # also repair name mismatches
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
	"strings"
	"time"
	"unicode"

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

// minNameSimilarity is the trigram similarity below which a variant's name is
// reported as not matching its catalogue product (pg_trgm's default threshold).
const minNameSimilarity = 0.3

// manualSKUPrefix is prepended to a reserved SKU rather than replacing it, so
// the retailer still recognises the SKU they typed.
const manualSKUPrefix = "MANUAL-"

// reservedSKU matches expr as migration 000047's constraint matches sku.
func reservedSKU(expr string) string {
	return `regexp_replace(` + expr + `, '[\u00AD\u200B-\u200F\u202A-\u202E\u2060-\u2064\uFEFF]', '', 'g') ~* '^\s*TDZ-'`
}

type options struct {
	apply          bool
	nameMismatches bool
	batch          int
}

func main() {
	var opts options
	flag.BoolVar(&opts.apply, "apply", false, "repair what is found (default: report only)")
	flag.BoolVar(&opts.nameMismatches, "include-name-mismatches", false, "with -apply, also clear keys whose name doesn't match the catalogue")
	flag.IntVar(&opts.batch, "batch", 500, "variants per catalogue lookup and transaction")
	flag.Parse()
	opts.batch = min(max(opts.batch, 1), 1000)

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
	os.Exit(run(dbURL, mongoURL, opts))
}

type report struct {
	checked, unknown, mismatched, reserved, cleared, renamed int
}

func (r report) findings(opts options) int {
	n := r.unknown + r.reserved
	if !opts.apply || opts.nameMismatches {
		n += r.mismatched
	}
	return n
}

// run returns the exit code, so its deferred cleanup runs before the process exits.
func run(dbURL, mongoURL string, opts options) int {
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
	var rep report
	err = checkKeys(ctx, db.Gorm, catalogue, opts, &rep)
	if err == nil {
		err = checkReservedSKUs(ctx, db.Gorm, opts, &rep)
	}
	log.Info().
		Bool("apply", opts.apply).
		Int("checked", rep.checked).
		Int("unknown", rep.unknown).
		Int("name_mismatch", rep.mismatched).
		Int("reserved_sku_without_key", rep.reserved).
		Int("keys_cleared", rep.cleared).
		Int("skus_renamed", rep.renamed).
		Dur("took", time.Since(start)).
		Msg("catalog key check finished")
	if err != nil {
		log.Error().Err(err).Msg("catalog key check failed")
		return 1
	}
	if !opts.apply && rep.findings(opts) > 0 {
		log.Warn().Msg("run with -apply to repair (add -include-name-mismatches to clear mismatched keys too)")
		return 1
	}
	return 0
}

type variant struct {
	ID         uuid.UUID
	RetailerID uuid.UUID
	SKU        string
	CatalogKey *string
	Name       string
}

// checkKeys pages through variants with a catalogue key and compares each with
// its catalogue product.
func checkKeys(ctx context.Context, db *gorm.DB, catalogue productcatalog.Repository, opts options, rep *report) error {
	after := uuid.Nil
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var page []variant
		err := db.WithContext(ctx).Raw(`
			SELECT v.id, v.retailer_id, v.sku, v.catalog_key, p.name
			FROM product_variant v JOIN product p ON p.id = v.product_id
			WHERE v.catalog_key LIKE ? AND v.id > ?
			ORDER BY v.id LIMIT ?`, productcatalog.CatalogKeySource+":%", after, opts.batch).Scan(&page).Error
		if err != nil {
			return fmt.Errorf("list keyed variants: %w", err)
		}
		if len(page) == 0 {
			return nil
		}
		after = page[len(page)-1].ID
		rep.checked += len(page)

		ids := make([]string, 0, len(page))
		for _, v := range page {
			if id, ok := productcatalog.CatalogKeyProductID(*v.CatalogKey); ok {
				ids = append(ids, id)
			}
		}
		products, err := catalogue.CanonicalProducts(ctx, ids)
		if err != nil {
			return fmt.Errorf("look up catalogue products: %w", err)
		}
		var repair []variant
		for _, v := range page {
			id, ok := productcatalog.CatalogKeyProductID(*v.CatalogKey)
			product, found := products[id]
			event := log.Warn().Str("variant_id", v.ID.String()).Str("retailer_id", v.RetailerID.String()).
				Str("sku", v.SKU).Str("catalog_key", *v.CatalogKey).Str("name", v.Name)
			switch {
			case !ok || !found:
				rep.unknown++
				event.Msg("product not in the catalogue")
				repair = append(repair, v)
			case nameSimilarity(v.Name, product.Name) < minNameSimilarity:
				rep.mismatched++
				event.Str("catalogue_name", product.Name).
					Float64("similarity", nameSimilarity(v.Name, product.Name)).
					Msg("name doesn't match the catalogue product")
				if opts.nameMismatches {
					repair = append(repair, v)
				}
			}
		}
		if !opts.apply || len(repair) == 0 {
			continue
		}
		if err := repairVariants(ctx, db, repair, rep); err != nil {
			return err
		}
	}
}

// checkReservedSKUs finds variants without a key that still use a TDZ- SKU.
func checkReservedSKUs(ctx context.Context, db *gorm.DB, opts options, rep *report) error {
	after := uuid.Nil
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var page []variant
		err := db.WithContext(ctx).Raw(`
			SELECT v.id, v.retailer_id, v.sku, v.catalog_key, p.name
			FROM product_variant v JOIN product p ON p.id = v.product_id
			WHERE v.catalog_key IS NULL AND `+reservedSKU("v.sku")+` AND v.id > ?
			ORDER BY v.id LIMIT ?`, after, opts.batch).Scan(&page).Error
		if err != nil {
			return fmt.Errorf("list reserved SKUs: %w", err)
		}
		if len(page) == 0 {
			return nil
		}
		after = page[len(page)-1].ID
		rep.reserved += len(page)
		for _, v := range page {
			log.Warn().Str("variant_id", v.ID.String()).Str("retailer_id", v.RetailerID.String()).
				Str("sku", v.SKU).Str("name", v.Name).Msg("product without a catalogue key uses a TDZ- SKU")
		}
		if opts.apply {
			if err := repairVariants(ctx, db, page, rep); err != nil {
				return err
			}
		}
	}
}

// repairVariants clears each variant's key and gives it a SKU outside the
// reserved prefix, then rebuilds the variants' search rows, in one
// transaction. A variant changed since it was read is left alone.
func repairVariants(ctx context.Context, db *gorm.DB, variants []variant, rep *report) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ids := make([]uuid.UUID, 0, len(variants))
		cleared, renamed := 0, 0
		for _, v := range variants {
			sku, err := manualSKU(tx, v)
			if err != nil {
				return err
			}
			res := tx.Exec(`UPDATE product_variant SET catalog_key = NULL, sku = ?, updated_at = NOW()
				WHERE id = ? AND sku = ? AND catalog_key IS NOT DISTINCT FROM ?`, sku, v.ID, v.SKU, v.CatalogKey)
			if res.Error != nil {
				return fmt.Errorf("repair variant %s: %w", v.ID, res.Error)
			}
			if res.RowsAffected == 0 {
				continue
			}
			ids = append(ids, v.ID)
			if v.CatalogKey != nil {
				cleared++
			}
			if sku != v.SKU {
				renamed++
				log.Info().Str("variant_id", v.ID.String()).Str("from", v.SKU).Str("to", sku).Msg("renamed SKU")
			}
		}
		if err := availability.RefreshVariants(tx, ids); err != nil {
			return err
		}
		rep.cleared += cleared
		rep.renamed += renamed
		return nil
	})
}

// manualSKU is the SKU a variant keeps once it is no longer a catalogue
// product: unchanged unless it is reserved, otherwise MANUAL-<sku>, with a
// numeric suffix if the retailer already has that SKU.
func manualSKU(tx *gorm.DB, v variant) (string, error) {
	var reserved bool
	if err := tx.Raw(`SELECT `+reservedSKU("?::text"), v.SKU).Scan(&reserved).Error; err != nil {
		return "", fmt.Errorf("check SKU: %w", err)
	}
	if !reserved {
		return v.SKU, nil
	}
	base := manualSKUPrefix + strings.TrimSpace(v.SKU)
	for n := 1; n <= 100; n++ {
		candidate := base
		if n > 1 {
			candidate = fmt.Sprintf("%s-%d", base, n)
		}
		var taken int64
		err := tx.Raw(`SELECT COUNT(*) FROM product_variant WHERE retailer_id = ? AND sku = ?`, v.RetailerID, candidate).
			Scan(&taken).Error
		if err != nil {
			return "", fmt.Errorf("check SKU: %w", err)
		}
		if taken == 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no free SKU for variant %s", v.ID)
}

// nameSimilarity is the trigram similarity of two names (as pg_trgm computes
// it): 1 for the same words, 0 for nothing in common.
func nameSimilarity(a, b string) float64 {
	ta, tb := trigrams(a), trigrams(b)
	if len(ta) == 0 && len(tb) == 0 {
		return 1
	}
	shared := 0
	for t := range ta {
		if tb[t] {
			shared++
		}
	}
	return float64(shared) / float64(len(ta)+len(tb)-shared)
}

func trigrams(s string) map[string]bool {
	out := map[string]bool{}
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, word := range words {
		padded := []rune("  " + word + " ")
		for i := 0; i+3 <= len(padded); i++ {
			out[string(padded[i:i+3])] = true
		}
	}
	return out
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
