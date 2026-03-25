package filter

import (
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestEmptyMessage_Name(t *testing.T) {
	f := NewEmptyMessage()
	if f.Name() != "empty-message" {
		t.Errorf("Name() = %q, want %q", f.Name(), "empty-message")
	}
}

func TestEmptyMessage_noEmpty(t *testing.T) {
	f := NewEmptyMessage()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{Content: "hello", Role: session.RoleUser},
			{Content: "hi", Role: session.RoleAssistant},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fr.Applied {
		t.Error("filter should not have applied")
	}
	if len(result.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(result.Messages))
	}
}

func TestEmptyMessage_removesEmpty(t *testing.T) {
	f := NewEmptyMessage()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{Content: "hello", Role: session.RoleUser},
			{Content: "", Role: session.RoleAssistant},    // empty: no content, no tools
			{Content: "   ", Role: session.RoleAssistant}, // empty: whitespace only
			{Content: "world", Role: session.RoleUser},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fr.Applied {
		t.Error("filter should have applied")
	}
	if fr.MessagesRemoved != 2 {
		t.Errorf("MessagesRemoved = %d, want 2", fr.MessagesRemoved)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result.Messages))
	}
	if result.Messages[0].Content != "hello" {
		t.Errorf("first message should be 'hello', got %q", result.Messages[0].Content)
	}
	if result.Messages[1].Content != "world" {
		t.Errorf("second message should be 'world', got %q", result.Messages[1].Content)
	}
}

func TestEmptyMessage_keepsToolCalls(t *testing.T) {
	f := NewEmptyMessage()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{
				Content: "", // no text content
				Role:    session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{ID: "tc-1", Name: "read", State: session.ToolStateCompleted},
				},
			},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fr.Applied {
		t.Error("message with tool calls should NOT be removed")
	}
	if len(result.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(result.Messages))
	}
}

func TestEmptyMessage_keepsThinking(t *testing.T) {
	f := NewEmptyMessage()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{
				Content:  "",
				Thinking: "let me think about this...",
				Role:     session.RoleAssistant,
			},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fr.Applied {
		t.Error("message with thinking should NOT be removed")
	}
	if len(result.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(result.Messages))
	}
}

func TestEmptyMessage_keepsImages(t *testing.T) {
	f := NewEmptyMessage()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{
				Content: "",
				Role:    session.RoleUser,
				Images:  []session.ImageMeta{{MediaType: "image/png"}},
			},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fr.Applied {
		t.Error("message with images should NOT be removed")
	}
	if len(result.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(result.Messages))
	}
}

func TestEmptyMessage_keepsContentBlocks(t *testing.T) {
	f := NewEmptyMessage()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{
				Content: "",
				Role:    session.RoleAssistant,
				ContentBlocks: []session.ContentBlock{
					{Type: session.ContentBlockText, Text: "some text"},
				},
			},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fr.Applied {
		t.Error("message with content blocks should NOT be removed")
	}
	if len(result.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(result.Messages))
	}
}

func TestEmptyMessage_removesEmptyContentBlocks(t *testing.T) {
	f := NewEmptyMessage()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{
				Content: "",
				Role:    session.RoleAssistant,
				ContentBlocks: []session.ContentBlock{
					{Type: session.ContentBlockText, Text: ""}, // empty block
				},
			},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fr.Applied {
		t.Error("message with only empty content blocks should be removed")
	}
	if len(result.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(result.Messages))
	}
}

func TestEmptyMessage_doesNotModifyOriginal(t *testing.T) {
	f := NewEmptyMessage()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{Content: "hello"},
			{Content: ""},
		},
	}

	_, _, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sess.Messages) != 2 {
		t.Error("original session should not be modified")
	}
}
