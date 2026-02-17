package hooks

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

func TestCmdHooksInstall(t *testing.T) {
	dir := initTestRepo(t)
	f := testFactory(t, dir)

	var buf bytes.Buffer
	f.IOStreams.Out = &buf

	opts := &InstallOptions{IO: f.IOStreams, Factory: f}
	err := runInstall(opts)
	if err != nil {
		t.Fatalf("runInstall() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Git hooks installed!") {
		t.Error("expected success message in output")
	}
	if !strings.Contains(output, "pre-commit: installed") {
		t.Error("expected pre-commit status in output")
	}

	// Verify hooks actually exist on disk
	hooksDir := filepath.Join(dir, ".git", "hooks")
	for _, name := range []string{"pre-commit", "commit-msg", "post-checkout"} {
		hookPath := filepath.Join(hooksDir, name)
		if _, statErr := os.Stat(hookPath); os.IsNotExist(statErr) {
			t.Errorf("hook %s was not created", name)
		}
	}
}

func TestCmdHooksUninstall(t *testing.T) {
	dir := initTestRepo(t)
	f := testFactory(t, dir)

	var buf bytes.Buffer
	f.IOStreams.Out = &buf

	// Install first
	installOpts := &InstallOptions{IO: f.IOStreams, Factory: f}
	if err := runInstall(installOpts); err != nil {
		t.Fatalf("runInstall() error = %v", err)
	}

	// Reset buffer
	buf.Reset()

	// Uninstall
	uninstallOpts := &UninstallOptions{IO: f.IOStreams, Factory: f}
	if err := runUninstall(uninstallOpts); err != nil {
		t.Fatalf("runUninstall() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Git hooks removed.") {
		t.Error("expected success message in output")
	}
}

func TestCmdHooksStatus(t *testing.T) {
	dir := initTestRepo(t)
	f := testFactory(t, dir)

	var buf bytes.Buffer
	f.IOStreams.Out = &buf

	// Status before install
	opts := &StatusOptions{IO: f.IOStreams, Factory: f}
	if err := runStatus(opts); err != nil {
		t.Fatalf("runStatus() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "not installed") {
		t.Error("expected 'not installed' before install")
	}

	// Install
	buf.Reset()
	installOpts := &InstallOptions{IO: f.IOStreams, Factory: f}
	if err := runInstall(installOpts); err != nil {
		t.Fatal(err)
	}

	// Status after install
	buf.Reset()
	if err := runStatus(opts); err != nil {
		t.Fatal(err)
	}

	output = buf.String()
	if strings.Contains(output, "not installed") {
		t.Error("expected all hooks to be installed after Install()")
	}
	if !strings.Contains(output, "pre-commit: installed") {
		t.Error("expected pre-commit: installed")
	}
}

func TestNewCmdHooks_hasSubcommands(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: iostreams.Test()}
	cmd := NewCmdHooks(f)

	subcommands := cmd.Commands()
	if len(subcommands) != 3 {
		t.Fatalf("expected 3 subcommands, got %d", len(subcommands))
	}

	names := make(map[string]bool)
	for _, sub := range subcommands {
		names[sub.Use] = true
	}
	for _, expected := range []string{"install", "uninstall", "status"} {
		if !names[expected] {
			t.Errorf("missing subcommand %q", expected)
		}
	}
}

func testFactory(t *testing.T, repoDir string) *cmdutil.Factory {
	t.Helper()
	client := git.NewClient(repoDir)

	return &cmdutil.Factory{
		IOStreams: iostreams.Test(),
		GitFunc: func() (*git.Client, error) {
			return client, nil
		},
	}
}

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
