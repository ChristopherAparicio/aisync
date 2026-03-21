package ollama_test

import (
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/ingest/ollama"
)

func TestConvert_SimpleConversation(t *testing.T) {
	req := ollama.Request{
		Model:       "qwen3-coder:30b",
		ProjectPath: "/Users/guardix/dev/Jarvis",
		Agent:       "jarvis",
		Summary:     "Weather query",
		Conversation: []ollama.Message{
			{Role: "user", Content: "What's the weather?"},
			{Role: "assistant", Content: "It's 22°C and sunny in Paris."},
		},
		PromptEvalCount: 450,
		EvalCount:       35,
	}

	result, err := ollama.Convert(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Provider != "ollama" {
		t.Errorf("expected provider 'ollama', got %q", result.Provider)
	}
	if result.Agent != "jarvis" {
		t.Errorf("expected agent 'jarvis', got %q", result.Agent)
	}
	if result.ProjectPath != "/Users/guardix/dev/Jarvis" {
		t.Errorf("expected project_path, got %q", result.ProjectPath)
	}
	if result.Summary != "Weather query" {
		t.Errorf("expected summary 'Weather query', got %q", result.Summary)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result.Messages))
	}

	// User message.
	if result.Messages[0].Role != "user" {
		t.Errorf("msg[0]: expected role 'user', got %q", result.Messages[0].Role)
	}
	if result.Messages[0].Content != "What's the weather?" {
		t.Errorf("msg[0]: unexpected content %q", result.Messages[0].Content)
	}

	// Assistant message.
	if result.Messages[1].Role != "assistant" {
		t.Errorf("msg[1]: expected role 'assistant', got %q", result.Messages[1].Role)
	}
	if result.Messages[1].Model != "qwen3-coder:30b" {
		t.Errorf("msg[1]: expected model 'qwen3-coder:30b', got %q", result.Messages[1].Model)
	}
	if result.Messages[1].InputTokens != 450 {
		t.Errorf("msg[1]: expected input_tokens=450, got %d", result.Messages[1].InputTokens)
	}
	if result.Messages[1].OutputTokens != 35 {
		t.Errorf("msg[1]: expected output_tokens=35, got %d", result.Messages[1].OutputTokens)
	}
}

func TestConvert_ToolCallsWithResults(t *testing.T) {
	req := ollama.Request{
		Model: "qwen3-coder:30b",
		Conversation: []ollama.Message{
			{Role: "user", Content: "What's the weather?"},
			{Role: "assistant", Content: "", ToolCalls: []ollama.ToolCall{
				{Type: "function", Function: ollama.FunctionCall{
					Name:      "get_weather",
					Arguments: map[string]any{"city": "Paris"},
				}},
			}},
			{Role: "tool", ToolName: "get_weather", Content: "22°C, sunny"},
			{Role: "assistant", Content: "It's 22°C and sunny in Paris."},
		},
	}

	result, err := ollama.Convert(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 3 messages: user, assistant (with tool call), assistant (final).
	// The tool message is absorbed into the assistant's ToolCall output.
	if len(result.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result.Messages))
	}

	// Check the assistant message with tool calls.
	assistantMsg := result.Messages[1]
	if len(assistantMsg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(assistantMsg.ToolCalls))
	}

	tc := assistantMsg.ToolCalls[0]
	if tc.Name != "get_weather" {
		t.Errorf("expected tool name 'get_weather', got %q", tc.Name)
	}
	if tc.Input != `{"city":"Paris"}` {
		t.Errorf("expected tool input '{\"city\":\"Paris\"}', got %q", tc.Input)
	}
	if tc.Output != "22°C, sunny" {
		t.Errorf("expected tool output '22°C, sunny', got %q", tc.Output)
	}
}

func TestConvert_MultipleToolCalls(t *testing.T) {
	req := ollama.Request{
		Model: "qwen3-coder:30b",
		Conversation: []ollama.Message{
			{Role: "user", Content: "Compare weather in Paris and London"},
			{Role: "assistant", Content: "", ToolCalls: []ollama.ToolCall{
				{Function: ollama.FunctionCall{Name: "get_weather", Arguments: map[string]any{"city": "Paris"}}},
				{Function: ollama.FunctionCall{Name: "get_weather", Arguments: map[string]any{"city": "London"}}},
			}},
			{Role: "tool", ToolName: "get_weather", Content: "22°C, sunny"},
			{Role: "tool", ToolName: "get_weather", Content: "15°C, cloudy"},
			{Role: "assistant", Content: "Paris is warmer at 22°C while London is 15°C."},
		},
	}

	result, err := ollama.Convert(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result.Messages))
	}

	assistantMsg := result.Messages[1]
	if len(assistantMsg.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(assistantMsg.ToolCalls))
	}

	// First tool call gets first tool result (Paris).
	if assistantMsg.ToolCalls[0].Output != "22°C, sunny" {
		t.Errorf("tc[0]: expected '22°C, sunny', got %q", assistantMsg.ToolCalls[0].Output)
	}
	// Second tool call gets second tool result (London).
	if assistantMsg.ToolCalls[1].Output != "15°C, cloudy" {
		t.Errorf("tc[1]: expected '15°C, cloudy', got %q", assistantMsg.ToolCalls[1].Output)
	}
}

