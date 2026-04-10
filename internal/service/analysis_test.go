package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/diagnostic"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// mockAnalyzer implements analysis.Analyzer for testing.
type mockAnalyzer struct {
	report *analysis.AnalysisReport
	err    error
	name   analysis.AdapterName
}

func (m *mockAnalyzer) Analyze(_ context.Context, _ analysis.AnalyzeRequest) (*analysis.AnalysisReport, error) {
	return m.report, m.err
}

func (m *mockAnalyzer) Name() analysis.AdapterName {
	if m.name != "" {
		return m.name
	}
	return analysis.AdapterLLM
}

func makeAnalysisTestSession() *session.Session {
	return &session.Session{
		ID:          "sess-001",
		Provider:    "opencode",
		Agent:       "claude",
		ProjectPath: "/tmp/test",
		CreatedAt:   time.Now(),
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "Fix the bug", InputTokens: 100},
			{
				Role: session.RoleAssistant, Content: "Done",
				ToolCalls: []session.ToolCall{
					{Name: "read_file", State: session.ToolStateCompleted},
					{Name: "edit_file", State: session.ToolStateError},
					{Name: "edit_file", State: session.ToolStateCompleted},
				},
			},
		},
		TokenUsage: session.TokenUsage{TotalTokens: 500},
	}
}

func TestAnalysisService_Analyze_Success(t *testing.T) {
	store := &mockStore{}
	sess := makeAnalysisTestSession()
	store.sessions = map[session.ID]*session.Session{sess.ID: sess}
	store.analyses = make(map[string]*analysis.SessionAnalysis)

	analyzer := &mockAnalyzer{
		report: &analysis.AnalysisReport{
			Score:   72,
			Summary: "Good session with minor issues.",
		},
	}

	svc := NewAnalysisService(AnalysisServiceConfig{
		Store:    store,
		Analyzer: analyzer,
	})

	result, err := svc.Analyze(context.Background(), AnalysisRequest{
		SessionID: sess.ID,
		Trigger:   analysis.TriggerManual,
	})
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	if !result.Analysis.OK() {
		t.Errorf("Analysis has error: %s", result.Analysis.Error)
	}
	if result.Analysis.Report.Score != 72 {
		t.Errorf("Score = %d, want 72", result.Analysis.Report.Score)
	}
	if result.Analysis.Adapter != analysis.AdapterLLM {
		t.Errorf("Adapter = %q, want %q", result.Analysis.Adapter, analysis.AdapterLLM)
	}
	if result.Analysis.Trigger != analysis.TriggerManual {
		t.Errorf("Trigger = %q, want %q", result.Analysis.Trigger, analysis.TriggerManual)
	}

	// Verify it was persisted
	stored, err := store.GetAnalysisBySession(string(sess.ID))
	if err != nil {
		t.Fatalf("GetAnalysisBySession() error: %v", err)
	}
	if stored.ID != result.Analysis.ID {
		t.Errorf("stored ID = %q, want %q", stored.ID, result.Analysis.ID)
	}
}

func TestAnalysisService_Analyze_AnalyzerError(t *testing.T) {
	store := &mockStore{}
	sess := makeAnalysisTestSession()
	store.sessions = map[session.ID]*session.Session{sess.ID: sess}
	store.analyses = make(map[string]*analysis.SessionAnalysis)

	analyzer := &mockAnalyzer{
		err: fmt.Errorf("LLM timeout"),
	}

	svc := NewAnalysisService(AnalysisServiceConfig{
		Store:    store,
		Analyzer: analyzer,
	})

	result, err := svc.Analyze(context.Background(), AnalysisRequest{
		SessionID: sess.ID,
		Trigger:   analysis.TriggerAuto,
	})
	// The error should be persisted, not returned as a top-level error
	if err != nil {
		t.Fatalf("Analyze() should not return error for analyzer failure: %v", err)
	}

	if result.Analysis.OK() {
		t.Error("expected analysis to have error")
	}
	if result.Analysis.Error != "LLM timeout" {
		t.Errorf("Error = %q, want %q", result.Analysis.Error, "LLM timeout")
	}
}

func TestAnalysisService_Analyze_NoAnalyzer(t *testing.T) {
	store := &mockStore{}
	svc := NewAnalysisService(AnalysisServiceConfig{Store: store})

	_, err := svc.Analyze(context.Background(), AnalysisRequest{
		SessionID: "any",
	})
	if err == nil {
		t.Error("expected error when no analyzer configured")
	}
}

func TestAnalysisService_Analyze_SessionNotFound(t *testing.T) {
	store := &mockStore{sessions: make(map[session.ID]*session.Session)}
	analyzer := &mockAnalyzer{}
	svc := NewAnalysisService(AnalysisServiceConfig{Store: store, Analyzer: analyzer})

	_, err := svc.Analyze(context.Background(), AnalysisRequest{
		SessionID: "nonexistent",
	})
	if err == nil {
		t.Error("expected error for missing session")
	}
}

