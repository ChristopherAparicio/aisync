package restore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestRestore_byBranch(t *testing.T) {
	sess := &session.Session{
		ID:          "ses-1",
		Provider:    session.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "main",
		ProjectPath: "/test/project",
		Summary:     "Test session",
	}

	store := &mockStore{sessionByBranch: sess}
	reg := provider.NewRegistry(&mockProvider{name: session.ProviderClaudeCode, canImportVal: true})
	svc := NewService(reg, store)

	projectDir := t.TempDir()
	result, err := svc.Restore(Request{
		ProjectPath: projectDir,
		Branch:      "main",
	})
	if err != nil {
		t.Fatalf("Restore() error: %v", err)
	}

	if result.Session.ID != "ses-1" {
		t.Errorf("Session.ID = %q, want %q", result.Session.ID, "ses-1")
	}
	// Provider.Import returns ErrImportNotSupported, so falls back to context
	if result.Method != "context" {
		t.Errorf("Method = %q, want %q", result.Method, "context")
	}
}

func TestRestore_bySessionID(t *testing.T) {
	sess := &session.Session{
		ID:          "ses-1",
		Provider:    session.ProviderClaudeCode,
		Agent:       "claude",
		ProjectPath: "/test/project",
		Summary:     "Test session",
	}

	store := &mockStore{sessionByID: sess}
	reg := provider.NewRegistry(&mockProvider{name: session.ProviderClaudeCode, canImportVal: true})
	svc := NewService(reg, store)

	projectDir := t.TempDir()
	result, err := svc.Restore(Request{
		ProjectPath: projectDir,
		SessionID:   "ses-1",
	})
	if err != nil {
		t.Fatalf("Restore() error: %v", err)
	}

	if result.Session.ID != "ses-1" {
		t.Errorf("Session.ID = %q, want %q", result.Session.ID, "ses-1")
	}
}

func TestRestore_asContext(t *testing.T) {
	sess := &session.Session{
		ID:          "ses-1",
		Provider:    session.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feat/auth",
		ProjectPath: "/test/project",
		Summary:     "Implemented OAuth2",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "Add OAuth2"},
			{ID: "m2", Role: session.RoleAssistant, Content: "Done!"},
		},
		FileChanges: []session.FileChange{
			{FilePath: "auth.go", ChangeType: session.ChangeCreated},
		},
	}

	store := &mockStore{sessionByBranch: sess}
	reg := provider.NewRegistry()
	svc := NewService(reg, store)

	projectDir := t.TempDir()
	result, err := svc.Restore(Request{
		ProjectPath: projectDir,
		Branch:      "feat/auth",
		AsContext:   true,
	})
	if err != nil {
		t.Fatalf("Restore() error: %v", err)
	}

	if result.Method != "context" {
		t.Errorf("Method = %q, want %q", result.Method, "context")
	}

	// Verify CONTEXT.md was created
	contextPath := filepath.Join(projectDir, "CONTEXT.md")
	data, readErr := os.ReadFile(contextPath)
	if readErr != nil {
		t.Fatalf("CONTEXT.md not created: %v", readErr)
	}

	content := string(data)
	if !strings.Contains(content, "OAuth2") {
		t.Error("CONTEXT.md should contain summary")
	}
	if !strings.Contains(content, "auth.go") {
		t.Error("CONTEXT.md should contain file changes")
	}
	if !strings.Contains(content, "Add OAuth2") {
		t.Error("CONTEXT.md should contain messages")
	}
}

func TestRestore_sessionNotFound(t *testing.T) {
	store := &mockStore{} // no sessions
	reg := provider.NewRegistry()
	svc := NewService(reg, store)

	_, err := svc.Restore(Request{
		ProjectPath: "/test/project",
		Branch:      "main",
	})
	if err == nil {
		t.Error("Restore() should return error when session not found")
	}
}

func TestRestore_crossProvider(t *testing.T) {
	sess := &session.Session{
		ID:          "ses-cross",
		Provider:    session.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feat/cross",
		ProjectPath: "/test/project",
		Summary:     "Cross-provider test",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "Hello"},
			{ID: "m2", Role: session.RoleAssistant, Content: "Hi!"},
		},
	}

	store := &mockStore{sessionByBranch: sess}
	importingProvider := &mockImportProvider{name: session.ProviderOpenCode}
	reg := provider.NewRegistry(importingProvider)
	conv := converter.New()
	svc := NewServiceWithConverter(reg, store, conv)

	projectDir := t.TempDir()
	result, err := svc.Restore(Request{
		ProjectPath:  projectDir,
		Branch:       "feat/cross",
		ProviderName: session.ProviderOpenCode, // different from source (claude-code)
	})
	if err != nil {
		t.Fatalf("Restore() error: %v", err)
	}

	if result.Method != "converted" {
		t.Errorf("Method = %q, want converted", result.Method)
	}

	// Verify the provider received the import call
	if !importingProvider.importCalled {
		t.Error("expected Import() to be called on target provider")
	}
}

