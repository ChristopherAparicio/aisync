package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/llm"
	"github.com/ChristopherAparicio/aisync/internal/session"
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
	sessions map[session.ID]*session.Session
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
func (m *mockStore) AddLink(_ session.ID, _ session.Link) error            { return nil }
func (m *mockStore) GetByLink(_ session.LinkType, _ string) ([]session.Summary, error) {
	return nil, session.ErrSessionNotFound
}
func (m *mockStore) Close() error                                   { return nil }
func (m *mockStore) SaveUser(_ *session.User) error                 { return nil }
func (m *mockStore) GetUser(_ session.ID) (*session.User, error)    { return nil, nil }
func (m *mockStore) GetUserByEmail(_ string) (*session.User, error) { return nil, nil }
func (m *mockStore) Search(_ session.SearchQuery) (*session.SearchResult, error) {
	return &session.SearchResult{}, nil
}
func (m *mockStore) GetSessionsByFile(_ session.BlameQuery) ([]session.BlameEntry, error) {
	return nil, nil
}
