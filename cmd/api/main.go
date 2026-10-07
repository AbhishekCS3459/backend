package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/AbhishekCS3459/find-me-backend/internal/identity"
	"github.com/AbhishekCS3459/find-me-backend/internal/inventory"
	"github.com/AbhishekCS3459/find-me-backend/internal/marketplace/live"
	"github.com/AbhishekCS3459/find-me-backend/internal/orders"
	"github.com/AbhishekCS3459/find-me-backend/internal/payment"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/database"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/mongodb"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/outbox"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/realtime"
	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
)

// @title           Find Me API
// @version         1.0.0
// @description     Find Me backend API with PostgreSQL, JWT authentication, and role-based access.
// @description     **Features:**
// @description     - Clean Architecture (Handler-Service-Repository)
// @description     - JWT Authentication
// @description     - Database Migrations
// @description     - OpenAPI Documentation
// @contact.name    Abhishek Kumar Vema
// @contact.url     https://github.com/AbhishekCS3459
// @host            localhost:8080
// @BasePath        /
// @schemes         http https
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and JWT token.

func main() {
	loadEnvFiles()

	if err := run(); err != nil {
		log.Fatal().Err(err).Msg("server stopped with error")
	}
}

// loadEnvFiles loads .env files for ENVIRONMENT (default "development").
// The first file to define a key wins, and variables already set in the process
// environment (e.g. Azure App Settings) are never overridden.
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

func run() error {
	// Load configuration
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Configure logging
	setupLogging(cfg.LogLevel)

	ctx := context.Background()

	// Connect to database
	db, err := database.NewDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()

	mongoClient := connectMongo(ctx, cfg.MongoDBURL)
	if mongoClient != nil {
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := mongoClient.Close(closeCtx); err != nil {
				log.Error().Err(err).Msg("mongodb close failed")
			}
		}()
	}

	userService := identity.NewService(identity.NewRepository(db.Pool), cfg.JWTSecret)
	err = userService.BootstrapAdmin(ctx, cfg.BootstrapAdminEmail, cfg.BootstrapAdminPassword, cfg.BootstrapAdminPhone)
	if err != nil {
		return fmt.Errorf("bootstrap admin user: %w", err)
	}

	rdb := connectRedis(ctx, cfg.RedisURL)
	if rdb != nil {
		defer func() {
			if err := rdb.Close(); err != nil {
				log.Error().Err(err).Msg("redis close failed")
			}
		}()
	}

	// Without Redis, events are only logged and customers poll for changes.
	var sink outbox.Sink = outbox.LogSink{}
	var gateway *live.Gateway
	if rdb != nil {
		sink = live.NewSink(rdb)
		gateway = live.NewGateway(rdb, live.GatewayConfig{
			StaleAfter:       cfg.MarketplaceStaleAfter,
			MaxSubscriptions: maxMarketplaceStreams,
		})
		defer gateway.Close()
	}

	stopPublisher := startOutboxPublisher(db, sink, cfg)
	defer stopPublisher()

	hub := realtime.NewHub(maxLiveStreams)
	stopListener := startListener(db, inventory.ChangesChannel, hub)
	defer stopListener()

	orderHub := realtime.NewHub(maxLiveStreams)
	stopOrderListener := startListener(db, orders.ChangesChannel, orderHub)
	defer stopOrderListener()
	orderService := orders.NewService(db.Gorm, inventory.NewLedger(), storeaccess.NewResolver(db.Gorm),
		payment.NewDummy(), orders.Config{
			PaymentHold:   cfg.OrderPaymentHold,
			AcceptTimeout: cfg.OrderAcceptTimeout,
			PickupWindow:  cfg.OrderPickupWindow,
			PickupSecret:  []byte(cfg.OrderPickupSecret),
		})
	stopSweeper := startOrderSweeper(orderService, cfg)
	defer stopSweeper()

	// Setup routes
	router := SetupRoutes(db, mongoClient, rdb, cfg, hub, gateway, orders.NewHandler(orderService, orderHub))

	// Create HTTP server
	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	// Shutdown waits for open requests; event streams would otherwise hold it until its deadline.
	server.RegisterOnShutdown(hub.Close)
	server.RegisterOnShutdown(orderHub.Close)
	if gateway != nil {
		server.RegisterOnShutdown(gateway.Close)
	}

	// Start server in a goroutine
	go func() {
		log.Info().
			Str("addr", server.Addr).
			Str("environment", cfg.Environment).
			Str("database", cfg.DatabaseTarget()).
			Str("log_level", cfg.LogLevel).
			Msg("server starting")

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server failed to start")
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("server shutting down")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("server forced to shutdown")
	}

	log.Info().Msg("server exited")
	return nil
}

