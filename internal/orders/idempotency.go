package orders

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,100}$`)

type idempotencyScope struct {
	customerID uuid.UUID
	key        string
	hash       string
}

func newScope(customerID uuid.UUID, key string, req any) (idempotencyScope, error) {
	if !keyPattern.MatchString(key) {
		return idempotencyScope{}, &ValidationError{
			Message: "Idempotency-Key header is required (8-100 letters, digits, '-', '_', '.', ':')",
		}
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return idempotencyScope{}, fmt.Errorf("hash request: %w", err)
	}
	sum := sha256.Sum256(raw)
	return idempotencyScope{customerID: customerID, key: key, hash: hex.EncodeToString(sum[:])}, nil
}

func replay[T any](db *gorm.DB, scope idempotencyScope) (*T, bool, error) {
	var rows []struct {
		RequestHash string
		Response    []byte
	}
	err := db.Raw(`SELECT request_hash, response::text AS response FROM order_idempotency
		WHERE customer_id = ? AND idempotency_key = ?`, scope.customerID, scope.key).Scan(&rows).Error
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
// in that same transaction, so a retried request (including one racing this
// one) gets the first response and never places a second order. Failures are
// not stored: retrying after "out of stock" checks the stock again.
func runIdempotent[T any](
	ctx context.Context, db *gorm.DB, scope idempotencyScope, fn func(tx *gorm.DB) (*T, error),
) (*T, bool, error) {
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
		if err := tx.Exec(`INSERT INTO order_idempotency (customer_id, idempotency_key, request_hash, response)
			VALUES (?, ?, ?, ?::jsonb)`, scope.customerID, scope.key, scope.hash, string(raw)).Error; err != nil {
			return err
		}
		result = out
		return nil
	})
	if isUniqueViolation(err, "order_idempotency_pkey") {
		return replay[T](db.WithContext(ctx), scope)
	}
	if err != nil {
		return nil, false, err
	}
	return result, false, nil
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && (constraint == "" || pgErr.ConstraintName == constraint)
}
