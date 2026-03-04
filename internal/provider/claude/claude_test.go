package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
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

	sess, err := p.Export("a1b2c3d4-1111-2222-3333-444455556666", session.StorageModeFull)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// Basic metadata
	if sess.Provider != session.ProviderClaudeCode {
		t.Errorf("Provider = %q, want %q", sess.Provider, session.ProviderClaudeCode)
	}
	if sess.Agent != "claude" {
		t.Errorf("Agent = %q, want %q", sess.Agent, "claude")
	}
	if sess.Branch != "feat/hello-world" {
		t.Errorf("Branch = %q, want %q", sess.Branch, "feat/hello-world")
	}
	if sess.Summary != "Implemented hello world function" {
		t.Errorf("Summary = %q, want %q", sess.Summary, "Implemented hello world function")
	}

	// Messages breakdown (JSONL lines → domain messages):
	// 1. user ("Add a hello world function to main.go")
	// 2. assistant (thinking block — same API msg_01, but separate JSONL line)
	// 3. assistant (tool_use Write — same API msg_01, separate JSONL line)
	// 4. user (tool_result) — SKIPPED: no text content, merged into tool call
	// 5. assistant (text response — API msg_02)
	// 6. user ("Looks great, thanks!")
	// = 5 domain messages
	if len(sess.Messages) != 5 {
		t.Fatalf("Messages count = %d, want 5", len(sess.Messages))
	}

	// First message: user
	if sess.Messages[0].Role != session.RoleUser {
		t.Errorf("Messages[0].Role = %q, want %q", sess.Messages[0].Role, session.RoleUser)
	}
	if sess.Messages[0].Content != "Add a hello world function to main.go" {
		t.Errorf("Messages[0].Content = %q, want %q", sess.Messages[0].Content, "Add a hello world function to main.go")
	}

	// Second message: assistant with thinking (full mode)
	if sess.Messages[1].Role != session.RoleAssistant {
		t.Errorf("Messages[1].Role = %q, want %q", sess.Messages[1].Role, session.RoleAssistant)
	}
	if sess.Messages[1].Thinking == "" {
		t.Error("Messages[1].Thinking should not be empty in full mode")
	}

	// Third message: assistant with tool_use (Write)
	if len(sess.Messages[2].ToolCalls) != 1 {
		t.Fatalf("Messages[2].ToolCalls count = %d, want 1", len(sess.Messages[2].ToolCalls))
	}
	tc := sess.Messages[2].ToolCalls[0]
	if tc.Name != "Write" {
		t.Errorf("ToolCall.Name = %q, want %q", tc.Name, "Write")
	}
	if tc.State != session.ToolStateCompleted {
		t.Errorf("ToolCall.State = %q, want %q", tc.State, session.ToolStateCompleted)
	}
	if tc.Output != "File written successfully." {
		t.Errorf("ToolCall.Output = %q, want %q", tc.Output, "File written successfully.")
	}

	// Token usage
	if sess.TokenUsage.TotalTokens == 0 {
		t.Error("TotalTokens should not be 0")
	}

	// File changes
	if len(sess.FileChanges) != 1 {
		t.Fatalf("FileChanges count = %d, want 1", len(sess.FileChanges))
	}
	if sess.FileChanges[0].FilePath != "main.go" {
		t.Errorf("FileChange.FilePath = %q, want %q", sess.FileChanges[0].FilePath, "main.go")
	}
}

func TestExport_compactMode(t *testing.T) {
	claudeHome := setupTestClaudeHome(t)
	p := New(claudeHome)

	sess, err := p.Export("a1b2c3d4-1111-2222-3333-444455556666", session.StorageModeCompact)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// Compact mode: no thinking, but has tool calls
	for _, msg := range sess.Messages {
		if msg.Thinking != "" {
			t.Error("Thinking should be empty in compact mode")
		}
	}
}

func TestExport_summaryMode(t *testing.T) {
	claudeHome := setupTestClaudeHome(t)
	p := New(claudeHome)

	sess, err := p.Export("a1b2c3d4-1111-2222-3333-444455556666", session.StorageModeSummary)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	if sess.Summary != "Implemented hello world function" {
		t.Errorf("Summary = %q, want %q", sess.Summary, "Implemented hello world function")
	}
	if len(sess.Messages) != 0 {
		t.Errorf("Messages count = %d, want 0 in summary mode", len(sess.Messages))
	}
}

