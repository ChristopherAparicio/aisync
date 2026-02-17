package export

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

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
func (m *mockStore) GetByBranch(_, _ string) (*domain.Session, error) {
	return nil, domain.ErrSessionNotFound
}
func (m *mockStore) List(_ domain.ListOptions) ([]domain.SessionSummary, error) {
	return nil, nil
}

func (m *mockStore) Get(id domain.SessionID) (*domain.Session, error) {
	for _, s := range m.sessions {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, domain.ErrSessionNotFound
}

func testSession() *domain.Session {
	return &domain.Session{
		ID:          "test-export-001",
		Provider:    domain.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feat/test",
		ProjectPath: "/tmp/test",
		StorageMode: domain.StorageModeCompact,
		Summary:     "Test session for export",
		Version:     1,
		ExportedAt:  time.Date(2026, 2, 17, 10, 0, 0, 0, time.UTC),
		CreatedAt:   time.Date(2026, 2, 17, 9, 0, 0, 0, time.UTC),
		Messages: []domain.Message{
			{
				ID:        "msg-001",
				Role:      domain.RoleUser,
				Content:   "Hello world",
				Timestamp: time.Date(2026, 2, 17, 9, 0, 0, 0, time.UTC),
			},
			{
				ID:        "msg-002",
				Role:      domain.RoleAssistant,
				Content:   "Hi there!",
				Model:     "claude-sonnet",
				Timestamp: time.Date(2026, 2, 17, 9, 0, 5, 0, time.UTC),
				Tokens:    100,
			},
		},
	}
}

func TestExport_AisyncFormat(t *testing.T) {
	ios := iostreams.Test()
	store := &mockStore{sessions: []*domain.Session{testSession()}}

	f := &cmdutil.Factory{
		IOStreams: ios,
		StoreFunc: func() (domain.Store, error) {
			return store, nil
		},
	}

	opts := &Options{
		IO:          ios,
		Factory:     f,
		FormatFlag:  "aisync",
		SessionFlag: "test-export-001",
	}

	err := runExport(opts)
	if err != nil {
		t.Fatalf("runExport() error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !json.Valid([]byte(output)) {
		t.Fatal("output is not valid JSON")
	}

	var session domain.Session
	if jsonErr := json.Unmarshal([]byte(output), &session); jsonErr != nil {
		t.Fatalf("unmarshal error: %v", jsonErr)
	}
	if session.ID != "test-export-001" {
		t.Errorf("session ID = %q, want test-export-001", session.ID)
	}
	if len(session.Messages) != 2 {
		t.Errorf("messages = %d, want 2", len(session.Messages))
	}
}

func TestExport_ClaudeFormat(t *testing.T) {
	ios := iostreams.Test()
	store := &mockStore{sessions: []*domain.Session{testSession()}}

	f := &cmdutil.Factory{
		IOStreams: ios,
		StoreFunc: func() (domain.Store, error) {
			return store, nil
		},
	}

	opts := &Options{
		IO:          ios,
		Factory:     f,
		FormatFlag:  "claude",
		SessionFlag: "test-export-001",
	}

	err := runExport(opts)
	if err != nil {
		t.Fatalf("runExport() error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 JSONL lines, got %d", len(lines))
	}

	// Each line should be valid JSON
	for i, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Errorf("line %d is not valid JSON", i)
		}
	}
}

func TestExport_OpenCodeFormat(t *testing.T) {
	ios := iostreams.Test()
	store := &mockStore{sessions: []*domain.Session{testSession()}}

	f := &cmdutil.Factory{
		IOStreams: ios,
		StoreFunc: func() (domain.Store, error) {
			return store, nil
		},
	}

	opts := &Options{
		IO:          ios,
		Factory:     f,
		FormatFlag:  "opencode",
		SessionFlag: "test-export-001",
	}

	err := runExport(opts)
	if err != nil {
		t.Fatalf("runExport() error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !json.Valid([]byte(output)) {
		t.Fatal("output is not valid JSON")
	}
}

func TestExport_ContextMDFormat(t *testing.T) {
	ios := iostreams.Test()
	store := &mockStore{sessions: []*domain.Session{testSession()}}

	f := &cmdutil.Factory{
		IOStreams: ios,
		StoreFunc: func() (domain.Store, error) {
			return store, nil
		},
	}

	opts := &Options{
		IO:          ios,
		Factory:     f,
		FormatFlag:  "context",
		SessionFlag: "test-export-001",
	}

	err := runExport(opts)
	if err != nil {
		t.Fatalf("runExport() error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "# AI Session Context") {
		t.Error("expected markdown header in output")
	}
	if !strings.Contains(output, "Hello world") {
		t.Error("expected message content in output")
	}
}

func TestExport_UnknownFormat(t *testing.T) {
	ios := iostreams.Test()
	store := &mockStore{sessions: []*domain.Session{testSession()}}

	f := &cmdutil.Factory{
		IOStreams: ios,
		StoreFunc: func() (domain.Store, error) {
			return store, nil
		},
	}

	opts := &Options{
		IO:          ios,
		Factory:     f,
		FormatFlag:  "xml",
		SessionFlag: "test-export-001",
	}

	err := runExport(opts)
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
	if !strings.Contains(err.Error(), "unknown format") {
		t.Errorf("expected 'unknown format' error, got: %v", err)
	}
}

func TestExport_SessionNotFound(t *testing.T) {
	ios := iostreams.Test()
	store := &mockStore{sessions: nil}

	f := &cmdutil.Factory{
		IOStreams: ios,
		StoreFunc: func() (domain.Store, error) {
			return store, nil
		},
	}

	opts := &Options{
		IO:          ios,
		Factory:     f,
		SessionFlag: "nonexistent",
	}

	err := runExport(opts)
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		name  string
		want  string
		input int
	}{
		{"bytes", "500 B", 500},
		{"kilobytes", "2.5 KB", 2560},
		{"megabytes", "1.5 MB", 1572864},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatSize(tt.input)
			if got != tt.want {
				t.Errorf("formatSize(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
