package outbox

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// recordingSink records every aggregate it is handed and fails the ones in fail.
type recordingSink struct {
	seen []uuid.UUID
	fail map[uuid.UUID]error
}

func (s *recordingSink) Publish(_ context.Context, messages []Message) map[uuid.UUID]error {
	errs := map[uuid.UUID]error{}
	for _, m := range messages {
		s.seen = append(s.seen, m.AggregateID)
		if err, ok := s.fail[m.AggregateID]; ok {
			errs[m.ID] = err
		}
	}
	return errs
}

func (s *recordingSink) times(id uuid.UUID) int {
	n := 0
	for _, seen := range s.seen {
		if seen == id {
			n++
		}
	}
	return n
}

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		t.Skip("DATABASE_URL / TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := database.NewDB(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(conn.Close)
	var column *string
	require.NoError(t, conn.Gorm.Raw(`
		SELECT column_name::text FROM information_schema.columns
		WHERE table_name = 'outbox_event' AND column_name = 'failed_at'`).Scan(&column).Error)
	if column == nil {
		t.Skip("migration 000043 (outbox retries) is not applied")
	}
	return conn.Gorm
}

// enqueueTestEvent commits one event and removes it when the test ends.
func enqueueTestEvent(t *testing.T, db *gorm.DB) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return Enqueue(tx, Event{
			AggregateType: "test", AggregateID: id, Type: "TestHappened", Payload: map[string]string{"k": "v"},
		})
	}))
	t.Cleanup(func() { _ = db.Exec(`DELETE FROM outbox_event WHERE aggregate_id = ?`, id).Error })
	return id
}

type eventState struct {
	PublishedAt   *time.Time
	FailedAt      *time.Time
	NextAttemptAt *time.Time
	Attempts      int
	LastError     *string
}

func state(t *testing.T, db *gorm.DB, aggregateID uuid.UUID) eventState {
	t.Helper()
	var s eventState
	require.NoError(t, db.Raw(`
		SELECT published_at, failed_at, next_attempt_at, attempts, last_error
		FROM outbox_event WHERE aggregate_id = ?`, aggregateID).Scan(&s).Error)
	return s
}

// publishUntilSeen runs batches until the sink has received every id, since
// other pending events may be ahead of them.
func publishUntilSeen(t *testing.T, p *Publisher, sink *recordingSink, ids ...uuid.UUID) {
	t.Helper()
	for range 50 {
		n, _ := p.PublishBatch(context.Background())
		if !slices.ContainsFunc(ids, func(id uuid.UUID) bool { return !slices.Contains(sink.seen, id) }) {
			return
		}
		if n == 0 {
			break
		}
	}
	t.Fatalf("events %v were not all published", ids)
}

// drain publishes until nothing is due.
func drain(t *testing.T, p *Publisher) {
	t.Helper()
	for range 100 {
		if n, _ := p.PublishBatch(context.Background()); n == 0 {
			return
		}
	}
	t.Fatal("the outbox never drained")
}

func TestPublisherMarksEventsPublished(t *testing.T) {
	db := testDB(t)
	id := enqueueTestEvent(t, db)
	assert.Nil(t, state(t, db, id).PublishedAt)

	sink := &recordingSink{}
	publishUntilSeen(t, NewPublisher(db, sink, PublisherConfig{}), sink, id)
	s := state(t, db, id)
	assert.NotNil(t, s.PublishedAt)
	assert.Nil(t, s.LastError)
}

func TestAFailedEventWaitsWithoutBlockingTheOthers(t *testing.T) {
	db := testDB(t)
	poison := enqueueTestEvent(t, db)
	good := enqueueTestEvent(t, db)

	sink := &recordingSink{fail: map[uuid.UUID]error{poison: errors.New("broker rejected it")}}
	p := NewPublisher(db, sink, PublisherConfig{RetryBase: time.Hour})
	publishUntilSeen(t, p, sink, poison, good)

	assert.NotNil(t, state(t, db, good).PublishedAt, "the event behind a failure is still delivered")
	s := state(t, db, poison)
	assert.Nil(t, s.PublishedAt)
	assert.Nil(t, s.FailedAt, "one failure only schedules a retry")
	assert.Equal(t, 1, s.Attempts)
	require.NotNil(t, s.LastError)
	assert.Equal(t, "broker rejected it", *s.LastError)
	require.NotNil(t, s.NextAttemptAt)
	assert.WithinDuration(t, time.Now().Add(time.Hour), *s.NextAttemptAt, 5*time.Minute)

	drain(t, p)
	assert.Equal(t, 1, sink.times(poison), "a failed event isn't retried before its next attempt is due")

	require.NoError(t, db.Exec(`UPDATE outbox_event SET next_attempt_at = NOW() WHERE aggregate_id = ?`, poison).Error)
	ok := &recordingSink{}
	publishUntilSeen(t, NewPublisher(db, ok, PublisherConfig{}), ok, poison)
	s = state(t, db, poison)
	assert.NotNil(t, s.PublishedAt, "the retry succeeds once the sink recovers")
	assert.Nil(t, s.LastError)
}

func TestAnEventIsParkedAfterMaxAttempts(t *testing.T) {
	db := testDB(t)
	poison := enqueueTestEvent(t, db)

	sink := &recordingSink{fail: map[uuid.UUID]error{poison: errors.New("malformed")}}
	p := NewPublisher(db, sink, PublisherConfig{MaxAttempts: 3, RetryBase: time.Millisecond})
	deadline := time.Now().Add(10 * time.Second)
	for state(t, db, poison).FailedAt == nil {
		require.True(t, time.Now().Before(deadline), "the event was never parked")
		if n, _ := p.PublishBatch(context.Background()); n == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	s := state(t, db, poison)
	assert.Nil(t, s.PublishedAt)
	assert.Equal(t, 3, s.Attempts)
	assert.Equal(t, 3, sink.times(poison))

	require.NoError(t, db.Exec(`UPDATE outbox_event SET next_attempt_at = NULL WHERE aggregate_id = ?`, poison).Error)
	drain(t, p)
	assert.Equal(t, 3, sink.times(poison), "a parked event is never handed out again")
}

func TestRetryDelayDoublesUpToTheCap(t *testing.T) {
	p := NewPublisher(nil, nil, PublisherConfig{RetryBase: time.Second, RetryMax: 10 * time.Second})
	for attempts, want := range map[int]time.Duration{
		1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 4: 8 * time.Second, 5: 10 * time.Second, 60: 10 * time.Second,
	} {
		assert.Equal(t, want, p.retryDelay(attempts), "after %d failures", attempts)
	}
}

func TestTruncateKeepsValidUTF8(t *testing.T) {
	assert.Equal(t, "ab", truncate("abc", 2))
	assert.Equal(t, "a", truncate("aé", 2), "a split character is dropped")
	assert.Equal(t, "short", truncate("short", 10))
}
