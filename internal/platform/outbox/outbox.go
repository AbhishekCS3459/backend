// Package outbox records domain events in the same database transaction as the
// change that caused them, and publishes them afterwards. An event is written
// if and only if its change commits, so consumers never miss or invent one.
package outbox

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Event is a change to publish. Payload is marshalled to JSON.
type Event struct {
	AggregateType string
	AggregateID   uuid.UUID
	Type          string
	Payload       any
}

// Message is a stored event as handed to a Sink.
type Message struct {
	ID            uuid.UUID
	Seq           int64
	AggregateType string
	AggregateID   uuid.UUID
	EventType     string
	Payload       json.RawMessage
	CreatedAt     time.Time
	Attempts      int
}

// enqueueBatch keeps each INSERT well under PostgreSQL's 65535 parameter limit.
const enqueueBatch = 1000

// Enqueue stores events inside tx, which must be the transaction making the change.
func Enqueue(tx *gorm.DB, events ...Event) error {
	for start := 0; start < len(events); start += enqueueBatch {
		chunk := events[start:min(start+enqueueBatch, len(events))]
		values := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)*4)
		for i, e := range chunk {
			payload, err := json.Marshal(e.Payload)
			if err != nil {
				return fmt.Errorf("marshal %s event: %w", e.Type, err)
			}
			values[i] = "(?, ?, ?, ?::jsonb)"
			args = append(args, e.AggregateType, e.AggregateID, e.Type, string(payload))
		}
		err := tx.Exec(`INSERT INTO outbox_event (aggregate_type, aggregate_id, event_type, payload) VALUES `+
			strings.Join(values, ", "), args...).Error
		if err != nil {
			return fmt.Errorf("enqueue events: %w", err)
		}
	}
	return nil
}
