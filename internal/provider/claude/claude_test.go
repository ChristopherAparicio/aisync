package claude

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/domain"
)

func TestEncodeProjectPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "simple path",
			path: "/Users/test/project",
			want: "-Users-test-project",
		},
		{
			name: "path with hyphens",
			path: "/Users/test/my-project",
			want: "-Users-test-my-project",
		},
		{
			name: "deep path",
			path: "/Users/test/dev/freelance/omogen/backend",
			want: "-Users-test-dev-freelance-omogen-backend",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := encodeProjectPath(tt.path)
			if got != tt.want {
				t.Errorf("encodeProjectPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestDetect(t *testing.T) {
	claudeHome := setupTestClaudeHome(t)
	p := New(claudeHome)

	t.Run("finds sessions for matching branch", func(t *testing.T) {
		summaries, err := p.Detect("/tmp/test/myproject", "feat/hello-world")
		if err != nil {
			t.Fatalf("Detect() error: %v", err)
		}
		if len(summaries) != 1 {
			t.Fatalf("Detect() returned %d summaries, want 1", len(summaries))
		}
		if summaries[0].Branch != "feat/hello-world" {
			t.Errorf("Branch = %q, want %q", summaries[0].Branch, "feat/hello-world")
		}
		if summaries[0].Summary != "Implemented hello world function" {
			t.Errorf("Summary = %q, want %q", summaries[0].Summary, "Implemented hello world function")
		}
	})

	t.Run("returns all sessions when branch is empty", func(t *testing.T) {
		summaries, err := p.Detect("/tmp/test/myproject", "")
		if err != nil {
			t.Fatalf("Detect() error: %v", err)
		}
		if len(summaries) != 2 {
			t.Fatalf("Detect() returned %d summaries, want 2", len(summaries))
		}
	})

	t.Run("returns empty for non-matching branch", func(t *testing.T) {
		summaries, err := p.Detect("/tmp/test/myproject", "main")
		if err != nil {
			t.Fatalf("Detect() error: %v", err)
		}
		if len(summaries) != 0 {
			t.Errorf("Detect() returned %d summaries, want 0", len(summaries))
		}
	})
}

func TestExport_simpleSession(t *testing.T) {
	claudeHome := setupTestClaudeHome(t)
	p := New(claudeHome)

	session, err := p.Export("a1b2c3d4-1111-2222-3333-444455556666", domain.StorageModeFull)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// Basic metadata
	if session.Provider != domain.ProviderClaudeCode {
		t.Errorf("Provider = %q, want %q", session.Provider, domain.ProviderClaudeCode)
	}
	if session.Agent != "claude" {
		t.Errorf("Agent = %q, want %q", session.Agent, "claude")
	}
	if session.Branch != "feat/hello-world" {
		t.Errorf("Branch = %q, want %q", session.Branch, "feat/hello-world")
	}
	if session.Summary != "Implemented hello world function" {
		t.Errorf("Summary = %q, want %q", session.Summary, "Implemented hello world function")
	}

	// Messages breakdown (JSONL lines → domain messages):
	// 1. user ("Add a hello world function to main.go")
	// 2. assistant (thinking block — same API msg_01, but separate JSONL line)
	// 3. assistant (tool_use Write — same API msg_01, separate JSONL line)
	// 4. user (tool_result) — SKIPPED: no text content, merged into tool call
	// 5. assistant (text response — API msg_02)
	// 6. user ("Looks great, thanks!")
	// = 5 domain messages
	if len(session.Messages) != 5 {
		t.Fatalf("Messages count = %d, want 5", len(session.Messages))
	}

	// First message: user
	if session.Messages[0].Role != domain.RoleUser {
		t.Errorf("Messages[0].Role = %q, want %q", session.Messages[0].Role, domain.RoleUser)
	}
	if session.Messages[0].Content != "Add a hello world function to main.go" {
		t.Errorf("Messages[0].Content = %q, want %q", session.Messages[0].Content, "Add a hello world function to main.go")
	}

	// Second message: assistant with thinking (full mode)
	if session.Messages[1].Role != domain.RoleAssistant {
		t.Errorf("Messages[1].Role = %q, want %q", session.Messages[1].Role, domain.RoleAssistant)
	}
	if session.Messages[1].Thinking == "" {
		t.Error("Messages[1].Thinking should not be empty in full mode")
	}

	// Third message: assistant with tool_use (Write)
	if len(session.Messages[2].ToolCalls) != 1 {
		t.Fatalf("Messages[2].ToolCalls count = %d, want 1", len(session.Messages[2].ToolCalls))
	}
	tc := session.Messages[2].ToolCalls[0]
	if tc.Name != "Write" {
		t.Errorf("ToolCall.Name = %q, want %q", tc.Name, "Write")
	}
	if tc.State != domain.ToolStateCompleted {
		t.Errorf("ToolCall.State = %q, want %q", tc.State, domain.ToolStateCompleted)
	}
	if tc.Output != "File written successfully." {
		t.Errorf("ToolCall.Output = %q, want %q", tc.Output, "File written successfully.")
	}

	// Token usage
	if session.TokenUsage.TotalTokens == 0 {
		t.Error("TotalTokens should not be 0")
	}

	// File changes
	if len(session.FileChanges) != 1 {
		t.Fatalf("FileChanges count = %d, want 1", len(session.FileChanges))
	}
	if session.FileChanges[0].FilePath != "main.go" {
		t.Errorf("FileChange.FilePath = %q, want %q", session.FileChanges[0].FilePath, "main.go")
	}
}

func TestExport_compactMode(t *testing.T) {
	claudeHome := setupTestClaudeHome(t)
	p := New(claudeHome)

	session, err := p.Export("a1b2c3d4-1111-2222-3333-444455556666", domain.StorageModeCompact)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// Compact mode: no thinking, but has tool calls
	for _, msg := range session.Messages {
		if msg.Thinking != "" {
			t.Error("Thinking should be empty in compact mode")
		}
	}
}

func TestExport_summaryMode(t *testing.T) {
	claudeHome := setupTestClaudeHome(t)
	p := New(claudeHome)

	session, err := p.Export("a1b2c3d4-1111-2222-3333-444455556666", domain.StorageModeSummary)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	if session.Summary != "Implemented hello world function" {
		t.Errorf("Summary = %q, want %q", session.Summary, "Implemented hello world function")
	}
	if len(session.Messages) != 0 {
		t.Errorf("Messages count = %d, want 0 in summary mode", len(session.Messages))
	}
}

func TestExport_toolError(t *testing.T) {
	claudeHome := setupTestClaudeHome(t)
	p := New(claudeHome)

	session, err := p.Export("c3d4e5f6-3333-4444-5555-666677778888", domain.StorageModeFull)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// Find the assistant message with tool calls
	var foundErrorTool bool
	for _, msg := range session.Messages {
		for _, tc := range msg.ToolCalls {
			if tc.Name == "Read" && tc.State == domain.ToolStateError {
				foundErrorTool = true
			}
		}
	}
	if !foundErrorTool {
		t.Error("Expected to find a tool call with error state")
	}
}

func TestExport_sessionNotFound(t *testing.T) {
	claudeHome := setupTestClaudeHome(t)
	p := New(claudeHome)

	_, err := p.Export("nonexistent-session-id", domain.StorageModeFull)
	if err != domain.ErrSessionNotFound {
		t.Errorf("Export() error = %v, want ErrSessionNotFound", err)
	}
}

func TestName(t *testing.T) {
	p := New("")
	if p.Name() != domain.ProviderClaudeCode {
		t.Errorf("Name() = %q, want %q", p.Name(), domain.ProviderClaudeCode)
	}
}

func TestCanImport(t *testing.T) {
	p := New("")
	if !p.CanImport() {
		t.Error("CanImport() = false, want true")
	}
}

// setupTestClaudeHome creates a temporary claude home with test fixtures.
func setupTestClaudeHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Create the project directory structure
	projectDir := filepath.Join(dir, projectsDir, encodeProjectPath("/tmp/test/myproject"))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll error: %v", err)
	}

	// Copy test fixtures
	fixtures := []struct {
		src string
		dst string
	}{
		{"testdata/sessions-index.json", filepath.Join(projectDir, sessionsIndex)},
		{"testdata/session_simple.jsonl", filepath.Join(projectDir, "a1b2c3d4-1111-2222-3333-444455556666.jsonl")},
		{"testdata/session_with_error.jsonl", filepath.Join(projectDir, "c3d4e5f6-3333-4444-5555-666677778888.jsonl")},
	}

	for _, f := range fixtures {
		data, err := os.ReadFile(f.src)
		if err != nil {
			t.Fatalf("ReadFile(%s) error: %v", f.src, err)
		}
		if err := os.WriteFile(f.dst, data, 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error: %v", f.dst, err)
		}
	}

	return dir
}
