package scheduler

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/notification"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/stalldetector"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
)

func TestStallAlertTask_Name(t *testing.T) {
	task := NewStallAlertTask(StallAlertTaskConfig{})
	if task.Name() != "stall_alert" {
		t.Errorf("Name() = %q, want %q", task.Name(), "stall_alert")
	}
}

func TestStallAlertTask_NilStore_NoOp(t *testing.T) {
	task := NewStallAlertTask(StallAlertTaskConfig{})
	if err := task.Run(context.Background()); err != nil {
		t.Errorf("Run() with nil store should be no-op, got: %v", err)
	}
}

func TestStallAlertTask_NilNotifService_NoOp(t *testing.T) {
	store := testutil.NewMockStore()
	task := NewStallAlertTask(StallAlertTaskConfig{Store: store})
	if err := task.Run(context.Background()); err != nil {
		t.Errorf("Run() with nil notif service should be no-op, got: %v", err)
	}
}

func TestStallAlertTask_NoThresholdsCrossed_NoNotify(t *testing.T) {
	store := testutil.NewMockStore()
	now := time.Now().UTC()

	// Insert one live stall — below the live threshold of 5.
	_ = store.UpsertStall(&session.SessionStall{
		ProviderSessionID: "ses_1",
		StartedAt:         now.Add(-20 * time.Minute),
		DetectedAt:        now.Add(-10 * time.Minute),
		RootCause:         session.StallRootCauseStreamStall,
		Provider:          "anthropic",
	})

	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestStallAlertNotifService(ch)

	task := NewStallAlertTask(StallAlertTaskConfig{
		Store:        store,
		NotifService: notifSvc,
		Thresholds: stalldetector.AlertThresholds{
			LiveCount:    5,
			NewStalls24h: 10,
			CostLost24h:  1.0,
		},
		Logger: log.Default(),
		Now:    func() time.Time { return now },
	})

	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) != 0 {
		t.Errorf("expected no notification, got %d", len(ch.sent))
	}
}

func TestStallAlertTask_LiveThresholdCrossed_SendsAlert(t *testing.T) {
	store := testutil.NewMockStore()
	now := time.Now().UTC()

	// Insert 6 live stalls (>= threshold of 5).
	for i := 0; i < 6; i++ {
		_ = store.UpsertStall(&session.SessionStall{
			ProviderSessionID: "ses_" + string(rune('a'+i)),
			StartedAt:         now.Add(-time.Duration(20+i) * time.Minute),
			DetectedAt:        now.Add(-time.Duration(5+i) * time.Minute),
			RootCause:         session.StallRootCauseStreamStall,
			Provider:          "anthropic",
		})
	}

	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestStallAlertNotifService(ch)

	task := NewStallAlertTask(StallAlertTaskConfig{
		Store:        store,
		NotifService: notifSvc,
		Thresholds:   stalldetector.AlertThresholds{LiveCount: 5},
		DashboardURL: "http://localhost:8371",
		Logger:       log.Default(),
		Now:          func() time.Time { return now },
	})

	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) == 0 {
		t.Errorf("expected notification, got 0")
	}
}

func TestStallAlertTask_CostThresholdCrossed_SendsAlert(t *testing.T) {
	store := testutil.NewMockStore()
	now := time.Now().UTC()
	endedAt := now.Add(-1 * time.Hour)

	// Insert one sealed stall with $2 lost in the last 24h.
	_ = store.UpsertStall(&session.SessionStall{
		ProviderSessionID: "ses_x",
		StartedAt:         now.Add(-2 * time.Hour),
		DetectedAt:        now.Add(-90 * time.Minute),
		EndedAt:           &endedAt,
		RootCause:         session.StallRootCauseAborted,
		Provider:          "openai",
		CostLostUSD:       2.0,
		TokensLost:        100000,
	})

	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestStallAlertNotifService(ch)

	task := NewStallAlertTask(StallAlertTaskConfig{
		Store:        store,
		NotifService: notifSvc,
		Thresholds: stalldetector.AlertThresholds{
			CostLost24h: 1.0,
		},
		Logger: log.Default(),
		Now:    func() time.Time { return now },
	})

	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) == 0 {
		t.Fatal("expected notification, got 0")
	}
}

// newTestStallAlertNotifService creates a notification service configured for stall alerts.
func newTestStallAlertNotifService(ch *mockNotifChannel) *notification.Service {
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
				Stalls: true,
			},
		}),
	})
}
