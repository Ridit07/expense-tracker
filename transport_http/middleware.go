package transport_http

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"expense-tracker/common"
	"expense-tracker/services"
)

type ctxKey string

const userIDKey ctxKey = "user_id"

// Middleware is a standard http handler decorator.
type Middleware func(http.Handler) http.Handler

// Chain applies middlewares in order (first listed runs outermost).
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// Recoverer converts panics into 500 responses so one bad request can't take
// down the server.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic recovered: %v", rec)
				common.WriteError(w, http.StatusInternalServerError, "internal_error", "something went wrong")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Logger logs method, path, status and latency for every request.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start))
	})
}

// JWTAuth validates the Bearer token and puts the resolved user id in the
// request context.
func JWTAuth(auth *services.AuthService) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			token = strings.TrimSpace(token)
			if !ok || token == "" {
				common.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
				return
			}

			userID, err := auth.ParseToken(token)
			if err != nil {
				common.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
				return
			}

			ctx := context.WithValue(r.Context(), userIDKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// userIDFromContext returns the authenticated user id set by APIKeyAuth.
func userIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(userIDKey).(string); ok {
		return v
	}
	return ""
}

// statusWriter captures the response status code for logging.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
