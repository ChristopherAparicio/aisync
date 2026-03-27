package scheduler

import (
	"context"
	"errors"
	"log"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/search"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Mock SessionServicer for task tests ──
//
// Minimal mock that implements service.SessionServicer.
// Only the methods used by GCTask, CaptureAllTask, and StatsReportTask
// have real implementations; the rest panic if called unexpectedly.

type mockSessionService struct {
	// GarbageCollect mock
	gcResult *service.GCResult
	gcErr    error
	gcCalled bool
	gcReq    service.GCRequest

	// CaptureAll mock
	captureResults []*service.CaptureResult
	captureErr     error
	captureCalled  bool

	// Stats mock
	statsResult *service.StatsResult
	statsErr    error
	statsCalled bool
}

// ── Methods used by tasks ──

func (m *mockSessionService) GarbageCollect(_ context.Context, req service.GCRequest) (*service.GCResult, error) {
	m.gcCalled = true
	m.gcReq = req
	return m.gcResult, m.gcErr
}

func (m *mockSessionService) CaptureAll(req service.CaptureRequest) ([]*service.CaptureResult, error) {
	m.captureCalled = true
	return m.captureResults, m.captureErr
}

func (m *mockSessionService) Stats(req service.StatsRequest) (*service.StatsResult, error) {
	m.statsCalled = true
	return m.statsResult, m.statsErr
}

// ── Stub methods (not used by tasks, must exist for interface satisfaction) ──

func (m *mockSessionService) Capture(_ service.CaptureRequest) (*service.CaptureResult, error) {
	return nil, nil
}
func (m *mockSessionService) CaptureByID(_ service.CaptureRequest, _ session.ID) (*service.CaptureResult, error) {
	return nil, nil
}
func (m *mockSessionService) Restore(_ service.RestoreRequest) (*service.RestoreResult, error) {
	return nil, nil
}
func (m *mockSessionService) Get(_ string) (*session.Session, error) {
	return nil, nil
}
func (m *mockSessionService) List(_ service.ListRequest) ([]session.Summary, error) {
	return nil, nil
}
func (m *mockSessionService) ListTree(_ context.Context, _ service.ListRequest) ([]session.SessionTreeNode, error) {
	return nil, nil
}
func (m *mockSessionService) Delete(_ session.ID) error { return nil }
func (m *mockSessionService) TagSession(_ context.Context, _ session.ID, _ string) error {
	return nil
}
func (m *mockSessionService) Export(_ service.ExportRequest) (*service.ExportResult, error) {
	return nil, nil
}
func (m *mockSessionService) Import(_ service.ImportRequest) (*service.ImportResult, error) {
	return nil, nil
}
func (m *mockSessionService) Link(_ service.LinkRequest) (*service.LinkResult, error) {
	return nil, nil
}
func (m *mockSessionService) Comment(_ service.CommentRequest) (*service.CommentResult, error) {
	return nil, nil
}
func (m *mockSessionService) Search(_ service.SearchRequest) (*session.SearchResult, error) {
	return nil, nil
}
func (m *mockSessionService) Blame(_ context.Context, _ service.BlameRequest) (*service.BlameResult, error) {
	return nil, nil
}
func (m *mockSessionService) EstimateCost(_ context.Context, _ string) (*session.CostEstimate, error) {
	return nil, nil
}
func (m *mockSessionService) ToolUsage(_ context.Context, _ string) (*session.ToolUsageStats, error) {
	return nil, nil
}
func (m *mockSessionService) Forecast(_ context.Context, _ service.ForecastRequest) (*session.ForecastResult, error) {
	return nil, nil
}
func (m *mockSessionService) ListProjects(_ context.Context) ([]session.ProjectGroup, error) {
	return nil, nil
}
func (m *mockSessionService) Trends(_ context.Context, _ service.TrendRequest) (*service.TrendResult, error) {
	return nil, nil
}
func (m *mockSessionService) Summarize(_ context.Context, _ service.SummarizeRequest) (*service.SummarizeResult, error) {
	return nil, nil
}
func (m *mockSessionService) Explain(_ context.Context, _ service.ExplainRequest) (*service.ExplainResult, error) {
	return nil, nil
}
func (m *mockSessionService) AnalyzeEfficiency(_ context.Context, _ service.EfficiencyRequest) (*service.EfficiencyResult, error) {
	return nil, nil
}
func (m *mockSessionService) ComputeObjective(_ context.Context, _ service.ComputeObjectiveRequest) (*session.SessionObjective, error) {
	return nil, nil
}
func (m *mockSessionService) GetObjective(_ context.Context, _ string) (*session.SessionObjective, error) {
	return nil, nil
}
func (m *mockSessionService) BranchTimeline(_ context.Context, _ service.TimelineRequest) ([]service.TimelineEntry, error) {
	return nil, nil
}
func (m *mockSessionService) ComputeTokenBuckets(_ context.Context, _ service.ComputeTokenBucketsRequest) (*service.ComputeTokenBucketsResult, error) {
	return &service.ComputeTokenBucketsResult{}, nil
}
func (m *mockSessionService) QueryTokenUsage(_ context.Context, _ service.QueryTokenUsageRequest) ([]session.TokenUsageBucket, error) {
	return nil, nil
}
func (m *mockSessionService) ToolCostSummary(_ context.Context, _ string, _, _ time.Time) (*session.ToolCostSummary, error) {
	return nil, nil
}
func (m *mockSessionService) AgentCostSummary(_ context.Context, _ string, _, _ time.Time) ([]session.AgentCostEntry, error) {
	return nil, nil
}
func (m *mockSessionService) CacheEfficiency(_ context.Context, _ string, _ time.Time) (*session.CacheEfficiency, error) {
	return nil, nil
}
func (m *mockSessionService) MCPCostMatrix(_ context.Context, _, _ time.Time) (*session.MCPProjectMatrix, error) {
	return nil, nil
}
func (m *mockSessionService) ContextSaturation(_ context.Context, _ string, _ time.Time) (*session.ContextSaturation, error) {
	return nil, nil
}
func (m *mockSessionService) ClassifySession(_ *session.Session) int {
	return 0
}
func (m *mockSessionService) ClassifyProjectSessions(_, _ string) (int, int, error) {
	return 0, 0, nil
}
func (m *mockSessionService) BudgetStatus(_ context.Context) ([]session.BudgetStatus, error) {
	return nil, nil
}
func (m *mockSessionService) SearchCapabilities() search.Capabilities {
	return search.Capabilities{}
}
func (m *mockSessionService) IndexAllSessions(_ context.Context) (int, int, error) {
	return 0, 0, nil
}
func (m *mockSessionService) SessionSaturationCurve(_ context.Context, _ session.ID) (*session.SaturationCurve, error) {
	return nil, nil
}
func (m *mockSessionService) AgentROIAnalysis(_ context.Context, _ string, _ time.Time) (*session.AgentROI, error) {
	return nil, nil
}
func (m *mockSessionService) SkillROIAnalysis(_ context.Context, _ string, _ time.Time) (*session.SkillROI, error) {
	return nil, nil
}
func (m *mockSessionService) GenerateRecommendations(_ context.Context, _ string) ([]session.Recommendation, error) {
	return nil, nil
}
func (m *mockSessionService) ExtractAndSaveFiles(_ *session.Session) (int, error) { return 0, nil }
func (m *mockSessionService) BackfillFileBlame(_ context.Context) (int, int, error) {
	return 0, 0, nil
}
func (m *mockSessionService) GetSessionFiles(_ context.Context, _ session.ID) ([]session.SessionFileRecord, error) {
	return nil, nil
}
func (m *mockSessionService) Rewind(_ context.Context, _ service.RewindRequest) (*service.RewindResult, error) {
	return nil, nil
}
func (m *mockSessionService) Diff(_ context.Context, _ service.DiffRequest) (*session.DiffResult, error) {
	return nil, nil
}
func (m *mockSessionService) DetectOffTopic(_ context.Context, _ service.OffTopicRequest) (*session.OffTopicResult, error) {
	return nil, nil
}
func (m *mockSessionService) Ingest(_ context.Context, _ service.IngestRequest) (*service.IngestResult, error) {
	return nil, nil
}
func (m *mockSessionService) LinkSessions(_ context.Context, _ service.SessionLinkRequest) (*session.SessionLink, error) {
	return nil, nil
}
func (m *mockSessionService) GetLinkedSessions(_ context.Context, _ session.ID) ([]session.SessionLink, error) {
	return nil, nil
}
func (m *mockSessionService) DeleteSessionLink(_ context.Context, _ session.ID) error {
	return nil
}
func (m *mockSessionService) BackfillRemoteURLs(_ context.Context) (*service.BackfillResult, error) {
	return &service.BackfillResult{}, nil
}
func (m *mockSessionService) DetectForksBatch(_ context.Context) (*service.ForkDetectionResult, error) {
	return &service.ForkDetectionResult{}, nil
}

// Compile-time check.
var _ service.SessionServicer = (*mockSessionService)(nil)

// ── GCTask Tests ──

func TestGCTask_Name(t *testing.T) {
	task := NewGCTask(GCTaskConfig{})
	if task.Name() != "gc" {
		t.Errorf("Name() = %q, want %q", task.Name(), "gc")
	}
}

func TestGCTask_DefaultRetention(t *testing.T) {
	task := NewGCTask(GCTaskConfig{})
	if task.retentionDays != 90 {
		t.Errorf("retentionDays = %d, want 90", task.retentionDays)
	}
}

func TestGCTask_CustomRetention(t *testing.T) {
	task := NewGCTask(GCTaskConfig{RetentionDays: 30})
	if task.retentionDays != 30 {
		t.Errorf("retentionDays = %d, want 30", task.retentionDays)
	}
}

func TestGCTask_Run_Success(t *testing.T) {
	mock := &mockSessionService{
		gcResult: &service.GCResult{Deleted: 5},
	}
	task := NewGCTask(GCTaskConfig{
		SessionService: mock,
		Logger:         log.Default(),
		RetentionDays:  30,
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.gcCalled {
		t.Error("GarbageCollect was not called")
	}
	if mock.gcReq.OlderThan != "30d" {
		t.Errorf("OlderThan = %q, want %q", mock.gcReq.OlderThan, "30d")
	}
	if mock.gcReq.DryRun {
		t.Error("DryRun should be false")
	}
}

func TestGCTask_Run_Error(t *testing.T) {
	mock := &mockSessionService{
		gcErr: errors.New("store unavailable"),
	}
	task := NewGCTask(GCTaskConfig{
		SessionService: mock,
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !mock.gcCalled {
		t.Error("GarbageCollect was not called")
	}
}

// ── CaptureAllTask Tests ──

func TestCaptureAllTask_Name(t *testing.T) {
	task := NewCaptureAllTask(CaptureAllTaskConfig{})
	if task.Name() != "capture_all" {
		t.Errorf("Name() = %q, want %q", task.Name(), "capture_all")
	}
}

func TestCaptureAllTask_Run_Success(t *testing.T) {
	mock := &mockSessionService{
		captureResults: []*service.CaptureResult{
			{Session: &session.Session{}, Skipped: false},
			{Session: &session.Session{}, Skipped: true},
			{Session: &session.Session{}, Skipped: false},
		},
	}
	task := NewCaptureAllTask(CaptureAllTaskConfig{
		SessionService: mock,
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.captureCalled {
		t.Error("CaptureAll was not called")
	}
}

func TestCaptureAllTask_Run_Error(t *testing.T) {
	mock := &mockSessionService{
		captureErr: errors.New("no providers"),
	}
	task := NewCaptureAllTask(CaptureAllTaskConfig{
		SessionService: mock,
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCaptureAllTask_Run_Empty(t *testing.T) {
	mock := &mockSessionService{
		captureResults: []*service.CaptureResult{},
	}
	task := NewCaptureAllTask(CaptureAllTaskConfig{
		SessionService: mock,
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── StatsReportTask Tests ──

func TestStatsReportTask_Name(t *testing.T) {
	task := NewStatsReportTask(StatsReportTaskConfig{})
	if task.Name() != "stats_report" {
		t.Errorf("Name() = %q, want %q", task.Name(), "stats_report")
	}
}

func TestStatsReportTask_Run_Success(t *testing.T) {
	mock := &mockSessionService{
		statsResult: &service.StatsResult{
			TotalSessions: 42,
			TotalTokens:   100000,
		},
	}
	task := NewStatsReportTask(StatsReportTaskConfig{
		SessionService: mock,
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.statsCalled {
		t.Error("Stats was not called")
	}
}

func TestStatsReportTask_Run_Error(t *testing.T) {
	mock := &mockSessionService{
		statsErr: errors.New("cache broken"),
	}
	task := NewStatsReportTask(StatsReportTaskConfig{
		SessionService: mock,
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

// ── Integration: tasks register and run via Scheduler ──

func TestScheduler_AllNewTasks(t *testing.T) {
	mock := &mockSessionService{
		gcResult:       &service.GCResult{Deleted: 0},
		captureResults: []*service.CaptureResult{},
		statsResult:    &service.StatsResult{},
	}

	s, err := New(Config{
		Entries: []Entry{
			{
				Schedule: "0 3 * * *",
				Task: NewGCTask(GCTaskConfig{
					SessionService: mock,
					RetentionDays:  30,
				}),
			},
			{
				Schedule: "*/30 * * * *",
				Task: NewCaptureAllTask(CaptureAllTaskConfig{
					SessionService: mock,
				}),
			},
			{
				Schedule: "0 * * * *",
				Task: NewStatsReportTask(StatsReportTaskConfig{
					SessionService: mock,
				}),
			},
		},
		Logger: log.Default(),
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Run each task directly to verify they work.
	gcTask := NewGCTask(GCTaskConfig{SessionService: mock, RetentionDays: 30})
	captureTask := NewCaptureAllTask(CaptureAllTaskConfig{SessionService: mock})
	statsTask := NewStatsReportTask(StatsReportTaskConfig{SessionService: mock})

	s.runTask(gcTask)
	s.runTask(captureTask)
	s.runTask(statsTask)

	results := s.Status()
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Verify all three tasks ran without error.
	for _, r := range results {
		if r.Error != "" {
			t.Errorf("task %q had error: %s", r.TaskName, r.Error)
		}
	}
}

// ── Reclassify Task Tests ──

// mockErrorService implements service.ErrorServicer for testing.
type mockErrorService struct {
	processResult *service.ProcessSessionResult
	processErr    error
	processCalled bool
}

func (m *mockErrorService) ProcessSession(sess *session.Session) (*service.ProcessSessionResult, error) {
	m.processCalled = true
	return m.processResult, m.processErr
}
func (m *mockErrorService) GetErrors(_ session.ID) ([]session.SessionError, error) { return nil, nil }
func (m *mockErrorService) GetSummary(_ session.ID) (*session.SessionErrorSummary, error) {
	return nil, nil
}
func (m *mockErrorService) ListRecent(_ int, _ session.ErrorCategory) ([]session.SessionError, error) {
	return nil, nil
}

// mockErrorStore implements storage.ErrorStore for testing reclassify task.
type mockErrorStore struct {
	recentErrors []session.SessionError
	listErr      error
	savedErrors  []session.SessionError
	saveErr      error
}

func (m *mockErrorStore) SaveErrors(errs []session.SessionError) error {
	m.savedErrors = errs
	return m.saveErr
}
func (m *mockErrorStore) GetErrors(_ session.ID) ([]session.SessionError, error) { return nil, nil }
func (m *mockErrorStore) GetErrorSummary(_ session.ID) (*session.SessionErrorSummary, error) {
	return nil, nil
}
func (m *mockErrorStore) ListRecentErrors(_ int, _ session.ErrorCategory) ([]session.SessionError, error) {
	return m.recentErrors, m.listErr
}

func TestReclassifyTask_Name(t *testing.T) {
	task := NewReclassifyTask(ReclassifyConfig{})
	if got := task.Name(); got != "reclassify_errors" {
		t.Errorf("Name() = %q, want %q", got, "reclassify_errors")
	}
}

func TestReclassifyTask_NoUnknownErrors(t *testing.T) {
	store := &mockErrorStore{recentErrors: nil}
	errSvc := &mockErrorService{}
	logger := log.New(log.Writer(), "", 0)

	task := NewReclassifyTask(ReclassifyConfig{
		ErrorService: errSvc,
		Store:        store,
		Logger:       logger,
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if errSvc.processCalled {
		t.Error("ProcessSession should not be called when there are no unknown errors")
	}
}

func TestReclassifyTask_ReclassifiesErrors(t *testing.T) {
	unknowns := []session.SessionError{
		{
			ID:        "err-1",
			SessionID: "sess-1",
			Category:  session.ErrorCategoryUnknown,
			Source:    session.ErrorSourceTool,
			Message:   "some unknown error",
		},
		{
			ID:        "err-2",
			SessionID: "sess-1",
			Category:  session.ErrorCategoryUnknown,
			Source:    session.ErrorSourceTool,
			Message:   "another unknown error",
		},
	}

	store := &mockErrorStore{recentErrors: unknowns}
	errSvc := &mockErrorService{
		processResult: &service.ProcessSessionResult{
			SessionID:  "sess-1",
			Processed:  2,
			ByCategory: map[session.ErrorCategory]int{session.ErrorCategoryToolError: 2},
		},
	}
	logger := log.New(log.Writer(), "", 0)

	task := NewReclassifyTask(ReclassifyConfig{
		ErrorService: errSvc,
		Store:        store,
		Logger:       logger,
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !errSvc.processCalled {
		t.Error("ProcessSession should have been called")
	}
}

func TestReclassifyTask_StoreError(t *testing.T) {
	store := &mockErrorStore{listErr: errors.New("db down")}
	errSvc := &mockErrorService{}
	logger := log.New(log.Writer(), "", 0)

	task := NewReclassifyTask(ReclassifyConfig{
		ErrorService: errSvc,
		Store:        store,
		Logger:       logger,
	})

	err := task.Run(context.Background())
	if err == nil {
		t.Error("expected error when store fails")
	}
}

func TestReclassifyTask_ContextCancelled(t *testing.T) {
	unknowns := []session.SessionError{
		{ID: "err-1", SessionID: "sess-1", Category: session.ErrorCategoryUnknown},
		{ID: "err-2", SessionID: "sess-2", Category: session.ErrorCategoryUnknown},
	}

	store := &mockErrorStore{recentErrors: unknowns}
	errSvc := &mockErrorService{
		processResult: &service.ProcessSessionResult{
			Processed:  1,
			ByCategory: map[session.ErrorCategory]int{session.ErrorCategoryToolError: 1},
		},
	}
	logger := log.New(log.Writer(), "", 0)

	task := NewReclassifyTask(ReclassifyConfig{
		ErrorService: errSvc,
		Store:        store,
		Logger:       logger,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	err := task.Run(ctx)
	if err == nil {
		t.Error("expected context cancellation error")
	}
}

// Suppress unused import warnings — these types are used in the mock.
var (
	_ analysis.SessionAnalysis
	_ session.SessionLink
)
