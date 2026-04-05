package resumecmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
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

// resumeTestFactory builds a Factory backed by a real temp git repo and a MockStore.
// The git repo has an initial commit so TopLevel() and Checkout() can run.
func resumeTestFactory(t *testing.T, store *testutil.MockStore) (*cmdutil.Factory, *iostreams.IOStreams) {
	t.Helper()
	ios := iostreams.Test()
	repoDir := testutil.InitTestRepo(t)

	if store == nil {
		store = testutil.NewMockStore()
	}
	gitClient := git.NewClient(repoDir)

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		StoreFunc: func() (storage.Store, error) { return store, nil },
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store:    store,
				Git:      gitClient,
				Registry: provider.NewRegistry(),
			}), nil
		},
	}

	return f, ios
}

// commitFile stages and commits a file in the given repo directory.
func commitFile(t *testing.T, repoDir, name, content string) {
	t.Helper()
	path := filepath.Join(repoDir, name)

	// Create parent directories if needed.
	if dir := filepath.Dir(path); dir != repoDir {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	for _, args := range [][]string{
		{"git", "add", name},
		{"git", "commit", "-m", "add " + name},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}
}

// resolvedRepoDir returns the symlink-resolved path for a repo dir.
// On macOS, TempDir often returns /var/... which is a symlink to /private/var/...,
// and git rev-parse --show-toplevel returns the resolved path.
func resolvedRepoDir(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", dir, err)
	}
	return resolved
}

// makeResumeRepo creates a temp git repo, commits a file named branchName,
// then modifies it so `git checkout -- branchName` (file restore) succeeds.
// Returns the repo dir (symlink-resolved) and a git client.
func makeResumeRepo(t *testing.T, branchName string) (string, *git.Client) {
	t.Helper()
	repoDir := testutil.InitTestRepo(t)
	commitFile(t, repoDir, branchName, "original")

	// Modify the file so checkout has something to restore.
	if err := os.WriteFile(filepath.Join(repoDir, branchName), []byte("modified"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}

	resolved := resolvedRepoDir(t, repoDir)
	return resolved, git.NewClient(repoDir)
}

// ── Flag registration ──

func TestNewCmdResume_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdResume(f)

	flags := []string{
		"session", "provider", "as-context",
		// Smart Restore filter flags
		"clean-errors", "strip-empty", "fix-orphans", "redact-secrets", "exclude",
	}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestNewCmdResume_requiresBranchArg(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdResume(f)

	// cobra.ExactArgs(1) — no args should fail.
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no branch argument provided")
	}
}

// ── Error: GitFunc fails ──

func TestResume_gitError(t *testing.T) {
	ios := iostreams.Test()

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc: func() (*git.Client, error) {
			return nil, fmt.Errorf("fatal: not a git repository")
		},
	}

	opts := &Options{
		IO:      ios,
		Factory: f,
		Branch:  "feat/auth",
	}

	err := runResume(opts)
	if err == nil {
		t.Fatal("expected error when git client init fails")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("expected 'not a git repository' in error, got: %v", err)
	}
}

// ── Error: Checkout fails (no file matching branch name) ──

func TestResume_checkoutError(t *testing.T) {
	f, ios := resumeTestFactory(t, nil)

	opts := &Options{
		IO:      ios,
		Factory: f,
		Branch:  "feat/nonexistent",
	}

	err := runResume(opts)
	if err == nil {
		t.Fatal("expected error when checkout fails")
	}
	if !strings.Contains(err.Error(), "git checkout feat/nonexistent") {
		t.Errorf("expected 'git checkout feat/nonexistent' in error, got: %v", err)
	}
}

// ── Error: SessionServiceFunc fails ──

func TestResume_serviceError(t *testing.T) {
	ios := iostreams.Test()
	repoDir, gitClient := makeResumeRepo(t, "mybranch")
	_ = repoDir

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return nil, fmt.Errorf("db unavailable")
		},
	}

	opts := &Options{
		IO:      ios,
		Factory: f,
		Branch:  "mybranch",
	}

	err := runResume(opts)
	if err == nil {
		t.Fatal("expected error when session service init fails")
	}
	if !strings.Contains(err.Error(), "initializing service") {
		t.Errorf("expected 'initializing service' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "db unavailable") {
		t.Errorf("expected 'db unavailable' in error, got: %v", err)
	}
}

// ── Error: invalid provider name ──

