package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/notification"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// ── Error Spike Detection Task ──

// ErrorSpikeTask detects error spikes within a configurable time window
// and fires EventErrorSpike notifications per project.
type ErrorSpikeTask struct {
	store        storage.ErrorStore
	notifSvc     *notification.Service
	dashboardURL string
	threshold    int // minimum error count to trigger spike alert
	windowMins   int // detection window in minutes
	logger       *log.Logger
}

// ErrorSpikeConfig holds the configuration for creating an ErrorSpikeTask.
type ErrorSpikeConfig struct {
	Store        storage.ErrorStore
	NotifService *notification.Service
	DashboardURL string
	Threshold    int // error count threshold (default: 10)
	WindowMins   int // time window in minutes (default: 60)
	Logger       *log.Logger
}

// NewErrorSpikeTask creates an error spike detection task.
func NewErrorSpikeTask(cfg ErrorSpikeConfig) *ErrorSpikeTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	threshold := cfg.Threshold
	if threshold <= 0 {
		threshold = 10
	}
	windowMins := cfg.WindowMins
	if windowMins <= 0 {
		windowMins = 60
	}
	return &ErrorSpikeTask{
		store:        cfg.Store,
		notifSvc:     cfg.NotifService,
		dashboardURL: cfg.DashboardURL,
		threshold:    threshold,
		windowMins:   windowMins,
		logger:       logger,
	}
}

func (t *ErrorSpikeTask) Name() string { return "error_spike" }

func (t *ErrorSpikeTask) Run(_ context.Context) error {
	if t.notifSvc == nil {
		return nil
	}
	if t.store == nil {
		return nil
	}

	// Fetch recent errors — we ask for a generous limit since we filter by time window.
	// The query returns errors ordered by occurred_at DESC.
	errors, err := t.store.ListRecentErrors(500, "")
	if err != nil {
		return fmt.Errorf("error_spike: list recent errors: %w", err)
	}

	if len(errors) == 0 {
		return nil
	}

	cutoff := time.Now().Add(-time.Duration(t.windowMins) * time.Minute)

	// Group errors by session's project context.
	// Since SessionError doesn't carry project info, we group by session ID
	// and report at a global level. For project-level granularity, we'd need
	// to join sessions — but for now, a global spike alert is sufficient.
	type spikeInfo struct {
		count      int
		sessions   map[string]bool
		errorTypes map[string]bool
	}

	spike := &spikeInfo{
		sessions:   make(map[string]bool),
		errorTypes: make(map[string]bool),
	}

	for _, e := range errors {
		if e.OccurredAt.Before(cutoff) {
			break // errors are ordered DESC, so we can stop early
		}
		spike.count++
		spike.sessions[string(e.SessionID)] = true
		if e.Category != "" {
			spike.errorTypes[string(e.Category)] = true
		}
	}

	if spike.count < t.threshold {
		t.logger.Printf("[error_spike] %d errors in last %d min (threshold: %d), no alert",
			spike.count, t.windowMins, t.threshold)
		return nil
	}

	// Collect unique session IDs and error types.
	sessionIDs := make([]string, 0, len(spike.sessions))
	for sid := range spike.sessions {
		sessionIDs = append(sessionIDs, sid)
	}
	errorTypes := make([]string, 0, len(spike.errorTypes))
	for et := range spike.errorTypes {
		errorTypes = append(errorTypes, et)
	}

	// Determine severity based on count relative to threshold.
	severity := notification.SeverityWarning
	if spike.count >= t.threshold*3 {
		severity = notification.SeverityCritical
	}

	event := notification.Event{
		Type:         notification.EventErrorSpike,
		Severity:     severity,
		Timestamp:    time.Now(),
		DashboardURL: t.dashboardURL,
		Data: notification.ErrorSpikeData{
			ErrorCount:    spike.count,
			WindowMinutes: t.windowMins,
			Sessions:      sessionIDs,
			ErrorTypes:    errorTypes,
		},
	}

	// Fire-and-forget (async) — same pattern as BudgetCheckTask.
	t.notifSvc.Notify(event)

	t.logger.Printf("[error_spike] alert: %d errors in last %d min across %d sessions",
		spike.count, t.windowMins, len(sessionIDs))

	return nil
}
