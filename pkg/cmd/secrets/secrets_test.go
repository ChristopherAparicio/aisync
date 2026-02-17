package secrets

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// mockStore implements domain.Store for testing.
type mockStore struct {
	sessions []*domain.Session
}

func (m *mockStore) Save(_ *domain.Session) error                    { return nil }
func (m *mockStore) Delete(_ domain.SessionID) error                 { return nil }
func (m *mockStore) AddLink(_ domain.SessionID, _ domain.Link) error { return nil }
func (m *mockStore) GetByLink(_ domain.LinkType, _ string) ([]domain.SessionSummary, error) {
	return nil, domain.ErrSessionNotFound
}
func (m *mockStore) Close() error { return nil }

func (m *mockStore) Get(id domain.SessionID) (*domain.Session, error) {
	for _, s := range m.sessions {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, domain.ErrSessionNotFound
}

func (m *mockStore) GetByBranch(_, _ string) (*domain.Session, error) {
	return nil, domain.ErrSessionNotFound
}

func (m *mockStore) List(_ domain.ListOptions) ([]domain.SessionSummary, error) {
	summaries := make([]domain.SessionSummary, 0, len(m.sessions))
	for _, s := range m.sessions {
		summaries = append(summaries, domain.SessionSummary{
			ID:       s.ID,
			Provider: s.Provider,
		})
	}
	return summaries, nil
}

func TestSecretsScan_cleanSession(t *testing.T) {
	ios := iostreams.Test()

	store := &mockStore{
		sessions: []*domain.Session{
			{
				ID:          "sess-1",
				Provider:    domain.ProviderClaudeCode,
				CreatedAt:   time.Now(),
				StorageMode: domain.StorageModeCompact,
				Messages: []domain.Message{
					{Role: domain.RoleUser, Content: "Hello, help me with Go code"},
					{Role: domain.RoleAssistant, Content: "Sure, I can help!"},
				},
			},
		},
	}

	f := &cmdutil.Factory{
		IOStreams: ios,
		StoreFunc: func() (domain.Store, error) {
			return store, nil
		},
	}

	opts := &ScanOptions{
		IO:      ios,
		Factory: f,
	}

	err := runScan(opts)
	if err != nil {
		t.Fatalf("runScan() error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "clean") {
		t.Errorf("expected 'clean' in output, got: %s", output)
	}
	if !strings.Contains(output, "No secrets detected") {
		t.Errorf("expected 'No secrets detected' in output, got: %s", output)
	}
}

func TestSecretsScan_sessionWithSecrets(t *testing.T) {
	ios := iostreams.Test()

	store := &mockStore{
		sessions: []*domain.Session{
			{
				ID:          "sess-2",
				Provider:    domain.ProviderClaudeCode,
				CreatedAt:   time.Now(),
				StorageMode: domain.StorageModeCompact,
				Messages: []domain.Message{
					{Role: domain.RoleUser, Content: "Use this key AKIAIOSFODNN7EXAMPLE"},
					{Role: domain.RoleAssistant, Content: "I'll use the ghp_ABCDEFghijklmnop1234567890abcdef token"},
				},
			},
		},
	}

	f := &cmdutil.Factory{
		IOStreams: ios,
		StoreFunc: func() (domain.Store, error) {
			return store, nil
		},
	}

	opts := &ScanOptions{
		IO:      ios,
		Factory: f,
	}

	err := runScan(opts)
	if err != nil {
		t.Fatalf("runScan() error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "secret(s) found") {
		t.Errorf("expected 'secret(s) found' in output, got: %s", output)
	}
}

func TestSecretsScan_specificSession(t *testing.T) {
	ios := iostreams.Test()

	store := &mockStore{
		sessions: []*domain.Session{
			{
				ID:          "sess-3",
				Provider:    domain.ProviderOpenCode,
				CreatedAt:   time.Now(),
				StorageMode: domain.StorageModeCompact,
				Messages: []domain.Message{
					{Role: domain.RoleUser, Content: "Just regular text"},
				},
			},
		},
	}

	f := &cmdutil.Factory{
		IOStreams: ios,
		StoreFunc: func() (domain.Store, error) {
			return store, nil
		},
	}

	opts := &ScanOptions{
		IO:          ios,
		Factory:     f,
		SessionFlag: "sess-3",
	}

	err := runScan(opts)
	if err != nil {
		t.Fatalf("runScan() error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Scanning 1 session(s)") {
		t.Errorf("expected 'Scanning 1 session(s)' in output, got: %s", output)
	}
	if !strings.Contains(output, "clean") {
		t.Errorf("expected 'clean' in output, got: %s", output)
	}
}

func TestSecretsScan_noSessions(t *testing.T) {
	ios := iostreams.Test()

	store := &mockStore{sessions: nil}

	f := &cmdutil.Factory{
		IOStreams: ios,
		StoreFunc: func() (domain.Store, error) {
			return store, nil
		},
	}

	opts := &ScanOptions{
		IO:      ios,
		Factory: f,
	}

	err := runScan(opts)
	if err != nil {
		t.Fatalf("runScan() error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "No sessions found") {
		t.Errorf("expected 'No sessions found' in output, got: %s", output)
	}
}

func TestSecretsScan_sessionNotFound(t *testing.T) {
	ios := iostreams.Test()

	store := &mockStore{sessions: nil}

	f := &cmdutil.Factory{
		IOStreams: ios,
		StoreFunc: func() (domain.Store, error) {
			return store, nil
		},
	}

	opts := &ScanOptions{
		IO:          ios,
		Factory:     f,
		SessionFlag: "nonexistent",
	}

	err := runScan(opts)
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}
