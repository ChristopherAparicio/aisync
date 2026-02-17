package restore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/internal/provider"
)

func TestRestore_byBranch(t *testing.T) {
	session := &domain.Session{
		ID:          "ses-1",
		Provider:    domain.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "main",
		ProjectPath: "/test/project",
		Summary:     "Test session",
	}

	store := &mockStore{sessionByBranch: session}
	reg := provider.NewRegistry(&mockProvider{name: domain.ProviderClaudeCode, canImportVal: true})
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
	session := &domain.Session{
		ID:          "ses-1",
		Provider:    domain.ProviderClaudeCode,
		Agent:       "claude",
		ProjectPath: "/test/project",
		Summary:     "Test session",
	}

	store := &mockStore{sessionByID: session}
	reg := provider.NewRegistry(&mockProvider{name: domain.ProviderClaudeCode, canImportVal: true})
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
	session := &domain.Session{
		ID:          "ses-1",
		Provider:    domain.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feat/auth",
		ProjectPath: "/test/project",
		Summary:     "Implemented OAuth2",
		Messages: []domain.Message{
			{ID: "m1", Role: domain.RoleUser, Content: "Add OAuth2"},
			{ID: "m2", Role: domain.RoleAssistant, Content: "Done!"},
		},
		FileChanges: []domain.FileChange{
			{FilePath: "auth.go", ChangeType: domain.ChangeCreated},
		},
	}

	store := &mockStore{sessionByBranch: session}
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
	session := &domain.Session{
		ID:          "ses-cross",
		Provider:    domain.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feat/cross",
		ProjectPath: "/test/project",
		Summary:     "Cross-provider test",
		Messages: []domain.Message{
			{ID: "m1", Role: domain.RoleUser, Content: "Hello"},
			{ID: "m2", Role: domain.RoleAssistant, Content: "Hi!"},
		},
	}

	store := &mockStore{sessionByBranch: session}
	importingProvider := &mockImportProvider{name: domain.ProviderOpenCode}
	reg := provider.NewRegistry(importingProvider)
	conv := &mockConverter{}
	svc := NewServiceWithConverter(reg, store, conv)

	projectDir := t.TempDir()
	result, err := svc.Restore(Request{
		ProjectPath:  projectDir,
		Branch:       "feat/cross",
		ProviderName: domain.ProviderOpenCode, // different from source (claude-code)
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
	session := &domain.Session{
		ID:          "ses-fallback",
		Provider:    domain.ProviderCursor, // cursor source
		Agent:       "cursor-agent",
		Branch:      "feat/cursor",
		ProjectPath: "/test/project",
		Summary:     "Cursor session",
	}

	store := &mockStore{sessionByBranch: session}
	noImportProvider := &mockProvider{name: domain.ProviderCursor}
	noImportProvider.canImportVal = false
	reg := provider.NewRegistry(noImportProvider)
	conv := &mockConverter{}
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
	session := &domain.Session{
		ID:          "ses-agent",
		Provider:    domain.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feat/agent",
		ProjectPath: "/test/project",
		Summary:     "Agent test",
	}

	store := &mockStore{sessionByBranch: session}
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
	session := &domain.Session{
		ID:          "ses-same",
		Provider:    domain.ProviderOpenCode,
		Agent:       "opencode",
		Branch:      "feat/same",
		ProjectPath: "/test/project",
		Summary:     "Same provider test",
	}

	store := &mockStore{sessionByBranch: session}
	importingProvider := &mockImportProvider{name: domain.ProviderOpenCode}
	reg := provider.NewRegistry(importingProvider)
	conv := &mockConverter{}
	svc := NewServiceWithConverter(reg, store, conv)

	projectDir := t.TempDir()
	result, err := svc.Restore(Request{
		ProjectPath:  projectDir,
		Branch:       "feat/same",
		ProviderName: domain.ProviderOpenCode, // same as source
	})
	if err != nil {
		t.Fatalf("Restore() error: %v", err)
	}

	// Same provider → direct import, no conversion
	if result.Method != "native" {
		t.Errorf("Method = %q, want native (same provider)", result.Method)
	}

	// Converter should NOT have been called
	if conv.toNativeCalled {
		t.Error("converter should not be called for same-provider restore")
	}
}

// --- Mocks ---

type mockStore struct {
	sessionByID     *domain.Session
	sessionByBranch *domain.Session
}

func (m *mockStore) Save(_ *domain.Session) error { return nil }
func (m *mockStore) Get(_ domain.SessionID) (*domain.Session, error) {
	if m.sessionByID != nil {
		return m.sessionByID, nil
	}
	return nil, domain.ErrSessionNotFound
}
func (m *mockStore) GetByBranch(_ string, _ string) (*domain.Session, error) {
	if m.sessionByBranch != nil {
		return m.sessionByBranch, nil
	}
	return nil, domain.ErrSessionNotFound
}
func (m *mockStore) List(_ domain.ListOptions) ([]domain.SessionSummary, error) {
	return nil, nil
}
func (m *mockStore) Delete(_ domain.SessionID) error { return nil }
func (m *mockStore) AddLink(_ domain.SessionID, _ domain.Link) error {
	return nil
}
func (m *mockStore) GetByLink(_ domain.LinkType, _ string) ([]domain.SessionSummary, error) {
	return nil, domain.ErrSessionNotFound
}
func (m *mockStore) Close() error { return nil }

type mockProvider struct {
	name         domain.ProviderName
	canImportVal bool
}

func (m *mockProvider) Name() domain.ProviderName { return m.name }
func (m *mockProvider) Detect(_ string, _ string) ([]domain.SessionSummary, error) {
	return nil, nil
}
func (m *mockProvider) Export(_ domain.SessionID, _ domain.StorageMode) (*domain.Session, error) {
	return nil, nil
}
func (m *mockProvider) CanImport() bool                { return m.canImportVal }
func (m *mockProvider) Import(_ *domain.Session) error { return domain.ErrImportNotSupported }

// mockImportProvider successfully imports sessions.
type mockImportProvider struct {
	name         domain.ProviderName
	importCalled bool
}

func (m *mockImportProvider) Name() domain.ProviderName { return m.name }
func (m *mockImportProvider) Detect(_ string, _ string) ([]domain.SessionSummary, error) {
	return nil, nil
}
func (m *mockImportProvider) Export(_ domain.SessionID, _ domain.StorageMode) (*domain.Session, error) {
	return nil, nil
}
func (m *mockImportProvider) CanImport() bool { return true }
func (m *mockImportProvider) Import(_ *domain.Session) error {
	m.importCalled = true
	return nil
}

// mockConverter simulates cross-provider conversion.
type mockConverter struct {
	toNativeCalled bool
}

func (m *mockConverter) SupportedFormats() []domain.ProviderName {
	return []domain.ProviderName{domain.ProviderClaudeCode, domain.ProviderOpenCode}
}

func (m *mockConverter) ToNative(session *domain.Session, _ domain.ProviderName) ([]byte, error) {
	m.toNativeCalled = true
	// Return a minimal valid JSON that FromNative can parse
	return []byte(`{"id":"` + string(session.ID) + `","provider":"opencode","messages":[]}`), nil
}

func (m *mockConverter) FromNative(data []byte, _ domain.ProviderName) (*domain.Session, error) {
	return &domain.Session{
		ID:       "converted",
		Provider: domain.ProviderOpenCode,
	}, nil
}
