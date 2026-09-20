package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

const (
	ErrUserNotFoundMsg = "user not found"
	ErrUserExistsMsg   = "user with this email or phone already exists"
)

var (
	ErrUserNotFound      = errors.New(ErrUserNotFoundMsg)
	ErrUserAlreadyExists = errors.New(ErrUserExistsMsg)
)

const userSelectColumns = `
	id, phone, is_phone_verified, email, password_hash, user_type, status,
	full_name, avatar_url, city, bio,
	password_reset_token, password_reset_expires_at, created_at, updated_at
`

// Repository is the identity persistence API.
type Repository interface {
	Create(ctx context.Context, user *User) error
	FindByEmail(ctx context.Context, email string) (*User, error)
	FindByPhone(ctx context.Context, phone string) (*User, error)
	FindByID(ctx context.Context, id uuid.UUID) (*User, error)
	MarkPhoneVerified(ctx context.Context, userID uuid.UUID) error
	List(ctx context.Context, limit, offset int) ([]*User, error)
	Update(ctx context.Context, user *User) error
	UpdatePassword(ctx context.Context, userID uuid.UUID, hashedPassword string) error
	SetPasswordResetToken(ctx context.Context, email, token string, expiresAt time.Time) error
	FindByPasswordResetToken(ctx context.Context, token string) (*User, error)
	UpdateRole(ctx context.Context, userID uuid.UUID, role UserRole) error
	HasRole(ctx context.Context, role UserRole) (bool, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) Repository {
	return &repository{db: db}
}

func scanUser(scanner interface {
	Scan(dest ...any) error
}, user *User) error {
	return scanner.Scan(
		&user.ID,
		&user.Phone,
		&user.IsPhoneVerified,
		&user.Email,
		&user.Password,
		&user.UserType,
		&user.Status,
		&user.FullName,
		&user.AvatarURL,
		&user.City,
		&user.Bio,
		&user.PasswordResetToken,
		&user.PasswordResetExpiresAt,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
}

func (r *repository) Create(ctx context.Context, user *User) error {
	query := `
		INSERT INTO users (id, phone, is_phone_verified, email, password_hash, user_type, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING ` + userSelectColumns

	now := time.Now()
	userType := string(user.UserType)
	if userType == "" {
		userType = string(UserTypeUser)
	}
	status := string(user.Status)
	if status == "" {
		status = string(UserStatusActive)
	}

	err := scanUser(r.db.QueryRow(
		ctx,
		query,
		user.ID,
		user.Phone,
		user.IsPhoneVerified,
		user.Email,
		user.Password,
		userType,
		status,
		now,
		now,
	), user)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrUserAlreadyExists
		}
		log.Error().Err(err).Str("email", user.Email).Msg("failed to create user")
		return fmt.Errorf("failed to create user: %w", err)
	}

	return nil
}

func (r *repository) FindByEmail(ctx context.Context, email string) (*User, error) {
	query := `SELECT ` + userSelectColumns + ` FROM users WHERE email = $1`
	user := &User{}
	err := scanUser(r.db.QueryRow(ctx, query, email), user)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrUserNotFound
		}
		log.Error().Err(err).Str("email", email).Msg("failed to find user by email")
		return nil, fmt.Errorf("failed to find user: %w", err)
	}
	return user, nil
}

func (r *repository) FindByPhone(ctx context.Context, phone string) (*User, error) {
	query := `SELECT ` + userSelectColumns + ` FROM users WHERE phone = $1`
	user := &User{}
	err := scanUser(r.db.QueryRow(ctx, query, phone), user)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrUserNotFound
		}
		log.Error().Err(err).Str("phone", phone).Msg("failed to find user by phone")
		return nil, fmt.Errorf("failed to find user: %w", err)
	}
	return user, nil
}

func (r *repository) MarkPhoneVerified(ctx context.Context, userID uuid.UUID) error {
	query := `
		UPDATE users
		SET is_phone_verified = TRUE, updated_at = $2
		WHERE id = $1
		RETURNING updated_at
	`
	now := time.Now()
	err := r.db.QueryRow(ctx, query, userID, now).Scan(&now)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrUserNotFound
		}
		return fmt.Errorf("failed to mark phone verified: %w", err)
	}
	return nil
}

