package service

import (
	"context"
	"fmt"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Blame ──

// BlameRequest contains inputs for a blame lookup.
type BlameRequest struct {
	FilePath    string               // required — file path relative to project root
	ProjectPath string               // used for Restore shortcut
	Branch      string               // optional filter
	Provider    session.ProviderName // optional filter
	All         bool                 // true = all sessions; false = most recent only
	Restore     bool                 // if true, restore the most recent session that touched the file
}

// BlameResult contains the outcome of a blame operation.
type BlameResult struct {
	Entries  []session.BlameEntry
	Restored *RestoreResult // non-nil only when Restore=true
	FilePath string
}

// Blame finds AI sessions that touched the given file.
// If Restore is set, it restores the most recent matching session.
func (s *SessionService) Blame(ctx context.Context, req BlameRequest) (*BlameResult, error) {
	if req.FilePath == "" {
		return nil, fmt.Errorf("file path is required")
	}

	query := session.BlameQuery{
		FilePath: req.FilePath,
		Branch:   req.Branch,
		Provider: req.Provider,
	}
	if !req.All {
		query.Limit = 1
	}

	entries, err := s.store.GetSessionsByFile(query)
	if err != nil {
		return nil, fmt.Errorf("blame lookup: %w", err)
	}

	result := &BlameResult{
		Entries:  entries,
		FilePath: req.FilePath,
	}

	// Restore shortcut: restore the most recent session that touched this file.
	if req.Restore && len(entries) > 0 {
		restoreResult, restoreErr := s.Restore(RestoreRequest{
			SessionID:   entries[0].SessionID,
			ProjectPath: req.ProjectPath,
		})
		if restoreErr != nil {
			return nil, fmt.Errorf("blame restore: %w", restoreErr)
		}
		result.Restored = restoreResult
	}

	return result, nil
}
