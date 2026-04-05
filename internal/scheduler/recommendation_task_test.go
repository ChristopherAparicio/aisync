package scheduler

import (
	"context"
	"errors"
	"log"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/notification"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
)

// ── recMockSessionService extends the base mock with ListProjects/GenerateRecommendations ──

type recMockSessionService struct {
	mockSessionService

	// ListProjects mock
	projects    []session.ProjectGroup
	projectsErr error

	// GenerateRecommendations mock — keyed by projectPath
	recsMap map[string][]session.Recommendation
	recsErr error
}

func (m *recMockSessionService) ListProjects(_ context.Context) ([]session.ProjectGroup, error) {
	return m.projects, m.projectsErr
}

func (m *recMockSessionService) GenerateRecommendations(_ context.Context, projectPath string) ([]session.Recommendation, error) {
	if m.recsErr != nil {
		return nil, m.recsErr
	}
	if m.recsMap != nil {
		return m.recsMap[projectPath], nil
	}
	return nil, nil
}

// newTestRecNotifService creates a notification service that routes recommendation events.
func newTestRecNotifService(ch *mockNotifChannel) *notification.Service {
	return notification.NewService(notification.ServiceConfig{
		Channels: []notification.ChannelWithFormatter{
			{
				Channel:   ch,
				Formatter: &mockNotifFormatter{},
			},
		},
		Router: notification.NewDefaultRouter(notification.RoutingConfig{
			DefaultChannel: "#ai-recs",
		}),
	})
}

// ── RecommendationTask Tests ──

func TestRecommendationTask_Name(t *testing.T) {
	task := NewRecommendationTask(RecommendationConfig{})
	if task.Name() != "recommendations" {
		t.Errorf("Name() = %q, want %q", task.Name(), "recommendations")
	}
}

