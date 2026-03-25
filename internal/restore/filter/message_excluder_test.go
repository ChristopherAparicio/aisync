package filter

import (
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestMessageExcluder_Name(t *testing.T) {
	f, _ := NewMessageExcluder(MessageExcluderConfig{})
	if f.Name() != "message-excluder" {
		t.Errorf("Name() = %q, want %q", f.Name(), "message-excluder")
	}
}

func TestMessageExcluder_noMatches(t *testing.T) {
	f, err := NewMessageExcluder(MessageExcluderConfig{Indices: []int{99}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sess := &session.Session{
		ID:       "test",
		Messages: []session.Message{{Content: "hello", Role: session.RoleUser}},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fr.Applied {
		t.Error("filter should not have applied")
	}
	if len(result.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(result.Messages))
	}
}

func TestMessageExcluder_byIndex(t *testing.T) {
	f, err := NewMessageExcluder(MessageExcluderConfig{Indices: []int{0, 2}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{Content: "msg-0", Role: session.RoleUser},
			{Content: "msg-1", Role: session.RoleAssistant},
			{Content: "msg-2", Role: session.RoleUser},
			{Content: "msg-3", Role: session.RoleAssistant},
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
	if result.Messages[0].Content != "msg-1" {
		t.Errorf("first remaining message should be msg-1, got %q", result.Messages[0].Content)
	}
	if result.Messages[1].Content != "msg-3" {
		t.Errorf("second remaining message should be msg-3, got %q", result.Messages[1].Content)
	}
}

func TestMessageExcluder_byRole(t *testing.T) {
	f, err := NewMessageExcluder(MessageExcluderConfig{Roles: []string{"system"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{Content: "system prompt", Role: session.RoleSystem},
			{Content: "hello", Role: session.RoleUser},
			{Content: "hi", Role: session.RoleAssistant},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fr.Applied {
		t.Error("filter should have applied")
	}
	if fr.MessagesRemoved != 1 {
		t.Errorf("MessagesRemoved = %d, want 1", fr.MessagesRemoved)
	}

	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result.Messages))
	}
	for _, msg := range result.Messages {
		if msg.Role == session.RoleSystem {
			t.Error("system messages should have been removed")
		}
	}
}

func TestMessageExcluder_byContentPattern(t *testing.T) {
	f, err := NewMessageExcluder(MessageExcluderConfig{ContentPattern: `(?i)error|fail`})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{Content: "hello world", Role: session.RoleUser},
			{Content: "ERROR: something went wrong", Role: session.RoleAssistant},
			{Content: "test passed", Role: session.RoleAssistant},
			{Content: "build failed", Role: session.RoleAssistant},
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
}

func TestMessageExcluder_combined(t *testing.T) {
	f, err := NewMessageExcluder(MessageExcluderConfig{
		Indices:        []int{0},
		Roles:          []string{"system"},
		ContentPattern: `debug`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{Content: "excluded by index", Role: session.RoleUser},  // 0: excluded by index
			{Content: "system prompt", Role: session.RoleSystem},    // 1: excluded by role
			{Content: "debug: trace info", Role: session.RoleUser},  // 2: excluded by pattern
			{Content: "hello world", Role: session.RoleUser},        // 3: kept
			{Content: "working on it", Role: session.RoleAssistant}, // 4: kept
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fr.MessagesRemoved != 3 {
		t.Errorf("MessagesRemoved = %d, want 3", fr.MessagesRemoved)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result.Messages))
	}
}

func TestMessageExcluder_invalidRole(t *testing.T) {
	_, err := NewMessageExcluder(MessageExcluderConfig{Roles: []string{"invalid"}})
	if err == nil {
		t.Error("expected error for invalid role")
	}
}

func TestMessageExcluder_invalidPattern(t *testing.T) {
	_, err := NewMessageExcluder(MessageExcluderConfig{ContentPattern: `[invalid`})
	if err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestMessageExcluder_summaryContainsDetails(t *testing.T) {
	f, err := NewMessageExcluder(MessageExcluderConfig{
		Indices:        []int{0},
		Roles:          []string{"system"},
		ContentPattern: `test`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{Content: "idx 0", Role: session.RoleUser},
			{Content: "sys", Role: session.RoleSystem},
			{Content: "test msg", Role: session.RoleUser},
			{Content: "kept", Role: session.RoleUser},
		},
	}

	_, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(fr.Summary, "by index") {
		t.Errorf("summary should mention index, got %q", fr.Summary)
	}
	if !strings.Contains(fr.Summary, "by role") {
		t.Errorf("summary should mention role, got %q", fr.Summary)
	}
	if !strings.Contains(fr.Summary, "by pattern") {
		t.Errorf("summary should mention pattern, got %q", fr.Summary)
	}
}

func TestMessageExcluder_doesNotModifyOriginal(t *testing.T) {
	f, _ := NewMessageExcluder(MessageExcluderConfig{Indices: []int{0}})

	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{Content: "msg-0"},
			{Content: "msg-1"},
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
