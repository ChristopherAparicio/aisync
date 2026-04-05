package scheduler

import (
	"context"
	"errors"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/notification"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// errDigestMock is a sentinel error for digest task tests.
var errDigestMock = errors.New("mock error")

// ── Mock notification channel for testing ──

type mockNotifChannel struct {
	mu      sync.Mutex
	sent    []notification.RenderedMessage
	sendErr error
	nameLit string
}

func (c *mockNotifChannel) Name() string { return c.nameLit }
func (c *mockNotifChannel) Send(_ notification.Recipient, msg notification.RenderedMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sendErr != nil {
		return c.sendErr
	}
	c.sent = append(c.sent, msg)
	return nil
}

type mockNotifFormatter struct{}

func (f *mockNotifFormatter) Format(event notification.Event) (notification.RenderedMessage, error) {
	return notification.RenderedMessage{
		Body:         []byte(`{"test": true}`),
		FallbackText: string(event.Type),
	}, nil
}

func newTestNotifService(ch *mockNotifChannel) *notification.Service {
	return notification.NewService(notification.ServiceConfig{
		Channels: []notification.ChannelWithFormatter{
			{
				Channel:   ch,
				Formatter: &mockNotifFormatter{},
			},
		},
		Router: notification.NewDefaultRouter(notification.RoutingConfig{
			DefaultChannel: "#test",
			Digest: notification.DigestConfig{
				Daily:  true,
				Weekly: true,
			},
		}),
	})
}

// ── DailyDigestTask Tests ──

func TestDailyDigestTask_Name(t *testing.T) {
	task := NewDailyDigestTask(DailyDigestConfig{})
	if task.Name() != "daily_digest" {
		t.Errorf("Name() = %q, want %q", task.Name(), "daily_digest")
	}
}

func TestDailyDigestTask_NilNotifService(t *testing.T) {
	task := NewDailyDigestTask(DailyDigestConfig{
		SessionService: &mockSessionService{
			statsResult: &service.StatsResult{TotalSessions: 10},
		},
		Logger: log.Default(),
	})

	// Should be a no-op when notifSvc is nil
	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDailyDigestTask_SendsDigest(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestNotifService(ch)

	mock := &mockSessionService{
		statsResult: &service.StatsResult{
			TotalSessions: 15,
			TotalTokens:   500000,
			TotalCost:     12.50,
		},
	}

	task := NewDailyDigestTask(DailyDigestConfig{
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
	if len(ch.sent) == 0 {
		t.Error("expected at least one notification to be sent")
	}
	if !mock.statsCalled {
		t.Error("Stats() was not called")
	}
}

func TestDailyDigestTask_SkipsNoSessions(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestNotifService(ch)

	mock := &mockSessionService{
		statsResult: &service.StatsResult{TotalSessions: 0},
	}

	task := NewDailyDigestTask(DailyDigestConfig{
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
		t.Errorf("expected no notifications when 0 sessions, got %d", len(ch.sent))
	}
}

func TestDailyDigestTask_StatsError(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestNotifService(ch)

	mock := &mockSessionService{
		statsErr: errDigestMock,
	}

	task := NewDailyDigestTask(DailyDigestConfig{
		SessionService: mock,
		NotifService:   notifSvc,
		Logger:         log.Default(),
	})

	err := task.Run(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ── WeeklyReportTask Tests ──

func TestWeeklyReportTask_Name(t *testing.T) {
	task := NewWeeklyReportTask(WeeklyReportConfig{})
	if task.Name() != "weekly_report" {
		t.Errorf("Name() = %q, want %q", task.Name(), "weekly_report")
	}
}

func TestWeeklyReportTask_NilNotifService(t *testing.T) {
	task := NewWeeklyReportTask(WeeklyReportConfig{
		SessionService: &mockSessionService{
			statsResult: &service.StatsResult{TotalSessions: 10},
		},
		Logger: log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWeeklyReportTask_SendsReport(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestNotifService(ch)

	mock := &mockSessionService{
		statsResult: &service.StatsResult{
			TotalSessions: 80,
			TotalTokens:   2000000,
			TotalCost:     55.00,
		},
	}

	task := NewWeeklyReportTask(WeeklyReportConfig{
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
	if len(ch.sent) == 0 {
		t.Error("expected at least one notification to be sent")
	}
}

func TestWeeklyReportTask_SkipsNoSessions(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestNotifService(ch)

	mock := &mockSessionService{
		statsResult: &service.StatsResult{TotalSessions: 0},
	}

	task := NewWeeklyReportTask(WeeklyReportConfig{
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
		t.Errorf("expected no notifications when 0 sessions, got %d", len(ch.sent))
	}
}

// ── Helper tests ──

func TestMostRecentMonday(t *testing.T) {
	// A known Wednesday: 2026-04-01
	tests := []struct {
		name string
		day  string // YYYY-MM-DD
		want string // expected Monday
	}{
		{"Monday", "2026-03-30", "2026-03-30"},
		{"Tuesday", "2026-03-31", "2026-03-30"},
		{"Wednesday", "2026-04-01", "2026-03-30"},
		{"Thursday", "2026-04-02", "2026-03-30"},
		{"Friday", "2026-04-03", "2026-03-30"},
		{"Saturday", "2026-04-04", "2026-03-30"},
		{"Sunday", "2026-04-05", "2026-03-30"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			day, _ := parseDate(tt.day)
			got := mostRecentMonday(day)
			want, _ := parseDate(tt.want)
			if got.Year() != want.Year() || got.Month() != want.Month() || got.Day() != want.Day() {
				t.Errorf("mostRecentMonday(%s) = %s, want %s",
					tt.day, got.Format("2006-01-02"), tt.want)
			}
		})
	}
}

func TestCountErrors(t *testing.T) {
	// Since Fix #10, countErrors returns stats.TotalErrors directly (populated
	// unconditionally by Stats() regardless of SessionType classification).
	stats := &service.StatsResult{
		TotalErrors: 4,
		// PerType is also populated by real Stats() calls but is no longer
		// consulted by countErrors — kept here to document the independence.
		PerType: []service.TypeStats{
			{Type: "bug", TotalErrors: 3},
			{Type: "feature", TotalErrors: 1},
		},
	}
	got := countErrors(stats)
	if got != 4 {
		t.Errorf("countErrors() = %d, want 4", got)
	}
}

func TestCountErrors_Empty(t *testing.T) {
	stats := &service.StatsResult{}
	got := countErrors(stats)
	if got != 0 {
		t.Errorf("countErrors() = %d, want 0", got)
	}
}

func TestCountErrors_UntypedSessions(t *testing.T) {
	// Regression test for the latent bug Fix #10 resolved: errors in sessions
	// without a SessionType classification were previously dropped because
	// countErrors only summed PerType entries. Now they flow through
	// TotalErrors directly.
	stats := &service.StatsResult{
		TotalErrors: 7, // 5 untyped + 2 typed, all must count
		PerType: []service.TypeStats{
			{Type: "bug", TotalErrors: 2},
		},
	}
	got := countErrors(stats)
	if got != 7 {
		t.Errorf("countErrors() = %d, want 7 (untyped errors must count)", got)
	}
}

// ── helpers ──

// parseDate parses a YYYY-MM-DD string into a time.Time.
func parseDate(s string) (time.Time, error) {
	return time.Parse("2006-01-02", s)
}

// Ensure we use these imports.
var _ session.ID
var _ notification.Event
