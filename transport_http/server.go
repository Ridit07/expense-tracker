package transport_http

import (
	"net/http"

	"expense-tracker/common"
	"expense-tracker/config"
	"expense-tracker/services"
)

// NewServer builds the fully wired http.Server: routes, middleware and
// dependencies. Public endpoints (health) skip auth; the /api/v1 surface is
// gated by API key + user id.
func NewServer(cfg *config.Config) *http.Server {
	mux := http.NewServeMux()

	authService := services.NewAuthService(cfg.JWTSecret)

	// Health check (unauthenticated).
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		common.WriteSuccess(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Public auth routes (more specific than /api/v1/, so they take precedence).
	NewAuthHandler(authService).Register(mux)

	// Protected API surface.
	apiMux := http.NewServeMux()
	NewMessageHandler(services.NewMessageService()).Register(apiMux)

	authed := Chain(apiMux, JWTAuth(authService))
	mux.Handle("/api/v1/", authed)

	// Global middleware wraps everything.
	handler := Chain(mux, Recoverer, Logger)

	return &http.Server{
		Addr:    ":" + cfg.HTTPPort,
		Handler: handler,
	}
}
