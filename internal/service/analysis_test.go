package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
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
