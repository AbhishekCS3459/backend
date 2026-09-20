package truecaller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/identity"
	"github.com/rs/zerolog/log"
)

type Service interface {
	Start(ctx context.Context, requestID string) error
	HandleCallback(ctx context.Context, req *CallbackRequest) error
	Status(ctx context.Context, requestID string) (*StatusResponse, error)
}

type service struct {
	store      Store
	users      identity.Service
	httpClient *http.Client
}

func NewService(store Store, users identity.Service) Service {
	return &service{
		store: store,
		users: users,
		httpClient: &http.Client{
			Timeout: 8 * time.Second,
		},
	}
}

func (s *service) Start(ctx context.Context, requestID string) error {
	if requestID == "" {
		return fmt.Errorf("requestId is required")
	}
	expires := time.Now().Add(SessionTTL)
	log.Info().
		Str("step", "start").
		Str("request_id", requestID).
		Time("expires_at", expires).
		Msg("truecaller: registered pending nonce")
	return s.store.Put(ctx, &Session{
		RequestID: requestID,
		Status:    StatusPending,
		ExpiresAt: expires,
	})
}

func (s *service) HandleCallback(ctx context.Context, req *CallbackRequest) error {
	if req == nil || req.RequestID == "" || req.AccessToken == "" || req.Endpoint == "" {
		log.Error().
			Str("step", "callback").
			Bool("has_request_id", req != nil && req.RequestID != "").
			Bool("has_access_token", req != nil && req.AccessToken != "").
			Bool("has_endpoint", req != nil && req.Endpoint != "").
			Msg("truecaller: callback payload missing fields")
		return fmt.Errorf("invalid callback payload")
	}
	if err := allowedProfileEndpoint(req.Endpoint); err != nil {
		log.Error().
			Err(err).
			Str("step", "callback").
			Str("request_id", req.RequestID).
			Str("endpoint", req.Endpoint).
			Msg("truecaller: rejected profile endpoint")
		return err
	}

	existing, err := s.store.Get(ctx, req.RequestID)
	if err != nil {
		log.Error().Err(err).Str("step", "callback").Str("request_id", req.RequestID).Msg("truecaller: store read failed")
		return err
	}
	if existing == nil || time.Now().After(existing.ExpiresAt) {
		log.Error().
			Str("step", "callback").
			Str("request_id", req.RequestID).
			Bool("session_found", existing != nil).
			Msg("truecaller: callback for unknown or expired requestId — frontend POST /start may have been skipped or used a different API instance")
		return fmt.Errorf("expired or unknown requestId")
	}

	log.Info().
		Str("step", "callback").
		Str("request_id", req.RequestID).
		Str("endpoint", req.Endpoint).
		Int("access_token_len", len(req.AccessToken)).
		Msg("truecaller: callback accepted, fetching profile")
	go s.fetchAndComplete(req)
	return nil
}

func (s *service) fetchAndComplete(req *CallbackRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.Endpoint, nil)
	if err != nil {
		log.Error().Err(err).Str("step", "profile").Str("request_id", req.RequestID).Msg("truecaller: failed to build profile request")
		s.fail(ctx, req.RequestID, "failed to build profile request")
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+req.AccessToken)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		log.Error().Err(err).Str("step", "profile").Str("request_id", req.RequestID).Str("endpoint", req.Endpoint).Msg("truecaller: profile request failed")
		s.fail(ctx, req.RequestID, "failed to fetch Truecaller profile")
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		log.Error().Err(err).Str("step", "profile").Str("request_id", req.RequestID).Msg("truecaller: failed to read profile body")
		s.fail(ctx, req.RequestID, "failed to read Truecaller profile")
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Error().
			Int("status", resp.StatusCode).
			Str("step", "profile").
			Str("request_id", req.RequestID).
			Int("body_len", len(body)).
			Msg("truecaller: profile HTTP error")
		s.fail(ctx, req.RequestID, "Truecaller profile request was rejected")
		return
	}

	profile, err := parseProfile(body)
	if err != nil {
		log.Error().
			Err(err).
			Str("step", "parse_profile").
			Str("request_id", req.RequestID).
			Int("body_len", len(body)).
			Msg("truecaller: could not parse profile JSON")
		s.fail(ctx, req.RequestID, err.Error())
		return
	}

	log.Info().
		Str("step", "parse_profile").
		Str("request_id", req.RequestID).
		Str("phone", profile.Phone).
		Str("email", profile.Email).
		Msg("truecaller: profile parsed")

	login, err := s.users.LoginWithVerifiedPhone(ctx, profile.Phone, profile.Email, identity.UserTypeRetailer)
	if err != nil {
		log.Error().Err(err).Str("step", "login").Str("request_id", req.RequestID).Str("phone", profile.Phone).Msg("truecaller: failed to create session")
		s.fail(ctx, req.RequestID, "failed to create user session")
		return
	}

	if err := s.store.Put(ctx, &Session{
		RequestID: req.RequestID,
		Status:    StatusCompleted,
		Profile:   profile,
		Token:     login.Token,
		User:      login.User.ToResponse(),
		ExpiresAt: time.Now().Add(SessionTTL),
	}); err != nil {
		log.Error().Err(err).Str("step", "complete").Str("request_id", req.RequestID).Msg("truecaller: failed to store completed session")
		s.fail(ctx, req.RequestID, "failed to store completed session")
		return
	}

	log.Info().
		Str("step", "complete").
		Str("request_id", req.RequestID).
		Str("user_id", login.User.ID.String()).
		Msg("truecaller: login completed")
}

func (s *service) fail(ctx context.Context, requestID, message string) {
	log.Error().Str("step", "fail").Str("request_id", requestID).Str("error", message).Msg("truecaller: marked session expired")
	existing, err := s.store.Get(ctx, requestID)
	expires := time.Now().Add(SessionTTL)
	if err == nil && existing != nil {
		expires = existing.ExpiresAt
	}
	_ = s.store.Put(ctx, &Session{
		RequestID: requestID,
		Status:    StatusExpired,
		Error:     message,
		ExpiresAt: expires,
	})
}

func (s *service) Status(ctx context.Context, requestID string) (*StatusResponse, error) {
	if requestID == "" {
		return nil, fmt.Errorf("requestId is required")
	}

	session, err := s.store.Get(ctx, requestID)
	if err != nil {
		log.Error().Err(err).Str("step", "status").Str("request_id", requestID).Msg("truecaller: store read failed")
		return nil, err
	}
	if session == nil {
		log.Warn().Str("step", "status").Str("request_id", requestID).Msg("truecaller: no session for requestId")
		return &StatusResponse{Status: StatusExpired}, nil
	}
	if time.Now().After(session.ExpiresAt) {
		log.Info().Str("step", "status").Str("request_id", requestID).Msg("truecaller: session TTL expired")
		_ = s.store.Delete(ctx, requestID)
		return &StatusResponse{Status: StatusExpired}, nil
	}

	if session.Status == StatusCompleted {
		log.Info().Str("step", "status").Str("request_id", requestID).Msg("truecaller: returning completed session")
		_ = s.store.Delete(ctx, requestID)
		return &StatusResponse{
			Status:  StatusCompleted,
			Token:   session.Token,
			User:    session.User,
			Profile: session.Profile,
		}, nil
	}

	log.Info().
		Str("step", "status").
		Str("request_id", requestID).
		Str("status", session.Status).
		Str("error", session.Error).
		Msg("truecaller: still waiting for Truecaller callback at POST /api/truecaller/callback")

	return &StatusResponse{
		Status: session.Status,
		Error:  session.Error,
	}, nil
}
