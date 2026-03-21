package session

import (
	"testing"
)

func TestDetectForks_BasicFork(t *testing.T) {
	// Session A: messages 1-5 (same content)
	// Session B: messages 1-5 (same content) + 6-8 (fork diverges)
	// IDs differ but content is identical — simulating OpenCode fork behavior.
	original := &Session{
		ID: "original",
		Messages: []Message{
			{ID: "orig-1", Role: RoleUser, Content: "hello", InputTokens: 100, OutputTokens: 50},
			{ID: "orig-2", Role: RoleAssistant, Content: "hi there", InputTokens: 200, OutputTokens: 100},
			{ID: "orig-3", Role: RoleUser, Content: "help me", InputTokens: 300, OutputTokens: 150},
			{ID: "orig-4", Role: RoleAssistant, Content: "sure thing", InputTokens: 400, OutputTokens: 200},
			{ID: "orig-5", Role: RoleUser, Content: "thanks", InputTokens: 500, OutputTokens: 250},
		},
	}

	fork := &Session{
		ID: "fork-1",
		Messages: []Message{
			{ID: "fork-1", Role: RoleUser, Content: "hello", InputTokens: 100, OutputTokens: 50},
			{ID: "fork-2", Role: RoleAssistant, Content: "hi there", InputTokens: 200, OutputTokens: 100},
			{ID: "fork-3", Role: RoleUser, Content: "help me", InputTokens: 300, OutputTokens: 150},
			{ID: "fork-4", Role: RoleAssistant, Content: "sure thing", InputTokens: 400, OutputTokens: 200},
			{ID: "fork-5", Role: RoleUser, Content: "thanks", InputTokens: 500, OutputTokens: 250},
			{ID: "fork-6", Role: RoleUser, Content: "new question", InputTokens: 600, OutputTokens: 300},
			{ID: "fork-7", Role: RoleAssistant, Content: "new answer", InputTokens: 700, OutputTokens: 350},
			{ID: "fork-8", Role: RoleUser, Content: "got it", InputTokens: 800, OutputTokens: 400},
		},
	}

	relations := DetectForks([]*Session{original, fork})

	if len(relations) != 1 {
		t.Fatalf("expected 1 fork relation, got %d", len(relations))
	}

	rel := relations[0]
	if rel.OriginalID != "original" {
		t.Errorf("OriginalID = %q, want %q", rel.OriginalID, "original")
	}
	if rel.ForkID != "fork-1" {
		t.Errorf("ForkID = %q, want %q", rel.ForkID, "fork-1")
	}
	if rel.ForkPoint != 5 {
		t.Errorf("ForkPoint = %d, want 5", rel.ForkPoint)
	}
	if rel.SharedMessages != 5 {
		t.Errorf("SharedMessages = %d, want 5", rel.SharedMessages)
	}
	if rel.OverlapRatio != 1.0 {
		t.Errorf("OverlapRatio = %f, want 1.0", rel.OverlapRatio)
	}
	// Shared tokens = sum of first 5 messages.
	expectedSharedInput := 100 + 200 + 300 + 400 + 500
	if rel.SharedInputTokens != expectedSharedInput {
		t.Errorf("SharedInputTokens = %d, want %d", rel.SharedInputTokens, expectedSharedInput)
	}
}

func TestDetectForks_NoOverlap(t *testing.T) {
	a := &Session{
		ID: "a",
		Messages: []Message{
			{Role: RoleUser, Content: "aaa"},
			{Role: RoleAssistant, Content: "bbb"},
			{Role: RoleUser, Content: "ccc"},
		},
	}
	b := &Session{
		ID: "b",
		Messages: []Message{
			{Role: RoleUser, Content: "xxx"},
			{Role: RoleAssistant, Content: "yyy"},
			{Role: RoleUser, Content: "zzz"},
		},
	}

	relations := DetectForks([]*Session{a, b})
	if len(relations) != 0 {
		t.Errorf("expected 0 fork relations, got %d", len(relations))
	}
}

