package main

import (
	"context"
	"fmt"
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
	// Load .env file if it exists (for local development)
	_ = godotenv.Load()

	if err := run(); err != nil {
		log.Fatal().Err(err).Msg("server stopped with error")
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

	userService := identity.NewService(identity.NewRepository(db.Pool), cfg.JWTSecret)
	err = userService.BootstrapAdmin(ctx, cfg.BootstrapAdminEmail, cfg.BootstrapAdminPassword, cfg.BootstrapAdminPhone)
	if err != nil {
		return fmt.Errorf("bootstrap admin user: %w", err)
	}

	// Setup routes
	router := SetupRoutes(db, cfg)

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
