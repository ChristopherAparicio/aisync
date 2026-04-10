package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/llm"
	"github.com/ChristopherAparicio/aisync/internal/registry"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// mockLLMClient is a test double for llm.Client.
type mockLLMClient struct {
	response *llm.CompletionResponse
	err      error

	// captured holds the last request for assertions.
	captured *llm.CompletionRequest
}

func (m *mockLLMClient) Complete(_ context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	m.captured = &req
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

func makeTestSession() session.Session {
	return session.Session{
		ID:          "test-session-001",
		Provider:    "opencode",
		Agent:       "claude",
		Branch:      "main",
		ProjectPath: "/tmp/test-project",
		CreatedAt:   time.Now(),
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "Fix the bug", InputTokens: 100},
			{
				Role: session.RoleAssistant, Content: "I'll fix it", OutputTokens: 200,
				ToolCalls: []session.ToolCall{
					{Name: "read_file", Input: "main.go", Output: "contents...", State: session.ToolStateCompleted, DurationMs: 50},
					{Name: "edit_file", Input: "main.go", State: session.ToolStateError, DurationMs: 100},
					{Name: "edit_file", Input: "main.go", Output: "ok", State: session.ToolStateCompleted, DurationMs: 80},
				},
			},
			{Role: session.RoleUser, Content: "Thanks!", InputTokens: 20},
		},
		TokenUsage:  session.TokenUsage{InputTokens: 120, OutputTokens: 200, TotalTokens: 320},
		FileChanges: []session.FileChange{{FilePath: "main.go", ChangeType: "modified"}},
	}
}

func validReportJSON(score int) string {
	report := analysis.AnalysisReport{
		Score:   score,
		Summary: "Test session had some issues with file editing retries.",
		Problems: []analysis.Problem{
			{Severity: analysis.SeverityMedium, Description: "File edit failed and was retried", ToolName: "edit_file"},
		},
		Recommendations: []analysis.Recommendation{
			{Category: analysis.CategoryTool, Title: "Validate file paths before editing", Description: "Check file exists before edit.", Priority: 2},
		},
		SkillSuggestions: []analysis.SkillSuggestion{
			{Name: "safe-edit", Description: "Pre-validate file edits", Trigger: "Before edit_file calls"},
		},
	}
	b, _ := json.Marshal(report)
	return string(b)
}

func TestAnalyzerName(t *testing.T) {
	a := NewAnalyzer(AnalyzerConfig{})
	if got := a.Name(); got != analysis.AdapterLLM {
		t.Errorf("Name() = %q, want %q", got, analysis.AdapterLLM)
	}
}

func TestAnalyzerAnalyze_Success(t *testing.T) {
	mock := &mockLLMClient{
		response: &llm.CompletionResponse{
			Content:      validReportJSON(72),
			Model:        "claude-sonnet-4-20250514",
			InputTokens:  500,
			OutputTokens: 300,
		},
	}

	a := NewAnalyzer(AnalyzerConfig{Client: mock, Model: "sonnet"})
	report, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session:        makeTestSession(),
		ErrorThreshold: 20,
	})
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	if report.Score != 72 {
		t.Errorf("Score = %d, want 72", report.Score)
	}
	if report.Summary == "" {
		t.Error("Summary is empty")
	}
	if len(report.Problems) != 1 {
		t.Errorf("Problems count = %d, want 1", len(report.Problems))
	}
	if len(report.Recommendations) != 1 {
		t.Errorf("Recommendations count = %d, want 1", len(report.Recommendations))
	}
	if len(report.SkillSuggestions) != 1 {
		t.Errorf("SkillSuggestions count = %d, want 1", len(report.SkillSuggestions))
	}

	// Verify model was passed through
	if mock.captured.Model != "sonnet" {
		t.Errorf("Model passed = %q, want %q", mock.captured.Model, "sonnet")
	}
}

func TestAnalyzerAnalyze_NilClient(t *testing.T) {
	a := NewAnalyzer(AnalyzerConfig{Client: nil})
	_, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session: makeTestSession(),
	})
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestAnalyzerAnalyze_NoMessages(t *testing.T) {
	a := NewAnalyzer(AnalyzerConfig{Client: &mockLLMClient{}})
	_, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session: session.Session{ID: "empty"},
	})
	if err == nil {
		t.Fatal("expected error for empty session")
	}
}

