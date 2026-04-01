package scheduler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/webhooks"
)

// webhookEvent mirrors the webhook payload for test assertions.
type webhookEvent struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

// TestIntegration_BudgetAlert_MonthlyExceeded tests the full pipeline:
// BudgetCheckTask → BudgetStatus (exceeded) → Webhook Fire → HTTP delivery.
func TestIntegration_BudgetAlert_MonthlyExceeded(t *testing.T) {
	var mu sync.Mutex
	var received []webhookEvent

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("X-AiSync-Event") != "budget.alert" {
			t.Errorf("event header = %s, want budget.alert", r.Header.Get("X-AiSync-Event"))
		}

		var evt webhookEvent
		if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		mu.Lock()
		received = append(received, evt)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dispatcher := webhooks.New(webhooks.Config{
		Hooks: []webhooks.HookConfig{
			{URL: srv.URL, Events: []webhooks.EventType{webhooks.EventBudgetAlert}},
		},
		Logger: log.Default(),
	})

	// Mock service returns one project that exceeded monthly budget.
	svc := &budgetMockService{
		statuses: []session.BudgetStatus{
			{
				ProjectName:    "backend",
				ProjectPath:    "/projects/backend",
				MonthlyLimit:   100.0,
				MonthlySpent:   115.0,
				MonthlyPercent: 115.0,
				MonthlyAlert:   "exceeded",
				ProjectedMonth: 150.0,
				DaysRemaining:  10,
			},
		},
	}

	task := NewBudgetCheckTask(BudgetCheckConfig{
		SessionService: svc,
		Dispatcher:     dispatcher,
		Logger:         log.Default(),
	})

	if task.Name() != "budget_check" {
		t.Errorf("Name = %s, want budget_check", task.Name())
	}

	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Wait for async webhook delivery.
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("received %d webhooks, want 1", len(received))
	}

	evt := received[0]
	if evt.Type != "budget.alert" {
		t.Errorf("event type = %s, want budget.alert", evt.Type)
	}

	// Verify payload fields.
	data := evt.Data
	if data["project"] != "backend" {
		t.Errorf("project = %v, want backend", data["project"])
	}
	if data["alert_type"] != "monthly" {
		t.Errorf("alert_type = %v, want monthly", data["alert_type"])
	}
	if data["alert_level"] != "exceeded" {
		t.Errorf("alert_level = %v, want exceeded", data["alert_level"])
	}
	// JSON unmarshals numbers as float64.
	if spent, ok := data["spent"].(float64); !ok || spent != 115.0 {
		t.Errorf("spent = %v, want 115", data["spent"])
	}
	if limit, ok := data["limit"].(float64); !ok || limit != 100.0 {
		t.Errorf("limit = %v, want 100", data["limit"])
	}
}

// TestIntegration_BudgetAlert_DailyWarning tests daily budget warnings.
func TestIntegration_BudgetAlert_DailyWarning(t *testing.T) {
	var mu sync.Mutex
	var received []webhookEvent

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var evt webhookEvent
		_ = json.NewDecoder(r.Body).Decode(&evt)
		mu.Lock()
		received = append(received, evt)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dispatcher := webhooks.New(webhooks.Config{
		Hooks: []webhooks.HookConfig{
			{URL: srv.URL}, // all events
		},
		Logger: log.Default(),
	})

	svc := &budgetMockService{
		statuses: []session.BudgetStatus{
			{
				ProjectName:  "frontend",
				DailyLimit:   10.0,
				DailySpent:   8.5,
				DailyPercent: 85.0,
				DailyAlert:   "warning",
			},
		},
	}

	task := NewBudgetCheckTask(BudgetCheckConfig{
		SessionService: svc,
		Dispatcher:     dispatcher,
		Logger:         log.Default(),
	})

	if err := task.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("received %d webhooks, want 1", len(received))
	}
	if received[0].Data["alert_type"] != "daily" {
		t.Errorf("alert_type = %v, want daily", received[0].Data["alert_type"])
	}
	if received[0].Data["alert_level"] != "warning" {
		t.Errorf("alert_level = %v, want warning", received[0].Data["alert_level"])
	}
}

