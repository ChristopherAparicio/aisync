package service

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/auth"
	"github.com/ChristopherAparicio/aisync/internal/llm"
	"github.com/ChristopherAparicio/aisync/internal/pricing"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
)

// ── Summarize ──

func TestSummarize_success(t *testing.T) {
	summary := session.StructuredSummary{
		Intent:    "Add authentication",
		Outcome:   "JWT middleware implemented",
		Decisions: []string{"Used JWT over sessions"},
		Friction:  []string{"CORS issues"},
		OpenItems: []string{"Add refresh tokens"},
	}
	summaryJSON, _ := json.Marshal(summary)

	mockLLM := &mockLLMClient{
		response: &llm.CompletionResponse{
			Content:      string(summaryJSON),
			Model:        "test-model",
			InputTokens:  100,
			OutputTokens: 50,
		},
	}

	store := &mockStore{
		sessions: make(map[session.ID]*session.Session),
	}

	svc := NewSessionService(SessionServiceConfig{
		Store: store,
		LLM:   mockLLM,
	})

	sess := &session.Session{
		ID: "test-session",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "Add JWT auth"},
			{Role: session.RoleAssistant, Content: "I'll implement JWT middleware..."},
		},
	}

	result, err := svc.Summarize(context.Background(), SummarizeRequest{Session: sess})
	if err != nil {
		t.Fatalf("Summarize() error: %v", err)
	}

	if result.Summary.Intent != "Add authentication" {
		t.Errorf("Intent = %q, want %q", result.Summary.Intent, "Add authentication")
	}
	if result.OneLine != "Add authentication: JWT middleware implemented" {
		t.Errorf("OneLine = %q, want %q", result.OneLine, "Add authentication: JWT middleware implemented")
	}
	if result.Model != "test-model" {
		t.Errorf("Model = %q, want %q", result.Model, "test-model")
	}
	if result.TokensUsed != 150 {
		t.Errorf("TokensUsed = %d, want 150", result.TokensUsed)
	}
}

func TestSummarize_noLLM(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{
		Store: &mockStore{sessions: make(map[session.ID]*session.Session)},
		// No LLM
	})

	sess := &session.Session{
		ID:       "test",
		Messages: []session.Message{{Role: session.RoleUser, Content: "hello"}},
	}

	_, err := svc.Summarize(context.Background(), SummarizeRequest{Session: sess})
	if err == nil {
		t.Fatal("expected error when LLM is nil")
	}
}

func TestSummarize_noMessages(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{
		Store: &mockStore{sessions: make(map[session.ID]*session.Session)},
		LLM:   &mockLLMClient{},
	})

	sess := &session.Session{ID: "test"}
	_, err := svc.Summarize(context.Background(), SummarizeRequest{Session: sess})
	if err == nil {
		t.Fatal("expected error for session with no messages")
	}
}

// ── Explain ──

func TestExplain_success(t *testing.T) {
	mockLLM := &mockLLMClient{
		response: &llm.CompletionResponse{
			Content:      "This session implemented JWT authentication...",
			Model:        "test-model",
			InputTokens:  200,
			OutputTokens: 100,
		},
	}

	sess := &session.Session{
		ID:       "explain-test",
		Messages: []session.Message{{Role: session.RoleUser, Content: "Add auth"}},
	}
	store := &mockStore{
		sessions: map[session.ID]*session.Session{
			sess.ID: sess,
		},
	}

	svc := NewSessionService(SessionServiceConfig{
		Store: store,
		LLM:   mockLLM,
	})

	result, err := svc.Explain(context.Background(), ExplainRequest{
		SessionID: sess.ID,
	})
	if err != nil {
		t.Fatalf("Explain() error: %v", err)
	}

	if result.Explanation != "This session implemented JWT authentication..." {
		t.Errorf("Explanation = %q, want expected text", result.Explanation)
	}
	if result.SessionID != "explain-test" {
		t.Errorf("SessionID = %q, want %q", result.SessionID, "explain-test")
	}
}

func TestExplain_noLLM(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{
		Store: &mockStore{sessions: make(map[session.ID]*session.Session)},
	})

	_, err := svc.Explain(context.Background(), ExplainRequest{SessionID: "any"})
	if err == nil {
		t.Fatal("expected error when LLM is nil")
	}
}

func TestExplain_sessionNotFound(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{
		Store: &mockStore{sessions: make(map[session.ID]*session.Session)},
		LLM:   &mockLLMClient{},
	})

	_, err := svc.Explain(context.Background(), ExplainRequest{SessionID: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

// ── Rewind ──

func TestRewind_success(t *testing.T) {
	original := &session.Session{
		ID:       "original",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Branch:   "feat/auth",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "Step 1", InputTokens: 10},
			{ID: "m2", Role: session.RoleAssistant, Content: "Done 1", OutputTokens: 20},
			{ID: "m3", Role: session.RoleUser, Content: "Step 2", InputTokens: 15},
			{ID: "m4", Role: session.RoleAssistant, Content: "Done 2", OutputTokens: 25},
			{ID: "m5", Role: session.RoleUser, Content: "Step 3", InputTokens: 12},
		},
		FileChanges: []session.FileChange{
			{FilePath: "auth.go", ChangeType: session.ChangeModified},
		},
	}

	store := &mockStore{
		sessions: map[session.ID]*session.Session{
			original.ID: original,
		},
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.Rewind(context.Background(), RewindRequest{
		SessionID: original.ID,
		AtMessage: 3,
	})
	if err != nil {
		t.Fatalf("Rewind() error: %v", err)
	}

	if result.OriginalID != "original" {
		t.Errorf("OriginalID = %q, want %q", result.OriginalID, "original")
	}
	if result.TruncatedAt != 3 {
		t.Errorf("TruncatedAt = %d, want 3", result.TruncatedAt)
	}
	if result.MessagesRemoved != 2 {
		t.Errorf("MessagesRemoved = %d, want 2", result.MessagesRemoved)
	}
	if len(result.NewSession.Messages) != 3 {
		t.Errorf("new session has %d messages, want 3", len(result.NewSession.Messages))
	}
	if result.NewSession.Messages[2].ID != "m3" {
		t.Errorf("last message ID = %q, want %q", result.NewSession.Messages[2].ID, "m3")
	}
	if result.NewSession.ParentID != "original" {
		t.Errorf("ParentID = %q, want %q", result.NewSession.ParentID, "original")
	}
	if result.NewSession.Branch != "feat/auth" {
		t.Errorf("Branch = %q, want %q", result.NewSession.Branch, "feat/auth")
	}
	// Verify it was saved
	if _, ok := store.sessions[result.NewSession.ID]; !ok {
		t.Error("new session was not saved to store")
	}
}

func TestRewind_outOfRange(t *testing.T) {
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "hello"},
		},
	}
	store := &mockStore{
		sessions: map[session.ID]*session.Session{sess.ID: sess},
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})

	_, err := svc.Rewind(context.Background(), RewindRequest{
		SessionID: sess.ID,
		AtMessage: 5, // only 1 message exists
	})
	if err == nil {
		t.Fatal("expected error for out-of-range message index")
	}
}

func TestRewind_noMessages(t *testing.T) {
	sess := &session.Session{ID: "empty"}
	store := &mockStore{
		sessions: map[session.ID]*session.Session{sess.ID: sess},
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})

	_, err := svc.Rewind(context.Background(), RewindRequest{
		SessionID: sess.ID,
		AtMessage: 1,
	})
	if err == nil {
		t.Fatal("expected error for session with no messages")
	}
}

// ── ToolUsage ──

