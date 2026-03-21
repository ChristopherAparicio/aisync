package replay

import (
	"context"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Compare Tests ──

func TestCompare_Improved(t *testing.T) {
	original := &session.Session{
		TokenUsage: session.TokenUsage{TotalTokens: 10000},
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "do task"},
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "Write", State: session.ToolStateCompleted},
				{Name: "bash", State: session.ToolStateError},
				{Name: "bash", State: session.ToolStateError},
			}},
		},
	}
	replay := &session.Session{
		TokenUsage: session.TokenUsage{TotalTokens: 7000},
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "do task"},
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "Write", State: session.ToolStateCompleted},
				{Name: "skill", Input: `{"name":"before-commit"}`},
			}},
		},
	}

	c := Compare(original, replay)
	if c.Verdict != "improved" {
		t.Errorf("Verdict = %q, want %q", c.Verdict, "improved")
	}
	if c.OriginalErrors != 2 {
		t.Errorf("OriginalErrors = %d, want 2", c.OriginalErrors)
	}
	if c.ReplayErrors != 0 {
		t.Errorf("ReplayErrors = %d, want 0", c.ReplayErrors)
	}
	if c.TokenDelta >= 0 {
		t.Errorf("TokenDelta = %d, want negative (fewer tokens)", c.TokenDelta)
	}
	if len(c.NewSkillsLoaded) != 1 || c.NewSkillsLoaded[0] != "before-commit" {
		t.Errorf("NewSkillsLoaded = %v, want [before-commit]", c.NewSkillsLoaded)
	}
}

func TestCompare_Degraded(t *testing.T) {
	original := &session.Session{
		TokenUsage: session.TokenUsage{TotalTokens: 5000},
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "task"},
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "Write", State: session.ToolStateCompleted},
			}},
		},
	}
	replay := &session.Session{
		TokenUsage: session.TokenUsage{TotalTokens: 15000},
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "task"},
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "Write", State: session.ToolStateError},
				{Name: "Write", State: session.ToolStateError},
				{Name: "bash", State: session.ToolStateError},
			}},
		},
	}

	c := Compare(original, replay)
	if c.Verdict != "degraded" {
		t.Errorf("Verdict = %q, want %q", c.Verdict, "degraded")
	}
}

func TestCompare_Same(t *testing.T) {
	sess := &session.Session{
		TokenUsage: session.TokenUsage{TotalTokens: 5000},
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "task"},
			{Role: session.RoleAssistant, Content: "done"},
		},
	}

	c := Compare(sess, sess)
	if c.Verdict != "same" {
		t.Errorf("Verdict = %q, want %q", c.Verdict, "same")
	}
	if c.TokenDelta != 0 {
		t.Errorf("TokenDelta = %d, want 0", c.TokenDelta)
	}
}

// ── ExtractUserMessages Tests ──

func TestExtractUserMessages(t *testing.T) {
	messages := []session.Message{
		{Role: session.RoleUser, Content: "first"},
		{Role: session.RoleAssistant, Content: "response"},
		{Role: session.RoleUser, Content: "second"},
		{Role: session.RoleUser, Content: "third"},
	}

	got := extractUserMessages(messages, 0)
	if len(got) != 3 {
		t.Fatalf("expected 3 user messages, got %d", len(got))
	}
	if got[0] != "first" || got[1] != "second" || got[2] != "third" {
		t.Errorf("messages = %v", got)
	}
}

func TestExtractUserMessages_Limited(t *testing.T) {
	messages := []session.Message{
		{Role: session.RoleUser, Content: "1"},
		{Role: session.RoleUser, Content: "2"},
		{Role: session.RoleUser, Content: "3"},
	}

	got := extractUserMessages(messages, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 (limited), got %d", len(got))
	}
}

func TestExtractUserMessages_Empty(t *testing.T) {
	got := extractUserMessages(nil, 0)
	if len(got) != 0 {
		t.Errorf("expected 0, got %d", len(got))
	}
}

// ── Mock Runner ──

type mockRunner struct {
	name   string
	output string
	err    error
	calls  []string
}

func (m *mockRunner) Name() string { return m.name }
func (m *mockRunner) Run(_ context.Context, _ string, message string, _ RunOptions) (string, error) {
	m.calls = append(m.calls, message)
	return m.output, m.err
}

// ── Worktree Tests (only run in git repos) ──

func TestCreateWorktree_NotGitRepo(t *testing.T) {
	tmpDir := t.TempDir() // not a git repo
	_, err := CreateWorktree(tmpDir, "HEAD")
	if err == nil {
		t.Fatal("expected error for non-git repo")
	}
}

// ── Integration: Worktree in actual repo ──

func TestCreateWorktree_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping worktree integration test")
	}

	// Use the aisync repo itself as the test repo.
	// This test only works when run from within the aisync git repo.
	wt, err := CreateWorktree(".", "HEAD")
	if err != nil {
		t.Skipf("could not create worktree (not in a git repo?): %v", err)
	}
	defer wt.Remove()

	if wt.Path() == "" {
		t.Error("worktree path is empty")
	}

	t.Logf("worktree created at %s", wt.Path())
}
