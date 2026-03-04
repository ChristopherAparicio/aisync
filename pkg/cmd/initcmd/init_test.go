package initcmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/hooks"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

func TestInitCmd_createsConfig(t *testing.T) {
	dir := initTestRepo(t)
	f := testFactory(t, dir)

	var buf bytes.Buffer
	f.IOStreams.Out = &buf

	opts := &InitOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	if err := runInit(opts); err != nil {
		t.Fatalf("runInit() error: %v", err)
	}

	// Verify config file was created
	configFile := filepath.Join(dir, ".aisync", "config.json")
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		t.Errorf("config file not created at %s", configFile)
	}

	output := buf.String()
	if !strings.Contains(output, "Initialized aisync") {
		t.Errorf("output should contain 'Initialized aisync', got: %s", output)
	}
}

func TestInitCmd_alreadyInitialized(t *testing.T) {
	dir := initTestRepo(t)
	f := testFactory(t, dir)

	// Pre-create the config file
	configDir := filepath.Join(dir, ".aisync")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	var buf bytes.Buffer
	f.IOStreams.Out = &buf

	opts := &InitOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	if err := runInit(opts); err != nil {
		t.Fatalf("runInit() error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "already initialized") {
		t.Errorf("output should contain 'already initialized', got: %s", output)
	}
}

func TestInitCmd_noHooksFlag(t *testing.T) {
	dir := initTestRepo(t)
	f := testFactory(t, dir)

	var buf bytes.Buffer
	f.IOStreams.Out = &buf

	opts := &InitOptions{
		IO:      f.IOStreams,
		Factory: f,
		NoHooks: true,
	}

	if err := runInit(opts); err != nil {
		t.Fatalf("runInit() error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "--no-hooks") {
		t.Errorf("output should mention --no-hooks, got: %s", output)
	}

	// Verify hooks were NOT installed
	hooksDir := filepath.Join(dir, ".git", "hooks")
	hookFile := filepath.Join(hooksDir, "pre-commit")
	data, err := os.ReadFile(hookFile)
	if err == nil && strings.Contains(string(data), "aisync:start") {
		t.Error("hooks should NOT be installed when --no-hooks is set")
	}
}

func TestInitCmd_installsHooks(t *testing.T) {
	dir := initTestRepo(t)
	f := testFactory(t, dir)

	var buf bytes.Buffer
	f.IOStreams.Out = &buf

	opts := &InitOptions{
		IO:      f.IOStreams,
		Factory: f,
		NoHooks: false,
	}

	if err := runInit(opts); err != nil {
		t.Fatalf("runInit() error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Hooks: installed") {
		t.Errorf("output should confirm hooks installed, got: %s", output)
	}

	// Verify hooks were installed on disk
	hooksDir := filepath.Join(dir, ".git", "hooks")
	for _, name := range []string{"pre-commit", "commit-msg", "post-checkout"} {
		hookFile := filepath.Join(hooksDir, name)
		data, err := os.ReadFile(hookFile)
		if err != nil {
			t.Errorf("hook %s not created: %v", name, err)
			continue
		}
		if !strings.Contains(string(data), "aisync:start") {
			t.Errorf("hook %s missing aisync section", name)
		}
	}
}

// testFactory creates a Factory for testing with a temp repo directory.
func testFactory(t *testing.T, repoDir string) *cmdutil.Factory {
	t.Helper()

	globalDir := filepath.Join(t.TempDir(), ".aisync")
	gitClient := git.NewClient(repoDir)

	f := &cmdutil.Factory{
		IOStreams: &iostreams.IOStreams{
			Out:    &bytes.Buffer{},
			ErrOut: &bytes.Buffer{},
		},
	}

	f.GitFunc = func() (*git.Client, error) {
		return gitClient, nil
	}

	f.ConfigFunc = func() (*config.Config, error) {
		return config.New(globalDir, filepath.Join(repoDir, ".aisync"))
	}

	f.HooksManagerFunc = func() (*hooks.Manager, error) {
		hooksDir, err := gitClient.HooksPath()
		if err != nil {
			return nil, err
		}
		return hooks.NewManager(hooksDir), nil
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
