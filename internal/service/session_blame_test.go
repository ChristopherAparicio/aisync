package service

import (
	"context"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// blameMockStore extends the local mockStore with configurable blame/project returns
// and records the last call arguments for assertion.
type blameMockStore struct {
	mockStore
	blameEntries       []session.BlameEntry
	projectFileEntries []session.ProjectFileEntry
	lastBlameQuery     session.BlameQuery
	lastProjectPath    string
}

func (m *blameMockStore) GetSessionsByFile(q session.BlameQuery) ([]session.BlameEntry, error) {
	m.lastBlameQuery = q
	return m.blameEntries, nil
}

func (m *blameMockStore) FilesForProject(projectPath string, _ string, _ int) ([]session.ProjectFileEntry, error) {
	m.lastProjectPath = projectPath
	return m.projectFileEntries, nil
}

func newBlameMockStore() *blameMockStore {
	return &blameMockStore{
		mockStore: mockStore{sessions: make(map[session.ID]*session.Session)},
	}
}

func TestBlame_SingleFile(t *testing.T) {
	store := newBlameMockStore()
	store.blameEntries = []session.BlameEntry{
		{SessionID: "sess-1", Provider: "claude-code", Branch: "main"},
	}
	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.Blame(context.Background(), BlameRequest{FilePath: "a.go"})
	if err != nil {
		t.Fatalf("Blame() error: %v", err)
	}
	if len(result.Entries) == 0 {
		t.Error("expected entries, got none")
	}
	if result.FilePath != "a.go" {
		t.Errorf("FilePath = %q, want %q", result.FilePath, "a.go")
	}
}

func TestBlame_MultiFile(t *testing.T) {
	store := newBlameMockStore()
	// Entries ordered created_at DESC as the real store returns them.
	store.blameEntries = []session.BlameEntry{
		{SessionID: "sess-a2", Provider: "claude-code", Agent: "jarvis", Branch: "main", FilePath: "a.go"},
		{SessionID: "sess-a1", Provider: "opencode", Agent: "", Branch: "main", FilePath: "a.go"},
		{SessionID: "sess-b1", Provider: "claude-code", Agent: "vega", Branch: "feat", FilePath: "b.go"},
	}
	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.Blame(context.Background(), BlameRequest{
		FilePaths: []string{"a.go", "b.go", "c.go"}, // c.go: untouched
	})
	if err != nil {
		t.Fatalf("Blame() error: %v", err)
	}

	if len(result.Entries) != 0 {
		t.Errorf("multi-file should populate FilesGrouped, not Entries; got %d entries", len(result.Entries))
	}
	if len(result.FilesGrouped) != 3 {
		t.Fatalf("expected 3 file groups (a,b,c), got %d", len(result.FilesGrouped))
	}
	if result.FilesGrouped[0].File != "a.go" || result.FilesGrouped[1].File != "b.go" || result.FilesGrouped[2].File != "c.go" {
		t.Errorf("file order not preserved: got %q,%q,%q",
			result.FilesGrouped[0].File, result.FilesGrouped[1].File, result.FilesGrouped[2].File)
	}
	// Default (All=false): each file keeps only its most recent session.
	if len(result.FilesGrouped[0].Sessions) != 1 {
		t.Fatalf("a.go should have 1 session (most recent) by default, got %d", len(result.FilesGrouped[0].Sessions))
	}
	if result.FilesGrouped[0].Sessions[0].SessionID != "sess-a2" {
		t.Errorf("a.go most-recent session = %q, want sess-a2", result.FilesGrouped[0].Sessions[0].SessionID)
	}
	if result.FilesGrouped[0].Sessions[0].Agent != "jarvis" {
		t.Errorf("agent should be preserved on grouped entry, got %q", result.FilesGrouped[0].Sessions[0].Agent)
	}
	if len(result.FilesGrouped[2].Sessions) != 0 {
		t.Errorf("c.go (untouched) should have 0 sessions, got %d", len(result.FilesGrouped[2].Sessions))
	}

	if len(store.lastBlameQuery.FilePaths) != 3 {
		t.Errorf("query.FilePaths = %v, want [a.go b.go c.go]", store.lastBlameQuery.FilePaths)
	}
	if store.lastBlameQuery.FilePath != "" {
		t.Errorf("single FilePath should be empty in multi-file mode, got %q", store.lastBlameQuery.FilePath)
	}
}

func TestBlame_MultiFileAll(t *testing.T) {
	store := newBlameMockStore()
	store.blameEntries = []session.BlameEntry{
		{SessionID: "sess-a2", Provider: "claude-code", Agent: "jarvis", Branch: "main", FilePath: "a.go"},
		{SessionID: "sess-a1", Provider: "opencode", Agent: "", Branch: "main", FilePath: "a.go"},
		{SessionID: "sess-b1", Provider: "claude-code", Agent: "vega", Branch: "feat", FilePath: "b.go"},
	}
	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.Blame(context.Background(), BlameRequest{
		FilePaths: []string{"a.go", "b.go"},
		All:       true,
	})
	if err != nil {
		t.Fatalf("Blame() error: %v", err)
	}
	if len(result.FilesGrouped) != 2 {
		t.Fatalf("expected 2 file groups, got %d", len(result.FilesGrouped))
	}
	// With All=true, a.go keeps both sessions in DESC order.
	if len(result.FilesGrouped[0].Sessions) != 2 {
		t.Fatalf("a.go with All=true should have 2 sessions, got %d", len(result.FilesGrouped[0].Sessions))
	}
	if result.FilesGrouped[0].Sessions[0].SessionID != "sess-a2" || result.FilesGrouped[0].Sessions[1].SessionID != "sess-a1" {
		t.Errorf("a.go sessions not in DESC order: %q,%q",
			result.FilesGrouped[0].Sessions[0].SessionID, result.FilesGrouped[0].Sessions[1].SessionID)
	}
}

func TestBlame_Project(t *testing.T) {
	store := newBlameMockStore()
	store.projectFileEntries = []session.ProjectFileEntry{
		{FilePath: "main.go", SessionCount: 3, WriteCount: 1},
		{FilePath: "auth.go", SessionCount: 5, WriteCount: 2},
	}
	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.Blame(context.Background(), BlameRequest{ProjectPath: "/proj"})
	if err != nil {
		t.Fatalf("Blame() error: %v", err)
	}
	if len(result.ProjectFiles) == 0 {
		t.Error("expected project files, got none")
	}
	if len(result.ProjectFiles) != 2 {
		t.Errorf("ProjectFiles count = %d, want 2", len(result.ProjectFiles))
	}
	if store.lastProjectPath != "/proj" {
		t.Errorf("lastProjectPath = %q, want /proj", store.lastProjectPath)
	}
	if len(result.Entries) != 0 {
		t.Errorf("Entries should be nil in project mode, got %d", len(result.Entries))
	}
}

func TestBlame_Validation(t *testing.T) {
	store := newBlameMockStore()
	svc := NewSessionService(SessionServiceConfig{Store: store})

	_, err := svc.Blame(context.Background(), BlameRequest{})
	if err == nil {
		t.Fatal("expected error for empty BlameRequest, got nil")
	}
}
