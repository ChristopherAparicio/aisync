package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/pricing"
	"github.com/ChristopherAparicio/aisync/internal/stalldetector"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// StallDetectorTask wraps stalldetector.Detector as a scheduler.Task.
// Each run:
//   1. Scans OpenCode's live SQLite DB for stuck tools + errored messages.
//   2. UpsertStall for every detected stall (idempotent — re-detected
//      live stalls just refresh updated_at).
//   3. Seals previously-live rows in session_stalls whose tool is no
//      longer running in OpenCode (the user resumed, restarted, or the
//      tool actually finished).
type StallDetectorTask struct {
	store    storage.SessionStallStore
	detector *stalldetector.Detector
	logger   *log.Logger
	now      func() time.Time
}

// StallDetectorTaskConfig configures the stall-detector task.
type StallDetectorTaskConfig struct {
	// Store is the session-stall persistence layer.
	Store storage.SessionStallStore
	// OpenCodeDBPath is the absolute path to opencode.db (read-only).
	OpenCodeDBPath string
	// Threshold for considering a `running` tool stalled.
	// Defaults to stalldetector.DefaultThreshold (15min).
	Threshold time.Duration
	// Lookback for the historical errored-message scan.
	// Defaults to stalldetector.DefaultLookback (24h).
	Lookback time.Duration
	// Pricing catalog used to estimate cost_lost_usd when the message
	// has no recorded cost. May be nil — cost falls back to 0.
	Pricing pricing.Catalog
	// Logger for run-summary output. Defaults to log.Default().
	Logger *log.Logger
	// Now is the clock for tests. Defaults to time.Now.
	Now func() time.Time
}

// NewStallDetectorTask creates a stall-detector scheduled task.
func NewStallDetectorTask(cfg StallDetectorTaskConfig) *StallDetectorTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	detector := stalldetector.New(stalldetector.Config{
		OpenCodeDBPath: cfg.OpenCodeDBPath,
		Threshold:      cfg.Threshold,
		Lookback:       cfg.Lookback,
		Pricing:        cfg.Pricing,
		Logger:         logger,
		Now:            now,
	})
	return &StallDetectorTask{
		store:    cfg.Store,
		detector: detector,
		logger:   logger,
		now:      now,
	}
}

// Name returns the task identifier.
func (t *StallDetectorTask) Name() string {
	return "stall_detector"
}

// Run executes one detection pass.
func (t *StallDetectorTask) Run(ctx context.Context) error {
	res, err := t.detector.Detect(ctx)
	if err != nil {
		return fmt.Errorf("stall_detector: detect: %w", err)
	}

	var upserts, sealed, failures int
	for i := range res.Stalls {
		stall := &res.Stalls[i]
		if upsertErr := t.store.UpsertStall(stall); upsertErr != nil {
			t.logger.Printf("[stall_detector] upsert %s/%s: %v",
				stall.ProviderSessionID, stall.RootCause, upsertErr)
			failures++
			continue
		}
		upserts++
	}

	// Seal previously-live stalls whose tool is no longer running in
	// OpenCode. The match key is (provider_session_id, started_at) —
	// see stalldetector.LiveKey.
	liveInDB, err := t.store.ListLiveStalls()
	if err != nil {
		t.logger.Printf("[stall_detector] list live stalls failed: %v", err)
		t.logger.Printf("[stall_detector] done: %d upserted, %d failures (sealing skipped)",
			upserts, failures)
		// Do not return — the upsert pass succeeded and is the
		// primary value of this task.
		return nil
	}
	now := t.now().UTC()
	for _, stall := range liveInDB {
		if _, stillLive := res.LiveKeys[stalldetector.LiveKey(stall.ProviderSessionID, stall.StartedAt)]; stillLive {
			continue
		}
		if sealErr := t.store.SealStall(stall.ID, now); sealErr != nil {
			t.logger.Printf("[stall_detector] seal id=%d: %v", stall.ID, sealErr)
			failures++
			continue
		}
		sealed++
	}

	t.logger.Printf("[stall_detector] done: %d upserted, %d sealed, %d failures (live=%d)",
		upserts, sealed, failures, len(res.LiveKeys))
	return nil
}
