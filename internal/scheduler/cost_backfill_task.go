package scheduler

import (
	"context"
	"log"

	"github.com/ChristopherAparicio/aisync/internal/pricing"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// CostBackfillTask populates the denormalized estimated_cost and actual_cost
// columns for sessions that were saved before migration 029.
//
// It runs at startup (via WarmUp) and periodically until all sessions are
// backfilled. Each run processes up to 200 sessions to avoid blocking the DB.
type CostBackfillTask struct {
	store   storage.Store
	pricing *pricing.Calculator
	logger  *log.Logger
}

// CostBackfillConfig configures the cost backfill task.
type CostBackfillConfig struct {
	Store   storage.Store
	Pricing *pricing.Calculator
	Logger  *log.Logger
}

// NewCostBackfillTask creates a new cost backfill task.
func NewCostBackfillTask(cfg CostBackfillConfig) *CostBackfillTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &CostBackfillTask{
		store:   cfg.Store,
		pricing: cfg.Pricing,
		logger:  logger,
	}
}

// Name returns the task identifier.
func (t *CostBackfillTask) Name() string {
	return "cost_backfill"
}

// Run loads sessions with zero costs, computes costs, and updates the columns.
func (t *CostBackfillTask) Run(ctx context.Context) error {
	if t.pricing == nil {
		return nil
	}

	ids, err := t.store.ListSessionsWithZeroCosts(200)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}

	updated := 0
	for _, id := range ids {
		select {
		case <-ctx.Done():
			t.logger.Printf("[cost_backfill] interrupted after %d/%d sessions", updated, len(ids))
			return ctx.Err()
		default:
		}

		sess, getErr := t.store.Get(id)
		if getErr != nil {
			continue
		}

		est := t.pricing.SessionCost(sess)
		estimatedCost := est.TotalCost.TotalCost
		actualCost := est.Breakdown.ActualCost.TotalCost

		if updateErr := t.store.UpdateCosts(id, estimatedCost, actualCost); updateErr != nil {
			continue
		}
		updated++
	}

	if updated > 0 {
		t.logger.Printf("[cost_backfill] updated %d/%d sessions", updated, len(ids))
	}
	return nil
}
