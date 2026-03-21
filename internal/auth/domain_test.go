package auth_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/auth"
)

// ── Role tests ──

func TestRole_Valid(t *testing.T) {
	tests := []struct {
		role  auth.Role
		valid bool
	}{
		{auth.RoleAdmin, true},
		{auth.RoleUser, true},
		{auth.RoleReadonly, true},
		{auth.Role("unknown"), false},
		{auth.Role(""), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			if got := tt.role.Valid(); got != tt.valid {
				t.Errorf("Role(%q).Valid() = %v, want %v", tt.role, got, tt.valid)
			}
		})
	}
}

func TestRole_String(t *testing.T) {
	if got := auth.RoleAdmin.String(); got != "admin" {
		t.Errorf("RoleAdmin.String() = %q, want %q", got, "admin")
	}
}

func TestRole_CanWrite(t *testing.T) {
	tests := []struct {
		role     auth.Role
		canWrite bool
	}{
		{auth.RoleAdmin, true},
		{auth.RoleUser, true},
		{auth.RoleReadonly, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			if got := tt.role.CanWrite(); got != tt.canWrite {
				t.Errorf("Role(%q).CanWrite() = %v, want %v", tt.role, got, tt.canWrite)
			}
		})
	}
}

func TestRole_IsAdmin(t *testing.T) {
	if !auth.RoleAdmin.IsAdmin() {
		t.Error("RoleAdmin.IsAdmin() should be true")
	}
	if auth.RoleUser.IsAdmin() {
		t.Error("RoleUser.IsAdmin() should be false")
	}
}

// ── User tests ──

func TestNewUser(t *testing.T) {
	user, err := auth.NewUser("alice", "secure-password-123", auth.RoleUser)
	if err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}

	if user.ID == "" {
		t.Error("ID should not be empty")
	}
	if user.Username != "alice" {
		t.Errorf("Username = %q, want %q", user.Username, "alice")
	}
	if user.PasswordHash == "" {
		t.Error("PasswordHash should not be empty")
	}
	if user.PasswordHash == "secure-password-123" {
		t.Error("PasswordHash should not be the plaintext password")
	}
	if user.Role != auth.RoleUser {
		t.Errorf("Role = %q, want %q", user.Role, auth.RoleUser)
	}
	if user.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if user.Active != true {
		t.Error("Active should be true for new users")
	}
}

func TestNewUser_InvalidRole(t *testing.T) {
	_, err := auth.NewUser("bob", "password", auth.Role("invalid"))
	if err == nil {
		t.Error("NewUser() with invalid role should return error")
	}
}

func TestNewUser_EmptyUsername(t *testing.T) {
	_, err := auth.NewUser("", "password", auth.RoleUser)
	if err == nil {
		t.Error("NewUser() with empty username should return error")
	}
}

func TestNewUser_ShortPassword(t *testing.T) {
	_, err := auth.NewUser("alice", "short", auth.RoleUser)
	if err == nil {
		t.Error("NewUser() with short password should return error")
	}
}

func TestUser_CheckPassword(t *testing.T) {
	user, err := auth.NewUser("alice", "correct-password", auth.RoleUser)
	if err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}

	if !user.CheckPassword("correct-password") {
		t.Error("CheckPassword() should return true for correct password")
	}
	if user.CheckPassword("wrong-password") {
		t.Error("CheckPassword() should return false for wrong password")
	}
	if user.CheckPassword("") {
		t.Error("CheckPassword() should return false for empty password")
	}
}

// ── API Key tests ──

func TestNewAPIKey(t *testing.T) {
	key, rawKey, err := auth.NewAPIKey("user-123", "My CI key")
	if err != nil {
		t.Fatalf("NewAPIKey() error = %v", err)
	}

	if key.ID == "" {
		t.Error("ID should not be empty")
	}
	if key.UserID != "user-123" {
		t.Errorf("UserID = %q, want %q", key.UserID, "user-123")
	}
	if key.Name != "My CI key" {
		t.Errorf("Name = %q, want %q", key.Name, "My CI key")
	}
	if key.KeyHash == "" {
		t.Error("KeyHash should not be empty")
	}
	if key.KeyPrefix == "" {
		t.Error("KeyPrefix should not be empty")
	}
	if key.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if key.Active != true {
		t.Error("Active should be true for new keys")
	}

	// Raw key should start with "sk-"
	if !strings.HasPrefix(rawKey, "sk-") {
		t.Errorf("rawKey should start with 'sk-', got %q", rawKey[:min(10, len(rawKey))])
	}

	// Raw key should NOT be stored in the struct (only the hash).
	if key.KeyHash == rawKey {
		t.Error("KeyHash should not equal the raw key")
	}

	// KeyPrefix should be the first few chars of the raw key.
	if !strings.HasPrefix(rawKey, key.KeyPrefix) {
		t.Errorf("rawKey should start with KeyPrefix %q", key.KeyPrefix)
	}
}

func TestNewAPIKey_EmptyUserID(t *testing.T) {
	_, _, err := auth.NewAPIKey("", "test")
	if err == nil {
		t.Error("NewAPIKey() with empty user ID should return error")
	}
}

func TestAPIKey_MatchesKey(t *testing.T) {
	key, rawKey, err := auth.NewAPIKey("user-1", "test key")
	if err != nil {
		t.Fatalf("NewAPIKey() error = %v", err)
	}

	if !key.MatchesKey(rawKey) {
		t.Error("MatchesKey() should return true for the original raw key")
	}
	if key.MatchesKey("sk-wrong-key") {
		t.Error("MatchesKey() should return false for a wrong key")
	}
	if key.MatchesKey("") {
		t.Error("MatchesKey() should return false for empty key")
	}
}

func TestAPIKey_IsExpired(t *testing.T) {
	key, _, err := auth.NewAPIKey("user-1", "test")
	if err != nil {
		t.Fatalf("NewAPIKey() error = %v", err)
	}

	// No expiry → not expired.
	if key.IsExpired() {
		t.Error("key with no expiry should not be expired")
	}

	// Set expiry in the future → not expired.
	future := time.Now().Add(24 * time.Hour)
	key.ExpiresAt = &future
	if key.IsExpired() {
		t.Error("key with future expiry should not be expired")
	}

	// Set expiry in the past → expired.
	past := time.Now().Add(-1 * time.Hour)
	key.ExpiresAt = &past
	if !key.IsExpired() {
		t.Error("key with past expiry should be expired")
	}
}

// ── Domain errors ──

func TestDomainErrors(t *testing.T) {
	// Just ensure they're distinct and non-nil.
	errors := []error{
		auth.ErrUserNotFound,
		auth.ErrUserExists,
		auth.ErrInvalidCredentials,
		auth.ErrTokenExpired,
		auth.ErrTokenInvalid,
		auth.ErrAPIKeyNotFound,
		auth.ErrAPIKeyRevoked,
		auth.ErrUnauthorized,
	}

	seen := make(map[string]bool)
	for _, err := range errors {
		msg := err.Error()
		if seen[msg] {
			t.Errorf("duplicate error message: %q", msg)
		}
		seen[msg] = true
	}
}

// ── Claims tests ──

func TestClaims_IsExpired(t *testing.T) {
	future := auth.Claims{
		UserID:    "user-1",
		Username:  "alice",
		Role:      auth.RoleUser,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	if future.IsExpired() {
		t.Error("future claims should not be expired")
	}

	past := auth.Claims{
		UserID:    "user-1",
		Username:  "alice",
		Role:      auth.RoleUser,
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}
	if !past.IsExpired() {
		t.Error("past claims should be expired")
	}
}
