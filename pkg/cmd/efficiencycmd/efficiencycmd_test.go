package efficiencycmd

import (
	"bytes"
	"fmt"
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

func efficiencyTestFactory(t *testing.T, store *testutil.MockStore) (*cmdutil.Factory, *iostreams.IOStreams) {
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
			// No LLM client — AnalyzeEfficiency will fail with "requires an LLM client"
			return service.NewSessionService(service.SessionServiceConfig{
				Store: store,
				Git:   gitClient,
			}), nil
		},
	}

	return f, ios
}

func TestNewCmdEfficiency_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdEfficiency(f)

	flags := []string{"json", "model"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestEfficiency_serviceInitError(t *testing.T) {
	ios := iostreams.Test()

	f := &cmdutil.Factory{
		IOStreams: ios,
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return nil, fmt.Errorf("database connection failed")
		},
	}

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "any",
	}

	err := runEfficiency(opts)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "database connection failed") {
		t.Errorf("expected 'database connection failed' in error, got: %v", err)
	}
}

func TestEfficiency_noLLM(t *testing.T) {
	store := testutil.NewMockStore()
	now := time.Now()

	sess := &session.Session{
		ID:       "eff-test",
		Provider: session.ProviderClaudeCode,
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "fix the bug", Timestamp: now},
			{ID: "m2", Role: session.RoleAssistant, Content: "Done", Timestamp: now},
		},
		CreatedAt: now,
	}
	_ = store.Save(sess)

	// Factory without LLM — AnalyzeEfficiency should return an error
	f, ios := efficiencyTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "eff-test",
	}

	err := runEfficiency(opts)
	if err == nil {
		t.Fatal("expected error when no LLM client is configured")
	}
	if !strings.Contains(err.Error(), "requires an LLM client") {
		t.Errorf("expected 'requires an LLM client' error, got: %v", err)
	}

	// Verify no output was written (error should have occurred before output)
	output := ios.Out.(*bytes.Buffer).String()
	if output != "" {
		t.Errorf("expected no output on LLM error, got: %s", output)
	}
}
