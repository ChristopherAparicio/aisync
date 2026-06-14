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
	store.blameEntries = []session.BlameEntry{
		{SessionID: "sess-1", Provider: "claude-code", Branch: "main"},
		{SessionID: "sess-2", Provider: "opencode", Branch: "feat"},
	}
	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.Blame(context.Background(), BlameRequest{
		FilePaths: []string{"a.go", "b.go"},
	})
	if err != nil {
		t.Fatalf("Blame() error: %v", err)
	}
	if len(result.Entries) == 0 {
		t.Error("expected entries for multi-file blame")
	}
	if len(store.lastBlameQuery.FilePaths) != 2 {
		t.Errorf("query.FilePaths = %v, want [a.go b.go]", store.lastBlameQuery.FilePaths)
	}
	if store.lastBlameQuery.FilePaths[0] != "a.go" || store.lastBlameQuery.FilePaths[1] != "b.go" {
		t.Errorf("query.FilePaths = %v, want [a.go b.go]", store.lastBlameQuery.FilePaths)
	}
	if store.lastBlameQuery.FilePath != "" {
		t.Errorf("single FilePath should be empty in multi-file mode, got %q", store.lastBlameQuery.FilePath)
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
