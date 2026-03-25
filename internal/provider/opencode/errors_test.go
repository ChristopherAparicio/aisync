package opencode

import (
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestExtractErrors_APIError(t *testing.T) {
	sessionID := session.ID("test-session")
	now := time.Now()

	messages := []ocMessage{
		{
			ID:   "msg_1",
			Role: "assistant",
			Time: ocMsgTime{Created: now.UnixMilli()},
			Error: &ocAPIError{
				Name: "APIError",
				Data: ocAPIErrorData{
					Message:     "Internal server error",
					StatusCode:  500,
					IsRetryable: false,
					ResponseHeaders: map[string]string{
						"request-id":                                 "req_abc123",
						"x-envoy-upstream-service-time":              "18597",
						"anthropic-ratelimit-unified-7d-utilization": "0.74",
					},
					ResponseBody: `{"type":"api_error","message":"Internal server error"}`,
				},
			},
			ProviderID: "anthropic",
		},
	}

	domainMessages := []session.Message{
		{
			ID:        "msg_1",
			Role:      session.RoleAssistant,
			Timestamp: now,
		},
	}

	errors := ExtractErrors(sessionID, messages, domainMessages)

	if len(errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errors))
	}

	err := errors[0]
	if err.SessionID != sessionID {
		t.Errorf("SessionID = %v, want %v", err.SessionID, sessionID)
	}
	if err.Source != session.ErrorSourceProvider {
		t.Errorf("Source = %v, want %v", err.Source, session.ErrorSourceProvider)
	}
	if err.HTTPStatus != 500 {
		t.Errorf("HTTPStatus = %d, want 500", err.HTTPStatus)
	}
	if err.ProviderName != "anthropic" {
		t.Errorf("ProviderName = %q, want %q", err.ProviderName, "anthropic")
	}
	if err.RequestID != "req_abc123" {
		t.Errorf("RequestID = %q, want %q", err.RequestID, "req_abc123")
	}
	if err.DurationMs != 18597 {
		t.Errorf("DurationMs = %d, want 18597", err.DurationMs)
	}
	if err.MessageID != "msg_1" {
		t.Errorf("MessageID = %q, want %q", err.MessageID, "msg_1")
	}
	if err.MessageIndex != 0 {
		t.Errorf("MessageIndex = %d, want 0", err.MessageIndex)
	}
	if err.ID == "" {
		t.Error("ID should not be empty")
	}
	if err.Headers["anthropic-ratelimit-unified-7d-utilization"] != "0.74" {
		t.Errorf("Headers missing rate limit utilization")
	}
}

func TestExtractErrors_ToolError(t *testing.T) {
	sessionID := session.ID("test-session")
	now := time.Now()

	messages := []ocMessage{
		{
			ID:   "msg_1",
			Role: "assistant",
			Time: ocMsgTime{Created: now.UnixMilli()},
		},
	}

	domainMessages := []session.Message{
		{
			ID:        "msg_1",
			Role:      session.RoleAssistant,
			Timestamp: now,
			ToolCalls: []session.ToolCall{
				{
					ID:         "tc_1",
					Name:       "bash",
					State:      session.ToolStateError,
					Output:     "exit code 1: command not found: foobar",
					DurationMs: 150,
				},
				{
					ID:     "tc_2",
					Name:   "Read",
					State:  session.ToolStateCompleted,
					Output: "file content here",
				},
			},
		},
	}

	errors := ExtractErrors(sessionID, messages, domainMessages)

	if len(errors) != 1 {
		t.Fatalf("expected 1 error (only the failed tool call), got %d", len(errors))
	}

	err := errors[0]
	if err.Source != session.ErrorSourceTool {
		t.Errorf("Source = %v, want %v", err.Source, session.ErrorSourceTool)
	}
	if err.ToolName != "bash" {
		t.Errorf("ToolName = %q, want %q", err.ToolName, "bash")
	}
	if err.ToolCallID != "tc_1" {
		t.Errorf("ToolCallID = %q, want %q", err.ToolCallID, "tc_1")
	}
	if err.DurationMs != 150 {
		t.Errorf("DurationMs = %d, want 150", err.DurationMs)
	}
	if err.RawError != "exit code 1: command not found: foobar" {
		t.Errorf("RawError = %q, unexpected value", err.RawError)
	}
}

