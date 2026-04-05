package scheduler

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Mock AnalysisServicer ──

type mockAnalysisService struct {
	analyzeCount int
	analyses     map[string]*analysis.SessionAnalysis // sessionID → latest analysis
	analyzeErr   error
}

func (m *mockAnalysisService) Analyze(_ context.Context, req service.AnalysisRequest) (*service.AnalysisResult, error) {
	if m.analyzeErr != nil {
		return nil, m.analyzeErr
	}
	m.analyzeCount++
	sa := &analysis.SessionAnalysis{
		ID:        "analysis-test",
		SessionID: string(req.SessionID),
		CreatedAt: time.Now(),
		Trigger:   req.Trigger,
		Adapter:   analysis.AdapterOllama,
		Report:    analysis.AnalysisReport{Score: 75, Summary: "test"},
	}
	if m.analyses == nil {
		m.analyses = make(map[string]*analysis.SessionAnalysis)
	}
	m.analyses[string(req.SessionID)] = sa
	return &service.AnalysisResult{Analysis: sa}, nil
}

func (m *mockAnalysisService) GetLatestAnalysis(sessionID string) (*analysis.SessionAnalysis, error) {
	if m.analyses == nil {
		return nil, analysis.ErrAnalysisNotFound
	}
	sa, ok := m.analyses[sessionID]
	if !ok {
		return nil, analysis.ErrAnalysisNotFound
	}
	return sa, nil
}

func (m *mockAnalysisService) ListAnalyses(_ string) ([]*analysis.SessionAnalysis, error) {
	return nil, nil
}

func (m *mockAnalysisService) AvailableModules() []analysis.ModuleInfo {
	return nil
}

// ── Mock SessionReader ──

type mockSessionReader struct {
	sessions []session.Summary
}

func (m *mockSessionReader) Get(_ session.ID) (*session.Session, error) {
	return nil, session.ErrSessionNotFound
}

func (m *mockSessionReader) GetBatch(_ []session.ID) (map[session.ID]*session.Session, error) {
	return map[session.ID]*session.Session{}, nil
}

func (m *mockSessionReader) GetLatestByBranch(_, _ string) (*session.Session, error) {
	return nil, session.ErrSessionNotFound
}

func (m *mockSessionReader) CountByBranch(_, _ string) (int, error) {
	return 0, nil
}

func (m *mockSessionReader) List(_ session.ListOptions) ([]session.Summary, error) {
	return m.sessions, nil
}

func (m *mockSessionReader) GetFreshness(_ session.ID) (int, int64, error) {
	return 0, 0, nil
}

func (m *mockSessionReader) ListProjects() ([]session.ProjectGroup, error) {
	return nil, nil
}

// ── Tests ──

func TestAnalyzeDailyTask_Name(t *testing.T) {
	task := NewAnalyzeDailyTask(AnalyzeDailyConfig{})
	if task.Name() != "analyze_daily" {
		t.Errorf("Name() = %q, want %q", task.Name(), "analyze_daily")
	}
}

func TestAnalyzeDailyTask_AnalyzesNewSessions(t *testing.T) {
	analysisSvc := &mockAnalysisService{}
	store := &mockSessionReader{
		sessions: []session.Summary{
			{ID: "sess-1", CreatedAt: time.Now().Add(-1 * time.Hour), ToolCallCount: 5},
			{ID: "sess-2", CreatedAt: time.Now().Add(-2 * time.Hour), ToolCallCount: 3},
		},
	}

	task := NewAnalyzeDailyTask(AnalyzeDailyConfig{
		AnalysisService: analysisSvc,
		Store:           store,
		Logger:          log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if analysisSvc.analyzeCount != 2 {
		t.Errorf("expected 2 analyses, got %d", analysisSvc.analyzeCount)
	}
}

func TestAnalyzeDailyTask_SkipsAlreadyAnalyzed(t *testing.T) {
	analysisSvc := &mockAnalysisService{
		analyses: map[string]*analysis.SessionAnalysis{
			"sess-1": {ID: "analysis-1", SessionID: "sess-1"},
		},
	}
	store := &mockSessionReader{
		sessions: []session.Summary{
			{ID: "sess-1", CreatedAt: time.Now().Add(-1 * time.Hour)},
			{ID: "sess-2", CreatedAt: time.Now().Add(-2 * time.Hour)},
		},
	}

	task := NewAnalyzeDailyTask(AnalyzeDailyConfig{
		AnalysisService: analysisSvc,
		Store:           store,
		Logger:          log.Default(),
	})

	_ = task.Run(context.Background())

	// Only sess-2 should be analyzed (sess-1 already has one).
	if analysisSvc.analyzeCount != 1 {
		t.Errorf("expected 1 analysis, got %d", analysisSvc.analyzeCount)
	}
}

func TestAnalyzeDailyTask_SkipsOldSessions(t *testing.T) {
	analysisSvc := &mockAnalysisService{}
	store := &mockSessionReader{
		sessions: []session.Summary{
			{ID: "old-session", CreatedAt: time.Now().Add(-48 * time.Hour)}, // 2 days ago
		},
	}

	task := NewAnalyzeDailyTask(AnalyzeDailyConfig{
		AnalysisService: analysisSvc,
		Store:           store,
		Logger:          log.Default(),
		LookbackHours:   24,
	})

	_ = task.Run(context.Background())

	if analysisSvc.analyzeCount != 0 {
		t.Errorf("expected 0 analyses (session too old), got %d", analysisSvc.analyzeCount)
	}
}

func TestAnalyzeDailyTask_MinToolCallsFilter(t *testing.T) {
	analysisSvc := &mockAnalysisService{}
	store := &mockSessionReader{
		sessions: []session.Summary{
			{ID: "few-tools", CreatedAt: time.Now().Add(-1 * time.Hour), ToolCallCount: 2},
			{ID: "many-tools", CreatedAt: time.Now().Add(-1 * time.Hour), ToolCallCount: 10},
		},
	}

	task := NewAnalyzeDailyTask(AnalyzeDailyConfig{
		AnalysisService: analysisSvc,
		Store:           store,
		Logger:          log.Default(),
		MinToolCalls:    5,
	})

	_ = task.Run(context.Background())

	// Only "many-tools" should be analyzed.
	if analysisSvc.analyzeCount != 1 {
		t.Errorf("expected 1 analysis (filtered by min_tool_calls), got %d", analysisSvc.analyzeCount)
	}
}

func TestAnalyzeDailyTask_NoSessions(t *testing.T) {
	analysisSvc := &mockAnalysisService{}
	store := &mockSessionReader{sessions: nil}

	task := NewAnalyzeDailyTask(AnalyzeDailyConfig{
		AnalysisService: analysisSvc,
		Store:           store,
		Logger:          log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if analysisSvc.analyzeCount != 0 {
		t.Errorf("expected 0 analyses, got %d", analysisSvc.analyzeCount)
	}
}

func TestAnalyzeDailyTask_ContextCancelled(t *testing.T) {
	analysisSvc := &mockAnalysisService{}
	store := &mockSessionReader{
		sessions: []session.Summary{
			{ID: "sess-1", CreatedAt: time.Now()},
			{ID: "sess-2", CreatedAt: time.Now()},
		},
	}

	task := NewAnalyzeDailyTask(AnalyzeDailyConfig{
		AnalysisService: analysisSvc,
		Store:           store,
		Logger:          log.Default(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	err := task.Run(ctx)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