func TestResume_invalidProvider(t *testing.T) {
	ios := iostreams.Test()
	repoDir, gitClient := makeResumeRepo(t, "mybranch")

	store := testutil.NewMockStore()

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store:    store,
				Git:      gitClient,
				Registry: provider.NewRegistry(),
			}), nil
		},
	}

	_ = repoDir

	opts := &Options{
		IO:       ios,
		Factory:  f,
		Branch:   "mybranch",
		Provider: "not-a-real-provider",
	}

	err := runResume(opts)
	if err == nil {
		t.Fatal("expected error for invalid provider")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("expected 'unknown provider' in error, got: %v", err)
	}
}

// ── Error: nil GitFunc (factory has no git configured) ──

func TestResume_nilGitFunc(t *testing.T) {
	ios := iostreams.Test()

	f := &cmdutil.Factory{
		IOStreams: ios,
		// GitFunc intentionally nil
	}

	opts := &Options{
		IO:      ios,
		Factory: f,
		Branch:  "main",
	}

	err := runResume(opts)
	if err == nil {
		t.Fatal("expected error when GitFunc is nil")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("expected 'not a git repository' in error, got: %v", err)
	}
}

// ── Error: restore fails (session not found for branch) ──

func TestResume_restoreSessionNotFound(t *testing.T) {
	store := testutil.NewMockStore()
	// Store is empty — no session for the branch.

	ios := iostreams.Test()
	repoDir, gitClient := makeResumeRepo(t, "mybranch")
	_ = repoDir

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store:    store,
				Git:      gitClient,
				Registry: provider.NewRegistry(),
			}), nil
		},
	}

	opts := &Options{
		IO:      ios,
		Factory: f,
		Branch:  "mybranch",
	}

	err := runResume(opts)
	if err == nil {
		t.Fatal("expected error when no session exists for branch")
	}
	if !strings.Contains(err.Error(), "restore") {
		t.Errorf("expected 'restore' in error, got: %v", err)
	}
}

// ── Success: full flow with CONTEXT.md fallback ──
//
// Because `git checkout -- <branch>` restores a FILE (not switches branch),
// we create a committed file named like the branch, modify it, then resume.
// The Restore() call falls back to CONTEXT.md when no native provider is
// registered, which still counts as a successful restore.

func TestResume_success(t *testing.T) {
	store := testutil.NewMockStore()
	ios := iostreams.Test()
	repoDir, gitClient := makeResumeRepo(t, "mybranch")

	// Put a session in the store keyed by resolved projectPath + branch.
	sess := testutil.NewSession("resume-test-sess")
	sess.Branch = "mybranch"
	sess.ProjectPath = repoDir
	store.Sessions[sess.ID] = sess
	store.ByBranch[repoDir+":mybranch"] = sess

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store:    store,
				Git:      gitClient,
				Registry: provider.NewRegistry(),
			}), nil
		},
	}

	opts := &Options{
		IO:      ios,
		Factory: f,
		Branch:  "mybranch",
	}

	err := runResume(opts)
	if err != nil {
		t.Fatalf("runResume() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()

	if !strings.Contains(output, "Switched to branch mybranch") {
		t.Errorf("expected 'Switched to branch mybranch' in output, got:\n%s", output)
	}
	if !strings.Contains(output, "Restored session") {
		t.Errorf("expected 'Restored session' in output, got:\n%s", output)
	}

	// CONTEXT.md should have been written as a fallback (no provider registered).
	contextPath := filepath.Join(repoDir, "CONTEXT.md")
	if _, statErr := os.Stat(contextPath); os.IsNotExist(statErr) {
		t.Error("expected CONTEXT.md to be created as restore fallback")
	}
}

// ── Success with --as-context flag ──

func TestResume_successAsContext(t *testing.T) {
	store := testutil.NewMockStore()
	ios := iostreams.Test()
	repoDir, gitClient := makeResumeRepo(t, "ctx-branch")

	sess := testutil.NewSession("resume-ctx-test")
	sess.Branch = "ctx-branch"
	sess.ProjectPath = repoDir
	store.Sessions[sess.ID] = sess
	store.ByBranch[repoDir+":ctx-branch"] = sess

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store:    store,
				Git:      gitClient,
				Registry: provider.NewRegistry(),
			}), nil
		},
	}

	opts := &Options{
		IO:        ios,
		Factory:   f,
		Branch:    "ctx-branch",
		AsContext: true,
	}

	err := runResume(opts)
	if err != nil {
		t.Fatalf("runResume() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Restored session") {
		t.Errorf("expected 'Restored session' in output, got:\n%s", output)
	}
	if !strings.Contains(output, "context") {
		t.Errorf("expected 'context' method in output, got:\n%s", output)
	}
}