func TestExtractErrors_BothAPIAndToolErrors(t *testing.T) {
	sessionID := session.ID("test-session")
	now := time.Now()

	messages := []ocMessage{
		{
			ID:   "msg_1",
			Role: "assistant",
			Time: ocMsgTime{Created: now.UnixMilli()},
		},
		{
			ID:   "msg_2",
			Role: "assistant",
			Time: ocMsgTime{Created: now.Add(time.Minute).UnixMilli()},
			Error: &ocAPIError{
				Name: "APIError",
				Data: ocAPIErrorData{
					Message:    "Internal server error",
					StatusCode: 500,
				},
			},
		},
	}

	domainMessages := []session.Message{
		{
			ID:        "msg_1",
			Role:      session.RoleAssistant,
			Timestamp: now,
			ToolCalls: []session.ToolCall{
				{
					ID:     "tc_1",
					Name:   "bash",
					State:  session.ToolStateError,
					Output: "Permission denied",
				},
			},
		},
		{
			ID:        "msg_2",
			Role:      session.RoleAssistant,
			Timestamp: now.Add(time.Minute),
		},
	}

	errors := ExtractErrors(sessionID, messages, domainMessages)

	if len(errors) != 2 {
		t.Fatalf("expected 2 errors (1 tool + 1 API), got %d", len(errors))
	}

	// First error should be the tool error (from msg_1).
	if errors[0].Source != session.ErrorSourceTool {
		t.Errorf("errors[0].Source = %v, want %v", errors[0].Source, session.ErrorSourceTool)
	}
	if errors[0].MessageIndex != 0 {
		t.Errorf("errors[0].MessageIndex = %d, want 0", errors[0].MessageIndex)
	}

	// Second error should be the API error (from msg_2).
	if errors[1].Source != session.ErrorSourceProvider {
		t.Errorf("errors[1].Source = %v, want %v", errors[1].Source, session.ErrorSourceProvider)
	}
	if errors[1].MessageIndex != 1 {
		t.Errorf("errors[1].MessageIndex = %d, want 1", errors[1].MessageIndex)
	}
}

func TestExtractErrors_NoErrors(t *testing.T) {
	messages := []ocMessage{
		{ID: "msg_1", Role: "user"},
		{ID: "msg_2", Role: "assistant"},
	}
	domainMessages := []session.Message{
		{ID: "msg_1", Role: session.RoleUser},
		{ID: "msg_2", Role: session.RoleAssistant},
	}

	errors := ExtractErrors("test", messages, domainMessages)
	if len(errors) != 0 {
		t.Errorf("expected 0 errors, got %d", len(errors))
	}
}

func TestExtractErrors_LongToolOutput_Truncated(t *testing.T) {
	sessionID := session.ID("test-session")
	longOutput := make([]byte, 5000)
	for i := range longOutput {
		longOutput[i] = 'x'
	}

	messages := []ocMessage{{ID: "msg_1", Role: "assistant"}}
	domainMessages := []session.Message{{
		ID:   "msg_1",
		Role: session.RoleAssistant,
		ToolCalls: []session.ToolCall{{
			ID:     "tc_1",
			Name:   "bash",
			State:  session.ToolStateError,
			Output: string(longOutput),
		}},
	}}

	errors := ExtractErrors(sessionID, messages, domainMessages)
	if len(errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errors))
	}
	if len(errors[0].RawError) > 2010 {
		t.Errorf("RawError should be truncated, got len=%d", len(errors[0].RawError))
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc..."},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}
