// Package auth implements authentication and authorization for aisync.
//
// It is a separate bounded context from session management. It provides:
//   - User accounts with bcrypt-hashed passwords
//   - API keys for machine-to-machine access
//   - JWT tokens for session-based authentication
//   - Role-based access control (admin, user, readonly)
//
// Architecture (Hexagonal/DDD):
//   - domain.go: value objects, entities, domain errors
//   - jwt.go: JWT token issuer/validator (infrastructure)
//   - service.go: AuthService use case (orchestration)
//   - Store interface defined in storage package (port)
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ── Domain Errors ──

var (
	// ErrUserNotFound is returned when a user cannot be found.
	ErrUserNotFound = errors.New("user not found")

	// ErrUserExists is returned when attempting to create a user with an existing username.
	ErrUserExists = errors.New("user already exists")

	// ErrInvalidCredentials is returned when username/password don't match.
	ErrInvalidCredentials = errors.New("invalid credentials")

	// ErrTokenExpired is returned when a JWT token has expired.
	ErrTokenExpired = errors.New("token expired")

	// ErrTokenInvalid is returned when a JWT token is malformed or has an invalid signature.
	ErrTokenInvalid = errors.New("token invalid")

	// ErrAPIKeyNotFound is returned when an API key cannot be found.
	ErrAPIKeyNotFound = errors.New("api key not found")

	// ErrAPIKeyRevoked is returned when an API key has been deactivated.
	ErrAPIKeyRevoked = errors.New("api key revoked")

	// ErrUnauthorized is returned when the caller lacks permission.
	ErrUnauthorized = errors.New("unauthorized")
)

// ── Role (Value Object) ──

// Role represents a user's authorization level.
type Role string

const (
	// RoleAdmin has full access — can manage users, API keys, and all data.
	RoleAdmin Role = "admin"

	// RoleUser has read-write access to sessions, analysis, and their own API keys.
	RoleUser Role = "user"

	// RoleReadonly has read-only access — can view sessions, stats, and analysis.
	RoleReadonly Role = "readonly"
)

// Valid reports whether r is a known role value.
func (r Role) Valid() bool {
	switch r {
	case RoleAdmin, RoleUser, RoleReadonly:
		return true
	}
	return false
}

// String returns the string representation of the role.
func (r Role) String() string {
	return string(r)
}

// CanWrite reports whether this role has write permissions.
func (r Role) CanWrite() bool {
	return r == RoleAdmin || r == RoleUser
}

// IsAdmin reports whether this role has admin privileges.
func (r Role) IsAdmin() bool {
	return r == RoleAdmin
}

// ── User (Entity) ──

// User represents an authenticated aisync user.
// The password is never stored in plaintext — only the bcrypt hash.
type User struct {
	// ID is a unique identifier for the user (UUID).
	ID string `json:"id"`

	// Username is the login name (unique, lowercase).
	Username string `json:"username"`

	// PasswordHash is the bcrypt hash of the user's password.
	// Never serialized to JSON responses.
	PasswordHash string `json:"-"`

	// Role determines the user's authorization level.
	Role Role `json:"role"`

	// Active indicates whether the user can authenticate.
	Active bool `json:"active"`

	// CreatedAt is when the user was created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when the user was last modified.
	UpdatedAt time.Time `json:"updated_at"`
}

// Minimum password length for new users.
const minPasswordLength = 8

