package auth_test

import (
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/auth"
)

func TestTokenFile_SaveLoadClear(t *testing.T) {
	dir := t.TempDir()

	// Initially no token.
	if got := auth.LoadToken(dir); got != "" {
		t.Errorf("LoadToken() = %q, want empty", got)
	}

	// Save a token.
	if err := auth.SaveToken(dir, "my-jwt-token-123"); err != nil {
		t.Fatalf("SaveToken() error = %v", err)
	}

	// Load it back.
	if got := auth.LoadToken(dir); got != "my-jwt-token-123" {
		t.Errorf("LoadToken() = %q, want %q", got, "my-jwt-token-123")
	}

	// Overwrite.
	if err := auth.SaveToken(dir, "new-token"); err != nil {
		t.Fatalf("SaveToken() error = %v", err)
	}
	if got := auth.LoadToken(dir); got != "new-token" {
		t.Errorf("LoadToken() after overwrite = %q, want %q", got, "new-token")
	}

	// Clear.
	if err := auth.ClearToken(dir); err != nil {
		t.Fatalf("ClearToken() error = %v", err)
	}
	if got := auth.LoadToken(dir); got != "" {
		t.Errorf("LoadToken() after clear = %q, want empty", got)
	}

	// Clear again (idempotent).
	if err := auth.ClearToken(dir); err != nil {
		t.Fatalf("ClearToken() twice error = %v", err)
	}
}
