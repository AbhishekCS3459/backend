package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration loaded from environment variables
type Config struct {
	// Server
	Port        string
	Environment string
	LogLevel    string

	// Database
	DatabaseURL string

	// MongoDB (optional - product catalogue)
	MongoDBURL             string
	MongoCatalogDatabase   string
	MongoCatalogCollection string
	// JWT
	JWTSecret string

	// Optional: API Access Token (for service-to-service auth)
	APIAccessToken string

	// Rate Limiting
	RateLimitEnabled        bool
	RateLimitRequestsPerSec float64
	RateLimitBurst          int

	// Queue (optional - for background job processing)
	QueueURL string // Redis URL or empty for in-memory queue

	// Redis (optional - Truecaller session cache, falling back to memory, and
	// the bus for live marketplace updates, which are off without it)
	RedisURL string

	// OutboxPublisherEnabled runs the outbox publisher in this process. Safe
	// on several instances at once.
	OutboxPublisherEnabled bool
	// OutboxPollInterval is the publisher's pause once the outbox is empty,
	// and so the usual delay before a live update goes out.
	OutboxPollInterval time.Duration

	// CORSAllowedOrigins are added to the built-in origins (e.g. a test UI).
	CORSAllowedOrigins []string

	// Marketplace (public customer search)
	// MarketplaceStaleAfter: stock unconfirmed for longer shows CONFIRM_WITH_STORE.
	MarketplaceStaleAfter time.Duration
	// MarketplaceDebug lets X-Debug: 1 return exact quantities. Always false in production.
	MarketplaceDebug bool
	// MarketplaceLiveMaxPerIP caps the live availability streams one client address holds open.
	MarketplaceLiveMaxPerIP int

	// Bootstrap admin (optional - created on startup if no admin exists)
	BootstrapAdminEmail    string
	BootstrapAdminPassword string
	BootstrapAdminPhone    string
}

const (
	environmentProduction = "production"
	defaultJWTSecret      = "dev-secret-change-in-production"
)

// LoadConfig loads configuration from environment variables with validation
func LoadConfig() (*Config, error) {
	cfg := &Config{}

	// Server
	cfg.Port = getEnv("PORT", "8080")
	cfg.Environment = getEnv("ENVIRONMENT", "development")
	cfg.LogLevel = getEnv("LOG_LEVEL", "info")

	// Database - required
	cfg.DatabaseURL = getEnvRequired("DATABASE_URL")
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("required environment variable DATABASE_URL is not set")
	}

	cfg.MongoDBURL = getEnv("DATABASE_URL_MONGODB_PROD", "")
	cfg.MongoCatalogDatabase = getEnv("MONGODB_CATALOG_DATABASE", "catalog")
	cfg.MongoCatalogCollection = getEnv("MONGODB_CATALOG_COLLECTION", "products")
	production := cfg.Environment == environmentProduction

	// JWT - required in production, but has default for development
	cfg.JWTSecret = getEnv("JWT_SECRET", defaultJWTSecret)
	if cfg.JWTSecret == defaultJWTSecret && production {
		return nil, fmt.Errorf("JWT_SECRET must be set in production environment")
	}
	if cfg.JWTSecret == defaultJWTSecret {
		fmt.Fprintf(os.Stderr, "WARNING: Using default JWT_SECRET. Change this in production!\n")
	}

	// API Access Token (optional - for service-to-service auth without login)
	cfg.APIAccessToken = getEnv("API_ACCESS_TOKEN", "")

	// Rate Limiting (enabled by default in production)
	cfg.RateLimitEnabled = getEnvBool("RATE_LIMIT_ENABLED", true)
	cfg.RateLimitRequestsPerSec = parseFloat(getEnv("RATE_LIMIT_REQUESTS_PER_SEC", "10.0"), 10.0)
	cfg.RateLimitBurst = parseInt(getEnv("RATE_LIMIT_BURST", "20"), 20)

	// Queue (optional - Redis URL for production, empty for in-memory in development)
	cfg.QueueURL = getEnv("QUEUE_URL", "")
	cfg.RedisURL = getEnv("REDIS_URL", cfg.QueueURL)
	cfg.OutboxPublisherEnabled = getEnvBool("OUTBOX_PUBLISHER_ENABLED", true)
	cfg.OutboxPollInterval = parseDuration(getEnv("OUTBOX_POLL_INTERVAL", "250ms"), 250*time.Millisecond)

	for _, origin := range strings.Split(getEnv("CORS_ALLOWED_ORIGINS", ""), ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			cfg.CORSAllowedOrigins = append(cfg.CORSAllowedOrigins, origin)
		}
	}

	cfg.MarketplaceStaleAfter = parseDuration(getEnv("MARKETPLACE_STALE_AFTER", "336h"), 14*24*time.Hour)
	// Never in production, whatever the flag says: debug mode exposes exact stock.
	marketplaceDebug := getEnvBool("DEBUG_MARKETPLACE", false)
	cfg.MarketplaceDebug = marketplaceDebug && !production
	if marketplaceDebug && production {
		fmt.Fprintf(os.Stderr, "WARNING: DEBUG_MARKETPLACE is ignored in production\n")
	}
	cfg.MarketplaceLiveMaxPerIP = parseInt(getEnv("MARKETPLACE_LIVE_MAX_PER_IP", "30"), 30)
	cfg.BootstrapAdminEmail = getEnv("BOOTSTRAP_ADMIN_EMAIL", "")
	cfg.BootstrapAdminPassword = getEnv("BOOTSTRAP_ADMIN_PASSWORD", "")
	cfg.BootstrapAdminPhone = getEnv("BOOTSTRAP_ADMIN_PHONE", "+10000000000")

	return cfg, nil
}

// DatabaseTarget returns host/database from DatabaseURL without credentials, for logging.
func (c *Config) DatabaseTarget() string {
	u, err := url.Parse(c.DatabaseURL)
	if err != nil || u.Host == "" {
		return "unknown"
	}
	return u.Host + u.Path
}

// getEnv gets an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return strings.TrimSpace(value)
}

// getEnvBool reports whether an environment variable is "true", or returns defaultValue when it is unset
func getEnvBool(key string, defaultValue bool) bool {
	value := getEnv(key, "")
	if value == "" {
		return defaultValue
	}
	return value == "true"
}

// getEnvRequired gets a required environment variable or returns empty string
func getEnvRequired(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

// parseInt parses an integer from a string, returning defaultValue on error
func parseInt(s string, defaultValue int) int {
	val, err := strconv.Atoi(s)
	if err != nil {
		return defaultValue
	}
	return val
}

// parseDuration parses a positive duration such as "336h", returning defaultValue otherwise
func parseDuration(s string, defaultValue time.Duration) time.Duration {
	val, err := time.ParseDuration(s)
	if err != nil || val <= 0 {
		return defaultValue
	}
	return val
}

// parseFloat parses a float from a string, returning defaultValue on error
func parseFloat(s string, defaultValue float64) float64 {
	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return defaultValue
	}
	return val
}