// ── Success with --session flag ──

func TestResume_successWithSessionID(t *testing.T) {
	store := testutil.NewMockStore()
	ios := iostreams.Test()
	repoDir, gitClient := makeResumeRepo(t, "sid-branch")

	sess := testutil.NewSession("specific-session-id")
	sess.Branch = "sid-branch"
	sess.ProjectPath = repoDir
	store.Sessions[sess.ID] = sess

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store:    store,
				Git:      gitClient,
				Registry: provider.NewRegistry(),
			}), nil
		},
	}

	opts := &Options{
		IO:        ios,
		Factory:   f,
		Branch:    "sid-branch",
		SessionID: string(sess.ID),
	}

	err := runResume(opts)
	if err != nil {
		t.Fatalf("runResume() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Restored session") {
		t.Errorf("expected 'Restored session' in output, got:\n%s", output)
	}
	if !strings.Contains(output, string(sess.ID)) {
		t.Errorf("expected session ID %s in output, got:\n%s", sess.ID, output)
	}
}

// ── buildFilters tests ──

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
		FixOrphans:    true,
		RedactSecrets: true,
		ExcludeFlag:   "0,system",
	}
	filters, err := buildFilters(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(filters) != 5 {
		t.Fatalf("expected 5 filters, got %d", len(filters))
	}

	// Verify filter order: exclude → fix-orphans → strip-empty → clean-errors → redact-secrets
	names := make([]string, len(filters))
	for i, f := range filters {
		names[i] = f.Name()
	}
	expected := []string{"message-excluder", "orphan-tool-fixer", "empty-message", "error-cleaner", "secret-redactor"}
	for i, want := range expected {
		if names[i] != want {
			t.Errorf("filter[%d]: got %q, want %q", i, names[i], want)
		}
	}
}

func TestBuildFilters_singleFlag(t *testing.T) {
	tests := []struct {
		name     string
		opts     Options
		wantName string
	}{
		{"clean-errors", Options{CleanErrors: true}, "error-cleaner"},
		{"strip-empty", Options{StripEmpty: true}, "empty-message"},
		{"fix-orphans", Options{FixOrphans: true}, "orphan-tool-fixer"},
		{"redact-secrets", Options{RedactSecrets: true}, "secret-redactor"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filters, err := buildFilters(&tt.opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(filters) != 1 {
				t.Fatalf("expected 1 filter, got %d", len(filters))
			}
			if filters[0].Name() != tt.wantName {
				t.Errorf("got %q, want %q", filters[0].Name(), tt.wantName)
			}
		})
	}
}

func TestBuildFilters_invalidExclude(t *testing.T) {
	opts := &Options{ExcludeFlag: "/[invalid/"}
	_, err := buildFilters(opts)
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
	if !strings.Contains(err.Error(), "--exclude") {
		t.Errorf("expected '--exclude' in error, got: %v", err)
	}
}

// ── parseExcludeFlag tests ──

func TestParseExcludeFlag_indices(t *testing.T) {
	f, err := parseExcludeFlag("0,3,5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.Name() != "message-excluder" {
		t.Errorf("expected 'message-excluder', got %q", f.Name())
	}
}

func TestParseExcludeFlag_roles(t *testing.T) {
	f, err := parseExcludeFlag("system")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.Name() != "message-excluder" {
		t.Errorf("expected 'message-excluder', got %q", f.Name())
	}
}

func TestParseExcludeFlag_pattern(t *testing.T) {
	f, err := parseExcludeFlag("/error/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.Name() != "message-excluder" {
		t.Errorf("expected 'message-excluder', got %q", f.Name())
	}
}

func TestParseExcludeFlag_mixed(t *testing.T) {
	f, err := parseExcludeFlag("0, system, /error/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.Name() != "message-excluder" {
		t.Errorf("expected 'message-excluder', got %q", f.Name())
	}
}

func TestParseExcludeFlag_negativeIndex(t *testing.T) {
	_, err := parseExcludeFlag("-1")
	if err == nil {
		t.Fatal("expected error for negative index")
	}
	if !strings.Contains(err.Error(), "negative index") {
		t.Errorf("expected 'negative index' in error, got: %v", err)
	}
}

