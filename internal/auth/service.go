package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// ── Port Interface ──

// Servicer is the Use Case Port for authentication.
// Driving adapters (API handlers, CLI) depend on this interface.
type Servicer interface {
	// Register creates a new user account. The first user becomes admin.
	Register(ctx context.Context, req RegisterRequest) (*AuthResult, error)

	// Login authenticates with username+password, returns a JWT token.
	Login(ctx context.Context, req LoginRequest) (*AuthResult, error)

	// ValidateToken verifies a JWT token and returns the claims.
	ValidateToken(ctx context.Context, token string) (*Claims, error)

	// ValidateAPIKey verifies a raw API key and returns the claims.
	ValidateAPIKey(ctx context.Context, rawKey string) (*Claims, error)

	// CreateAPIKey generates a new API key for a user.
	CreateAPIKey(ctx context.Context, req CreateAPIKeyRequest) (*APIKeyResult, error)

	// ListAPIKeys returns all API keys for a user.
	ListAPIKeys(ctx context.Context, userID string) ([]*APIKey, error)

	// RevokeAPIKey deactivates an API key.
	RevokeAPIKey(ctx context.Context, keyID, userID string) error

	// DeleteAPIKey permanently removes an API key.
	DeleteAPIKey(ctx context.Context, keyID, userID string) error

	// ListUsers returns all users (admin only in practice, enforced at handler level).
	ListUsers(ctx context.Context) ([]*User, error)
}

// ── Store Port (Dependency Inversion) ──

// Store is the persistence port for the auth bounded context.
// Matches the AuthStore interface in storage package.
type Store interface {
	CreateAuthUser(user *User) error
	GetAuthUser(id string) (*User, error)
	GetAuthUserByUsername(username string) (*User, error)
	UpdateAuthUser(user *User) error
	ListAuthUsers() ([]*User, error)
	CountAuthUsers() (int, error)
	CreateAPIKey(key *APIKey) error
	GetAPIKeyByHash(keyHash string) (*APIKey, error)
	ListAPIKeysByUser(userID string) ([]*APIKey, error)
	UpdateAPIKey(key *APIKey) error
	DeleteAPIKey(id string) error
}

// ── Request/Response DTOs ──

// RegisterRequest is the input for user registration.
type RegisterRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginRequest is the input for login.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AuthResult is returned by Register and Login.
type AuthResult struct {
	User  *User  `json:"user"`
	Token string `json:"token"`
}

// CreateAPIKeyRequest is the input for API key creation.
type CreateAPIKeyRequest struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

// APIKeyResult is returned when creating an API key.
// RawKey is only available at creation time.
type APIKeyResult struct {
	APIKey *APIKey `json:"api_key"`
	RawKey string  `json:"raw_key"` // only returned once
}

// ── Service Implementation ──

// Service orchestrates authentication use cases.
type Service struct {
	store Store
	jwt   *JWTManager
}

// ServiceConfig holds dependencies for creating a Service.
type ServiceConfig struct {
	// Store is the auth persistence adapter (required).
	Store Store

	// JWTSecret is the HMAC signing key for JWT tokens (required).
	JWTSecret string

	// TokenTTL is the lifetime of issued JWT tokens (default: 24h).
	TokenTTL time.Duration
}

// NewService creates a new auth service.
func NewService(cfg ServiceConfig) *Service {
	ttl := cfg.TokenTTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	return &Service{
		store: cfg.Store,
		jwt:   NewJWTManager(cfg.JWTSecret, ttl),
	}
}

// Compile-time check.
var _ Servicer = (*Service)(nil)

// ── Use Cases ──

// Register creates a new user. The first registered user becomes admin.
func (s *Service) Register(_ context.Context, req RegisterRequest) (*AuthResult, error) {
	// Determine role: first user is admin, subsequent users are regular users.
	role := RoleUser
	count, err := s.store.CountAuthUsers()
	if err != nil {
		return nil, fmt.Errorf("counting users: %w", err)
	}
	if count == 0 {
		role = RoleAdmin
	}

	user, err := NewUser(req.Username, req.Password, role)
	if err != nil {
		return nil, err
	}

	if err := s.store.CreateAuthUser(user); err != nil {
		return nil, err
	}

	token, err := s.jwt.Issue(user)
	if err != nil {
		return nil, fmt.Errorf("issuing token: %w", err)
	}

	return &AuthResult{User: user, Token: token}, nil
}

