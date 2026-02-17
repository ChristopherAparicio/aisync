package restore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// mockProvider for restore tests.
type mockProvider struct {
	importErr   error
	imported    *domain.Session
	name        domain.ProviderName
	detectSumms []domain.SessionSummary
	canImport   bool
}

func (m *mockProvider) Name() domain.ProviderName { return m.name }

func (m *mockProvider) Detect(_, _ string) ([]domain.SessionSummary, error) {
	if m.detectSumms == nil {
		return nil, domain.ErrProviderNotDetected
	}
	return m.detectSumms, nil
}

func (m *mockProvider) Export(_ domain.SessionID, _ domain.StorageMode) (*domain.Session, error) {
	return nil, domain.ErrSessionNotFound
}

func (m *mockProvider) CanImport() bool { return m.canImport }

func (m *mockProvider) Import(s *domain.Session) error {
	if m.importErr != nil {
		return m.importErr
	}
	m.imported = s
	return nil
}

// mockStore for restore tests — stores sessions in memory.
type mockStore struct {
	sessions map[domain.SessionID]*domain.Session
	byBranch map[string]*domain.Session         // key = "projectPath:branch"
	links    map[string][]domain.SessionSummary // key = "linkType:ref"
}

func newMockStore() *mockStore {
	return &mockStore{
		sessions: make(map[domain.SessionID]*domain.Session),
		byBranch: make(map[string]*domain.Session),
		links:    make(map[string][]domain.SessionSummary),
	}
}

func (m *mockStore) Save(s *domain.Session) error {
	m.sessions[s.ID] = s
	key := s.ProjectPath + ":" + s.Branch
	m.byBranch[key] = s
	return nil
}

func (m *mockStore) Get(id domain.SessionID) (*domain.Session, error) {
	s, ok := m.sessions[id]
	if !ok {
		return nil, domain.ErrSessionNotFound
	}
	return s, nil
}

func (m *mockStore) GetByBranch(projectPath, branch string) (*domain.Session, error) {
	key := projectPath + ":" + branch
	s, ok := m.byBranch[key]
	if !ok {
		return nil, domain.ErrSessionNotFound
	}
	return s, nil
}

func (m *mockStore) List(_ domain.ListOptions) ([]domain.SessionSummary, error) { return nil, nil }
func (m *mockStore) Delete(_ domain.SessionID) error                            { return nil }

func (m *mockStore) AddLink(sessionID domain.SessionID, link domain.Link) error {
	key := string(link.LinkType) + ":" + link.Ref
	s, ok := m.sessions[sessionID]
	if !ok {
		return domain.ErrSessionNotFound
	}
	summary := domain.SessionSummary{
		ID:       s.ID,
		Provider: s.Provider,
		Branch:   s.Branch,
	}
	m.links[key] = append(m.links[key], summary)
	return nil
}

func (m *mockStore) GetByLink(linkType domain.LinkType, ref string) ([]domain.SessionSummary, error) {
	key := string(linkType) + ":" + ref
	summaries, ok := m.links[key]
	if !ok || len(summaries) == 0 {
		return nil, domain.ErrSessionNotFound
	}
	return summaries, nil
}
func (m *mockStore) Close() error { return nil }

func testFactory(t *testing.T, prov *mockProvider, store *mockStore) (*cmdutil.Factory, *iostreams.IOStreams, string) {
	t.Helper()
	ios := iostreams.Test()
	repoDir := testutil.InitTestRepo(t)

	if store == nil {
		store = newMockStore()
	}
	gitClient := git.NewClient(repoDir)

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		StoreFunc: func() (domain.Store, error) {
			return store, nil
		},
		RegistryFunc: func() *provider.Registry {
			if prov != nil {
				return provider.NewRegistry(prov)
			}
			return provider.NewRegistry()
		},
	}

	return f, ios, repoDir
}

func TestRestore_byBranch_contextFallback(t *testing.T) {
	// Provider that cannot import → falls back to CONTEXT.md
	prov := &mockProvider{
		name:      domain.ProviderClaudeCode,
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

	session := testutil.NewSession("restore-001")
	session.Provider = domain.ProviderClaudeCode
	session.ProjectPath = topLevel
	session.Branch = branch
	_ = store.Save(session)

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
		name:      domain.ProviderClaudeCode,
		canImport: false,
	}
	store := newMockStore()

	session := testutil.NewSession("restore-by-id")
	session.Provider = domain.ProviderClaudeCode
	_ = store.Save(session)

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
		name:      domain.ProviderClaudeCode,
		canImport: true,
	}
	store := newMockStore()

	session := testutil.NewSession("restore-ctx")
	session.Provider = domain.ProviderClaudeCode
	_ = store.Save(session)

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
		name:      domain.ProviderClaudeCode,
		canImport: false,
	}
	store := newMockStore()

	session := testutil.NewSession("restore-prov")
	session.Provider = domain.ProviderOpenCode
	_ = store.Save(session)

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
		name:      domain.ProviderClaudeCode,
		canImport: false,
	}
	store := newMockStore()

	session := testutil.NewSession("restore-agent")
	session.Provider = domain.ProviderClaudeCode
	_ = store.Save(session)

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
		name:      domain.ProviderClaudeCode,
		canImport: true,
	}
	store := newMockStore()

	session := testutil.NewSession("restore-native")
	session.Provider = domain.ProviderClaudeCode
	_ = store.Save(session)

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
		name:      domain.ProviderClaudeCode,
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
		name:      domain.ProviderClaudeCode,
		canImport: false,
	}
	store := newMockStore()

	session := testutil.NewSession("restore-pr-42")
	session.Provider = domain.ProviderClaudeCode
	_ = store.Save(session)

	// Link session to PR #42
	_ = store.AddLink(session.ID, domain.Link{
		LinkType: domain.LinkPR,
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
	if !strings.Contains(output, "PR #42") {
		t.Error("expected PR reference in output")
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
