package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// ObjectiveBackfillTask computes work objectives for sessions that don't have one yet.
// It requires a configured LLM adapter (objectives use Summarize + Explain).
// Sessions with fewer than 2 messages are skipped (ComputeObjective rejects them).
type ObjectiveBackfillTask struct {
	sessionSvc service.SessionServicer
	store      storage.Store
	logger     *log.Logger
	batchSize  int // max sessions to process per run (0 = unlimited)
	minMsgs    int // minimum messages required (default: 5, guard before LLM cost)
}

// ObjectiveBackfillConfig configures the objective backfill task.
type ObjectiveBackfillConfig struct {
	SessionService service.SessionServicer
	Store          storage.Store
	Logger         *log.Logger
	BatchSize      int // max sessions per run (default: 50)
	MinMessages    int // min messages to consider (default: 5)
}

// NewObjectiveBackfillTask creates a new objective backfill task.
func NewObjectiveBackfillTask(cfg ObjectiveBackfillConfig) *ObjectiveBackfillTask {
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 50
	}
	minMsgs := cfg.MinMessages
	if minMsgs <= 0 {
		minMsgs = 5
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &ObjectiveBackfillTask{
		sessionSvc: cfg.SessionService,
		store:      cfg.Store,
		logger:     logger,
		batchSize:  batchSize,
		minMsgs:    minMsgs,
	}
}

// Name returns the task identifier.
func (t *ObjectiveBackfillTask) Name() string {
	return "objective_backfill"
}

// Run finds sessions without objectives and computes them.
func (t *ObjectiveBackfillTask) Run(ctx context.Context) error {
	// List all sessions.
	summaries, err := t.store.List(session.ListOptions{All: true})
	if err != nil {
		return fmt.Errorf("listing sessions: %w", err)
	}

	// Filter to sessions with enough messages.
	var candidates []session.Summary
	for _, s := range summaries {
		if s.MessageCount >= t.minMsgs {
			candidates = append(candidates, s)
		}
	}

	if len(candidates) == 0 {
		t.logger.Println("[objective_backfill] no candidate sessions found")
		return nil
	}

	// Bulk-fetch existing objectives to find gaps.
	ids := make([]session.ID, len(candidates))
	for i, s := range candidates {
		ids[i] = s.ID
	}
	existing, err := t.store.ListObjectives(ids)
	if err != nil {
		return fmt.Errorf("listing objectives: %w", err)
	}

	// Find sessions missing objectives.
	var missing []session.Summary
	for _, s := range candidates {
		if existing[s.ID] == nil {
			missing = append(missing, s)
		}
	}

	if len(missing) == 0 {
		t.logger.Printf("[objective_backfill] all %d candidate sessions already have objectives", len(candidates))
		return nil
	}

	// Cap to batch size.
	if len(missing) > t.batchSize {
		missing = missing[:t.batchSize]
	}

	t.logger.Printf("[objective_backfill] processing %d sessions (of %d missing, %d total)",
		len(missing), len(missing), len(candidates))

	var computed, failed int
	start := time.Now()

	for _, s := range missing {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		_, compErr := t.sessionSvc.ComputeObjective(ctx, service.ComputeObjectiveRequest{
			SessionID: string(s.ID),
		})
		if compErr != nil {
			t.logger.Printf("[objective_backfill] session %s failed: %v", s.ID, compErr)
			failed++
			continue
		}
		computed++
	}

	t.logger.Printf("[objective_backfill] done in %s: %d computed, %d failed",
		time.Since(start).Round(time.Millisecond), computed, failed)
	return nil
}
