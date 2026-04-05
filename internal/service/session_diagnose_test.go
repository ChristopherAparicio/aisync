package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/llm"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Diagnose Quick Scan ──

func TestDiagnose_QuickScan_Healthy(t *testing.T) {
	sess := &session.Session{
		ID: "diag-healthy",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "Add auth", InputTokens: 100},
			{Role: session.RoleAssistant, Content: "Done", OutputTokens: 200,
				ToolCalls: []session.ToolCall{
					{Name: "write", State: session.ToolStateCompleted},
				}},
			{Role: session.RoleUser, Content: "Thanks", InputTokens: 50},
			{Role: session.RoleAssistant, Content: "You're welcome", OutputTokens: 100},
		},
		TokenUsage: session.TokenUsage{InputTokens: 150, OutputTokens: 300, TotalTokens: 450},
	}

	store := &mockStore{sessions: map[session.ID]*session.Session{sess.ID: sess}}
	svc := NewSessionService(SessionServiceConfig{Store: store})

	report, err := svc.Diagnose(context.Background(), DiagnoseRequest{SessionID: sess.ID})
	if err != nil {
		t.Fatalf("Diagnose() error: %v", err)
	}

	if report.Verdict.Status != "healthy" {
		t.Errorf("expected healthy verdict, got %q", report.Verdict.Status)
	}
	if report.Verdict.Score == 0 {
		t.Error("expected non-zero health score")
	}
	if report.ToolReport.TotalCalls != 1 {
		t.Errorf("expected 1 tool call, got %d", report.ToolReport.TotalCalls)
	}
	if report.RestoreAdvice != nil {
		t.Error("expected no restore advice for healthy session")
	}
	// Deep analysis fields should be empty.
	if report.RootCause != "" {
		t.Error("expected empty root cause without --deep")
	}
}

func TestDiagnose_QuickScan_Broken(t *testing.T) {
	msgs := make([]session.Message, 40)
	for i := range msgs {
		msgs[i].InputTokens = 1000
		msgs[i].OutputTokens = 200
	}
	// Add errors in the last quarter.
	for i := 30; i < 40; i++ {
		msgs[i].ToolCalls = []session.ToolCall{
			{Name: "bash", State: session.ToolStateError},
			{Name: "bash", State: session.ToolStateError},
			{Name: "bash", State: session.ToolStateError},
		}
	}
	sess := &session.Session{
		ID:       "diag-broken",
		Messages: msgs,
		Errors: []session.SessionError{
			{MessageIndex: 30, Category: session.ErrorCategoryToolError},
			{MessageIndex: 31, Category: session.ErrorCategoryToolError},
			{MessageIndex: 32, Category: session.ErrorCategoryToolError, ToolCallID: "tc-1"},
		},
		TokenUsage: session.TokenUsage{InputTokens: 40000, OutputTokens: 8000, TotalTokens: 48000},
	}

	store := &mockStore{sessions: map[session.ID]*session.Session{sess.ID: sess}}
	svc := NewSessionService(SessionServiceConfig{Store: store})

	report, err := svc.Diagnose(context.Background(), DiagnoseRequest{SessionID: sess.ID})
	if err != nil {
		t.Fatalf("Diagnose() error: %v", err)
	}

	// Should detect degradation.
	if report.Verdict.Status == "healthy" {
		t.Error("expected non-healthy verdict for session with many errors")
	}

	// Phase analysis should detect late crash pattern.
	if report.Phases.Pattern == "healthy" || report.Phases.Pattern == "too-short" {
		t.Errorf("expected degraded pattern, got %q", report.Phases.Pattern)
	}

	// Error timeline should have entries.
	if len(report.ErrorTimeline) == 0 {
		t.Error("expected error timeline entries")
	}

	// Restore advice should be present.
	if report.RestoreAdvice == nil {
		t.Error("expected restore advice for broken session")
	}
	if report.RestoreAdvice != nil && len(report.RestoreAdvice.SuggestedFilters) == 0 {
		t.Error("expected suggested filters in restore advice")
	}
}

