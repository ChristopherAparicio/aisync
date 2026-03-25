package scheduler

import (
	"context"
	"log"

	"github.com/ChristopherAparicio/aisync/internal/service"
)

// BackfillRemoteURLTask resolves and persists git remote URLs for sessions
// that have an empty remote_url. This fixes worktree deduplication by
// ensuring sessions are grouped by their git repository, not by local path.
type BackfillRemoteURLTask struct {
	sessionSvc service.SessionServicer
	logger     *log.Logger
}

// BackfillRemoteURLConfig configures the backfill task.
type BackfillRemoteURLConfig struct {
	SessionService service.SessionServicer
	Logger         *log.Logger
}

// NewBackfillRemoteURLTask creates a new backfill task.
func NewBackfillRemoteURLTask(cfg BackfillRemoteURLConfig) *BackfillRemoteURLTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &BackfillRemoteURLTask{
		sessionSvc: cfg.SessionService,
		logger:     logger,
	}
}

// Name returns the task identifier.
func (t *BackfillRemoteURLTask) Name() string {
	return "backfill_remote_url"
}

// Run resolves git remote URLs for sessions with empty remote_url.
func (t *BackfillRemoteURLTask) Run(ctx context.Context) error {
	result, err := t.sessionSvc.BackfillRemoteURLs(ctx)
	if err != nil {
		return err
	}

	t.logger.Printf("[backfill_remote_url] done: %d candidates, %d updated, %d skipped",
		result.Candidates, result.Updated, result.Skipped)
	return nil
}

// ForkDetectionTask runs fork detection on all sessions and persists
// fork relations to the database.
type ForkDetectionTask struct {
	sessionSvc service.SessionServicer
	logger     *log.Logger
}

// ForkDetectionConfig configures the fork detection task.
type ForkDetectionConfig struct {
	SessionService service.SessionServicer
	Logger         *log.Logger
}

// NewForkDetectionTask creates a new fork detection task.
func NewForkDetectionTask(cfg ForkDetectionConfig) *ForkDetectionTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &ForkDetectionTask{
		sessionSvc: cfg.SessionService,
		logger:     logger,
	}
}

// Name returns the task identifier.
func (t *ForkDetectionTask) Name() string {
	return "fork_detection"
}

// Run detects forks across all sessions and persists the relationships.
func (t *ForkDetectionTask) Run(ctx context.Context) error {
	result, err := t.sessionSvc.DetectForksBatch(ctx)
	if err != nil {
		return err
	}

	t.logger.Printf("[fork_detection] done: %d sessions scanned, %d forks detected, %d relations saved",
		result.SessionsScanned, result.ForksDetected, result.RelationsSaved)
	return nil
}
