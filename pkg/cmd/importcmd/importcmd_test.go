package importcmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
	saved []*session.Session
}

func (m *mockStore) Save(s *session.Session) error {
	m.saved = append(m.saved, s)
	return nil
}
func (m *mockStore) Get(_ session.ID) (*session.Session, error) {
	return nil, session.ErrSessionNotFound
}
func (m *mockStore) GetByBranch(_, _ string) (*session.Session, error) {
	return nil, session.ErrSessionNotFound
}
func (m *mockStore) List(_ session.ListOptions) ([]session.Summary, error) {
	return nil, nil
}
func (m *mockStore) Delete(_ session.ID) error                  { return nil }
func (m *mockStore) AddLink(_ session.ID, _ session.Link) error { return nil }
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

func testSession() *session.Session {
	return &session.Session{
		ID:          "import-test-001",
		Provider:    session.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feat/import",
		ProjectPath: "/tmp/test",
		StorageMode: session.StorageModeCompact,
		Summary:     "Session for import test",
		Version:     1,
		ExportedAt:  time.Date(2026, 2, 17, 10, 0, 0, 0, time.UTC),
		CreatedAt:   time.Date(2026, 2, 17, 9, 0, 0, 0, time.UTC),
		Messages: []session.Message{
			{
				ID:        "msg-001",
				Role:      session.RoleUser,
				Content:   "Hello",
				Timestamp: time.Date(2026, 2, 17, 9, 0, 0, 0, time.UTC),
			},
			{
				ID:        "msg-002",
				Role:      session.RoleAssistant,
				Content:   "Hi!",
				Timestamp: time.Date(2026, 2, 17, 9, 0, 5, 0, time.UTC),
			},
		},
	}
}

func writeTestFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func importTestFactory(store storage.Store) *cmdutil.Factory {
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

func TestImport_AisyncJSON(t *testing.T) {
	ios := iostreams.Test()
	store := &mockStore{}

	sess := testSession()
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	filePath := writeTestFile(t, "session.json", data)

	f := importTestFactory(store)
	f.IOStreams = ios

	opts := &Options{
		IO:       ios,
		Factory:  f,
		IntoFlag: "aisync",
	}

	err = runImport(opts, filePath)
	if err != nil {
		t.Fatalf("runImport() error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Stored session") {
		t.Errorf("expected 'Stored session' in output, got: %s", output)
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected 1 saved session, got %d", len(store.saved))
	}
	if store.saved[0].Summary != "Session for import test" {
		t.Errorf("saved summary = %q, want 'Session for import test'", store.saved[0].Summary)
	}
}

func TestImport_ClaudeJSONL(t *testing.T) {
	ios := iostreams.Test()
	store := &mockStore{}

	jsonl := `{"type":"summary","summary":"Imported from Claude"}
{"type":"user","uuid":"u1","timestamp":"2026-02-17T09:00:00Z","sessionId":"sess1","gitBranch":"main","cwd":"/tmp","message":{"role":"user","content":"Hello"},"isSidechain":false}
{"type":"assistant","uuid":"a1","timestamp":"2026-02-17T09:00:05Z","sessionId":"sess1","message":{"role":"assistant","model":"claude-sonnet","id":"msg1","type":"message","content":[{"type":"text","text":"Hi!"}]},"isSidechain":false}`

	filePath := writeTestFile(t, "session.jsonl", []byte(jsonl))

	f := importTestFactory(store)
	f.IOStreams = ios

	opts := &Options{
		IO:         ios,
		Factory:    f,
		FormatFlag: "claude",
		IntoFlag:   "aisync",
	}

	err := runImport(opts, filePath)
	if err != nil {
		t.Fatalf("runImport() error: %v", err)
	}

	if len(store.saved) != 1 {
		t.Fatalf("expected 1 saved session, got %d", len(store.saved))
	}
	if store.saved[0].Summary != "Imported from Claude" {
		t.Errorf("saved summary = %q, want 'Imported from Claude'", store.saved[0].Summary)
	}
	if store.saved[0].Provider != session.ProviderClaudeCode {
		t.Errorf("provider = %q, want claude-code", store.saved[0].Provider)
	}
}

func TestImport_EmptyFile(t *testing.T) {
	ios := iostreams.Test()
	store := &mockStore{}

	filePath := writeTestFile(t, "empty.json", []byte{})

	f := importTestFactory(store)
	f.IOStreams = ios

	opts := &Options{
		IO:      ios,
		Factory: f,
	}

	err := runImport(opts, filePath)
	if err == nil {
		t.Fatal("expected error for empty file")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected 'empty' error, got: %v", err)
	}
}

func TestImport_NonexistentFile(t *testing.T) {
	ios := iostreams.Test()
	store := &mockStore{}

	f := importTestFactory(store)
	f.IOStreams = ios

	opts := &Options{
		IO:      ios,
		Factory: f,
	}

	err := runImport(opts, "/tmp/nonexistent-session-file.json")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestImport_UnknownFormat(t *testing.T) {
	ios := iostreams.Test()
	store := &mockStore{}

	filePath := writeTestFile(t, "session.xml", []byte("<session/>"))

	f := importTestFactory(store)
	f.IOStreams = ios

	opts := &Options{
		IO:         ios,
		Factory:    f,
		FormatFlag: "xml",
	}

	err := runImport(opts, filePath)
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
	if !strings.Contains(err.Error(), "unknown format") {
		t.Errorf("expected 'unknown format' error, got: %v", err)
	}
}

func TestImport_UnknownTarget(t *testing.T) {
	ios := iostreams.Test()
	store := &mockStore{}

	sess := testSession()
	data, _ := json.Marshal(sess)
	filePath := writeTestFile(t, "session.json", data)

	f := importTestFactory(store)
	f.IOStreams = ios

	opts := &Options{
		IO:       ios,
		Factory:  f,
		IntoFlag: "cursor",
	}

	err := runImport(opts, filePath)
	if err == nil {
		t.Fatal("expected error for unknown target")
	}
	if !strings.Contains(err.Error(), "unknown target") {
		t.Errorf("expected 'unknown target' error, got: %v", err)
	}
}
