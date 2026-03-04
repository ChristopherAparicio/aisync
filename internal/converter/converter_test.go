package converter

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func testSession() *session.Session {
	return &session.Session{
		ID:          "test-session-001",
		Provider:    session.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feat/hello",
		ProjectPath: "/tmp/test/myproject",
		StorageMode: session.StorageModeFull,
		Summary:     "Implemented hello world",
		Version:     1,
		ExportedBy:  "aisync",
		ExportedAt:  time.Date(2026, 2, 17, 10, 0, 0, 0, time.UTC),
		CreatedAt:   time.Date(2026, 2, 17, 9, 0, 0, 0, time.UTC),
		Messages: []session.Message{
			{
				ID:        "msg-001",
				Role:      session.RoleUser,
				Content:   "Add a hello world function",
				Timestamp: time.Date(2026, 2, 17, 9, 0, 0, 0, time.UTC),
			},
			{
				ID:           "msg-002",
				Role:         session.RoleAssistant,
				Content:      "I'll create the function for you.",
				Model:        "claude-sonnet-4-5-20250929",
				Thinking:     "Need to write a simple Go function",
				Timestamp:    time.Date(2026, 2, 17, 9, 0, 5, 0, time.UTC),
				OutputTokens: 150,
				ToolCalls: []session.ToolCall{
					{
						ID:     "tool-001",
						Name:   "Write",
						Input:  `{"file_path":"main.go","content":"package main"}`,
						State:  session.ToolStateCompleted,
						Output: "File written successfully.",
					},
				},
			},
			{
				ID:        "msg-003",
				Role:      session.RoleUser,
				Content:   "Looks great, thanks!",
				Timestamp: time.Date(2026, 2, 17, 9, 0, 20, 0, time.UTC),
			},
		},
		FileChanges: []session.FileChange{
			{FilePath: "main.go", ChangeType: session.ChangeCreated},
		},
		TokenUsage: session.TokenUsage{
			InputTokens:  100,
			OutputTokens: 50,
			TotalTokens:  150,
		},
	}
}

func TestConverter_SupportedFormats(t *testing.T) {
	c := New()
	formats := c.SupportedFormats()
	if len(formats) != 2 {
		t.Fatalf("expected 2 formats, got %d", len(formats))
	}
	found := map[session.ProviderName]bool{}
	for _, f := range formats {
		found[f] = true
	}
	if !found[session.ProviderClaudeCode] {
		t.Error("expected claude-code in supported formats")
	}
	if !found[session.ProviderOpenCode] {
		t.Error("expected opencode in supported formats")
	}
}

func TestConverter_ToNative_UnsupportedFormat(t *testing.T) {
	c := New()
	_, err := c.ToNative(testSession(), session.ProviderCursor)
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

func TestConverter_FromNative_UnsupportedFormat(t *testing.T) {
	c := New()
	_, err := c.FromNative([]byte("{}"), session.ProviderCursor)
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

func TestConverter_RoundTrip_Claude(t *testing.T) {
	c := New()
	sess := testSession()

	// Convert to Claude JSONL
	data, err := c.ToNative(sess, session.ProviderClaudeCode)
	if err != nil {
		t.Fatalf("ToNative(claude) error: %v", err)
	}

	// Verify it's JSONL (multiple lines)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 JSONL lines, got %d", len(lines))
	}

	// Each line should be valid JSON
	for i, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Errorf("line %d is not valid JSON: %s", i, line)
		}
	}

	// Parse back
	restored, err := c.FromNative(data, session.ProviderClaudeCode)
	if err != nil {
		t.Fatalf("FromNative(claude) error: %v", err)
	}

	// Verify key fields
	if restored.Summary != sess.Summary {
		t.Errorf("Summary = %q, want %q", restored.Summary, sess.Summary)
	}
	if restored.Branch != sess.Branch {
		t.Errorf("Branch = %q, want %q", restored.Branch, sess.Branch)
	}
	if restored.ProjectPath != sess.ProjectPath {
		t.Errorf("ProjectPath = %q, want %q", restored.ProjectPath, sess.ProjectPath)
	}
	if restored.Provider != session.ProviderClaudeCode {
		t.Errorf("Provider = %q, want claude-code", restored.Provider)
	}

	// Should have user messages (tool_result-only messages are merged)
	if len(restored.Messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(restored.Messages))
	}

	// First message should be user
	if restored.Messages[0].Role != session.RoleUser {
		t.Errorf("first message role = %q, want user", restored.Messages[0].Role)
	}
	if restored.Messages[0].Content != "Add a hello world function" {
		t.Errorf("first message content = %q, want 'Add a hello world function'", restored.Messages[0].Content)
	}
}

