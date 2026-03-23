package service

import (
	"context"
	"fmt"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ComputeObjectiveRequest specifies which session to compute an objective for.
type ComputeObjectiveRequest struct {
	SessionID string
	Session   *session.Session // optional: pass session directly to avoid DB re-load
	FullMode  bool             // if true, also compute the full explain (costs more tokens)
}

// ComputeObjective generates and persists a work objective for a session.
// It uses the existing Summarize (StructuredSummary) and Explain (short) services.
//
// This is idempotent — calling it again overwrites the previous objective.
func (s *SessionService) ComputeObjective(ctx context.Context, req ComputeObjectiveRequest) (*session.SessionObjective, error) {
	sess := req.Session
	if sess == nil {
		var err error
		sess, err = s.store.Get(session.ID(req.SessionID))
		if err != nil {
			return nil, fmt.Errorf("loading session: %w", err)
		}
	}

	if len(sess.Messages) < 2 {
		return nil, fmt.Errorf("session has too few messages for objective analysis")
	}

	obj := session.SessionObjective{
		SessionID: sess.ID,
	}

	// Step 1: Generate StructuredSummary (intent, outcome, decisions, friction, open_items).
	if s.llm != nil {
		sumResult, sumErr := s.Summarize(ctx, SummarizeRequest{
			Session: sess,
		})
		if sumErr == nil {
			obj.Summary = sumResult.Summary
		}
	}

	// Fallback: if no LLM, use session summary as intent.
	if obj.Summary.Intent == "" && sess.Summary != "" {
		obj.Summary.Intent = sess.Summary
	}

	// Step 2: Generate short explanation.
	if s.llm != nil {
		expResult, expErr := s.Explain(ctx, ExplainRequest{
			SessionID: sess.ID,
			Short:     true,
		})
		if expErr == nil {
			obj.ExplainShort = expResult.Explanation
		}
	}

	// Step 3 (optional): Generate full explanation.
	if req.FullMode && s.llm != nil {
		expResult, expErr := s.Explain(ctx, ExplainRequest{
			SessionID: sess.ID,
			Short:     false,
		})
		if expErr == nil {
			obj.ExplainFull = expResult.Explanation
		}
	}

	// Persist.
	if err := s.store.SaveObjective(obj); err != nil {
		return nil, fmt.Errorf("saving objective: %w", err)
	}

	return &obj, nil
}

// GetObjective retrieves the persisted objective for a session.
func (s *SessionService) GetObjective(ctx context.Context, sessionID string) (*session.SessionObjective, error) {
	return s.store.GetObjective(session.ID(sessionID))
}
