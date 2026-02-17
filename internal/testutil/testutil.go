// Package testutil provides shared test helpers for aisync test suites.
// It centralizes common test fixtures like git repositories, SQLite stores,
// and session factories to eliminate duplication across test files.
package testutil

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/internal/storage/sqlite"
)

// InitTestRepo creates a temporary git repository with an initial empty commit.
// The repository is automatically cleaned up when the test completes.
func InitTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	commands := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "commit", "--allow-empty", "-m", "initial"},
	}

	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}

	return dir
}

// MustOpenStore creates a temporary SQLite store that is automatically
// closed when the test completes.
func MustOpenStore(t *testing.T) *sqlite.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("sqlite.New(%q) error = %v", dbPath, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// NewSession creates a domain.Session with the given ID and reasonable defaults.
// All fields are populated for comprehensive testing.
func NewSession(id string) *domain.Session {
	now := time.Date(2026, 2, 17, 10, 0, 0, 0, time.UTC)
	return &domain.Session{
		ID:          domain.SessionID(id),
		Version:     1,
		Provider:    domain.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feature/test",
		CommitSHA:   "abc1234",
		ProjectPath: "/tmp/test-project",
		ExportedBy:  "test",
		ExportedAt:  now,
		CreatedAt:   now,
		Summary:     "Test session " + id,
		StorageMode: domain.StorageModeCompact,
		Messages: []domain.Message{
			{
				ID:        "msg-001",
				Role:      domain.RoleUser,
				Content:   "Hello from " + id,
				Timestamp: now,
			},
			{
				ID:        "msg-002",
				Role:      domain.RoleAssistant,
				Content:   "Response for " + id,
				Model:     "claude-sonnet",
				Timestamp: now,
				Tokens:    100,
			},
		},
		FileChanges: []domain.FileChange{
			{FilePath: "src/main.go", ChangeType: domain.ChangeModified},
		},
		TokenUsage: domain.TokenUsage{
			InputTokens:  100,
			OutputTokens: 200,
			TotalTokens:  300,
		},
		Links: []domain.Link{
			{LinkType: domain.LinkBranch, Ref: "feature/test"},
		},
	}
}
