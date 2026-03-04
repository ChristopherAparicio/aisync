package provider

import (
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// mockProvider is a simple mock for testing the registry.
type mockProvider struct {
	detectErr error
	name      session.ProviderName
	sessions  []session.Summary
}

func (m *mockProvider) Name() session.ProviderName { return m.name }
func (m *mockProvider) Detect(_ string, _ string) ([]session.Summary, error) {
	return m.sessions, m.detectErr
}
func (m *mockProvider) Export(_ session.ID, _ session.StorageMode) (*session.Session, error) {
	return nil, nil
}
func (m *mockProvider) CanImport() bool                 { return false }
func (m *mockProvider) Import(_ *session.Session) error { return nil }

func TestRegistry_Get(t *testing.T) {
	p := &mockProvider{name: session.ProviderClaudeCode}
	r := NewRegistry(p)

	t.Run("found", func(t *testing.T) {
		got, err := r.Get(session.ProviderClaudeCode)
		if err != nil {
			t.Fatalf("Get() error: %v", err)
		}
		if got.Name() != session.ProviderClaudeCode {
			t.Errorf("Name() = %q, want %q", got.Name(), session.ProviderClaudeCode)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := r.Get(session.ProviderOpenCode)
		if err == nil {
			t.Error("Get() should return error for unregistered provider")
		}
	})
}

func TestRegistry_DetectAll(t *testing.T) {
	now := time.Now()

	claude := &mockProvider{
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "s1", Provider: session.ProviderClaudeCode, CreatedAt: now},
		},
	}
	opencode := &mockProvider{
		name: session.ProviderOpenCode,
		sessions: []session.Summary{
			{ID: "s2", Provider: session.ProviderOpenCode, CreatedAt: now.Add(-time.Hour)},
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
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "s1", Provider: session.ProviderClaudeCode},
		},
	}
	broken := &mockProvider{
		name:      session.ProviderOpenCode,
		detectErr: session.ErrProviderNotDetected,
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
		name: session.ProviderClaudeCode,
		sessions: []session.Summary{
			{ID: "old", Provider: session.ProviderClaudeCode, CreatedAt: now.Add(-time.Hour)},
		},
	}
	newer := &mockProvider{
		name: session.ProviderOpenCode,
		sessions: []session.Summary{
			{ID: "new", Provider: session.ProviderOpenCode, CreatedAt: now},
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
	if provider.Name() != session.ProviderOpenCode {
		t.Errorf("DetectBest() provider = %q, want %q", provider.Name(), session.ProviderOpenCode)
	}
}

func TestRegistry_DetectBest_noSessions(t *testing.T) {
	empty := &mockProvider{
		name:     session.ProviderClaudeCode,
		sessions: nil,
	}

	r := NewRegistry(empty)

	_, _, err := r.DetectBest("/project", "main")
	if err != session.ErrProviderNotDetected {
		t.Errorf("DetectBest() error = %v, want ErrProviderNotDetected", err)
	}
}

func TestRegistry_Names(t *testing.T) {
	p1 := &mockProvider{name: session.ProviderClaudeCode}
	p2 := &mockProvider{name: session.ProviderOpenCode}
	r := NewRegistry(p1, p2)

	names := r.Names()
	if len(names) != 2 {
		t.Errorf("Names() returned %d names, want 2", len(names))
	}
}
