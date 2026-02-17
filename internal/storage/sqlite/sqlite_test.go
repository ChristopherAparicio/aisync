package sqlite

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/domain"
)

func mustOpenStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New(%q) error = %v", dbPath, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testSession(id string) *domain.Session {
	now := time.Date(2026, 2, 16, 14, 0, 0, 0, time.UTC)
	return &domain.Session{
		ID:          domain.SessionID(id),
		Version:     1,
		Provider:    domain.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feature/auth",
		CommitSHA:   "abc1234",
		ProjectPath: "/home/chris/my-app",
		ExportedBy:  "Christopher",
		ExportedAt:  now,
		CreatedAt:   now,
		Summary:     "Implement OAuth2",
		StorageMode: domain.StorageModeCompact,
		Messages: []domain.Message{
			{
				ID:        "msg-001",
				Role:      domain.RoleUser,
				Content:   "Implement OAuth2",
				Timestamp: now,
			},
		},
		FileChanges: []domain.FileChange{
			{FilePath: "src/auth.py", ChangeType: domain.ChangeCreated},
		},
		TokenUsage: domain.TokenUsage{
			InputTokens:  1000,
			OutputTokens: 500,
			TotalTokens:  1500,
		},
		Links: []domain.Link{
			{LinkType: domain.LinkBranch, Ref: "feature/auth"},
		},
	}
}

func TestSaveAndGet(t *testing.T) {
	store := mustOpenStore(t)
	session := testSession("sess-1")

	// Save
	if err := store.Save(session); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Get
	got, err := store.Get(domain.SessionID("sess-1"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if got.ID != session.ID {
		t.Errorf("ID = %q, want %q", got.ID, session.ID)
	}
	if got.Provider != session.Provider {
		t.Errorf("Provider = %q, want %q", got.Provider, session.Provider)
	}
	if got.Agent != "claude" {
		t.Errorf("Agent = %q, want %q", got.Agent, "claude")
	}
	if got.Branch != "feature/auth" {
		t.Errorf("Branch = %q, want %q", got.Branch, "feature/auth")
	}
	if got.Summary != "Implement OAuth2" {
		t.Errorf("Summary = %q, want %q", got.Summary, "Implement OAuth2")
	}
	if len(got.Messages) != 1 {
		t.Fatalf("Messages count = %d, want 1", len(got.Messages))
	}
	if got.Messages[0].Role != domain.RoleUser {
		t.Errorf("Messages[0].Role = %q, want %q", got.Messages[0].Role, domain.RoleUser)
	}
	if len(got.FileChanges) != 1 {
		t.Fatalf("FileChanges count = %d, want 1", len(got.FileChanges))
	}
	if got.TokenUsage.TotalTokens != 1500 {
		t.Errorf("TotalTokens = %d, want 1500", got.TokenUsage.TotalTokens)
	}
}

func TestGet_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	_, err := store.Get(domain.SessionID("nonexistent"))
	if !errors.Is(err, domain.ErrSessionNotFound) {
		t.Errorf("Get(nonexistent) error = %v, want ErrSessionNotFound", err)
	}
}

func TestSave_Upsert(t *testing.T) {
	store := mustOpenStore(t)
	session := testSession("sess-1")

	// Save initial
	if err := store.Save(session); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Update summary and save again
	session.Summary = "Updated summary"
	if err := store.Save(session); err != nil {
		t.Fatalf("Save() upsert error = %v", err)
	}

	got, err := store.Get(domain.SessionID("sess-1"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Summary != "Updated summary" {
		t.Errorf("Summary = %q, want %q", got.Summary, "Updated summary")
	}
}

func TestGetByBranch(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("sess-1")
	s1.Branch = "feature/auth"
	s1.ProjectPath = "/project"

	s2 := testSession("sess-2")
	s2.Branch = "feature/other"
	s2.ProjectPath = "/project"

	if err := store.Save(s1); err != nil {
		t.Fatalf("Save(s1) error = %v", err)
	}
	if err := store.Save(s2); err != nil {
		t.Fatalf("Save(s2) error = %v", err)
	}

	got, err := store.GetByBranch("/project", "feature/auth")
	if err != nil {
		t.Fatalf("GetByBranch() error = %v", err)
	}
	if got.ID != domain.SessionID("sess-1") {
		t.Errorf("ID = %q, want %q", got.ID, "sess-1")
	}
}

func TestGetByBranch_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	_, err := store.GetByBranch("/project", "nonexistent")
	if !errors.Is(err, domain.ErrSessionNotFound) {
		t.Errorf("GetByBranch(nonexistent) error = %v, want ErrSessionNotFound", err)
	}
}

func TestList(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("sess-1")
	s1.Branch = "feature/auth"
	s1.ProjectPath = "/project"

	s2 := testSession("sess-2")
	s2.Branch = "feature/auth"
	s2.ProjectPath = "/project"
	s2.Provider = domain.ProviderOpenCode
	s2.Agent = "coder"

	s3 := testSession("sess-3")
	s3.Branch = "main"
	s3.ProjectPath = "/project"

	for _, s := range []*domain.Session{s1, s2, s3} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	t.Run("list by branch", func(t *testing.T) {
		summaries, err := store.List(domain.ListOptions{
			ProjectPath: "/project",
			Branch:      "feature/auth",
		})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(summaries) != 2 {
			t.Errorf("List(branch=feature/auth) count = %d, want 2", len(summaries))
		}
	})

	t.Run("list all", func(t *testing.T) {
		summaries, err := store.List(domain.ListOptions{
			ProjectPath: "/project",
			All:         true,
		})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(summaries) != 3 {
			t.Errorf("List(all) count = %d, want 3", len(summaries))
		}
	})
}

func TestDelete(t *testing.T) {
	store := mustOpenStore(t)
	session := testSession("sess-1")

	if err := store.Save(session); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Delete(domain.SessionID("sess-1")); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, err := store.Get(domain.SessionID("sess-1"))
	if !errors.Is(err, domain.ErrSessionNotFound) {
		t.Errorf("Get after Delete: error = %v, want ErrSessionNotFound", err)
	}
}

func TestDelete_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	err := store.Delete(domain.SessionID("nonexistent"))
	if !errors.Is(err, domain.ErrSessionNotFound) {
		t.Errorf("Delete(nonexistent) error = %v, want ErrSessionNotFound", err)
	}
}

func TestAddLink(t *testing.T) {
	store := mustOpenStore(t)
	session := testSession("sess-1")

	if err := store.Save(session); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	link := domain.Link{LinkType: domain.LinkCommit, Ref: "def5678"}
	if err := store.AddLink(domain.SessionID("sess-1"), link); err != nil {
		t.Fatalf("AddLink() error = %v", err)
	}

	got, err := store.Get(domain.SessionID("sess-1"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	// Should have original link + new one
	if len(got.Links) != 2 {
		t.Fatalf("Links count = %d, want 2", len(got.Links))
	}

	found := false
	for _, l := range got.Links {
		if l.LinkType == domain.LinkCommit && l.Ref == "def5678" {
			found = true
			break
		}
	}
	if !found {
		t.Error("AddLink: commit link not found after adding")
	}
}
