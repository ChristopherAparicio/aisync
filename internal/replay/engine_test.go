package replay_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/replay"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
)

// ── Mock Runner (external test) ──

type mockRunner struct {
	name   string
	output string
	err    error
	calls  []string
}

func (m *mockRunner) Name() string { return m.name }
func (m *mockRunner) Run(_ context.Context, _ string, message string, _ replay.RunOptions) (string, error) {
	m.calls = append(m.calls, message)
	return m.output, m.err
}

// ── Engine Tests ──

func TestEngine_SessionNotFound(t *testing.T) {
	store := testutil.NewMockStore()
	runner := &mockRunner{name: "opencode"}

	engine := replay.NewEngine(replay.EngineConfig{
		Store:   store,
		Runners: []replay.Runner{runner},
	})

	_, err := engine.Replay(context.Background(), replay.Request{
		SourceSessionID: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestEngine_NoRunnerForProvider(t *testing.T) {
	sess := &session.Session{
		ID:          "test-session",
		Provider:    session.ProviderClaudeCode,
		Agent:       "default",
		ProjectPath: "/tmp/some-project",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "do something"},
		},
	}
	store := testutil.NewMockStore(sess)

	// Only an opencode runner, but session uses claude-code.
	runner := &mockRunner{name: "opencode"}
	engine := replay.NewEngine(replay.EngineConfig{
		Store:   store,
		Runners: []replay.Runner{runner},
	})

	_, err := engine.Replay(context.Background(), replay.Request{
		SourceSessionID: "test-session",
	})
	if err == nil {
		t.Fatal("expected error for missing runner")
	}
	t.Logf("got expected error: %v", err)
}

func TestEngine_NoUserMessages(t *testing.T) {
	sess := &session.Session{
		ID:          "test-session",
		Provider:    session.ProviderOpenCode,
		Agent:       "coder",
		ProjectPath: "/tmp/some-project",
		Messages: []session.Message{
			{Role: session.RoleAssistant, Content: "hello"}, // no user messages
		},
	}
	store := testutil.NewMockStore(sess)

	runner := &mockRunner{name: "opencode"}
	engine := replay.NewEngine(replay.EngineConfig{
		Store:   store,
		Runners: []replay.Runner{runner},
	})

	_, err := engine.Replay(context.Background(), replay.Request{
		SourceSessionID: "test-session",
	})
	if err == nil {
		t.Fatal("expected error for no user messages")
	}
	t.Logf("got expected error: %v", err)
}

func TestEngine_NoProjectPath(t *testing.T) {
	sess := &session.Session{
		ID:       "test-session",
		Provider: session.ProviderOpenCode,
		Agent:    "coder",
		// ProjectPath intentionally empty.
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "do something"},
		},
	}
	store := testutil.NewMockStore(sess)

	runner := &mockRunner{name: "opencode"}
	engine := replay.NewEngine(replay.EngineConfig{
		Store:   store,
		Runners: []replay.Runner{runner},
	})

	_, err := engine.Replay(context.Background(), replay.Request{
		SourceSessionID: "test-session",
	})
	if err == nil {
		t.Fatal("expected error for no project path")
	}
	t.Logf("got expected error: %v", err)
}

func TestEngine_ProviderOverride(t *testing.T) {
	sess := &session.Session{
		ID:          "test-session",
		Provider:    session.ProviderClaudeCode,
		Agent:       "default",
		ProjectPath: "/tmp/some-project",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "do something"},
		},
	}
	store := testutil.NewMockStore(sess)

	// Override to use opencode runner.
	runner := &mockRunner{name: "opencode"}
	engine := replay.NewEngine(replay.EngineConfig{
		Store:   store,
		Runners: []replay.Runner{runner},
	})

	// This will fail at worktree creation (not a git repo) but verifies
	// the provider override path is taken (runner selected correctly).
	_, err := engine.Replay(context.Background(), replay.Request{
		SourceSessionID: "test-session",
		Provider:        session.ProviderOpenCode,
	})
	// The error should be about the worktree, NOT about the runner.
	if err == nil {
		t.Fatal("expected error (worktree creation)")
	}
	// If it says "no runner available for provider claude-code", the override failed.
	wantNotContain := fmt.Sprintf("no runner available for provider %q", session.ProviderClaudeCode)
	if err.Error() == wantNotContain {
		t.Fatal("provider override was not applied — still looking for claude-code runner")
	}
	t.Logf("got expected worktree error: %v", err)
}

func TestEngine_ContextCancellation(t *testing.T) {
	sess := &session.Session{
		ID:          "test-session",
		Provider:    session.ProviderOpenCode,
		Agent:       "coder",
		ProjectPath: "/tmp/some-project",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "do something"},
		},
	}
	store := testutil.NewMockStore(sess)

	runner := &mockRunner{name: "opencode"}
	engine := replay.NewEngine(replay.EngineConfig{
		Store:   store,
		Runners: []replay.Runner{runner},
	})

	// Cancel the context immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// This will fail at worktree creation, but validates ctx is propagated.
	_, err := engine.Replay(ctx, replay.Request{
		SourceSessionID: "test-session",
	})
	if err == nil {
		t.Fatal("expected error for cancelled context or worktree")
	}
	t.Logf("got error: %v", err)
}
