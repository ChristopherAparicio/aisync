package scheduler

import (
	"context"
	"fmt"
	"log"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// ReclassifyTask finds errors with "unknown" category and reclassifies them
// using the configured ErrorClassifier (which may include LLM fallback).
// This task is intended to run on a schedule (e.g. nightly) or on-demand.
type ReclassifyTask struct {
	errorSvc service.ErrorServicer
	store    storage.ErrorStore
	logger   *log.Logger
	limit    int // max errors to reclassify per run (default: 100)
}

// ReclassifyConfig configures the reclassification task.
type ReclassifyConfig struct {
	ErrorService service.ErrorServicer
	Store        storage.ErrorStore
	Logger       *log.Logger
	Limit        int // default: 100
}

// NewReclassifyTask creates a new reclassification task.
func NewReclassifyTask(cfg ReclassifyConfig) *ReclassifyTask {
	limit := cfg.Limit
	if limit <= 0 {
		limit = 100
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &ReclassifyTask{
		errorSvc: cfg.ErrorService,
		store:    cfg.Store,
		logger:   logger,
		limit:    limit,
	}
}

// Name returns the task identifier.
func (t *ReclassifyTask) Name() string {
	return "reclassify_errors"
}

// Run finds unknown errors and attempts to reclassify them.
// Currently, this re-runs the configured classifier (which may now include
// an LLM fallback if the user enabled composite mode). In the future,
// a dedicated LLMClassifier adapter will handle ambiguous tool errors.
func (t *ReclassifyTask) Run(ctx context.Context) error {
	// Fetch unknown errors.
	unknowns, err := t.store.ListRecentErrors(t.limit, session.ErrorCategoryUnknown)
	if err != nil {
		return fmt.Errorf("listing unknown errors: %w", err)
	}

	if len(unknowns) == 0 {
		t.logger.Printf("[reclassify_errors] no unknown errors found, nothing to do")
		return nil
	}

	t.logger.Printf("[reclassify_errors] found %d unknown errors to reclassify", len(unknowns))

	// Group by session for efficient re-processing.
	bySession := make(map[session.ID][]session.SessionError)
	for _, e := range unknowns {
		bySession[e.SessionID] = append(bySession[e.SessionID], e)
	}

	var reclassified, unchanged, failed int

	for sessionID, errors := range bySession {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Re-classify each error: the ErrorService's ProcessSession expects
		// a Session with Errors populated. We create a minimal session wrapper.
		sess := &session.Session{
			ID:     sessionID,
			Errors: errors,
		}

		result, processErr := t.errorSvc.ProcessSession(sess)
		if processErr != nil {
			t.logger.Printf("[reclassify_errors] session %s failed: %v", sessionID, processErr)
			failed += len(errors)
			continue
		}

		// Count how many actually changed from unknown.
		if result != nil {
			for cat, count := range result.ByCategory {
				if cat != session.ErrorCategoryUnknown {
					reclassified += count
				} else {
					unchanged += count
				}
			}
		}
	}

	t.logger.Printf("[reclassify_errors] done: %d reclassified, %d still unknown, %d failed (across %d sessions)",
		reclassified, unchanged, failed, len(bySession))
	return nil
}
