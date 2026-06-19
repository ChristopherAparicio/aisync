package session

import (
	"testing"
	"time"
)

func TestApplyWindowRetentionKeepsUsersCompactionsAndTail(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	sess := &Session{
		ID:          "retention-1",
		StorageMode: StorageModeFull,
		TokenUsage:  TokenUsage{TotalTokens: 1000},
		Messages: []Message{
			{ID: "old-user", Role: RoleUser, Content: "original ask"},
			{ID: "old-assistant", Role: RoleAssistant, Content: "old answer", InputTokens: 200, Thinking: "drop me"},
			{ID: "compact", Role: RoleAssistant, Content: "summary", IsCompactionSummary: true, Thinking: "drop me too"},
			{ID: "middle-assistant", Role: RoleAssistant, Content: "middle", InputTokens: 50},
			{ID: "tail-assistant", Role: RoleAssistant, Content: "recent", InputTokens: 120, Thinking: "recent thinking", ToolCalls: []ToolCall{{ID: "tool-1", Name: "bash", Output: "keep recent output", OutputTokens: 10}}},
			{ID: "tail-user", Role: RoleUser, Content: "latest ask"},
		},
	}

	stats := ApplyWindowRetention(sess, WindowRetentionPolicy{
		MaxTokens:        150,
		KeepUserMessages: true,
		KeepCompactions:  true,
		KeepToolOutputs:  false,
		KeepThinking:     false,
	}, now)

	if stats.OriginalMessages != 6 || stats.RetainedMessages != 4 {
		t.Fatalf("stats = %+v, want 6 original and 4 retained", stats)
	}
	if sess.RetentionTier != RetentionTierWarm {
		t.Fatalf("RetentionTier = %q, want warm", sess.RetentionTier)
	}
	if sess.RetentionFidelity != RetentionFidelityWindowed {
		t.Fatalf("RetentionFidelity = %q, want windowed", sess.RetentionFidelity)
	}
	gotIDs := []string{sess.Messages[0].ID, sess.Messages[1].ID, sess.Messages[2].ID, sess.Messages[3].ID}
	wantIDs := []string{"old-user", "compact", "tail-assistant", "tail-user"}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Fatalf("retained IDs = %v, want %v", gotIDs, wantIDs)
		}
	}
	if sess.Messages[1].Thinking != "" || sess.Messages[2].Thinking != "" {
		t.Fatalf("thinking was not stripped from retained messages")
	}
	if got := sess.Messages[2].ToolCalls[0].Output; got != "keep recent output" {
		t.Fatalf("tail tool output = %q, want preserved", got)
	}
}

func TestMessageTokenCountIncludesCacheTokens(t *testing.T) {
	msg := Message{InputTokens: 10, OutputTokens: 20, CacheReadTokens: 30, CacheWriteTokens: 40}
	if got := MessageTokenCount(msg); got != 100 {
		t.Fatalf("MessageTokenCount() = %d, want 100", got)
	}
}
