package provider

import (
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/domain"
)

// mockProvider is a simple mock for testing the registry.
type mockProvider struct {
	detectErr error
	name      domain.ProviderName
	sessions  []domain.SessionSummary
}

func (m *mockProvider) Name() domain.ProviderName { return m.name }
func (m *mockProvider) Detect(_ string, _ string) ([]domain.SessionSummary, error) {
	return m.sessions, m.detectErr
}
func (m *mockProvider) Export(_ domain.SessionID, _ domain.StorageMode) (*domain.Session, error) {
	return nil, nil
}
func (m *mockProvider) CanImport() bool                { return false }
func (m *mockProvider) Import(_ *domain.Session) error { return nil }

func TestRegistry_Get(t *testing.T) {
	p := &mockProvider{name: domain.ProviderClaudeCode}
	r := NewRegistry(p)

	t.Run("found", func(t *testing.T) {
		got, err := r.Get(domain.ProviderClaudeCode)
		if err != nil {
			t.Fatalf("Get() error: %v", err)
		}
		if got.Name() != domain.ProviderClaudeCode {
			t.Errorf("Name() = %q, want %q", got.Name(), domain.ProviderClaudeCode)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := r.Get(domain.ProviderOpenCode)
		if err == nil {
			t.Error("Get() should return error for unregistered provider")
		}
	})
}

func TestRegistry_DetectAll(t *testing.T) {
	now := time.Now()

	claude := &mockProvider{
		name: domain.ProviderClaudeCode,
		sessions: []domain.SessionSummary{
			{ID: "s1", Provider: domain.ProviderClaudeCode, CreatedAt: now},
		},
	}
	opencode := &mockProvider{
		name: domain.ProviderOpenCode,
		sessions: []domain.SessionSummary{
			{ID: "s2", Provider: domain.ProviderOpenCode, CreatedAt: now.Add(-time.Hour)},
		},
	}

	r := NewRegistry(claude, opencode)

	all, err := r.DetectAll("/project", "main")
	if err != nil {
		t.Fatalf("DetectAll() error: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("DetectAll() returned %d summaries, want 2", len(all))
	}
}

func TestRegistry_DetectAll_skipsErrors(t *testing.T) {
	working := &mockProvider{
		name: domain.ProviderClaudeCode,
		sessions: []domain.SessionSummary{
			{ID: "s1", Provider: domain.ProviderClaudeCode},
		},
	}
	broken := &mockProvider{
		name:      domain.ProviderOpenCode,
		detectErr: domain.ErrProviderNotDetected,
	}

	r := NewRegistry(working, broken)

	all, err := r.DetectAll("/project", "main")
	if err != nil {
		t.Fatalf("DetectAll() error: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("DetectAll() returned %d summaries, want 1", len(all))
	}
}

func TestRegistry_DetectBest(t *testing.T) {
	now := time.Now()

	older := &mockProvider{
		name: domain.ProviderClaudeCode,
		sessions: []domain.SessionSummary{
			{ID: "old", Provider: domain.ProviderClaudeCode, CreatedAt: now.Add(-time.Hour)},
		},
	}
	newer := &mockProvider{
		name: domain.ProviderOpenCode,
		sessions: []domain.SessionSummary{
			{ID: "new", Provider: domain.ProviderOpenCode, CreatedAt: now},
		},
	}

	r := NewRegistry(older, newer)

	summary, provider, err := r.DetectBest("/project", "main")
	if err != nil {
		t.Fatalf("DetectBest() error: %v", err)
	}
	if summary.ID != "new" {
		t.Errorf("DetectBest() ID = %q, want %q", summary.ID, "new")
	}
	if provider.Name() != domain.ProviderOpenCode {
		t.Errorf("DetectBest() provider = %q, want %q", provider.Name(), domain.ProviderOpenCode)
	}
}

func TestRegistry_DetectBest_noSessions(t *testing.T) {
	empty := &mockProvider{
		name:     domain.ProviderClaudeCode,
		sessions: nil,
	}

	r := NewRegistry(empty)

	_, _, err := r.DetectBest("/project", "main")
	if err != domain.ErrProviderNotDetected {
		t.Errorf("DetectBest() error = %v, want ErrProviderNotDetected", err)
	}
}

func TestRegistry_Names(t *testing.T) {
	p1 := &mockProvider{name: domain.ProviderClaudeCode}
	p2 := &mockProvider{name: domain.ProviderOpenCode}
	r := NewRegistry(p1, p2)

	names := r.Names()
	if len(names) != 2 {
		t.Errorf("Names() returned %d names, want 2", len(names))
	}
}
