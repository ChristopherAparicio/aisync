package auth_test

import (
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/auth"
)

func TestJWTManager_IssueAndValidate(t *testing.T) {
	mgr := auth.NewJWTManager("test-secret-key-at-least-32-bytes!", 1*time.Hour)

	user := &auth.User{
		ID:       "user-123",
		Username: "alice",
		Role:     auth.RoleUser,
	}

	token, err := mgr.Issue(user)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if token == "" {
		t.Fatal("Issue() returned empty token")
	}

	claims, err := mgr.Validate(token)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if claims.UserID != "user-123" {
		t.Errorf("UserID = %q, want %q", claims.UserID, "user-123")
	}
	if claims.Username != "alice" {
		t.Errorf("Username = %q, want %q", claims.Username, "alice")
	}
	if claims.Role != auth.RoleUser {
		t.Errorf("Role = %q, want %q", claims.Role, auth.RoleUser)
	}
	if claims.IsExpired() {
		t.Error("freshly issued token should not be expired")
	}
}

func TestJWTManager_ExpiredToken(t *testing.T) {
	// Issue with a very short TTL.
	mgr := auth.NewJWTManager("test-secret-key-at-least-32-bytes!", -1*time.Hour)

	user := &auth.User{
		ID:       "user-1",
		Username: "bob",
		Role:     auth.RoleAdmin,
	}

	token, err := mgr.Issue(user)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	_, err = mgr.Validate(token)
	if err == nil {
		t.Error("Validate() should return error for expired token")
	}
}

func TestJWTManager_InvalidToken(t *testing.T) {
	mgr := auth.NewJWTManager("test-secret-key-at-least-32-bytes!", 1*time.Hour)

	_, err := mgr.Validate("not-a-valid-jwt")
	if err == nil {
		t.Error("Validate() should return error for invalid token")
	}
}

func TestJWTManager_WrongSecret(t *testing.T) {
	mgr1 := auth.NewJWTManager("secret-one-at-least-32-bytes-long!", 1*time.Hour)
	mgr2 := auth.NewJWTManager("secret-two-at-least-32-bytes-long!", 1*time.Hour)

	user := &auth.User{
		ID:       "user-1",
		Username: "alice",
		Role:     auth.RoleUser,
	}

	token, err := mgr1.Issue(user)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	_, err = mgr2.Validate(token)
	if err == nil {
		t.Error("Validate() should fail when signed with a different secret")
	}
}

func TestJWTManager_AdminRole(t *testing.T) {
	mgr := auth.NewJWTManager("test-secret-key-at-least-32-bytes!", 1*time.Hour)

	user := &auth.User{
		ID:       "admin-1",
		Username: "superadmin",
		Role:     auth.RoleAdmin,
	}

	token, err := mgr.Issue(user)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	claims, err := mgr.Validate(token)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if claims.Role != auth.RoleAdmin {
		t.Errorf("Role = %q, want %q", claims.Role, auth.RoleAdmin)
	}
	if !claims.Role.IsAdmin() {
		t.Error("admin claims should have IsAdmin() == true")
	}
}