func TestDiagnose_NoMessages(t *testing.T) {
	sess := &session.Session{ID: "diag-empty", Messages: nil}
	store := &mockStore{sessions: map[session.ID]*session.Session{sess.ID: sess}}
	svc := NewSessionService(SessionServiceConfig{Store: store})

	_, err := svc.Diagnose(context.Background(), DiagnoseRequest{SessionID: sess.ID})
	if err == nil {
		t.Fatal("expected error for empty session")
	}
}

func TestDiagnose_SessionNotFound(t *testing.T) {
	store := &mockStore{sessions: map[session.ID]*session.Session{}}
	svc := NewSessionService(SessionServiceConfig{Store: store})

	_, err := svc.Diagnose(context.Background(), DiagnoseRequest{SessionID: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for missing session")
	}
}

// ── Diagnose Deep Scan ──

func TestDiagnose_DeepScan_WithLLM(t *testing.T) {
	report := session.EfficiencyReport{
		Score:       65,
		Summary:     "Session had moderate retry loops in bash tool",
		Strengths:   []string{"Good file organization"},
		Issues:      []string{"Excessive bash retries"},
		Suggestions: []string{"Use write tool for file edits"},
		Patterns:    []string{"retry_loops"},
	}
	reportJSON, _ := json.Marshal(report)

	mockLLM := &mockLLMClient{
		response: &llm.CompletionResponse{
			Content:      string(reportJSON),
			Model:        "test-model",
			InputTokens:  500,
			OutputTokens: 200,
		},
	}

	sess := &session.Session{
		ID: "diag-deep",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "Fix the bug", InputTokens: 100},
			{Role: session.RoleAssistant, Content: "Looking into it", OutputTokens: 200,
				ToolCalls: []session.ToolCall{{Name: "bash", State: session.ToolStateError}}},
			{Role: session.RoleAssistant, Content: "Retrying", OutputTokens: 200,
				ToolCalls: []session.ToolCall{{Name: "bash", State: session.ToolStateCompleted}}},
			{Role: session.RoleAssistant, Content: "Done", OutputTokens: 100},
		},
		TokenUsage: session.TokenUsage{InputTokens: 100, OutputTokens: 500, TotalTokens: 600},
	}

	store := &mockStore{sessions: map[session.ID]*session.Session{sess.ID: sess}}
	svc := NewSessionService(SessionServiceConfig{Store: store, LLM: mockLLM})

	result, err := svc.Diagnose(context.Background(), DiagnoseRequest{
		SessionID: sess.ID,
		Deep:      true,
	})
	if err != nil {
		t.Fatalf("Diagnose(deep) error: %v", err)
	}

	// Quick scan should still be present.
	if result.Verdict.Score == 0 {
		t.Error("expected non-zero health score")
	}

	// Deep analysis should be populated.
	if result.Efficiency == nil {
		t.Fatal("expected efficiency report with --deep")
	}
	if result.RootCause == "" {
		t.Error("expected root cause with --deep")
	}
	if len(result.Suggestions) == 0 {
		t.Error("expected suggestions with --deep")
	}
}

func TestDiagnose_DeepScan_NoLLM(t *testing.T) {
	sess := &session.Session{
		ID: "diag-nollm",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "Hello", InputTokens: 10},
			{Role: session.RoleAssistant, Content: "Hi", OutputTokens: 10},
			{Role: session.RoleUser, Content: "Done", InputTokens: 10},
			{Role: session.RoleAssistant, Content: "Bye", OutputTokens: 10},
		},
		TokenUsage: session.TokenUsage{InputTokens: 20, OutputTokens: 20, TotalTokens: 40},
	}

	store := &mockStore{sessions: map[session.ID]*session.Session{sess.ID: sess}}
	svc := NewSessionService(SessionServiceConfig{Store: store}) // no LLM

	result, err := svc.Diagnose(context.Background(), DiagnoseRequest{
		SessionID: sess.ID,
		Deep:      true, // requesting deep but no LLM → graceful fallback
	})
	if err != nil {
		t.Fatalf("Diagnose(deep, no LLM) error: %v", err)
	}

	// Quick scan should work.
	if result.Verdict.Status == "" {
		t.Error("expected verdict even without LLM")
	}

	// Deep analysis should be empty (no LLM available).
	if result.Efficiency != nil {
		t.Error("expected nil efficiency without LLM")
	}
}
