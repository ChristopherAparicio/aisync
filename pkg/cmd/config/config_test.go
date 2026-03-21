package config

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// newTestFactory returns a Factory wired to the given config and a buffer
// capturing stdout.
func newTestFactory(t *testing.T, cfg *config.Config) (*cmdutil.Factory, *bytes.Buffer) {
	t.Helper()
	ios := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams:  ios,
		ConfigFunc: func() (*config.Config, error) { return cfg, nil },
	}
	return f, ios.Out.(*bytes.Buffer)
}

// newTestConfig creates a real Config backed by temp dirs.
func newTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	return cfg
}

// --- Tests ---

func TestNewCmdConfig_subcommands(t *testing.T) {
	cfg := newTestConfig(t)
	f, _ := newTestFactory(t, cfg)
	cmd := NewCmdConfig(f)

	want := map[string]bool{"get": false, "set": false, "list": false}
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

func TestConfigGet_success(t *testing.T) {
	cfg := newTestConfig(t)
	// Pre-set a value so we can read it back.
	if err := cfg.Set("storage_mode", "full"); err != nil {
		t.Fatalf("cfg.Set: %v", err)
	}

	f, out := newTestFactory(t, cfg)

	cmd := NewCmdConfig(f)
	cmd.SetArgs([]string{"get", "storage_mode"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := out.String()
	if got != "full\n" {
		t.Errorf("got %q, want %q", got, "full\n")
	}
}

func TestConfigGet_unknownKey(t *testing.T) {
	cfg := newTestConfig(t)
	f, _ := newTestFactory(t, cfg)

	cmd := NewCmdConfig(f)
	cmd.SetArgs([]string{"get", "no_such_key"})
	// Silence usage on error so the test output stays clean.
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
	if got := err.Error(); !contains(got, "unknown config key") {
		t.Errorf("error = %q, want it to contain %q", got, "unknown config key")
	}
}

func TestConfigGet_configError(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams:  ios,
		ConfigFunc: func() (*config.Config, error) { return nil, fmt.Errorf("boom") },
	}

	cmd := NewCmdConfig(f)
	cmd.SetArgs([]string{"get", "storage_mode"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); !contains(got, "boom") {
		t.Errorf("error = %q, want it to contain %q", got, "boom")
	}
}

func TestConfigSet_success(t *testing.T) {
	cfg := newTestConfig(t)
	f, out := newTestFactory(t, cfg)

	cmd := NewCmdConfig(f)
	cmd.SetArgs([]string{"set", "storage_mode", "full"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := out.String()
	if !contains(got, "Set storage_mode = full") {
		t.Errorf("output = %q, want it to contain %q", got, "Set storage_mode = full")
	}

	// Verify the value was actually persisted in the config object.
	val, err := cfg.Get("storage_mode")
	if err != nil {
		t.Fatalf("cfg.Get: %v", err)
	}
	if val != "full" {
		t.Errorf("cfg.Get(storage_mode) = %q, want %q", val, "full")
	}
}

func TestConfigSet_configError(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams:  ios,
		ConfigFunc: func() (*config.Config, error) { return nil, fmt.Errorf("kaboom") },
	}

	cmd := NewCmdConfig(f)
	cmd.SetArgs([]string{"set", "storage_mode", "full"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); !contains(got, "kaboom") {
		t.Errorf("error = %q, want it to contain %q", got, "kaboom")
	}
}

func TestConfigList(t *testing.T) {
	cfg := newTestConfig(t)
	f, out := newTestFactory(t, cfg)

	cmd := NewCmdConfig(f)
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := out.String()
	for _, key := range []string{"storage_mode", "auto_capture", "secrets.mode"} {
		if !contains(got, key) {
			t.Errorf("output missing key %q; got:\n%s", key, got)
		}
	}
}

func TestConfigList_alias(t *testing.T) {
	cfg := newTestConfig(t)
	f, out := newTestFactory(t, cfg)

	cmd := NewCmdConfig(f)
	cmd.SetArgs([]string{"ls"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := out.String()
	if !contains(got, "storage_mode") {
		t.Errorf("ls alias should work like list; got:\n%s", got)
	}
}

func TestValidConfigKeys(t *testing.T) {
	keys := ValidConfigKeys()
	want := map[string]bool{
		"storage_mode":                true,
		"auto_capture":                true,
		"secrets.mode":                true,
		"secrets.custom_patterns.add": true,
	}

	if len(keys) != len(want) {
		t.Fatalf("len(ValidConfigKeys()) = %d, want %d", len(keys), len(want))
	}
	for _, k := range keys {
		if !want[k] {
			t.Errorf("unexpected key %q in ValidConfigKeys()", k)
		}
	}
}

// contains is a small helper so we don't need strings as a dependency.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
