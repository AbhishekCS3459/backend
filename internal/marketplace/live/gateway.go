package live

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"github.com/AbhishekCS3459/find-me-backend/internal/availability"
)

var (
	// ErrBusy means the gateway already holds as many subscriptions as it allows.
	ErrBusy = errors.New("live: too many subscriptions")
	// ErrClosed means the gateway is shutting down.
	ErrClosed = errors.New("live: gateway closed")
)

const (
	// commandTimeout bounds one SUBSCRIBE or UNSUBSCRIBE. One that fails is
	// not lost: go-redis subscribes to every wanted channel when it reconnects.
	commandTimeout = 5 * time.Second
	// minTimestampStep: an update that only moves the stock time on by less
	// than this is not worth a message.
	minTimestampStep = time.Minute
)

// GatewayConfig tunes a Gateway.
type GatewayConfig struct {
	// StaleAfter shows stock unconfirmed for longer as CONFIRM_WITH_STORE, as the marketplace does.
	StaleAfter time.Duration
	// MaxSubscriptions caps the subscriptions, and so the streams, one instance holds.
	MaxSubscriptions int
}

// Gateway shares one Redis connection among every stream of this instance.
// It subscribes to a product's channel while at least one stream watches it.
type Gateway struct {
	pubsub     *redis.PubSub
	staleAfter time.Duration
	maxSubs    int
	now        func() time.Time
	// commands orders SUBSCRIBE and UNSUBSCRIBE, so a quick leave and rejoin
	// can't leave the channel unsubscribed.
	commands chan command
	done     chan struct{}
	stopped  sync.WaitGroup

	mu     sync.Mutex
	topics map[string]*topic
	// awaiting counts each channel's SUBSCRIBE commands not yet confirmed.
	awaiting map[string]int
	subs     int
	closed   bool
}

type command struct {
	channel   string
	subscribe bool
}

// topic is one product channel and the subscriptions watching it.
type topic struct {
	subs map[*Subscription]struct{}
	// ready closes once Redis confirms the subscription; nothing published
	// before then reaches this instance.
	ready     chan struct{}
	confirmed bool
	// last is the newest update per store, to drop repeats and changes customers can't see.
	last map[uuid.UUID]Update
}

// NewGateway starts a gateway on rdb. Redis need not be reachable yet.
func NewGateway(rdb *redis.Client, cfg GatewayConfig) *Gateway {
	if cfg.StaleAfter <= 0 {
		cfg.StaleAfter = 14 * 24 * time.Hour
	}
	if cfg.MaxSubscriptions <= 0 {
		cfg.MaxSubscriptions = 10000
	}
	g := &Gateway{
		pubsub:     rdb.Subscribe(context.Background()),
		staleAfter: cfg.StaleAfter,
		maxSubs:    cfg.MaxSubscriptions,
		now:        time.Now,
		commands:   make(chan command, 1024),
		done:       make(chan struct{}),
		topics:     make(map[string]*topic),
		awaiting:   make(map[string]int),
	}
	// Subscription confirmations come through too: a repeated one means go-redis reconnected.
	messages := g.pubsub.ChannelWithSubscriptions(redis.WithChannelSize(1024))
	g.stopped.Add(2)
	go g.receive(messages)
	go g.runCommands()
	return g
}

// Done is closed when the gateway starts shutting down.
func (g *Gateway) Done() <-chan struct{} { return g.done }

// Close ends every stream and the Redis subscription. Safe to call more than once.
func (g *Gateway) Close() {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.closed = true
	close(g.done)
	g.mu.Unlock()
	if err := g.pubsub.Close(); err != nil {
		log.Warn().Err(err).Msg("live gateway: closing redis subscription failed")
	}
	g.stopped.Wait()
}

// Subscribe watches a product's updates, only those of store unless it is
// uuid.Nil. Close the subscription when done.
func (g *Gateway) Subscribe(catalogKey string, store uuid.UUID) (*Subscription, error) {
	channel := Channel(catalogKey)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, ErrClosed
	}
	if g.subs >= g.maxSubs {
		return nil, ErrBusy
	}
	t := g.topics[channel]
	if t == nil {
		t = &topic{subs: make(map[*Subscription]struct{}), ready: make(chan struct{}), last: make(map[uuid.UUID]Update)}
		g.topics[channel] = t
		g.awaiting[channel]++
		g.command(command{channel: channel, subscribe: true})
	}
	s := &Subscription{
		gateway: g,
		channel: channel,
		store:   store,
		ready:   t.ready,
		notify:  make(chan struct{}, 1),
		pending: make(map[uuid.UUID]Update),
	}
	t.subs[s] = struct{}{}
	g.subs++
	return s, nil
}

func (g *Gateway) unsubscribe(s *Subscription) {
	g.mu.Lock()
	defer g.mu.Unlock()
	t := g.topics[s.channel]
	if t == nil {
		return
	}
	if _, ok := t.subs[s]; !ok {
		return
	}
	delete(t.subs, s)
	g.subs--
	if len(t.subs) == 0 {
		delete(g.topics, s.channel)
		if !g.closed {
			g.command(command{channel: s.channel})
		}
	}
}