func TestParseExcludeFlag_multiplePatterns(t *testing.T) {
	_, err := parseExcludeFlag("/foo/,/bar/")
	if err == nil {
		t.Fatal("expected error for multiple patterns")
	}
	if !strings.Contains(err.Error(), "only one content pattern") {
		t.Errorf("expected 'only one content pattern' in error, got: %v", err)
	}
}

func TestParseExcludeFlag_invalidRegex(t *testing.T) {
	_, err := parseExcludeFlag("/[invalid/")
	if err != nil {
		// NewMessageExcluder should return error for invalid regex
		return
	}
	t.Fatal("expected error for invalid regex pattern")
}

// ── Success with filter flags ──

func TestResume_successWithStripEmpty(t *testing.T) {
	store := testutil.NewMockStore()
	ios := iostreams.Test()
	repoDir, gitClient := makeResumeRepo(t, "filter-branch")

	// Create a session with an empty message (no content, no tool calls)
	sess := testutil.NewSession("filter-test-sess")
	sess.Branch = "filter-branch"
	sess.ProjectPath = repoDir
	sess.Messages = append(sess.Messages, session.Message{
		ID:   "empty-msg",
		Role: session.RoleAssistant,
		// Content is empty, no tool calls
	})
	store.Sessions[sess.ID] = sess
	store.ByBranch[repoDir+":filter-branch"] = sess

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store:    store,
				Git:      gitClient,
				Registry: provider.NewRegistry(),
			}), nil
		},
	}

	opts := &Options{
		IO:         ios,
		Factory:    f,
		Branch:     "filter-branch",
		StripEmpty: true,
	}

	err := runResume(opts)
	if err != nil {
		t.Fatalf("runResume() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Restored session") {
		t.Errorf("expected 'Restored session' in output, got:\n%s", output)
	}

	// The filter should have been applied (the empty message was removed)
	if !strings.Contains(output, "Smart Restore filters applied") {
		t.Errorf("expected 'Smart Restore filters applied' in output, got:\n%s", output)
	}
	if !strings.Contains(output, "empty-message") {
		t.Errorf("expected 'empty-message' filter name in output, got:\n%s", output)
	}
}

func TestResume_successWithCleanErrors(t *testing.T) {
	store := testutil.NewMockStore()
	ios := iostreams.Test()
	repoDir, gitClient := makeResumeRepo(t, "errors-branch")

	// Create a session with tool error messages
	sess := testutil.NewSession("error-filter-sess")
	sess.Branch = "errors-branch"
	sess.ProjectPath = repoDir
	sess.Messages = []session.Message{
		{
			ID:      "msg-1",
			Role:    session.RoleUser,
			Content: "fix the bug",
		},
		{
			ID:   "msg-2",
			Role: session.RoleAssistant,
			ToolCalls: []session.ToolCall{
				{
					ID:     "tc-1",
					Name:   "bash",
					Input:  `{"command": "go build"}`,
					Output: "Error: compilation failed\nundefined: foo\n/path/to/file.go:42:5\nsome very long error output",
					State:  session.ToolStateError,
				},
			},
		},
		{
			ID:      "msg-3",
			Role:    session.RoleAssistant,
			Content: "I see the compilation error. Let me fix that.",
		},
	}
	store.Sessions[sess.ID] = sess
	store.ByBranch[repoDir+":errors-branch"] = sess

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store:    store,
				Git:      gitClient,
				Registry: provider.NewRegistry(),
			}), nil
		},
	}

	opts := &Options{
		IO:          ios,
		Factory:     f,
		Branch:      "errors-branch",
		CleanErrors: true,
	}

	err := runResume(opts)
	if err != nil {
		t.Fatalf("runResume() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Restored session") {
		t.Errorf("expected 'Restored session' in output, got:\n%s", output)
	}
}

func TestResume_successWithMultipleFilters(t *testing.T) {
	store := testutil.NewMockStore()
	ios := iostreams.Test()
	repoDir, gitClient := makeResumeRepo(t, "multi-filter")

	// Create a session with an empty message
	sess := testutil.NewSession("multi-filter-sess")
	sess.Branch = "multi-filter"
	sess.ProjectPath = repoDir
	sess.Messages = append(sess.Messages, session.Message{
		ID:   "empty",
		Role: session.RoleAssistant,
		// empty
	})
	store.Sessions[sess.ID] = sess
	store.ByBranch[repoDir+":multi-filter"] = sess

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store:    store,
				Git:      gitClient,
				Registry: provider.NewRegistry(),
			}), nil
		},
	}

	opts := &Options{
		IO:          ios,
		Factory:     f,
		Branch:      "multi-filter",
		StripEmpty:  true,
		CleanErrors: true,
		FixOrphans:  true,
	}

	err := runResume(opts)
	if err != nil {
		t.Fatalf("runResume() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Restored session") {
		t.Errorf("expected 'Restored session' in output, got:\n%s", output)
	}
}

