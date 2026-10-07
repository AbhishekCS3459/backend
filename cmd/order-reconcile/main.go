// Command order-reconcile checks orders against stock and payments: reserved
// units that don't match active reservations, orders past their deadline,
// accepted orders that never became ready, refunds that stopped retrying and
// payments that never reached their order. It only reads, and exits 1 if
// anything is found. The API's sweeper runs the same check periodically.
//
//	go run ./cmd/order-reconcile
//
// It reads DATABASE_URL from the same .env files as the API, chosen by
// ENVIRONMENT (default development).
package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/signal"
	"time"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/AbhishekCS3459/find-me-backend/internal/orders"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/database"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	loadEnvFiles()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal().Msg("DATABASE_URL is not set")
	}
	os.Exit(run(dbURL))
}

// run returns the exit code, so its deferred cleanup runs before the process exits.
func run(dbURL string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	db, err := database.NewDB(ctx, dbURL)
	if err != nil {
		log.Error().Err(err).Msg("connect to database")
		return 1
	}
	defer db.Close()

	report, err := orders.Reconcile(ctx, db.Gorm, time.Now().UTC())
	if err != nil {
		log.Error().Err(err).Msg("order reconciliation failed")
		return 1
	}
	for _, d := range report.ReservedDrift {
		log.Warn().Str("inventory_id", d.InventoryID.String()).Int("reserved", d.Reserved).Int("held", d.Held).
			Msg("reserved units don't match active reservations")
	}
	warnIDs("order is past its deadline but still open", "order_id", report.Overdue)
	warnIDs("order was accepted over a day ago and never marked ready", "order_id", report.StaleAccepted)
	warnIDs("refund stopped retrying; refund it by hand", "payment_id", report.ParkedRefunds)
	warnIDs("payment succeeded but its order is unpaid", "payment_id", report.UnappliedPayments)
	if !report.Clean() {
		return 1
	}
	log.Info().Msg("orders reconcile clean")
	return 0
}

func warnIDs[T interface{ String() string }](msg, field string, ids []T) {
	for _, id := range ids {
		log.Warn().Str(field, id.String()).Msg(msg)
	}
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
