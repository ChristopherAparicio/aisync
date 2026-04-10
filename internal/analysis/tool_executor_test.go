package analysis

import (
	"encoding/json"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// testSessionForTools returns a session with varied message types, tool calls,
// and token data suitable for testing all tool executor methods.
func testSessionForTools() *session.Session {
	return &session.Session{
		ID:       "test-sess-tools",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Messages: []session.Message{
			{
				ID:          "msg-0",
				Role:        session.RoleUser,
				Content:     "Please implement authentication for the API",
				InputTokens: 100,
			},
			{
				ID:           "msg-1",
				Role:         session.RoleAssistant,
				Content:      "I'll implement JWT authentication. Let me start by reading the existing code.",
				InputTokens:  5000,
				OutputTokens: 200,
				Model:        "claude-sonnet-4-20250514",
				ToolCalls: []session.ToolCall{
					{ID: "tc-1", Name: "Read", Input: "/src/auth.go", Output: "package auth\n...", State: session.ToolStateCompleted, DurationMs: 50},
					{ID: "tc-2", Name: "Read", Input: "/src/config.go", Output: "package config\n...", State: session.ToolStateCompleted, DurationMs: 30},
				},
			},
			{
				ID:           "msg-2",
				Role:         session.RoleAssistant,
				Content:      "Now I'll write the auth middleware.",
				InputTokens:  8000,
				OutputTokens: 500,
				Model:        "claude-sonnet-4-20250514",
				ToolCalls: []session.ToolCall{
					{ID: "tc-3", Name: "Write", Input: "/src/middleware.go", Output: "", State: session.ToolStateCompleted, DurationMs: 100},
					{ID: "tc-4", Name: "Bash", Input: "go test ./...", Output: "FAIL: TestAuth", State: session.ToolStateError, DurationMs: 2000},
				},
			},
			{
				ID:      "msg-3",
				Role:    session.RoleUser,
				Content: "The test failed, please fix the import path",
			},
			{
				ID:           "msg-4",
				Role:         session.RoleAssistant,
				Content:      "I see the issue. Let me fix the import.",
				InputTokens:  10000,
				OutputTokens: 300,
				Model:        "claude-sonnet-4-20250514",
				ToolCalls: []session.ToolCall{
					{ID: "tc-5", Name: "Edit", Input: "/src/middleware.go", Output: "", State: session.ToolStateCompleted, DurationMs: 80},
					{ID: "tc-6", Name: "Bash", Input: "go test ./...", Output: "PASS", State: session.ToolStateCompleted, DurationMs: 1500},
				},
			},
		},
	}
}

func TestSessionToolExecutor_GetMessages_FullRange(t *testing.T) {
	exec := NewSessionToolExecutor(testSessionForTools())
	result, err := exec.GetMessages(0, 4)
	if err != nil {
		t.Fatalf("GetMessages() error = %v", err)
	}

	var msgs []toolMessageResult
	if err := json.Unmarshal(result, &msgs); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(msgs) != 5 {
		t.Fatalf("got %d messages, want 5", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("msgs[0].Role = %q, want %q", msgs[0].Role, "user")
	}
	if msgs[1].ToolCalls != 2 {
		t.Errorf("msgs[1].ToolCalls = %d, want 2", msgs[1].ToolCalls)
	}
}

func TestSessionToolExecutor_GetMessages_Clamped(t *testing.T) {
	exec := NewSessionToolExecutor(testSessionForTools())
	result, err := exec.GetMessages(-5, 100)
	if err != nil {
		t.Fatalf("GetMessages() error = %v", err)
	}

	var msgs []toolMessageResult
	if err := json.Unmarshal(result, &msgs); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(msgs) != 5 {
		t.Errorf("got %d messages (should be clamped to full range), want 5", len(msgs))
	}
}

func TestSessionToolExecutor_GetMessages_EmptyRange(t *testing.T) {
	exec := NewSessionToolExecutor(testSessionForTools())
	result, err := exec.GetMessages(3, 1) // from > to
	if err != nil {
		t.Fatalf("GetMessages() error = %v", err)
	}

	var msgs []toolMessageResult
	if err := json.Unmarshal(result, &msgs); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(msgs) != 0 {
		t.Errorf("got %d messages, want 0 for invalid range", len(msgs))
	}
}

func TestSessionToolExecutor_GetMessages_EmptySession(t *testing.T) {
	exec := NewSessionToolExecutor(&session.Session{})
	result, err := exec.GetMessages(0, 10)
	if err != nil {
		t.Fatalf("GetMessages() error = %v", err)
	}

	var msgs []toolMessageResult
	if err := json.Unmarshal(result, &msgs); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(msgs) != 0 {
		t.Errorf("got %d messages, want 0 for empty session", len(msgs))
	}
}

func TestSessionToolExecutor_GetToolCalls_All(t *testing.T) {
	exec := NewSessionToolExecutor(testSessionForTools())
	result, err := exec.GetToolCalls(ToolCallFilter{})
	if err != nil {
		t.Fatalf("GetToolCalls() error = %v", err)
	}

	var calls []toolCallResult
	if err := json.Unmarshal(result, &calls); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(calls) != 6 {
		t.Fatalf("got %d tool calls, want 6", len(calls))
	}
}

func TestSessionToolExecutor_GetToolCalls_FilterByName(t *testing.T) {
	exec := NewSessionToolExecutor(testSessionForTools())
	result, err := exec.GetToolCalls(ToolCallFilter{Name: "read"}) // case-insensitive
	if err != nil {
		t.Fatalf("GetToolCalls() error = %v", err)
	}

	var calls []toolCallResult
	if err := json.Unmarshal(result, &calls); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(calls) != 2 {
		t.Errorf("got %d Read calls, want 2", len(calls))
	}
	for _, c := range calls {
		if c.Name != "Read" {
			t.Errorf("unexpected tool name %q", c.Name)
		}
	}
}

func TestSessionToolExecutor_GetToolCalls_FilterByState(t *testing.T) {
	exec := NewSessionToolExecutor(testSessionForTools())
	result, err := exec.GetToolCalls(ToolCallFilter{State: "error"})
	if err != nil {
		t.Fatalf("GetToolCalls() error = %v", err)
	}

	var calls []toolCallResult
	if err := json.Unmarshal(result, &calls); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("got %d error calls, want 1", len(calls))
	}
	if calls[0].Name != "Bash" {
		t.Errorf("error call name = %q, want %q", calls[0].Name, "Bash")
	}
}

func TestSessionToolExecutor_GetToolCalls_Limit(t *testing.T) {
	exec := NewSessionToolExecutor(testSessionForTools())
	result, err := exec.GetToolCalls(ToolCallFilter{Limit: 2})
	if err != nil {
		t.Fatalf("GetToolCalls() error = %v", err)
	}

	var calls []toolCallResult
	if err := json.Unmarshal(result, &calls); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(calls) != 2 {
		t.Errorf("got %d calls, want 2 (limit)", len(calls))
	}
}

func TestSessionToolExecutor_SearchMessages(t *testing.T) {
	exec := NewSessionToolExecutor(testSessionForTools())
	result, err := exec.SearchMessages("implement auth", 10)
	if err != nil {
		t.Fatalf("SearchMessages() error = %v", err)
	}

	var results []toolSearchResult
	if err := json.Unmarshal(result, &results); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("got %d search results, want 1", len(results))
	}
	if results[0].Index != 0 {
		t.Errorf("matched message index = %d, want 0", results[0].Index)
	}
}

