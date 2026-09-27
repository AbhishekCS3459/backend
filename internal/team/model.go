package team

import (
	"errors"
	"time"

	"github.com/AbhishekCS3459/find-me-backend/internal/storeaccess"
	"github.com/google/uuid"
)

var (
	ErrNotFound      = errors.New("team member not found")
	ErrForbidden     = errors.New("you don't have permission to manage this team member")
	ErrNoTeamAccess  = errors.New("you don't manage any store team")
	ErrAccountInUse  = errors.New("this phone or email already belongs to another Todayz account")
	ErrAlreadyMember = errors.New("this person is already on the team for the selected stores")
	ErrSelf          = errors.New("you can't change your own team access")
)

// ValidationError is returned for bad input; its message is safe to show.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

const (
	StatusActive  = "active"
	StatusPending = "pending"
	StatusPaused  = "paused"
)

type StoreRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type Member struct {
	UserID      uuid.UUID                `json:"user_id"`
	FullName    string                   `json:"full_name"`
	Phone       string                   `json:"phone"`
	Email       string                   `json:"email"`
	Role        storeaccess.Role         `json:"role"`
	Permissions []storeaccess.Permission `json:"permissions"`
	Stores      []StoreRef               `json:"stores"`
	// Status is "pending" until the member replaces their temporary password.
	Status  string    `json:"status"`
	AddedAt time.Time `json:"added_at"`
}

type CreateMemberRequest struct {
	FullName          string           `json:"full_name" validate:"required,min=2,max=255"`
	Phone             string           `json:"phone" validate:"required,max=32"`
	Email             string           `json:"email" validate:"omitempty,email,max=255"`
	Role              storeaccess.Role `json:"role" validate:"required"`
	Permissions       []string         `json:"permissions"`
	StoreIDs          []uuid.UUID      `json:"store_ids" validate:"required,min=1,max=100"`
	TemporaryPassword string           `json:"temporary_password" validate:"required,min=8,max=72"`
}

type CreateMemberResponse struct {
	Member Member `json:"member"`
	// ExistingAccount is true when the phone/email already had a staff account;
	// the temporary password is ignored and they keep their own password.
	ExistingAccount bool `json:"existing_account"`
}

type UpdateMemberRequest struct {
	FullName    string           `json:"full_name" validate:"required,min=2,max=255"`
	Role        storeaccess.Role `json:"role" validate:"required"`
	Permissions []string         `json:"permissions"`
	StoreIDs    []uuid.UUID      `json:"store_ids" validate:"required,min=1,max=100"`
	Active      bool             `json:"active"`
}

type ResetPasswordRequest struct {
	TemporaryPassword string `json:"temporary_password" validate:"required,min=8,max=72"`
}
