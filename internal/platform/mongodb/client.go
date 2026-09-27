// Package mongodb provides the MongoDB client used for the product catalogue.
package mongodb

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

const (
	appName                = "find-me-backend"
	serverSelectionTimeout = 10 * time.Second
	pingTimeout            = 10 * time.Second
)

// Client wraps a connected *mongo.Client.
type Client struct {
	*mongo.Client
	target string
}

// Connect creates a client for uri and verifies it with a ping against the primary.
func Connect(ctx context.Context, uri string) (*Client, error) {
	opts := options.Client().
		ApplyURI(uri).
		SetAppName(appName).
		SetServerAPIOptions(options.ServerAPI(options.ServerAPIVersion1)).
		SetServerSelectionTimeout(serverSelectionTimeout)

	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("create mongodb client: %w", err)
	}

	target := redactedTarget(uri)
	start := time.Now()
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := client.Ping(pingCtx, readpref.Primary()); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("ping mongodb %s: %w", target, err)
	}

	log.Info().
		Str("target", target).
		Dur("ping", time.Since(start)).
		Msg("mongodb connected successfully")

	return &Client{Client: client, target: target}, nil
}

// Health pings the primary.
func (c *Client) Health(ctx context.Context) error {
	return c.Ping(ctx, readpref.Primary())
}

// Close disconnects the client.
func (c *Client) Close(ctx context.Context) error {
	if err := c.Disconnect(ctx); err != nil {
		return fmt.Errorf("disconnect mongodb: %w", err)
	}
	log.Info().Str("target", c.target).Msg("mongodb connection closed")
	return nil
}

func redactedTarget(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Host == "" {
		return "unknown"
	}
	return u.Scheme + "://" + u.Host + u.Path
}
