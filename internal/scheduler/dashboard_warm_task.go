package scheduler

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// DashboardWarmTask pre-computes the expensive caches that the dashboard and
// cost pages depend on: stats, forecast, and trends.
//
// This task is designed to run at startup (via WarmUp) and periodically.
// Cache keys follow the same pattern as the web handler's cached* functions.
type DashboardWarmTask struct {
	sessionSvc service.SessionServicer
	store      storage.Store
	logger     *log.Logger
}

// DashboardWarmTaskConfig configures the dashboard warm-up task.
type DashboardWarmTaskConfig struct {
	SessionService service.SessionServicer
	Store          storage.Store
	Logger         *log.Logger
}

// NewDashboardWarmTask creates a new dashboard warm-up task.
func NewDashboardWarmTask(cfg DashboardWarmTaskConfig) *DashboardWarmTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &DashboardWarmTask{
		sessionSvc: cfg.SessionService,
		store:      cfg.Store,
		logger:     logger,
	}
}

// Name returns the task identifier.
func (t *DashboardWarmTask) Name() string {
	return "dashboard_warm"
}

// Run pre-computes stats and trends (global first, then per-project).
// NOTE: Forecast warming was removed — the Forecast handler now reads from
// pre-computed session_analytics rows and completes in <100ms.
func (t *DashboardWarmTask) Run(ctx context.Context) error {
	// 1. Global stats (warm this first — used by every page).
	t.warmStats(ctx, "", true)

	// 2. Global trends (used by dashboard).
	t.warmTrends(ctx, "")

	// 3. Per-project (stats + trends).
	projects, err := t.sessionSvc.ListProjects(ctx)
	if err != nil {
		t.logger.Printf("[dashboard_warm] list projects failed: %v (global caches still valid)", err)
		return nil
	}

	for _, p := range projects {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		t.warmStats(ctx, p.ProjectPath, false)
		t.warmTrends(ctx, p.ProjectPath)
	}

	t.logger.Printf("[dashboard_warm] warmed stats + trends for %d projects", len(projects))
	return nil
}

// warmStats computes and caches stats for a project (or global if project is empty).
func (t *DashboardWarmTask) warmStats(ctx context.Context, project string, all bool) {
	req := service.StatsRequest{All: all, ProjectPath: project}
	result, err := t.sessionSvc.Stats(req)
	if err != nil {
		t.logger.Printf("[dashboard_warm] stats %q failed: %v", project, err)
		return
	}

	cacheKey := "stats:" + project + ":"
	data, err := json.Marshal(result)
	if err != nil {
		return
	}
	_ = t.store.SetCache(cacheKey, data)
}

// warmTrends computes and caches the trends for a project (or global if empty).
func (t *DashboardWarmTask) warmTrends(ctx context.Context, project string) {
	req := service.TrendRequest{
		Period:      7 * 24 * time.Hour,
		ProjectPath: project,
	}
	cacheKey := "trends:" + project

	result, err := t.sessionSvc.Trends(ctx, req)
	if err != nil {
		t.logger.Printf("[dashboard_warm] trends %q failed: %v", project, err)
		return
	}

	data, err := json.Marshal(result)
	if err != nil {
		return
	}
	_ = t.store.SetCache(cacheKey, data)
}
