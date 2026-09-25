package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/rs/zerolog/log"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB wraps the database connection pool and optional GORM handle.
// Prefer golang-migrate for schema changes; do not AutoMigrate production tables.
type DB struct {
	Pool *pgxpool.Pool
	Gorm *gorm.DB
}

// NewDB creates a new database connection pool with production-ready settings
// and a GORM client that shares the same underlying pgx pool.
func NewDB(ctx context.Context, databaseURL string) (*DB, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	// Configure connection pool for production
	// These are sensible defaults that can be overridden via connection string parameters
	if config.MaxConns == 0 {
		config.MaxConns = 25 // Default max connections
	}
	if config.MinConns == 0 {
		config.MinConns = 5 // Keep minimum connections alive
	}
	if config.MaxConnLifetime == 0 {
		config.MaxConnLifetime = 30 * time.Minute // Recycle connections after 30min
	}
	if config.MaxConnIdleTime == 0 {
		config.MaxConnIdleTime = 5 * time.Minute // Close idle connections after 5min
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Test the connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	gormDB, err := openGorm(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}

	log.Info().Msg("database connected successfully (pgx + gorm)")

	return &DB{Pool: pool, Gorm: gormDB}, nil
}

func openGorm(pool *pgxpool.Pool) (*gorm.DB, error) {
	sqlDB := stdlib.OpenDBFromPool(pool)
	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: sqlDB,
	}), &gorm.Config{
		// Schema stays in migrations/; avoid AutoMigrate fighting migrate files.
		DisableForeignKeyConstraintWhenMigrating: true,
		Logger:                                   logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open gorm: %w", err)
	}
	return gormDB, nil
}

// Close closes the database connection pool
func (db *DB) Close() {
	if db.Pool != nil {
		db.Pool.Close()
		log.Info().Msg("database connection closed")
	}
}

// Health checks if the database is healthy
func (db *DB) Health(ctx context.Context) error {
	return db.Pool.Ping(ctx)
}
