package gitsync

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/internal/storage/sqlite"
)

// initTestRepo creates a temporary git repository for testing.
func initTestRepo(t *testing.T) string {
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

// mustOpenStore creates a temporary SQLite store.
func mustOpenStore(t *testing.T) *sqlite.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("sqlite.New() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testSession(id string) *domain.Session {
	now := time.Date(2026, 2, 17, 10, 0, 0, 0, time.UTC)
	return &domain.Session{
		ID:          domain.SessionID(id),
		Version:     1,
		Provider:    domain.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feature/sync",
		ProjectPath: "/tmp/test-project",
		CreatedAt:   now,
		ExportedAt:  now,
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
				Timestamp: now,
			},
		},
		TokenUsage: domain.TokenUsage{
			InputTokens:  100,
			OutputTokens: 200,
			TotalTokens:  300,
		},
	}
}

func TestPush_emptyStore(t *testing.T) {
	repoDir := initTestRepo(t)
	gitClient := git.NewClient(repoDir)
	store := mustOpenStore(t)
	svc := NewService(gitClient, store)

	result, err := svc.Push(false)
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if result.Pushed != 0 {
		t.Errorf("Pushed = %d, want 0", result.Pushed)
	}
	if result.Remote {
		t.Error("Remote = true, want false")
	}
}

func TestPush_oneSession(t *testing.T) {
	repoDir := initTestRepo(t)
	gitClient := git.NewClient(repoDir)
	store := mustOpenStore(t)
	svc := NewService(gitClient, store)

	// Save a session to the store
	session := testSession("sess-push-1")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	result, err := svc.Push(false)
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if result.Pushed != 1 {
		t.Errorf("Pushed = %d, want 1", result.Pushed)
	}

	// Verify sync branch was created
	if !gitClient.SyncBranchExists() {
		t.Error("sync branch does not exist after push")
	}

	// Verify index
	idx, readErr := svc.ReadIndex()
	if readErr != nil {
		t.Fatalf("ReadIndex() error = %v", readErr)
	}
	if idx == nil {
		t.Fatal("ReadIndex() returned nil")
	}
	if len(idx.Entries) != 1 {
		t.Fatalf("len(Entries) = %d, want 1", len(idx.Entries))
	}
	if idx.Entries[0].ID != session.ID {
		t.Errorf("Entry.ID = %q, want %q", idx.Entries[0].ID, session.ID)
	}
	if idx.Entries[0].MessageCount != 2 {
		t.Errorf("Entry.MessageCount = %d, want 2", idx.Entries[0].MessageCount)
	}
}

func TestPush_appendOnly(t *testing.T) {
	repoDir := initTestRepo(t)
	gitClient := git.NewClient(repoDir)
	store := mustOpenStore(t)
	svc := NewService(gitClient, store)

	// Push first session
	s1 := testSession("sess-append-1")
	if err := store.Save(s1); err != nil {
		t.Fatal(err)
	}
	r1, err := svc.Push(false)
	if err != nil {
		t.Fatalf("Push() #1 error = %v", err)
	}
	if r1.Pushed != 1 {
		t.Errorf("Push #1: Pushed = %d, want 1", r1.Pushed)
	}

	// Add second session and push again
	s2 := testSession("sess-append-2")
	if saveErr := store.Save(s2); saveErr != nil {
		t.Fatal(saveErr)
	}
	r2, err := svc.Push(false)
	if err != nil {
		t.Fatalf("Push() #2 error = %v", err)
	}
	if r2.Pushed != 1 {
		t.Errorf("Push #2: Pushed = %d, want 1 (only new session)", r2.Pushed)
	}

	// Verify index has both entries
	idx, readErr := svc.ReadIndex()
	if readErr != nil {
		t.Fatalf("ReadIndex() error = %v", readErr)
	}
	if len(idx.Entries) != 2 {
		t.Fatalf("len(Entries) = %d, want 2", len(idx.Entries))
	}
}

func TestPush_noRemote(t *testing.T) {
	repoDir := initTestRepo(t)
	gitClient := git.NewClient(repoDir)
	store := mustOpenStore(t)
	svc := NewService(gitClient, store)

	session := testSession("sess-noremote")
	if err := store.Save(session); err != nil {
		t.Fatal(err)
	}

	// Push with pushRemote=true but no remote configured
	result, err := svc.Push(true)
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if result.Pushed != 1 {
		t.Errorf("Pushed = %d, want 1", result.Pushed)
	}
	if result.Remote {
		t.Error("Remote = true, want false (no remote configured)")
	}
}

func TestPull_noBranch(t *testing.T) {
	repoDir := initTestRepo(t)
	gitClient := git.NewClient(repoDir)
	store := mustOpenStore(t)
	svc := NewService(gitClient, store)

	result, err := svc.Pull(false)
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if result.Pulled != 0 {
		t.Errorf("Pulled = %d, want 0", result.Pulled)
	}
}