func TestConverter_RoundTrip_OpenCode(t *testing.T) {
	c := New()
	sess := testSession()

	// Convert to OpenCode JSON
	data, err := c.ToNative(sess, session.ProviderOpenCode)
	if err != nil {
		t.Fatalf("ToNative(opencode) error: %v", err)
	}

	// Verify it's valid JSON
	if !json.Valid(data) {
		t.Fatal("OpenCode output is not valid JSON")
	}

	// Parse back
	restored, err := c.FromNative(data, session.ProviderOpenCode)
	if err != nil {
		t.Fatalf("FromNative(opencode) error: %v", err)
	}

	// Verify key fields
	if restored.Summary != sess.Summary {
		t.Errorf("Summary = %q, want %q", restored.Summary, sess.Summary)
	}
	if restored.ProjectPath != sess.ProjectPath {
		t.Errorf("ProjectPath = %q, want %q", restored.ProjectPath, sess.ProjectPath)
	}
	if restored.Provider != session.ProviderOpenCode {
		t.Errorf("Provider = %q, want opencode", restored.Provider)
	}
	if len(restored.Messages) != len(sess.Messages) {
		t.Fatalf("expected %d messages, got %d", len(sess.Messages), len(restored.Messages))
	}

	// Verify tool calls preserved
	var foundToolCall bool
	for _, msg := range restored.Messages {
		for _, tc := range msg.ToolCalls {
			if tc.Name == "Write" {
				foundToolCall = true
				if tc.State != session.ToolStateCompleted {
					t.Errorf("tool state = %q, want completed", tc.State)
				}
			}
		}
	}
	if !foundToolCall {
		t.Error("expected to find Write tool call in restored session")
	}
}

func TestConverter_ToContextMD(t *testing.T) {
	sess := testSession()
	md := ToContextMD(sess)
	content := string(md)

	checks := []string{
		"# AI Session Context",
		"**Provider:** claude-code",
		"**Branch:** feat/hello",
		"**Summary:** Implemented hello world",
		"## Files Changed",
		"`main.go` (created)",
		"## Conversation",
		"### User",
		"Add a hello world function",
		"### Assistant",
		"I'll create the function for you.",
		"**Tool: Write**",
	}

	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("CONTEXT.md missing %q", check)
		}
	}
}

func TestConverter_ToContextMD_WithChildren(t *testing.T) {
	sess := testSession()
	sess.Children = []session.Session{
		{
			ID:    "child-001",
			Agent: "task",
			Messages: []session.Message{
				{Role: session.RoleUser, Content: "Subtask: run tests"},
				{Role: session.RoleAssistant, Content: "Tests passed!"},
			},
		},
	}

	md := ToContextMD(sess)
	content := string(md)

	if !strings.Contains(content, "Sub-agent: task") {
		t.Error("CONTEXT.md should contain sub-agent section")
	}
	if !strings.Contains(content, "Subtask: run tests") {
		t.Error("CONTEXT.md should contain child messages")
	}
}

func TestDetectFormat_ClaudeJSONL(t *testing.T) {
	data := `{"type":"summary","summary":"test"}
{"type":"user","message":{"role":"user","content":"hello"}}`
	format := DetectFormat([]byte(data))
	if format != session.ProviderClaudeCode {
		t.Errorf("DetectFormat = %q, want claude-code", format)
	}
}

func TestDetectFormat_OpenCodeJSON(t *testing.T) {
	data := `{"projectID":"abc","directory":"/tmp/test"}`
	format := DetectFormat([]byte(data))
	if format != session.ProviderOpenCode {
		t.Errorf("DetectFormat = %q, want opencode", format)
	}
}

func TestDetectFormat_UnifiedJSON(t *testing.T) {
	data := `{"provider":"claude-code","messages":[{"role":"user","content":"hello"}]}`
	format := DetectFormat([]byte(data))
	if format != "" {
		t.Errorf("DetectFormat = %q, want empty (unified)", format)
	}
}

func TestConverter_FromClaude_PreservesToolCalls(t *testing.T) {
	jsonl := `{"type":"summary","summary":"Test session"}
{"type":"user","uuid":"u1","timestamp":"2026-02-17T09:00:00Z","sessionId":"sess1","gitBranch":"main","cwd":"/tmp","message":{"role":"user","content":"Do something"},"isSidechain":false}
{"type":"assistant","uuid":"a1","timestamp":"2026-02-17T09:00:05Z","sessionId":"sess1","message":{"role":"assistant","model":"claude-sonnet","id":"msg1","type":"message","content":[{"type":"text","text":"I will write a file"},{"type":"tool_use","id":"tool1","name":"Write","input":{"file_path":"test.go","content":"package main"}}],"usage":{"input_tokens":50,"output_tokens":30}},"isSidechain":false}
{"type":"user","uuid":"u2","timestamp":"2026-02-17T09:00:06Z","sessionId":"sess1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool1","content":"File written."}]},"isSidechain":false}`

	c := New()
	sess, err := c.FromNative([]byte(jsonl), session.ProviderClaudeCode)
	if err != nil {
		t.Fatalf("FromNative error: %v", err)
	}

	if sess.Summary != "Test session" {
		t.Errorf("Summary = %q, want 'Test session'", sess.Summary)
	}

	// The assistant message should have a tool call with output merged
	var foundTool bool
	for _, msg := range sess.Messages {
		for _, tc := range msg.ToolCalls {
			if tc.Name == "Write" {
				foundTool = true
				if tc.Output != "File written." {
					t.Errorf("tool output = %q, want 'File written.'", tc.Output)
				}
				if tc.State != session.ToolStateCompleted {
					t.Errorf("tool state = %q, want completed", tc.State)
				}
			}
		}
	}
	if !foundTool {
		t.Error("expected Write tool call in parsed session")
	}
}
