package outbox

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// Sink delivers messages somewhere. It returns an error for each message it
// could not deliver, keyed by message ID; every other message counts as
// delivered. A Sink must tolerate receiving a message more than once, and
// retries mean a message can arrive after a newer one for the same aggregate.
type Sink interface {
	Publish(ctx context.Context, messages []Message) map[uuid.UUID]error
}

// LogSink only logs. It stands in until a real consumer (notifications,
// analytics, a search index) needs the events.
type LogSink struct{}

func (LogSink) Publish(_ context.Context, messages []Message) map[uuid.UUID]error {
	for _, m := range messages {
		log.Debug().
			Int64("seq", m.Seq).
			Str("event_type", m.EventType).
			Str("aggregate_type", m.AggregateType).
			Str("aggregate_id", m.AggregateID.String()).
			RawJSON("payload", m.Payload).
			Msg("outbox event published")
	}
	return nil
}

type PublisherConfig struct {
	Interval  time.Duration // pause between polls once the backlog is empty
	BatchSize int
	Retention time.Duration // published events older than this are deleted
	// MaxAttempts is how many failed deliveries park an event in failed_at.
	MaxAttempts int
	RetryBase   time.Duration // wait after the first failure; doubles after each one
	RetryMax    time.Duration // longest wait between attempts
}

func DefaultPublisherConfig() PublisherConfig {
	return PublisherConfig{
		Interval:    2 * time.Second,
		BatchSize:   100,
		Retention:   7 * 24 * time.Hour,
		MaxAttempts: 10,
		RetryBase:   5 * time.Second,
		RetryMax:    10 * time.Minute,
	}
}

// Publisher polls outbox_event and hands pending events to a Sink. Several
// instances can run at once: each claims rows with FOR UPDATE SKIP LOCKED.
type Publisher struct {
	db   *gorm.DB
	sink Sink
	cfg  PublisherConfig
}

func NewPublisher(db *gorm.DB, sink Sink, cfg PublisherConfig) *Publisher {
	def := DefaultPublisherConfig()
	if cfg.Interval <= 0 {
		cfg.Interval = def.Interval
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = def.BatchSize
	}
	if cfg.Retention <= 0 {
		cfg.Retention = def.Retention
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = def.MaxAttempts
	}
	if cfg.RetryBase <= 0 {
		cfg.RetryBase = def.RetryBase
	}
	if cfg.RetryMax < cfg.RetryBase {
		cfg.RetryMax = max(def.RetryMax, cfg.RetryBase)
	}
	return &Publisher{db: db, sink: sink, cfg: cfg}
}

// Run publishes until ctx is cancelled.
func (p *Publisher) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		n, err := p.PublishBatch(ctx)
		if err != nil && ctx.Err() == nil {
			log.Error().Err(err).Msg("outbox publish failed")
		}
		if err := p.deletePublished(ctx); err != nil && ctx.Err() == nil {
			log.Error().Err(err).Msg("outbox cleanup failed")
		}
		// A full batch means more may be waiting, so poll again right away.
		wait := p.cfg.Interval
		if err == nil && n == p.cfg.BatchSize {
			wait = 0
		}
		timer.Reset(wait)
	}
}

// PublishBatch publishes up to BatchSize due events and returns how many it
// claimed. An event the Sink fails is retried after a growing delay, and after
// MaxAttempts failures it is parked in failed_at so it stops holding up the
// events behind it.
func (p *Publisher) PublishBatch(ctx context.Context) (int, error) {
	var claimed, failed, parked int
	err := p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var messages []Message
		err := tx.Raw(`
			SELECT id, seq, aggregate_type, aggregate_id, event_type, payload, created_at, attempts
			FROM outbox_event
			WHERE published_at IS NULL AND failed_at IS NULL
				AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())
			ORDER BY seq
			LIMIT ?
			FOR UPDATE SKIP LOCKED`, p.cfg.BatchSize).Scan(&messages).Error
		if err != nil {
			return fmt.Errorf("claim outbox events: %w", err)
		}
		claimed = len(messages)
		if claimed == 0 {
			return nil
		}
		errs := p.sink.Publish(ctx, messages)
		delivered := make([]uuid.UUID, 0, claimed)
		for _, m := range messages {
			sendErr, ok := errs[m.ID]
			if !ok || sendErr == nil {
				delivered = append(delivered, m.ID)
				continue
			}
			failed++
			wasParked, err := p.recordFailure(tx, m, sendErr)
			if err != nil {
				return fmt.Errorf("record outbox failure: %w", err)
			}
			if wasParked {
				parked++
			}
		}
		if len(delivered) == 0 {
			return nil
		}
		return tx.Exec(`UPDATE outbox_event SET published_at = NOW(), last_error = NULL WHERE id IN ?`,
			delivered).Error
	})
	if err != nil {
		return 0, err
	}
	if failed > 0 {
		return claimed, fmt.Errorf("publish outbox events: %d of %d failed, %d parked after %d attempts",
			failed, claimed, parked, p.cfg.MaxAttempts)
	}
	return claimed, nil
}

// recordFailure schedules the next attempt, or parks the event once it has
// failed MaxAttempts times, and reports whether it parked it.
func (p *Publisher) recordFailure(tx *gorm.DB, m Message, sendErr error) (bool, error) {
	attempts := m.Attempts + 1
	lastError := truncate(sendErr.Error(), 1000)
	if attempts >= p.cfg.MaxAttempts {
		if err := tx.Exec(`UPDATE outbox_event SET attempts = ?, last_error = ?, failed_at = NOW() WHERE id = ?`,
			attempts, lastError, m.ID).Error; err != nil {
			return false, err
		}
		log.Error().
			Str("event_id", m.ID.String()).
			Int64("seq", m.Seq).
			Str("event_type", m.EventType).
			Int("attempts", attempts).
			Str("last_error", lastError).
			Msg("outbox event parked after too many failed deliveries")
		return true, nil
	}
	return false, tx.Exec(`
		UPDATE outbox_event
		SET attempts = ?, last_error = ?, next_attempt_at = NOW() + make_interval(secs => ?)
		WHERE id = ?`, attempts, lastError, p.retryDelay(attempts).Seconds(), m.ID).Error
}

// retryDelay is RetryBase doubled for each failure after the first, capped at
// RetryMax.
func (p *Publisher) retryDelay(attempts int) time.Duration {
	delay := p.cfg.RetryBase
	for i := 1; i < attempts && delay < p.cfg.RetryMax; i++ {
		delay *= 2
	}
	return min(delay, p.cfg.RetryMax)
}

func (p *Publisher) deletePublished(ctx context.Context) error {
	cutoff := time.Now().Add(-p.cfg.Retention)
	return p.db.WithContext(ctx).Exec(`
		DELETE FROM outbox_event
		WHERE id IN (
			SELECT id FROM outbox_event
			WHERE published_at IS NOT NULL AND published_at < ?
			LIMIT 1000
		)`, cutoff).Error
}

// truncate cuts s to at most n bytes without splitting a UTF-8 character,
// which PostgreSQL would reject.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}