func TestToolUsage_success(t *testing.T) {
	sess := &session.Session{
		ID: "tool-test",
		Messages: []session.Message{
			{
				ID: "m1", Role: session.RoleAssistant, Model: "claude-sonnet-4",
				ToolCalls: []session.ToolCall{
					{ID: "tc1", Name: "Read", Input: "path/to/file.go", Output: "package main...", State: session.ToolStateCompleted, DurationMs: 50, InputTokens: 4, OutputTokens: 3},
					{ID: "tc2", Name: "Write", Input: `{"path":"out.go","content":"..."}`, Output: "ok", State: session.ToolStateCompleted, DurationMs: 100, InputTokens: 8, OutputTokens: 1},
				},
			},
			{
				ID: "m2", Role: session.RoleAssistant, Model: "claude-sonnet-4",
				ToolCalls: []session.ToolCall{
					{ID: "tc3", Name: "Read", Input: "another.go", Output: "func foo()...", State: session.ToolStateCompleted, DurationMs: 30, InputTokens: 2, OutputTokens: 3},
					{ID: "tc4", Name: "Bash", Input: "go test ./...", Output: "PASS", State: session.ToolStateError, DurationMs: 5000, InputTokens: 3, OutputTokens: 1},
				},
			},
		},
	}

	store := &mockStore{
		sessions: map[session.ID]*session.Session{sess.ID: sess},
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.ToolUsage(context.Background(), "tool-test")
	if err != nil {
		t.Fatalf("ToolUsage() error: %v", err)
	}

	if result.TotalCalls != 4 {
		t.Errorf("TotalCalls = %d, want 4", result.TotalCalls)
	}
	if len(result.Tools) != 3 {
		t.Fatalf("len(Tools) = %d, want 3 (Bash, Read, Write)", len(result.Tools))
	}

	// Should be sorted by total tokens descending.
	// Read: input=4+2=6, output=3+3=6, total=12
	// Write: input=8, output=1, total=9
	// Bash: input=3, output=1, total=4
	if result.Tools[0].Name != "Read" {
		t.Errorf("Tools[0].Name = %q, want Read (highest tokens)", result.Tools[0].Name)
	}
	if result.Tools[0].Calls != 2 {
		t.Errorf("Read.Calls = %d, want 2", result.Tools[0].Calls)
	}
	if result.Tools[0].TotalTokens != 12 {
		t.Errorf("Read.TotalTokens = %d, want 12", result.Tools[0].TotalTokens)
	}
	if result.Tools[0].AvgDuration != 40 { // (50+30)/2 = 40
		t.Errorf("Read.AvgDuration = %d, want 40", result.Tools[0].AvgDuration)
	}

	// Bash should have 1 error
	bashFound := false
	for _, tool := range result.Tools {
		if tool.Name == "Bash" {
			bashFound = true
			if tool.ErrorCount != 1 {
				t.Errorf("Bash.ErrorCount = %d, want 1", tool.ErrorCount)
			}
		}
	}
	if !bashFound {
		t.Error("Bash tool not found in results")
	}

	// Percentages should sum to ~100
	var totalPct float64
	for _, tool := range result.Tools {
		totalPct += tool.Percentage
	}
	if totalPct < 99.0 || totalPct > 101.0 {
		t.Errorf("Total percentage = %.1f, want ~100", totalPct)
	}
}

func TestToolUsage_noToolCalls(t *testing.T) {
	sess := &session.Session{
		ID:       "no-tools",
		Messages: []session.Message{{Role: session.RoleAssistant, Content: "just text"}},
	}
	store := &mockStore{
		sessions: map[session.ID]*session.Session{sess.ID: sess},
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.ToolUsage(context.Background(), "no-tools")
	if err != nil {
		t.Fatalf("ToolUsage() error: %v", err)
	}
	if result.TotalCalls != 0 {
		t.Errorf("TotalCalls = %d, want 0", result.TotalCalls)
	}
	if len(result.Tools) != 0 {
		t.Errorf("len(Tools) = %d, want 0", len(result.Tools))
	}
}

func TestToolUsage_estimatesFromContent(t *testing.T) {
	// ToolCalls with no explicit token counts — should estimate from content size
	sess := &session.Session{
		ID: "estimate-test",
		Messages: []session.Message{
			{
				ID: "m1", Role: session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{ID: "tc1", Name: "Read", Input: "abcdefghijklmnop", Output: "abcdefghijklmnopqrstuvwx", State: session.ToolStateCompleted},
				},
			},
		},
	}
	store := &mockStore{sessions: map[session.ID]*session.Session{sess.ID: sess}}
	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.ToolUsage(context.Background(), "estimate-test")
	if err != nil {
		t.Fatalf("ToolUsage() error: %v", err)
	}
	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
	// Input: 16 chars / 4 = 4 tokens. Output: 24 chars / 4 = 6 tokens.
	if result.Tools[0].InputTokens != 4 {
		t.Errorf("InputTokens = %d, want 4 (16/4)", result.Tools[0].InputTokens)
	}
	if result.Tools[0].OutputTokens != 6 {
		t.Errorf("OutputTokens = %d, want 6 (24/4)", result.Tools[0].OutputTokens)
	}
}

// ── AnalyzeEfficiency ──

func TestAnalyzeEfficiency_success(t *testing.T) {
	report := session.EfficiencyReport{
		Score:       72,
		Summary:     "The session was reasonably efficient with some minor retry patterns.",
		Strengths:   []string{"Focused tool usage", "Clean conversation flow"},
		Issues:      []string{"Repeated file reads for the same file"},
		Suggestions: []string{"Cache file contents to avoid re-reading"},
		Patterns:    []string{"over-reading"},
	}
	reportJSON, _ := json.Marshal(report)

	mockLLM := &mockLLMClient{
		response: &llm.CompletionResponse{
			Content:      string(reportJSON),
			Model:        "test-model",
			InputTokens:  300,
			OutputTokens: 200,
		},
	}

	sess := &session.Session{
		ID:       "eff-test",
		Provider: session.ProviderClaudeCode,
		Branch:   "feat/optimize",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "Refactor the auth module", InputTokens: 50},
			{
				ID: "m2", Role: session.RoleAssistant, Content: "I'll read the file first", OutputTokens: 100, Model: "claude-sonnet-4",
				ToolCalls: []session.ToolCall{
					{ID: "tc1", Name: "Read", Input: "auth.go", Output: "package auth...", State: session.ToolStateCompleted, DurationMs: 30, InputTokens: 2, OutputTokens: 10},
				},
			},
			{ID: "m3", Role: session.RoleUser, Content: "Looks good, now write the test", InputTokens: 30},
			{
				ID: "m4", Role: session.RoleAssistant, Content: "Writing the test...", OutputTokens: 150, Model: "claude-sonnet-4",
				ToolCalls: []session.ToolCall{
					{ID: "tc2", Name: "Write", Input: `{"path":"auth_test.go"}`, Output: "ok", State: session.ToolStateCompleted, DurationMs: 50, InputTokens: 5, OutputTokens: 1},
				},
			},
		},
		TokenUsage: session.TokenUsage{InputTokens: 80, OutputTokens: 250, TotalTokens: 330},
		FileChanges: []session.FileChange{
			{FilePath: "auth.go", ChangeType: session.ChangeModified},
			{FilePath: "auth_test.go", ChangeType: session.ChangeCreated},
		},
	}

	store := &mockStore{
		sessions: map[session.ID]*session.Session{sess.ID: sess},
	}

	svc := NewSessionService(SessionServiceConfig{
		Store: store,
		LLM:   mockLLM,
	})

	result, err := svc.AnalyzeEfficiency(context.Background(), EfficiencyRequest{
		SessionID: sess.ID,
	})
	if err != nil {
		t.Fatalf("AnalyzeEfficiency() error: %v", err)
	}

	if result.Report.Score != 72 {
		t.Errorf("Score = %d, want 72", result.Report.Score)
	}
	if result.Report.Summary == "" {
		t.Error("Summary should not be empty")
	}
	if len(result.Report.Strengths) != 2 {
		t.Errorf("len(Strengths) = %d, want 2", len(result.Report.Strengths))
	}
	if len(result.Report.Issues) != 1 {
		t.Errorf("len(Issues) = %d, want 1", len(result.Report.Issues))
	}
	if len(result.Report.Suggestions) != 1 {
		t.Errorf("len(Suggestions) = %d, want 1", len(result.Report.Suggestions))
	}
	if len(result.Report.Patterns) != 1 {
		t.Errorf("len(Patterns) = %d, want 1", len(result.Report.Patterns))
	}
	if result.SessionID != "eff-test" {
		t.Errorf("SessionID = %q, want %q", result.SessionID, "eff-test")
	}
	if result.Model != "test-model" {
		t.Errorf("Model = %q, want %q", result.Model, "test-model")
	}
	if result.TokensUsed != 500 {
		t.Errorf("TokensUsed = %d, want 500", result.TokensUsed)
	}

	// Verify the LLM prompt includes session stats
	if mockLLM.lastReq.SystemPrompt == "" {
		t.Error("expected non-empty system prompt")
	}
	userPrompt := mockLLM.lastReq.UserPrompt
	if !contains(userPrompt, "eff-test") {
		t.Error("user prompt should contain session ID")
	}
	if !contains(userPrompt, "Tool calls:") {
		t.Error("user prompt should contain tool call stats")
	}
	if !contains(userPrompt, "Read") {
		t.Error("user prompt should contain tool names")
	}
}

func TestAnalyzeEfficiency_noLLM(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{
		Store: &mockStore{sessions: make(map[session.ID]*session.Session)},
		// No LLM
	})

	_, err := svc.AnalyzeEfficiency(context.Background(), EfficiencyRequest{SessionID: "any"})
	if err == nil {
		t.Fatal("expected error when LLM is nil")
	}
}

