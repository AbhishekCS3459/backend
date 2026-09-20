package truecaller

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

type Store interface {
	Put(ctx context.Context, session *Session) error
	Get(ctx context.Context, requestID string) (*Session, error)
	Delete(ctx context.Context, requestID string) error
}

type memoryStore struct {
	mu   sync.RWMutex
	data map[string]*Session
}

func NewMemoryStore() Store {
	return &memoryStore{data: make(map[string]*Session)}
}

func (s *memoryStore) Put(_ context.Context, session *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *session
	s.data[session.RequestID] = &cp
	return nil
}

func (s *memoryStore) Get(_ context.Context, requestID string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.data[requestID]
	if !ok {
		return nil, nil
	}
	if time.Now().After(session.ExpiresAt) {
		return &Session{RequestID: requestID, Status: StatusExpired, ExpiresAt: session.ExpiresAt}, nil
	}
	cp := *session
	return &cp, nil
}

func (s *memoryStore) Delete(_ context.Context, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, requestID)
	return nil
}

type redisStore struct {
	client *redis.Client
}

func NewRedisStore(redisURL string) (Store, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &redisStore{client: client}, nil
}

func sessionKey(requestID string) string {
	return "truecaller:session:" + requestID
}

func (s *redisStore) Put(ctx context.Context, session *Session) error {
	payload, err := json.Marshal(session)
	if err != nil {
		return err
	}
	ttl := time.Until(session.ExpiresAt)
	if ttl <= 0 {
		ttl = SessionTTL
	}
	return s.client.Set(ctx, sessionKey(session.RequestID), payload, ttl).Err()
}

func (s *redisStore) Get(ctx context.Context, requestID string) (*Session, error) {
	payload, err := s.client.Get(ctx, sessionKey(requestID)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	session := &Session{}
	if err := json.Unmarshal(payload, session); err != nil {
		return nil, err
	}
	if time.Now().After(session.ExpiresAt) {
		session.Status = StatusExpired
	}
	return session, nil
}

func (s *redisStore) Delete(ctx context.Context, requestID string) error {
	return s.client.Del(ctx, sessionKey(requestID)).Err()
}

func NewStore(redisURL string) Store {
	if redisURL == "" {
		log.Info().Str("step", "store").Msg("truecaller: using in-memory session store")
		return NewMemoryStore()
	}
	store, err := NewRedisStore(redisURL)
	if err != nil {
		log.Warn().Err(err).Str("step", "store").Msg("truecaller: redis unavailable, falling back to memory")
		return NewMemoryStore()
	}
	log.Info().Str("step", "store").Msg("truecaller: using redis session store")
	return store
}
