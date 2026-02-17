package status

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/internal/storage/sqlite"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

func TestStatusCmd_showsBranchAndProviders(t *testing.T) {
	dir := initTestRepo(t)
	f := testFactory(t, dir)

	var buf bytes.Buffer
	f.IOStreams.Out = &buf

	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	if err := runStatus(opts); err != nil {
		t.Fatalf("runStatus() error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Branch:") {
		t.Errorf("output should contain 'Branch:', got: %s", output)
	}
	if !strings.Contains(output, "Providers:") {
		t.Errorf("output should contain 'Providers:', got: %s", output)
	}
	if !strings.Contains(output, "Session:") {
		t.Errorf("output should contain 'Session:', got: %s", output)
	}
	if !strings.Contains(output, "Hooks:") {
		t.Errorf("output should contain 'Hooks:', got: %s", output)
	}
}

func TestStatusCmd_noSession(t *testing.T) {
	dir := initTestRepo(t)
	f := testFactory(t, dir)

	var buf bytes.Buffer
	f.IOStreams.Out = &buf

	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	if err := runStatus(opts); err != nil {
		t.Fatalf("runStatus() error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "none on this branch") {
		t.Errorf("output should contain 'none on this branch', got: %s", output)
	}
}

func TestStatusCmd_withSession(t *testing.T) {
	dir := initTestRepo(t)
	f := testFactory(t, dir)

	// Save a session to the store
	store, err := f.Store()
	if err != nil {
		t.Fatalf("Store() error: %v", err)
	}
	defer func() { _ = store.Close() }()

	gitClient, _ := f.Git()
	topLevel, _ := gitClient.TopLevel()
	branch, _ := gitClient.CurrentBranch()

	session := &domain.Session{
		ID:          domain.NewSessionID(),
		Provider:    domain.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      branch,
		ProjectPath: topLevel,
		StorageMode: domain.StorageModeCompact,
		Messages: []domain.Message{
			{ID: "m1", Role: domain.RoleUser, Content: "hello"},
			{ID: "m2", Role: domain.RoleAssistant, Content: "hi"},
		},
	}

	if saveErr := store.Save(session); saveErr != nil {
		t.Fatalf("Save() error: %v", saveErr)
	}

	var buf bytes.Buffer
	f.IOStreams.Out = &buf

	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	if statusErr := runStatus(opts); statusErr != nil {
		t.Fatalf("runStatus() error: %v", statusErr)
	}

	output := buf.String()
	if !strings.Contains(output, "claude-code") {
		t.Errorf("output should contain 'claude-code', got: %s", output)
	}
	if !strings.Contains(output, "2 messages") {
		t.Errorf("output should contain '2 messages', got: %s", output)
	}
}

// testFactory creates a Factory for testing with a temp repo directory.
func testFactory(t *testing.T, repoDir string) *cmdutil.Factory {
	t.Helper()

	globalDir := filepath.Join(t.TempDir(), ".aisync")
	dbPath := filepath.Join(t.TempDir(), "test.db")

	f := &cmdutil.Factory{
		IOStreams: &iostreams.IOStreams{
			Out:    &bytes.Buffer{},
			ErrOut: &bytes.Buffer{},
		},
	}

	f.GitFunc = func() (*git.Client, error) {
		return git.NewClient(repoDir), nil
	}

	f.ConfigFunc = func() (domain.Config, error) {
		return config.New(globalDir, filepath.Join(repoDir, ".aisync"))
	}

	f.StoreFunc = func() (domain.Store, error) {
		return sqlite.New(dbPath)
	}

	return f
}

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
