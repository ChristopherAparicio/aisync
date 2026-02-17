package synccmd

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/internal/storage/sqlite"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// initTestRepo creates a temporary git repository for testing.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	commands := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "commit", "--allow-empty", "-m", "initial"},
	}

	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}

	return dir
}

// mustOpenStore creates a temporary SQLite store.
func mustOpenStore(t *testing.T) *sqlite.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("sqlite.New() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testSession(id string) *domain.Session {
	now := time.Date(2026, 2, 17, 10, 0, 0, 0, time.UTC)
	return &domain.Session{
		ID:          domain.SessionID(id),
		Version:     1,
		Provider:    domain.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feature/sync",
		ProjectPath: "/tmp/test-project",
		CreatedAt:   now,
		ExportedAt:  now,
		Summary:     "Test session " + id,
		StorageMode: domain.StorageModeCompact,
		Messages: []domain.Message{
			{
				ID:        "msg-001",
				Role:      domain.RoleUser,
				Content:   "Hello",
				Timestamp: now,
			},
		},
		TokenUsage: domain.TokenUsage{
			InputTokens:  100,
			OutputTokens: 200,
			TotalTokens:  300,
		},
	}
}

func testFactory(t *testing.T) (*cmdutil.Factory, *iostreams.IOStreams, *sqlite.Store, *git.Client) {
	t.Helper()
	ios := iostreams.Test()
	repoDir := initTestRepo(t)
	gitClient := git.NewClient(repoDir)
	store := mustOpenStore(t)

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc: func() (*git.Client, error) {
			return gitClient, nil
		},
		StoreFunc: func() (domain.Store, error) {
			return store, nil
		},
	}

	return f, ios, store, gitClient
}

func TestPushCmd_nothingToPush(t *testing.T) {
	f, ios, _, _ := testFactory(t)

	opts := &PushOptions{
		IO:       ios,
		Factory:  f,
		NoRemote: true,
	}

	err := runPush(opts)
	if err != nil {
		t.Fatalf("runPush() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if output != "Nothing to push. All sessions already synced.\n" {
		t.Errorf("output = %q, want nothing to push message", output)
	}
}

func TestPushCmd_oneSession(t *testing.T) {
	f, ios, store, _ := testFactory(t)

	session := testSession("sess-cmd-push-1")
	if err := store.Save(session); err != nil {
		t.Fatal(err)
	}

	opts := &PushOptions{
		IO:       ios,
		Factory:  f,
		NoRemote: true,
	}

	err := runPush(opts)
	if err != nil {
		t.Fatalf("runPush() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	expected := "Pushed 1 session(s) to aisync/sessions branch.\n"
	if output != expected {
		t.Errorf("output = %q, want %q", output, expected)
	}
}

func TestPullCmd_nothingToPull(t *testing.T) {
	f, ios, _, _ := testFactory(t)

	opts := &PullOptions{
		IO:       ios,
		Factory:  f,
		NoRemote: true,
	}

	err := runPull(opts)
	if err != nil {
		t.Fatalf("runPull() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if output != "Nothing to pull. Local store is up to date.\n" {
		t.Errorf("output = %q, want nothing to pull message", output)
	}
}

func TestPullCmd_afterPush(t *testing.T) {
	// Setup: push from one store, pull into another
	repoDir := initTestRepo(t)
	gitClient := git.NewClient(repoDir)
	ios := iostreams.Test()

	storeA := mustOpenStore(t)
	session := testSession("sess-cmd-pull-1")
	if err := storeA.Save(session); err != nil {
		t.Fatal(err)
	}

	// Push from store A
	fA := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		StoreFunc: func() (domain.Store, error) {
			return storeA, nil
		},
	}
	pushOpts := &PushOptions{IO: ios, Factory: fA, NoRemote: true}
	if err := runPush(pushOpts); err != nil {
		t.Fatal(err)
	}

	// Reset output
	ios.Out.(*bytes.Buffer).Reset()

	// Pull into store B
	storeB := mustOpenStore(t)
	fB := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		StoreFunc: func() (domain.Store, error) {
			return storeB, nil
		},
	}
	pullOpts := &PullOptions{IO: ios, Factory: fB, NoRemote: true}
	if err := runPull(pullOpts); err != nil {
		t.Fatalf("runPull() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	expected := "Pulled 1 session(s) from aisync/sessions branch.\n"
	if output != expected {
		t.Errorf("output = %q, want %q", output, expected)
	}
}

func TestSyncCmd_nothingToSync(t *testing.T) {
	f, ios, _, _ := testFactory(t)

	opts := &SyncOptions{
		IO:       ios,
		Factory:  f,
		NoRemote: true,
	}

	err := runSync(opts)
	if err != nil {
		t.Fatalf("runSync() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if output != "Everything up to date.\n" {
		t.Errorf("output = %q, want everything up to date message", output)
	}
}

func TestSyncCmd_pushAndPull(t *testing.T) {
	repoDir := initTestRepo(t)
	gitClient := git.NewClient(repoDir)
	ios := iostreams.Test()

	store := mustOpenStore(t)
	session := testSession("sess-cmd-sync-1")
	if err := store.Save(session); err != nil {
		t.Fatal(err)
	}

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		StoreFunc: func() (domain.Store, error) {
			return store, nil
		},
	}

	opts := &SyncOptions{
		IO:       ios,
		Factory:  f,
		NoRemote: true,
	}

	err := runSync(opts)
	if err != nil {
		t.Fatalf("runSync() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if output != "Pushed 1 session(s).\n" {
		t.Errorf("output = %q, want pushed 1 session message", output)
	}
}

func TestNewCmdPush_hasNoRemoteFlag(t *testing.T) {
	f, _, _, _ := testFactory(t)
	cmd := NewCmdPush(f)

	flag := cmd.Flags().Lookup("no-remote")
	if flag == nil {
		t.Fatal("expected --no-remote flag")
	}
	if flag.DefValue != "false" {
		t.Errorf("--no-remote default = %q, want false", flag.DefValue)
	}
}

func TestNewCmdPull_hasNoRemoteFlag(t *testing.T) {
	f, _, _, _ := testFactory(t)
	cmd := NewCmdPull(f)

	flag := cmd.Flags().Lookup("no-remote")
	if flag == nil {
		t.Fatal("expected --no-remote flag")
	}
}

func TestNewCmdSync_hasNoRemoteFlag(t *testing.T) {
	f, _, _, _ := testFactory(t)
	cmd := NewCmdSync(f)

	flag := cmd.Flags().Lookup("no-remote")
	if flag == nil {
		t.Fatal("expected --no-remote flag")
	}
}
