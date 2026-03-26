package api

import (
	"net/http"

	"github.com/ChristopherAparicio/aisync/internal/auth"
)

// ── View DTOs (separate from domain) ──
// These are the JSON shapes exposed to API consumers.
// They deliberately differ from domain types to maintain boundary separation.

type registerRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authResponse struct {
	Token string       `json:"token"`
	User  userResponse `json:"user"`
}

type userResponse struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"created_at"`
}

type createKeyRequest struct {
	Name string `json:"name"`
}

type apiKeyResponse struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	KeyPrefix string  `json:"key_prefix"`
	Active    bool    `json:"active"`
	ExpiresAt *string `json:"expires_at,omitempty"`
	CreatedAt string  `json:"created_at"`
}

type createKeyResponse struct {
	RawKey string         `json:"raw_key"` // shown only once
	APIKey apiKeyResponse `json:"api_key"`
}

// ── Mappers (domain → view) ──

func toUserResponse(u *auth.User) userResponse {
	return userResponse{
		ID:        u.ID,
		Username:  u.Username,
		Role:      u.Role.String(),
		Active:    u.Active,
		CreatedAt: u.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

func toAPIKeyResponse(k *auth.APIKey) apiKeyResponse {
	resp := apiKeyResponse{
		ID:        k.ID,
		Name:      k.Name,
		KeyPrefix: k.KeyPrefix,
		Active:    k.Active,
		CreatedAt: k.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if k.ExpiresAt != nil {
		s := k.ExpiresAt.Format("2006-01-02T15:04:05Z")
		resp.ExpiresAt = &s
	}
	return resp
}

// ── Handlers ──

// handleAuthRegister creates a new user account.
// POST /api/v1/auth/register
func (s *Server) handleAuthRegister(w http.ResponseWriter, r *http.Request) {
	if s.authSvc == nil {
		writeError(w, http.StatusNotFound, "authentication not configured")
		return
	}

	var req registerRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	result, err := s.authSvc.Register(r.Context(), auth.RegisterRequest{
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		mapAuthError(w, err)
		return
	}

	// Invalidate the "no users" cache so the auth middleware starts enforcing
	// credentials on subsequent requests.
	s.authHasUsers.Store(0)

	writeJSON(w, http.StatusCreated, authResponse{
		Token: result.Token,
		User:  toUserResponse(result.User),
	})
}

// handleAuthLogin authenticates a user and returns a JWT token.
// POST /api/v1/auth/login
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if s.authSvc == nil {
		writeError(w, http.StatusNotFound, "authentication not configured")
		return
	}

	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	result, err := s.authSvc.Login(r.Context(), auth.LoginRequest{
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		mapAuthError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, authResponse{
		Token: result.Token,
		User:  toUserResponse(result.User),
	})
}

// handleAuthMe returns the current user's info from the JWT/API key claims.
// GET /api/v1/auth/me
func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	if s.authSvc == nil {
		writeError(w, http.StatusNotFound, "authentication not configured")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		writeErrorWithCode(w, http.StatusUnauthorized, "unauthenticated", "no valid credentials")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":  claims.UserID,
		"username": claims.Username,
		"role":     claims.Role.String(),
		"source":   claims.Source,
	})
}

// handleCreateAPIKey creates a new API key for the authenticated user.
// POST /api/v1/auth/keys
func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	if s.authSvc == nil {
		writeError(w, http.StatusNotFound, "authentication not configured")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		writeErrorWithCode(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	var req createKeyRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	result, err := s.authSvc.CreateAPIKey(r.Context(), auth.CreateAPIKeyRequest{
		UserID: claims.UserID,
		Name:   req.Name,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, createKeyResponse{
		RawKey: result.RawKey,
		APIKey: toAPIKeyResponse(result.APIKey),
	})
}

// handleListAPIKeys returns all API keys for the authenticated user.
// GET /api/v1/auth/keys
func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	if s.authSvc == nil {
		writeError(w, http.StatusNotFound, "authentication not configured")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		writeErrorWithCode(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	keys, err := s.authSvc.ListAPIKeys(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var response []apiKeyResponse
	for _, k := range keys {
		response = append(response, toAPIKeyResponse(k))
	}
	if response == nil {
		response = []apiKeyResponse{}
	}

	writeJSON(w, http.StatusOK, response)
}

// handleRevokeAPIKey deactivates an API key.
// POST /api/v1/auth/keys/{id}/revoke
func (s *Server) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	if s.authSvc == nil {
		writeError(w, http.StatusNotFound, "authentication not configured")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		writeErrorWithCode(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	keyID := r.PathValue("id")
	if keyID == "" {
		writeError(w, http.StatusBadRequest, "key id is required")
		return
	}

	if err := s.authSvc.RevokeAPIKey(r.Context(), keyID, claims.UserID); err != nil {
		mapAuthError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// handleDeleteAPIKey permanently removes an API key.
// DELETE /api/v1/auth/keys/{id}
func (s *Server) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	if s.authSvc == nil {
		writeError(w, http.StatusNotFound, "authentication not configured")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		writeErrorWithCode(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	keyID := r.PathValue("id")
	if keyID == "" {
		writeError(w, http.StatusBadRequest, "key id is required")
		return
	}

	if err := s.authSvc.DeleteAPIKey(r.Context(), keyID, claims.UserID); err != nil {
		mapAuthError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleListUsers returns all users (admin only).
// GET /api/v1/auth/users
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if s.authSvc == nil {
		writeError(w, http.StatusNotFound, "authentication not configured")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		writeErrorWithCode(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	if !claims.Role.IsAdmin() {
		writeErrorWithCode(w, http.StatusForbidden, "forbidden", "admin access required")
		return
	}

	users, err := s.authSvc.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var response []userResponse
	for _, u := range users {
		response = append(response, toUserResponse(u))
	}
	if response == nil {
		response = []userResponse{}
	}

	writeJSON(w, http.StatusOK, response)
}

// ── Error mapping ──

// mapAuthError maps auth domain errors to HTTP status codes.
func mapAuthError(w http.ResponseWriter, err error) {
	switch err {
	case auth.ErrUserNotFound:
		writeErrorWithCode(w, http.StatusNotFound, "user_not_found", err.Error())
	case auth.ErrUserExists:
		writeErrorWithCode(w, http.StatusConflict, "user_exists", err.Error())
	case auth.ErrInvalidCredentials:
		writeErrorWithCode(w, http.StatusUnauthorized, "invalid_credentials", err.Error())
	case auth.ErrTokenExpired:
		writeErrorWithCode(w, http.StatusUnauthorized, "token_expired", err.Error())
	case auth.ErrTokenInvalid:
		writeErrorWithCode(w, http.StatusUnauthorized, "token_invalid", err.Error())
	case auth.ErrAPIKeyNotFound:
		writeErrorWithCode(w, http.StatusNotFound, "api_key_not_found", err.Error())
	case auth.ErrAPIKeyRevoked:
		writeErrorWithCode(w, http.StatusUnauthorized, "api_key_revoked", err.Error())
	case auth.ErrUnauthorized:
		writeErrorWithCode(w, http.StatusForbidden, "unauthorized", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
