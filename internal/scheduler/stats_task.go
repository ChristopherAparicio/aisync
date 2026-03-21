package scheduler

import (
	"context"
	"log"

	"github.com/ChristopherAparicio/aisync/internal/service"
)

// StatsReportTask warms the stats cache by calling Stats() periodically.
// This ensures the dashboard and API always serve fresh statistics
// without waiting for an on-demand computation.
type StatsReportTask struct {
	sessionSvc service.SessionServicer
	logger     *log.Logger
}

// StatsReportTaskConfig configures the stats report task.
type StatsReportTaskConfig struct {
	SessionService service.SessionServicer
	Logger         *log.Logger
}

// NewStatsReportTask creates a new stats report/cache warming task.
func NewStatsReportTask(cfg StatsReportTaskConfig) *StatsReportTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &StatsReportTask{
		sessionSvc: cfg.SessionService,
		logger:     logger,
	}
}

// Name returns the task identifier.
func (t *StatsReportTask) Name() string {
	return "stats_report"
}

// Run calls Stats() to refresh the stats cache.
func (t *StatsReportTask) Run(_ context.Context) error {
	result, err := t.sessionSvc.Stats(service.StatsRequest{All: true})
	if err != nil {
		return err
	}

	t.logger.Printf("[stats_report] cache refreshed: %d sessions, %d tokens",
		result.TotalSessions, result.TotalTokens)
	return nil
}
