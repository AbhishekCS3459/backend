package inventory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	defaultHistoryLimit = 20
	maxHistoryLimit     = 100
)

type Service interface {
	Get(ctx context.Context, userID, storeID, variantID uuid.UUID) (*Item, error)
	Receive(ctx context.Context, userID, storeID, variantID uuid.UUID, key string, req *ReceiveRequest) (*Result, bool, error)
	ReceiveBatch(ctx context.Context, userID, storeID uuid.UUID, key string, req *ReceiveBatchRequest) (*BatchResult, bool, error)
	Adjust(ctx context.Context, userID, storeID, variantID uuid.UUID, key string, req *AdjustRequest) (*Result, bool, error)
	History(ctx context.Context, userID, storeID, variantID uuid.UUID, cursor string, limit int) (*HistoryPage, error)
}

type service struct {
	db     *gorm.DB
	access storeaccess.Resolver
	ledger *Ledger
}

func NewService(db *gorm.DB, access storeaccess.Resolver, ledger *Ledger) Service {
	return &service{db: db, access: access, ledger: ledger}
}

func (s *service) Get(ctx context.Context, userID, storeID, variantID uuid.UUID) (*Item, error) {
	if _, err := s.access.Require(ctx, userID, storeID, storeaccess.InventoryView); err != nil {
		return nil, err
	}
	row, err := findRow(s.db.WithContext(ctx), storeID, variantID, false)
	if err != nil {
		return nil, err
	}
	item := row.item()
	return &item, nil
}

func (s *service) Receive(
	ctx context.Context, userID, storeID, variantID uuid.UUID, key string, req *ReceiveRequest,
) (*Result, bool, error) {
	scope, err := s.authorizeWrite(ctx, userID, storeID, key, "receive", variantID, req)
	if err != nil {
		return nil, false, err
	}
	return runIdempotent(ctx, s.db, scope, func(tx *gorm.DB) (*Result, error) {
		actor, err := ResolveActor(tx, userID)
		if err != nil {
			return nil, err
		}
		res, err := s.ledger.Receive(tx, ReceiveInput{
			StoreID: storeID, VariantID: variantID, Quantity: req.Quantity,
			Reference: req.Reference, Note: req.Note, Actor: actor,
		})
		if err != nil {
			return nil, err
		}
		return &res, nil
	})
}

// ReceiveBatch records a delivery of several products: every line is applied
// or none is.
func (s *service) ReceiveBatch(
	ctx context.Context, userID, storeID uuid.UUID, key string, req *ReceiveBatchRequest,
) (*BatchResult, bool, error) {
	seen := make(map[uuid.UUID]bool, len(req.Items))
	for _, item := range req.Items {
		if seen[item.VariantID] {
			return nil, false, &ValidationError{Message: "each product can appear only once in a delivery"}
		}
		seen[item.VariantID] = true
	}
	scope, err := s.authorizeWrite(ctx, userID, storeID, key, "receive_batch", req)
	if err != nil {
		return nil, false, err
	}
	return runIdempotent(ctx, s.db, scope, func(tx *gorm.DB) (*BatchResult, error) {
		actor, err := ResolveActor(tx, userID)
		if err != nil {
			return nil, err
		}
		batchID := uuid.New()
		// Lock rows in a fixed order so two deliveries touching the same
		// products cannot deadlock.
		order := make([]int, len(req.Items))
		for i := range order {
			order[i] = i
		}
		sort.Slice(order, func(a, b int) bool {
			return bytes.Compare(req.Items[order[a]].VariantID[:], req.Items[order[b]].VariantID[:]) < 0
		})
		results := make([]Result, len(req.Items))
		for _, i := range order {
			line := req.Items[i]
			res, err := s.ledger.Receive(tx, ReceiveInput{
				StoreID: storeID, VariantID: line.VariantID, Quantity: line.Quantity,
				Reference: req.Reference, Note: req.Note, BatchID: &batchID, Actor: actor,
			})
			if err != nil {
				return nil, lineError(i, err)
			}
			results[i] = res
		}
		return &BatchResult{BatchID: batchID, Reference: req.Reference, Items: results}, nil
	})
}

func (s *service) Adjust(
	ctx context.Context, userID, storeID, variantID uuid.UUID, key string, req *AdjustRequest,
) (*Result, bool, error) {
	scope, err := s.authorizeWrite(ctx, userID, storeID, key, "adjust", variantID, req)
	if err != nil {
		return nil, false, err
	}
	return runIdempotent(ctx, s.db, scope, func(tx *gorm.DB) (*Result, error) {
		actor, err := ResolveActor(tx, userID)
		if err != nil {
			return nil, err
		}
		res, err := s.ledger.Adjust(tx, storeID, variantID, *req, actor)
		if err != nil {
			return nil, err
		}
		return &res, nil
	})
}

