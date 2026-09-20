package identity

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/httputil"
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// Handler handles identity HTTP requests (auth + users).
type Handler struct {
	svc Service
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

// Register handles user registration
// @Summary Register a new user
// @Description Create a new user account
// @Tags Authentication
// @Accept json
// @Produce json
// @Param request body RegisterRequest true "Registration data"
// @Success 201 {object} UserResponse
// @Failure 400 {object} httputil.ErrorResponse
// @Failure 409 {object} httputil.ErrorResponse
// @Router /api/auth/register [post]
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := httputil.ValidateStruct(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	user, err := h.svc.Register(r.Context(), &req)
	if err != nil {
		if errors.Is(err, ErrUserAlreadyExists) {
			httputil.WriteError(w, http.StatusConflict, "user with this email already exists")
			return
		}
		log.Error().Err(err).Str("email", req.Email).Msg("failed to register user")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to register user")
		return
	}

	httputil.WriteJSON(w, http.StatusCreated, user.ToResponse())
}

// Login handles user login
// @Summary Login user
// @Description Authenticate user and get JWT token
// @Tags Authentication
// @Accept json
// @Produce json
// @Param request body LoginRequest true "Login credentials"
// @Success 200 {object} LoginResponse
// @Failure 401 {object} httputil.ErrorResponse
// @Router /api/auth/login [post]
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := httputil.ValidateStruct(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	response, err := h.svc.Login(r.Context(), &req)
	if err != nil {
		log.Error().Err(err).Str("email", req.Email).Msg("failed to login")
		httputil.WriteError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, response)
}

// RequestPasswordReset handles password reset requests
// @Summary Request password reset
// @Description Request a password reset token via email
// @Tags Authentication
// @Accept json
// @Produce json
// @Param request body PasswordResetRequest true "Password reset request"
// @Success 200 {object} map[string]string
// @Failure 400 {object} httputil.ErrorResponse
// @Router /api/auth/request-password-reset [post]
func (h *Handler) RequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req PasswordResetRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := httputil.ValidateStruct(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.svc.RequestPasswordReset(r.Context(), req.Email); err != nil {
		log.Error().Err(err).Str("email", req.Email).Msg("failed to request password reset")
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "If an account with that email exists, a password reset link has been sent",
	})
}

// ResetPassword handles password reset confirmation
// @Summary Reset password
// @Description Reset password using a reset token
// @Tags Authentication
// @Accept json
// @Produce json
// @Param request body PasswordResetConfirm true "Password reset confirmation"
// @Success 200 {object} map[string]string
// @Failure 400 {object} httputil.ErrorResponse
// @Failure 401 {object} httputil.ErrorResponse
// @Router /api/auth/reset-password [post]
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req PasswordResetConfirm
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := httputil.ValidateStruct(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.svc.ResetPassword(r.Context(), req.Token, req.Password); err != nil {
		log.Error().Err(err).Msg("failed to reset password")
		httputil.WriteError(w, http.StatusBadRequest, "invalid or expired reset token")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "Password has been reset successfully",
	})
}

// ChangePassword handles password change for logged-in users
// @Summary Change password
// @Description Change password for the authenticated user
// @Tags Authentication
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ChangePasswordRequest true "Password change request"
// @Success 200 {object} map[string]string
// @Failure 400 {object} httputil.ErrorResponse
// @Failure 401 {object} httputil.ErrorResponse
// @Router /api/auth/change-password [post]
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req ChangePasswordRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := httputil.ValidateStruct(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.svc.ChangePassword(r.Context(), userID, req.CurrentPassword, req.NewPassword); err != nil {
		log.Error().Err(err).Str("user_id", userID.String()).Msg("failed to change password")
		if err.Error() == "current password is incorrect" {
			httputil.WriteError(w, http.StatusBadRequest, "current password is incorrect")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, "failed to change password")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "Password has been changed successfully",
	})
}

// GetMe retrieves the current authenticated user's profile
// @Summary Get current user
// @Description Get the authenticated user's own profile
// @Tags Users
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} UserResponse
// @Failure 401 {object} httputil.ErrorResponse
// @Router /api/users/me [get]
func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	user, err := h.svc.GetUser(r.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			httputil.WriteError(w, http.StatusNotFound, ErrUserNotFoundMsg)
			return
		}
		log.Error().Err(err).Str("id", userID.String()).Msg("failed to get user")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to get user")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, user.ToResponse())
}

// UpdateMe updates the authenticated user's profile
// @Summary Update current user profile
// @Description Patch the authenticated user's profile fields
// @Tags Users
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body UpdateProfileRequest true "Profile fields"
// @Success 200 {object} UserResponse
// @Failure 400 {object} httputil.ErrorResponse
// @Failure 401 {object} httputil.ErrorResponse
// @Router /api/users/me [patch]
func (h *Handler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req UpdateProfileRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, err := h.svc.UpdateProfile(r.Context(), userID, &req)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			httputil.WriteError(w, http.StatusNotFound, ErrUserNotFoundMsg)
			return
		}
		log.Error().Err(err).Str("id", userID.String()).Msg("failed to update profile")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update profile")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, user.ToResponse())
}

// VerifyPhone marks the authenticated user's phone as verified after OTP check
// @Summary Verify phone with OTP
// @Description Dummy OTP verification for the current user (accepts 123456 or any 6-digit code)
// @Tags Users
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body VerifyPhoneRequest true "OTP payload"
// @Success 200 {object} UserResponse
// @Failure 400 {object} httputil.ErrorResponse
// @Failure 401 {object} httputil.ErrorResponse
// @Router /api/users/me/verify-phone [post]
func (h *Handler) VerifyPhone(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		httputil.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req VerifyPhoneRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := httputil.ValidateStruct(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	user, err := h.svc.VerifyPhone(r.Context(), userID, req.OTP)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			httputil.WriteError(w, http.StatusNotFound, ErrUserNotFoundMsg)
			return
		}
		if err.Error() == "invalid otp" {
			httputil.WriteError(w, http.StatusBadRequest, "invalid otp")
			return
		}
		log.Error().Err(err).Str("id", userID.String()).Msg("failed to verify phone")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to verify phone")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, user.ToResponse())
}