func TestAnalyzeEfficiency_sessionNotFound(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{
		Store: &mockStore{sessions: make(map[session.ID]*session.Session)},
		LLM:   &mockLLMClient{},
	})

	_, err := svc.AnalyzeEfficiency(context.Background(), EfficiencyRequest{SessionID: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestAnalyzeEfficiency_noMessages(t *testing.T) {
	sess := &session.Session{ID: "empty-eff"}
	store := &mockStore{sessions: map[session.ID]*session.Session{sess.ID: sess}}

	svc := NewSessionService(SessionServiceConfig{
		Store: store,
		LLM:   &mockLLMClient{},
	})

	_, err := svc.AnalyzeEfficiency(context.Background(), EfficiencyRequest{SessionID: sess.ID})
	if err == nil {
		t.Fatal("expected error for session with no messages")
	}
}

func TestAnalyzeEfficiency_clampsScore(t *testing.T) {
	// LLM returns score > 100, should be clamped
	report := session.EfficiencyReport{
		Score:   150,
		Summary: "Perfect session",
	}
	reportJSON, _ := json.Marshal(report)

	mockLLM := &mockLLMClient{
		response: &llm.CompletionResponse{
			Content:      string(reportJSON),
			Model:        "test-model",
			InputTokens:  100,
			OutputTokens: 50,
		},
	}

	sess := &session.Session{
		ID:       "clamp-test",
		Messages: []session.Message{{Role: session.RoleUser, Content: "hello"}},
	}
	store := &mockStore{sessions: map[session.ID]*session.Session{sess.ID: sess}}

	svc := NewSessionService(SessionServiceConfig{
		Store: store,
		LLM:   mockLLM,
	})

	result, err := svc.AnalyzeEfficiency(context.Background(), EfficiencyRequest{SessionID: sess.ID})
	if err != nil {
		t.Fatalf("AnalyzeEfficiency() error: %v", err)
	}
	if result.Report.Score != 100 {
		t.Errorf("Score = %d, want 100 (clamped from 150)", result.Report.Score)
	}
}

func TestBuildEfficiencyPrompt(t *testing.T) {
	sess := &session.Session{
		ID:       "prompt-test",
		Provider: session.ProviderClaudeCode,
		Branch:   "main",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "Do something", InputTokens: 10},
			{
				ID: "m2", Role: session.RoleAssistant, Content: "Done", OutputTokens: 20,
				ToolCalls: []session.ToolCall{
					{Name: "Read", Input: "file.go", Output: "content", State: session.ToolStateCompleted, DurationMs: 50},
					{Name: "Read", Input: "other.go", Output: "", State: session.ToolStateError, DurationMs: 10},
				},
			},
		},
		TokenUsage: session.TokenUsage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
		FileChanges: []session.FileChange{
			{FilePath: "file.go", ChangeType: session.ChangeModified},
		},
	}

	prompt := buildEfficiencyPrompt(sess, nil)

	// Should contain key sections
	for _, want := range []string{
		"prompt-test",
		"claude-code",
		"Messages: 2",
		"Tool calls: 2 total, 1 errors",
		"Read:",
		"Conversation flow",
		"Files changed: 1",
		"file.go",
	} {
		if !contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestSafePercent(t *testing.T) {
	if got := safePercent(1, 4); got != 25.0 {
		t.Errorf("safePercent(1,4) = %f, want 25.0", got)
	}
	if got := safePercent(0, 0); got != 0.0 {
		t.Errorf("safePercent(0,0) = %f, want 0.0", got)
	}
	if got := safePercent(3, 3); got != 100.0 {
		t.Errorf("safePercent(3,3) = %f, want 100.0", got)
	}
}

// ── StructuredSummary.OneLine ──

func TestStructuredSummary_OneLine(t *testing.T) {
	tests := []struct {
		name     string
		summary  session.StructuredSummary
		expected string
	}{
		{
			name:     "both fields",
			summary:  session.StructuredSummary{Intent: "Add auth", Outcome: "JWT implemented"},
			expected: "Add auth: JWT implemented",
		},
		{
			name:     "intent only",
			summary:  session.StructuredSummary{Intent: "Add auth"},
			expected: "Add auth",
		},
		{
			name:     "outcome only",
			summary:  session.StructuredSummary{Outcome: "Done"},
			expected: "Done",
		},
		{
			name:     "both empty",
			summary:  session.StructuredSummary{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.summary.OneLine(); got != tt.expected {
				t.Errorf("OneLine() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// ── buildSessionTranscript ──

func TestBuildSessionTranscript_emptySession(t *testing.T) {
	sess := &session.Session{ID: "empty"}
	if got := buildSessionTranscript(sess); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestBuildSessionTranscript_withMessages(t *testing.T) {
	sess := &session.Session{
		ID:       "test",
		Provider: session.ProviderClaudeCode,
		Branch:   "main",
		Summary:  "Test session",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "hello"},
			{Role: session.RoleAssistant, Content: "hi there"},
		},
		FileChanges: []session.FileChange{
			{FilePath: "main.go", ChangeType: session.ChangeModified},
		},
	}

	result := buildSessionTranscript(sess)
	if result == "" {
		t.Fatal("expected non-empty transcript")
	}
	// Should contain key info
	for _, want := range []string{"test", "claude-code", "main", "hello", "hi there", "main.go"} {
		if !contains(result, want) {
			t.Errorf("transcript missing %q", want)
		}
	}
}

func TestBuildSessionTranscript_truncatesLargeSessions(t *testing.T) {
	messages := make([]session.Message, 30)
	for i := range messages {
		messages[i] = session.Message{
			Role:    session.RoleUser,
			Content: "message content",
		}
	}

	sess := &session.Session{
		ID:       "large",
		Messages: messages,
	}

	result := buildSessionTranscript(sess)
	// Should mention truncation
	if !contains(result, "showing") {
		t.Error("expected truncation notice for large session")
	}
}

// ── Helpers ──

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ── Get ──

func TestGet_bySessionID(t *testing.T) {
	store := &mockStore{
		sessions: map[session.ID]*session.Session{
			"abc-123": {ID: "abc-123", Provider: "claude-code"},
		},
	}
	svc := NewSessionService(SessionServiceConfig{Store: store})

	sess, err := svc.Get("abc-123")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if sess.ID != "abc-123" {
		t.Errorf("ID = %q, want %q", sess.ID, "abc-123")
	}
}

func TestGet_notFound(t *testing.T) {
	store := &mockStore{sessions: make(map[session.ID]*session.Session)}
	svc := NewSessionService(SessionServiceConfig{Store: store})

	_, err := svc.Get("nonexistent-id")
	if err == nil {
		t.Fatal("Get() expected error for nonexistent session, got nil")
	}
}

func TestGet_noGitClient_fallsBackToID(t *testing.T) {
	// Even if the argument looks like a commit SHA, without a git client
	// it should fall back to session ID lookup.
	store := &mockStore{
		sessions: map[session.ID]*session.Session{
			"deadbeef": {ID: "deadbeef", Provider: "claude-code"},
		},
	}
	svc := NewSessionService(SessionServiceConfig{Store: store}) // no Git client

	sess, err := svc.Get("deadbeef")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if sess.ID != "deadbeef" {
		t.Errorf("ID = %q, want %q", sess.ID, "deadbeef")
	}
}

// ── looksLikeCommitSHA ──

func TestLooksLikeCommitSHA(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"abc1234", true},      // 7 hex chars — short SHA
		{"deadbeefcafe", true}, // 12 hex chars
		{"a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0", true}, // 40 chars — full SHA
		{"ABCDEF1", true},      // uppercase hex
		{"abc123", false},      // too short (6)
		{"", false},            // empty
		{"abc123g", false},     // non-hex char 'g'
		{"abc-123-def", false}, // dashes (UUID-like)
		{"a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0a", false}, // 41 chars — too long
	}

	for _, tt := range tests {
		got := looksLikeCommitSHA(tt.input)
		if got != tt.want {
			t.Errorf("looksLikeCommitSHA(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ── Mocks ──

type mockLLMClient struct {
	response *llm.CompletionResponse
	err      error
	lastReq  llm.CompletionRequest
}

func (m *mockLLMClient) Complete(_ context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	m.lastReq = req
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

type mockStore struct {
	sessions     map[session.ID]*session.Session
	analyses     map[string]*analysis.SessionAnalysis
	searchResult *session.SearchResult                // configurable for voice search tests
	sessionLinks map[session.ID][]session.SessionLink // session-to-session links
}

func (m *mockStore) Save(sess *session.Session) error {
	m.sessions[sess.ID] = sess
	return nil
}

func (m *mockStore) Get(id session.ID) (*session.Session, error) {
	s, ok := m.sessions[id]
	if !ok {
		return nil, session.ErrSessionNotFound
	}
	return s, nil
}

func (m *mockStore) GetLatestByBranch(_, _ string) (*session.Session, error) {
	return nil, session.ErrSessionNotFound
}
func (m *mockStore) CountByBranch(_, _ string) (int, error)                { return 0, nil }
func (m *mockStore) List(_ session.ListOptions) ([]session.Summary, error) { return nil, nil }
func (m *mockStore) Delete(_ session.ID) error                             { return nil }
func (m *mockStore) UpdateSummary(_ session.ID, _ string) error            { return nil }
func (m *mockStore) UpdateSessionType(_ session.ID, _ string) error        { return nil }
func (m *mockStore) UpdateProjectCategory(_ string, _ string) (int, error) { return 0, nil }
func (m *mockStore) SetProjectCategory(_ session.ID, _ string) error       { return nil }
func (m *mockStore) SaveForkRelation(_ session.ForkRelation) error         { return nil }
func (m *mockStore) GetForkRelations(_ session.ID) ([]session.ForkRelation, error) {
	return nil, nil
}
func (m *mockStore) GetTotalDeduplication() (int, int, error)       { return 0, 0, nil }
func (m *mockStore) SaveObjective(_ session.SessionObjective) error { return nil }
func (m *mockStore) GetObjective(_ session.ID) (*session.SessionObjective, error) {
	return nil, nil
}
func (m *mockStore) ListObjectives(_ []session.ID) (map[session.ID]*session.SessionObjective, error) {
	return nil, nil
}
func (m *mockStore) UpsertTokenBucket(_ session.TokenUsageBucket) error { return nil }
func (m *mockStore) QueryTokenBuckets(_ string, _, _ time.Time, _ string) ([]session.TokenUsageBucket, error) {
	return nil, nil
}
func (m *mockStore) GetLastBucketComputeTime(_ string) (time.Time, error) {
	return time.Time{}, nil
}
func (m *mockStore) AddLink(_ session.ID, _ session.Link) error { return nil }
func (m *mockStore) GetByLink(_ session.LinkType, _ string) ([]session.Summary, error) {
	return nil, session.ErrSessionNotFound
}
func (m *mockStore) DeleteOlderThan(_ time.Time) (int, error) { return 0, nil }
func (m *mockStore) Close() error                             { return nil }

func (m *mockStore) GetPreferences(_ session.ID) (*session.UserPreferences, error) { return nil, nil }
func (m *mockStore) SavePreferences(_ *session.UserPreferences) error              { return nil }

func (m *mockStore) GetCache(_ string, _ time.Duration) ([]byte, error) { return nil, nil }
func (m *mockStore) SetCache(_ string, _ []byte) error                  { return nil }
func (m *mockStore) InvalidateCache(_ string) error                     { return nil }
func (m *mockStore) GetFreshness(_ session.ID) (int, int64, error)      { return 0, 0, nil }
func (m *mockStore) ListProjects() ([]session.ProjectGroup, error)      { return nil, nil }
func (m *mockStore) SaveUser(_ *session.User) error                     { return nil }
func (m *mockStore) GetUser(_ session.ID) (*session.User, error)        { return nil, nil }
func (m *mockStore) GetUserByEmail(_ string) (*session.User, error)     { return nil, nil }
func (m *mockStore) Search(_ session.SearchQuery) (*session.SearchResult, error) {
	if m.searchResult != nil {
		return m.searchResult, nil
	}
	return &session.SearchResult{}, nil
}
func (m *mockStore) GetSessionsByFile(_ session.BlameQuery) ([]session.BlameEntry, error) {
	return nil, nil
}
func (m *mockStore) SaveAnalysis(a *analysis.SessionAnalysis) error {
	if m.analyses == nil {
		m.analyses = make(map[string]*analysis.SessionAnalysis)
	}
	m.analyses[a.ID] = a
	return nil
}
func (m *mockStore) GetAnalysis(id string) (*analysis.SessionAnalysis, error) {
	if m.analyses != nil {
		if a, ok := m.analyses[id]; ok {
			return a, nil
		}
	}
	return nil, analysis.ErrAnalysisNotFound
}
func (m *mockStore) GetAnalysisBySession(sessionID string) (*analysis.SessionAnalysis, error) {
	if m.analyses != nil {
		for _, a := range m.analyses {
			if a.SessionID == sessionID {
				return a, nil
			}
		}
	}
	return nil, analysis.ErrAnalysisNotFound
}
func (m *mockStore) ListAnalyses(sessionID string) ([]*analysis.SessionAnalysis, error) {
	var results []*analysis.SessionAnalysis
	if m.analyses != nil {
		for _, a := range m.analyses {
			if a.SessionID == sessionID {
				results = append(results, a)
			}
		}
	}
	return results, nil
}
func (m *mockStore) LinkSessions(link session.SessionLink) error {
	if m.sessionLinks == nil {
		m.sessionLinks = make(map[session.ID][]session.SessionLink)
	}
	m.sessionLinks[link.SourceSessionID] = append(m.sessionLinks[link.SourceSessionID], link)
	// Also store the inverse link.
	inverse := link
	inverse.ID = session.NewID()
	inverse.SourceSessionID, inverse.TargetSessionID = link.TargetSessionID, link.SourceSessionID
	inverse.LinkType = link.LinkType.Inverse()
	m.sessionLinks[inverse.SourceSessionID] = append(m.sessionLinks[inverse.SourceSessionID], inverse)
	return nil
}
func (m *mockStore) GetLinkedSessions(id session.ID) ([]session.SessionLink, error) {
	if m.sessionLinks == nil {
		return nil, nil
	}
	return m.sessionLinks[id], nil
}
func (m *mockStore) DeleteSessionLink(id session.ID) error {
	for src, links := range m.sessionLinks {
		for i, l := range links {
			if l.ID == id {
				m.sessionLinks[src] = append(links[:i], links[i+1:]...)
				return nil
			}
		}
	}
	return nil
}

// Error store stubs.
func (m *mockStore) SaveErrors(_ []session.SessionError) error { return nil }
func (m *mockStore) GetErrors(_ session.ID) ([]session.SessionError, error) {
	return nil, nil
}
func (m *mockStore) GetErrorSummary(_ session.ID) (*session.SessionErrorSummary, error) {
	return nil, nil
}
func (m *mockStore) ListRecentErrors(_ int, _ session.ErrorCategory) ([]session.SessionError, error) {
	return nil, nil
}

// Auth store stubs.
func (m *mockStore) CreateAuthUser(_ *auth.User) error        { return nil }
func (m *mockStore) GetAuthUser(_ string) (*auth.User, error) { return nil, auth.ErrUserNotFound }
func (m *mockStore) GetAuthUserByUsername(_ string) (*auth.User, error) {
	return nil, auth.ErrUserNotFound
}
func (m *mockStore) UpdateAuthUser(_ *auth.User) error    { return nil }
func (m *mockStore) ListAuthUsers() ([]*auth.User, error) { return nil, nil }
func (m *mockStore) CreateAPIKey(_ *auth.APIKey) error    { return nil }
func (m *mockStore) GetAPIKeyByHash(_ string) (*auth.APIKey, error) {
	return nil, auth.ErrAPIKeyNotFound
}
func (m *mockStore) ListAPIKeysByUser(_ string) ([]*auth.APIKey, error) { return nil, nil }
func (m *mockStore) UpdateAPIKey(_ *auth.APIKey) error                  { return nil }
func (m *mockStore) DeleteAPIKey(_ string) error                        { return nil }
func (m *mockStore) CountAuthUsers() (int, error)                       { return 0, nil }

// Session event store stubs.
func (m *mockStore) SaveEvents(_ []sessionevent.Event) error { return nil }
func (m *mockStore) GetSessionEvents(_ session.ID) ([]sessionevent.Event, error) {
	return nil, nil
}
func (m *mockStore) QueryEvents(_ sessionevent.EventQuery) ([]sessionevent.Event, error) {
	return nil, nil
}
func (m *mockStore) DeleteSessionEvents(_ session.ID) error                { return nil }
func (m *mockStore) UpsertEventBucket(_ sessionevent.EventBucket) error    { return nil }
func (m *mockStore) UpsertEventBuckets(_ []sessionevent.EventBucket) error { return nil }
func (m *mockStore) QueryEventBuckets(_ sessionevent.BucketQuery) ([]sessionevent.EventBucket, error) {
	return nil, nil
}

// ── Garbage Collection tests ──

// gcMockStore is a more functional mock that supports List, Delete, DeleteOlderThan.
type gcMockStore struct {
	mockStore
	allSessions []*session.Session
	deleted     []session.ID
	deletedByGC int
}

func (g *gcMockStore) Save(s *session.Session) error {
	g.allSessions = append(g.allSessions, s)
	return g.mockStore.Save(s)
}

func (g *gcMockStore) Get(id session.ID) (*session.Session, error) {
	for _, s := range g.allSessions {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, session.ErrSessionNotFound
}

func (g *gcMockStore) List(_ session.ListOptions) ([]session.Summary, error) {
	// Return in reverse order (most recent first)
	var summaries []session.Summary
	for i := len(g.allSessions) - 1; i >= 0; i-- {
		s := g.allSessions[i]
		summaries = append(summaries, session.Summary{
			ID:           s.ID,
			Provider:     s.Provider,
			Branch:       s.Branch,
			Summary:      s.Summary,
			MessageCount: len(s.Messages),
			TotalTokens:  s.TokenUsage.TotalTokens,
		})
	}
	return summaries, nil
}

func (g *gcMockStore) Delete(id session.ID) error {
	g.deleted = append(g.deleted, id)
	return nil
}

func (g *gcMockStore) DeleteOlderThan(before time.Time) (int, error) {
	count := 0
	for _, s := range g.allSessions {
		if s.CreatedAt.Before(before) {
			count++
		}
	}
	g.deletedByGC = count
	return count, nil
}

func newGCMockStore() *gcMockStore {
	return &gcMockStore{
		mockStore: mockStore{sessions: make(map[session.ID]*session.Session)},
	}
}

func TestGarbageCollect_olderThan(t *testing.T) {
	store := newGCMockStore()
	now := time.Now().UTC()

	// Create sessions: 2 old (40 days ago), 1 recent (1 day ago)
	old1 := &session.Session{ID: "old1", Provider: "claude-code", Branch: "main", CreatedAt: now.Add(-40 * 24 * time.Hour)}
	old2 := &session.Session{ID: "old2", Provider: "claude-code", Branch: "main", CreatedAt: now.Add(-35 * 24 * time.Hour)}
	recent := &session.Session{ID: "recent", Provider: "claude-code", Branch: "main", CreatedAt: now.Add(-24 * time.Hour)}

	store.allSessions = []*session.Session{old1, old2, recent}

	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.GarbageCollect(context.Background(), GCRequest{OlderThan: "30d"})
	if err != nil {
		t.Fatalf("GarbageCollect: %v", err)
	}

	if result.Deleted != 2 {
		t.Errorf("expected 2 deleted, got %d", result.Deleted)
	}
}

func TestGarbageCollect_dryRun(t *testing.T) {
	store := newGCMockStore()
	now := time.Now().UTC()

	old := &session.Session{ID: "old", Provider: "claude-code", Branch: "main", CreatedAt: now.Add(-40 * 24 * time.Hour)}
	recent := &session.Session{ID: "recent", Provider: "claude-code", Branch: "main", CreatedAt: now.Add(-24 * time.Hour)}
	store.allSessions = []*session.Session{old, recent}
	store.mockStore.sessions["old"] = old
	store.mockStore.sessions["recent"] = recent

	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.GarbageCollect(context.Background(), GCRequest{OlderThan: "30d", DryRun: true})
	if err != nil {
		t.Fatalf("GarbageCollect: %v", err)
	}

	if result.DryRun != true {
		t.Error("expected DryRun=true")
	}
	if result.Would != 1 {
		t.Errorf("expected 1 would-be-deleted, got %d", result.Would)
	}
	if result.Deleted != 0 {
		t.Errorf("expected 0 deleted in dry-run, got %d", result.Deleted)
	}
}

func TestGarbageCollect_keepLatest(t *testing.T) {
	store := newGCMockStore()
	now := time.Now().UTC()

	// 4 sessions on "main", 2 on "dev"
	for i := 0; i < 4; i++ {
		s := &session.Session{
			ID:        session.ID("main-" + string(rune('a'+i))),
			Provider:  "claude-code",
			Branch:    "main",
			CreatedAt: now.Add(-time.Duration(4-i) * 24 * time.Hour),
		}
		store.allSessions = append(store.allSessions, s)
	}
	for i := 0; i < 2; i++ {
		s := &session.Session{
			ID:        session.ID("dev-" + string(rune('a'+i))),
			Provider:  "claude-code",
			Branch:    "dev",
			CreatedAt: now.Add(-time.Duration(2-i) * 24 * time.Hour),
		}
		store.allSessions = append(store.allSessions, s)
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.GarbageCollect(context.Background(), GCRequest{KeepLatest: 2})
	if err != nil {
		t.Fatalf("GarbageCollect: %v", err)
	}

	// main: 4 sessions, keep 2 → delete 2
	// dev: 2 sessions, keep 2 → delete 0
	if result.Deleted != 2 {
		t.Errorf("expected 2 deleted, got %d", result.Deleted)
	}
}

func TestGarbageCollect_noPolicy(t *testing.T) {
	store := newGCMockStore()
	svc := NewSessionService(SessionServiceConfig{Store: store})

	_, err := svc.GarbageCollect(context.Background(), GCRequest{})
	if err == nil {
		t.Error("expected error when no policy is specified")
	}
}

// ── Diff tests ──

func TestDiff_basic(t *testing.T) {
	store := &mockStore{sessions: make(map[session.ID]*session.Session)}

	left := &session.Session{
		ID:       "left-1",
		Provider: "claude-code",
		Branch:   "main",
		Summary:  "Add auth",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "Add JWT auth"},
			{Role: session.RoleAssistant, Content: "I'll implement JWT...", Model: "claude-sonnet-4"},
			{Role: session.RoleUser, Content: "Looks good, add tests"},
			{Role: session.RoleAssistant, Content: "Adding tests...", Model: "claude-sonnet-4"},
		},
		TokenUsage: session.TokenUsage{InputTokens: 1000, OutputTokens: 2000, TotalTokens: 3000},
		FileChanges: []session.FileChange{
			{FilePath: "auth.go"},
			{FilePath: "auth_test.go"},
			{FilePath: "main.go"},
		},
	}
	right := &session.Session{
		ID:       "right-1",
		Provider: "opencode",
		Branch:   "feature/auth",
		Summary:  "Add auth v2",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "Add JWT auth"},
			{Role: session.RoleAssistant, Content: "I'll implement JWT...", Model: "gpt-4o"},
			{Role: session.RoleUser, Content: "Use sessions instead"},
			{Role: session.RoleAssistant, Content: "Switching to sessions...", Model: "gpt-4o"},
			{Role: session.RoleAssistant, Content: "Done with sessions", Model: "gpt-4o"},
		},
		TokenUsage: session.TokenUsage{InputTokens: 1500, OutputTokens: 3000, TotalTokens: 4500},
		FileChanges: []session.FileChange{
			{FilePath: "auth.go"},
			{FilePath: "session.go"},
			{FilePath: "main.go"},
		},
	}

	_ = store.Save(left)
	_ = store.Save(right)

	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.Diff(context.Background(), DiffRequest{LeftID: "left-1", RightID: "right-1"})
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}

	// Check sides
	if result.Left.ID != "left-1" {
		t.Errorf("Left.ID = %q, want %q", result.Left.ID, "left-1")
	}
	if result.Right.ID != "right-1" {
		t.Errorf("Right.ID = %q, want %q", result.Right.ID, "right-1")
	}
	if result.Left.MessageCount != 4 {
		t.Errorf("Left.MessageCount = %d, want 4", result.Left.MessageCount)
	}
	if result.Right.MessageCount != 5 {
		t.Errorf("Right.MessageCount = %d, want 5", result.Right.MessageCount)
	}

	// Token delta
	if result.TokenDelta.InputDelta != 500 {
		t.Errorf("TokenDelta.InputDelta = %d, want 500", result.TokenDelta.InputDelta)
	}
	if result.TokenDelta.OutputDelta != 1000 {
		t.Errorf("TokenDelta.OutputDelta = %d, want 1000", result.TokenDelta.OutputDelta)
	}
	if result.TokenDelta.TotalDelta != 1500 {
		t.Errorf("TokenDelta.TotalDelta = %d, want 1500", result.TokenDelta.TotalDelta)
	}

	// File diff
	if len(result.FileDiff.Shared) != 2 {
		t.Errorf("FileDiff.Shared count = %d, want 2", len(result.FileDiff.Shared))
	}
	if len(result.FileDiff.LeftOnly) != 1 || result.FileDiff.LeftOnly[0] != "auth_test.go" {
		t.Errorf("FileDiff.LeftOnly = %v, want [auth_test.go]", result.FileDiff.LeftOnly)
	}
	if len(result.FileDiff.RightOnly) != 1 || result.FileDiff.RightOnly[0] != "session.go" {
		t.Errorf("FileDiff.RightOnly = %v, want [session.go]", result.FileDiff.RightOnly)
	}

	// Message delta: first 2 messages share same role+content, then diverge
	if result.MessageDelta.CommonPrefix != 2 {
		t.Errorf("MessageDelta.CommonPrefix = %d, want 2", result.MessageDelta.CommonPrefix)
	}
	if result.MessageDelta.LeftAfter != 2 {
		t.Errorf("MessageDelta.LeftAfter = %d, want 2", result.MessageDelta.LeftAfter)
	}
	if result.MessageDelta.RightAfter != 3 {
		t.Errorf("MessageDelta.RightAfter = %d, want 3", result.MessageDelta.RightAfter)
	}
}

func TestDiff_identicalSessions(t *testing.T) {
	store := &mockStore{sessions: make(map[session.ID]*session.Session)}

	sess := &session.Session{
		ID:       "sess-a",
		Provider: "claude-code",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "hello"},
			{Role: session.RoleAssistant, Content: "hi there"},
		},
		TokenUsage:  session.TokenUsage{InputTokens: 100, OutputTokens: 200, TotalTokens: 300},
		FileChanges: []session.FileChange{{FilePath: "main.go"}},
	}

	// Save same session under two IDs
	_ = store.Save(sess)
	copy := *sess
	copy.ID = "sess-b"
	_ = store.Save(&copy)

	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.Diff(context.Background(), DiffRequest{LeftID: "sess-a", RightID: "sess-b"})
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}

	if result.TokenDelta.TotalDelta != 0 {
		t.Errorf("TokenDelta.TotalDelta = %d, want 0", result.TokenDelta.TotalDelta)
	}
	if result.MessageDelta.CommonPrefix != 2 {
		t.Errorf("CommonPrefix = %d, want 2", result.MessageDelta.CommonPrefix)
	}
	if result.MessageDelta.LeftAfter != 0 || result.MessageDelta.RightAfter != 0 {
		t.Errorf("LeftAfter=%d, RightAfter=%d, want 0,0", result.MessageDelta.LeftAfter, result.MessageDelta.RightAfter)
	}
	if len(result.FileDiff.Shared) != 1 {
		t.Errorf("Shared = %d, want 1", len(result.FileDiff.Shared))
	}
	if len(result.FileDiff.LeftOnly) != 0 || len(result.FileDiff.RightOnly) != 0 {
		t.Error("expected no LeftOnly/RightOnly files for identical sessions")
	}
}

func TestDiff_emptyMessages(t *testing.T) {
	store := &mockStore{sessions: make(map[session.ID]*session.Session)}
	_ = store.Save(&session.Session{ID: "empty-a", Provider: "claude-code"})
	_ = store.Save(&session.Session{ID: "empty-b", Provider: "opencode"})

	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.Diff(context.Background(), DiffRequest{LeftID: "empty-a", RightID: "empty-b"})
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if result.MessageDelta.CommonPrefix != 0 {
		t.Errorf("CommonPrefix = %d, want 0", result.MessageDelta.CommonPrefix)
	}
}

func TestDiff_toolDiff(t *testing.T) {
	store := &mockStore{sessions: make(map[session.ID]*session.Session)}

	left := &session.Session{
		ID:       "tool-left",
		Provider: "claude-code",
		Messages: []session.Message{
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "Read", State: session.ToolStateCompleted},
				{Name: "Read", State: session.ToolStateCompleted},
				{Name: "Write", State: session.ToolStateCompleted},
			}},
		},
	}
	right := &session.Session{
		ID:       "tool-right",
		Provider: "claude-code",
		Messages: []session.Message{
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "Read", State: session.ToolStateCompleted},
				{Name: "Bash", State: session.ToolStateCompleted},
				{Name: "Bash", State: session.ToolStateCompleted},
				{Name: "Bash", State: session.ToolStateCompleted},
			}},
		},
	}

	_ = store.Save(left)
	_ = store.Save(right)

	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.Diff(context.Background(), DiffRequest{LeftID: "tool-left", RightID: "tool-right"})
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}

	// Should have 3 tools: Bash, Read, Write (sorted alphabetically)
	if len(result.ToolDiff.Entries) != 3 {
		t.Fatalf("ToolDiff.Entries count = %d, want 3", len(result.ToolDiff.Entries))
	}

	// Bash: 0 left, 3 right
	bash := result.ToolDiff.Entries[0]
	if bash.Name != "Bash" || bash.LeftCalls != 0 || bash.RightCalls != 3 || bash.CallsDelta != 3 {
		t.Errorf("Bash entry = %+v, want {Bash 0 3 3}", bash)
	}

	// Read: 2 left, 1 right
	read := result.ToolDiff.Entries[1]
	if read.Name != "Read" || read.LeftCalls != 2 || read.RightCalls != 1 || read.CallsDelta != -1 {
		t.Errorf("Read entry = %+v, want {Read 2 1 -1}", read)
	}

	// Write: 1 left, 0 right
	write := result.ToolDiff.Entries[2]
	if write.Name != "Write" || write.LeftCalls != 1 || write.RightCalls != 0 || write.CallsDelta != -1 {
		t.Errorf("Write entry = %+v, want {Write 1 0 -1}", write)
	}
}

