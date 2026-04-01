package scheduler

import (
	"context"
	"encoding/json"
	"log"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// StatsReportTask warms the stats cache by calling Stats() periodically.
// This ensures the dashboard and API always serve fresh statistics
// without waiting for an on-demand computation.
// It warms the global stats first, then per-project stats for each known project.
type StatsReportTask struct {
	sessionSvc service.SessionServicer
	store      storage.Store
	logger     *log.Logger
}

// StatsReportTaskConfig configures the stats report task.
type StatsReportTaskConfig struct {
	SessionService service.SessionServicer
	Store          storage.Store // optional; when set, results are written to cache
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
		store:      cfg.Store,
		logger:     logger,
	}
}

// Name returns the task identifier.
func (t *StatsReportTask) Name() string {
	return "stats_report"
}

// Run calls Stats() to refresh the stats cache — global then per-project.
// When a Store is configured, results are written to the cache table using the same
// key format as the web handler's cachedStats() ("stats:<projectPath>:<branch>").
func (t *StatsReportTask) Run(ctx context.Context) error {
	// 1. Global stats.
	result, err := t.sessionSvc.Stats(service.StatsRequest{All: true})
	if err != nil {
		return err
	}
	t.cacheResult("stats::", result)
	t.logger.Printf("[stats_report] global cache refreshed: %d sessions, %d tokens",
		result.TotalSessions, result.TotalTokens)

	// 2. Per-project stats.
	projects, err := t.sessionSvc.ListProjects(ctx)
	if err != nil {
		// Non-fatal: global stats already warmed.
		t.logger.Printf("[stats_report] list projects failed: %v (global cache still valid)", err)
		return nil
	}

	warmed := 0
	for _, p := range projects {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		pResult, pErr := t.sessionSvc.Stats(service.StatsRequest{ProjectPath: p.ProjectPath})
		if pErr != nil {
			t.logger.Printf("[stats_report] project %s failed: %v", p.ProjectPath, pErr)
			continue
		}
		t.cacheResult("stats:"+p.ProjectPath+":", pResult)
		warmed++
	}

	t.logger.Printf("[stats_report] warmed %d/%d project caches", warmed, len(projects))
	return nil
}

// cacheResult writes a stats result to the cache table if a store is configured.
func (t *StatsReportTask) cacheResult(key string, result *service.StatsResult) {
	if t.store == nil {
		return
	}
	data, err := json.Marshal(result)
	if err != nil {
		return
	}
	_ = t.store.SetCache(key, data)
}
