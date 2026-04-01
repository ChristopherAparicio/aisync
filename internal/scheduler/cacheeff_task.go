package scheduler

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// CacheEfficiencyTask pre-computes cache efficiency analysis in the background.
// Results are stored in the general-purpose cache table so that web handlers
// can read them instantly instead of scanning all sessions on every request.
//
// Two time windows are computed per project: 7-day (dashboard, project detail)
// and 90-day (cost page). Cache keys follow the pattern:
//
//	cacheeff:7d:<projectPath>   (empty projectPath = global)
//	cacheeff:90d:<projectPath>
type CacheEfficiencyTask struct {
	sessionSvc service.SessionServicer
	store      storage.Store
	logger     *log.Logger
}

// CacheEfficiencyTaskConfig configures the cache efficiency pre-compute task.
type CacheEfficiencyTaskConfig struct {
	SessionService service.SessionServicer
	Store          storage.Store
	Logger         *log.Logger
}

// NewCacheEfficiencyTask creates a new cache efficiency pre-compute task.
func NewCacheEfficiencyTask(cfg CacheEfficiencyTaskConfig) *CacheEfficiencyTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &CacheEfficiencyTask{
		sessionSvc: cfg.SessionService,
		store:      cfg.Store,
		logger:     logger,
	}
}

// Name returns the task identifier.
func (t *CacheEfficiencyTask) Name() string {
	return "cacheeff_precompute"
}

// Run computes cache efficiency for all projects (7d + 90d windows) and caches results.
func (t *CacheEfficiencyTask) Run(ctx context.Context) error {
	now := time.Now()
	since7d := now.AddDate(0, 0, -7)
	since90d := now.AddDate(0, 0, -90)

	// 1. Global (all projects) — both windows.
	if err := t.computeAndCache(ctx, "", since7d, "cacheeff:7d:"); err != nil {
		return err
	}
	if err := t.computeAndCache(ctx, "", since90d, "cacheeff:90d:"); err != nil {
		return err
	}
	t.logger.Printf("[cacheeff_precompute] global: 7d + 90d computed")

	// 2. Per-project.
	projects, err := t.sessionSvc.ListProjects(ctx)
	if err != nil {
		// Non-fatal: we still have the global results.
		t.logger.Printf("[cacheeff_precompute] list projects failed: %v", err)
		return nil
	}

	for _, p := range projects {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// 7-day window (used by dashboard + project detail).
		if pErr := t.computeAndCache(ctx, p.ProjectPath, since7d, "cacheeff:7d:"+p.ProjectPath); pErr != nil {
			t.logger.Printf("[cacheeff_precompute] project %s (7d) failed: %v", p.ProjectPath, pErr)
		}
		// 90-day window (used by cost page).
		if pErr := t.computeAndCache(ctx, p.ProjectPath, since90d, "cacheeff:90d:"+p.ProjectPath); pErr != nil {
			t.logger.Printf("[cacheeff_precompute] project %s (90d) failed: %v", p.ProjectPath, pErr)
		}
	}

	t.logger.Printf("[cacheeff_precompute] computed for %d projects (7d + 90d each)", len(projects))
	return nil
}

// computeAndCache calls CacheEfficiency and stores the result in the cache table.
func (t *CacheEfficiencyTask) computeAndCache(ctx context.Context, project string, since time.Time, cacheKey string) error {
	result, err := t.sessionSvc.CacheEfficiency(ctx, project, since)
	if err != nil {
		return err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return t.store.SetCache(cacheKey, data)
}
