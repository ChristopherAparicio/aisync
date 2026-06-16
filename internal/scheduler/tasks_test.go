package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/search"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
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
func (m *mockSessionService) ExportAll(_ service.ExportAllRequest) (*service.ExportAllResult, error) {
	return nil, nil
}
func (m *mockSessionService) ImportBundle(_ service.ImportRequest) (*service.ImportBundleResult, error) {
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
func (m *mockSessionService) WorkItems(_ context.Context, _ service.WorkItemRequest) (*session.WorkItemList, error) {
	return nil, nil
}
func (m *mockSessionService) WorkItem(_ context.Context, _ string) (*session.WorkItem, error) {
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
func (m *mockSessionService) Diagnose(_ context.Context, _ service.DiagnoseRequest) (*session.DiagnosisReport, error) {
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

// Manual tag stubs (PR2): the scheduler does not exercise tagging.
func (m *mockSessionService) AddTags(_ context.Context, _ session.ID, _ []string) (int, error) {
	return 0, nil
}
func (m *mockSessionService) RemoveTags(_ context.Context, _ session.ID, _ []string) (int, error) {
	return 0, nil
}
func (m *mockSessionService) GetSessionTags(_ context.Context, _ session.ID) ([]string, error) {
	return nil, nil
}
func (m *mockSessionService) ListAllTags(_ context.Context) ([]session.TagCount, error) {
	return nil, nil
}
func (m *mockSessionService) ResolveCurrentSessionID(_ context.Context, _ string) (session.ID, error) {
	return "", service.ErrNoCurrentSession
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
		},
		Logger: log.Default(),
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Run each task directly to verify they work.
	gcTask := NewGCTask(GCTaskConfig{SessionService: mock, RetentionDays: 30})
	captureTask := NewCaptureAllTask(CaptureAllTaskConfig{SessionService: mock})

	s.runTask(gcTask)
	s.runTask(captureTask)

	results := s.Status()
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Verify all tasks ran without error.
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
func (m *mockErrorService) ListUnclassifiedGroups(_ int) ([]session.ErrorFingerprintGroup, error) {
	return nil, nil
}
func (m *mockErrorService) ClassifyGroup(_ string, _ session.ErrorCategory, _ string, _ string) error {
	return nil
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
func (m *mockErrorStore) UpsertFingerprint(_ session.ErrorFingerprintGroup) error { return nil }
func (m *mockErrorStore) ListFingerprintGroups(_ bool, _ int) ([]session.ErrorFingerprintGroup, error) {
	return nil, nil
}
func (m *mockErrorStore) GetFingerprintGroup(_ string) (*session.ErrorFingerprintGroup, error) {
	return nil, nil
}
func (m *mockErrorStore) ClassifyFingerprintGroup(_ string, _ session.ErrorCategory, _ string, _ string) error {
	return nil
}
func (m *mockErrorStore) GetFingerprintMatch(_ string) (*session.ErrorFingerprintGroup, error) {
	return nil, nil
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

// NOTE: SaturationTask, CacheEfficiencyTask, DashboardWarmTask, and
// StatsReportTask tests were removed in the Phase 4 CQRS cleanup. Those
// pre-compute tasks were obsoleted by the session_analytics read-model
// and the analytics_backfill task.

// ── ObjectiveBackfillTask Tests ──

// mockObjectiveStore implements the List + ListObjectives methods needed.
type mockObjectiveStore struct {
	storage.Store // embed interface to satisfy it
	sessions      []session.Summary
	objectives    map[session.ID]*session.SessionObjective
	listErr       error
	objErr        error
}

func (m *mockObjectiveStore) List(_ session.ListOptions) ([]session.Summary, error) {
	return m.sessions, m.listErr
}

func (m *mockObjectiveStore) ListObjectives(ids []session.ID) (map[session.ID]*session.SessionObjective, error) {
	if m.objErr != nil {
		return nil, m.objErr
	}
	result := make(map[session.ID]*session.SessionObjective)
	for _, id := range ids {
		if obj, ok := m.objectives[id]; ok {
			result[id] = obj
		}
	}
	return result, nil
}

// mockObjSessionService extends the base mock with objective-specific tracking.
type mockObjSessionService struct {
	mockSessionService
	computedIDs []string
	computeErr  error
}

func (m *mockObjSessionService) ComputeObjective(_ context.Context, req service.ComputeObjectiveRequest) (*session.SessionObjective, error) {
	if m.computeErr != nil {
		return nil, m.computeErr
	}
	m.computedIDs = append(m.computedIDs, req.SessionID)
	return &session.SessionObjective{SessionID: session.ID(req.SessionID)}, nil
}

func TestObjectiveBackfillTask_Name(t *testing.T) {
	task := NewObjectiveBackfillTask(ObjectiveBackfillConfig{})
	if task.Name() != "objective_backfill" {
		t.Errorf("Name() = %q, want %q", task.Name(), "objective_backfill")
	}
}

func TestObjectiveBackfillTask_Defaults(t *testing.T) {
	task := NewObjectiveBackfillTask(ObjectiveBackfillConfig{})
	if task.batchSize != 50 {
		t.Errorf("batchSize = %d, want 50", task.batchSize)
	}
	if task.minMsgs != 5 {
		t.Errorf("minMsgs = %d, want 5", task.minMsgs)
	}
}

func TestObjectiveBackfillTask_SkipsTooFewMessages(t *testing.T) {
	store := &mockObjectiveStore{
		sessions: []session.Summary{
			{ID: "s1", MessageCount: 2}, // too few
			{ID: "s2", MessageCount: 1}, // too few
		},
		objectives: map[session.ID]*session.SessionObjective{},
	}
	svc := &mockObjSessionService{}
	task := NewObjectiveBackfillTask(ObjectiveBackfillConfig{
		SessionService: svc,
		Store:          store,
		Logger:         log.Default(),
		MinMessages:    5,
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(svc.computedIDs) != 0 {
		t.Errorf("expected 0 computed, got %d", len(svc.computedIDs))
	}
}

func TestObjectiveBackfillTask_SkipsExistingObjectives(t *testing.T) {
	store := &mockObjectiveStore{
		sessions: []session.Summary{
			{ID: "s1", MessageCount: 10},
			{ID: "s2", MessageCount: 15},
		},
		objectives: map[session.ID]*session.SessionObjective{
			"s1": {SessionID: "s1"},
			"s2": {SessionID: "s2"},
		},
	}
	svc := &mockObjSessionService{}
	task := NewObjectiveBackfillTask(ObjectiveBackfillConfig{
		SessionService: svc,
		Store:          store,
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(svc.computedIDs) != 0 {
		t.Errorf("expected 0 computed (all exist), got %d", len(svc.computedIDs))
	}
}

func TestObjectiveBackfillTask_ComputesMissingObjectives(t *testing.T) {
	store := &mockObjectiveStore{
		sessions: []session.Summary{
			{ID: "s1", MessageCount: 10},
			{ID: "s2", MessageCount: 20},
			{ID: "s3", MessageCount: 8},
		},
		objectives: map[session.ID]*session.SessionObjective{
			"s1": {SessionID: "s1"}, // already has objective
		},
	}
	svc := &mockObjSessionService{}
	task := NewObjectiveBackfillTask(ObjectiveBackfillConfig{
		SessionService: svc,
		Store:          store,
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(svc.computedIDs) != 2 {
		t.Fatalf("expected 2 computed, got %d: %v", len(svc.computedIDs), svc.computedIDs)
	}
}

func TestObjectiveBackfillTask_RespectssBatchSize(t *testing.T) {
	sessions := make([]session.Summary, 20)
	for i := range sessions {
		sessions[i] = session.Summary{
			ID:           session.ID(fmt.Sprintf("s%d", i)),
			MessageCount: 10,
		}
	}
	store := &mockObjectiveStore{
		sessions:   sessions,
		objectives: map[session.ID]*session.SessionObjective{},
	}
	svc := &mockObjSessionService{}
	task := NewObjectiveBackfillTask(ObjectiveBackfillConfig{
		SessionService: svc,
		Store:          store,
		Logger:         log.Default(),
		BatchSize:      5,
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(svc.computedIDs) != 5 {
		t.Errorf("expected 5 computed (batch cap), got %d", len(svc.computedIDs))
	}
}

func TestObjectiveBackfillTask_ListError(t *testing.T) {
	store := &mockObjectiveStore{
		listErr: errors.New("db down"),
	}
	task := NewObjectiveBackfillTask(ObjectiveBackfillConfig{
		SessionService: &mockObjSessionService{},
		Store:          store,
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err == nil {
		t.Error("expected error from List failure")
	}
}

func TestObjectiveBackfillTask_ComputeErrorIsCounted(t *testing.T) {
	store := &mockObjectiveStore{
		sessions: []session.Summary{
			{ID: "s1", MessageCount: 10},
			{ID: "s2", MessageCount: 10},
		},
		objectives: map[session.ID]*session.SessionObjective{},
	}
	svc := &mockObjSessionService{computeErr: errors.New("llm down")}
	task := NewObjectiveBackfillTask(ObjectiveBackfillConfig{
		SessionService: svc,
		Store:          store,
		Logger:         log.Default(),
	})

	// Should not return error — individual failures are logged, not fatal.
	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(svc.computedIDs) != 0 {
		t.Errorf("expected 0 successful computes, got %d", len(svc.computedIDs))
	}
}

// Suppress unused import warnings — these types are used in the mock.
var (
	_ analysis.SessionAnalysis
	_ session.SessionLink
)
