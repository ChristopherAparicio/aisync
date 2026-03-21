package agentscmd

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// ---------------------------------------------------------------------------
// NewCmdAgents — subcommand registration
// ---------------------------------------------------------------------------

func TestNewCmdAgents_subcommands(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}

	cmd := NewCmdAgents(f)

	want := map[string]bool{"list": false, "tree": false, "show": false}
	for _, sub := range cmd.Commands() {
		if _, ok := want[sub.Name()]; ok {
			want[sub.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected subcommand %q not found", name)
		}
	}
}

func TestNewCmdAgents_aliases(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}

	cmd := NewCmdAgents(f)

	aliases := cmd.Aliases
	wantAliases := map[string]bool{"agent": false, "registry": false}
	for _, a := range aliases {
		wantAliases[a] = true
	}
	for alias, found := range wantAliases {
		if !found {
			t.Errorf("expected alias %q not found", alias)
		}
	}
}

// ---------------------------------------------------------------------------
// NewCmdList — flags
// ---------------------------------------------------------------------------

func TestNewCmdList_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}

	cmd := NewCmdList(f)

	if cmd.Flags().Lookup("json") == nil {
		t.Error("expected --json flag on list command")
	}
}

// ---------------------------------------------------------------------------
// NewCmdTree — flags
// ---------------------------------------------------------------------------

func TestNewCmdTree_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}

	cmd := NewCmdTree(f)

	for _, name := range []string{"json", "project"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag on tree command", name)
		}
	}
}

// ---------------------------------------------------------------------------
// NewCmdShow — flags
// ---------------------------------------------------------------------------

func TestNewCmdShow_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}

	cmd := NewCmdShow(f)

	for _, name := range []string{"json", "project"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag on show command", name)
		}
	}
}

// ---------------------------------------------------------------------------
// runList — RegistryService error
// ---------------------------------------------------------------------------

func TestList_serviceError(t *testing.T) {
	ios := iostreams.Test()

	f := &cmdutil.Factory{
		IOStreams: ios,
		RegistryServiceFunc: func() (*service.RegistryService, error) {
			return nil, errors.New("not configured")
		},
	}

	opts := &ListOptions{
		IO:      ios,
		Factory: f,
	}

	err := runList(opts)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "could not initialize registry service") {
		t.Errorf("expected 'could not initialize registry service' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected 'not configured' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// runList — no projects found
// ---------------------------------------------------------------------------

func TestList_noProjects(t *testing.T) {
	ios := iostreams.Test()

	// Create a RegistryService with an empty ScannerRegistry (no scanners → no projects).
	// ListProjects calls discoverProjectPaths which reads from XDG_DATA_HOME.
	// We set XDG_DATA_HOME to a temp dir with the expected directory structure but no project files.
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)

	projectDir := tmpDir + "/opencode/storage/project"
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	svc := service.NewRegistryService(service.RegistryServiceConfig{
		Scanners: provider.NewScannerRegistry(), // no scanners
	})

	f := &cmdutil.Factory{
		IOStreams: ios,
		RegistryServiceFunc: func() (*service.RegistryService, error) {
			return svc, nil
		},
	}

	opts := &ListOptions{
		IO:      ios,
		Factory: f,
	}

	err := runList(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "No projects found") {
		t.Errorf("expected 'No projects found' in output, got: %q", output)
	}
}

// ---------------------------------------------------------------------------
// runTree — RegistryService error
// ---------------------------------------------------------------------------

func TestTree_serviceError(t *testing.T) {
	ios := iostreams.Test()

	f := &cmdutil.Factory{
		IOStreams: ios,
		RegistryServiceFunc: func() (*service.RegistryService, error) {
			return nil, errors.New("no registry")
		},
	}

	opts := &TreeOptions{
		IO:      ios,
		Factory: f,
	}

	err := runTree(opts)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "could not initialize registry service") {
		t.Errorf("expected 'could not initialize registry service' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// runShow — RegistryService error
// ---------------------------------------------------------------------------

func TestShow_serviceError(t *testing.T) {
	ios := iostreams.Test()

	f := &cmdutil.Factory{
		IOStreams: ios,
		RegistryServiceFunc: func() (*service.RegistryService, error) {
			return nil, errors.New("no registry")
		},
	}

	opts := &ShowOptions{
		IO:      ios,
		Factory: f,
		Name:    "test-agent",
	}

	err := runShow(opts)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "could not initialize registry service") {
		t.Errorf("expected 'could not initialize registry service' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Helpers: capitalize, scopeLabel
// ---------------------------------------------------------------------------

func TestCapitalize(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"agent", "Agent"},
		{"Agent", "Agent"},
		{"a", "A"},
	}
	for _, tt := range tests {
		if got := capitalize(tt.in); got != tt.want {
			t.Errorf("capitalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestScopeLabel(t *testing.T) {
	got := scopeLabel("/home/user/project")
	if got != "(/home/user/project)" {
		t.Errorf("scopeLabel = %q, want %q", got, "(/home/user/project)")
	}
}