func TestConvert_SystemMessage(t *testing.T) {
	req := ollama.Request{
		Model: "qwen3-coder:30b",
		Conversation: []ollama.Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Hi"},
			{Role: "assistant", Content: "Hello!"},
		},
	}

	result, err := ollama.Convert(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result.Messages))
	}
	if result.Messages[0].Role != "system" {
		t.Errorf("expected system role, got %q", result.Messages[0].Role)
	}
	if result.Messages[0].Content != "You are a helpful assistant." {
		t.Errorf("unexpected system content: %q", result.Messages[0].Content)
	}
}

func TestConvert_EmptyConversation(t *testing.T) {
	req := ollama.Request{
		Model:        "qwen3-coder:30b",
		Conversation: nil,
	}

	_, err := ollama.Convert(req)
	if err == nil {
		t.Fatal("expected error for empty conversation, got nil")
	}
}

func TestConvert_TokensOnFirstAssistant(t *testing.T) {
	req := ollama.Request{
		Model: "qwen3-coder:30b",
		Conversation: []ollama.Message{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: "question"},
			{Role: "assistant", Content: "answer 1"},
			{Role: "user", Content: "follow up"},
			{Role: "assistant", Content: "answer 2"},
		},
		PromptEvalCount: 100,
		EvalCount:       50,
	}

	result, err := ollama.Convert(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Tokens should be on the first assistant message (index 2).
	if result.Messages[2].InputTokens != 100 {
		t.Errorf("expected first assistant input_tokens=100, got %d", result.Messages[2].InputTokens)
	}
	if result.Messages[2].OutputTokens != 50 {
		t.Errorf("expected first assistant output_tokens=50, got %d", result.Messages[2].OutputTokens)
	}

	// Second assistant should have zero tokens.
	if result.Messages[4].InputTokens != 0 {
		t.Errorf("expected second assistant input_tokens=0, got %d", result.Messages[4].InputTokens)
	}
}

func TestConvert_OrphanToolResult(t *testing.T) {
	// A tool result without a matching tool call should be preserved as a standalone message.
	req := ollama.Request{
		Model: "qwen3-coder:30b",
		Conversation: []ollama.Message{
			{Role: "user", Content: "hi"},
			{Role: "tool", ToolName: "unknown_tool", Content: "some output"},
			{Role: "assistant", Content: "ok"},
		},
	}

	result, err := ollama.Convert(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// user + orphan tool + assistant = 3 messages.
	if len(result.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result.Messages))
	}
	if result.Messages[1].Role != "tool" {
		t.Errorf("orphan tool message should preserve role, got %q", result.Messages[1].Role)
	}
}

func TestConvert_ToolCallNoArguments(t *testing.T) {
	req := ollama.Request{
		Model: "qwen3-coder:30b",
		Conversation: []ollama.Message{
			{Role: "user", Content: "list files"},
			{Role: "assistant", Content: "", ToolCalls: []ollama.ToolCall{
				{Function: ollama.FunctionCall{Name: "list_dir"}},
			}},
			{Role: "tool", ToolName: "list_dir", Content: "file1.go\nfile2.go"},
			{Role: "assistant", Content: "Found 2 files."},
		},
	}

	result, err := ollama.Convert(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tc := result.Messages[1].ToolCalls[0]
	if tc.Input != "" {
		t.Errorf("expected empty input for no-arg tool call, got %q", tc.Input)
	}
	if tc.Output != "file1.go\nfile2.go" {
		t.Errorf("expected output, got %q", tc.Output)
	}
}

func TestConvert_PreservesMetadata(t *testing.T) {
	req := ollama.Request{
		Model:       "llama3.1",
		ProjectPath: "/home/user/project",
		Agent:       "code-helper",
		Branch:      "feature/test",
		Summary:     "Test session",
		SessionID:   "custom-123",
		Conversation: []ollama.Message{
			{Role: "user", Content: "hello"},
		},
	}

	result, err := ollama.Convert(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ProjectPath != "/home/user/project" {
		t.Errorf("project_path: %q", result.ProjectPath)
	}
	if result.Branch != "feature/test" {
		t.Errorf("branch: %q", result.Branch)
	}
	if result.SessionID != "custom-123" {
		t.Errorf("session_id: %q", result.SessionID)
	}
}

func TestConvert_ToolResultWithoutToolName(t *testing.T) {
	// A tool result without tool_name should be skipped (no crash).
	req := ollama.Request{
		Model: "qwen3-coder:30b",
		Conversation: []ollama.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "", ToolCalls: []ollama.ToolCall{
				{Function: ollama.FunctionCall{Name: "read_file", Arguments: map[string]any{"path": "main.go"}}},
			}},
			{Role: "tool", Content: "package main"}, // no tool_name
			{Role: "assistant", Content: "The file starts with package main"},
		},
	}

	result, err := ollama.Convert(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Tool result without tool_name is skipped, so the tool call has no output.
	tc := result.Messages[1].ToolCalls[0]
	if tc.Output != "" {
		t.Errorf("expected empty output for unmatched tool, got %q", tc.Output)
	}
}

func TestDurationNsToMs(t *testing.T) {
	tests := []struct {
		ns   int64
		want int
	}{
		{0, 0},
		{1_000_000, 1},
		{1_200_000_000, 1200},
		{500_000, 0}, // < 1ms rounds down
	}
	for _, tt := range tests {
		got := ollama.DurationNsToMs(tt.ns)
		if got != tt.want {
			t.Errorf("DurationNsToMs(%d) = %d, want %d", tt.ns, got, tt.want)
		}
	}
}