// NewUser creates a new User with a hashed password.
// Validates username (non-empty), password (>= 8 chars), and role.
func NewUser(username, password string, role Role) (*User, error) {
	if username == "" {
		return nil, fmt.Errorf("username cannot be empty")
	}
	if len(password) < minPasswordLength {
		return nil, fmt.Errorf("password must be at least %d characters", minPasswordLength)
	}
	if !role.Valid() {
		return nil, fmt.Errorf("invalid role: %q", role)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hashing password: %w", err)
	}

	now := time.Now().UTC()
	return &User{
		ID:           generateID(),
		Username:     username,
		PasswordHash: string(hash),
		Role:         role,
		Active:       true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

// CheckPassword verifies a plaintext password against the stored hash.
func (u *User) CheckPassword(password string) bool {
	if password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil
}

// ── API Key (Entity) ──

// APIKey represents a machine-to-machine authentication token.
// The raw key is returned only at creation time — only the hash is stored.
type APIKey struct {
	// ID is a unique identifier for this API key (UUID).
	ID string `json:"id"`

	// UserID is the owner of this API key.
	UserID string `json:"user_id"`

	// Name is a human-readable label (e.g. "CI pipeline", "My laptop").
	Name string `json:"name"`

	// KeyHash is the SHA-256 hash of the raw API key.
	// Used for lookup. Never exposed.
	KeyHash string `json:"-"`

	// KeyPrefix stores the first few characters of the raw key for display
	// (e.g. "sk-abc1..." so the user can identify which key it is).
	KeyPrefix string `json:"key_prefix"`

	// Active indicates whether the key is usable.
	Active bool `json:"active"`

	// ExpiresAt is optional — nil means the key never expires.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	// LastUsedAt records the most recent authentication with this key.
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`

	// CreatedAt is when the key was generated.
	CreatedAt time.Time `json:"created_at"`
}

const (
	// apiKeyPrefix is prepended to all generated API keys.
	apiKeyPrefix = "sk-"

	// apiKeyRandomBytes is the number of random bytes in a key (32 bytes = 64 hex chars).
	apiKeyRandomBytes = 32

	// keyPrefixLength is how many characters of the raw key to store for display.
	keyPrefixLength = 8
)

// NewAPIKey creates a new API key for the given user.
// Returns the APIKey (with hash) and the raw key string (shown once to the user).
func NewAPIKey(userID, name string) (*APIKey, string, error) {
	if userID == "" {
		return nil, "", fmt.Errorf("user ID cannot be empty")
	}

	// Generate a random key.
	rawBytes := make([]byte, apiKeyRandomBytes)
	if _, err := rand.Read(rawBytes); err != nil {
		return nil, "", fmt.Errorf("generating random key: %w", err)
	}
	rawKey := apiKeyPrefix + hex.EncodeToString(rawBytes)

	// Hash the key with SHA-256 for storage/lookup.
	hash := sha256.Sum256([]byte(rawKey))
	keyHash := hex.EncodeToString(hash[:])

	// Store a prefix for user-facing display.
	prefix := rawKey[:min(keyPrefixLength, len(rawKey))]

	now := time.Now().UTC()
	return &APIKey{
		ID:        generateID(),
		UserID:    userID,
		Name:      name,
		KeyHash:   keyHash,
		KeyPrefix: prefix,
		Active:    true,
		CreatedAt: now,
	}, rawKey, nil
}

// MatchesKey checks if a raw API key matches this key's hash.
func (k *APIKey) MatchesKey(rawKey string) bool {
	if rawKey == "" {
		return false
	}
	hash := sha256.Sum256([]byte(rawKey))
	return k.KeyHash == hex.EncodeToString(hash[:])
}

// IsExpired reports whether this key has passed its expiration time.
// Returns false if ExpiresAt is nil (no expiry).
func (k *APIKey) IsExpired() bool {
	if k.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*k.ExpiresAt)
}

// ── Claims (Value Object) ──

// Claims represents the authenticated identity extracted from a JWT or API key.
// Injected into request context by the auth middleware.
type Claims struct {
	// UserID is the authenticated user's ID.
	UserID string `json:"user_id"`

	// Username is the authenticated user's login name.
	Username string `json:"username"`

	// Role is the user's authorization level.
	Role Role `json:"role"`

	// ExpiresAt is when the token expires.
	ExpiresAt time.Time `json:"expires_at"`

	// IssuedAt is when the token was issued.
	IssuedAt time.Time `json:"issued_at"`

	// Source indicates how the caller authenticated ("jwt" or "api_key").
	Source string `json:"source"`
}

// IsExpired reports whether these claims have expired.
func (c *Claims) IsExpired() bool {
	return time.Now().After(c.ExpiresAt)
}

// ── Helpers ──

// generateID creates a unique ID using crypto/rand.
func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback — should never happen.
		return fmt.Sprintf("auth-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
