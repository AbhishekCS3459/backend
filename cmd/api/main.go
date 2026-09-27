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
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/AbhishekCS3459/find-me-backend/internal/identity"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/database"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/mongodb"
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

	// Setup routes
	router := SetupRoutes(db, mongoClient, cfg)

	// Create HTTP server
	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
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
