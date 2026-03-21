package blamecmd

import (
	"bytes"
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

	flags := []string{"all", "restore", "branch", "provider", "json", "quiet"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestBlame_noResults(t *testing.T) {
	store := testutil.NewMockStore()
	f, ios := blameTestFactory(t, store)

	opts := &Options{
		IO:       ios,
		Factory:  f,
		FilePath: "nonexistent.go",
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
			Branch:     "feat/auth",
			ChangeType: session.ChangeModified,
			Summary:    "Implement login handler",
			CreatedAt:  time.Now(),
		},
	}
	f, ios := blameTestFactory(t, store)

	opts := &Options{
		IO:       ios,
		Factory:  f,
		FilePath: "handler.go",
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
}

func TestBlame_allFlag(t *testing.T) {
	store := testutil.NewMockStore()
	store.BlameEntries = []session.BlameEntry{
		{SessionID: "sess-1", Provider: session.ProviderClaudeCode, Branch: "main", ChangeType: session.ChangeModified, CreatedAt: time.Now()},
		{SessionID: "sess-2", Provider: session.ProviderOpenCode, Branch: "feat/a", ChangeType: session.ChangeCreated, CreatedAt: time.Now()},
	}
	f, ios := blameTestFactory(t, store)

	opts := &Options{
		IO:       ios,
		Factory:  f,
		FilePath: "file.go",
		All:      true,
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
		IO:       ios,
		Factory:  f,
		FilePath: "file.go",
		JSON:     true,
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
		IO:       ios,
		Factory:  f,
		FilePath: "file.go",
		All:      true,
		Quiet:    true,
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
	// Should NOT contain table headers
	if strings.Contains(output, "PROVIDER") {
		t.Errorf("quiet mode should not contain table headers, got: %s", output)
	}
}
