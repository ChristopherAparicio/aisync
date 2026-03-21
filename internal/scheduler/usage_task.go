package scheduler

import (
	"context"
	"log"

	"github.com/ChristopherAparicio/aisync/internal/service"
)

// TokenUsageTask computes token usage buckets (hourly + daily) for the dashboard.
// Designed to run as a nightly scheduled task (off-peak hours).
type TokenUsageTask struct {
	sessionSvc service.SessionServicer
	logger     *log.Logger
}

// TokenUsageTaskConfig configures the token usage computation task.
type TokenUsageTaskConfig struct {
	SessionService service.SessionServicer
	Logger         *log.Logger
}

// NewTokenUsageTask creates a new token usage computation task.
func NewTokenUsageTask(cfg TokenUsageTaskConfig) *TokenUsageTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &TokenUsageTask{
		sessionSvc: cfg.SessionService,
		logger:     logger,
	}
}

// Name returns the task identifier.
func (t *TokenUsageTask) Name() string {
	return "token_usage_compute"
}

// Run computes hourly and daily token usage buckets.
func (t *TokenUsageTask) Run(ctx context.Context) error {
	// Hourly buckets (incremental).
	hourlyResult, err := t.sessionSvc.ComputeTokenBuckets(ctx, service.ComputeTokenBucketsRequest{
		Granularity: "1h",
		Incremental: true,
	})
	if err != nil {
		return err
	}
	t.logger.Printf("[token_usage] hourly: %d buckets, %d sessions, %d messages in %v",
		hourlyResult.BucketsWritten, hourlyResult.SessionsScanned,
		hourlyResult.MessagesScanned, hourlyResult.Duration)

	// Daily buckets (incremental).
	dailyResult, err := t.sessionSvc.ComputeTokenBuckets(ctx, service.ComputeTokenBucketsRequest{
		Granularity: "1d",
		Incremental: true,
	})
	if err != nil {
		return err
	}
	t.logger.Printf("[token_usage] daily: %d buckets, %d sessions in %v",
		dailyResult.BucketsWritten, dailyResult.SessionsScanned, dailyResult.Duration)

	return nil
}