func TestAnalysisService_Analyze_EmptySession(t *testing.T) {
	store := &mockStore{}
	empty := &session.Session{ID: "empty"}
	store.sessions = map[session.ID]*session.Session{empty.ID: empty}

	analyzer := &mockAnalyzer{}
	svc := NewAnalysisService(AnalysisServiceConfig{Store: store, Analyzer: analyzer})

	_, err := svc.Analyze(context.Background(), AnalysisRequest{
		SessionID: "empty",
	})
	if err == nil {
		t.Error("expected error for session with no messages")
	}
}

func TestShouldAutoAnalyze(t *testing.T) {
	tests := []struct {
		name           string
		toolCalls      []session.ToolCall
		errorThreshold float64
		minToolCalls   int
		want           bool
	}{
		{
			name:           "high error rate, enough calls",
			toolCalls:      makeToolCalls(10, 3), // 30% error rate
			errorThreshold: 20,
			minToolCalls:   5,
			want:           true,
		},
		{
			name:           "low error rate",
			toolCalls:      makeToolCalls(10, 1), // 10% error rate
			errorThreshold: 20,
			minToolCalls:   5,
			want:           false,
		},
		{
			name:           "not enough tool calls",
			toolCalls:      makeToolCalls(3, 2), // 66% error rate but only 3 calls
			errorThreshold: 20,
			minToolCalls:   5,
			want:           false,
		},
		{
			name:           "exact threshold",
			toolCalls:      makeToolCalls(10, 2), // 20% = threshold
			errorThreshold: 20,
			minToolCalls:   5,
			want:           true,
		},
		{
			name:           "no tool calls",
			toolCalls:      nil,
			errorThreshold: 20,
			minToolCalls:   5,
			want:           false,
		},
		{
			name:           "no messages",
			toolCalls:      nil,
			errorThreshold: 20,
			minToolCalls:   0,
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := &session.Session{}
			if tt.toolCalls != nil || tt.name != "no messages" {
				sess.Messages = []session.Message{
					{Role: session.RoleAssistant, ToolCalls: tt.toolCalls},
				}
			}

			got := ShouldAutoAnalyze(sess, tt.errorThreshold, tt.minToolCalls)
			if got != tt.want {
				t.Errorf("ShouldAutoAnalyze() = %v, want %v", got, tt.want)
			}
		})
	}
}

// makeToolCalls creates a slice of tool calls with the given total and error count.
func makeToolCalls(total, errors int) []session.ToolCall {
	calls := make([]session.ToolCall, total)
	for i := range calls {
		calls[i].Name = "test_tool"
		if i < errors {
			calls[i].State = session.ToolStateError
		} else {
			calls[i].State = session.ToolStateCompleted
		}
	}
	return calls
}

// ── toDiagnosticSummary tests ──

func TestToDiagnosticSummary_Nil(t *testing.T) {
	if got := toDiagnosticSummary(nil); got != nil {
		t.Errorf("toDiagnosticSummary(nil) = %v, want nil", got)
	}
}

