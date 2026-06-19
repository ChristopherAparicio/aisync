package blamecmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

func blameTestFactory(t *testing.T, store *testutil.MockStore) (*cmdutil.Factory, *iostreams.IOStreams) {
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
				Store: store,
				Git:   gitClient,
			}), nil
		},
	}

	return f, ios
}

func TestNewCmdBlame_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdBlame(f)

	flags := []string{"all", "restore", "branch", "provider", "json", "quiet", "project", "files-from"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

// TestBlame_noArgsNoProject verifies that calling blame with no args and no --project
// returns an error requiring at least one source.
func TestBlame_noArgsNoProject(t *testing.T) {
	f, ios := blameTestFactory(t, nil)
	opts := &Options{
		IO:      ios,
		Factory: f,
	}

	err := runBlame(opts)
	if err == nil {
		t.Fatal("expected error when no args and no --project")
	}
	if !strings.Contains(err.Error(), "at least one file argument or --project flag") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBlame_noResults(t *testing.T) {
	store := testutil.NewMockStore()
	f, ios := blameTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		FilePaths: []string{"nonexistent.go"},
	}

	err := runBlame(opts)
	if err != nil {
		t.Fatalf("runBlame() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "No AI sessions found") {
		t.Errorf("expected 'No AI sessions found', got: %s", output)
	}
}

func TestBlame_tableOutput(t *testing.T) {
	store := testutil.NewMockStore()
	store.BlameEntries = []session.BlameEntry{
		{
			SessionID:  "sess-123",
			Provider:   session.ProviderClaudeCode,
			Agent:      "copilot",
			Branch:     "feat/auth",
			ChangeType: session.ChangeModified,
			Summary:    "Implement login handler",
			CreatedAt:  time.Now(),
		},
	}
	f, ios := blameTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		FilePaths: []string{"handler.go"},
	}

	err := runBlame(opts)
	if err != nil {
		t.Fatalf("runBlame() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "sess-123") {
		t.Errorf("expected session ID in output, got: %s", output)
	}
	if !strings.Contains(output, "claude-code") {
		t.Errorf("expected provider in output, got: %s", output)
	}
	if !strings.Contains(output, "feat/auth") {
		t.Errorf("expected branch in output, got: %s", output)
	}
	if !strings.Contains(output, "Last AI session") {
		t.Errorf("expected 'Last AI session' header, got: %s", output)
	}
	if !strings.Contains(output, "AGENT") {
		t.Errorf("expected AGENT column header, got: %s", output)
	}
	if !strings.Contains(output, "copilot") {
		t.Errorf("expected agent name in output, got: %s", output)
	}
}

func TestBlame_allFlag(t *testing.T) {
	store := testutil.NewMockStore()
	store.BlameEntries = []session.BlameEntry{
		{SessionID: "sess-1", Provider: session.ProviderClaudeCode, Branch: "main", ChangeType: session.ChangeModified, CreatedAt: time.Now()},
		{SessionID: "sess-2", Provider: session.ProviderOpenCode, Branch: "feat/a", ChangeType: session.ChangeCreated, CreatedAt: time.Now()},
	}
	f, ios := blameTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		FilePaths: []string{"file.go"},
		All:       true,
	}

	err := runBlame(opts)
	if err != nil {
		t.Fatalf("runBlame() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "sess-1") || !strings.Contains(output, "sess-2") {
		t.Errorf("expected both sessions in output, got: %s", output)
	}
	if !strings.Contains(output, "2 found") {
		t.Errorf("expected '2 found' in header, got: %s", output)
	}
}

func TestBlame_jsonOutput(t *testing.T) {
	store := testutil.NewMockStore()
	store.BlameEntries = []session.BlameEntry{
		{SessionID: "json-sess", Provider: session.ProviderClaudeCode, Branch: "main", ChangeType: session.ChangeModified, CreatedAt: time.Now()},
	}
	f, ios := blameTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		FilePaths: []string{"file.go"},
		JSON:      true,
	}

	err := runBlame(opts)
	if err != nil {
		t.Fatalf("runBlame() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, `"session_id"`) {
		t.Errorf("expected JSON key in output, got: %s", output)
	}
}

func TestBlame_quietOutput(t *testing.T) {
	store := testutil.NewMockStore()
	store.BlameEntries = []session.BlameEntry{
		{SessionID: "quiet-1", Provider: session.ProviderClaudeCode, CreatedAt: time.Now()},
		{SessionID: "quiet-2", Provider: session.ProviderOpenCode, CreatedAt: time.Now()},
	}
	f, ios := blameTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		FilePaths: []string{"file.go"},
		All:       true,
		Quiet:     true,
	}

	err := runBlame(opts)
	if err != nil {
		t.Fatalf("runBlame() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	lines := strings.TrimSpace(output)
	if !strings.Contains(lines, "quiet-1") || !strings.Contains(lines, "quiet-2") {
		t.Errorf("expected session IDs only, got: %s", output)
	}
	if strings.Contains(output, "PROVIDER") {
		t.Errorf("quiet mode should not contain table headers, got: %s", output)
	}
}

// TestBlame_multipleFiles verifies multi-file mode renders the grouped-by-file table with a FILE
// column and that the AGENT column and per-file sessions appear.
func TestBlame_multipleFiles(t *testing.T) {
	store := testutil.NewMockStore()
	store.BlameEntries = []session.BlameEntry{
		{SessionID: "sess-a", Provider: session.ProviderClaudeCode, Agent: "jarvis", Branch: "main", ChangeType: session.ChangeModified, FilePath: "src/a.go", CreatedAt: time.Now()},
		{SessionID: "sess-b", Provider: session.ProviderOpenCode, Agent: "", Branch: "main", ChangeType: session.ChangeCreated, FilePath: "src/b.go", CreatedAt: time.Now()},
	}
	f, ios := blameTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		FilePaths: []string{"src/a.go", "src/b.go"},
		All:       true,
	}

	err := runBlame(opts)
	if err != nil {
		t.Fatalf("runBlame() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "sess-a") || !strings.Contains(output, "sess-b") {
		t.Errorf("expected both sessions in multi-file output, got: %s", output)
	}
	if !strings.Contains(output, "FILE") {
		t.Errorf("expected FILE column header in grouped multi-file output, got: %s", output)
	}
	if !strings.Contains(output, "src/a.go") || !strings.Contains(output, "src/b.go") {
		t.Errorf("expected both file paths in grouped output, got: %s", output)
	}
	if !strings.Contains(output, "AGENT") {
		t.Errorf("expected AGENT column header in multi-file output, got: %s", output)
	}
	if !strings.Contains(output, "jarvis") {
		t.Errorf("expected agent 'jarvis' in output, got: %s", output)
	}
}

// TestBlame_filesFromLineManifest verifies --files-from reads a one-path-per-line manifest
// (skipping blanks and # comments) and renders the grouped-by-file table.
func TestBlame_filesFromLineManifest(t *testing.T) {
	store := testutil.NewMockStore()
	store.BlameEntries = []session.BlameEntry{
		{SessionID: "ff-a", Provider: session.ProviderClaudeCode, Agent: "jarvis", Branch: "main", ChangeType: session.ChangeModified, FilePath: "src/a.go", CreatedAt: time.Now()},
		{SessionID: "ff-b", Provider: session.ProviderOpenCode, Branch: "main", ChangeType: session.ChangeCreated, FilePath: "src/b.go", CreatedAt: time.Now()},
	}
	f, ios := blameTestFactory(t, store)

	manifest := filepath.Join(t.TempDir(), "files.txt")
	if err := os.WriteFile(manifest, []byte("src/a.go\n# a comment\n\nsrc/b.go\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	opts := &Options{
		IO:        ios,
		Factory:   f,
		FilesFrom: manifest,
		All:       true,
	}

	if err := runBlame(opts); err != nil {
		t.Fatalf("runBlame() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	for _, want := range []string{"FILE", "src/a.go", "src/b.go", "ff-a", "ff-b"} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in --files-from output, got:\n%s", want, output)
		}
	}
}

// TestBlame_filesFromJSONManifest verifies --files-from auto-detects a JSON array manifest.
func TestBlame_filesFromJSONManifest(t *testing.T) {
	store := testutil.NewMockStore()
	store.BlameEntries = []session.BlameEntry{
		{SessionID: "jf-a", Provider: session.ProviderClaudeCode, FilePath: "src/a.go", CreatedAt: time.Now()},
		{SessionID: "jf-b", Provider: session.ProviderOpenCode, FilePath: "src/b.go", CreatedAt: time.Now()},
	}
	f, ios := blameTestFactory(t, store)

	manifest := filepath.Join(t.TempDir(), "files.json")
	if err := os.WriteFile(manifest, []byte(`["src/a.go", "src/b.go"]`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	opts := &Options{
		IO:        ios,
		Factory:   f,
		FilesFrom: manifest,
		All:       true,
	}

	if err := runBlame(opts); err != nil {
		t.Fatalf("runBlame() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	for _, want := range []string{"src/a.go", "src/b.go", "jf-a", "jf-b"} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in JSON --files-from output, got:\n%s", want, output)
		}
	}
}

// TestBlame_filesFromMissingFile verifies a missing manifest path returns an error.
func TestBlame_filesFromMissingFile(t *testing.T) {
	f, ios := blameTestFactory(t, nil)
	opts := &Options{
		IO:        ios,
		Factory:   f,
		FilesFrom: filepath.Join(t.TempDir(), "does-not-exist.txt"),
	}
	if err := runBlame(opts); err == nil {
		t.Fatal("expected error for missing --files-from manifest, got nil")
	}
}

// TestBlame_agentEmptyRendersAsDash verifies that an empty agent field renders as "-" in the table.
func TestBlame_agentEmptyRendersAsDash(t *testing.T) {
	store := testutil.NewMockStore()
	store.BlameEntries = []session.BlameEntry{
		{SessionID: "sess-abc", Provider: session.ProviderClaudeCode, Agent: "", Branch: "main", ChangeType: session.ChangeModified, CreatedAt: time.Now()},
	}
	f, ios := blameTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		FilePaths: []string{"main.go"},
	}

	err := runBlame(opts)
	if err != nil {
		t.Fatalf("runBlame() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "AGENT") {
		t.Errorf("expected AGENT column header in output, got: %s", output)
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "sess-abc") {
			// The agent column should be "-", not blank.
			// Split fields: SESSION_ID  PROVIDER  AGENT  BRANCH  ...
			// We verify by finding "  -  " (padded dash) in the line.
			fields := strings.Fields(line)
			if len(fields) < 3 {
				t.Fatalf("unexpected table line: %q", line)
			}
			// fields[2] is the AGENT column value
			if fields[2] != "-" {
				t.Errorf("expected '-' for empty agent in output line, got field[2]=%q in %q", fields[2], line)
			}
			return
		}
	}
	t.Fatalf("expected to find session line in output:\n%s", output)
}

// TestBlame_projectMode verifies --project with no file args renders the project-view table
// with FILE | SESSION_ID | AGENT | DATE columns.
func TestBlame_projectMode(t *testing.T) {
	store := testutil.NewMockStore()
	store.ProjectFileEntries = []session.ProjectFileEntry{
		{
			FilePath:        "src/main.go",
			LastSessionID:   "pf-sess-1",
			LastAgent:       "cody",
			LastSessionTime: time.Now(),
		},
		{
			FilePath:        "src/auth.go",
			LastSessionID:   "pf-sess-2",
			LastAgent:       "",
			LastSessionTime: time.Now(),
		},
	}
	f, ios := blameTestFactory(t, store)

	opts := &Options{
		IO:          ios,
		Factory:     f,
		ProjectPath: "/some/project",
	}

	err := runBlame(opts)
	if err != nil {
		t.Fatalf("runBlame() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	for _, want := range []string{"FILE", "SESSION_ID", "AGENT", "src/main.go", "pf-sess-1", "cody"} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in project-mode output, got:\n%s", want, output)
		}
	}
	// DATE header must appear; PROVIDER and BRANCH must NOT (project-view is a different layout).
	if !strings.Contains(output, "DATE") {
		t.Errorf("expected DATE column in project-mode output, got: %s", output)
	}
}

// TestBlame_projectModeJSON verifies --project --json encodes ProjectFiles (not Entries).
func TestBlame_projectModeJSON(t *testing.T) {
	store := testutil.NewMockStore()
	store.ProjectFileEntries = []session.ProjectFileEntry{
		{FilePath: "src/main.go", LastSessionID: "json-proj-1", LastAgent: "copilot"},
	}
	f, ios := blameTestFactory(t, store)

	opts := &Options{
		IO:          ios,
		Factory:     f,
		ProjectPath: "/some/project",
		JSON:        true,
	}

	err := runBlame(opts)
	if err != nil {
		t.Fatalf("runBlame() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, `"file_path"`) {
		t.Errorf("expected 'file_path' key in JSON output (ProjectFiles), got: %s", output)
	}
	if !strings.Contains(output, `"last_session_id"`) {
		t.Errorf("expected 'last_session_id' key in JSON output (ProjectFiles), got: %s", output)
	}
	// Must NOT encode file-mode fields like "session_id" at top level.
	if strings.Contains(output, `"session_id"`) && !strings.Contains(output, `"last_session_id"`) {
		t.Errorf("project-mode JSON should encode ProjectFiles, not Entries")
	}
}

// TestBlame_projectModeQuiet verifies --project --quiet prints LastSessionID per line, no headers.
func TestBlame_projectModeQuiet(t *testing.T) {
	store := testutil.NewMockStore()
	store.ProjectFileEntries = []session.ProjectFileEntry{
		{FilePath: "src/a.go", LastSessionID: "quiet-proj-1"},
		{FilePath: "src/b.go", LastSessionID: "quiet-proj-2"},
	}
	f, ios := blameTestFactory(t, store)

	opts := &Options{
		IO:          ios,
		Factory:     f,
		ProjectPath: "/some/project",
		Quiet:       true,
	}

	err := runBlame(opts)
	if err != nil {
		t.Fatalf("runBlame() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "quiet-proj-1") || !strings.Contains(output, "quiet-proj-2") {
		t.Errorf("expected both session IDs in quiet project output, got: %s", output)
	}
	if strings.Contains(output, "FILE") || strings.Contains(output, "AGENT") {
		t.Errorf("quiet mode should not contain table headers, got: %s", output)
	}
}

// TestBlame_quietModeNoAgentColumn ensures --quiet file-mode never emits the AGENT column header.
func TestBlame_quietModeNoAgentColumn(t *testing.T) {
	store := testutil.NewMockStore()
	store.BlameEntries = []session.BlameEntry{
		{SessionID: "quiet-f-1", Provider: session.ProviderClaudeCode, Agent: "jarvis", CreatedAt: time.Now()},
	}
	f, ios := blameTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		FilePaths: []string{"main.go"},
		Quiet:     true,
	}

	err := runBlame(opts)
	if err != nil {
		t.Fatalf("runBlame() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if strings.Contains(output, "AGENT") {
		t.Errorf("quiet mode should not contain AGENT column, got: %s", output)
	}
	if !strings.Contains(output, "quiet-f-1") {
		t.Errorf("expected session ID in quiet output, got: %s", output)
	}
}