func TestSessionToolExecutor_SearchMessages_CaseInsensitive(t *testing.T) {
	exec := NewSessionToolExecutor(testSessionForTools())
	result, err := exec.SearchMessages("JWT", 10)
	if err != nil {
		t.Fatalf("SearchMessages() error = %v", err)
	}

	var results []toolSearchResult
	if err := json.Unmarshal(result, &results); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("got %d search results, want 1", len(results))
	}
}

func TestSessionToolExecutor_SearchMessages_NoMatch(t *testing.T) {
	exec := NewSessionToolExecutor(testSessionForTools())
	result, err := exec.SearchMessages("kubernetes", 10)
	if err != nil {
		t.Fatalf("SearchMessages() error = %v", err)
	}

	var results []toolSearchResult
	if err := json.Unmarshal(result, &results); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("got %d search results, want 0", len(results))
	}
}

func TestSessionToolExecutor_SearchMessages_EmptyPattern(t *testing.T) {
	exec := NewSessionToolExecutor(testSessionForTools())
	result, err := exec.SearchMessages("", 10)
	if err != nil {
		t.Fatalf("SearchMessages() error = %v", err)
	}

	var results []toolSearchResult
	if err := json.Unmarshal(result, &results); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("got %d search results, want 0 for empty pattern", len(results))
	}
}

func TestSessionToolExecutor_GetCompactionDetails(t *testing.T) {
	exec := NewSessionToolExecutor(testSessionForTools())
	result, err := exec.GetCompactionDetails()
	if err != nil {
		t.Fatalf("GetCompactionDetails() error = %v", err)
	}

	var cr toolCompactionResult
	if err := json.Unmarshal(result, &cr); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	// With the small test session, there should be no compaction events.
	if cr.TotalCompactions != 0 {
		t.Errorf("TotalCompactions = %d, want 0 for small test session", cr.TotalCompactions)
	}
}

