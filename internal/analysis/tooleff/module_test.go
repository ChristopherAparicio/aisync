package tooleff

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/llm"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// mockLLM implements llm.Client for testing.
type mockLLM struct {
	response string
	err      error
}

func (m *mockLLM) Complete(_ context.Context, _ llm.CompletionRequest) (*llm.CompletionResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.CompletionResponse{
		Content:      m.response,
		InputTokens:  100,
		OutputTokens: 200,
	}, nil
}

func TestModuleName(t *testing.T) {
	mod := NewModule(nil)
	if mod.Name() != analysis.ModuleToolEfficiency {
		t.Errorf("Name() = %q, want %q", mod.Name(), analysis.ModuleToolEfficiency)
	}
}

func TestAnalyze_NoToolCalls(t *testing.T) {
	mod := NewModule(&mockLLM{})
	result, err := mod.Analyze(context.Background(), analysis.ModuleRequest{
		Session: session.Session{
			Messages: []session.Message{
				{Role: session.RoleUser, Content: "Hello"},
				{Role: session.RoleAssistant, Content: "Hi there"},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Module != analysis.ModuleToolEfficiency {
		t.Errorf("module = %q, want tool_efficiency", result.Module)
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}

	var report analysis.ToolEfficiencyReport
	if err := json.Unmarshal(result.Payload, &report); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if report.OverallScore != 100 {
		t.Errorf("score = %d, want 100 for no tool calls", report.OverallScore)
	}
}

func TestAnalyze_WithToolCalls(t *testing.T) {
	llmResp := `{
		"tool_evaluations": [
			{"index": 0, "tool_name": "read", "usefulness": "useful", "reason": "Needed the config"},
			{"index": 1, "tool_name": "bash", "usefulness": "redundant", "reason": "Same command"}
		],
		"summary": "Mixed efficiency",
		"overall_score": 65,
		"patterns": ["repeated bash calls"]
	}`

	mod := NewModule(&mockLLM{response: llmResp})
	result, err := mod.Analyze(context.Background(), analysis.ModuleRequest{
		Session: session.Session{
			Messages: []session.Message{
				{Role: session.RoleUser, Content: "Fix the bug"},
				{
					Role: session.RoleAssistant,
					ToolCalls: []session.ToolCall{
						{Name: "read", Input: `{"file":"config.go"}`, Output: "package config...", State: session.ToolStateCompleted},
						{Name: "bash", Input: `{"cmd":"go test"}`, Output: "FAIL", State: session.ToolStateError},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("module error: %s", result.Error)
	}
	if result.TokensUsed != 300 {
		t.Errorf("tokens = %d, want 300", result.TokensUsed)
	}
	if result.DurationMs <= 0 {
		t.Error("duration should be positive")
	}

	var report analysis.ToolEfficiencyReport
	if err := json.Unmarshal(result.Payload, &report); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if report.OverallScore != 65 {
		t.Errorf("score = %d, want 65", report.OverallScore)
	}
	if report.UsefulCalls != 1 {
		t.Errorf("useful = %d, want 1", report.UsefulCalls)
	}
	if report.RedundantCalls != 1 {
		t.Errorf("redundant = %d, want 1", report.RedundantCalls)
	}
	if len(report.Patterns) != 1 {
		t.Errorf("patterns = %d, want 1", len(report.Patterns))
	}
}

func TestAnalyze_NilClient(t *testing.T) {
	mod := NewModule(nil)
	sess := session.Session{
		Messages: []session.Message{
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{{Name: "read"}}},
		},
	}
	_, err := mod.Analyze(context.Background(), analysis.ModuleRequest{Session: sess})
	if err == nil {
		t.Error("expected error for nil client")
	}
}

func TestAnalyze_LLMResponseWithCodeFences(t *testing.T) {
	llmResp := "```json\n{\"tool_evaluations\":[], \"summary\":\"ok\", \"overall_score\":90}\n```"
	mod := NewModule(&mockLLM{response: llmResp})
	result, err := mod.Analyze(context.Background(), analysis.ModuleRequest{
		Session: session.Session{
			Messages: []session.Message{
				{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{{Name: "read", State: session.ToolStateCompleted}}},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("module error: %s", result.Error)
	}
	var report analysis.ToolEfficiencyReport
	if err := json.Unmarshal(result.Payload, &report); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if report.OverallScore != 90 {
		t.Errorf("score = %d, want 90", report.OverallScore)
	}
}

func TestCollectToolCalls(t *testing.T) {
	sess := session.Session{
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "Fix it"},
			{
				Role: session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{Name: "read", InputTokens: 10, OutputTokens: 50},
					{Name: "edit", InputTokens: 20, OutputTokens: 5},
				},
			},
			{Role: session.RoleUser, Content: "Thanks"},
		},
	}
	calls := collectToolCalls(sess)
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if calls[0].ToolCall.Name != "read" {
		t.Errorf("call[0] = %q, want read", calls[0].ToolCall.Name)
	}
	if calls[0].PrevContent != "Fix it" {
		t.Errorf("prev content = %q, want 'Fix it'", calls[0].PrevContent)
	}
	if calls[0].NextContent != "Thanks" {
		t.Errorf("next content = %q, want 'Thanks'", calls[0].NextContent)
	}
}

func TestCleanJSON(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{`{"ok":true}`, `{"ok":true}`},
		{"```json\n{\"ok\":true}\n```", `{"ok":true}`},
		{"```\n{\"ok\":true}\n```", `{"ok":true}`},
		{"  {\"ok\":true}  ", `{"ok":true}`},
	}
	for _, tt := range tests {
		got := cleanJSON(tt.input)
		if got != tt.want {
			t.Errorf("cleanJSON(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTruncateStr(t *testing.T) {
	if got := truncateStr("hello", 10); got != "hello" {
		t.Errorf("truncateStr short = %q", got)
	}
	if got := truncateStr("hello world", 5); got != "hello..." {
		t.Errorf("truncateStr long = %q", got)
	}
}
