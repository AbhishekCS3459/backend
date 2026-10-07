package live

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"github.com/AbhishekCS3459/find-me-backend/internal/availability"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/outbox"
)

// publishTimeout bounds one batch; the publisher holds its outbox rows locked meanwhile.
const publishTimeout = 3 * time.Second

// Sink publishes InventoryChanged events to their product's Redis channel.
// Other events have no subscribers yet, so they are only logged.
type Sink struct {
	rdb *redis.Client
}

func NewSink(rdb *redis.Client) *Sink { return &Sink{rdb: rdb} }

func (s *Sink) Publish(ctx context.Context, messages []outbox.Message) map[uuid.UUID]error {
	outbox.LogSink{}.Publish(ctx, messages)

	ctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	pipe := s.rdb.Pipeline()
	cmds := make(map[uuid.UUID]*redis.IntCmd, len(messages))
	for _, m := range messages {
		u, ok := updateFrom(m)
		if !ok {
			continue
		}
		payload, err := json.Marshal(u)
		if err != nil {
			log.Error().Err(err).Str("event_id", m.ID.String()).Msg("live update not encodable")
			continue
		}
		cmds[m.ID] = pipe.Publish(ctx, Channel(u.CatalogKey), payload)
	}
	if len(cmds) == 0 {
		return nil
	}
	if _, err := pipe.Exec(ctx); err == nil {
		return nil
	}
	errs := make(map[uuid.UUID]error, len(cmds))
	for id, cmd := range cmds {
		if err := cmd.Err(); err != nil {
			errs[id] = err
		}
	}
	return errs
}

// updateFrom reads the customer-visible part of an InventoryChanged event.
// It reports false for any other event, and for one it can't use: retrying
// would never make that one deliverable.
func updateFrom(m outbox.Message) (Update, bool) {
	if m.EventType != availability.EventInventoryChanged {
		return Update{}, false
	}
	var row availability.Row
	if err := json.Unmarshal(m.Payload, &row); err != nil {
		log.Warn().Err(err).Str("event_id", m.ID.String()).Msg("InventoryChanged payload unreadable; not sent live")
		return Update{}, false
	}
	price, err := strconv.ParseFloat(row.Price, 64)
	if err != nil || row.CatalogKey == "" || row.StoreID == uuid.Nil {
		log.Warn().Str("event_id", m.ID.String()).Msg("InventoryChanged payload incomplete; not sent live")
		return Update{}, false
	}
	return Update{
		CatalogKey:         row.CatalogKey,
		StoreID:            row.StoreID,
		Price:              price,
		AvailabilityBucket: string(row.Bucket),
		MaxOrderQuantity:   availability.OrderableQuantity(row.AvailableQty),
		Searchable:         row.Searchable,
		LastStockUpdateAt:  row.LastStockUpdateAt,
		Version:            row.Version,
	}, true
}
