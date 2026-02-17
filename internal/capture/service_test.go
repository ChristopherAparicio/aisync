package capture

import (
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/internal/provider"
)

func TestCapture_autoDetect(t *testing.T) {
	store := &mockStore{}
	prov := &mockProvider{
		name: domain.ProviderClaudeCode,
		sessions: []domain.SessionSummary{
			{ID: "ses-1", Provider: domain.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &domain.Session{
			ID:          "ses-1",
			Provider:    domain.ProviderClaudeCode,
			Agent:       "claude",
			Branch:      "main",
			ProjectPath: "/test/project",
			StorageMode: domain.StorageModeCompact,
			Summary:     "Test session",
			Messages:    []domain.Message{{ID: "m1", Role: domain.RoleUser, Content: "hello"}},
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	result, err := svc.Capture(Request{
		ProjectPath: "/test/project",
		Branch:      "main",
		Mode:        domain.StorageModeCompact,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}

	if result.Session.ID != "ses-1" {
		t.Errorf("Session.ID = %q, want %q", result.Session.ID, "ses-1")
	}
	if result.Provider != domain.ProviderClaudeCode {
		t.Errorf("Provider = %q, want %q", result.Provider, domain.ProviderClaudeCode)
	}

	// Verify session was stored
	if store.savedSession == nil {
		t.Error("Session was not saved to store")
	}

	// Verify branch link was added
	var hasBranchLink bool
	for _, link := range result.Session.Links {
		if link.LinkType == domain.LinkBranch && link.Ref == "main" {
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
		name: domain.ProviderClaudeCode,
		sessions: []domain.SessionSummary{
			{ID: "claude-1", Provider: domain.ProviderClaudeCode, Branch: "feat"},
		},
		exportSession: &domain.Session{
			ID:       "claude-1",
			Provider: domain.ProviderClaudeCode,
		},
	}
	opencode := &mockProvider{
		name: domain.ProviderOpenCode,
		sessions: []domain.SessionSummary{
			{ID: "oc-1", Provider: domain.ProviderOpenCode, CreatedAt: time.Now()},
		},
	}

	reg := provider.NewRegistry(claude, opencode)
	svc := NewService(reg, store)

	result, err := svc.Capture(Request{
		ProjectPath:  "/test/project",
		Branch:       "feat",
		Mode:         domain.StorageModeCompact,
		ProviderName: domain.ProviderClaudeCode,
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
		name: domain.ProviderClaudeCode,
		sessions: []domain.SessionSummary{
			{ID: "ses-1", Provider: domain.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &domain.Session{
			ID:      "ses-1",
			Summary: "Original summary",
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	result, err := svc.Capture(Request{
		ProjectPath: "/test/project",
		Branch:      "main",
		Mode:        domain.StorageModeCompact,
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
		name: domain.ProviderClaudeCode,
		sessions: []domain.SessionSummary{
			{ID: "ses-1", Provider: domain.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &domain.Session{
			ID:       "ses-1",
			Provider: domain.ProviderClaudeCode,
			Messages: []domain.Message{
				{Content: "Here is AKIAIOSFODNN7EXAMPLE", Role: domain.RoleUser},
			},
		},
	}

	scanner := &mockScanner{mode: domain.SecretModeMask}
	reg := provider.NewRegistry(prov)
	svc := NewServiceWithScanner(reg, store, scanner)

	result, err := svc.Capture(Request{
		ProjectPath: "/test",
		Branch:      "main",
		Mode:        domain.StorageModeCompact,
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
		name: domain.ProviderClaudeCode,
		sessions: []domain.SessionSummary{
			{ID: "ses-1", Provider: domain.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &domain.Session{
			ID:       "ses-1",
			Provider: domain.ProviderClaudeCode,
			Messages: []domain.Message{
				{Content: "Here is AKIAIOSFODNN7EXAMPLE", Role: domain.RoleUser},
			},
		},
	}

	scanner := &mockScanner{mode: domain.SecretModeBlock}
	reg := provider.NewRegistry(prov)
	svc := NewServiceWithScanner(reg, store, scanner)

	_, err := svc.Capture(Request{
		ProjectPath: "/test",
		Branch:      "main",
		Mode:        domain.StorageModeCompact,
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
		name: domain.ProviderClaudeCode,
		sessions: []domain.SessionSummary{
			{ID: "ses-1", Provider: domain.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &domain.Session{
			ID:       "ses-1",
			Provider: domain.ProviderClaudeCode,
			Messages: []domain.Message{
				{Content: "Here is AKIAIOSFODNN7EXAMPLE", Role: domain.RoleUser},
			},
		},
	}

	scanner := &mockScanner{mode: domain.SecretModeWarn}
	reg := provider.NewRegistry(prov)
	svc := NewServiceWithScanner(reg, store, scanner)

	result, err := svc.Capture(Request{
		ProjectPath: "/test",
		Branch:      "main",
		Mode:        domain.StorageModeCompact,
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
		name: domain.ProviderClaudeCode,
		sessions: []domain.SessionSummary{
			{ID: "ses-1", Provider: domain.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &domain.Session{
			ID:       "ses-1",
			Provider: domain.ProviderClaudeCode,
			Messages: []domain.Message{
				{Content: "Here is AKIAIOSFODNN7EXAMPLE", Role: domain.RoleUser},
			},
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store) // No scanner

	result, err := svc.Capture(Request{
		ProjectPath: "/test",
		Branch:      "main",
		Mode:        domain.StorageModeCompact,
	})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}

	if result.SecretsFound != 0 {
		t.Error("expected 0 secrets when no scanner configured")
	}
}

func TestCapture_deduplication(t *testing.T) {
	store := &mockStore{}
	prov := &mockProvider{
		name: domain.ProviderClaudeCode,
		sessions: []domain.SessionSummary{
			{ID: "ses-first", Provider: domain.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
		},
		exportSession: &domain.Session{
			ID:          "ses-first",
			Provider:    domain.ProviderClaudeCode,
			Branch:      "main",
			ProjectPath: "/test/project",
			Messages:    []domain.Message{{ID: "m1", Role: domain.RoleUser, Content: "hello"}},
		},
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	// First capture
	result1, err := svc.Capture(Request{
		ProjectPath: "/test/project",
		Branch:      "main",
		Mode:        domain.StorageModeCompact,
	})
	if err != nil {
		t.Fatalf("First Capture() error: %v", err)
	}
	firstID := result1.Session.ID

	// Second capture with a different provider session ID
	prov.sessions = []domain.SessionSummary{
		{ID: "ses-second", Provider: domain.ProviderClaudeCode, Branch: "main", CreatedAt: time.Now()},
	}
	prov.exportSession = &domain.Session{
		ID:          "ses-second",
		Provider:    domain.ProviderClaudeCode,
		Branch:      "main",
		ProjectPath: "/test/project",
		Summary:     "Updated session",
		Messages:    []domain.Message{{ID: "m2", Role: domain.RoleUser, Content: "world"}},
	}

	result2, err := svc.Capture(Request{
		ProjectPath: "/test/project",
		Branch:      "main",
		Mode:        domain.StorageModeCompact,
	})
	if err != nil {
		t.Fatalf("Second Capture() error: %v", err)
	}

	// Should reuse the first session's ID (dedup)
	if result2.Session.ID != firstID {
		t.Errorf("Second capture ID = %q, want %q (should reuse existing)", result2.Session.ID, firstID)
	}

	// But summary should be updated
	if result2.Session.Summary != "Updated session" {
		t.Errorf("Summary = %q, want %q", result2.Session.Summary, "Updated session")
	}
}

func TestCapture_noSessionsFound(t *testing.T) {
	store := &mockStore{}
	prov := &mockProvider{
		name:     domain.ProviderClaudeCode,
		sessions: nil, // no sessions
	}

	reg := provider.NewRegistry(prov)
	svc := NewService(reg, store)

	_, err := svc.Capture(Request{
		ProjectPath: "/test/project",
		Branch:      "main",
		Mode:        domain.StorageModeCompact,
	})
	if err == nil {
		t.Error("Capture() should return error when no sessions found")
	}
}

// --- Mocks ---

type mockStore struct {
	savedSession *domain.Session
	byBranch     map[string]*domain.Session // key: "projectPath:branch"
	saveCount    int
}

func (m *mockStore) Save(session *domain.Session) error {
	m.savedSession = session
	m.saveCount++
	if m.byBranch == nil {
		m.byBranch = make(map[string]*domain.Session)
	}
	key := session.ProjectPath + ":" + session.Branch
	m.byBranch[key] = session
	return nil
}
func (m *mockStore) Get(_ domain.SessionID) (*domain.Session, error) {
	return nil, domain.ErrSessionNotFound
}
func (m *mockStore) GetByBranch(projectPath string, branch string) (*domain.Session, error) {
	if m.byBranch == nil {
		return nil, domain.ErrSessionNotFound
	}
	key := projectPath + ":" + branch
	s, ok := m.byBranch[key]
	if !ok {
		return nil, domain.ErrSessionNotFound
	}
	return s, nil
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
	exportSession *domain.Session
	name          domain.ProviderName
	sessions      []domain.SessionSummary
}

func (m *mockProvider) Name() domain.ProviderName { return m.name }
func (m *mockProvider) Detect(_ string, _ string) ([]domain.SessionSummary, error) {
	return m.sessions, nil
}
func (m *mockProvider) Export(_ domain.SessionID, _ domain.StorageMode) (*domain.Session, error) {
	if m.exportSession == nil {
		return nil, domain.ErrSessionNotFound
	}
	// Return a copy to avoid mutation
	s := *m.exportSession
	return &s, nil
}
func (m *mockProvider) CanImport() bool                { return true }
func (m *mockProvider) Import(_ *domain.Session) error { return nil }

type mockScanner struct {
	mode domain.SecretMode
}

func (m *mockScanner) Scan(content string) []domain.SecretMatch {
	// Simple: detect "AKIA" as a fake secret for testing
	var matches []domain.SecretMatch
	idx := 0
	for {
		pos := indexOf(content[idx:], "AKIA")
		if pos == -1 {
			break
		}
		start := idx + pos
		end := start + 20
		if end > len(content) {
			end = len(content)
		}
		matches = append(matches, domain.SecretMatch{
			Type:     "AWS_ACCESS_KEY",
			Value:    content[start:end],
			StartPos: start,
			EndPos:   end,
		})
		idx = end
	}
	return matches
}

func (m *mockScanner) Mask(content string) string {
	matches := m.Scan(content)
	if len(matches) == 0 {
		return content
	}
	result := content
	for i := len(matches) - 1; i >= 0; i-- {
		match := matches[i]
		result = result[:match.StartPos] + "***REDACTED:AWS_ACCESS_KEY***" + result[match.EndPos:]
	}
	return result
}

func (m *mockScanner) Mode() domain.SecretMode { return m.mode }

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