// TestIntegration_BudgetAlert_MultipleProjects tests alerts for multiple projects.
func TestIntegration_BudgetAlert_MultipleProjects(t *testing.T) {
	var mu sync.Mutex
	var received []webhookEvent

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var evt webhookEvent
		_ = json.NewDecoder(r.Body).Decode(&evt)
		mu.Lock()
		received = append(received, evt)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dispatcher := webhooks.New(webhooks.Config{
		Hooks:  []webhooks.HookConfig{{URL: srv.URL}},
		Logger: log.Default(),
	})

	svc := &budgetMockService{
		statuses: []session.BudgetStatus{
			{
				ProjectName:    "backend",
				MonthlyLimit:   100.0,
				MonthlySpent:   110.0,
				MonthlyPercent: 110.0,
				MonthlyAlert:   "exceeded",
				DaysRemaining:  5,
			},
			{
				ProjectName:    "frontend",
				MonthlyLimit:   50.0,
				MonthlySpent:   45.0,
				MonthlyPercent: 90.0,
				MonthlyAlert:   "warning",
				DaysRemaining:  5,
			},
			{
				ProjectName:    "infra",
				MonthlyLimit:   200.0,
				MonthlySpent:   30.0,
				MonthlyPercent: 15.0,
				// No alert — under threshold.
			},
		},
	}

	task := NewBudgetCheckTask(BudgetCheckConfig{
		SessionService: svc,
		Dispatcher:     dispatcher,
		Logger:         log.Default(),
	})

	if err := task.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Only backend (exceeded) and frontend (warning) should fire — infra has no alert.
	if len(received) != 2 {
		t.Fatalf("received %d webhooks, want 2 (backend exceeded + frontend warning)", len(received))
	}

	// Verify projects in received (order may vary due to goroutines).
	projects := make(map[string]string)
	for _, evt := range received {
		name, _ := evt.Data["project"].(string)
		level, _ := evt.Data["alert_level"].(string)
		projects[name] = level
	}
	if projects["backend"] != "exceeded" {
		t.Errorf("backend alert = %s, want exceeded", projects["backend"])
	}
	if projects["frontend"] != "warning" {
		t.Errorf("frontend alert = %s, want warning", projects["frontend"])
	}
}

// TestIntegration_BudgetAlert_NoAlerts verifies no webhooks fire when all projects are under budget.
func TestIntegration_BudgetAlert_NoAlerts(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dispatcher := webhooks.New(webhooks.Config{
		Hooks:  []webhooks.HookConfig{{URL: srv.URL}},
		Logger: log.Default(),
	})

	svc := &budgetMockService{
		statuses: []session.BudgetStatus{
			{ProjectName: "backend", MonthlyLimit: 100, MonthlySpent: 30, MonthlyPercent: 30},
			{ProjectName: "frontend", MonthlyLimit: 50, MonthlySpent: 10, MonthlyPercent: 20},
		},
	}

	task := NewBudgetCheckTask(BudgetCheckConfig{
		SessionService: svc,
		Dispatcher:     dispatcher,
		Logger:         log.Default(),
	})

	if err := task.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)

	if callCount != 0 {
		t.Fatalf("expected 0 webhook calls, got %d", callCount)
	}
}

// TestIntegration_BudgetAlert_NilDispatcher verifies no panic when dispatcher is nil.
func TestIntegration_BudgetAlert_NilDispatcher(t *testing.T) {
	svc := &budgetMockService{
		statuses: []session.BudgetStatus{
			{ProjectName: "test", MonthlyAlert: "exceeded", MonthlyLimit: 100, MonthlySpent: 150},
		},
	}

	task := NewBudgetCheckTask(BudgetCheckConfig{
		SessionService: svc,
		Dispatcher:     nil, // no dispatcher
		Logger:         log.Default(),
	})

	// Should not panic.
	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run with nil dispatcher: %v", err)
	}
}

// budgetMockService is a minimal mock that only implements BudgetStatus.
// It embeds mockSessionService from tasks_test.go for the other methods.
type budgetMockService struct {
	mockSessionService
	statuses []session.BudgetStatus
}

func (m *budgetMockService) BudgetStatus(_ context.Context) ([]session.BudgetStatus, error) {
	return m.statuses, nil
}