// ── Dry-run mode ──

func TestResume_dryRun(t *testing.T) {
	store := testutil.NewMockStore()
	ios := iostreams.Test()
	repoDir, gitClient := makeResumeRepo(t, "dry-branch")

	sess := testutil.NewSession("dry-run-sess")
	sess.Branch = "dry-branch"
	sess.ProjectPath = repoDir
	store.Sessions[sess.ID] = sess
	store.ByBranch[repoDir+":dry-branch"] = sess

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store:    store,
				Git:      gitClient,
				Registry: provider.NewRegistry(),
			}), nil
		},
	}

	opts := &Options{
		IO:      ios,
		Factory: f,
		Branch:  "dry-branch",
		DryRun:  true,
	}

	err := runResume(opts)
	if err != nil {
		t.Fatalf("runResume(DryRun) error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()

	// Dry-run should print preview, not "Restored session"
	if !strings.Contains(output, "Dry-run preview") {
		t.Errorf("expected 'Dry-run preview' in output, got:\n%s", output)
	}
	if strings.Contains(output, "Restored session") {
		t.Errorf("dry-run should NOT print 'Restored session', got:\n%s", output)
	}
	if !strings.Contains(output, "Run without --dry-run to apply") {
		t.Errorf("expected 'Run without --dry-run' hint in output, got:\n%s", output)
	}
	if !strings.Contains(output, string(sess.ID)) {
		t.Errorf("expected session ID in output, got:\n%s", output)
	}
}

func TestResume_dryRunWithFilters(t *testing.T) {
	store := testutil.NewMockStore()
	ios := iostreams.Test()
	repoDir, gitClient := makeResumeRepo(t, "dry-filter")

	sess := testutil.NewSession("dry-filter-sess")
	sess.Branch = "dry-filter"
	sess.ProjectPath = repoDir
	sess.Messages = append(sess.Messages, session.Message{
		ID:   "empty-msg",
		Role: session.RoleAssistant,
		// empty content, no tool calls
	})
	store.Sessions[sess.ID] = sess
	store.ByBranch[repoDir+":dry-filter"] = sess

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store:    store,
				Git:      gitClient,
				Registry: provider.NewRegistry(),
			}), nil
		},
	}

	opts := &Options{
		IO:         ios,
		Factory:    f,
		Branch:     "dry-filter",
		DryRun:     true,
		StripEmpty: true,
	}

	err := runResume(opts)
	if err != nil {
		t.Fatalf("runResume(DryRun+Filter) error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Dry-run preview") {
		t.Errorf("expected 'Dry-run preview' in output, got:\n%s", output)
	}
	if !strings.Contains(output, "Filters") {
		t.Errorf("expected filter info in output, got:\n%s", output)
	}
}

func TestNewCmdResume_dryRunFlag(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdResume(f)

	if cmd.Flags().Lookup("dry-run") == nil {
		t.Error("expected --dry-run flag")
	}
}

// ── Error: invalid --exclude flag ──

func TestResume_invalidExcludeFlag(t *testing.T) {
	ios := iostreams.Test()
	repoDir, gitClient := makeResumeRepo(t, "excl-branch")
	store := testutil.NewMockStore()

	sess := testutil.NewSession("excl-test")
	sess.Branch = "excl-branch"
	sess.ProjectPath = repoDir
	store.Sessions[sess.ID] = sess
	store.ByBranch[repoDir+":excl-branch"] = sess

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store:    store,
				Git:      gitClient,
				Registry: provider.NewRegistry(),
			}), nil
		},
	}

	opts := &Options{
		IO:          ios,
		Factory:     f,
		Branch:      "excl-branch",
		ExcludeFlag: "/[invalid/",
	}

	err := runResume(opts)
	if err == nil {
		t.Fatal("expected error for invalid exclude pattern")
	}
	if !strings.Contains(err.Error(), "--exclude") {
		t.Errorf("expected '--exclude' in error, got: %v", err)
	}
}