func TestRestore_crossProviderFallbackToContext(t *testing.T) {
	sess := &session.Session{
		ID:          "ses-fallback",
		Provider:    session.ProviderCursor, // cursor source
		Agent:       "cursor-agent",
		Branch:      "feat/cursor",
		ProjectPath: "/test/project",
		Summary:     "Cursor session",
	}

	store := &mockStore{sessionByBranch: sess}
	noImportProvider := &mockProvider{name: session.ProviderCursor}
	noImportProvider.canImportVal = false
	reg := provider.NewRegistry(noImportProvider)
	conv := converter.New()
	svc := NewServiceWithConverter(reg, store, conv)

	projectDir := t.TempDir()
	result, err := svc.Restore(Request{
		ProjectPath: projectDir,
		Branch:      "feat/cursor",
	})
	if err != nil {
		t.Fatalf("Restore() error: %v", err)
	}

	if result.Method != "context" {
		t.Errorf("Method = %q, want context (CanImport=false)", result.Method)
	}
}

func TestRestore_agentOverride(t *testing.T) {
	sess := &session.Session{
		ID:          "ses-agent",
		Provider:    session.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feat/agent",
		ProjectPath: "/test/project",
		Summary:     "Agent test",
	}

	store := &mockStore{sessionByBranch: sess}
	reg := provider.NewRegistry()
	svc := NewService(reg, store)

	projectDir := t.TempDir()
	result, err := svc.Restore(Request{
		ProjectPath: projectDir,
		Branch:      "feat/agent",
		Agent:       "custom-agent",
		AsContext:   true,
	})
	if err != nil {
		t.Fatalf("Restore() error: %v", err)
	}

	// The agent should have been overridden
	if result.Session.Agent != "custom-agent" {
		t.Errorf("Session.Agent = %q, want custom-agent", result.Session.Agent)
	}
}

func TestRestore_sameProviderNoConversion(t *testing.T) {
	sess := &session.Session{
		ID:          "ses-same",
		Provider:    session.ProviderOpenCode,
		Agent:       "opencode",
		Branch:      "feat/same",
		ProjectPath: "/test/project",
		Summary:     "Same provider test",
	}

	store := &mockStore{sessionByBranch: sess}
	importingProvider := &mockImportProvider{name: session.ProviderOpenCode}
	reg := provider.NewRegistry(importingProvider)
	conv := converter.New()
	svc := NewServiceWithConverter(reg, store, conv)

	projectDir := t.TempDir()
	result, err := svc.Restore(Request{
		ProjectPath:  projectDir,
		Branch:       "feat/same",
		ProviderName: session.ProviderOpenCode, // same as source
	})
	if err != nil {
		t.Fatalf("Restore() error: %v", err)
	}

	// Same provider → direct import, no conversion
	if result.Method != "native" {
		t.Errorf("Method = %q, want native (same provider)", result.Method)
	}
}

// --- Mocks ---

type mockStore struct {
	sessionByID     *session.Session
	sessionByBranch *session.Session
}

func (m *mockStore) Save(_ *session.Session) error { return nil }
func (m *mockStore) Get(_ session.ID) (*session.Session, error) {
	if m.sessionByID != nil {
		return m.sessionByID, nil
	}
	return nil, session.ErrSessionNotFound
}
func (m *mockStore) GetByBranch(_ string, _ string) (*session.Session, error) {
	if m.sessionByBranch != nil {
		return m.sessionByBranch, nil
	}
	return nil, session.ErrSessionNotFound
}
func (m *mockStore) List(_ session.ListOptions) ([]session.Summary, error) {
	return nil, nil
}
func (m *mockStore) Delete(_ session.ID) error { return nil }
func (m *mockStore) AddLink(_ session.ID, _ session.Link) error {
	return nil
}
func (m *mockStore) GetByLink(_ session.LinkType, _ string) ([]session.Summary, error) {
	return nil, session.ErrSessionNotFound
}
func (m *mockStore) Close() error                                   { return nil }
func (m *mockStore) SaveUser(_ *session.User) error                 { return nil }
func (m *mockStore) GetUser(_ session.ID) (*session.User, error)    { return nil, nil }
func (m *mockStore) GetUserByEmail(_ string) (*session.User, error) { return nil, nil }
func (m *mockStore) Search(_ session.SearchQuery) (*session.SearchResult, error) {
	return &session.SearchResult{}, nil
}

type mockProvider struct {
	name         session.ProviderName
	canImportVal bool
}

func (m *mockProvider) Name() session.ProviderName { return m.name }
func (m *mockProvider) Detect(_ string, _ string) ([]session.Summary, error) {
	return nil, nil
}
func (m *mockProvider) Export(_ session.ID, _ session.StorageMode) (*session.Session, error) {
	return nil, nil
}
func (m *mockProvider) CanImport() bool                 { return m.canImportVal }
func (m *mockProvider) Import(_ *session.Session) error { return session.ErrImportNotSupported }

// mockImportProvider successfully imports sessions.
type mockImportProvider struct {
	name         session.ProviderName
	importCalled bool
}

func (m *mockImportProvider) Name() session.ProviderName { return m.name }
func (m *mockImportProvider) Detect(_ string, _ string) ([]session.Summary, error) {
	return nil, nil
}
func (m *mockImportProvider) Export(_ session.ID, _ session.StorageMode) (*session.Session, error) {
	return nil, nil
}
func (m *mockImportProvider) CanImport() bool { return true }
func (m *mockImportProvider) Import(_ *session.Session) error {
	m.importCalled = true
	return nil
}
