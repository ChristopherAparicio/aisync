package capture

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// mockProvider returns a session when Detect + Export are called.
type mockProvider struct {
	session *session.Session
	name    session.ProviderName
}

func (m *mockProvider) Name() session.ProviderName { return m.name }

func (m *mockProvider) Detect(_, _ string) ([]session.Summary, error) {
	if m.session == nil {
		return nil, session.ErrProviderNotDetected
	}
	return []session.Summary{{
		ID:       m.session.ID,
		Provider: m.name,
		Agent:    m.session.Agent,
		Branch:   m.session.Branch,
		Summary:  m.session.Summary,
	}}, nil
}

func (m *mockProvider) Export(_ session.ID, _ session.StorageMode) (*session.Session, error) {
	if m.session == nil {
		return nil, session.ErrSessionNotFound
	}
	return m.session, nil
}

func (m *mockProvider) CanImport() bool                 { return false }
func (m *mockProvider) Import(_ *session.Session) error { return session.ErrImportNotSupported }

// mockStore saves sessions in memory.
type mockStore struct {
	saved []*session.Session
}

func (m *mockStore) Save(s *session.Session) error { m.saved = append(m.saved, s); return nil }
func (m *mockStore) Get(_ session.ID) (*session.Session, error) {
	return nil, session.ErrSessionNotFound
}
func (m *mockStore) GetLatestByBranch(_, _ string) (*session.Session, error) {
	return nil, session.ErrSessionNotFound
}
func (m *mockStore) CountByBranch(_, _ string) (int, error)                { return 0, nil }
func (m *mockStore) List(_ session.ListOptions) ([]session.Summary, error) { return nil, nil }
func (m *mockStore) Delete(_ session.ID) error                             { return nil }
func (m *mockStore) AddLink(_ session.ID, _ session.Link) error            { return nil }
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

func testFactory(t *testing.T, prov *mockProvider) (*cmdutil.Factory, *iostreams.IOStreams, *mockStore) {
	t.Helper()
	ios := iostreams.Test()
	repoDir := testutil.InitTestRepo(t)
	gitClient := git.NewClient(repoDir)
	store := &mockStore{}

	registry := provider.NewRegistry()
	if prov != nil {
		registry = provider.NewRegistry(prov)
	}

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		StoreFunc: func() (storage.Store, error) {
			return store, nil
		},
		RegistryFunc: func() *provider.Registry {
			return registry
		},
		SessionServiceFunc: func() (*service.SessionService, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store:    store,
				Registry: registry,
				Git:      gitClient,
			}), nil
		},
	}

	return f, ios, store
}

func TestCapture_success(t *testing.T) {
	sess := testutil.NewSession("cap-001")
	prov := &mockProvider{
		name:    session.ProviderClaudeCode,
		session: sess,
	}
	f, ios, store := testFactory(t, prov)

	opts := &Options{
		IO:      ios,
		Factory: f,
	}

	err := runCapture(opts)
	if err != nil {
		t.Fatalf("runCapture() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Captured session") {
		t.Error("expected 'Captured session' in output")
	}
	if !strings.Contains(output, "claude-code") {
		t.Error("expected provider name in output")
	}

	if len(store.saved) != 1 {
		t.Fatalf("expected 1 saved session, got %d", len(store.saved))
	}
}

func TestCapture_withMessage(t *testing.T) {
	sess := testutil.NewSession("cap-msg")
	prov := &mockProvider{
		name:    session.ProviderClaudeCode,
		session: sess,
	}
	f, ios, store := testFactory(t, prov)

	opts := &Options{
		IO:      ios,
		Factory: f,
		Message: "Custom summary",
	}

	err := runCapture(opts)
	if err != nil {
		t.Fatalf("runCapture() error = %v", err)
	}

	if len(store.saved) != 1 {
		t.Fatal("expected 1 saved session")
	}
	if store.saved[0].Summary != "Custom summary" {
		t.Errorf("Summary = %q, want 'Custom summary'", store.saved[0].Summary)
	}
}

func TestCapture_withModeFlag(t *testing.T) {
	sess := testutil.NewSession("cap-mode")
	prov := &mockProvider{
		name:    session.ProviderClaudeCode,
		session: sess,
	}
	f, ios, _ := testFactory(t, prov)

	opts := &Options{
		IO:       ios,
		Factory:  f,
		ModeFlag: "summary",
	}

	err := runCapture(opts)
	if err != nil {
		t.Fatalf("runCapture() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "summary") {
		t.Error("expected 'summary' mode in output")
	}
}

func TestCapture_invalidMode(t *testing.T) {
	sess := testutil.NewSession("cap-bad-mode")
	prov := &mockProvider{
		name:    session.ProviderClaudeCode,
		session: sess,
	}
	f, ios, _ := testFactory(t, prov)

	opts := &Options{
		IO:       ios,
		Factory:  f,
		ModeFlag: "invalid",
	}

	err := runCapture(opts)
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

func TestCapture_invalidProvider(t *testing.T) {
	f, ios, _ := testFactory(t, nil)

	opts := &Options{
		IO:           ios,
		Factory:      f,
		ProviderFlag: "nonexistent",
	}

	err := runCapture(opts)
	if err == nil {
		t.Fatal("expected error for invalid provider")
	}
}

func TestCapture_noProviderDetected(t *testing.T) {
	// Provider with no session
	prov := &mockProvider{
		name:    session.ProviderClaudeCode,
		session: nil,
	}
	f, ios, _ := testFactory(t, prov)

	opts := &Options{
		IO:      ios,
		Factory: f,
	}

	err := runCapture(opts)
	if err == nil {
		t.Fatal("expected error when no provider detected")
	}
}

func TestCapture_autoModeSilent(t *testing.T) {
	// Auto mode silently succeeds even when no provider is found
	f, ios, _ := testFactory(t, nil)

	opts := &Options{
		IO:      ios,
		Factory: f,
		Auto:    true,
	}

	err := runCapture(opts)
	if err != nil {
		t.Fatalf("auto mode should not return error, got: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if output != "" {
		t.Errorf("auto mode should produce no output, got %q", output)
	}
}

func TestNewCmdCapture_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdCapture(f)

	flags := []string{"provider", "mode", "message", "auto"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}
