package scheduler

import (
	"context"
	"fmt"
	"log"

	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/service"
)

// RetentionCompactor is the service port used by the retention scheduler.
type RetentionCompactor interface {
	CompactIdleSessions(ctx context.Context, req service.RetentionRequest) (*service.RetentionResult, error)
}

// RetentionTask runs destructive warm-tier compaction for idle sessions.
type RetentionTask struct {
	sessionSvc RetentionCompactor
	policy     config.RetentionPolicy
	logger     *log.Logger
}

// RetentionTaskConfig configures the warm-tier retention task.
type RetentionTaskConfig struct {
	SessionService RetentionCompactor
	Policy         config.RetentionPolicy
	Logger         *log.Logger
}

// NewRetentionTask creates a warm-tier retention task.
func NewRetentionTask(cfg RetentionTaskConfig) *RetentionTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &RetentionTask{sessionSvc: cfg.SessionService, policy: cfg.Policy, logger: logger}
}

// Name returns the task identifier.
func (t *RetentionTask) Name() string {
	return "retention_compact"
}

// Run compacts eligible idle sessions into the warm tier.
func (t *RetentionTask) Run(ctx context.Context) error {
	if t.sessionSvc == nil {
		return fmt.Errorf("retention task missing session service")
	}
	result, err := t.sessionSvc.CompactIdleSessions(ctx, service.RetentionRequest{Policy: t.policy})
	if err != nil {
		return err
	}
	t.logger.Printf("[retention] scanned=%d candidates=%d compacted=%d skipped=%d errors=%d bytes=%d->%d",
		result.Scanned, result.Candidates, result.Compacted, result.Skipped, result.Errors, result.BytesBefore, result.BytesAfter)
	return nil
}
