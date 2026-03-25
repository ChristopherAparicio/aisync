// Package service — error.go implements the ErrorService.
//
// ErrorService orchestrates error classification and persistence for captured sessions.
// It follows the same pattern as AnalysisService: a standalone service with its own
// dependencies (ErrorClassifier port + ErrorStore) that can be invoked from the
// PostCaptureFunc pipeline or on-demand via CLI/API.
//
// Flow:
//  1. Session is captured (provider.Export populates Session.Errors with raw errors)
//  2. PostCaptureFunc calls ErrorService.ProcessSession(session)
//  3. ErrorService classifies each raw error using the ErrorClassifier
//  4. Classified errors are persisted via ErrorStore.SaveErrors()
//  5. Query methods (GetErrors, GetSummary, ListRecent) surface the data
package service

import (
	"fmt"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// ── ErrorService ──

// ErrorService orchestrates error classification and persistence.
type ErrorService struct {
	store      storage.ErrorStore
	classifier session.ErrorClassifier
}

// ErrorServiceConfig holds all dependencies for creating an ErrorService.
type ErrorServiceConfig struct {
	Store      storage.ErrorStore      // required — where to persist errors
	Classifier session.ErrorClassifier // required — how to classify errors
}

// NewErrorService creates an ErrorService with all dependencies.
func NewErrorService(cfg ErrorServiceConfig) *ErrorService {
	return &ErrorService{
		store:      cfg.Store,
		classifier: cfg.Classifier,
	}
}

// ── Public API ──

// ProcessSessionResult contains the outcome of error processing.
type ProcessSessionResult struct {
	SessionID  session.ID                    `json:"session_id"`
	Processed  int                           `json:"processed"` // number of errors classified and saved
	Skipped    int                           `json:"skipped"`   // errors already classified (had category set)
	ByCategory map[session.ErrorCategory]int `json:"by_category,omitempty"`
}

// ProcessSession classifies and persists all errors from a captured session.
// It reads errors from sess.Errors, classifies each one using the configured
// ErrorClassifier, and saves them via ErrorStore.SaveErrors().
//
// This method is idempotent: re-processing the same session replaces existing
// errors (ErrorStore uses upsert by error ID).
func (s *ErrorService) ProcessSession(sess *session.Session) (*ProcessSessionResult, error) {
	if s.classifier == nil {
		return nil, fmt.Errorf("no error classifier configured")
	}
	if s.store == nil {
		return nil, fmt.Errorf("no error store configured")
	}
	if sess == nil {
		return nil, fmt.Errorf("session is nil")
	}

	result := &ProcessSessionResult{
		SessionID:  sess.ID,
		ByCategory: make(map[session.ErrorCategory]int),
	}

	if len(sess.Errors) == 0 {
		return result, nil
	}

	classified := make([]session.SessionError, 0, len(sess.Errors))
	for _, raw := range sess.Errors {
		// Skip errors that already have a valid category (re-classification guard).
		if raw.Category.Valid() && raw.Category != session.ErrorCategoryUnknown {
			result.Skipped++
			classified = append(classified, raw)
			result.ByCategory[raw.Category]++
			continue
		}

		// Classify the raw error.
		c := s.classifier.Classify(raw)
		classified = append(classified, c)
		result.Processed++
		result.ByCategory[c.Category]++
	}

	// Persist all classified errors.
	if err := s.store.SaveErrors(classified); err != nil {
		return nil, fmt.Errorf("saving errors for session %s: %w", sess.ID, err)
	}

	return result, nil
}

// GetErrors retrieves all classified errors for a session.
func (s *ErrorService) GetErrors(sessionID session.ID) ([]session.SessionError, error) {
	if s.store == nil {
		return nil, fmt.Errorf("no error store configured")
	}
	return s.store.GetErrors(sessionID)
}

// GetSummary computes aggregated error statistics for a session.
func (s *ErrorService) GetSummary(sessionID session.ID) (*session.SessionErrorSummary, error) {
	if s.store == nil {
		return nil, fmt.Errorf("no error store configured")
	}
	return s.store.GetErrorSummary(sessionID)
}

// ListRecent returns recent errors across all sessions, optionally filtered by category.
// Pass an empty category to return all. Limit 0 uses the store default (50).
func (s *ErrorService) ListRecent(limit int, category session.ErrorCategory) ([]session.SessionError, error) {
	if s.store == nil {
		return nil, fmt.Errorf("no error store configured")
	}
	return s.store.ListRecentErrors(limit, category)
}
