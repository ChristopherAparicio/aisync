package restore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/config"
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

func testFactory(t *testing.T, prov *mockProvider, store *testutil.MockStore) (*cmdutil.Factory, *iostreams.IOStreams, string) {
	t.Helper()
	ios := iostreams.Test()
	repoDir := testutil.InitTestRepo(t)

	if store == nil {
		store = testutil.NewMockStore()
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
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store:    store,
				Registry: registry,
				Git:      gitClient,
			}), nil
		},
	}

	return f, ios, repoDir
}

// testFactoryWithConfig is like testFactory but wires a Config into the
// SessionServiceConfig and exposes it via Factory.ConfigFunc.
func testFactoryWithConfig(t *testing.T, prov *mockProvider, store *testutil.MockStore, cfg *config.Config) (*cmdutil.Factory, *iostreams.IOStreams, string) {
	t.Helper()
	ios := iostreams.Test()
	repoDir := testutil.InitTestRepo(t)

	if store == nil {
		store = testutil.NewMockStore()
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
		ConfigFunc: func() (*config.Config, error) {
			return cfg, nil
		},
		RegistryFunc: func() *provider.Registry {
			return registry
		},
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store:    store,
				Registry: registry,
				Git:      gitClient,
				Config:   cfg,
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
	store := testutil.NewMockStore()

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
	store := testutil.NewMockStore()

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
	store := testutil.NewMockStore()

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
	store := testutil.NewMockStore()

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
	store := testutil.NewMockStore()

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
	store := testutil.NewMockStore()

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

	flags := []string{"session", "provider", "agent", "as-context", "pr",
		"clean-errors", "strip-empty", "redact-secrets", "exclude"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestParseExcludeFlag_indices(t *testing.T) {
	f, err := parseExcludeFlag("0,3,5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.Indices[0] || !f.Indices[3] || !f.Indices[5] {
		t.Error("expected indices 0, 3, 5 to be set")
	}
	if len(f.Roles) != 0 {
		t.Errorf("expected no roles, got %v", f.Roles)
	}
}

func TestParseExcludeFlag_roles(t *testing.T) {
	f, err := parseExcludeFlag("system")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.Roles) != 1 || f.Roles[0] != session.RoleSystem {
		t.Errorf("expected [system], got %v", f.Roles)
	}
}

func TestParseExcludeFlag_pattern(t *testing.T) {
	f, err := parseExcludeFlag("/debug|trace/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.ContentPattern == nil {
		t.Fatal("expected content pattern to be set")
	}
	if !f.ContentPattern.MatchString("debug info") {
		t.Error("pattern should match 'debug info'")
	}
}

func TestParseExcludeFlag_combined(t *testing.T) {
	f, err := parseExcludeFlag("0,system,/error/,2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.Indices[0] || !f.Indices[2] {
		t.Error("expected indices 0 and 2")
	}
	if len(f.Roles) != 1 || f.Roles[0] != session.RoleSystem {
		t.Errorf("expected [system], got %v", f.Roles)
	}
	if f.ContentPattern == nil {
		t.Error("expected content pattern")
	}
}

func TestParseExcludeFlag_invalidRole(t *testing.T) {
	_, err := parseExcludeFlag("invalid_role_name")
	if err == nil {
		t.Error("expected error for invalid role")
	}
}

func TestParseExcludeFlag_invalidPattern(t *testing.T) {
	_, err := parseExcludeFlag("/[invalid/")
	if err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestParseExcludeFlag_negativeIndex(t *testing.T) {
	_, err := parseExcludeFlag("-1")
	if err == nil {
		t.Error("expected error for negative index")
	}
}

func TestBuildFilters_noFlags(t *testing.T) {
	opts := &Options{}
	filters, err := buildFilters(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(filters) != 0 {
		t.Errorf("expected 0 filters, got %d", len(filters))
	}
}

func TestBuildFilters_allFlags(t *testing.T) {
	opts := &Options{
		CleanErrors:   true,
		StripEmpty:    true,
		RedactSecrets: true,
		ExcludeFlag:   "0,system",
	}
	filters, err := buildFilters(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(filters) != 4 {
		t.Fatalf("expected 4 filters, got %d", len(filters))
	}

	// Verify order: exclude → empty → errors → secrets
	names := make([]string, len(filters))
	for i, f := range filters {
		names[i] = f.Name()
	}
	if names[0] != "message-excluder" {
		t.Errorf("first filter should be message-excluder, got %q", names[0])
	}
	if names[1] != "empty-message" {
		t.Errorf("second filter should be empty-message, got %q", names[1])
	}
	if names[2] != "error-cleaner" {
		t.Errorf("third filter should be error-cleaner, got %q", names[2])
	}
	if names[3] != "secret-redactor" {
		t.Errorf("fourth filter should be secret-redactor, got %q", names[3])
	}
}

func TestBuildFilters_invalidExclude(t *testing.T) {
	opts := &Options{ExcludeFlag: "bogus_role"}
	_, err := buildFilters(opts)
	if err == nil {
		t.Error("expected error for invalid exclude flag")
	}
}

func TestRestore_withFilterFlags(t *testing.T) {
	prov := &mockProvider{
		name:      session.ProviderClaudeCode,
		canImport: false,
	}
	store := testutil.NewMockStore()

	sess := testutil.NewSession("restore-filter-test")
	sess.Provider = session.ProviderClaudeCode
	sess.Messages = []session.Message{
		{ID: "m1", Role: session.RoleUser, Content: "fix bug"},
		{ID: "m2", Role: session.RoleAssistant, Content: ""}, // empty
		{ID: "m3", Role: session.RoleAssistant, Content: "Done!"},
	}
	_ = store.Save(sess)

	f, ios, _ := testFactory(t, prov, store)

	opts := &Options{
		IO:          ios,
		Factory:     f,
		SessionFlag: "restore-filter-test",
		StripEmpty:  true,
	}

	err := runRestore(opts)
	if err != nil {
		t.Fatalf("runRestore() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Smart Restore filters applied") {
		t.Error("expected filter results in output")
	}
	if !strings.Contains(output, "empty-message") {
		t.Error("expected empty-message filter name in output")
	}
}

func TestRestore_byPR(t *testing.T) {
	prov := &mockProvider{
		name:      session.ProviderClaudeCode,
		canImport: false,
	}
	store := testutil.NewMockStore()

	sess := testutil.NewSession("restore-pr-42")
	sess.Provider = session.ProviderClaudeCode
	_ = store.Save(sess)

	// Populate PR sessions (the new resolveSessionFromPR path uses GetSessionsForPR).
	store.PRSessions["myorg/myrepo#42"] = []session.Summary{
		{ID: sess.ID, Provider: sess.Provider, Summary: "PR fix"},
	}

	cfg, err := config.New(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	_ = cfg.Set("github.default_owner", "myorg")
	_ = cfg.Set("github.default_repo", "myrepo")

	f, ios, _ := testFactoryWithConfig(t, prov, store, cfg)

	opts := &Options{
		IO:      ios,
		Factory: f,
		PRFlag:  42,
	}

	err = runRestore(opts)
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
	store := testutil.NewMockStore()

	// Config must be set so resolveSessionFromPR gets past the "not configured" check.
	cfg, err := config.New(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	_ = cfg.Set("github.default_owner", "myorg")
	_ = cfg.Set("github.default_repo", "myrepo")

	f, ios, _ := testFactoryWithConfig(t, nil, store, cfg)

	opts := &Options{
		IO:      ios,
		Factory: f,
		PRFlag:  999,
	}

	err = runRestore(opts)
	if err == nil {
		t.Fatal("expected error when no session linked to PR")
	}
	if !strings.Contains(err.Error(), "PR #999") {
		t.Errorf("error should mention PR #999, got: %v", err)
	}
}

// ── New flag registration tests ──

func TestNewCmdRestore_newFlags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdRestore(f)

	newFlags := []string{"file", "worktree", "pick", "fix-orphans"}
	for _, name := range newFlags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag to be registered", name)
		}
	}
}

// ── pickSessionForPR tests ──

// testPickStore is a minimal pickStore implementation for picker tests.
type testPickStore struct {
	prSessions  map[int][]session.Summary
	fileEntries map[string][]session.BlameEntry
}

func (s *testPickStore) GetSessionsForPR(_, _ string, prNumber int) ([]session.Summary, error) {
	if summaries, ok := s.prSessions[prNumber]; ok {
		return summaries, nil
	}
	return nil, nil
}

func (s *testPickStore) GetSessionsByFile(query session.BlameQuery) ([]session.BlameEntry, error) {
	if entries, ok := s.fileEntries[query.FilePath]; ok {
		return entries, nil
	}
	return nil, nil
}

func TestPickSessionForPR_singleSession(t *testing.T) {
	ios := iostreams.Test()
	store := &testPickStore{
		prSessions: map[int][]session.Summary{
			42: {{ID: "sess-42", Provider: "claude-code"}},
		},
	}

	opts := &Options{IO: ios}
	sid, err := pickSessionForPR(opts, store, 42, "org", "repo")
	if err != nil {
		t.Fatalf("pickSessionForPR() error: %v", err)
	}
	if sid != "sess-42" {
		t.Errorf("session ID = %q, want %q", sid, "sess-42")
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Only one session") {
		t.Error("expected 'Only one session' message for single result")
	}
}

func TestPickSessionForPR_noSessions(t *testing.T) {
	ios := iostreams.Test()
	store := &testPickStore{prSessions: map[int][]session.Summary{}}

	opts := &Options{IO: ios}
	_, err := pickSessionForPR(opts, store, 99, "org", "repo")
	if err == nil {
		t.Fatal("expected error when no sessions linked to PR")
	}
}

func TestPickSessionForPR_multipleSessions(t *testing.T) {
	ios := iostreams.Test()
	// Simulate user entering "2" in stdin
	ios.In = strings.NewReader("2\n")

	store := &testPickStore{
		prSessions: map[int][]session.Summary{
			42: {
				{ID: "sess-a", Provider: "claude-code", Summary: "First"},
				{ID: "sess-b", Provider: "opencode", Summary: "Second"},
			},
		},
	}

	opts := &Options{IO: ios}
	sid, err := pickSessionForPR(opts, store, 42, "org", "repo")
	if err != nil {
		t.Fatalf("pickSessionForPR() error: %v", err)
	}
	if sid != "sess-b" {
		t.Errorf("session ID = %q, want %q (user chose #2)", sid, "sess-b")
	}
}

// ── pickSessionForFile tests ──

func TestPickSessionForFile_singleSession(t *testing.T) {
	ios := iostreams.Test()
	store := &testPickStore{
		fileEntries: map[string][]session.BlameEntry{
			"src/auth.go": {{SessionID: "sess-file-1", Provider: "claude-code"}},
		},
	}

	opts := &Options{IO: ios}
	sid, err := pickSessionForFile(opts, store, "src/auth.go")
	if err != nil {
		t.Fatalf("pickSessionForFile() error: %v", err)
	}
	if sid != "sess-file-1" {
		t.Errorf("session ID = %q, want %q", sid, "sess-file-1")
	}
}

func TestPickSessionForFile_noSessions(t *testing.T) {
	ios := iostreams.Test()
	store := &testPickStore{fileEntries: map[string][]session.BlameEntry{}}

	opts := &Options{IO: ios}
	_, err := pickSessionForFile(opts, store, "nonexistent.go")
	if err == nil {
		t.Fatal("expected error when no sessions found for file")
	}
}

func TestPickSessionForFile_multipleSessions(t *testing.T) {
	ios := iostreams.Test()
	ios.In = strings.NewReader("1\n")

	store := &testPickStore{
		fileEntries: map[string][]session.BlameEntry{
			"main.go": {
				{SessionID: "sess-x", Provider: "claude-code", Summary: "Fix main"},
				{SessionID: "sess-y", Provider: "opencode", Summary: "Refactor"},
			},
		},
	}

	opts := &Options{IO: ios}
	sid, err := pickSessionForFile(opts, store, "main.go")
	if err != nil {
		t.Fatalf("pickSessionForFile() error: %v", err)
	}
	if sid != "sess-x" {
		t.Errorf("session ID = %q, want %q (user chose #1)", sid, "sess-x")
	}
}

// ── promptPickFromSummaries tests ──

func TestPromptPickFromSummaries_validChoice(t *testing.T) {
	ios := iostreams.Test()
	ios.In = strings.NewReader("2\n")

	summaries := []session.Summary{
		{ID: "s1", Provider: "claude-code", Summary: "Task A"},
		{ID: "s2", Provider: "opencode", Summary: "Task B"},
		{ID: "s3", Provider: "claude-code", Summary: "Task C"},
	}

	opts := &Options{IO: ios}
	sid, err := promptPickFromSummaries(opts, summaries)
	if err != nil {
		t.Fatalf("promptPickFromSummaries() error: %v", err)
	}
	if sid != "s2" {
		t.Errorf("session ID = %q, want %q", sid, "s2")
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Task A") {
		t.Error("expected 'Task A' in table output")
	}
	if !strings.Contains(output, "Task B") {
		t.Error("expected 'Task B' in table output")
	}
}

func TestPromptPickFromSummaries_invalidChoice(t *testing.T) {
	ios := iostreams.Test()
	ios.In = strings.NewReader("99\n")

	summaries := []session.Summary{
		{ID: "s1", Provider: "claude-code"},
	}

	opts := &Options{IO: ios}
	_, err := promptPickFromSummaries(opts, summaries)
	if err == nil {
		t.Fatal("expected error for out-of-range choice")
	}
}

func TestPromptPickFromSummaries_noInput(t *testing.T) {
	ios := iostreams.Test()
	ios.In = strings.NewReader("") // empty input

	summaries := []session.Summary{
		{ID: "s1", Provider: "claude-code"},
	}

	opts := &Options{IO: ios}
	_, err := promptPickFromSummaries(opts, summaries)
	if err == nil {
		t.Fatal("expected error when no input received")
	}
}

// ── truncateID tests ──

func TestTruncateID(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"abcdef", 10, "abcdef"},
		{"abcdefghij", 10, "abcdefghij"},
		{"abcdefghijk", 10, "abcdefg..."},
		{"a", 5, "a"},
	}

	for _, tt := range tests {
		got := truncateID(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncateID(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}