func TestDiff_missingSession(t *testing.T) {
	store := &mockStore{sessions: make(map[session.ID]*session.Session)}
	_ = store.Save(&session.Session{ID: "exists", Provider: "claude-code"})

	svc := NewSessionService(SessionServiceConfig{Store: store})

	// Missing right
	_, err := svc.Diff(context.Background(), DiffRequest{LeftID: "exists", RightID: "missing"})
	if err == nil {
		t.Error("expected error for missing right session")
	}

	// Missing left
	_, err = svc.Diff(context.Background(), DiffRequest{LeftID: "missing", RightID: "exists"})
	if err == nil {
		t.Error("expected error for missing left session")
	}
}

func TestDiff_emptyIDs(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{
		Store: &mockStore{sessions: make(map[session.ID]*session.Session)},
	})

	_, err := svc.Diff(context.Background(), DiffRequest{LeftID: "", RightID: "any"})
	if err == nil {
		t.Error("expected error for empty left ID")
	}

	_, err = svc.Diff(context.Background(), DiffRequest{LeftID: "any", RightID: ""})
	if err == nil {
		t.Error("expected error for empty right ID")
	}
}

// ── BuildTree tests ──

func TestBuildTree_noParents(t *testing.T) {
	summaries := []session.Summary{
		{ID: "a", Provider: "claude-code", Branch: "main"},
		{ID: "b", Provider: "opencode", Branch: "main"},
	}

	tree := buildTree(summaries)
	if len(tree) != 2 {
		t.Fatalf("expected 2 roots, got %d", len(tree))
	}
	if tree[0].Summary.ID != "a" || tree[1].Summary.ID != "b" {
		t.Errorf("unexpected root order: %s, %s", tree[0].Summary.ID, tree[1].Summary.ID)
	}
}