func (r *repository) FindByID(ctx context.Context, id uuid.UUID) (*User, error) {
	query := `SELECT ` + userSelectColumns + ` FROM users WHERE id = $1`
	user := &User{}
	err := scanUser(r.db.QueryRow(ctx, query, id), user)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrUserNotFound
		}
		log.Error().Err(err).Str("id", id.String()).Msg("failed to find user by id")
		return nil, fmt.Errorf("failed to find user: %w", err)
	}
	return user, nil
}

func (r *repository) List(ctx context.Context, limit, offset int) ([]*User, error) {
	query := `SELECT ` + userSelectColumns + ` FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2`
	rows, err := r.db.Query(ctx, query, limit, offset)
	if err != nil {
		log.Error().Err(err).Msg("failed to list users")
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	defer rows.Close()

	users := []*User{}
	for rows.Next() {
		user := &User{}
		if err := scanUser(rows, user); err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating users: %w", err)
	}
	return users, nil
}

func (r *repository) Update(ctx context.Context, user *User) error {
	query := `
		UPDATE users
		SET email = $2, phone = $3, full_name = $4, avatar_url = $5, city = $6, bio = $7,
		    is_phone_verified = $8, updated_at = $9
		WHERE id = $1
		RETURNING updated_at
	`
	now := time.Now()
	err := r.db.QueryRow(
		ctx,
		query,
		user.ID,
		user.Email,
		user.Phone,
		user.FullName,
		user.AvatarURL,
		user.City,
		user.Bio,
		user.IsPhoneVerified,
		now,
	).Scan(&user.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrUserNotFound
		}
		log.Error().Err(err).Str("id", user.ID.String()).Msg("failed to update user")
		return fmt.Errorf("failed to update user: %w", err)
	}
	return nil
}

func (r *repository) UpdatePassword(ctx context.Context, userID uuid.UUID, hashedPassword string) error {
	query := `
		UPDATE users
		SET password_hash = $2, password_reset_token = NULL, password_reset_expires_at = NULL, updated_at = $3
		WHERE id = $1
		RETURNING updated_at
	`
	now := time.Now()
	var updatedAt time.Time
	err := r.db.QueryRow(ctx, query, userID, hashedPassword, now).Scan(&updatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrUserNotFound
		}
		log.Error().Err(err).Str("id", userID.String()).Msg("failed to update password")
		return fmt.Errorf("failed to update password: %w", err)
	}
	return nil
}

func (r *repository) SetPasswordResetToken(ctx context.Context, email, token string, expiresAt time.Time) error {
	query := `
		UPDATE users
		SET password_reset_token = $2, password_reset_expires_at = $3, updated_at = $4
		WHERE email = $1
	`
	now := time.Now()
	result, err := r.db.Exec(ctx, query, email, token, expiresAt, now)
	if err != nil {
		log.Error().Err(err).Str("email", email).Msg("failed to set password reset token")
		return fmt.Errorf("failed to set password reset token: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (r *repository) FindByPasswordResetToken(ctx context.Context, token string) (*User, error) {
	query := `SELECT ` + userSelectColumns + `
		FROM users
		WHERE password_reset_token = $1 AND password_reset_expires_at > NOW()`
	user := &User{}
	err := scanUser(r.db.QueryRow(ctx, query, token), user)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, errors.New("invalid or expired reset token")
		}
		log.Error().Err(err).Msg("failed to find user by reset token")
		return nil, fmt.Errorf("failed to find user: %w", err)
	}
	return user, nil
}

func (r *repository) UpdateRole(ctx context.Context, userID uuid.UUID, role UserRole) error {
	query := `
		UPDATE users
		SET user_type = $2, updated_at = $3
		WHERE id = $1
		RETURNING updated_at
	`
	now := time.Now()
	err := r.db.QueryRow(ctx, query, userID, string(role), now).Scan(&now)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrUserNotFound
		}
		log.Error().Err(err).Str("id", userID.String()).Msg("failed to update user type")
		return fmt.Errorf("failed to update user type: %w", err)
	}
	return nil
}

func (r *repository) HasRole(ctx context.Context, role UserRole) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE user_type = $1)`, string(role)).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check user type: %w", err)
	}
	return exists, nil
}

func (r *repository) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		log.Error().Err(err).Str("id", id.String()).Msg("failed to delete user")
		return fmt.Errorf("failed to delete user: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}
