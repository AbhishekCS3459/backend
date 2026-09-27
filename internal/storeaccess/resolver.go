package storeaccess

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	// ErrNotFound hides stores the caller has no relationship with.
	ErrNotFound  = errors.New("store not found")
	ErrForbidden = errors.New("you don't have permission to do this in this store")
)

type Access struct {
	StoreID     uuid.UUID    `json:"-"`
	RetailerID  uuid.UUID    `json:"-"`
	Role        Role         `json:"role"`
	Permissions []Permission `json:"permissions"`
}

func (a *Access) IsOwner() bool { return a.Role == RoleOwner }

func (a *Access) Can(permission Permission) bool {
	return a.IsOwner() || slices.Contains(a.Permissions, permission)
}

type Resolver interface {
	// Store returns the caller's access to one store, or ErrNotFound.
	Store(ctx context.Context, userID, storeID uuid.UUID) (*Access, error)
	// Stores returns every store the caller owns or actively staffs.
	Stores(ctx context.Context, userID uuid.UUID) ([]Access, error)
	// Require is Store plus a permission check (ErrForbidden when missing).
	Require(ctx context.Context, userID, storeID uuid.UUID, permission Permission) (*Access, error)
}

type resolver struct {
	db *gorm.DB
}

func NewResolver(db *gorm.DB) Resolver {
	return &resolver{db: db}
}

type accessRow struct {
	StoreID     uuid.UUID
	RetailerID  uuid.UUID
	Role        string
	Permissions string
}

func (row accessRow) toAccess() Access {
	access := Access{StoreID: row.StoreID, RetailerID: row.RetailerID, Role: Role(row.Role)}
	if access.Role == RoleOwner {
		access.Permissions = allPermissions()
		return access
	}
	access.Permissions = []Permission{}
	for _, key := range strings.Split(row.Permissions, ",") {
		if _, ok := lookup(Permission(key)); ok {
			access.Permissions = append(access.Permissions, Permission(key))
		}
	}
	return access
}

const accessSelect = `
	SELECT s.id AS store_id, s.retailer_id, 'OWNER' AS role, '' AS permissions, s.created_at
	FROM store s
	JOIN retailers r ON r.id = s.retailer_id
	WHERE r.user_id = @user %s
	UNION ALL
	SELECT s.id, s.retailer_id, sm.role, array_to_string(sm.permissions, ','), s.created_at
	FROM staff_member sm
	JOIN store s ON s.id = sm.store_id
	JOIN retailers r ON r.id = s.retailer_id
	WHERE sm.user_id = @user AND sm.is_active AND r.user_id <> @user %s
	ORDER BY created_at DESC`

func (r *resolver) Store(ctx context.Context, userID, storeID uuid.UUID) (*Access, error) {
	var rows []accessRow
	query := fmt.Sprintf(accessSelect, "AND s.id = @store", "AND s.id = @store")
	args := map[string]any{"user": userID, "store": storeID}
	if err := r.db.WithContext(ctx).Raw(query, args).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("resolve store access: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	access := rows[0].toAccess()
	return &access, nil
}

func (r *resolver) Stores(ctx context.Context, userID uuid.UUID) ([]Access, error) {
	var rows []accessRow
	query := fmt.Sprintf(accessSelect, "", "")
	if err := r.db.WithContext(ctx).Raw(query, map[string]any{"user": userID}).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("resolve stores access: %w", err)
	}
	out := make([]Access, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toAccess())
	}
	return out, nil
}

func (r *resolver) Require(ctx context.Context, userID, storeID uuid.UUID, permission Permission) (*Access, error) {
	access, err := r.Store(ctx, userID, storeID)
	if err != nil {
		return nil, err
	}
	if !access.Can(permission) {
		return nil, ErrForbidden
	}
	return access, nil
}