func TestBuildTree_parentChild(t *testing.T) {
	summaries := []session.Summary{
		{ID: "root", Provider: "claude-code", Branch: "main"},
		{ID: "child1", Provider: "claude-code", Branch: "main", ParentID: "root"},
		{ID: "child2", Provider: "claude-code", Branch: "main", ParentID: "root"},
		{ID: "grandchild", Provider: "claude-code", Branch: "main", ParentID: "child1"},
	}

	tree := buildTree(summaries)
	if len(tree) != 1 {
		t.Fatalf("expected 1 root, got %d", len(tree))
	}
	root := tree[0]
	if root.Summary.ID != "root" {
		t.Errorf("root ID = %q, want %q", root.Summary.ID, "root")
	}
	if len(root.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(root.Children))
	}
	if !root.Children[0].IsFork || !root.Children[1].IsFork {
		t.Error("expected children to be marked as forks")
	}

	// child1 should have grandchild
	child1 := root.Children[0]
	if child1.Summary.ID != "child1" {
		t.Errorf("child1 ID = %q, want %q", child1.Summary.ID, "child1")
	}
	if len(child1.Children) != 1 {
		t.Fatalf("expected 1 grandchild, got %d", len(child1.Children))
	}
	if child1.Children[0].Summary.ID != "grandchild" {
		t.Errorf("grandchild ID = %q, want %q", child1.Children[0].Summary.ID, "grandchild")
	}
}

func TestBuildTree_orphanParent(t *testing.T) {
	// Child has a ParentID that's not in the result set → treated as root.
	summaries := []session.Summary{
		{ID: "orphan", Provider: "claude-code", Branch: "main", ParentID: "missing-parent"},
	}

	tree := buildTree(summaries)
	if len(tree) != 1 {
		t.Fatalf("expected 1 root, got %d", len(tree))
	}
	if tree[0].Summary.ID != "orphan" {
		t.Errorf("root ID = %q, want %q", tree[0].Summary.ID, "orphan")
	}
	if tree[0].IsFork {
		t.Error("orphan should not be marked as fork")
	}
}

func TestBuildTree_empty(t *testing.T) {
	tree := buildTree(nil)
	if len(tree) != 0 {
		t.Errorf("expected 0 roots, got %d", len(tree))
	}
}

// ── Off-Topic Detection tests ──

// offTopicMockStore supports List (filtered by branch) and Get (with FileChanges).
type offTopicMockStore struct {
	mockStore
}

func newOffTopicStore(sessions ...*session.Session) *offTopicMockStore {
	store := &offTopicMockStore{
		mockStore: mockStore{sessions: make(map[session.ID]*session.Session)},
	}
	for _, s := range sessions {
		store.sessions[s.ID] = s
	}
	return store
}

