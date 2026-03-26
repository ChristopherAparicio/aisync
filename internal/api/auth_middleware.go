package api

import (
	"net/http"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/auth"
)

// authMiddleware checks for authentication via Bearer token or X-API-Key header.
// If auth is disabled (authSvc is nil), requests pass through unauthenticated.
// Public routes (health, auth endpoints) bypass authentication.
//
// "Open until first registration" mode: when auth is enabled but no users have
// registered yet, all requests pass through without credentials. This makes the
// API immediately usable for development and debugging. Once the first user
// registers via POST /api/v1/auth/register, auth enforcement activates.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If auth is not configured, pass through.
		if s.authSvc == nil {
			next.ServeHTTP(w, r)
			return
		}

		// Public routes that don't require authentication.
		if isPublicRoute(r.Method, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// "Open until first registration" — bypass auth when no users exist.
		// The cache is invalidated (reset to 0) by handleAuthRegister so the
		// next request re-checks. This avoids a DB query on every request.
		state := s.authHasUsers.Load()
		if state != 2 {
			has, err := s.authSvc.HasUsers(r.Context())
			if err != nil {
				// If we can't check, fall through to normal auth (safe default).
				s.logger.Printf("WARN: failed to check user count: %v", err)
			} else if !has {
				s.authHasUsers.Store(1)
				next.ServeHTTP(w, r)
				return
			} else {
				s.authHasUsers.Store(2)
			}
		}

		// Try Bearer token first.
		if token := extractBearerToken(r); token != "" {
			claims, err := s.authSvc.ValidateToken(r.Context(), token)
			if err != nil {
				writeErrorWithCode(w, http.StatusUnauthorized, "invalid_token", "invalid or expired token")
				return
			}
			ctx := auth.WithClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Try API key.
		if apiKey := extractAPIKey(r); apiKey != "" {
			claims, err := s.authSvc.ValidateAPIKey(r.Context(), apiKey)
			if err != nil {
				writeErrorWithCode(w, http.StatusUnauthorized, "invalid_api_key", "invalid or revoked API key")
				return
			}
			ctx := auth.WithClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// No credentials provided.
		writeErrorWithCode(w, http.StatusUnauthorized, "missing_credentials",
			"authentication required — provide Authorization: Bearer <token> or X-API-Key header")
	})
}

// isPublicRoute returns true for routes that don't require authentication.
func isPublicRoute(method, path string) bool {
	switch {
	case path == "/api/v1/health":
		return true
	case method == "POST" && path == "/api/v1/auth/register":
		return true
	case method == "POST" && path == "/api/v1/auth/login":
		return true
	default:
		return false
	}
}

// extractBearerToken extracts the token from "Authorization: Bearer <token>".
func extractBearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// extractAPIKey extracts the API key from the X-API-Key header or api_key query param.
func extractAPIKey(r *http.Request) string {
	if key := r.Header.Get("X-API-Key"); key != "" {
		return key
	}
	return r.URL.Query().Get("api_key")
}