// Login authenticates a user and returns a JWT token.
func (s *Service) Login(_ context.Context, req LoginRequest) (*AuthResult, error) {
	user, err := s.store.GetAuthUserByUsername(req.Username)
	if err != nil {
		// Don't leak whether the username exists.
		return nil, ErrInvalidCredentials
	}

	if !user.Active {
		return nil, ErrInvalidCredentials
	}

	if !user.CheckPassword(req.Password) {
		return nil, ErrInvalidCredentials
	}

	token, err := s.jwt.Issue(user)
	if err != nil {
		return nil, fmt.Errorf("issuing token: %w", err)
	}

	return &AuthResult{User: user, Token: token}, nil
}

// ValidateToken verifies a JWT token and returns claims.
func (s *Service) ValidateToken(_ context.Context, token string) (*Claims, error) {
	return s.jwt.Validate(token)
}

// ValidateAPIKey looks up a raw API key, verifies it, and returns claims.
func (s *Service) ValidateAPIKey(_ context.Context, rawKey string) (*Claims, error) {
	// Hash the raw key to look it up.
	hash := sha256.Sum256([]byte(rawKey))
	keyHash := hex.EncodeToString(hash[:])

	key, err := s.store.GetAPIKeyByHash(keyHash)
	if err != nil {
		return nil, ErrAPIKeyNotFound
	}

	if !key.Active {
		return nil, ErrAPIKeyRevoked
	}

	if key.IsExpired() {
		return nil, ErrTokenExpired
	}

	// Look up the owning user for role information.
	user, err := s.store.GetAuthUser(key.UserID)
	if err != nil {
		return nil, fmt.Errorf("looking up key owner: %w", err)
	}

	// Update last_used_at (best-effort, don't fail the request).
	now := time.Now().UTC()
	key.LastUsedAt = &now
	_ = s.store.UpdateAPIKey(key)

	return &Claims{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		Source:   "api_key",
	}, nil
}

// CreateAPIKey generates a new API key for a user.
func (s *Service) CreateAPIKey(_ context.Context, req CreateAPIKeyRequest) (*APIKeyResult, error) {
	// Verify the user exists.
	if _, err := s.store.GetAuthUser(req.UserID); err != nil {
		return nil, fmt.Errorf("user not found: %w", err)
	}

	key, rawKey, err := NewAPIKey(req.UserID, req.Name)
	if err != nil {
		return nil, err
	}

	if err := s.store.CreateAPIKey(key); err != nil {
		return nil, fmt.Errorf("storing API key: %w", err)
	}

	return &APIKeyResult{APIKey: key, RawKey: rawKey}, nil
}

// ListAPIKeys returns all API keys for a user.
func (s *Service) ListAPIKeys(_ context.Context, userID string) ([]*APIKey, error) {
	return s.store.ListAPIKeysByUser(userID)
}

// RevokeAPIKey deactivates an API key (soft delete).
func (s *Service) RevokeAPIKey(_ context.Context, keyID, userID string) error {
	// List user's keys and find the matching one.
	keys, err := s.store.ListAPIKeysByUser(userID)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if k.ID == keyID {
			k.Active = false
			now := time.Now().UTC()
			k.LastUsedAt = &now
			return s.store.UpdateAPIKey(k)
		}
	}
	return ErrAPIKeyNotFound
}

// DeleteAPIKey permanently removes an API key.
func (s *Service) DeleteAPIKey(_ context.Context, keyID, _ string) error {
	return s.store.DeleteAPIKey(keyID)
}

// ListUsers returns all registered users.
func (s *Service) ListUsers(_ context.Context) ([]*User, error) {
	return s.store.ListAuthUsers()
}