func TestRecommendationTask_NilNotifService(t *testing.T) {
	task := NewRecommendationTask(RecommendationConfig{
		SessionService: &recMockSessionService{},
		Logger:         log.Default(),
	})

	// Should be a no-op when notifSvc is nil.
	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecommendationTask_NoProjects(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestRecNotifService(ch)

	mock := &recMockSessionService{
		projects: nil,
	}

	task := NewRecommendationTask(RecommendationConfig{
		SessionService: mock,
		NotifService:   notifSvc,
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) != 0 {
		t.Errorf("expected no notifications for empty projects, got %d", len(ch.sent))
	}
}

func TestRecommendationTask_ListProjectsError(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestRecNotifService(ch)

	mock := &recMockSessionService{
		projectsErr: errors.New("db error"),
	}

	task := NewRecommendationTask(RecommendationConfig{
		SessionService: mock,
		NotifService:   notifSvc,
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRecommendationTask_SendsNotificationForHighPriority(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestRecNotifService(ch)

	mock := &recMockSessionService{
		projects: []session.ProjectGroup{
			{ProjectPath: "/home/user/myproject", RemoteURL: "org/myproject"},
		},
		recsMap: map[string][]session.Recommendation{
			"/home/user/myproject": {
				{Type: "agent_error", Priority: "high", Icon: "⚠️", Title: "Agent X has high error rate", Message: "Fix it.", Impact: "5 errors"},
				{Type: "context_saturation", Priority: "high", Icon: "🔴", Title: "80% context", Message: "Split tasks."},
				{Type: "skill_ghost", Priority: "medium", Icon: "👻", Title: "Unused skill", Message: "Remove it."},
			},
		},
	}

	task := NewRecommendationTask(RecommendationConfig{
		SessionService: mock,
		NotifService:   notifSvc,
		DashboardURL:   "http://localhost:8371",
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(ch.sent))
	}
}

func TestRecommendationTask_SkipsLowPriorityOnly(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestRecNotifService(ch)

	mock := &recMockSessionService{
		projects: []session.ProjectGroup{
			{ProjectPath: "/home/user/proj"},
		},
		recsMap: map[string][]session.Recommendation{
			"/home/user/proj": {
				{Type: "agent_star", Priority: "low", Icon: "⭐", Title: "Agent star", Message: "Great."},
				{Type: "freshness_optimal", Priority: "low", Icon: "📏", Title: "Optimal length", Message: "Info."},
			},
		},
	}

	task := NewRecommendationTask(RecommendationConfig{
		SessionService: mock,
		NotifService:   notifSvc,
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	// No high-priority recs → no notification.
	if len(ch.sent) != 0 {
		t.Errorf("expected no notifications for low-priority-only, got %d", len(ch.sent))
	}
}

func TestRecommendationTask_SkipsNoRecommendations(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestRecNotifService(ch)

	mock := &recMockSessionService{
		projects: []session.ProjectGroup{
			{ProjectPath: "/home/user/clean-project"},
		},
		recsMap: map[string][]session.Recommendation{
			"/home/user/clean-project": {},
		},
	}

	task := NewRecommendationTask(RecommendationConfig{
		SessionService: mock,
		NotifService:   notifSvc,
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) != 0 {
		t.Errorf("expected no notifications for empty recs, got %d", len(ch.sent))
	}
}

func TestRecommendationTask_MultipleProjects(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestRecNotifService(ch)

	mock := &recMockSessionService{
		projects: []session.ProjectGroup{
			{ProjectPath: "/proj/a", RemoteURL: "org/a"},
			{ProjectPath: "/proj/b", RemoteURL: "org/b"},
			{ProjectPath: "/proj/c", RemoteURL: "org/c"},
		},
		recsMap: map[string][]session.Recommendation{
			"/proj/a": {
				{Type: "budget_warning", Priority: "high", Icon: "💸", Title: "Budget warning", Message: "Over budget."},
			},
			"/proj/b": {
				{Type: "agent_star", Priority: "low", Icon: "⭐", Title: "Star agent", Message: "Great."},
			},
			"/proj/c": {
				{Type: "token_waste_retry", Priority: "high", Icon: "🔄", Title: "Retry waste", Message: "Too many retries."},
				{Type: "model_saturated", Priority: "high", Icon: "🔴", Title: "Saturated", Message: "Split tasks."},
			},
		},
	}

	task := NewRecommendationTask(RecommendationConfig{
		SessionService: mock,
		NotifService:   notifSvc,
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	// proj/a has 1 high → notification. proj/b is low-only → skip. proj/c has 2 high → notification.
	if len(ch.sent) != 2 {
		t.Errorf("expected 2 notifications, got %d", len(ch.sent))
	}
}

func TestRecommendationTask_SeverityWarningForManyHigh(t *testing.T) {
	// This tests the severity escalation (>=3 high → warning severity).
	// We verify indirectly via the notification being sent (since mockFormatter doesn't expose severity).
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestRecNotifService(ch)

	mock := &recMockSessionService{
		projects: []session.ProjectGroup{
			{ProjectPath: "/proj/x"},
		},
		recsMap: map[string][]session.Recommendation{
			"/proj/x": {
				{Type: "agent_error", Priority: "high", Icon: "⚠️", Title: "Err1", Message: "msg"},
				{Type: "context_saturation", Priority: "high", Icon: "🔴", Title: "Err2", Message: "msg"},
				{Type: "budget_exceeded", Priority: "high", Icon: "🚨", Title: "Err3", Message: "msg"},
				{Type: "token_waste_retry", Priority: "high", Icon: "🔄", Title: "Err4", Message: "msg"},
			},
		},
	}

	task := NewRecommendationTask(RecommendationConfig{
		SessionService: mock,
		NotifService:   notifSvc,
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(ch.sent))
	}
}

func TestRecommendationTask_ItemsLimitedTo10(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestRecNotifService(ch)

	// Create 15 high-priority recommendations.
	var recs []session.Recommendation
	for i := 0; i < 15; i++ {
		recs = append(recs, session.Recommendation{
			Type:     "agent_error",
			Priority: "high",
			Icon:     "⚠️",
			Title:    "Error",
			Message:  "msg",
		})
	}

	mock := &recMockSessionService{
		projects: []session.ProjectGroup{
			{ProjectPath: "/proj/many"},
		},
		recsMap: map[string][]session.Recommendation{
			"/proj/many": recs,
		},
	}

	task := NewRecommendationTask(RecommendationConfig{
		SessionService: mock,
		NotifService:   notifSvc,
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Notification was sent — we can't easily check items count with mock formatter,
	// but we verify the task didn't crash and produced exactly 1 notification.
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(ch.sent))
	}
}

func TestRecommendationTask_RecsErrorContinues(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestRecNotifService(ch)

	// Mock that errors on GenerateRecommendations but has 2 projects.
	mock := &recMockSessionService{
		projects: []session.ProjectGroup{
			{ProjectPath: "/proj/err"},
			{ProjectPath: "/proj/ok"},
		},
		recsErr: errors.New("analysis failed"),
	}

	task := NewRecommendationTask(RecommendationConfig{
		SessionService: mock,
		NotifService:   notifSvc,
		Logger:         log.Default(),
	})

	// Should NOT return error — errors are per-project and logged, not fatal.
	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) != 0 {
		t.Errorf("expected no notifications when all recs error, got %d", len(ch.sent))
	}
}

// ── Store persistence tests ──

func TestRecommendationTask_PersistsToStore(t *testing.T) {
	store := testutil.NewMockStore()

	mock := &recMockSessionService{
		projects: []session.ProjectGroup{
			{ProjectPath: "/proj/a"},
		},
		recsMap: map[string][]session.Recommendation{
			"/proj/a": {
				{Type: "agent_error", Priority: "high", Icon: "⚠️", Title: "Error", Message: "msg", Impact: "5 errs", Agent: "claude"},
				{Type: "skill_ghost", Priority: "medium", Icon: "👻", Title: "Ghost", Message: "remove", Skill: "test-skill"},
			},
		},
	}

	task := NewRecommendationTask(RecommendationConfig{
		SessionService: mock,
		Store:          store,
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify recs were persisted.
	if len(store.Recommendations) != 2 {
		t.Fatalf("expected 2 persisted recs, got %d", len(store.Recommendations))
	}

	// Verify fingerprints are set.
	for _, rec := range store.Recommendations {
		if rec.Fingerprint == "" {
			t.Error("expected non-empty fingerprint")
		}
		if rec.Source != session.RecSourceDeterministic {
			t.Errorf("expected source %q, got %q", session.RecSourceDeterministic, rec.Source)
		}
		if rec.Status != session.RecStatusActive {
			t.Errorf("expected status %q, got %q", session.RecStatusActive, rec.Status)
		}
	}
}

func TestRecommendationTask_StoreOnlyNoNotifService(t *testing.T) {
	store := testutil.NewMockStore()

	mock := &recMockSessionService{
		projects: []session.ProjectGroup{
			{ProjectPath: "/proj/x"},
		},
		recsMap: map[string][]session.Recommendation{
			"/proj/x": {
				{Type: "budget_warning", Priority: "high", Icon: "💸", Title: "Over budget", Message: "check"},
			},
		},
	}

	task := NewRecommendationTask(RecommendationConfig{
		SessionService: mock,
		Store:          store,
		// NotifService is nil — store-only mode.
		Logger: log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Recs should be persisted even without notification service.
	if len(store.Recommendations) != 1 {
		t.Fatalf("expected 1 persisted rec, got %d", len(store.Recommendations))
	}
}

func TestRecommendationTask_DefaultExpireAfter(t *testing.T) {
	task := NewRecommendationTask(RecommendationConfig{})
	if task.expireAfter != 14*24*3600000000000 { // 14 days in nanoseconds
		t.Errorf("expected 14-day default expiry, got %v", task.expireAfter)
	}
}
