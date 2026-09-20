package database_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/database"
	"github.com/stretchr/testify/require"
)

func TestGormSharesPool(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		t.Skip("DATABASE_URL / TEST_DATABASE_URL not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := database.NewDB(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(db.Close)

	require.NotNil(t, db.Pool)
	require.NotNil(t, db.Gorm)

	var one int
	require.NoError(t, db.Gorm.WithContext(ctx).Raw("SELECT 1").Scan(&one).Error)
	require.Equal(t, 1, one)
}