// ListUsers retrieves a list of users (admin only)
// @Summary List users
// @Description Get a paginated list of all users (admin only)
// @Tags Users
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param limit query int false "Limit" default(10)
// @Param offset query int false "Offset" default(0)
// @Success 200 {array} UserResponse
// @Failure 403 {object} httputil.ErrorResponse
// @Failure 401 {object} httputil.ErrorResponse
// @Router /api/users [get]
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	limit := 10
	offset := 0

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	users, err := h.svc.ListUsers(r.Context(), limit, offset)
	if err != nil {
		log.Error().Err(err).Msg("failed to list users")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to list users")
		return
	}

	responses := make([]*UserResponse, len(users))
	for i, user := range users {
		responses[i] = user.ToResponse()
	}

	httputil.WriteJSON(w, http.StatusOK, responses)
}

// GetUser retrieves a user by ID
// @Summary Get user by ID
// @Description Get a specific user by their ID. Users can only access their own profile, admins can access any.
// @Tags Users
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "User ID"
// @Success 200 {object} UserResponse
// @Failure 403 {object} httputil.ErrorResponse
// @Failure 404 {object} httputil.ErrorResponse
// @Failure 401 {object} httputil.ErrorResponse
// @Router /api/users/{id} [get]
func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	user, err := h.svc.GetUser(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			httputil.WriteError(w, http.StatusNotFound, ErrUserNotFoundMsg)
			return
		}
		log.Error().Err(err).Str("id", id.String()).Msg("failed to get user")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to get user")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, user.ToResponse())
}

// CreateUser creates a new user
// @Summary Create a new user
// @Description Create a new user account (admin only)
// @Tags Users
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body RegisterRequest true "User data"
// @Success 201 {object} UserResponse
// @Failure 400 {object} httputil.ErrorResponse
// @Failure 401 {object} httputil.ErrorResponse
// @Router /api/users [post]
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := httputil.ValidateStruct(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	user, err := h.svc.Register(r.Context(), &req)
	if err != nil {
		if errors.Is(err, ErrUserAlreadyExists) {
			httputil.WriteError(w, http.StatusConflict, ErrUserExistsMsg)
			return
		}
		log.Error().Err(err).Str("email", req.Email).Msg("failed to create user")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	httputil.WriteJSON(w, http.StatusCreated, user.ToResponse())
}

// UpdateUser updates a user's information
// @Summary Update user
// @Description Update a user's information. Users can only update their own profile, admins can update any.
// @Tags Users
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "User ID"
// @Param request body UpdateUserRequest true "User data"
// @Success 200 {object} UserResponse
// @Failure 400 {object} httputil.ErrorResponse
// @Failure 403 {object} httputil.ErrorResponse
// @Failure 404 {object} httputil.ErrorResponse
// @Failure 401 {object} httputil.ErrorResponse
// @Router /api/users/{id} [put]
func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	var req UpdateUserRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := httputil.ValidateStruct(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	user, err := h.svc.UpdateUser(r.Context(), id, req.Email, req.Phone)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			httputil.WriteError(w, http.StatusNotFound, ErrUserNotFoundMsg)
			return
		}
		log.Error().Err(err).Str("id", id.String()).Msg("failed to update user")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, user.ToResponse())
}

// DeleteUser deletes a user (admin only)
// @Summary Delete user
// @Description Delete a user by ID (admin only)
// @Tags Users
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "User ID"
// @Success 204 "No Content"
// @Failure 403 {object} httputil.ErrorResponse
// @Failure 404 {object} httputil.ErrorResponse
// @Failure 401 {object} httputil.ErrorResponse
// @Router /api/users/{id} [delete]
func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	if err := h.svc.DeleteUser(r.Context(), id); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			httputil.WriteError(w, http.StatusNotFound, ErrUserNotFoundMsg)
			return
		}
		log.Error().Err(err).Str("id", id.String()).Msg("failed to delete user")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to delete user")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// UpdateUserRole updates a user's role (admin only)
// @Summary Update user role
// @Description Update a user's role (admin only)
// @Tags Users
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "User ID"
// @Param request body map[string]string true "Role update" example({"role": "admin"})
// @Success 200 {object} UserResponse
// @Failure 400 {object} httputil.ErrorResponse
// @Failure 403 {object} httputil.ErrorResponse
// @Failure 404 {object} httputil.ErrorResponse
// @Router /api/users/{id}/role [put]
func (h *Handler) UpdateUserRole(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	var req struct {
		UserType string `json:"user_type" validate:"required"`
	}

	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := httputil.ValidateStruct(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	newRole := UserType(req.UserType)
	if !IsValidUserType(newRole) {
		httputil.WriteError(w, http.StatusBadRequest, "user_type must be USER, RETAILER, STAFF, or ADMIN")
		return
	}
	if err := h.svc.UpdateUserRole(r.Context(), id, newRole); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			httputil.WriteError(w, http.StatusNotFound, ErrUserNotFoundMsg)
			return
		}
		log.Error().Err(err).Str("id", id.String()).Msg("failed to update user role")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update user role")
		return
	}

	user, err := h.svc.GetUser(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("id", id.String()).Msg("failed to get updated user")
		httputil.WriteError(w, http.StatusInternalServerError, "failed to get updated user")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, user.ToResponse())
}