func TestExport_toolError(t *testing.T) {
	claudeHome := setupTestClaudeHome(t)
	p := New(claudeHome)

	sess, err := p.Export("c3d4e5f6-3333-4444-5555-666677778888", session.StorageModeFull)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// Find the assistant message with tool calls
	var foundErrorTool bool
	for _, msg := range sess.Messages {
		for _, tc := range msg.ToolCalls {
			if tc.Name == "Read" && tc.State == session.ToolStateError {
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

	_, err := p.Export("nonexistent-session-id", session.StorageModeFull)
	if err != session.ErrSessionNotFound {
		t.Errorf("Export() error = %v, want ErrSessionNotFound", err)
	}
}

func TestName(t *testing.T) {
	p := New("")
	if p.Name() != session.ProviderClaudeCode {
		t.Errorf("Name() = %q, want %q", p.Name(), session.ProviderClaudeCode)
	}
}

func TestCanImport(t *testing.T) {
	p := New("")
	if !p.CanImport() {
		t.Error("CanImport() = false, want true")
	}
}

func TestImport_basicSession(t *testing.T) {
	claudeHome := t.TempDir()
	p := New(claudeHome)

	sess := &session.Session{
		ID:          "test-import-001",
		Provider:    session.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feat/import-test",
		Summary:     "Test import session",
		ProjectPath: "/tmp/test/import-project",
		StorageMode: session.StorageModeFull,
		CreatedAt:   time.Date(2025, 2, 25, 10, 0, 0, 0, time.UTC),
		Messages: []session.Message{
			{
				ID:        "msg-001",
				Role:      session.RoleUser,
				Content:   "Write a hello world function",
				Timestamp: time.Date(2025, 2, 25, 10, 0, 0, 0, time.UTC),
			},
			{
				ID:        "msg-002",
				Role:      session.RoleAssistant,
				Content:   "I'll create a hello world function for you.",
				Model:     "claude-sonnet-4-20250514",
				Timestamp: time.Date(2025, 2, 25, 10, 0, 1, 0, time.UTC),
				Tokens:    150,
				ToolCalls: []session.ToolCall{
					{
						ID:    "tc-001",
						Name:  "Write",
						Input: `{"file_path":"main.go","content":"package main\n\nfunc hello() string {\n\treturn \"hello world\"\n}"}`,
						State: session.ToolStateCompleted,
					},
				},
			},
			{
				ID:        "msg-003",
				Role:      session.RoleUser,
				Content:   "Looks great!",
				Timestamp: time.Date(2025, 2, 25, 10, 0, 2, 0, time.UTC),
			},
		},
		TokenUsage: session.TokenUsage{
			InputTokens:  100,
			OutputTokens: 50,
			TotalTokens:  150,
		},
	}

	err := p.Import(sess)
	if err != nil {
		t.Fatalf("Import() error: %v", err)
	}

	// Verify JSONL file was created
	projDir := filepath.Join(claudeHome, projectsDir, encodeProjectPath("/tmp/test/import-project"))
	jsonlPath := filepath.Join(projDir, "test-import-001.jsonl")
	if _, err := os.Stat(jsonlPath); os.IsNotExist(err) {
		t.Fatalf("JSONL file not created at %s", jsonlPath)
	}

	// Verify sessions-index.json was created
	indexPath := filepath.Join(projDir, sessionsIndex)
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("Reading sessions index: %v", err)
	}

	var index sessionsIndexFile
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("Parsing sessions index: %v", err)
	}

	if len(index.Entries) != 1 {
		t.Fatalf("Index entries = %d, want 1", len(index.Entries))
	}
	entry := index.Entries[0]
	if entry.SessionID != "test-import-001" {
		t.Errorf("SessionID = %q, want %q", entry.SessionID, "test-import-001")
	}
	if entry.GitBranch != "feat/import-test" {
		t.Errorf("GitBranch = %q, want %q", entry.GitBranch, "feat/import-test")
	}
	if entry.Summary != "Test import session" {
		t.Errorf("Summary = %q, want %q", entry.Summary, "Test import session")
	}
	if entry.MessageCount != 3 {
		t.Errorf("MessageCount = %d, want 3", entry.MessageCount)
	}
	if entry.FirstPrompt != "Write a hello world function" {
		t.Errorf("FirstPrompt = %q, want %q", entry.FirstPrompt, "Write a hello world function")
	}

	// Verify the JSONL content is valid and re-parseable
	lines, err := readJSONLFile(jsonlPath)
	if err != nil {
		t.Fatalf("Reading JSONL: %v", err)
	}

	// summary + user + assistant + tool_result + user = 5 lines
	if len(lines) < 4 {
		t.Fatalf("JSONL lines = %d, want at least 4", len(lines))
	}

	// First line should be the summary
	if lines[0].Type != "summary" {
		t.Errorf("First line type = %q, want %q", lines[0].Type, "summary")
	}
	if lines[0].Summary != "Test import session" {
		t.Errorf("Summary = %q, want %q", lines[0].Summary, "Test import session")
	}
}

func TestImport_roundTrip(t *testing.T) {
	// Export a real session, import it, then verify it can be detected and exported again
	claudeHome := setupTestClaudeHome(t)
	p := New(claudeHome)

	// Export the simple session
	original, err := p.Export("a1b2c3d4-1111-2222-3333-444455556666", session.StorageModeFull)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// Import to a new project path
	importHome := t.TempDir()
	importProvider := New(importHome)

	original.ID = "roundtrip-test-001"
	original.ProjectPath = "/tmp/test/roundtrip"

	err = importProvider.Import(original)
	if err != nil {
		t.Fatalf("Import() error: %v", err)
	}

	// Detect the imported session
	summaries, err := importProvider.Detect("/tmp/test/roundtrip", original.Branch)
	if err != nil {
		t.Fatalf("Detect() error: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("Detect() returned %d summaries, want 1", len(summaries))
	}
	if summaries[0].ID != "roundtrip-test-001" {
		t.Errorf("ID = %q, want %q", summaries[0].ID, "roundtrip-test-001")
	}
	if summaries[0].Summary != original.Summary {
		t.Errorf("Summary = %q, want %q", summaries[0].Summary, original.Summary)
	}
}

func TestImport_nilSession(t *testing.T) {
	p := New(t.TempDir())
	err := p.Import(nil)
	if err == nil {
		t.Error("Import(nil) should return error")
	}
}

func TestImport_noProjectPath(t *testing.T) {
	p := New(t.TempDir())
	sess := &session.Session{ID: "test-123"}
	err := p.Import(sess)
	if err == nil {
		t.Error("Import() with empty ProjectPath should return error")
	}
}

func TestImport_updatesExistingIndex(t *testing.T) {
	claudeHome := t.TempDir()
	p := New(claudeHome)

	sess1 := &session.Session{
		ID:          "sess-001",
		Provider:    session.ProviderClaudeCode,
		ProjectPath: "/tmp/test/multi",
		Branch:      "feat/first",
		Summary:     "First session",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "Hello", Timestamp: time.Now()},
		},
	}
	sess2 := &session.Session{
		ID:          "sess-002",
		Provider:    session.ProviderClaudeCode,
		ProjectPath: "/tmp/test/multi",
		Branch:      "feat/second",
		Summary:     "Second session",
		Messages: []session.Message{
			{ID: "m2", Role: session.RoleUser, Content: "World", Timestamp: time.Now()},
		},
	}

	if err := p.Import(sess1); err != nil {
		t.Fatalf("Import(sess1) error: %v", err)
	}
	if err := p.Import(sess2); err != nil {
		t.Fatalf("Import(sess2) error: %v", err)
	}

	// Both should be in the index
	projDir := filepath.Join(claudeHome, projectsDir, encodeProjectPath("/tmp/test/multi"))
	indexData, err := os.ReadFile(filepath.Join(projDir, sessionsIndex))
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	var index sessionsIndexFile
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if len(index.Entries) != 2 {
		t.Fatalf("Index entries = %d, want 2", len(index.Entries))
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