func TestToDiagnosticSummary_FullReport(t *testing.T) {
	report := &diagnostic.InspectReport{
		SessionID: "sess-test",
		Provider:  "opencode",
		Agent:     "claude",
		Messages:  100,
		UserMsgs:  40,
		AsstMsgs:  60,
		Tokens: &diagnostic.TokenSection{
			Input:            468_000_000,
			Output:           2_400_000,
			Image:            27_100_000,
			CachePct:         95.3,
			InputOutputRatio: 195.0,
			EstCost:          360.0,
		},
		Images: &diagnostic.ImageSection{
			InlineImages:   5,
			ToolReadImages: 228,
			SimctlCaptures: 246,
			SipsResizes:    240,
			TotalBilledTok: 27_100_000,
			EstImageCost:   81.3,
			AvgTurnsInCtx:  79.0,
		},
		Compaction: &diagnostic.CompactionSection{
			Count:           39,
			CascadeCount:    1,
			PerUserMsg:      0.152,
			IntervalMedian:  94,
			AvgBeforeTokens: 185_000,
		},
		ToolErrors: &diagnostic.ToolErrorSection{
			TotalToolCalls: 1200,
			ErrorCount:     45,
			ErrorRate:      3.75,
			ConsecutiveMax: 4,
		},
		Patterns: &diagnostic.PatternSection{
			UserCorrectionCount:   12,
			WriteWithoutReadCount: 5,
			GlobStormCount:        3,
			LongestRunLength:      15,
		},
		Problems: []diagnostic.Problem{
			{
				ID:       diagnostic.ProblemExpensiveScreenshots,
				Severity: diagnostic.SeverityHigh,
				Category: diagnostic.CategoryImages,
				Title:    "Expensive screenshots",
			},
		},
	}

	ds := toDiagnosticSummary(report)
	if ds == nil {
		t.Fatal("toDiagnosticSummary returned nil for non-nil report")
	}

	// Token economy.
	if ds.InputTokens != 468_000_000 {
		t.Errorf("InputTokens = %d, want 468000000", ds.InputTokens)
	}
	if ds.CacheReadPct != 95.3 {
		t.Errorf("CacheReadPct = %.1f, want 95.3", ds.CacheReadPct)
	}
	if ds.EstimatedCost != 360.0 {
		t.Errorf("EstimatedCost = %.1f, want 360.0", ds.EstimatedCost)
	}

	// Images.
	if ds.ToolReadImages != 228 {
		t.Errorf("ToolReadImages = %d, want 228", ds.ToolReadImages)
	}
	if ds.ImageCost != 81.3 {
		t.Errorf("ImageCost = %.1f, want 81.3", ds.ImageCost)
	}
	if ds.AvgTurnsInCtx != 79.0 {
		t.Errorf("AvgTurnsInCtx = %.1f, want 79.0", ds.AvgTurnsInCtx)
	}

	// Compaction.
	if ds.CompactionCount != 39 {
		t.Errorf("CompactionCount = %d, want 39", ds.CompactionCount)
	}
	if ds.MedianInterval != 94 {
		t.Errorf("MedianInterval = %d, want 94", ds.MedianInterval)
	}

	// Tool errors.
	if ds.TotalToolCalls != 1200 {
		t.Errorf("TotalToolCalls = %d, want 1200", ds.TotalToolCalls)
	}
	if ds.MaxConsecErrors != 4 {
		t.Errorf("MaxConsecErrors = %d, want 4", ds.MaxConsecErrors)
	}

	// Patterns.
	if ds.CorrectionCount != 12 {
		t.Errorf("CorrectionCount = %d, want 12", ds.CorrectionCount)
	}
	if ds.WriteWithoutReadCount != 5 {
		t.Errorf("WriteWithoutReadCount = %d, want 5", ds.WriteWithoutReadCount)
	}

	// Problems.
	if len(ds.Problems) != 1 {
		t.Fatalf("Problems count = %d, want 1", len(ds.Problems))
	}
	if ds.Problems[0].ID != "expensive-screenshots" {
		t.Errorf("Problem ID = %q, want expensive-screenshots", ds.Problems[0].ID)
	}
	if ds.Problems[0].Severity != "high" {
		t.Errorf("Problem Severity = %q, want high", ds.Problems[0].Severity)
	}
}

func TestToDiagnosticSummary_EmptySections(t *testing.T) {
	// Report with nil sections should produce a summary with zero values.
	report := &diagnostic.InspectReport{
		SessionID: "sess-empty",
	}

	ds := toDiagnosticSummary(report)
	if ds == nil {
		t.Fatal("toDiagnosticSummary returned nil for non-nil report")
	}
	if ds.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0", ds.InputTokens)
	}
	if len(ds.Problems) != 0 {
		t.Errorf("Problems = %d, want 0", len(ds.Problems))
	}
}

func TestAnalysisService_Analyze_BuildsDiagnostic(t *testing.T) {
	// Verify that AnalysisService.Analyze passes the Diagnostic field to the analyzer.
	store := &mockStore{}
	sess := makeAnalysisTestSession()
	store.sessions = map[session.ID]*session.Session{sess.ID: sess}
	store.analyses = make(map[string]*analysis.SessionAnalysis)

	var capturedDiag *analysis.DiagnosticSummary
	analyzer := &mockAnalyzerCapture{
		report: &analysis.AnalysisReport{
			Score:   65,
			Summary: "Good session.",
		},
		onAnalyze: func(req analysis.AnalyzeRequest) {
			capturedDiag = req.Diagnostic
		},
	}

	svc := NewAnalysisService(AnalysisServiceConfig{
		Store:    store,
		Analyzer: analyzer,
	})

	_, err := svc.Analyze(context.Background(), AnalysisRequest{
		SessionID: sess.ID,
		Trigger:   analysis.TriggerManual,
	})
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	if capturedDiag == nil {
		t.Fatal("Diagnostic was not passed to analyzer")
	}

	// The diagnostic should have been built from the session data.
	// The test session has 3 tool calls (2 completed, 1 error).
	if capturedDiag.TotalToolCalls != 3 {
		t.Errorf("Diagnostic.TotalToolCalls = %d, want 3", capturedDiag.TotalToolCalls)
	}
	if capturedDiag.ErrorToolCalls != 1 {
		t.Errorf("Diagnostic.ErrorToolCalls = %d, want 1", capturedDiag.ErrorToolCalls)
	}
}

// mockAnalyzerCapture captures the AnalyzeRequest for test assertions.
type mockAnalyzerCapture struct {
	report    *analysis.AnalysisReport
	onAnalyze func(analysis.AnalyzeRequest)
}

func (m *mockAnalyzerCapture) Analyze(_ context.Context, req analysis.AnalyzeRequest) (*analysis.AnalysisReport, error) {
	if m.onAnalyze != nil {
		m.onAnalyze(req)
	}
	return m.report, nil
}

func (m *mockAnalyzerCapture) Name() analysis.AdapterName {
	return analysis.AdapterLLM
}
