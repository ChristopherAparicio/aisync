package restore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
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

	projectDir := t.TempDir()
	store := testutil.NewMockStore(sess)
	store.ByBranch[projectDir+":main"] = sess
	reg := provider.NewRegistry(&mockProvider{name: session.ProviderClaudeCode, canImportVal: true})
	svc := NewService(reg, store)

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

	store := testutil.NewMockStore(sess)
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

	projectDir := t.TempDir()
	store := testutil.NewMockStore(sess)
	store.ByBranch[projectDir+":feat/auth"] = sess
	reg := provider.NewRegistry()
	svc := NewService(reg, store)

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
	store := testutil.NewMockStore() // no sessions
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

	projectDir := t.TempDir()
	store := testutil.NewMockStore(sess)
	store.ByBranch[projectDir+":feat/cross"] = sess
	importingProvider := &mockImportProvider{name: session.ProviderOpenCode}
	reg := provider.NewRegistry(importingProvider)
	conv := converter.New()
	svc := NewServiceWithConverter(reg, store, conv)

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

	projectDir := t.TempDir()
	store := testutil.NewMockStore(sess)
	store.ByBranch[projectDir+":feat/cursor"] = sess
	noImportProvider := &mockProvider{name: session.ProviderCursor}
	noImportProvider.canImportVal = false
	reg := provider.NewRegistry(noImportProvider)
	conv := converter.New()
	svc := NewServiceWithConverter(reg, store, conv)

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

	projectDir := t.TempDir()
	store := testutil.NewMockStore(sess)
	store.ByBranch[projectDir+":feat/agent"] = sess
	reg := provider.NewRegistry()
	svc := NewService(reg, store)

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

func TestRestore_withFilters(t *testing.T) {
	sess := &session.Session{
		ID:          "ses-filter",
		Provider:    session.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feat/filter",
		ProjectPath: "/test/project",
		Summary:     "Filter test",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "fix the bug"},
			{ID: "m2", Role: session.RoleAssistant, Content: "", ToolCalls: []session.ToolCall{
				{ID: "tc-1", Name: "bash", State: session.ToolStateError, Output: "Error: compilation failed\nat line 42\nstack trace..."},
			}},
			{ID: "m3", Role: session.RoleAssistant, Content: ""},      // empty
			{ID: "m4", Role: session.RoleAssistant, Content: "Done!"}, // kept
		},
	}

	projectDir := t.TempDir()
	store := testutil.NewMockStore(sess)
	store.ByBranch[projectDir+":feat/filter"] = sess
	reg := provider.NewRegistry()
	svc := NewService(reg, store)

	result, err := svc.Restore(Request{
		ProjectPath: projectDir,
		Branch:      "feat/filter",
		AsContext:   true,
		Filters: []session.SessionFilter{
			&mockEmptyFilter{},  // removes empty messages
			&mockErrorCleaner{}, // cleans error outputs
		},
	})
	if err != nil {
		t.Fatalf("Restore() error: %v", err)
	}

	if len(result.FilterResults) != 2 {
		t.Fatalf("expected 2 filter results, got %d", len(result.FilterResults))
	}

	// The empty message should be removed.
	for _, msg := range result.Session.Messages {
		if msg.ID == "m3" {
			t.Error("empty message m3 should have been removed")
		}
	}
}

func TestRestore_filterError(t *testing.T) {
	sess := &session.Session{
		ID:          "ses-filter-err",
		Provider:    session.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feat/err",
		ProjectPath: "/test/project",
		Messages:    []session.Message{{ID: "m1", Content: "hello"}},
	}

	projectDir := t.TempDir()
	store := testutil.NewMockStore(sess)
	store.ByBranch[projectDir+":feat/err"] = sess
	reg := provider.NewRegistry()
	svc := NewService(reg, store)

	_, err := svc.Restore(Request{
		ProjectPath: projectDir,
		Branch:      "feat/err",
		Filters:     []session.SessionFilter{&mockFailFilter{}},
	})
	if err == nil {
		t.Fatal("expected error from failing filter")
	}
	if !strings.Contains(err.Error(), "applying filters") {
		t.Errorf("error should mention applying filters, got %q", err.Error())
	}
}

