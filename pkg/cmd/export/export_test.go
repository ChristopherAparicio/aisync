package export

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

type mockStore struct {
	sessions []*session.Session
}

func (m *mockStore) Save(_ *session.Session) error              { return nil }
func (m *mockStore) Delete(_ session.ID) error                  { return nil }
func (m *mockStore) AddLink(_ session.ID, _ session.Link) error { return nil }
func (m *mockStore) GetByLink(_ session.LinkType, _ string) ([]session.Summary, error) {
	return nil, session.ErrSessionNotFound
}
func (m *mockStore) Close() error { return nil }
func (m *mockStore) GetByBranch(_, _ string) (*session.Session, error) {
	return nil, session.ErrSessionNotFound
}
func (m *mockStore) List(_ session.ListOptions) ([]session.Summary, error) {
	return nil, nil
}

func (m *mockStore) Get(id session.ID) (*session.Session, error) {
	for _, s := range m.sessions {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, session.ErrSessionNotFound
}
func (m *mockStore) SaveUser(_ *session.User) error                 { return nil }
func (m *mockStore) GetUser(_ session.ID) (*session.User, error)    { return nil, nil }
func (m *mockStore) GetUserByEmail(_ string) (*session.User, error) { return nil, nil }
func (m *mockStore) Search(_ session.SearchQuery) (*session.SearchResult, error) {
	return &session.SearchResult{}, nil
}
func (m *mockStore) GetSessionsByFile(_ session.BlameQuery) ([]session.BlameEntry, error) {
	return nil, nil
}

func testSession() *session.Session {
	return &session.Session{
		ID:          "test-export-001",
		Provider:    session.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feat/test",
		ProjectPath: "/tmp/test",
		StorageMode: session.StorageModeCompact,
		Summary:     "Test session for export",
		Version:     1,
		ExportedAt:  time.Date(2026, 2, 17, 10, 0, 0, 0, time.UTC),
		CreatedAt:   time.Date(2026, 2, 17, 9, 0, 0, 0, time.UTC),
		Messages: []session.Message{
			{
				ID:        "msg-001",
				Role:      session.RoleUser,
				Content:   "Hello world",
				Timestamp: time.Date(2026, 2, 17, 9, 0, 0, 0, time.UTC),
			},
			{
				ID:        "msg-002",
				Role:      session.RoleAssistant,
				Content:   "Hi there!",
				Model:     "claude-sonnet",
				Timestamp: time.Date(2026, 2, 17, 9, 0, 5, 0, time.UTC),
				Tokens:    100,
			},
		},
	}
}

func exportTestFactory(store storage.Store) *cmdutil.Factory {
	return &cmdutil.Factory{
		SessionServiceFunc: func() (*service.SessionService, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store: store,
			}), nil
		},
		StoreFunc: func() (storage.Store, error) {
			return store, nil
		},
	}
}

func TestExport_AisyncFormat(t *testing.T) {
	ios := iostreams.Test()
	store := &mockStore{sessions: []*session.Session{testSession()}}

	f := exportTestFactory(store)
	f.IOStreams = ios

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

	var sess session.Session
	if jsonErr := json.Unmarshal([]byte(output), &sess); jsonErr != nil {
		t.Fatalf("unmarshal error: %v", jsonErr)
	}
	if sess.ID != "test-export-001" {
		t.Errorf("session ID = %q, want test-export-001", sess.ID)
	}
	if len(sess.Messages) != 2 {
		t.Errorf("messages = %d, want 2", len(sess.Messages))
	}
}

func TestExport_ClaudeFormat(t *testing.T) {
	ios := iostreams.Test()
	store := &mockStore{sessions: []*session.Session{testSession()}}

	f := exportTestFactory(store)
	f.IOStreams = ios

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
	store := &mockStore{sessions: []*session.Session{testSession()}}

	f := exportTestFactory(store)
	f.IOStreams = ios

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
	store := &mockStore{sessions: []*session.Session{testSession()}}

	f := exportTestFactory(store)
	f.IOStreams = ios

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
	store := &mockStore{sessions: []*session.Session{testSession()}}

	f := exportTestFactory(store)
	f.IOStreams = ios

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

	f := exportTestFactory(store)
	f.IOStreams = ios

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