func TestAnalyzerAnalyze_LLMError(t *testing.T) {
	mock := &mockLLMClient{err: fmt.Errorf("connection refused")}
	a := NewAnalyzer(AnalyzerConfig{Client: mock})
	_, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session: makeTestSession(),
	})
	if err == nil {
		t.Fatal("expected error when LLM fails")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error = %q, expected to contain 'connection refused'", err)
	}
}

func TestAnalyzerAnalyze_InvalidJSON(t *testing.T) {
	mock := &mockLLMClient{
		response: &llm.CompletionResponse{Content: "not json at all"},
	}
	a := NewAnalyzer(AnalyzerConfig{Client: mock})
	_, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session: makeTestSession(),
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "parsing LLM analysis response") {
		t.Errorf("error = %q, expected parsing error", err)
	}
}

func TestAnalyzerAnalyze_ScoreClamping(t *testing.T) {
	// Score > 100 should be clamped to 100
	mock := &mockLLMClient{
		response: &llm.CompletionResponse{Content: validReportJSON(150)},
	}
	a := NewAnalyzer(AnalyzerConfig{Client: mock})
	report, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session: makeTestSession(),
	})
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}
	if report.Score != 100 {
		t.Errorf("Score = %d, want 100 (clamped from 150)", report.Score)
	}
}

func TestBuildAnalysisPrompt_ContainsSessionData(t *testing.T) {
	req := analysis.AnalyzeRequest{
		Session:        makeTestSession(),
		ErrorThreshold: 25,
		Capabilities: []registry.Capability{
			{Name: "test-skill", Kind: registry.KindSkill, Description: "A test skill"},
		},
		MCPServers: []registry.MCPServer{
			{Name: "sentry", Type: "remote", Enabled: true},
		},
	}

	prompt := BuildAnalysisPrompt(req)

	// Should contain key session data
	checks := []string{
		"test-session-001",
		"opencode",
		"main",
		"Messages: 3",
		"Tool calls: 3 total, 1 errors",
		"read_file",
		"edit_file",
		"error rate exceeded 25%",
		"test-skill",
		"sentry",
		"main.go (modified)",
	}
	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\n\nfull prompt:\n%s", want, prompt)
		}
	}
}

func TestBuildAnalysisPrompt_NoDiagnostic(t *testing.T) {
	req := analysis.AnalyzeRequest{
		Session:    makeTestSession(),
		Diagnostic: nil,
	}

	prompt := BuildAnalysisPrompt(req)

	// Should NOT contain diagnostic section when Diagnostic is nil.
	if strings.Contains(prompt, "DETERMINISTIC DIAGNOSTIC REPORT") {
		t.Error("prompt should not contain diagnostic section when Diagnostic is nil")
	}
}

