package gccmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/storage"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

func gcTestFactory(t *testing.T, store *testutil.MockStore) (*cmdutil.Factory, *iostreams.IOStreams) {
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

func TestNewCmdGC_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdGC(f)

	flags := []string{"older-than", "keep-latest", "dry-run", "json"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestGC_deletesSessions(t *testing.T) {
	store := testutil.NewMockStore()
	f, ios := gcTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		OlderThan: "999h",
	}

	err := runGC(opts)
	if err != nil {
		t.Fatalf("runGC() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Deleted") {
		t.Errorf("expected 'Deleted' in output, got: %s", output)
	}
	if !strings.Contains(output, "session(s)") {
		t.Errorf("expected 'session(s)' in output, got: %s", output)
	}
}

func TestGC_dryRun(t *testing.T) {
	store := testutil.NewMockStore()
	f, ios := gcTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		OlderThan: "999h",
		DryRun:    true,
	}

	err := runGC(opts)
	if err != nil {
		t.Fatalf("runGC() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Dry run") {
		t.Errorf("expected 'Dry run' in output, got: %s", output)
	}
	if !strings.Contains(output, "would be deleted") {
		t.Errorf("expected 'would be deleted' in output, got: %s", output)
	}
}

func TestGC_jsonOutput(t *testing.T) {
	store := testutil.NewMockStore()
	f, ios := gcTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		OlderThan: "999h",
		JSON:      true,
	}

	err := runGC(opts)
	if err != nil {
		t.Fatalf("runGC() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, `"deleted"`) {
		t.Errorf("expected JSON key 'deleted' in output, got: %s", output)
	}
	if !strings.Contains(output, `"dry_run"`) {
		t.Errorf("expected JSON key 'dry_run' in output, got: %s", output)
	}
	if !strings.Contains(output, `"would"`) {
		t.Errorf("expected JSON key 'would' in output, got: %s", output)
	}
}

func TestGC_serviceError(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: ios,
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return nil, fmt.Errorf("db connection failed")
		},
	}

	opts := &Options{
		IO:        ios,
		Factory:   f,
		OlderThan: "24h",
	}

	err := runGC(opts)
	if err == nil {
		t.Fatal("expected error from runGC, got nil")
	}
	if !strings.Contains(err.Error(), "db connection failed") {
		t.Errorf("expected 'db connection failed' in error, got: %v", err)
	}
}