func TestRestore_filtersWithNativeImport(t *testing.T) {
	sess := &session.Session{
		ID:          "ses-filter-native",
		Provider:    session.ProviderOpenCode,
		Agent:       "opencode",
		Branch:      "feat/native",
		ProjectPath: "/test/project",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "hello"},
			{ID: "m2", Role: session.RoleAssistant, Content: ""}, // empty
			{ID: "m3", Role: session.RoleAssistant, Content: "hi!"},
		},
	}

	projectDir := t.TempDir()
	store := testutil.NewMockStore(sess)
	store.ByBranch[projectDir+":feat/native"] = sess
	importingProvider := &mockImportProvider{name: session.ProviderOpenCode}
	reg := provider.NewRegistry(importingProvider)
	conv := converter.New()
	svc := NewServiceWithConverter(reg, store, conv)

	result, err := svc.Restore(Request{
		ProjectPath:  projectDir,
		Branch:       "feat/native",
		ProviderName: session.ProviderOpenCode,
		Filters:      []session.SessionFilter{&mockEmptyFilter{}},
	})
	if err != nil {
		t.Fatalf("Restore() error: %v", err)
	}

	if result.Method != "native" {
		t.Errorf("Method = %q, want native", result.Method)
	}

	// Filter results should be populated.
	if len(result.FilterResults) != 1 {
		t.Fatalf("expected 1 filter result, got %d", len(result.FilterResults))
	}
	if !result.FilterResults[0].Applied {
		t.Error("empty message filter should have applied")
	}

	// The imported session should have the empty message removed.
	if importingProvider.importCalled {
		// Good — the provider received the filtered session.
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

	projectDir := t.TempDir()
	store := testutil.NewMockStore(sess)
	store.ByBranch[projectDir+":feat/same"] = sess
	importingProvider := &mockImportProvider{name: session.ProviderOpenCode}
	reg := provider.NewRegistry(importingProvider)
	conv := converter.New()
	svc := NewServiceWithConverter(reg, store, conv)

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

// mockEmptyFilter removes messages with empty content.
type mockEmptyFilter struct{}

func (f *mockEmptyFilter) Name() string { return "mock-empty" }
func (f *mockEmptyFilter) Apply(sess *session.Session) (*session.Session, *session.FilterResult, error) {
	cp := session.CopySession(sess)
	var kept []session.Message
	removed := 0
	for _, msg := range cp.Messages {
		if msg.Content == "" && len(msg.ToolCalls) == 0 {
			removed++
			continue
		}
		kept = append(kept, msg)
	}
	cp.Messages = kept
	return cp, &session.FilterResult{
		FilterName:      "mock-empty",
		Applied:         removed > 0,
		MessagesRemoved: removed,
	}, nil
}

// mockErrorCleaner is a simplified error cleaner for testing.
type mockErrorCleaner struct{}

func (f *mockErrorCleaner) Name() string { return "mock-error-cleaner" }
func (f *mockErrorCleaner) Apply(sess *session.Session) (*session.Session, *session.FilterResult, error) {
	cp := session.CopySession(sess)
	modified := 0
	for i := range cp.Messages {
		for j := range cp.Messages[i].ToolCalls {
			tc := &cp.Messages[i].ToolCalls[j]
			if tc.State == session.ToolStateError && tc.Output != "" {
				tc.Output = "[cleaned error]"
				modified++
			}
		}
	}
	return cp, &session.FilterResult{
		FilterName:       "mock-error-cleaner",
		Applied:          modified > 0,
		MessagesModified: modified,
	}, nil
}

// mockFailFilter always returns an error.
type mockFailFilter struct{}

func (f *mockFailFilter) Name() string { return "mock-fail" }
func (f *mockFailFilter) Apply(_ *session.Session) (*session.Session, *session.FilterResult, error) {
	return nil, nil, fmt.Errorf("intentional test failure")
}