func TestBuildAnalysisPrompt_WithDiagnosticFull(t *testing.T) {
	req := analysis.AnalyzeRequest{
		Session: makeTestSession(),
		Diagnostic: &analysis.DiagnosticSummary{
			InputTokens:           468_000_000,
			OutputTokens:          2_400_000,
			ImageTokens:           27_100_000,
			CacheReadPct:          95.3,
			InputOutputRatio:      195.0,
			EstimatedCost:         360.0,
			InlineImages:          0,
			ToolReadImages:        228,
			SimctlCaptures:        246,
			SipsResizes:           240,
			ImageBilledTok:        27_100_000,
			ImageCost:             81.3,
			AvgTurnsInCtx:         79.0,
			CompactionCount:       39,
			CascadeCount:          1,
			CompactionsPerUser:    0.152,
			MedianInterval:        94,
			AvgBeforeTokens:       185_000,
			TotalToolCalls:        1200,
			ErrorToolCalls:        45,
			ToolErrorRate:         3.75,
			MaxConsecErrors:       4,
			CorrectionCount:       12,
			WriteWithoutReadCount: 5,
			GlobStormCount:        3,
			LongestUnguided:       15,
			Problems: []analysis.DiagnosticProblem{
				{
					ID:          "expensive-screenshots",
					Severity:    "high",
					Category:    "images",
					Title:       "Expensive screenshots in context",
					Observation: "228 images read via tool, avg 79 assistant turns in context.",
					Impact:      "~27.1M billed input tokens from images, est. $81.30 at $3/M input.",
				},
				{
					ID:          "frequent-compaction",
					Severity:    "medium",
					Category:    "compaction",
					Title:       "Frequent context compaction",
					Observation: "39 compactions detected, 0.152 per user message.",
					Impact:      "Frequent compaction indicates context saturation.",
				},
			},
		},
	}

	prompt := BuildAnalysisPrompt(req)

	// Verify diagnostic header and footer.
	checks := []string{
		"=== DETERMINISTIC DIAGNOSTIC REPORT ===",
		"=== END DIAGNOSTIC REPORT ===",
		"Pre-computed by aisync",

		// Token economy.
		"Token economy:",
		"Input: 468000000",
		"Cache read: 95.3%",
		"I/O ratio: 195.0:1",
		"$360.00",

		// Images.
		"Image analysis:",
		"Tool-read images: 228",
		"Simulator captures: 246",
		"Sips resizes: 240",
		"Billed image tokens: 27100000",
		"$81.30",
		"Avg turns in context before compaction: 79.0",

		// Compaction.
		"Compaction analysis:",
		"Compactions: 39",
		"Cascades: 1",
		"0.152 per user message",
		"Median interval: 94 messages",
		"Avg tokens before compaction: 185000",

		// Tool errors.
		"Tool error analysis:",
		"Total tool calls: 1200",
		"Errors: 45",
		"Rate: 3.8%",
		"Max consecutive errors: 4",

		// Behavioral patterns.
		"Behavioral patterns:",
		"User corrections: 12",
		"Write-without-read (yolo edits): 5",
		"Glob/search storms (>5 consecutive): 3",
		"Longest unguided assistant run: 15",

		// Problems.
		"DETECTED PROBLEMS (2):",
		"[high/images] Expensive screenshots in context",
		"228 images read via tool",
		"$81.30",
		"[medium/compaction] Frequent context compaction",
	}
	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\n\nprompt:\n%s", want, prompt)
		}
	}
}

func TestBuildAnalysisPrompt_DiagnosticMinimal(t *testing.T) {
	// Minimal diagnostic: no images, no compaction, no errors, no patterns.
	req := analysis.AnalyzeRequest{
		Session: makeTestSession(),
		Diagnostic: &analysis.DiagnosticSummary{
			InputTokens:  1000,
			OutputTokens: 500,
			CacheReadPct: 60.0,
		},
	}

	prompt := BuildAnalysisPrompt(req)

	// Should have the diagnostic section.
	if !strings.Contains(prompt, "DETERMINISTIC DIAGNOSTIC REPORT") {
		t.Error("prompt missing diagnostic header")
	}
	// Should have token economy.
	if !strings.Contains(prompt, "Token economy:") {
		t.Error("prompt missing token economy section")
	}
	// Should NOT have image/compaction/tool error/pattern sections.
	for _, absent := range []string{"Image analysis:", "Compaction analysis:", "Tool error analysis:", "Behavioral patterns:"} {
		if strings.Contains(prompt, absent) {
			t.Errorf("prompt should not contain %q for minimal diagnostic", absent)
		}
	}
	// Should say no problems.
	if !strings.Contains(prompt, "No deterministic problems detected.") {
		t.Error("prompt should indicate no problems detected")
	}
}

func TestSystemPrompt_ContainsDiagnosticGuidance(t *testing.T) {
	// Verify the updated system prompt references diagnostic data.
	checks := []string{
		"DETERMINISTIC DIAGNOSTIC REPORT",
		"grounding facts",
		"diagnostic problem IDs",
		"provider-aware",
	}
	for _, want := range checks {
		if !strings.Contains(SystemPrompt, want) {
			t.Errorf("SystemPrompt missing %q", want)
		}
	}
}
