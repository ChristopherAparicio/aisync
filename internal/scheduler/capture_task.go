package scheduler

import (
	"context"
	"log"

	"github.com/ChristopherAparicio/aisync/internal/service"
)

// CaptureAllTask periodically captures all sessions from all
// configured providers, storing any new or updated sessions.
type CaptureAllTask struct {
	sessionSvc service.SessionServicer
	logger     *log.Logger
}

// CaptureAllTaskConfig configures the periodic capture-all task.
type CaptureAllTaskConfig struct {
	SessionService service.SessionServicer
	Logger         *log.Logger
}

// NewCaptureAllTask creates a new capture-all task.
func NewCaptureAllTask(cfg CaptureAllTaskConfig) *CaptureAllTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &CaptureAllTask{
		sessionSvc: cfg.SessionService,
		logger:     logger,
	}
}

// Name returns the task identifier.
func (t *CaptureAllTask) Name() string {
	return "capture_all"
}

// Run captures all sessions from all providers.
func (t *CaptureAllTask) Run(_ context.Context) error {
	results, err := t.sessionSvc.CaptureAll(service.CaptureRequest{})
	if err != nil {
		return err
	}

	var captured, skipped int
	for _, r := range results {
		if r.Skipped {
			skipped++
		} else {
			captured++
		}
	}

	t.logger.Printf("[capture_all] captured %d sessions, skipped %d unchanged",
		captured, skipped)
	return nil
}
