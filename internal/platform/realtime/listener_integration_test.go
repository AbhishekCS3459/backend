package realtime

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestListenRelaysCommittedNotifications(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		t.Skip("DATABASE_URL / TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := database.NewDB(ctx, dbURL)
	require.NoError(t, err)
	defer db.Close()

	channel := "realtime_test_" + uuid.NewString()[:8]
	hub := NewHub(0)
	committed, stopCommitted, err := hub.Subscribe("committed")
	require.NoError(t, err)
	defer stopCommitted()
	rolledBack, stopRolledBack, err := hub.Subscribe("rolled-back")
	require.NoError(t, err)
	defer stopRolledBack()

	done := make(chan struct{})
	go func() {
		defer close(done)
		Listen(ctx, db.Pool.Config().ConnConfig, channel, hub)
	}()

	// LISTEN is asynchronous; keep notifying until the listener hears one.
	require.Eventually(t, func() bool {
		return Notify(db.Gorm, channel, "committed") == nil && pending(committed)
	}, 5*time.Second, 50*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	pending(committed)

	errRollback := errors.New("roll back")
	err = db.Gorm.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, Notify(tx, channel, "rolled-back"))
		return errRollback
	})
	require.ErrorIs(t, err, errRollback)

	err = db.Gorm.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, Notify(tx, channel, "committed"))
		require.NoError(t, Notify(tx, channel, "committed"))
		time.Sleep(200 * time.Millisecond)
		require.False(t, pending(committed), "a notification must not arrive before its transaction commits")
		return nil
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return pending(committed) }, 2*time.Second, 10*time.Millisecond)
	require.False(t, pending(rolledBack), "a rolled-back transaction must not notify")

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Listen did not stop when its context ended")
	}
}
