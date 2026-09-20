package identity

import (
	"github.com/AbhishekCS3459/find-me-backend/internal/platform/middleware"
	"github.com/go-chi/chi/v5"
)

// AuthRoutes registers /register, /login, and password endpoints (mount under /auth).
func (h *Handler) AuthRoutes() chi.Router {
	r := chi.NewRouter()
	r.Post("/register", h.Register)
	r.Post("/login", h.Login)
	r.Post("/request-password-reset", h.RequestPasswordReset)
	r.Post("/reset-password", h.ResetPassword)
	r.Post("/change-password", h.ChangePassword)
	return r
}

// UserRoutes registers user CRUD endpoints (mount under /users).
func (h *Handler) UserRoutes() chi.Router {
	r := chi.NewRouter()
	r.Get("/me", h.GetMe)
	r.Patch("/me", h.UpdateMe)
	r.Post("/me/verify-phone", h.VerifyPhone)
	r.With(middleware.RequireAdmin).Get("/", h.ListUsers)
	r.With(middleware.RequireAdmin).Post("/", h.CreateUser)
	r.With(middleware.RequireAdmin).Delete("/{id}", h.DeleteUser)
	r.With(middleware.RequireAdmin).Put("/{id}/role", h.UpdateUserRole)
	r.With(middleware.RequireOwnerOrAdmin("id")).Get("/{id}", h.GetUser)
	r.With(middleware.RequireOwnerOrAdmin("id")).Put("/{id}", h.UpdateUser)
	return r
}

// Routes registers this domain's endpoints relative to /api.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Mount("/auth", h.AuthRoutes())
	r.Mount("/users", h.UserRoutes())
	return r
}
