package capture

import (
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/secrets"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestCapture_autoDetect(t *testing.T) {
	store := &mockStore{}
	prov := &mockProvider{
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "ses-1", Provider: session.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &session.Session{
			ID:          "ses-1",
			Provider:    session.ProviderClaudeCode,
			Agent:       "claude",
			Branch:      "main",
			ProjectPath: "/test/project",
			StorageMode: session.StorageModeCompact,
			Summary:     "Test session",
			Messages:    []session.Message{{ID: "m1", Role: session.RoleUser, Content: "hello"}},
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	result, err := svc.Capture(Request{
		ProjectPath: "/test/project",
		Branch:      "main",
		Mode:        session.StorageModeCompact,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}

	if result.Session.ID != "ses-1" {
		t.Errorf("Session.ID = %q, want %q", result.Session.ID, "ses-1")
	}
	if result.Provider != session.ProviderClaudeCode {
		t.Errorf("Provider = %q, want %q", result.Provider, session.ProviderClaudeCode)
	}

	// Verify session was stored
	if store.savedSession == nil {
		t.Error("Session was not saved to store")
	}

	// Verify branch link was added
	var hasBranchLink bool
	for _, link := range result.Session.Links {
		if link.LinkType == session.LinkBranch && link.Ref == "main" {
			hasBranchLink = true
		}
	}
	if !hasBranchLink {
		t.Error("Branch link not added to session")
	}
}

func TestCapture_explicitProvider(t *testing.T) {
	store := &mockStore{}
	claude := &mockProvider{
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "claude-1", Provider: session.ProviderClaudeCode, Branch: "feat"},
		},
		exportSession: &session.Session{
			ID:       "claude-1",
			Provider: session.ProviderClaudeCode,
		},
	}
	opencode := &mockProvider{
		name: session.ProviderOpenCode,
		sessions: []session.Summary{
			{ID: "oc-1", Provider: session.ProviderOpenCode, CreatedAt: time.Now()},
		},
	}

	reg := provider.NewRegistry(claude, opencode)
	svc := NewService(reg, store)

	result, err := svc.Capture(Request{
		ProjectPath:  "/test/project",
		Branch:       "feat",
		Mode:         session.StorageModeCompact,
		ProviderName: session.ProviderClaudeCode,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}

	if result.Session.ID != "claude-1" {
		t.Errorf("Session.ID = %q, want %q", result.Session.ID, "claude-1")
	}
}

func TestCapture_messageOverride(t *testing.T) {
	store := &mockStore{}
	prov := &mockProvider{
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "ses-1", Provider: session.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &session.Session{
			ID:      "ses-1",
			Summary: "Original summary",
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	result, err := svc.Capture(Request{
		ProjectPath: "/test/project",
		Branch:      "main",
		Mode:        session.StorageModeCompact,
		Message:     "Custom summary",
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}

	if result.Session.Summary != "Custom summary" {
		t.Errorf("Summary = %q, want %q", result.Session.Summary, "Custom summary")
	}
}

func TestCapture_withScanner_maskMode(t *testing.T) {
	store := &mockStore{}
	prov := &mockProvider{
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "ses-1", Provider: session.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &session.Session{
			ID:       "ses-1",
			Provider: session.ProviderClaudeCode,
			Messages: []session.Message{
				{Content: "Here is AKIAIOSFODNN7EXAMPLE", Role: session.RoleUser},
			},
		},
	}

	sc := secrets.NewScanner(session.SecretModeMask, nil)
	reg := provider.NewRegistry(prov)
	svc := NewServiceWithScanner(reg, store, sc)

	result, err := svc.Capture(Request{
		ProjectPath: "/test",
		Branch:      "main",
		Mode:        session.StorageModeCompact,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}

	if result.SecretsFound == 0 {
		t.Error("expected secrets to be found")
	}
	// Verify masking was applied
	content := result.Session.Messages[0].Content
	if content == "Here is AKIAIOSFODNN7EXAMPLE" {
		t.Error("content should be masked but was not modified")
	}
}

func TestCapture_withScanner_blockMode(t *testing.T) {
	store := &mockStore{}
	prov := &mockProvider{
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "ses-1", Provider: session.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &session.Session{
			ID:       "ses-1",
			Provider: session.ProviderClaudeCode,
			Messages: []session.Message{
				{Content: "Here is AKIAIOSFODNN7EXAMPLE", Role: session.RoleUser},
			},
		},
	}

	sc := secrets.NewScanner(session.SecretModeBlock, nil)
	reg := provider.NewRegistry(prov)
	svc := NewServiceWithScanner(reg, store, sc)

	_, err := svc.Capture(Request{
		ProjectPath: "/test",
		Branch:      "main",
		Mode:        session.StorageModeCompact,
	})
	if err == nil {
		t.Fatal("Capture() should return error in block mode when secrets found")
	}
	if store.savedSession != nil {
		t.Error("session should NOT be saved in block mode")
	}
}

func TestCapture_withScanner_warnMode(t *testing.T) {
	store := &mockStore{}
	prov := &mockProvider{
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "ses-1", Provider: session.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &session.Session{
			ID:       "ses-1",
			Provider: session.ProviderClaudeCode,
			Messages: []session.Message{
				{Content: "Here is AKIAIOSFODNN7EXAMPLE", Role: session.RoleUser},
			},
		},
	}

	sc := secrets.NewScanner(session.SecretModeWarn, nil)
	reg := provider.NewRegistry(prov)
	svc := NewServiceWithScanner(reg, store, sc)

	result, err := svc.Capture(Request{
		ProjectPath: "/test",
		Branch:      "main",
		Mode:        session.StorageModeCompact,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}

	if result.SecretsFound == 0 {
		t.Error("expected secrets to be reported")
	}
	// In warn mode, content should NOT be masked
	if result.Session.Messages[0].Content != "Here is AKIAIOSFODNN7EXAMPLE" {
		t.Error("content should NOT be masked in warn mode")
	}
}

func TestCapture_noScanner(t *testing.T) {
	store := &mockStore{}
	prov := &mockProvider{
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "ses-1", Provider: session.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &session.Session{
			ID:       "ses-1",
			Provider: session.ProviderClaudeCode,
			Messages: []session.Message{
				{Content: "Here is AKIAIOSFODNN7EXAMPLE", Role: session.RoleUser},
			},
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store) // No scanner

	result, err := svc.Capture(Request{
		ProjectPath: "/test",
		Branch:      "main",
		Mode:        session.StorageModeCompact,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}

	if result.SecretsFound != 0 {
		t.Error("expected 0 secrets when no scanner configured")
	}
}

func TestCapture_multiSession(t *testing.T) {
	store := &mockStore{}
	prov := &mockProvider{
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "ses-first", Provider: session.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &session.Session{
			ID:          "ses-first",
			Provider:    session.ProviderClaudeCode,
			Branch:      "main",
			ProjectPath: "/test/project",
			Messages:    []session.Message{{ID: "m1", Role: session.RoleUser, Content: "hello"}},
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	// First capture
	result1, err := svc.Capture(Request{
		ProjectPath: "/test/project",
		Branch:      "main",
		Mode:        session.StorageModeCompact,
	})
	if err != nil {
		t.Fatalf("First Capture() error: %v", err)
	}
	firstID := result1.Session.ID

	// Second capture with a different provider session ID
	prov.sessions = []session.Summary{
		{ID: "ses-second", Provider: session.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
	}
	prov.exportSession = &session.Session{
		ID:          "ses-second",
		Provider:    session.ProviderClaudeCode,
		Branch:      "main",
		ProjectPath: "/test/project",
		Summary:     "Updated session",
		Messages:    []session.Message{{ID: "m2", Role: session.RoleUser, Content: "world"}},
	}

	result2, err := svc.Capture(Request{
		ProjectPath: "/test/project",
		Branch:      "main",
		Mode:        session.StorageModeCompact,
	})
	if err != nil {
		t.Fatalf("Second Capture() error: %v", err)
	}

	// Multi-session: each capture should produce a distinct ID
	if result2.Session.ID == firstID {
		t.Errorf("Second capture ID = %q, should differ from first (no dedup)", firstID)
	}

	// Both sessions should be saved (saveCount == 2)
	if store.saveCount != 2 {
		t.Errorf("saveCount = %d, want 2", store.saveCount)
	}

	// Summary should reflect the second session
	if result2.Session.Summary != "Updated session" {
		t.Errorf("Summary = %q, want %q", result2.Session.Summary, "Updated session")
	}
}

func TestCapture_noSessionsFound(t *testing.T) {
	store := &mockStore{}
	prov := &mockProvider{
		name:     session.ProviderClaudeCode,
		sessions: nil, // no sessions
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	_, err := svc.Capture(Request{
		ProjectPath: "/test/project",
		Branch:      "main",
		Mode:        session.StorageModeCompact,
	})
	if err == nil {
		t.Error("Capture() should return error when no sessions found")
	}
}

// --- Mocks ---

type mockStore struct {
	savedSession *session.Session
	byBranch     map[string]*session.Session // key: "projectPath:branch"
	saveCount    int
}

func (m *mockStore) Save(sess *session.Session) error {
	m.savedSession = sess
	m.saveCount++
	if m.byBranch == nil {
		m.byBranch = make(map[string]*session.Session)
	}
	key := sess.ProjectPath + ":" + sess.Branch
	m.byBranch[key] = sess
	return nil
}
func (m *mockStore) Get(_ session.ID) (*session.Session, error) {
	return nil, session.ErrSessionNotFound
}
func (m *mockStore) GetLatestByBranch(projectPath string, branch string) (*session.Session, error) {
	if m.byBranch == nil {
		return nil, session.ErrSessionNotFound
	}
	key := projectPath + ":" + branch
	s, ok := m.byBranch[key]
	if !ok {
		return nil, session.ErrSessionNotFound
	}
	return s, nil
}

// CountByBranch is a stub — capture service does not call it.
// Use saveCount to verify multi-session behavior instead.
func (m *mockStore) CountByBranch(_, _ string) (int, error) { return m.saveCount, nil }
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
func (m *mockStore) GetSessionsByFile(_ session.BlameQuery) ([]session.BlameEntry, error) {
	return nil, nil
}

type mockProvider struct {
	exportSession *session.Session
	name          session.ProviderName
	sessions      []session.Summary
}

func (m *mockProvider) Name() session.ProviderName { return m.name }
func (m *mockProvider) Detect(_ string, _ string) ([]session.Summary, error) {
	return m.sessions, nil
}
func (m *mockProvider) Export(_ session.ID, _ session.StorageMode) (*session.Session, error) {
	if m.exportSession == nil {
		return nil, session.ErrSessionNotFound
	}
	// Return a copy to avoid mutation
	s := *m.exportSession
	return &s, nil
}
func (m *mockProvider) CanImport() bool                 { return true }
func (m *mockProvider) Import(_ *session.Session) error { return nil }
