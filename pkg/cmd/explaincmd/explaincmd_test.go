package explaincmd

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

func explainTestFactory(t *testing.T, store *testutil.MockStore) (*cmdutil.Factory, *iostreams.IOStreams) {
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

func TestNewCmdExplain_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdExplain(f)

	flags := []string{"json", "short", "model"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestExplain_serviceInitError(t *testing.T) {
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
		SessionID: "some-id",
	}

	err := runExplain(opts)
	if err == nil {
		t.Fatal("expected error from service init failure")
	}
	if !strings.Contains(err.Error(), "initializing service") {
		t.Errorf("expected 'initializing service' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "db connection failed") {
		t.Errorf("expected 'db connection failed' in error, got: %v", err)
	}
}

func TestExplain_noLLM(t *testing.T) {
	store := testutil.NewMockStore()

	// Save a session so Get() succeeds — but Explain() will fail because no LLM.
	sess := testutil.NewSession("explain-test-1")
	store.Sessions[sess.ID] = sess

	f, ios := explainTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: string(sess.ID),
	}

	err := runExplain(opts)
	if err == nil {
		t.Fatal("expected error when no LLM client is configured")
	}
	if !strings.Contains(err.Error(), "AI explanation requires an LLM client") {
		t.Errorf("expected LLM client error, got: %v", err)
	}
}

func TestExplain_invalidSessionID(t *testing.T) {
	store := testutil.NewMockStore()
	// Store is empty — no session with this ID exists.

	f, ios := explainTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "nonexistent-session-id",
	}

	// ParseID accepts any non-empty string, so the error comes from
	// the service's Explain → store.Get → "session not found",
	// but actually it hits the LLM nil check first (no LLM configured).
	// The service checks LLM before loading from store.
	err := runExplain(opts)
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
	// The LLM nil check fires before store.Get, so we get the LLM error.
	if !strings.Contains(err.Error(), "LLM client") {
		t.Errorf("expected LLM-related error, got: %v", err)
	}
}

func TestExplain_nilSessionServiceFunc(t *testing.T) {
	ios := iostreams.Test()

	// Factory with no SessionServiceFunc → returns ErrConfigNotFound.
	f := &cmdutil.Factory{
		IOStreams: ios,
	}

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "any-id",
	}

	err := runExplain(opts)
	if err == nil {
		t.Fatal("expected error when SessionServiceFunc is nil")
	}
	if !strings.Contains(err.Error(), "initializing service") {
		t.Errorf("expected 'initializing service' in error, got: %v", err)
	}
}

func TestExplain_emptySessionID(t *testing.T) {
	store := testutil.NewMockStore()
	f, ios := explainTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "",
	}

	err := runExplain(opts)
	if err == nil {
		t.Fatal("expected error for empty session ID")
	}
	if !strings.Contains(err.Error(), "session ID cannot be empty") {
		t.Errorf("expected empty ID error, got: %v", err)
	}

	// Verify nothing was written to output.
	output := ios.Out.(*bytes.Buffer).String()
	if output != "" {
		t.Errorf("expected no output, got: %s", output)
	}
}