// command queues a Redis command. Call with g.mu held.
func (g *Gateway) command(c command) {
	select {
	case g.commands <- c:
	case <-g.done:
	}
}

func (g *Gateway) runCommands() {
	defer g.stopped.Done()
	for {
		select {
		case <-g.done:
			return
		case c := <-g.commands:
			ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
			var err error
			if c.subscribe {
				err = g.pubsub.Subscribe(ctx, c.channel)
			} else {
				err = g.pubsub.Unsubscribe(ctx, c.channel)
			}
			cancel()
			if err != nil {
				log.Warn().Err(err).Str("channel", c.channel).Bool("subscribe", c.subscribe).
					Msg("live gateway: redis command failed; retried on reconnect")
			}
		}
	}
}

func (g *Gateway) receive(messages <-chan any) {
	defer g.stopped.Done()
	for msg := range messages {
		switch m := msg.(type) {
		case *redis.Subscription:
			if m.Kind == "subscribe" {
				g.confirmed(m.Channel)
			}
		case *redis.Message:
			var u Update
			if err := json.Unmarshal([]byte(m.Payload), &u); err != nil {
				log.Warn().Err(err).Str("channel", m.Channel).Msg("live gateway: unreadable update dropped")
				continue
			}
			g.dispatch(m.Channel, u)
		}
	}
}

// confirmed handles Redis confirming a subscription to channel: the first
// confirmation makes the topic ready, a later one follows a reconnect.
func (g *Gateway) confirmed(channel string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.awaiting[channel] > 0 {
		g.awaiting[channel]--
		if g.awaiting[channel] == 0 {
			delete(g.awaiting, channel)
		}
	}
	t := g.topics[channel]
	if t == nil {
		return
	}
	if !t.confirmed {
		// A confirmation for an earlier SUBSCRIBE doesn't cover this topic's.
		if g.awaiting[channel] == 0 {
			t.confirmed = true
			close(t.ready)
		}
		return
	}
	// Anything published while the connection was down never arrived.
	clear(t.last)
	for s := range t.subs {
		s.markResync()
	}
}

// dispatch hands u to the topic's subscriptions unless it is old or changes
// nothing customers see.
func (g *Gateway) dispatch(channel string, u Update) {
	g.mu.Lock()
	defer g.mu.Unlock()
	t := g.topics[channel]
	if t == nil {
		return
	}
	prev, seen := t.last[u.StoreID]
	if seen && u.Version <= prev.Version {
		return
	}
	t.last[u.StoreID] = u
	if seen && !visibleChange(prev, u) {
		return
	}
	for s := range t.subs {
		s.offer(u)
	}
}

func visibleChange(prev, next Update) bool {
	return prev.AvailabilityBucket != next.AvailabilityBucket ||
		prev.MaxOrderQuantity != next.MaxOrderQuantity ||
		prev.Price != next.Price ||
		prev.Searchable != next.Searchable ||
		next.LastStockUpdateAt.Sub(prev.LastStockUpdateAt) >= minTimestampStep
}

// customerView applies the marketplace's staleness rule to u.
func (g *Gateway) customerView(u Update) Update {
	if u.LastStockUpdateAt.Before(g.now().Add(-g.staleAfter)) {
		u.AvailabilityBucket = string(availability.ConfirmWithStore)
	}
	return u
}

// Subscription is one stream's view of a product. Updates for the same store
// coalesce until the stream takes them, so a slow stream never holds up others.
type Subscription struct {
	gateway *Gateway
	channel string
	store   uuid.UUID
	ready   <-chan struct{}
	notify  chan struct{}

	mu      sync.Mutex
	pending map[uuid.UUID]Update
	resync  bool
}

// Ready is closed once updates published from now on will arrive.
func (s *Subscription) Ready() <-chan struct{} { return s.ready }

// Notify receives a value when Take has something new.
func (s *Subscription) Notify() <-chan struct{} { return s.notify }

// Take returns the updates since the last call, newest per store, and whether
// some were lost so the client must fetch a fresh snapshot.
func (s *Subscription) Take() (updates []Update, resync bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.pending {
		updates = append(updates, s.gateway.customerView(u))
	}
	clear(s.pending)
	resync, s.resync = s.resync, false
	return updates, resync
}

// Close stops the subscription. Safe to call more than once.
func (s *Subscription) Close() { s.gateway.unsubscribe(s) }

func (s *Subscription) offer(u Update) {
	if s.store != uuid.Nil && u.StoreID != s.store {
		return
	}
	s.mu.Lock()
	s.pending[u.StoreID] = u
	s.mu.Unlock()
	s.signal()
}

func (s *Subscription) markResync() {
	s.mu.Lock()
	s.resync = true
	clear(s.pending)
	s.mu.Unlock()
	s.signal()
}

func (s *Subscription) signal() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}