func TestDetectForks_LowOverlap(t *testing.T) {
	// Only 2/10 messages shared — below 50% threshold.
	msgs := func(contents ...string) []Message {
		var m []Message
		for i, c := range contents {
			role := RoleUser
			if i%2 == 1 {
				role = RoleAssistant
			}
			m = append(m, Message{Role: role, Content: c})
		}
		return m
	}

	a := &Session{ID: "a", Messages: msgs("s1", "s2", "a3", "a4", "a5", "a6", "a7", "a8", "a9", "a10")}
	b := &Session{ID: "b", Messages: msgs("s1", "s2", "b3", "b4", "b5")}

	relations := DetectForks([]*Session{a, b})
	if len(relations) != 0 {
		t.Errorf("expected 0 fork relations (below threshold), got %d", len(relations))
	}
}

func TestDetectForks_MultipleForks(t *testing.T) {
	original := &Session{
		ID: "orig",
		Messages: []Message{
			{Role: RoleUser, Content: "m1"},
			{Role: RoleAssistant, Content: "m2"},
			{Role: RoleUser, Content: "m3"},
			{Role: RoleAssistant, Content: "m4"},
			{Role: RoleUser, Content: "m5"},
		},
	}
	fork1 := &Session{
		ID: "f1",
		Messages: []Message{
			{Role: RoleUser, Content: "m1"},
			{Role: RoleAssistant, Content: "m2"},
			{Role: RoleUser, Content: "m3"},
			{Role: RoleAssistant, Content: "m4"},
			{Role: RoleUser, Content: "m5"},
			{Role: RoleUser, Content: "f1-m6"},
		},
	}
	fork2 := &Session{
		ID: "f2",
		Messages: []Message{
			{Role: RoleUser, Content: "m1"},
			{Role: RoleAssistant, Content: "m2"},
			{Role: RoleUser, Content: "m3"},
			{Role: RoleAssistant, Content: "f2-m4"},
			{Role: RoleUser, Content: "f2-m5"},
		},
	}

	relations := DetectForks([]*Session{original, fork1, fork2})

	// orig→f1 (5 shared, 100% overlap) — detected
	// orig→f2 (3 shared, 60% overlap) — detected
	// f1→f2 would be transitive — skipped because f1 is already a fork
	if len(relations) < 2 {
		t.Errorf("expected at least 2 fork relations, got %d", len(relations))
	}
}

func TestDetectForks_EmptyMessages(t *testing.T) {
	a := &Session{ID: "a", Messages: nil}
	b := &Session{ID: "b", Messages: []Message{{Role: RoleUser, Content: "hi"}}}

	relations := DetectForks([]*Session{a, b})
	if len(relations) != 0 {
		t.Errorf("expected 0 relations for empty session, got %d", len(relations))
	}
}

func TestDetectForks_SingleSession(t *testing.T) {
	a := &Session{ID: "a", Messages: []Message{
		{Role: RoleUser, Content: "m1"},
		{Role: RoleAssistant, Content: "m2"},
		{Role: RoleUser, Content: "m3"},
	}}
	relations := DetectForks([]*Session{a})
	if len(relations) != 0 {
		t.Errorf("expected 0 relations for single session, got %d", len(relations))
	}
}

func TestDetectForks_Nil(t *testing.T) {
	relations := DetectForks(nil)
	if relations != nil {
		t.Errorf("expected nil for nil input, got %v", relations)
	}
}

func TestCommonPrefixLen(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want int
	}{
		{"identical", []string{"a", "b", "c"}, []string{"a", "b", "c"}, 3},
		{"prefix", []string{"a", "b", "c"}, []string{"a", "b", "d"}, 2},
		{"no overlap", []string{"a"}, []string{"b"}, 0},
		{"empty", []string{}, []string{"a"}, 0},
		{"different lengths", []string{"a", "b"}, []string{"a", "b", "c", "d"}, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := commonPrefixLen(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("commonPrefixLen() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMessageContentHash(t *testing.T) {
	// Same content, same role → same hash.
	a := messageContentHash(&Message{Role: RoleUser, Content: "hello"})
	b := messageContentHash(&Message{Role: RoleUser, Content: "hello"})
	if a != b {
		t.Errorf("same content should produce same hash: %q vs %q", a, b)
	}

	// Same content, different role → different hash.
	c := messageContentHash(&Message{Role: RoleAssistant, Content: "hello"})
	if a == c {
		t.Error("different role should produce different hash")
	}

	// Different content → different hash.
	d := messageContentHash(&Message{Role: RoleUser, Content: "world"})
	if a == d {
		t.Error("different content should produce different hash")
	}
}
