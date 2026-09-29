package team

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

type memberRow struct {
	UserID             uuid.UUID
	FullName           string
	Phone              string
	Email              string
	MustChangePassword bool
	StoreID            uuid.UUID
	StoreName          string
	Role               string
	Permissions        string
	IsActive           bool
	CreatedAt          time.Time
}

type account struct {
	ID       uuid.UUID
	UserType string
}

type membership struct {
	StoreID    uuid.UUID
	RetailerID uuid.UUID
	Role       string
}

type assignment struct {
	StoreID     uuid.UUID
	Role        storeaccess.Role
	Permissions []storeaccess.Permission
	Active      bool
}

type newAccount struct {
	ID           uuid.UUID
	FullName     string
	Phone        string
	Email        string
	PasswordHash string
}

type Repository interface {
	ListMembers(ctx context.Context, storeIDs []uuid.UUID) ([]memberRow, error)
	FindAccount(ctx context.Context, phones []string, email string) (*account, error)
	Memberships(ctx context.Context, userID uuid.UUID) ([]membership, error)
	CreateStaff(ctx context.Context, acct newAccount, rows []assignment, invitedBy uuid.UUID) error
	// SaveAssignments makes the member's rows inside scope exactly match rows.
	SaveAssignments(ctx context.Context, userID uuid.UUID, scope []uuid.UUID, rows []assignment, invitedBy uuid.UUID) error
	RemoveAssignments(ctx context.Context, userID uuid.UUID, scope []uuid.UUID) error
	UpdateName(ctx context.Context, userID uuid.UUID, fullName string) error
	SetTemporaryPassword(ctx context.Context, userID uuid.UUID, passwordHash string) error
}

type repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) Repository {
	return &repository{db: db}
}

func (r *repository) ListMembers(ctx context.Context, storeIDs []uuid.UUID) ([]memberRow, error) {
	var rows []memberRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT u.id AS user_id, COALESCE(u.full_name, '') AS full_name, u.phone, u.email, u.must_change_password,
		       s.id AS store_id, s.name AS store_name, sm.role,
		       array_to_string(sm.permissions, ',') AS permissions, sm.is_active, sm.created_at
		FROM staff_member sm
		JOIN users u ON u.id = sm.user_id
		JOIN store s ON s.id = sm.store_id
		WHERE sm.store_id IN ?
		ORDER BY sm.created_at, s.name`, storeIDs).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list team members: %w", err)
	}
	return rows, nil
}

func (r *repository) FindAccount(ctx context.Context, phones []string, email string) (*account, error) {
	var rows []account
	query := r.db.WithContext(ctx).Table("users").Select("id, user_type").Where("phone IN ?", phones)
	if email != "" {
		query = query.Or("LOWER(email) = ?", email)
	}
	if err := query.Limit(1).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("find account: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

func (r *repository) Memberships(ctx context.Context, userID uuid.UUID) ([]membership, error) {
	var rows []membership
	err := r.db.WithContext(ctx).Raw(`
		SELECT sm.store_id, s.retailer_id, sm.role
		FROM staff_member sm
		JOIN store s ON s.id = sm.store_id
		WHERE sm.user_id = ? AND s.deleted_at IS NULL`, userID).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	return rows, nil
}

func (r *repository) CreateStaff(ctx context.Context, acct newAccount, rows []assignment, invitedBy uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Exec(`
			INSERT INTO users (id, phone, is_phone_verified, email, password_hash, user_type, status,
			                   full_name, must_change_password, created_at, updated_at)
			VALUES (?, ?, FALSE, ?, ?, 'STAFF', 'ACTIVE', ?, TRUE, NOW(), NOW())`,
			acct.ID, acct.Phone, acct.Email, acct.PasswordHash, acct.FullName).Error
		if isUniqueViolation(err) {
			return ErrAccountInUse
		}
		if err != nil {
			return fmt.Errorf("create staff account: %w", err)
		}
		return upsertAssignments(tx, acct.ID, rows, invitedBy)
	})
}

func (r *repository) SaveAssignments(
	ctx context.Context, userID uuid.UUID, scope []uuid.UUID, rows []assignment, invitedBy uuid.UUID,
) error {
	keep := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		keep = append(keep, row.StoreID)
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Exec(`DELETE FROM staff_member WHERE user_id = ? AND store_id IN ? AND store_id NOT IN ?`,
			userID, scope, keep).Error
		if err != nil {
			return fmt.Errorf("remove store assignments: %w", err)
		}
		return upsertAssignments(tx, userID, rows, invitedBy)
	})
}

func upsertAssignments(tx *gorm.DB, userID uuid.UUID, rows []assignment, invitedBy uuid.UUID) error {
	for _, row := range rows {
		perms := make([]string, len(row.Permissions))
		for i, p := range row.Permissions {
			perms[i] = string(p)
		}
		err := tx.Exec(`
			INSERT INTO staff_member (user_id, store_id, role, permissions, invited_by, is_active, created_at, updated_at)
			VALUES (?, ?, ?, string_to_array(?, ','), ?, ?, NOW(), NOW())
			ON CONFLICT (user_id, store_id) DO UPDATE
			SET role = EXCLUDED.role, permissions = EXCLUDED.permissions,
			    is_active = EXCLUDED.is_active, updated_at = NOW()`,
			userID, row.StoreID, string(row.Role), strings.Join(perms, ","), invitedBy, row.Active).Error
		if err != nil {
			return fmt.Errorf("save store assignment: %w", err)
		}
	}
	return nil
}

func (r *repository) RemoveAssignments(ctx context.Context, userID uuid.UUID, scope []uuid.UUID) error {
	err := r.db.WithContext(ctx).Exec(`DELETE FROM staff_member WHERE user_id = ? AND store_id IN ?`, userID, scope).Error
	if err != nil {
		return fmt.Errorf("remove team member: %w", err)
	}
	return nil
}

func (r *repository) UpdateName(ctx context.Context, userID uuid.UUID, fullName string) error {
	err := r.db.WithContext(ctx).
		Exec(`UPDATE users SET full_name = ?, updated_at = NOW() WHERE id = ?`, fullName, userID).Error
	if err != nil {
		return fmt.Errorf("update member name: %w", err)
	}
	return nil
}

func (r *repository) SetTemporaryPassword(ctx context.Context, userID uuid.UUID, passwordHash string) error {
	err := r.db.WithContext(ctx).Exec(`
		UPDATE users
		SET password_hash = ?, must_change_password = TRUE,
		    password_reset_token = NULL, password_reset_expires_at = NULL, updated_at = NOW()
		WHERE id = ?`, passwordHash, userID).Error
	if err != nil {
		return fmt.Errorf("set temporary password: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
