package scheduler

import (
	"context"
	"log"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// HotspotsSchemaVersion is bumped whenever the SessionHotspots struct changes.
// The backfill task re-computes all rows with schema_version < this value.
const HotspotsSchemaVersion = 1

// HotspotsTask pre-computes investigation hot-spots for sessions.
// It runs nightly and processes sessions that either have no hot-spots
// row or have a stale schema_version.
type HotspotsTask struct {
	store    storage.Store
	logger   *log.Logger
	batchMax int // max sessions per run (prevents long-running passes)
}

// HotspotsTaskConfig configures the hot-spots pre-compute task.
type HotspotsTaskConfig struct {
	Store    storage.Store
	Logger   *log.Logger
	BatchMax int // defaults to 200 if zero
}

// NewHotspotsTask creates a new hot-spots pre-compute task.
func NewHotspotsTask(cfg HotspotsTaskConfig) *HotspotsTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	batchMax := cfg.BatchMax
	if batchMax <= 0 {
		batchMax = 200
	}
	return &HotspotsTask{
		store:    cfg.Store,
		logger:   logger,
		batchMax: batchMax,
	}
}

// Name returns the task identifier.
func (t *HotspotsTask) Name() string {
	return "hotspots_compute"
}

// Run computes hot-spots for sessions that need them and persists the results.
func (t *HotspotsTask) Run(ctx context.Context) error {
	ids, err := t.store.ListSessionsNeedingHotspots(HotspotsSchemaVersion, t.batchMax)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		t.logger.Println("[hotspots_compute] all sessions up to date")
		return nil
	}

	t.logger.Printf("[hotspots_compute] processing %d sessions", len(ids))

	var computed, skipped int
	for _, id := range ids {
		if ctx.Err() != nil {
			t.logger.Printf("[hotspots_compute] context cancelled after %d/%d", computed, len(ids))
			return ctx.Err()
		}

		sess, getErr := t.store.Get(id)
		if getErr != nil {
			t.logger.Printf("[hotspots_compute] skip %s: %v", id, getErr)
			skipped++
			continue
		}

		h := session.ComputeHotspots(sess, 0)

		if setErr := t.store.SetHotspots(id, h, HotspotsSchemaVersion); setErr != nil {
			t.logger.Printf("[hotspots_compute] save %s failed: %v", id, setErr)
			skipped++
			continue
		}
		computed++
	}

	t.logger.Printf("[hotspots_compute] done: %d computed, %d skipped", computed, skipped)
	return nil
}
