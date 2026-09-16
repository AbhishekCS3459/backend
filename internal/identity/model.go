package identity

import (
	"time"

	"github.com/google/uuid"
)

type UserType string

const (
	UserTypeUser     UserType = "USER"
	UserTypeRetailer UserType = "RETAILER"
	UserTypeStaff    UserType = "STAFF"
	UserTypeAdmin    UserType = "ADMIN"
)

type UserStatus string

const (
	UserStatusActive   UserStatus = "ACTIVE"
	UserStatusInactive UserStatus = "INACTIVE"
)

// UserRole is an alias so existing auth helpers keep compiling.
type UserRole = UserType

const (
	RoleUser  = UserTypeUser
	RoleAdmin = UserTypeAdmin
)

type User struct {
	ID                     uuid.UUID  `json:"id" db:"id"`
	Phone                  string     `json:"phone" db:"phone"`
	IsPhoneVerified        bool       `json:"is_phone_verified" db:"is_phone_verified"`
	Email                  string     `json:"email" db:"email"`
	Password               string     `json:"-" db:"password_hash"`
	UserType               UserType   `json:"user_type" db:"user_type"`
	Status                 UserStatus `json:"status" db:"status"`
	PasswordResetToken     *string    `json:"-" db:"password_reset_token"`
	PasswordResetExpiresAt *time.Time `json:"-" db:"password_reset_expires_at"`
	CreatedAt              time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at" db:"updated_at"`
}

func (u *User) IsAdmin() bool {
	return u.UserType == UserTypeAdmin
}

func IsValidUserType(t UserType) bool {
	switch t {
	case UserTypeUser, UserTypeRetailer, UserTypeStaff, UserTypeAdmin:
		return true
	default:
		return false
	}
}

type KYCStatus string

const (
	KYCStatusPending  KYCStatus = "PENDING"
	KYCStatusApproved KYCStatus = "APPROVED"
	KYCStatusRejected KYCStatus = "REJECTED"
)

type Retailer struct {
	ID        uuid.UUID `json:"id" db:"id"`
	UserID    uuid.UUID `json:"user_id" db:"user_id"`
	LegalName string    `json:"legal_name" db:"legal_name"`
	OwnerName string    `json:"owner_name" db:"owner_name"`
	KYCStatus KYCStatus `json:"kyc_status" db:"kyc_status"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type RetailerKYC struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	RetailerID     uuid.UUID  `json:"retailer_id" db:"retailer_id"`
	IDProofURL     string     `json:"id_proof_url" db:"id_proof_url"`
	BusinessRegURL string     `json:"business_reg_url" db:"business_reg_url"`
	Status         KYCStatus  `json:"status" db:"status"`
	ReviewedBy     *uuid.UUID `json:"reviewed_by,omitempty" db:"reviewed_by"`
	ReviewedAt     *time.Time `json:"reviewed_at,omitempty" db:"reviewed_at"`
}

type RegisterRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=8"`
	Phone    string `json:"phone" validate:"required,min=8"`
}

type LoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
}

type LoginResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

type UpdateUserRequest struct {
	Email string `json:"email" validate:"required,email"`
	Phone string `json:"phone" validate:"required,min=8"`
}

type PasswordResetRequest struct {
	Email string `json:"email" validate:"required,email"`
}

type PasswordResetConfirm struct {
	Token    string `json:"token" validate:"required"`
	Password string `json:"password" validate:"required,min=8"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" validate:"required"`
	NewPassword     string `json:"new_password" validate:"required,min=8"`
}

type UserResponse struct {
	ID              uuid.UUID `json:"id"`
	Phone           string    `json:"phone"`
	IsPhoneVerified bool      `json:"is_phone_verified"`
	Email           string    `json:"email"`
	UserType        string    `json:"user_type"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (u *User) ToResponse() *UserResponse {
	return &UserResponse{
		ID:              u.ID,
		Phone:           u.Phone,
		IsPhoneVerified: u.IsPhoneVerified,
		Email:           u.Email,
		UserType:        string(u.UserType),
		Status:          string(u.Status),
		CreatedAt:       u.CreatedAt,
		UpdatedAt:       u.UpdatedAt,
	}
}
