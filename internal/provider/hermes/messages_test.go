package hermes

import (
	"database/sql"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
}

func TestMapMessage_Basic(t *testing.T) {
	ts := float64(1700000000)

	user := mapMessage(hermesMessage{
		ID:        "msg-user-1",
		Role:      "user",
		Content:   nullString("hello world"),
		Timestamp: ts,
	})
	if user.Role != session.RoleUser {
		t.Errorf("user role = %q, want %q", user.Role, session.RoleUser)
	}
	if user.Content != "hello world" {
		t.Errorf("user content = %q, want %q", user.Content, "hello world")
	}
	if user.Timestamp != time.Unix(int64(ts), 0) {
		t.Errorf("user timestamp = %v, want %v", user.Timestamp, time.Unix(int64(ts), 0))
	}

	asst := mapMessage(hermesMessage{
		ID:         "msg-asst-1",
		Role:       "assistant",
		Content:    nullString("I can help"),
		Timestamp:  ts,
		TokenCount: 42,
	})
	if asst.Role != session.RoleAssistant {
		t.Errorf("assistant role = %q, want %q", asst.Role, session.RoleAssistant)
	}
	if asst.Content != "I can help" {
		t.Errorf("assistant content = %q, want %q", asst.Content, "I can help")
	}
	if asst.OutputTokens != 42 {
		t.Errorf("output tokens = %d, want 42", asst.OutputTokens)
	}
	if user.OutputTokens != 0 {
		t.Errorf("user output tokens = %d, want 0", user.OutputTokens)
	}
}

func TestMapMessage_SentinelDecoded(t *testing.T) {
	raw := "\x00\x01{\"text\":\"hello from sentinel\"}"
	msg := mapMessage(hermesMessage{
		ID:        "msg-sentinel-1",
		Role:      "user",
		Content:   nullString(raw),
		Timestamp: 1700000001.0,
	})

	want := "{\"text\":\"hello from sentinel\"}"
	if msg.Content != want {
		t.Errorf("content = %q, want %q (sentinel not stripped)", msg.Content, want)
	}
	if len(msg.Content) >= 2 && msg.Content[0] == '\x00' && msg.Content[1] == '\x01' {
		t.Error("sentinel prefix still present in mapped content")
	}
}

func TestMapMessage_ToolCall(t *testing.T) {
	toolCallsJSON := `[{"id":"tc1","name":"delegate_task","input":{"session_id":"child-001"}}]`
	msg := mapMessage(hermesMessage{
		ID:        "msg-tc-1",
		Role:      "assistant",
		ToolCalls: nullString(toolCallsJSON),
		Timestamp: 1700000002.0,
	})

	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool calls len = %d, want 1", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.Name != "delegate_task" {
		t.Errorf("tool call name = %q, want %q", tc.Name, "delegate_task")
	}
	if tc.ID != "tc1" {
		t.Errorf("tool call id = %q, want %q", tc.ID, "tc1")
	}
	if tc.State != session.ToolStateCompleted {
		t.Errorf("tool call state = %q, want %q", tc.State, session.ToolStateCompleted)
	}
	if tc.Input == "" {
		t.Error("tool call input is empty, want JSON object string")
	}
}

func TestMapMessage_Reasoning(t *testing.T) {
	msg := mapMessage(hermesMessage{
		ID:        "msg-reason-1",
		Role:      "assistant",
		Content:   nullString("final answer"),
		Reasoning: nullString("I should think carefully about this"),
		Timestamp: 1700000003.0,
	})

	if msg.Thinking != "I should think carefully about this" {
		t.Errorf("thinking = %q, want reasoning string", msg.Thinking)
	}

	msgFallback := mapMessage(hermesMessage{
		ID:               "msg-reason-2",
		Role:             "assistant",
		ReasoningContent: nullString("fallback reasoning"),
		Timestamp:        1700000004.0,
	})
	if msgFallback.Thinking != "fallback reasoning" {
		t.Errorf("thinking (fallback) = %q, want reasoning_content string", msgFallback.Thinking)
	}
}
