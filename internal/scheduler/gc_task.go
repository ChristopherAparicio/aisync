package scheduler

import (
	"context"
	"fmt"
	"log"

	"github.com/ChristopherAparicio/aisync/internal/service"
)

// GCTask performs garbage collection by removing old sessions
// based on a configurable retention period (in days).
type GCTask struct {
	sessionSvc    service.SessionServicer
	logger        *log.Logger
	retentionDays int // sessions older than this many days are deleted
}

// GCTaskConfig configures the garbage collection task.
type GCTaskConfig struct {
	SessionService service.SessionServicer
	Logger         *log.Logger
	RetentionDays  int // default: 90
}

// NewGCTask creates a new garbage collection task.
func NewGCTask(cfg GCTaskConfig) *GCTask {
	retention := cfg.RetentionDays
	if retention <= 0 {
		retention = 90
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &GCTask{
		sessionSvc:    cfg.SessionService,
		logger:        logger,
		retentionDays: retention,
	}
}

// Name returns the task identifier.
func (t *GCTask) Name() string {
	return "gc"
}

// Run deletes sessions older than the retention period.
func (t *GCTask) Run(ctx context.Context) error {
	olderThan := fmt.Sprintf("%dd", t.retentionDays)

	result, err := t.sessionSvc.GarbageCollect(ctx, service.GCRequest{
		OlderThan: olderThan,
		DryRun:    false,
	})
	if err != nil {
		return fmt.Errorf("garbage collection failed: %w", err)
	}

	t.logger.Printf("[gc] deleted %d sessions older than %s", result.Deleted, olderThan)
	return nil
}