type historyRow struct {
	ID              uuid.UUID
	Seq             int64
	Type            TxType
	Reason          *Reason
	Quantity        int
	BeforeOnHand    int
	AfterOnHand     int
	BeforeReserved  int
	AfterReserved   int
	CountedQuantity *int
	Reference       *string
	Note            *string
	BatchID         *uuid.UUID
	CreatedBy       *uuid.UUID
	CreatedByName   string
	CreatedAt       time.Time
}

func (s *service) History(
	ctx context.Context, userID, storeID, variantID uuid.UUID, cursor string, limit int,
) (*HistoryPage, error) {
	if _, err := s.access.Require(ctx, userID, storeID, storeaccess.InventoryView); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultHistoryLimit
	}
	limit = min(limit, maxHistoryLimit)
	db := s.db.WithContext(ctx)
	row, err := findRow(db, storeID, variantID, false)
	if err != nil {
		return nil, err
	}

	sql := `
		SELECT t.id, t.seq, t.type, t.reason, t.quantity, t.before_on_hand, t.after_on_hand,
			t.before_reserved, t.after_reserved, t.counted_quantity, t.reference, t.note, t.batch_id,
			t.created_by, COALESCE(NULLIF(u.full_name, ''), u.phone, '') AS created_by_name, t.created_at
		FROM inventory_transaction t
		LEFT JOIN users u ON u.id = t.created_by
		WHERE t.inventory_id = ?`
	args := []any{row.ID}
	if cursor != "" {
		seq, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || seq <= 0 {
			return nil, &ValidationError{Message: "invalid cursor"}
		}
		sql += ` AND t.seq < ?`
		args = append(args, seq)
	}
	sql += ` ORDER BY t.seq DESC LIMIT ?`
	args = append(args, limit+1)

	var rows []historyRow
	if err := db.Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load inventory history: %w", err)
	}
	page := &HistoryPage{Transactions: make([]Transaction, 0, min(len(rows), limit))}
	for i, r := range rows {
		if i == limit {
			page.NextCursor = strconv.FormatInt(rows[limit-1].Seq, 10)
			break
		}
		entry := Transaction{
			ID: r.ID, Type: r.Type, Reason: r.Reason, Quantity: r.Quantity,
			BeforeOnHand: r.BeforeOnHand, AfterOnHand: r.AfterOnHand,
			BeforeReserved: r.BeforeReserved, AfterReserved: r.AfterReserved,
			CountedQuantity: r.CountedQuantity, Reference: r.Reference, Note: r.Note,
			BatchID: r.BatchID, CreatedAt: r.CreatedAt,
		}
		if r.CreatedBy != nil {
			entry.CreatedBy = &Actor{ID: *r.CreatedBy, Name: r.CreatedByName}
		}
		page.Transactions = append(page.Transactions, entry)
	}
	return page, nil
}

func (s *service) authorizeWrite(
	ctx context.Context, userID, storeID uuid.UUID, key, operation string, parts ...any,
) (idempotencyScope, error) {
	if _, err := s.access.Require(ctx, userID, storeID, storeaccess.InventoryUpdate); err != nil {
		return idempotencyScope{}, err
	}
	if err := ValidateKey(key); err != nil {
		return idempotencyScope{}, err
	}
	hash, err := requestHash(operation, parts...)
	if err != nil {
		return idempotencyScope{}, err
	}
	return idempotencyScope{storeID: storeID, userID: userID, key: key, hash: hash}, nil
}

// lineError names the delivery line that failed, keeping the error's type so
// it still maps to the right HTTP status.
func lineError(index int, err error) error {
	prefix := fmt.Sprintf("Item %d: ", index+1)
	var ve *ValidationError
	var se *StockError
	switch {
	case errors.As(err, &ve):
		return &ValidationError{Message: prefix + ve.Message}
	case errors.As(err, &se):
		return &StockError{Message: prefix + se.Message}
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrUnlisted):
		return &LineError{Index: index, Err: err}
	default:
		return err
	}
}

// LineError wraps a sentinel error with the delivery line it came from.
type LineError struct {
	Index int
	Err   error
}

func (e *LineError) Error() string { return fmt.Sprintf("Item %d: %s", e.Index+1, e.Err.Error()) }
func (e *LineError) Unwrap() error { return e.Err }
