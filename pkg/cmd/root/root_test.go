package root

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

func newTestFactory() *cmdutil.Factory {
	return &cmdutil.Factory{
		IOStreams: iostreams.Test(),
	}
}

func TestNewCmdRoot_subcommands(t *testing.T) {
	f := newTestFactory()
	cmd := NewCmdRoot(f, "0.0.0-test")

	want := []string{
		"capture",
		"restore",
		"list",
		"show",
		"config",
		"version",
		"completion",
		"init",
		"status",
		"export",
	}

	subs := make(map[string]bool)
	for _, c := range cmd.Commands() {
		subs[c.Name()] = true
	}

	for _, name := range want {
		if !subs[name] {
			t.Errorf("expected subcommand %q not found", name)
		}
	}
}

func TestNewCmdRoot_version(t *testing.T) {
	f := newTestFactory()
	cmd := NewCmdRoot(f, "1.2.3")

	if cmd.Version != "1.2.3" {
		t.Errorf("Version = %q, want %q", cmd.Version, "1.2.3")
	}
}

func TestVersion_output(t *testing.T) {
	f := newTestFactory()
	cmd := NewCmdRoot(f, "1.2.3")

	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "1.2.3") {
		t.Errorf("output = %q, want it to contain %q", out, "1.2.3")
	}
}

func TestCompletion_bash(t *testing.T) {
	f := newTestFactory()
	cmd := NewCmdRoot(f, "0.0.0")

	cmd.SetArgs([]string{"completion", "bash"})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("completion bash returned error: %v", err)
	}
}

func TestCompletion_invalidShell(t *testing.T) {
	f := newTestFactory()
	cmd := NewCmdRoot(f, "0.0.0")

	cmd.SetArgs([]string{"completion", "invalid"})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid shell, got nil")
	}
}
