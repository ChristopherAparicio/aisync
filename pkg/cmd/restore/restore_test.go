package restore

import (
	"bytes"
	"os"
	"path/filepath"
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

// mockProvider for restore tests.
type mockProvider struct {
	importErr   error
	imported    *session.Session
	name        session.ProviderName
	detectSumms []session.Summary
	canImport   bool
}

func (m *mockProvider) Name() session.ProviderName { return m.name }

func (m *mockProvider) Detect(_, _ string) ([]session.Summary, error) {
	if m.detectSumms == nil {
		return nil, session.ErrProviderNotDetected
	}
	return m.detectSumms, nil
}

func (m *mockProvider) Export(_ session.ID, _ session.StorageMode) (*session.Session, error) {
	return nil, session.ErrSessionNotFound
}

func (m *mockProvider) CanImport() bool { return m.canImport }

func (m *mockProvider) Import(s *session.Session) error {
	if m.importErr != nil {
		return m.importErr
	}
	m.imported = s
	return nil
}

// mockStore for restore tests — stores sessions in memory.
type mockStore struct {
	sessions map[session.ID]*session.Session
	byBranch map[string]*session.Session  // key = "projectPath:branch"
	links    map[string][]session.Summary // key = "linkType:ref"
}

func newMockStore() *mockStore {
	return &mockStore{
		sessions: make(map[session.ID]*session.Session),
		byBranch: make(map[string]*session.Session),
		links:    make(map[string][]session.Summary),
	}
}

func (m *mockStore) Save(s *session.Session) error {
	m.sessions[s.ID] = s
	key := s.ProjectPath + ":" + s.Branch
	m.byBranch[key] = s
	return nil
}

func (m *mockStore) Get(id session.ID) (*session.Session, error) {
	s, ok := m.sessions[id]
	if !ok {
		return nil, session.ErrSessionNotFound
	}
	return s, nil
}

func (m *mockStore) GetByBranch(projectPath, branch string) (*session.Session, error) {
	key := projectPath + ":" + branch
	s, ok := m.byBranch[key]
	if !ok {
		return nil, session.ErrSessionNotFound
	}
	return s, nil
}

func (m *mockStore) List(_ session.ListOptions) ([]session.Summary, error) { return nil, nil }
func (m *mockStore) Delete(_ session.ID) error                             { return nil }

func (m *mockStore) AddLink(sessionID session.ID, link session.Link) error {
	key := string(link.LinkType) + ":" + link.Ref
	s, ok := m.sessions[sessionID]
	if !ok {
		return session.ErrSessionNotFound
	}
	summary := session.Summary{
		ID:       s.ID,
		Provider: s.Provider,
		Branch:   s.Branch,
	}
	m.links[key] = append(m.links[key], summary)
	return nil
}

func (m *mockStore) GetByLink(linkType session.LinkType, ref string) ([]session.Summary, error) {
	key := string(linkType) + ":" + ref
	summaries, ok := m.links[key]
	if !ok || len(summaries) == 0 {
		return nil, session.ErrSessionNotFound
	}
	return summaries, nil
}
func (m *mockStore) Close() error                                   { return nil }
func (m *mockStore) SaveUser(_ *session.User) error                 { return nil }
func (m *mockStore) GetUser(_ session.ID) (*session.User, error)    { return nil, nil }
func (m *mockStore) GetUserByEmail(_ string) (*session.User, error) { return nil, nil }
func (m *mockStore) Search(_ session.SearchQuery) (*session.SearchResult, error) {
	return &session.SearchResult{}, nil
}

func testFactory(t *testing.T, prov *mockProvider, store *mockStore) (*cmdutil.Factory, *iostreams.IOStreams, string) {
	t.Helper()
	ios := iostreams.Test()
	repoDir := testutil.InitTestRepo(t)

	if store == nil {
		store = newMockStore()
	}
	gitClient := git.NewClient(repoDir)

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

	return f, ios, repoDir
}

func TestRestore_byBranch_contextFallback(t *testing.T) {
	// Provider that cannot import → falls back to CONTEXT.md
	prov := &mockProvider{
		name:      session.ProviderClaudeCode,
		canImport: false,
	}
	store := newMockStore()

	f, ios, repoDir := testFactory(t, prov, store)

	// Use gitClient.TopLevel() to get the resolved path (handles macOS /private symlinks)
	gitClient := git.NewClient(repoDir)
	topLevel, err := gitClient.TopLevel()
	if err != nil {
		t.Fatal(err)
	}
	branch, err := gitClient.CurrentBranch()
	if err != nil {
		t.Fatal(err)
	}

	sess := testutil.NewSession("restore-001")
	sess.Provider = session.ProviderClaudeCode
	sess.ProjectPath = topLevel
	sess.Branch = branch
	_ = store.Save(sess)

	opts := &Options{
		IO:      ios,
		Factory: f,
	}

	err = runRestore(opts)
	if err != nil {
		t.Fatalf("runRestore() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Restored session") {
		t.Error("expected 'Restored session' in output")
	}
	if !strings.Contains(output, "CONTEXT.md") {
		t.Error("expected CONTEXT.md fallback in output")
	}

	// Verify CONTEXT.md file was created
	contextPath := filepath.Join(topLevel, "CONTEXT.md")
	if _, statErr := os.Stat(contextPath); os.IsNotExist(statErr) {
		t.Error("expected CONTEXT.md file to be created")
	}
}

func TestRestore_bySessionID(t *testing.T) {
	prov := &mockProvider{
		name:      session.ProviderClaudeCode,
		canImport: false,
	}
	store := newMockStore()

	sess := testutil.NewSession("restore-by-id")
	sess.Provider = session.ProviderClaudeCode
	_ = store.Save(sess)

	f, ios, repoDir := testFactory(t, prov, store)

	// The session is stored with a different branch/project, so we use --session flag
	_ = repoDir

	opts := &Options{
		IO:          ios,
		Factory:     f,
		SessionFlag: "restore-by-id",
	}

	err := runRestore(opts)
	if err != nil {
		t.Fatalf("runRestore() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "restore-by-id") {
		t.Error("expected session ID in output")
	}
}

func TestRestore_asContext(t *testing.T) {
	prov := &mockProvider{
		name:      session.ProviderClaudeCode,
		canImport: true,
	}
	store := newMockStore()

	sess := testutil.NewSession("restore-ctx")
	sess.Provider = session.ProviderClaudeCode
	_ = store.Save(sess)

	f, ios, repoDir := testFactory(t, prov, store)
	_ = repoDir

	opts := &Options{
		IO:          ios,
		Factory:     f,
		SessionFlag: "restore-ctx",
		AsContext:   true,
	}

	err := runRestore(opts)
	if err != nil {
		t.Fatalf("runRestore() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "CONTEXT.md") {
		t.Error("expected CONTEXT.md method in output")
	}
}

func TestRestore_withProviderFlag(t *testing.T) {
	// Session from opencode, restore with --provider claude-code
	// Provider cannot import → falls back to context
	prov := &mockProvider{
		name:      session.ProviderClaudeCode,
		canImport: false,
	}
	store := newMockStore()

	sess := testutil.NewSession("restore-prov")
	sess.Provider = session.ProviderOpenCode
	_ = store.Save(sess)

	f, ios, _ := testFactory(t, prov, store)

	opts := &Options{
		IO:           ios,
		Factory:      f,
		SessionFlag:  "restore-prov",
		ProviderFlag: "claude-code",
	}

	err := runRestore(opts)
	if err != nil {
		t.Fatalf("runRestore() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "CONTEXT.md") {
		t.Error("expected CONTEXT.md fallback when provider can't import")
	}
}

func TestRestore_withAgentFlag(t *testing.T) {
	prov := &mockProvider{
		name:      session.ProviderClaudeCode,
		canImport: false,
	}
	store := newMockStore()

	sess := testutil.NewSession("restore-agent")
	sess.Provider = session.ProviderClaudeCode
	_ = store.Save(sess)

	f, ios, _ := testFactory(t, prov, store)

	opts := &Options{
		IO:          ios,
		Factory:     f,
		SessionFlag: "restore-agent",
		AgentFlag:   "custom-agent",
	}

	err := runRestore(opts)
	if err != nil {
		t.Fatalf("runRestore() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Restored session") {
		t.Error("expected 'Restored session' in output")
	}
}

func TestRestore_nativeImport(t *testing.T) {
	// Provider that can import → should use native method
	prov := &mockProvider{
		name:      session.ProviderClaudeCode,
		canImport: true,
	}
	store := newMockStore()

	sess := testutil.NewSession("restore-native")
	sess.Provider = session.ProviderClaudeCode
	_ = store.Save(sess)

	f, ios, _ := testFactory(t, prov, store)

	opts := &Options{
		IO:          ios,
		Factory:     f,
		SessionFlag: "restore-native",
	}

	err := runRestore(opts)
	if err != nil {
		t.Fatalf("runRestore() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "native import") {
		t.Error("expected 'native import' method in output")
	}

	// Verify import was called
	if prov.imported == nil {
		t.Error("expected provider.Import() to be called")
	}
}

func TestRestore_invalidProvider(t *testing.T) {
	f, ios, _ := testFactory(t, nil, nil)

	opts := &Options{
		IO:           ios,
		Factory:      f,
		SessionFlag:  "some-id",
		ProviderFlag: "nonexistent",
	}

	err := runRestore(opts)
	if err == nil {
		t.Fatal("expected error for invalid provider flag")
	}
}

func TestRestore_sessionNotFound(t *testing.T) {
	prov := &mockProvider{
		name:      session.ProviderClaudeCode,
		canImport: true,
	}
	f, ios, _ := testFactory(t, prov, nil)

	opts := &Options{
		IO:          ios,
		Factory:     f,
		SessionFlag: "does-not-exist",
	}

	err := runRestore(opts)
	if err == nil {
		t.Fatal("expected error when session not found")
	}
}

func TestNewCmdRestore_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdRestore(f)

	flags := []string{"session", "provider", "agent", "as-context", "pr"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestRestore_byPR(t *testing.T) {
	prov := &mockProvider{
		name:      session.ProviderClaudeCode,
		canImport: false,
	}
	store := newMockStore()

	sess := testutil.NewSession("restore-pr-42")
	sess.Provider = session.ProviderClaudeCode
	_ = store.Save(sess)

	// Link session to PR #42
	_ = store.AddLink(sess.ID, session.Link{
		LinkType: session.LinkPR,
		Ref:      "42",
	})

	f, ios, _ := testFactory(t, prov, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
		PRFlag:  42,
	}

	err := runRestore(opts)
	if err != nil {
		t.Fatalf("runRestore() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "restore-pr-42") {
		t.Error("expected session ID in output")
	}
	if !strings.Contains(output, "Restored session") {
		t.Error("expected 'Restored session' in output")
	}
}

func TestRestore_byPR_notFound(t *testing.T) {
	store := newMockStore()
	f, ios, _ := testFactory(t, nil, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
		PRFlag:  999,
	}

	err := runRestore(opts)
	if err == nil {
		t.Fatal("expected error when no session linked to PR")
	}
	if !strings.Contains(err.Error(), "PR #999") {
		t.Errorf("error should mention PR #999, got: %v", err)
	}
}
