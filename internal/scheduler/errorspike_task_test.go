package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/notification"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── ErrorSpikeTask Tests ──

func TestErrorSpikeTask_Name(t *testing.T) {
	task := NewErrorSpikeTask(ErrorSpikeConfig{})
	if task.Name() != "error_spike" {
		t.Errorf("Name() = %q, want %q", task.Name(), "error_spike")
	}
}

func TestErrorSpikeTask_Defaults(t *testing.T) {
	task := NewErrorSpikeTask(ErrorSpikeConfig{})
	if task.threshold != 10 {
		t.Errorf("threshold = %d, want 10", task.threshold)
	}
	if task.windowMins != 60 {
		t.Errorf("windowMins = %d, want 60", task.windowMins)
	}
}

func TestErrorSpikeTask_CustomConfig(t *testing.T) {
	task := NewErrorSpikeTask(ErrorSpikeConfig{
		Threshold:  5,
		WindowMins: 30,
	})
	if task.threshold != 5 {
		t.Errorf("threshold = %d, want 5", task.threshold)
	}
	if task.windowMins != 30 {
		t.Errorf("windowMins = %d, want 30", task.windowMins)
	}
}

func TestErrorSpikeTask_NilNotifService(t *testing.T) {
	store := &mockErrorStore{
		recentErrors: []session.SessionError{
			{ID: "e1", SessionID: "s1", Category: session.ErrorCategoryToolError, OccurredAt: time.Now()},
		},
	}
	task := NewErrorSpikeTask(ErrorSpikeConfig{
		Store:  store,
		Logger: log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestErrorSpikeTask_NilStore(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestErrorSpikeNotifService(ch)

	task := NewErrorSpikeTask(ErrorSpikeConfig{
		NotifService: notifSvc,
		Logger:       log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestErrorSpikeTask_NoErrors(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestErrorSpikeNotifService(ch)
	store := &mockErrorStore{recentErrors: nil}

	task := NewErrorSpikeTask(ErrorSpikeConfig{
		Store:        store,
		NotifService: notifSvc,
		Logger:       log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) != 0 {
		t.Errorf("expected no notifications for empty errors, got %d", len(ch.sent))
	}
}

func TestErrorSpikeTask_BelowThreshold(t *testing.T) {
	now := time.Now()
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestErrorSpikeNotifService(ch)
	store := &mockErrorStore{
		recentErrors: []session.SessionError{
			{ID: "e1", SessionID: "s1", Category: session.ErrorCategoryToolError, OccurredAt: now.Add(-5 * time.Minute)},
			{ID: "e2", SessionID: "s2", Category: session.ErrorCategoryProviderError, OccurredAt: now.Add(-10 * time.Minute)},
		},
	}

	task := NewErrorSpikeTask(ErrorSpikeConfig{
		Store:        store,
		NotifService: notifSvc,
		Threshold:    5, // need 5, only have 2
		WindowMins:   60,
		Logger:       log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) != 0 {
		t.Errorf("expected no notification below threshold, got %d", len(ch.sent))
	}
}

func TestErrorSpikeTask_ExceedsThreshold_SendsAlert(t *testing.T) {
	now := time.Now()
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestErrorSpikeNotifService(ch)

	// Create 5 errors within the window.
	errs := make([]session.SessionError, 5)
	for i := range errs {
		errs[i] = session.SessionError{
			ID:         fmt.Sprintf("e%d", i),
			SessionID:  session.ID(fmt.Sprintf("s%d", i%3)),
			Category:   session.ErrorCategoryToolError,
			OccurredAt: now.Add(-time.Duration(i) * time.Minute),
		}
	}
	store := &mockErrorStore{recentErrors: errs}

	task := NewErrorSpikeTask(ErrorSpikeConfig{
		Store:        store,
		NotifService: notifSvc,
		Threshold:    3, // need 3, have 5
		WindowMins:   60,
		DashboardURL: "http://localhost:8371",
		Logger:       log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Notify is async — give it a moment.
	time.Sleep(50 * time.Millisecond)

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) == 0 {
		t.Error("expected at least one notification to be sent")
	}
}

func TestErrorSpikeTask_OldErrorsFilteredOut(t *testing.T) {
	now := time.Now()
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestErrorSpikeNotifService(ch)

	// 2 recent + 3 old (outside 30-min window).
	errs := []session.SessionError{
		{ID: "e1", SessionID: "s1", Category: session.ErrorCategoryToolError, OccurredAt: now.Add(-5 * time.Minute)},
		{ID: "e2", SessionID: "s2", Category: session.ErrorCategoryToolError, OccurredAt: now.Add(-10 * time.Minute)},
		{ID: "e3", SessionID: "s3", Category: session.ErrorCategoryToolError, OccurredAt: now.Add(-45 * time.Minute)},
		{ID: "e4", SessionID: "s4", Category: session.ErrorCategoryToolError, OccurredAt: now.Add(-50 * time.Minute)},
		{ID: "e5", SessionID: "s5", Category: session.ErrorCategoryToolError, OccurredAt: now.Add(-55 * time.Minute)},
	}
	store := &mockErrorStore{recentErrors: errs}

	task := NewErrorSpikeTask(ErrorSpikeConfig{
		Store:        store,
		NotifService: notifSvc,
		Threshold:    3, // need 3, only 2 within window
		WindowMins:   30,
		Logger:       log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) != 0 {
		t.Errorf("expected no notification (only 2 in window), got %d", len(ch.sent))
	}
}

func TestErrorSpikeTask_CriticalSeverity(t *testing.T) {
	now := time.Now()
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestErrorSpikeNotifService(ch)

	// Create 15 errors (>= 3x threshold of 5).
	errs := make([]session.SessionError, 15)
	for i := range errs {
		errs[i] = session.SessionError{
			ID:         fmt.Sprintf("e%d", i),
			SessionID:  "s1",
			Category:   session.ErrorCategoryProviderError,
			OccurredAt: now.Add(-time.Duration(i) * time.Minute),
		}
	}
	store := &mockErrorStore{recentErrors: errs}

	task := NewErrorSpikeTask(ErrorSpikeConfig{
		Store:        store,
		NotifService: notifSvc,
		Threshold:    5,
		WindowMins:   60,
		Logger:       log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The notification is sent — severity is checked by the formatter, not here.
	// We verify the task ran without error and sent a notification.
	time.Sleep(50 * time.Millisecond)

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) == 0 {
		t.Error("expected at least one notification for critical spike")
	}
}

func TestErrorSpikeTask_StoreError(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestErrorSpikeNotifService(ch)
	store := &mockErrorStore{listErr: errors.New("db down")}

	task := NewErrorSpikeTask(ErrorSpikeConfig{
		Store:        store,
		NotifService: notifSvc,
		Logger:       log.Default(),
	})

	err := task.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when store fails")
	}
}

func TestErrorSpikeTask_MultipleCategories(t *testing.T) {
	now := time.Now()
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestErrorSpikeNotifService(ch)

	errs := []session.SessionError{
		{ID: "e1", SessionID: "s1", Category: session.ErrorCategoryToolError, OccurredAt: now.Add(-1 * time.Minute)},
		{ID: "e2", SessionID: "s2", Category: session.ErrorCategoryProviderError, OccurredAt: now.Add(-2 * time.Minute)},
		{ID: "e3", SessionID: "s3", Category: session.ErrorCategoryRateLimit, OccurredAt: now.Add(-3 * time.Minute)},
	}
	store := &mockErrorStore{recentErrors: errs}

	task := NewErrorSpikeTask(ErrorSpikeConfig{
		Store:        store,
		NotifService: notifSvc,
		Threshold:    3,
		WindowMins:   60,
		Logger:       log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Wait for async notification.
	time.Sleep(50 * time.Millisecond)

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) == 0 {
		t.Error("expected notification for 3 errors across categories")
	}
}

// ── Helpers ──

// newTestErrorSpikeNotifService creates a notification service configured for error spike alerts.
func newTestErrorSpikeNotifService(ch *mockNotifChannel) *notification.Service {
	return notification.NewService(notification.ServiceConfig{
		Channels: []notification.ChannelWithFormatter{
			{
				Channel:   ch,
				Formatter: &mockNotifFormatter{},
			},
		},
		Router: notification.NewDefaultRouter(notification.RoutingConfig{
			DefaultChannel: "#test",
			Alerts: notification.AlertConfig{
				Errors: true, // enable error spike routing
			},
		}),
	})
}