func TestSessionToolExecutor_GetErrorDetails(t *testing.T) {
	exec := NewSessionToolExecutor(testSessionForTools())
	result, err := exec.GetErrorDetails(0)
	if err != nil {
		t.Fatalf("GetErrorDetails() error = %v", err)
	}

	var errors []toolErrorDetail
	if err := json.Unmarshal(result, &errors); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(errors) != 1 {
		t.Fatalf("got %d errors, want 1", len(errors))
	}
	if errors[0].ToolName != "Bash" {
		t.Errorf("error tool = %q, want %q", errors[0].ToolName, "Bash")
	}
	if errors[0].ErrorOutput != "FAIL: TestAuth" {
		t.Errorf("error output = %q, want %q", errors[0].ErrorOutput, "FAIL: TestAuth")
	}
}

func TestSessionToolExecutor_GetErrorDetails_NoErrors(t *testing.T) {
	sess := &session.Session{
		Messages: []session.Message{
			{
				Role: session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{Name: "Read", State: session.ToolStateCompleted},
				},
			},
		},
	}
	exec := NewSessionToolExecutor(sess)
	result, err := exec.GetErrorDetails(0)
	if err != nil {
		t.Fatalf("GetErrorDetails() error = %v", err)
	}

	var errors []toolErrorDetail
	if err := json.Unmarshal(result, &errors); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(errors) != 0 {
		t.Errorf("got %d errors, want 0", len(errors))
	}
}

func TestSessionToolExecutor_GetTokenTimeline(t *testing.T) {
	exec := NewSessionToolExecutor(testSessionForTools())
	result, err := exec.GetTokenTimeline()
	if err != nil {
		t.Fatalf("GetTokenTimeline() error = %v", err)
	}

	var timeline []toolTokenEntry
	if err := json.Unmarshal(result, &timeline); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	// Messages with token data: msg-0 (input=100), msg-1 (5000/200), msg-2 (8000/500), msg-4 (10000/300).
	// msg-3 has no tokens.
	if len(timeline) != 4 {
		t.Fatalf("got %d timeline entries, want 4", len(timeline))
	}

	// Verify ordering.
	if timeline[0].InputTokens != 100 {
		t.Errorf("first entry input_tokens = %d, want 100", timeline[0].InputTokens)
	}
	if timeline[3].InputTokens != 10000 {
		t.Errorf("last entry input_tokens = %d, want 10000", timeline[3].InputTokens)
	}
}

func TestSessionToolExecutor_GetMessages_Truncation(t *testing.T) {
	// Create a session with a very long message.
	longContent := make([]byte, 5000)
	for i := range longContent {
		longContent[i] = 'x'
	}
	sess := &session.Session{
		Messages: []session.Message{
			{Role: session.RoleUser, Content: string(longContent)},
		},
	}

	exec := NewSessionToolExecutor(sess)
	result, err := exec.GetMessages(0, 0)
	if err != nil {
		t.Fatalf("GetMessages() error = %v", err)
	}

	var msgs []toolMessageResult
	if err := json.Unmarshal(result, &msgs); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}

	// Should be truncated to maxToolContentLen + truncation suffix.
	if len(msgs[0].Content) <= maxToolContentLen {
		t.Error("expected truncated content to include suffix")
	}
	if len(msgs[0].Content) > maxToolContentLen+100 {
		t.Errorf("truncated content too long: %d chars", len(msgs[0].Content))
	}
}

// ── Tool definitions tests ──

func TestAnalystTools_SingleTool(t *testing.T) {
	tools := AnalystTools()
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1 (single polymorphic tool)", len(tools))
	}
	if tools[0].Name != AnalystToolName {
		t.Errorf("tool name = %q, want %q", tools[0].Name, AnalystToolName)
	}
}

func TestAnalystTools_ValidJSON(t *testing.T) {
	tools := AnalystTools()
	tool := tools[0]

	if tool.Description == "" {
		t.Error("tool has empty description")
	}

	// Verify InputSchema is valid JSON with action enum.
	var schema map[string]interface{}
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatalf("invalid InputSchema JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("schema type = %v, want %q", schema["type"], "object")
	}

	// Check action is a required field.
	required, ok := schema["required"].([]interface{})
	if !ok || len(required) == 0 {
		t.Fatal("schema missing required array")
	}
	if required[0] != "action" {
		t.Errorf("first required field = %v, want %q", required[0], "action")
	}

	// Check action enum contains all 6 actions.
	props := schema["properties"].(map[string]interface{})
	actionProp := props["action"].(map[string]interface{})
	actionEnum := actionProp["enum"].([]interface{})
	expectedActions := []string{
		"get_messages", "get_tool_calls", "search_messages",
		"get_compaction_details", "get_error_details", "get_token_timeline",
	}
	if len(actionEnum) != len(expectedActions) {
		t.Fatalf("action enum has %d values, want %d", len(actionEnum), len(expectedActions))
	}
	for i, expected := range expectedActions {
		if actionEnum[i] != expected {
			t.Errorf("action enum[%d] = %v, want %q", i, actionEnum[i], expected)
		}
	}
}
