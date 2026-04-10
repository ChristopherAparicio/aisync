package scheduler

import (
	"context"
	"log"

	"github.com/ChristopherAparicio/aisync/internal/pricing"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// AnalyticsBackfillTask populates the session_analytics and session_agent_usage
// materialized read-model tables for sessions that were ingested before
// migration 031 or whose schema_version is behind AnalyticsSchemaVersion.
//
// It runs at startup (via WarmUp) and periodically until all sessions are
// backfilled. Each run processes up to 200 sessions to avoid blocking the DB.
type AnalyticsBackfillTask struct {
	store   storage.Store
	pricing *pricing.Calculator
	logger  *log.Logger
}

// AnalyticsBackfillConfig configures the analytics backfill task.
type AnalyticsBackfillConfig struct {
	Store   storage.Store
	Pricing *pricing.Calculator
	Logger  *log.Logger
}

// NewAnalyticsBackfillTask creates a new analytics backfill task.
func NewAnalyticsBackfillTask(cfg AnalyticsBackfillConfig) *AnalyticsBackfillTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &AnalyticsBackfillTask{
		store:   cfg.Store,
		pricing: cfg.Pricing,
		logger:  logger,
	}
}

// Name returns the task identifier.
func (t *AnalyticsBackfillTask) Name() string {
	return "analytics_backfill"
}

// Run loads sessions needing analytics computation, computes analytics for each
// one, and upserts the result. It mirrors the logic in
// SessionService.stampAnalytics but works in batch for historical data.
func (t *AnalyticsBackfillTask) Run(ctx context.Context) error {
	ids, err := t.store.ListSessionsNeedingAnalytics(session.AnalyticsSchemaVersion, 200)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}

	// Build the pricing lookup once for the whole batch.
	var pricingLookup session.AnalyticsPricingLookup
	if t.pricing != nil {
		pricingLookup = t.pricing
	}

	updated := 0
	for _, id := range ids {
		select {
		case <-ctx.Done():
			t.logger.Printf("[analytics_backfill] interrupted after %d/%d sessions", updated, len(ids))
			return ctx.Err()
		default:
		}

		sess, getErr := t.store.Get(id)
		if getErr != nil {
			continue
		}
		if len(sess.Messages) == 0 {
			continue
		}

		// Determine fork offset (same logic as stampAnalytics).
		var forkOffset int
		rels, relErr := t.store.GetForkRelations(id)
		if relErr == nil {
			for _, r := range rels {
				if r.ForkID == id {
					forkOffset = r.SharedMessages
					break
				}
			}
		}

		a := session.ComputeAnalytics(sess, pricingLookup, forkOffset)

		if upsertErr := t.store.UpsertSessionAnalytics(a); upsertErr != nil {
			continue
		}
		updated++
	}

	if updated > 0 {
		t.logger.Printf("[analytics_backfill] computed analytics for %d/%d sessions", updated, len(ids))
	}
	return nil
}
