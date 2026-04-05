package service

import (
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── resolveSessionFromPR tests ──

func TestResolveSessionFromPR_success(t *testing.T) {
	store := newRestoreMockStore()

	// Save a session and link it to PR #42.
	sess := &session.Session{ID: "sess-pr-42", Provider: "claude-code", Branch: "feat/auth"}
	store.sessions[sess.ID] = sess
	store.prSessions[prKey{"org", "repo", 42}] = []session.Summary{
		{ID: "sess-pr-42", Provider: "claude-code", Branch: "feat/auth", CreatedAt: time.Now()},
	}

	cfg := mustTestConfig(t)
	_ = cfg.Set("github.default_owner", "org")
	_ = cfg.Set("github.default_repo", "repo")

	svc := NewSessionService(SessionServiceConfig{Store: store, Config: cfg})

	sid, err := svc.resolveSessionFromPR(42)
	if err != nil {
		t.Fatalf("resolveSessionFromPR() error: %v", err)
	}
	if sid != "sess-pr-42" {
		t.Errorf("session ID = %q, want %q", sid, "sess-pr-42")
	}
}

func TestResolveSessionFromPR_picksNewest(t *testing.T) {
	store := newRestoreMockStore()
	now := time.Now()

	store.prSessions[prKey{"org", "repo", 10}] = []session.Summary{
		{ID: "newer", Provider: "claude-code", CreatedAt: now},
		{ID: "older", Provider: "claude-code", CreatedAt: now.Add(-time.Hour)},
	}

	cfg := mustTestConfig(t)
	_ = cfg.Set("github.default_owner", "org")
	_ = cfg.Set("github.default_repo", "repo")

	svc := NewSessionService(SessionServiceConfig{Store: store, Config: cfg})

	sid, err := svc.resolveSessionFromPR(10)
	if err != nil {
		t.Fatalf("resolveSessionFromPR() error: %v", err)
	}
	// GetSessionsForPR returns ordered by created_at DESC, so first = newest.
	if sid != "newer" {
		t.Errorf("session ID = %q, want %q (most recent)", sid, "newer")
	}
}

func TestResolveSessionFromPR_noConfig(t *testing.T) {
	store := newRestoreMockStore()

	// No config → can't resolve owner/repo.
	svc := NewSessionService(SessionServiceConfig{Store: store})

	_, err := svc.resolveSessionFromPR(42)
	if err == nil {
		t.Fatal("expected error when config is nil")
	}
}

func TestResolveSessionFromPR_noSessions(t *testing.T) {
	store := newRestoreMockStore()

	cfg := mustTestConfig(t)
	_ = cfg.Set("github.default_owner", "org")
	_ = cfg.Set("github.default_repo", "repo")

	svc := NewSessionService(SessionServiceConfig{Store: store, Config: cfg})

	_, err := svc.resolveSessionFromPR(999)
	if err == nil {
		t.Fatal("expected error when no sessions linked to PR")
	}
}

// ── resolveSessionFromFile tests ──

func TestResolveSessionFromFile_success(t *testing.T) {
	store := newRestoreMockStore()

	store.blameEntries = map[string][]session.BlameEntry{
		"src/auth.go": {
			{SessionID: "sess-file-1", Provider: "claude-code", Branch: "main", Summary: "Add auth", CreatedAt: time.Now()},
		},
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})

	sid, err := svc.resolveSessionFromFile("src/auth.go", "/home/user/project")
	if err != nil {
		t.Fatalf("resolveSessionFromFile() error: %v", err)
	}
	if sid != "sess-file-1" {
		t.Errorf("session ID = %q, want %q", sid, "sess-file-1")
	}
}

func TestResolveSessionFromFile_picksNewest(t *testing.T) {
	store := newRestoreMockStore()
	now := time.Now()

	store.blameEntries = map[string][]session.BlameEntry{
		"main.go": {
			{SessionID: "newest", Provider: "claude-code", CreatedAt: now},
			{SessionID: "oldest", Provider: "claude-code", CreatedAt: now.Add(-time.Hour)},
		},
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})

	sid, err := svc.resolveSessionFromFile("main.go", "")
	if err != nil {
		t.Fatalf("resolveSessionFromFile() error: %v", err)
	}
	if sid != "newest" {
		t.Errorf("session ID = %q, want %q (most recent)", sid, "newest")
	}
}

func TestResolveSessionFromFile_noEntries(t *testing.T) {
	store := newRestoreMockStore()

	svc := NewSessionService(SessionServiceConfig{Store: store})

	_, err := svc.resolveSessionFromFile("nonexistent.go", "")
	if err == nil {
		t.Fatal("expected error when no sessions found for file")
	}
}

// ── resolveRepo tests ──

func TestResolveRepo_withConfig(t *testing.T) {
	cfg := mustTestConfig(t)
	_ = cfg.Set("github.default_owner", "my-org")
	_ = cfg.Set("github.default_repo", "my-repo")

	svc := NewSessionService(SessionServiceConfig{
		Store:  &mockStore{sessions: make(map[session.ID]*session.Session)},
		Config: cfg,
	})

	owner, repo := svc.resolveRepo()
	if owner != "my-org" {
		t.Errorf("owner = %q, want %q", owner, "my-org")
	}
	if repo != "my-repo" {
		t.Errorf("repo = %q, want %q", repo, "my-repo")
	}
}

func TestResolveRepo_noConfig(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{
		Store: &mockStore{sessions: make(map[session.ID]*session.Session)},
	})

	owner, repo := svc.resolveRepo()
	if owner != "" || repo != "" {
		t.Errorf("expected empty owner/repo without config, got %q/%q", owner, repo)
	}
}

// ── createWorktree tests ──

func TestCreateWorktree_noGitClient(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{
		Store: &mockStore{sessions: make(map[session.ID]*session.Session)},
	})

	sess := &session.Session{
		ID:        "sess-wt-test",
		CommitSHA: "abc123def456",
	}

	_, err := svc.createWorktree(sess, 0)
	if err == nil {
		t.Fatal("expected error when git client is nil")
	}
}

// ── restoreMockStore ──
// A minimal mock store that supports GetSessionsForPR and GetSessionsByFile
// for testing the restore resolution logic.

type prKey struct {
	owner  string
	repo   string
	number int
}

type restoreMockStore struct {
	mockStore
	prSessions   map[prKey][]session.Summary
	blameEntries map[string][]session.BlameEntry
}

func newRestoreMockStore() *restoreMockStore {
	return &restoreMockStore{
		mockStore:    mockStore{sessions: make(map[session.ID]*session.Session)},
		prSessions:   make(map[prKey][]session.Summary),
		blameEntries: make(map[string][]session.BlameEntry),
	}
}

func (m *restoreMockStore) GetSessionsForPR(owner, repo string, number int) ([]session.Summary, error) {
	key := prKey{owner, repo, number}
	if summaries, ok := m.prSessions[key]; ok {
		return summaries, nil
	}
	return nil, nil
}

func (m *restoreMockStore) GetSessionsByFile(query session.BlameQuery) ([]session.BlameEntry, error) {
	if entries, ok := m.blameEntries[query.FilePath]; ok {
		return entries, nil
	}
	return nil, nil
}

// mustTestConfig creates a Config backed by a temp directory for tests.
func mustTestConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.New(dir, "")
	if err != nil {
		t.Fatalf("config.New() error: %v", err)
	}
	return cfg
}