func (m *offTopicMockStore) List(opts session.ListOptions) ([]session.Summary, error) {
	var result []session.Summary
	for _, s := range m.sessions {
		if opts.Branch != "" && s.Branch != opts.Branch {
			continue
		}
		result = append(result, session.Summary{
			ID:          s.ID,
			Provider:    s.Provider,
			Branch:      s.Branch,
			Summary:     s.Summary,
			TotalTokens: s.TokenUsage.TotalTokens,
			CreatedAt:   s.CreatedAt,
		})
	}
	// Sort by ID for deterministic test output
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func TestDetectOffTopic_twoOverlapping(t *testing.T) {
	// Two sessions sharing files → neither is off-topic
	s1 := &session.Session{
		ID: "s1", Provider: "claude-code", Branch: "feat",
		FileChanges: []session.FileChange{
			{FilePath: "auth.go"}, {FilePath: "main.go"},
		},
	}
	s2 := &session.Session{
		ID: "s2", Provider: "claude-code", Branch: "feat",
		FileChanges: []session.FileChange{
			{FilePath: "auth.go"}, {FilePath: "auth_test.go"},
		},
	}

	store := newOffTopicStore(s1, s2)
	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.DetectOffTopic(context.Background(), OffTopicRequest{Branch: "feat"})
	if err != nil {
		t.Fatalf("DetectOffTopic: %v", err)
	}

	if result.Total != 2 {
		t.Errorf("Total = %d, want 2", result.Total)
	}
	if result.OffTopic != 0 {
		t.Errorf("OffTopic = %d, want 0", result.OffTopic)
	}

	// s1: auth.go is shared → 1/2 = 0.5 overlap
	// s2: auth.go is shared → 1/2 = 0.5 overlap
	for _, entry := range result.Sessions {
		if entry.Overlap < 0.2 {
			t.Errorf("session %s overlap = %.2f, expected >= 0.2", entry.ID, entry.Overlap)
		}
		if entry.IsOffTopic {
			t.Errorf("session %s should not be off-topic", entry.ID)
		}
	}
}

func TestDetectOffTopic_oneOffTopic(t *testing.T) {
	// Three sessions: s1 and s2 share files, s3 touches completely different files
	s1 := &session.Session{
		ID: "s1", Provider: "claude-code", Branch: "feat",
		FileChanges: []session.FileChange{
			{FilePath: "auth.go"}, {FilePath: "main.go"}, {FilePath: "config.go"},
		},
	}
	s2 := &session.Session{
		ID: "s2", Provider: "claude-code", Branch: "feat",
		FileChanges: []session.FileChange{
			{FilePath: "auth.go"}, {FilePath: "main.go"},
		},
	}
	s3 := &session.Session{
		ID: "s3", Provider: "opencode", Branch: "feat",
		FileChanges: []session.FileChange{
			{FilePath: "readme.md"}, {FilePath: "docs/api.md"},
		},
	}

	store := newOffTopicStore(s1, s2, s3)
	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.DetectOffTopic(context.Background(), OffTopicRequest{Branch: "feat"})
	if err != nil {
		t.Fatalf("DetectOffTopic: %v", err)
	}

	if result.OffTopic != 1 {
		t.Errorf("OffTopic = %d, want 1", result.OffTopic)
	}

	// s3 has 0% overlap → off-topic
	for _, entry := range result.Sessions {
		if entry.ID == "s3" {
			if !entry.IsOffTopic {
				t.Errorf("session s3 should be off-topic (overlap=%.2f)", entry.Overlap)
			}
			if entry.Overlap != 0.0 {
				t.Errorf("s3 overlap = %.2f, want 0.0", entry.Overlap)
			}
		}
	}
}

func TestDetectOffTopic_singleSession(t *testing.T) {
	// With only 1 session, nothing can be off-topic
	s := &session.Session{
		ID: "solo", Provider: "claude-code", Branch: "feat",
		FileChanges: []session.FileChange{{FilePath: "main.go"}},
	}

	store := newOffTopicStore(s)
	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.DetectOffTopic(context.Background(), OffTopicRequest{Branch: "feat"})
	if err != nil {
		t.Fatalf("DetectOffTopic: %v", err)
	}

	if result.Total != 1 {
		t.Errorf("Total = %d, want 1", result.Total)
	}
	if result.OffTopic != 0 {
		t.Errorf("OffTopic = %d, want 0", result.OffTopic)
	}
	if result.Sessions[0].Overlap != 1.0 {
		t.Errorf("single session overlap = %.2f, want 1.0", result.Sessions[0].Overlap)
	}
}

func TestDetectOffTopic_noFileChanges(t *testing.T) {
	// Sessions without file changes should not be flagged
	s1 := &session.Session{
		ID: "s1", Provider: "claude-code", Branch: "feat",
		FileChanges: []session.FileChange{{FilePath: "main.go"}},
	}
	s2 := &session.Session{
		ID: "s2", Provider: "claude-code", Branch: "feat",
		// No file changes
	}

	store := newOffTopicStore(s1, s2)
	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.DetectOffTopic(context.Background(), OffTopicRequest{Branch: "feat"})
	if err != nil {
		t.Fatalf("DetectOffTopic: %v", err)
	}

	// s2 has no files → overlap = 1.0 (not off-topic)
	for _, entry := range result.Sessions {
		if entry.ID == "s2" {
			if entry.IsOffTopic {
				t.Errorf("session with no files should not be off-topic")
			}
			if entry.Overlap != 1.0 {
				t.Errorf("empty session overlap = %.2f, want 1.0", entry.Overlap)
			}
		}
	}
}

func TestDetectOffTopic_branchRequired(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{
		Store: &mockStore{sessions: make(map[session.ID]*session.Session)},
	})

	_, err := svc.DetectOffTopic(context.Background(), OffTopicRequest{})
	if err == nil {
		t.Error("expected error when branch is empty")
	}
}

func TestDetectOffTopic_topFiles(t *testing.T) {
	// Verify top files are sorted by frequency
	s1 := &session.Session{
		ID: "s1", Provider: "claude-code", Branch: "feat",
		FileChanges: []session.FileChange{
			{FilePath: "a.go"}, {FilePath: "b.go"}, {FilePath: "c.go"},
		},
	}
	s2 := &session.Session{
		ID: "s2", Provider: "claude-code", Branch: "feat",
		FileChanges: []session.FileChange{
			{FilePath: "a.go"}, {FilePath: "b.go"},
		},
	}
	s3 := &session.Session{
		ID: "s3", Provider: "claude-code", Branch: "feat",
		FileChanges: []session.FileChange{
			{FilePath: "a.go"},
		},
	}

	store := newOffTopicStore(s1, s2, s3)
	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.DetectOffTopic(context.Background(), OffTopicRequest{Branch: "feat"})
	if err != nil {
		t.Fatalf("DetectOffTopic: %v", err)
	}

	if len(result.TopFiles) < 3 {
		t.Fatalf("TopFiles count = %d, want >= 3", len(result.TopFiles))
	}
	// a.go appears in 3 sessions, b.go in 2, c.go in 1
	if result.TopFiles[0] != "a.go" {
		t.Errorf("TopFiles[0] = %q, want %q", result.TopFiles[0], "a.go")
	}
	if result.TopFiles[1] != "b.go" {
		t.Errorf("TopFiles[1] = %q, want %q", result.TopFiles[1], "b.go")
	}
	if result.TopFiles[2] != "c.go" {
		t.Errorf("TopFiles[2] = %q, want %q", result.TopFiles[2], "c.go")
	}
}

func TestDetectOffTopic_customThreshold(t *testing.T) {
	// With a very high threshold (0.9), more sessions should be flagged
	s1 := &session.Session{
		ID: "s1", Provider: "claude-code", Branch: "feat",
		FileChanges: []session.FileChange{
			{FilePath: "a.go"}, {FilePath: "b.go"}, {FilePath: "unique1.go"},
		},
	}
	s2 := &session.Session{
		ID: "s2", Provider: "claude-code", Branch: "feat",
		FileChanges: []session.FileChange{
			{FilePath: "a.go"}, {FilePath: "b.go"}, {FilePath: "unique2.go"},
		},
	}

	store := newOffTopicStore(s1, s2)
	svc := NewSessionService(SessionServiceConfig{Store: store})

	// Default threshold (0.2): 2/3 overlap → not off-topic
	result, err := svc.DetectOffTopic(context.Background(), OffTopicRequest{Branch: "feat"})
	if err != nil {
		t.Fatalf("DetectOffTopic: %v", err)
	}
	if result.OffTopic != 0 {
		t.Errorf("default threshold: OffTopic = %d, want 0", result.OffTopic)
	}

	// High threshold (0.9): 2/3 overlap ≈ 0.67 < 0.9 → both off-topic
	result, err = svc.DetectOffTopic(context.Background(), OffTopicRequest{Branch: "feat", Threshold: 0.9})
	if err != nil {
		t.Fatalf("DetectOffTopic with high threshold: %v", err)
	}
	if result.OffTopic != 2 {
		t.Errorf("high threshold: OffTopic = %d, want 2", result.OffTopic)
	}
}

// ── Forecast tests ──

func TestForecast_basic(t *testing.T) {
	now := time.Now().UTC()
	// 3 sessions spread over the last 30 days, using claude-sonnet-4
	s1 := &session.Session{
		ID: "f1", Provider: "claude-code", Branch: "main",
		CreatedAt:  now.Add(-25 * 24 * time.Hour),
		TokenUsage: session.TokenUsage{InputTokens: 5000, OutputTokens: 10000, TotalTokens: 15000},
		Messages: []session.Message{
			{Role: session.RoleAssistant, Model: "claude-sonnet-4", InputTokens: 5000, OutputTokens: 10000},
		},
	}
	s2 := &session.Session{
		ID: "f2", Provider: "claude-code", Branch: "main",
		CreatedAt:  now.Add(-15 * 24 * time.Hour),
		TokenUsage: session.TokenUsage{InputTokens: 8000, OutputTokens: 20000, TotalTokens: 28000},
		Messages: []session.Message{
			{Role: session.RoleAssistant, Model: "claude-sonnet-4", InputTokens: 8000, OutputTokens: 20000},
		},
	}
	s3 := &session.Session{
		ID: "f3", Provider: "claude-code", Branch: "main",
		CreatedAt:  now.Add(-5 * 24 * time.Hour),
		TokenUsage: session.TokenUsage{InputTokens: 3000, OutputTokens: 7000, TotalTokens: 10000},
		Messages: []session.Message{
			{Role: session.RoleAssistant, Model: "claude-sonnet-4", InputTokens: 3000, OutputTokens: 7000},
		},
	}

	store := newOffTopicStore(s1, s2, s3)
	svc := NewSessionService(SessionServiceConfig{Store: store, Pricing: pricing.NewCalculator()})

	result, err := svc.Forecast(context.Background(), ForecastRequest{Days: 90})
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}

	if result.SessionCount != 3 {
		t.Errorf("SessionCount = %d, want 3", result.SessionCount)
	}
	if result.TotalCost <= 0 {
		t.Errorf("TotalCost = %.4f, want > 0", result.TotalCost)
	}
	if result.Projected30d < 0 {
		t.Errorf("Projected30d = %.4f, want >= 0", result.Projected30d)
	}
	if result.Projected90d < 0 {
		t.Errorf("Projected90d = %.4f, want >= 0", result.Projected90d)
	}
	if result.Period != "weekly" {
		t.Errorf("Period = %q, want %q", result.Period, "weekly")
	}

	// Should have model breakdown
	if len(result.ModelBreakdown) != 1 {
		t.Fatalf("ModelBreakdown count = %d, want 1", len(result.ModelBreakdown))
	}
	if result.ModelBreakdown[0].Model != "claude-sonnet-4" {
		t.Errorf("ModelBreakdown[0].Model = %q, want %q", result.ModelBreakdown[0].Model, "claude-sonnet-4")
	}
}

