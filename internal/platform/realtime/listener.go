package realtime

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

const (
	// idleCheck is how long the listener waits for a notification before
	// pinging, so a connection dropped by a firewall or proxy is noticed.
	idleCheck    = 30 * time.Second
	pingTimeout  = 5 * time.Second
	retryBase    = time.Second
	retryMax     = 30 * time.Second
	healthyAfter = time.Minute
)

// Listen relays every notification on channel to hub, using the payload as the
// topic, until ctx ends. It holds one dedicated connection, not one from the
// pool, and reconnects with backoff. Notifications sent while disconnected are
// lost, so after reconnecting every subscriber is told to reload.
//
// config must reach PostgreSQL directly or through a session-mode pooler:
// LISTEN does not work through a transaction-mode pooler.
func Listen(ctx context.Context, config *pgx.ConnConfig, channel string, hub *Hub) {
	var failures int
	for retrying := false; ctx.Err() == nil; retrying = true {
		started := time.Now()
		err := listen(ctx, config, channel, hub, retrying)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > healthyAfter {
			failures = 0
		}
		failures++
		wait := backoff(failures)
		log.Warn().Err(err).Str("channel", channel).Dur("retry_in", wait).Msg("realtime listener disconnected")
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func listen(ctx context.Context, config *pgx.ConnConfig, channel string, hub *Hub, retrying bool) error {
	conn, err := pgx.ConnectConfig(ctx, config.Copy())
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), pingTimeout)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
		return err
	}
	log.Info().Str("channel", channel).Msg("realtime listener connected")
	if retrying {
		hub.PublishAll()
	}
	for {
		waitCtx, cancel := context.WithTimeout(ctx, idleCheck)
		notification, err := conn.WaitForNotification(waitCtx)
		cancel()
		switch {
		case err == nil:
			hub.Publish(notification.Payload)
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, context.DeadlineExceeded):
			pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
			err = conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return err
			}
		default:
			return err
		}
	}
}

// backoff doubles from retryBase up to retryMax, with jitter so instances
// don't all reconnect at once after a database restart.
func backoff(failures int) time.Duration {
	wait := retryBase
	for i := 1; i < failures && wait < retryMax; i++ {
		wait *= 2
	}
	wait = min(wait, retryMax)
	return wait/2 + rand.N(wait/2+1)
}
