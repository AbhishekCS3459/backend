package inventory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,100}$`)

// ValidateKey checks an Idempotency-Key header value.
func ValidateKey(key string) error {
	if !keyPattern.MatchString(key) {
		return &ValidationError{
			Message: "Idempotency-Key header is required (8-100 letters, digits, '-', '_', '.', ':')",
		}
	}
	return nil
}

type idempotencyScope struct {
	storeID uuid.UUID
	userID  uuid.UUID
	key     string
	hash    string
}

func requestHash(operation string, parts ...any) (string, error) {
	raw, err := json.Marshal(append([]any{operation}, parts...))
	if err != nil {
		return "", fmt.Errorf("hash request: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

type storedResponse struct {
	RequestHash string
	Response    []byte
}

// replay returns the stored response for the key, if any.
func replay[T any](db *gorm.DB, scope idempotencyScope) (*T, bool, error) {
	var rows []storedResponse
	err := db.Raw(`SELECT request_hash, response::text AS response FROM inventory_idempotency
		WHERE store_id = ? AND idempotency_key = ?`, scope.storeID, scope.key).Scan(&rows).Error
	if err != nil {
		return nil, false, fmt.Errorf("load idempotency key: %w", err)
	}
	if len(rows) == 0 {
		return nil, false, nil
	}
	if rows[0].RequestHash != scope.hash {
		return nil, false, ErrKeyReused
	}
	var out T
	if err := json.Unmarshal(rows[0].Response, &out); err != nil {
		return nil, false, fmt.Errorf("decode stored response: %w", err)
	}
	return &out, true, nil
}

// runIdempotent runs fn in a transaction and stores its response under the key
// in that same transaction. A request that was already processed, including
// one that raced this one and committed first, gets the original response and
// no second stock change. The bool reports whether the response was replayed.
func runIdempotent[T any](ctx context.Context, db *gorm.DB, scope idempotencyScope, fn func(tx *gorm.DB) (*T, error)) (*T, bool, error) {
	if out, ok, err := replay[T](db.WithContext(ctx), scope); err != nil || ok {
		return out, ok, err
	}
	var result *T
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		out, err := fn(tx)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(out)
		if err != nil {
			return fmt.Errorf("encode response: %w", err)
		}
		err = tx.Exec(`INSERT INTO inventory_idempotency (store_id, idempotency_key, request_hash, response, created_by)
			VALUES (?, ?, ?, ?::jsonb, ?)`, scope.storeID, scope.key, scope.hash, string(raw), scope.userID).Error
		if err != nil {
			return err
		}
		result = out
		return nil
	})
	if isUniqueViolation(err, "inventory_idempotency_pkey") {
		return replay[T](db.WithContext(ctx), scope)
	}
	if err != nil {
		var ve *ValidationError
		var se *StockError
		if errors.As(err, &ve) || errors.As(err, &se) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnlisted) {
			return nil, false, err
		}
		return nil, false, fmt.Errorf("inventory change: %w", err)
	}
	return result, false, nil
}