// startOutboxPublisher runs the publisher until the returned stop function is
// called; stop waits for an in-flight batch to finish.
func startOutboxPublisher(db *database.DB, sink outbox.Sink, cfg *Config) (stop func()) {
	if !cfg.OutboxPublisherEnabled {
		log.Info().Msg("outbox publisher disabled")
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	publisherCfg := outbox.DefaultPublisherConfig()
	publisherCfg.Interval = cfg.OutboxPollInterval
	publisher := outbox.NewPublisher(db.Gorm, sink, publisherCfg)
	go func() {
		defer close(done)
		publisher.Run(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

// startOrderSweeper closes overdue orders and retries refunds until the
// returned stop function is called; stop waits for the current pass.
func startOrderSweeper(svc *orders.Service, cfg *Config) (stop func()) {
	if !cfg.OrderSweeperEnabled {
		log.Info().Msg("order sweeper disabled")
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.RunSweeper(ctx, cfg.OrderSweepInterval)
	}()
	return func() {
		cancel()
		<-done
	}
}

// maxLiveStreams caps the retailer event streams (per kind) one instance holds open.
const maxLiveStreams = 10000

// maxMarketplaceStreams caps the customer availability streams one instance holds open.
const maxMarketplaceStreams = 20000

// connectRedis returns nil when Redis is not configured. A configured but
// unreachable Redis still returns a client: it connects once Redis is up.
func connectRedis(ctx context.Context, rawURL string) *redis.Client {
	if rawURL == "" {
		log.Warn().Msg("REDIS_URL not set; live marketplace updates disabled")
		return nil
	}
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		log.Error().Err(err).Msg("invalid REDIS_URL; live marketplace updates disabled")
		return nil
	}
	client := redis.NewClient(opts)
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		log.Warn().Err(err).Str("addr", opts.Addr).Msg("redis unreachable; live marketplace updates start once it is")
	} else {
		log.Info().Str("addr", opts.Addr).Msg("redis connected; live marketplace updates enabled")
	}
	return client
}

// startListener relays changes committed by any instance on channel to this
// instance's event streams, until the returned stop function is called.
func startListener(db *database.DB, channel string, hub *realtime.Hub) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		realtime.Listen(ctx, db.Pool.Config().ConnConfig, channel, hub)
	}()
	return func() {
		cancel()
		<-done
	}
}

// connectMongo returns nil when MongoDB is not configured or unreachable, so the
// rest of the API keeps serving while the catalogue is unavailable.
func connectMongo(ctx context.Context, uri string) *mongodb.Client {
	if uri == "" {
		log.Warn().Msg("DATABASE_URL_MONGODB_PROD not set; product catalogue disabled")
		return nil
	}
	client, err := mongodb.Connect(ctx, uri)
	if err != nil {
		log.Error().Err(err).Msg("mongodb connection failed; product catalogue disabled")
		return nil
	}
	return client
}

// setupLogging configures the global logger
func setupLogging(level string) {
	// Set log level
	switch level {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	// Pretty console output for development
	if os.Getenv("ENVIRONMENT") == "development" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}
}