func TestForecast_noSessions(t *testing.T) {
	store := newOffTopicStore() // empty
	svc := NewSessionService(SessionServiceConfig{Store: store, Pricing: pricing.NewCalculator()})

	result, err := svc.Forecast(context.Background(), ForecastRequest{Days: 30})
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}

	if result.SessionCount != 0 {
		t.Errorf("SessionCount = %d, want 0", result.SessionCount)
	}
	if result.TrendDir != "stable" {
		t.Errorf("TrendDir = %q, want %q", result.TrendDir, "stable")
	}
}

func TestForecast_invalidPeriod(t *testing.T) {
	store := newOffTopicStore()
	svc := NewSessionService(SessionServiceConfig{Store: store, Pricing: pricing.NewCalculator()})

	_, err := svc.Forecast(context.Background(), ForecastRequest{Period: "monthly"})
	if err == nil {
		t.Error("expected error for invalid period")
	}
}

func TestForecast_dailyPeriod(t *testing.T) {
	now := time.Now().UTC()
	s := &session.Session{
		ID: "d1", Provider: "claude-code", Branch: "main",
		CreatedAt:  now.Add(-3 * 24 * time.Hour),
		TokenUsage: session.TokenUsage{InputTokens: 1000, OutputTokens: 2000, TotalTokens: 3000},
		Messages: []session.Message{
			{Role: session.RoleAssistant, Model: "claude-sonnet-4", InputTokens: 1000, OutputTokens: 2000},
		},
	}

	store := newOffTopicStore(s)
	svc := NewSessionService(SessionServiceConfig{Store: store, Pricing: pricing.NewCalculator()})

	result, err := svc.Forecast(context.Background(), ForecastRequest{Period: "daily", Days: 30})
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if result.Period != "daily" {
		t.Errorf("Period = %q, want %q", result.Period, "daily")
	}
	if len(result.Buckets) == 0 {
		t.Error("expected at least 1 bucket for daily period")
	}
}

func TestForecast_modelRecommendation(t *testing.T) {
	now := time.Now().UTC()
	// Using expensive claude-opus-4 model
	s := &session.Session{
		ID: "exp1", Provider: "claude-code", Branch: "main",
		CreatedAt:  now.Add(-10 * 24 * time.Hour),
		TokenUsage: session.TokenUsage{InputTokens: 10000, OutputTokens: 20000, TotalTokens: 30000},
		Messages: []session.Message{
			{Role: session.RoleAssistant, Model: "claude-opus-4", InputTokens: 10000, OutputTokens: 20000},
		},
	}

	store := newOffTopicStore(s)
	svc := NewSessionService(SessionServiceConfig{Store: store, Pricing: pricing.NewCalculator()})

	result, err := svc.Forecast(context.Background(), ForecastRequest{Days: 30})
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}

	if len(result.ModelBreakdown) != 1 {
		t.Fatalf("ModelBreakdown count = %d, want 1", len(result.ModelBreakdown))
	}

	// opus-4 should get a recommendation to switch to sonnet-4
	rec := result.ModelBreakdown[0].Recommendation
	if rec == "" {
		t.Error("expected recommendation for claude-opus-4 to switch to cheaper model")
	}
	if result.ModelBreakdown[0].Share != 100.0 {
		t.Errorf("Share = %.1f, want 100.0", result.ModelBreakdown[0].Share)
	}
}

func TestForecast_multiModel(t *testing.T) {
	now := time.Now().UTC()
	s := &session.Session{
		ID: "multi", Provider: "claude-code", Branch: "main",
		CreatedAt:  now.Add(-10 * 24 * time.Hour),
		TokenUsage: session.TokenUsage{InputTokens: 20000, OutputTokens: 40000, TotalTokens: 60000},
		Messages: []session.Message{
			{Role: session.RoleAssistant, Model: "claude-opus-4", InputTokens: 10000, OutputTokens: 20000},
			{Role: session.RoleAssistant, Model: "claude-sonnet-4", InputTokens: 10000, OutputTokens: 20000},
		},
	}

	store := newOffTopicStore(s)
	svc := NewSessionService(SessionServiceConfig{Store: store, Pricing: pricing.NewCalculator()})

	result, err := svc.Forecast(context.Background(), ForecastRequest{Days: 30})
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}

	if len(result.ModelBreakdown) != 2 {
		t.Fatalf("ModelBreakdown count = %d, want 2", len(result.ModelBreakdown))
	}

	// Most expensive (opus) should be first
	if result.ModelBreakdown[0].Model != "claude-opus-4" {
		t.Errorf("ModelBreakdown[0].Model = %q, want %q (most expensive first)", result.ModelBreakdown[0].Model, "claude-opus-4")
	}
}

func TestComputeTrend(t *testing.T) {
	// Steadily increasing costs
	buckets := []session.CostBucket{
		{Cost: 1.0}, {Cost: 2.0}, {Cost: 3.0}, {Cost: 4.0},
	}
	_, dir := computeTrend(buckets, 7*24*time.Hour)
	if dir != "increasing" {
		t.Errorf("trend dir = %q, want %q", dir, "increasing")
	}

	// Steadily decreasing
	buckets = []session.CostBucket{
		{Cost: 4.0}, {Cost: 3.0}, {Cost: 2.0}, {Cost: 1.0},
	}
	_, dir = computeTrend(buckets, 7*24*time.Hour)
	if dir != "decreasing" {
		t.Errorf("trend dir = %q, want %q", dir, "decreasing")
	}

	// Flat
	buckets = []session.CostBucket{
		{Cost: 2.0}, {Cost: 2.0}, {Cost: 2.0}, {Cost: 2.0},
	}
	_, dir = computeTrend(buckets, 7*24*time.Hour)
	if dir != "stable" {
		t.Errorf("trend dir = %q, want %q", dir, "stable")
	}

	// Single bucket
	_, dir = computeTrend([]session.CostBucket{{Cost: 5.0}}, 7*24*time.Hour)
	if dir != "stable" {
		t.Errorf("single bucket trend = %q, want %q", dir, "stable")
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input   string
		wantHrs float64
		wantErr bool
	}{
		{"30d", 720, false},
		{"7d", 168, false},
		{"24h", 24, false},
		{"1d12h", 36, false},
		{"2h30m", 2.5, false},
		{"", 0, true},
		{"abc", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			dur, err := parseDuration(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			gotHrs := dur.Hours()
			if gotHrs != tt.wantHrs {
				t.Errorf("expected %.1f hours, got %.1f", tt.wantHrs, gotHrs)
			}
		})
	}
}

// ── Ingest Tests ──

func newIngestService(t *testing.T) (*SessionService, *mockStore) {
	t.Helper()
	store := &mockStore{sessions: make(map[session.ID]*session.Session)}
	svc := NewSessionService(SessionServiceConfig{Store: store})
	return svc, store
}

func TestIngest_MinimalPayload(t *testing.T) {
	svc, store := newIngestService(t)

	result, err := svc.Ingest(context.Background(), IngestRequest{
		Provider: "parlay",
		Messages: []IngestMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there!"},
		},
	})
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	if result.SessionID == "" {
		t.Fatal("expected non-empty session ID")
	}
	if result.Provider != session.ProviderParlay {
		t.Errorf("provider = %q, want %q", result.Provider, session.ProviderParlay)
	}

	// Verify stored.
	sess, ok := store.sessions[result.SessionID]
	if !ok {
		t.Fatal("session not found in store")
	}
	if len(sess.Messages) != 2 {
		t.Errorf("messages count = %d, want 2", len(sess.Messages))
	}
	if sess.Agent != "parlay" {
		t.Errorf("agent = %q, want %q (defaulted to provider)", sess.Agent, "parlay")
	}
}

