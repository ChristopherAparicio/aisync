package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/notification"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/stalldetector"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// StallAlertTask polls the session_stalls table, evaluates configured
// thresholds, and fires a notification.EventStallSpike when any rule
// is crossed. It is the Phase 3 counterpart to StallDetectorTask.
type StallAlertTask struct {
	store        storage.SessionStallStore
	notifSvc     *notification.Service
	thresholds   stalldetector.AlertThresholds
	dashboardURL string
	logger       *log.Logger
	now          func() time.Time
}

// StallAlertTaskConfig configures the stall-alert task.
type StallAlertTaskConfig struct {
	Store        storage.SessionStallStore
	NotifService *notification.Service
	Thresholds   stalldetector.AlertThresholds
	DashboardURL string
	Logger       *log.Logger
	Now          func() time.Time
}

// NewStallAlertTask creates the task. Nil-safe on nil store / notif service —
// Run() becomes a no-op so the task can sit idle in the scheduler when stall
// alerts are disabled.
func NewStallAlertTask(cfg StallAlertTaskConfig) *StallAlertTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &StallAlertTask{
		store:        cfg.Store,
		notifSvc:     cfg.NotifService,
		thresholds:   cfg.Thresholds,
		dashboardURL: cfg.DashboardURL,
		logger:       logger,
		now:          now,
	}
}

// Name returns the scheduler task identifier.
func (t *StallAlertTask) Name() string { return "stall_alert" }

// Run executes one threshold-check pass.
func (t *StallAlertTask) Run(_ context.Context) error {
	if t == nil || t.store == nil || t.notifSvc == nil {
		return nil
	}

	now := t.now().UTC()
	since24h := now.Add(-24 * time.Hour)

	liveStats, err := t.store.StallStats(session.StallFilter{OnlyLive: true})
	if err != nil {
		return fmt.Errorf("stall_alert: live stats: %w", err)
	}

	recentStats, err := t.store.StallStats(session.StallFilter{Since: since24h, Until: now})
	if err != nil {
		return fmt.Errorf("stall_alert: recent stats: %w", err)
	}

	decision := stalldetector.Evaluate(t.thresholds, liveStats, recentStats)
	if decision == nil {
		t.logger.Printf("[stall_alert] no thresholds crossed (live=%d, new24h=%d, cost24h=$%.2f)",
			liveStats.LiveCount, recentStats.TotalCount, recentStats.CostLostUSD)
		return nil
	}

	rootCauseCounts := make(map[string]int, len(decision.RootCauseCounts))
	for k, v := range decision.RootCauseCounts {
		rootCauseCounts[string(k)] = v
	}

	severity := notification.SeverityWarning
	if decision.Severity == stalldetector.AlertSeverityCritical {
		severity = notification.SeverityCritical
	}

	event := notification.Event{
		Type:         notification.EventStallSpike,
		Severity:     severity,
		Timestamp:    now,
		DashboardURL: t.dashboardURL,
		Data: notification.StallSpikeData{
			LiveCount:       decision.LiveCount,
			NewStalls24h:    decision.NewStalls24h,
			CostLost24h:     decision.CostLost24h,
			TokensLost24h:   decision.TokensLost24h,
			TopRootCause:    string(decision.TopRootCause),
			TopProvider:     decision.TopProvider,
			RootCauseCounts: rootCauseCounts,
			ProviderCounts:  decision.ProviderCounts,
			Reasons:         decision.Reasons,
		},
	}

	t.notifSvc.Notify(event)
	t.logger.Printf("[stall_alert] %s alert fired: live=%d new24h=%d cost24h=$%.2f (reasons: %v)",
		decision.Severity, decision.LiveCount, decision.NewStalls24h, decision.CostLost24h, decision.Reasons)
	return nil
}
