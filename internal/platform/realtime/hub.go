// Package realtime tells connected screens that something they show has
// changed. Changes are announced with PostgreSQL NOTIFY inside the writing
// transaction, so every API instance hears about commits from every other one;
// each instance fans them out to its own subscribers through a Hub.
package realtime

import (
	"errors"
	"sync"
)

// ErrTooManySubscribers is returned once an instance holds MaxSubscribers streams.
var ErrTooManySubscribers = errors.New("too many live connections")

// Hub fans change signals out to the subscribers of a topic. A signal carries
// no data: subscribers reload what they show. Signals to a slow subscriber
// merge into one, so publishing never blocks.
type Hub struct {
	maxSubscribers int

	mu     sync.Mutex
	topics map[string]map[chan struct{}]struct{}
	count  int
	closed bool
	done   chan struct{}
}

// NewHub returns a Hub holding at most maxSubscribers subscriptions (0 means no limit).
func NewHub(maxSubscribers int) *Hub {
	return &Hub{
		maxSubscribers: maxSubscribers,
		topics:         make(map[string]map[chan struct{}]struct{}),
		done:           make(chan struct{}),
	}
}

// Subscribe returns a channel that receives a value after each change to topic,
// and a function that ends the subscription.
func (h *Hub) Subscribe(topic string) (<-chan struct{}, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, nil, errors.New("realtime hub is closed")
	}
	if h.maxSubscribers > 0 && h.count >= h.maxSubscribers {
		return nil, nil, ErrTooManySubscribers
	}
	ch := make(chan struct{}, 1)
	subs := h.topics[topic]
	if subs == nil {
		subs = make(map[chan struct{}]struct{})
		h.topics[topic] = subs
	}
	subs[ch] = struct{}{}
	h.count++

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			delete(subs, ch)
			if len(subs) == 0 {
				delete(h.topics, topic)
			}
			h.count--
		})
	}, nil
}

// Publish signals every subscriber of topic.
func (h *Hub) Publish(topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.topics[topic] {
		signal(ch)
	}
}

// PublishAll signals every subscriber, for when changes may have been missed.
func (h *Hub) PublishAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, subs := range h.topics {
		for ch := range subs {
			signal(ch)
		}
	}
}

// Done is closed when the hub closes; open streams should end then.
func (h *Hub) Done() <-chan struct{} { return h.done }

// Close ends every stream and refuses new subscriptions.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.closed {
		h.closed = true
		close(h.done)
	}
}

// Subscribers reports how many subscriptions are open.
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default: // a signal is already pending; the subscriber reloads once for both
	}
}