func TestPushThenPull(t *testing.T) {
	repoDir := initTestRepo(t)
	gitClient := git.NewClient(repoDir)

	// Store A: push sessions
	storeA := mustOpenStore(t)
	svcA := NewService(gitClient, storeA)

	s1 := testSession("sess-roundtrip-1")
	s2 := testSession("sess-roundtrip-2")
	if err := storeA.Save(s1); err != nil {
		t.Fatal(err)
	}
	if err := storeA.Save(s2); err != nil {
		t.Fatal(err)
	}
	pushResult, err := svcA.Push(false)
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if pushResult.Pushed != 2 {
		t.Fatalf("Pushed = %d, want 2", pushResult.Pushed)
	}

	// Store B: pull sessions (simulates a different user)
	storeB := mustOpenStore(t)
	svcB := NewService(gitClient, storeB)

	pullResult, err := svcB.Pull(false)
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if pullResult.Pulled != 2 {
		t.Errorf("Pulled = %d, want 2", pullResult.Pulled)
	}

	// Verify sessions are in store B
	got1, err := storeB.Get(s1.ID)
	if err != nil {
		t.Fatalf("Get(s1) error = %v", err)
	}
	if got1.Summary != s1.Summary {
		t.Errorf("Summary = %q, want %q", got1.Summary, s1.Summary)
	}

	got2, err := storeB.Get(s2.ID)
	if err != nil {
		t.Fatalf("Get(s2) error = %v", err)
	}
	if got2.Summary != s2.Summary {
		t.Errorf("Summary = %q, want %q", got2.Summary, s2.Summary)
	}
}

func TestPull_skipsExisting(t *testing.T) {
	repoDir := initTestRepo(t)
	gitClient := git.NewClient(repoDir)

	// Push a session
	storeA := mustOpenStore(t)
	svcA := NewService(gitClient, storeA)
	session := testSession("sess-existing")
	if err := storeA.Save(session); err != nil {
		t.Fatal(err)
	}
	if _, err := svcA.Push(false); err != nil {
		t.Fatal(err)
	}

	// Pull into store that already has the session
	storeB := mustOpenStore(t)
	if err := storeB.Save(session); err != nil {
		t.Fatal(err)
	}
	svcB := NewService(gitClient, storeB)

	pullResult, err := svcB.Pull(false)
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if pullResult.Pulled != 0 {
		t.Errorf("Pulled = %d, want 0 (session already in store)", pullResult.Pulled)
	}
}

func TestReadIndex_noBranch(t *testing.T) {
	repoDir := initTestRepo(t)
	gitClient := git.NewClient(repoDir)
	store := mustOpenStore(t)
	svc := NewService(gitClient, store)

	idx, err := svc.ReadIndex()
	if err != nil {
		// It's OK if ReadIndex returns an error when no branch exists
		return
	}
	if idx != nil {
		t.Error("ReadIndex() should return nil when no sync branch exists")
	}
}

func TestPush_multipleSessions(t *testing.T) {
	repoDir := initTestRepo(t)
	gitClient := git.NewClient(repoDir)
	store := mustOpenStore(t)
	svc := NewService(gitClient, store)

	// Save multiple sessions
	for i := range 5 {
		session := testSession("sess-multi-" + string(rune('a'+i)))
		if err := store.Save(session); err != nil {
			t.Fatalf("Save() error for session %d: %v", i, err)
		}
	}

	result, err := svc.Push(false)
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if result.Pushed != 5 {
		t.Errorf("Pushed = %d, want 5", result.Pushed)
	}

	idx, readErr := svc.ReadIndex()
	if readErr != nil {
		t.Fatalf("ReadIndex() error = %v", readErr)
	}
	if len(idx.Entries) != 5 {
		t.Errorf("len(Entries) = %d, want 5", len(idx.Entries))
	}
}

func TestPush_idempotent(t *testing.T) {
	repoDir := initTestRepo(t)
	gitClient := git.NewClient(repoDir)
	store := mustOpenStore(t)
	svc := NewService(gitClient, store)

	session := testSession("sess-idempotent")
	if err := store.Save(session); err != nil {
		t.Fatal(err)
	}

	// Push once
	r1, err := svc.Push(false)
	if err != nil {
		t.Fatalf("Push #1 error = %v", err)
	}
	if r1.Pushed != 1 {
		t.Errorf("Push #1: Pushed = %d, want 1", r1.Pushed)
	}

	// Push again — should be no-op
	r2, err := svc.Push(false)
	if err != nil {
		t.Fatalf("Push #2 error = %v", err)
	}
	if r2.Pushed != 0 {
		t.Errorf("Push #2: Pushed = %d, want 0 (already synced)", r2.Pushed)
	}
}

func TestIndex_sortedByCreatedAtDescending(t *testing.T) {
	repoDir := initTestRepo(t)
	gitClient := git.NewClient(repoDir)
	store := mustOpenStore(t)
	svc := NewService(gitClient, store)

	base := time.Date(2026, 2, 17, 10, 0, 0, 0, time.UTC)

	// Save sessions with different timestamps
	for i := range 3 {
		s := testSession("sess-sort-" + string(rune('a'+i)))
		s.CreatedAt = base.Add(time.Duration(i) * time.Hour)
		if err := store.Save(s); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := svc.Push(false); err != nil {
		t.Fatal(err)
	}

	idx, err := svc.ReadIndex()
	if err != nil {
		t.Fatal(err)
	}

	// Should be sorted descending by created_at (newest first)
	for i := 1; i < len(idx.Entries); i++ {
		if idx.Entries[i].CreatedAt.After(idx.Entries[i-1].CreatedAt) {
			t.Errorf("index not sorted: entry[%d].CreatedAt (%v) > entry[%d].CreatedAt (%v)",
				i, idx.Entries[i].CreatedAt, i-1, idx.Entries[i-1].CreatedAt)
		}
	}
}
