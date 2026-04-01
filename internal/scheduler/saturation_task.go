package scheduler

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// SaturationTask pre-computes context saturation analysis in the background.
// Results are stored in the general-purpose cache table so that web handlers
// can read them instantly instead of scanning all sessions on every request.
type SaturationTask struct {
	sessionSvc service.SessionServicer
	store      storage.Store
	logger     *log.Logger
	sinceDays  int // how far back to analyze (default 90)
}

// SaturationTaskConfig configures the saturation pre-compute task.
type SaturationTaskConfig struct {
	SessionService service.SessionServicer
	Store          storage.Store
	Logger         *log.Logger
	SinceDays      int // defaults to 90 if zero
}

// NewSaturationTask creates a new context saturation pre-compute task.
func NewSaturationTask(cfg SaturationTaskConfig) *SaturationTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	days := cfg.SinceDays
	if days <= 0 {
		days = 90
	}
	return &SaturationTask{
		sessionSvc: cfg.SessionService,
		store:      cfg.Store,
		logger:     logger,
		sinceDays:  days,
	}
}

// Name returns the task identifier.
func (t *SaturationTask) Name() string {
	return "saturation_precompute"
}

// Run computes context saturation for all projects and caches the results.
// It first computes the global (all-projects) result, then per-project results
// for each known project.
func (t *SaturationTask) Run(ctx context.Context) error {
	since := time.Now().AddDate(0, 0, -t.sinceDays)

	// 1. Global (all projects).
	globalResult, err := t.sessionSvc.ContextSaturation(ctx, "", since)
	if err != nil {
		return err
	}
	if cacheErr := t.cacheResult("saturation:", globalResult); cacheErr != nil {
		t.logger.Printf("[saturation_precompute] cache write (global) failed: %v", cacheErr)
	}
	t.logger.Printf("[saturation_precompute] global: %d sessions analyzed", globalResult.TotalSessions)

	// 2. Per-project.
	projects, err := t.sessionSvc.ListProjects(ctx)
	if err != nil {
		// Non-fatal: we still have the global result.
		t.logger.Printf("[saturation_precompute] list projects failed: %v", err)
		return nil
	}

	for _, p := range projects {
		result, pErr := t.sessionSvc.ContextSaturation(ctx, p.ProjectPath, since)
		if pErr != nil {
			t.logger.Printf("[saturation_precompute] project %s failed: %v", p.ProjectPath, pErr)
			continue
		}
		if cacheErr := t.cacheResult("saturation:"+p.ProjectPath, result); cacheErr != nil {
			t.logger.Printf("[saturation_precompute] cache write (%s) failed: %v", p.ProjectPath, cacheErr)
		}
	}

	t.logger.Printf("[saturation_precompute] computed for %d projects", len(projects))
	return nil
}

// cacheResult serializes and stores a saturation result in the cache table.
func (t *SaturationTask) cacheResult(key string, result interface{}) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return t.store.SetCache(key, data)
}
