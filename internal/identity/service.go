package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
)

// Service is the identity business-logic API.
type Service interface {
	Register(ctx context.Context, req *RegisterRequest) (*User, error)
	Login(ctx context.Context, req *LoginRequest) (*LoginResponse, error)
	GetUser(ctx context.Context, id uuid.UUID) (*User, error)
	ListUsers(ctx context.Context, limit, offset int) ([]*User, error)
	UpdateUser(ctx context.Context, id uuid.UUID, email, phone string) (*User, error)
	DeleteUser(ctx context.Context, id uuid.UUID) error
	ValidateToken(tokenString string) (uuid.UUID, string, string, error)
	RequestPasswordReset(ctx context.Context, email string) error
	ResetPassword(ctx context.Context, token, newPassword string) error
	ChangePassword(ctx context.Context, userID uuid.UUID, currentPassword, newPassword string) error
	BootstrapAdmin(ctx context.Context, email, password, phone string) error
	UpdateUserRole(ctx context.Context, targetUserID uuid.UUID, newRole UserRole) error
}

type service struct {
	repo      Repository
	jwtSecret []byte
}

func NewService(repo Repository, jwtSecret string) Service {
	return &service{
		repo:      repo,
		jwtSecret: []byte(jwtSecret),
	}
}

func (s *service) Register(ctx context.Context, req *RegisterRequest) (*User, error) {
	_, err := s.repo.FindByEmail(ctx, req.Email)
	if err == nil {
		return nil, ErrUserAlreadyExists
	}
	if !errors.Is(err, ErrUserNotFound) {
		return nil, fmt.Errorf("failed to check if user exists: %w", err)
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	user := &User{
		ID:       uuid.New(),
		Email:    req.Email,
		Phone:    req.Phone,
		Password: string(hashedPassword),
		UserType: UserTypeUser,
		Status:   UserStatusActive,
	}

	if err := s.repo.Create(ctx, user); err != nil {
		if err == ErrUserAlreadyExists {
			return nil, err
		}
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	return user, nil
}

func (s *service) Login(ctx context.Context, req *LoginRequest) (*LoginResponse, error) {
	user, err := s.repo.FindByEmail(ctx, req.Email)
	if err != nil {
		return nil, fmt.Errorf("invalid email or password")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		return nil, fmt.Errorf("invalid email or password")
	}

	token, err := s.generateToken(user.ID, user.Email, user.UserType)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	return &LoginResponse{
		Token: token,
		User:  *user,
	}, nil
}

func (s *service) GetUser(ctx context.Context, id uuid.UUID) (*User, error) {
	user, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *service) ListUsers(ctx context.Context, limit, offset int) ([]*User, error) {
	users, err := s.repo.List(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	return users, nil
}

func (s *service) UpdateUser(ctx context.Context, id uuid.UUID, email, phone string) (*User, error) {
	user, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	user.Email = email
	user.Phone = phone

	if err := s.repo.Update(ctx, user); err != nil {
		return nil, err
	}

	return user, nil
}

func (s *service) DeleteUser(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}

func (s *service) generateToken(userID uuid.UUID, email string, role UserRole) (string, error) {
	claims := jwt.MapClaims{
		"user_id": userID.String(),
		"email":   email,
		"role":    string(role),
		"exp":     time.Now().Add(time.Hour * 24 * 7).Unix(),
		"iat":     time.Now().Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	return tokenString, nil
}

func (s *service) ValidateToken(tokenString string) (uuid.UUID, string, string, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.jwtSecret, nil
	})

	if err != nil {
		return uuid.Nil, "", string(RoleUser), fmt.Errorf("invalid token: %w", err)
	}

	if !token.Valid {
		return uuid.Nil, "", string(RoleUser), fmt.Errorf("invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return uuid.Nil, "", string(RoleUser), fmt.Errorf("invalid token claims")
	}

	userIDStr, ok := claims["user_id"].(string)
	if !ok {
		return uuid.Nil, "", string(RoleUser), fmt.Errorf("invalid user_id in token")
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return uuid.Nil, "", string(RoleUser), fmt.Errorf("invalid user_id format: %w", err)
	}

	email, _ := claims["email"].(string)
	roleStr, _ := claims["role"].(string)
	role := UserRole(roleStr)
	if !IsValidUserType(role) {
		role = UserTypeUser
	}

	return userID, email, string(role), nil
}

func (s *service) RequestPasswordReset(ctx context.Context, email string) error {
	_, err := s.repo.FindByEmail(ctx, email)
	if err != nil {
		return nil
	}

	resetToken := uuid.New().String()
	expiresAt := time.Now().Add(time.Hour * 1)

	if err := s.repo.SetPasswordResetToken(ctx, email, resetToken, expiresAt); err != nil {
		return fmt.Errorf("failed to set reset token: %w", err)
	}

	log.Info().
		Str("email", email).
		Str("token", resetToken).
		Msg("password reset token generated (send email in production)")
	fmt.Printf("Password reset token for %s: %s (expires in 1 hour)\n", email, resetToken)
	fmt.Printf("Reset link: http://localhost:8080/api/auth/reset-password?token=%s\n", resetToken)

	return nil
}

func (s *service) ResetPassword(ctx context.Context, token, newPassword string) error {
	user, err := s.repo.FindByPasswordResetToken(ctx, token)
	if err != nil {
		return fmt.Errorf("invalid or expired reset token")
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	if err := s.repo.UpdatePassword(ctx, user.ID, string(hashedPassword)); err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}

	return nil
}

func (s *service) ChangePassword(ctx context.Context, userID uuid.UUID, currentPassword, newPassword string) error {
	user, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("user not found")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(currentPassword)); err != nil {
		return fmt.Errorf("current password is incorrect")
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	if err := s.repo.UpdatePassword(ctx, userID, string(hashedPassword)); err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}

	return nil
}

func (s *service) BootstrapAdmin(ctx context.Context, email, password, phone string) error {
	if email == "" || password == "" {
		return nil
	}
	if len(password) < 8 {
		return fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD must be at least 8 characters")
	}

	hasAdmin, err := s.repo.HasRole(ctx, UserTypeAdmin)
	if err != nil {
		return err
	}
	if hasAdmin {
		log.Info().Msg("admin user already exists, skipping bootstrap")
		return nil
	}

	existing, err := s.repo.FindByEmail(ctx, email)
	if err == nil {
		if err := s.repo.UpdateRole(ctx, existing.ID, UserTypeAdmin); err != nil {
			return err
		}
		log.Info().Str("email", email).Msg("promoted existing user to admin")
		return nil
	}
	if !errors.Is(err, ErrUserNotFound) {
		return err
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash bootstrap admin password: %w", err)
	}

	if phone == "" {
		phone = "+10000000000"
	}

	user := &User{
		ID:       uuid.New(),
		Email:    email,
		Phone:    phone,
		Password: string(hashedPassword),
		UserType: UserTypeAdmin,
		Status:   UserStatusActive,
	}
	if err := s.repo.Create(ctx, user); err != nil {
		return fmt.Errorf("failed to create bootstrap admin: %w", err)
	}

	log.Info().Str("email", email).Msg("created bootstrap admin user")
	return nil
}

func (s *service) UpdateUserRole(ctx context.Context, targetUserID uuid.UUID, newRole UserRole) error {
	if !IsValidUserType(newRole) {
		return fmt.Errorf("invalid user_type")
	}

	return s.repo.UpdateRole(ctx, targetUserID, newRole)
}