func TestIngest_MissingProvider(t *testing.T) {
	svc, _ := newIngestService(t)

	_, err := svc.Ingest(context.Background(), IngestRequest{
		Messages: []IngestMessage{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error for missing provider")
	}
}

func TestIngest_MissingMessages(t *testing.T) {
	svc, _ := newIngestService(t)

	_, err := svc.Ingest(context.Background(), IngestRequest{
		Provider: "ollama",
	})
	if err == nil {
		t.Fatal("expected error for missing messages")
	}
}

func TestIngest_InvalidProvider(t *testing.T) {
	svc, _ := newIngestService(t)

	_, err := svc.Ingest(context.Background(), IngestRequest{
		Provider: "unknown-tool",
		Messages: []IngestMessage{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error for invalid provider")
	}
}

func TestIngest_ComputesTokens(t *testing.T) {
	svc, store := newIngestService(t)

	result, err := svc.Ingest(context.Background(), IngestRequest{
		Provider: "ollama",
		Messages: []IngestMessage{
			{Role: "user", Content: "Hello", InputTokens: 100},
			{Role: "assistant", Content: "Hi", OutputTokens: 50},
			{Role: "user", Content: "Bye", InputTokens: 30},
		},
	})
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}

	sess := store.sessions[result.SessionID]
	if sess.TokenUsage.InputTokens != 130 {
		t.Errorf("InputTokens = %d, want 130", sess.TokenUsage.InputTokens)
	}
	if sess.TokenUsage.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50", sess.TokenUsage.OutputTokens)
	}
	if sess.TokenUsage.TotalTokens != 180 {
		t.Errorf("TotalTokens = %d, want 180", sess.TokenUsage.TotalTokens)
	}
}

func TestIngest_ComputesToolCounts(t *testing.T) {
	svc, store := newIngestService(t)

	result, err := svc.Ingest(context.Background(), IngestRequest{
		Provider: "parlay",
		Messages: []IngestMessage{
			{Role: "assistant", Content: "Running...", ToolCalls: []IngestToolCall{
				{Name: "bash", Input: "ls", Output: "file.txt", State: "completed"},
				{Name: "bash", Input: "rm bad", State: "error"},
			}},
			{Role: "assistant", Content: "Done", ToolCalls: []IngestToolCall{
				{Name: "memory", Input: "save", State: "completed"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}

	sess := store.sessions[result.SessionID]
	// 3 total tool calls, 1 error
	totalTC := 0
	totalErr := 0
	for _, msg := range sess.Messages {
		totalTC += len(msg.ToolCalls)
		for _, tc := range msg.ToolCalls {
			if tc.State == session.ToolStateError {
				totalErr++
			}
		}
	}
	if totalTC != 3 {
		t.Errorf("tool call count = %d, want 3", totalTC)
	}
	if totalErr != 1 {
		t.Errorf("error count = %d, want 1", totalErr)
	}
}

func TestIngest_AutoGeneratesID(t *testing.T) {
	svc, _ := newIngestService(t)

	result, err := svc.Ingest(context.Background(), IngestRequest{
		Provider: "parlay",
		Messages: []IngestMessage{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	if result.SessionID == "" {
		t.Fatal("expected auto-generated session ID")
	}
}

func TestIngest_PreservesExplicitID(t *testing.T) {
	svc, _ := newIngestService(t)

	result, err := svc.Ingest(context.Background(), IngestRequest{
		Provider:  "parlay",
		SessionID: "my-custom-id-123",
		Messages:  []IngestMessage{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	if string(result.SessionID) != "my-custom-id-123" {
		t.Errorf("session ID = %q, want %q", result.SessionID, "my-custom-id-123")
	}
}

func TestIngest_SetsDefaultToolState(t *testing.T) {
	svc, store := newIngestService(t)

	result, err := svc.Ingest(context.Background(), IngestRequest{
		Provider: "ollama",
		Messages: []IngestMessage{
			{Role: "assistant", Content: "ok", ToolCalls: []IngestToolCall{
				{Name: "bash", Input: "echo hi"}, // no State → should default to "completed"
			}},
		},
	})
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}

	sess := store.sessions[result.SessionID]
	tc := sess.Messages[0].ToolCalls[0]
	if tc.State != session.ToolStateCompleted {
		t.Errorf("tool state = %q, want %q", tc.State, session.ToolStateCompleted)
	}
}

// ── Delegate Detection Tests ──

func TestIngest_ExplicitDelegatedFrom_CreatesLink(t *testing.T) {
	svc, store := newIngestService(t)

	// First, plant a "parent" session directly in the store.
	parentID := session.ID("parent-session-001")
	store.sessions[parentID] = &session.Session{ID: parentID}

	result, err := svc.Ingest(context.Background(), IngestRequest{
		Provider:               "parlay",
		SessionID:              "child-session-001",
		DelegatedFromSessionID: string(parentID),
		Messages: []IngestMessage{
			{Role: "user", Content: "do sub-task"},
		},
	})
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}

	// Child should have a delegated_from link pointing to parent.
	links := store.sessionLinks[result.SessionID]
	if len(links) == 0 {
		t.Fatal("expected at least one link, got none")
	}
	found := false
	for _, l := range links {
		if l.LinkType == session.SessionLinkDelegatedFrom && l.TargetSessionID == parentID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected delegated_from link to parent %q, links = %+v", parentID, links)
	}
}

func TestIngest_HeuristicDelegation_ToolCallWithSessionID(t *testing.T) {
	svc, store := newIngestService(t)

	targetID := session.ID("sub-agent-session-xyz")
	store.sessions[targetID] = &session.Session{ID: targetID}

	result, err := svc.Ingest(context.Background(), IngestRequest{
		Provider:  "parlay",
		SessionID: "orchestrator-001",
		Messages: []IngestMessage{
			{
				Role:    "assistant",
				Content: "delegating auth work",
				ToolCalls: []IngestToolCall{
					{
						Name:  "delegate",
						Input: `{"session_id":"sub-agent-session-xyz","task":"implement auth"}`,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}

	// Should have a delegated_to link to the sub-agent session.
	links := store.sessionLinks[result.SessionID]
	found := false
	for _, l := range links {
		if l.LinkType == session.SessionLinkDelegatedTo && l.TargetSessionID == targetID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected delegated_to link to %q, links = %+v", targetID, links)
	}
}

func TestIngest_HeuristicDelegation_UnknownTool_NoLink(t *testing.T) {
	svc, store := newIngestService(t)

	_, err := svc.Ingest(context.Background(), IngestRequest{
		Provider: "parlay",
		Messages: []IngestMessage{
			{
				Role:    "assistant",
				Content: "using bash",
				ToolCalls: []IngestToolCall{
					{
						Name:  "bash",
						Input: `{"session_id":"some-other-session"}`,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}

	// "bash" is not a delegate tool — no link should be created.
	total := 0
	for _, links := range store.sessionLinks {
		total += len(links)
	}
	if total != 0 {
		t.Errorf("expected no links for non-delegate tool, got %d", total)
	}
}

func TestIngest_HeuristicDelegation_SelfLink_Skipped(t *testing.T) {
	svc, store := newIngestService(t)

	result, err := svc.Ingest(context.Background(), IngestRequest{
		Provider:  "parlay",
		SessionID: "self-session",
		Messages: []IngestMessage{
			{
				Role: "assistant",
				ToolCalls: []IngestToolCall{
					{
						// Points to itself — should be silently ignored.
						Name:  "delegate",
						Input: `{"session_id":"self-session"}`,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}

	links := store.sessionLinks[result.SessionID]
	if len(links) != 0 {
		t.Errorf("expected no self-link, got %d links", len(links))
	}
}

func TestExtractSessionID(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`{"session_id":"abc123"}`, "abc123"},
		{`{"sessionId":"xyz"}`, "xyz"},
		{`{"session-id":"hyphen"}`, "hyphen"},
		{`{"other_key":"value"}`, ""},
		{`not json`, ""},
		{``, ""},
		{`{"session_id":""}`, ""},
	}
	for _, tc := range cases {
		got := extractSessionID(tc.raw)
		if got != tc.want {
			t.Errorf("extractSessionID(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// ── NormalizeRemoteURL Tests ──

func TestNormalizeRemoteURL(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		// HTTPS format
		{"https://github.com/org/repo.git", "github.com/org/repo"},
		{"https://github.com/org/repo", "github.com/org/repo"},
		{"http://github.com/org/repo.git", "github.com/org/repo"},
		// SSH format
		{"git@github.com:org/repo.git", "github.com/org/repo"},
		{"git@github.com:org/repo", "github.com/org/repo"},
		{"git@gitlab.com:team/project.git", "gitlab.com/team/project"},
		// ssh:// format
		{"ssh://git@github.com/org/repo.git", "github.com/org/repo"},
		// Edge cases
		{"", ""},
		{"  ", ""},
		{"https://github.com/org/repo.git/", "github.com/org/repo"},
	}
	for _, tc := range cases {
		got := NormalizeRemoteURL(tc.raw)
		if got != tc.want {
			t.Errorf("NormalizeRemoteURL(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// ── Voice Search Tests ──

func TestSearch_VoiceDefaultsLimitTo5(t *testing.T) {
	store := &mockStore{
		sessions: make(map[session.ID]*session.Session),
		searchResult: &session.SearchResult{
			Sessions:   make([]session.Summary, 10),
			TotalCount: 10,
			Limit:      5,
		},
	}
	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.Search(SearchRequest{Voice: true})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	// The service should have set limit=5 (the mock returns it back).
	if result.Limit != 5 {
		t.Errorf("expected limit=5 for voice mode, got %d", result.Limit)
	}
}

func TestSearch_VoiceReturnsVoiceResults(t *testing.T) {
	now := time.Now().UTC()
	store := &mockStore{
		sessions: make(map[session.ID]*session.Session),
		searchResult: &session.SearchResult{
			Sessions: []session.Summary{
				{
					ID:        "sess-1",
					Summary:   "Implemented **dark mode** toggle. Added CSS variables.",
					Agent:     "jarvis",
					Branch:    "feat/dark-mode",
					CreatedAt: now.Add(-2 * time.Hour),
				},
				{
					ID:        "sess-2",
					Summary:   "Fixed `NullPointerException` in user service.",
					Agent:     "copilot",
					Branch:    "main",
					CreatedAt: now.Add(-25 * time.Hour),
				},
			},
			TotalCount: 2,
			Limit:      5,
		},
	}
	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.Search(SearchRequest{Voice: true})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if len(result.VoiceResults) != 2 {
		t.Fatalf("expected 2 voice results, got %d", len(result.VoiceResults))
	}

	// Check first voice result.
	v0 := result.VoiceResults[0]
	if v0.ID != "sess-1" {
		t.Errorf("v0.ID = %q, want 'sess-1'", v0.ID)
	}
	if v0.Agent != "jarvis" {
		t.Errorf("v0.Agent = %q, want 'jarvis'", v0.Agent)
	}
	if v0.Branch != "feat/dark-mode" {
		t.Errorf("v0.Branch = %q, want 'feat/dark-mode'", v0.Branch)
	}
	// Summary should have markdown stripped.
	if v0.Summary != "Implemented dark mode toggle. Added CSS variables." {
		t.Errorf("v0.Summary = %q, want markdown stripped", v0.Summary)
	}
	// TimeAgo should be "2 hours ago".
	if v0.TimeAgo != "2 hours ago" {
		t.Errorf("v0.TimeAgo = %q, want '2 hours ago'", v0.TimeAgo)
	}

	// Check second voice result.
	v1 := result.VoiceResults[1]
	if v1.Summary != "Fixed NullPointerException in user service." {
		t.Errorf("v1.Summary = %q, want inline code stripped", v1.Summary)
	}
	if v1.TimeAgo != "yesterday" {
		t.Errorf("v1.TimeAgo = %q, want 'yesterday'", v1.TimeAgo)
	}
}

func TestSearch_NonVoiceOmitsVoiceResults(t *testing.T) {
	store := &mockStore{
		sessions: make(map[session.ID]*session.Session),
		searchResult: &session.SearchResult{
			Sessions: []session.Summary{
				{ID: "sess-1", Summary: "hello"},
			},
			TotalCount: 1,
			Limit:      50,
		},
	}
	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.Search(SearchRequest{})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if result.VoiceResults != nil {
		t.Errorf("expected nil VoiceResults for non-voice search, got %d items", len(result.VoiceResults))
	}
}

func TestSearch_VoiceExplicitLimitOverridesDefault(t *testing.T) {
	store := &mockStore{
		sessions: make(map[session.ID]*session.Session),
		searchResult: &session.SearchResult{
			Sessions:   make([]session.Summary, 3),
			TotalCount: 3,
			Limit:      3,
		},
	}
	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.Search(SearchRequest{Voice: true, Limit: 3})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Limit != 3 {
		t.Errorf("expected explicit limit=3, got %d", result.Limit)
	}
}

func TestSearch_VoiceTruncatesSummaryToTwoSentences(t *testing.T) {
	store := &mockStore{
		sessions: make(map[session.ID]*session.Session),
		searchResult: &session.SearchResult{
			Sessions: []session.Summary{
				{
					ID:        "long",
					Summary:   "First sentence. Second sentence. Third sentence. Fourth sentence.",
					CreatedAt: time.Now().UTC(),
				},
			},
			TotalCount: 1,
			Limit:      5,
		},
	}
	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.Search(SearchRequest{Voice: true})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	got := result.VoiceResults[0].Summary
	want := "First sentence. Second sentence."
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}
