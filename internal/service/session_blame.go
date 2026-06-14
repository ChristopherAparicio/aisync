package service

import (
	"context"
	"fmt"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Blame ──

// BlameRequest contains inputs for a blame lookup.
type BlameRequest struct {
	FilePath    string               // single-file mode — required when FilePaths is empty and no project-view
	FilePaths   []string             // multi-file mode; takes priority over FilePath when non-empty
	ProjectPath string               // project-view mode when FilePath and FilePaths are empty; also used as Restore destination
	Branch      string               // optional filter
	Provider    session.ProviderName // optional filter
	All         bool                 // true = all sessions; false = most recent only
	Restore     bool                 // if true, restore the most recent session that touched the file
}

// BlameResult contains the outcome of a blame operation.
type BlameResult struct {
	Entries      []session.BlameEntry
	ProjectFiles []session.ProjectFileEntry // populated only in project-view mode
	Restored     *RestoreResult             // non-nil only when Restore=true
	FilePath     string
}

// Blame finds AI sessions that touched the given file(s), or lists all project files.
// Routing:
//  1. Project-view: ProjectPath set with no file paths → FilesForProject
//  2. Multi-file: FilePaths non-empty → GetSessionsByFile with IN(...)
//  3. Single-file: FilePath set → existing behavior with optional Restore
//  4. None set → validation error
func (s *SessionService) Blame(ctx context.Context, req BlameRequest) (*BlameResult, error) {
	// Project-view mode: ProjectPath set, no file path(s) specified.
	if req.ProjectPath != "" && req.FilePath == "" && len(req.FilePaths) == 0 {
		entries, err := s.store.FilesForProject(req.ProjectPath, "", 0)
		if err != nil {
			return nil, fmt.Errorf("blame project: %w", err)
		}
		return &BlameResult{ProjectFiles: entries}, nil
	}

	// Multi-file mode: FilePaths takes priority over FilePath.
	if len(req.FilePaths) > 0 {
		query := session.BlameQuery{
			FilePaths: req.FilePaths,
			Branch:    req.Branch,
			Provider:  req.Provider,
		}
		entries, err := s.store.GetSessionsByFile(query)
		if err != nil {
			return nil, fmt.Errorf("blame lookup: %w", err)
		}
		return &BlameResult{Entries: entries}, nil
	}

	// Single-file mode (original behavior).
	if req.FilePath == "" {
		return nil, fmt.Errorf("file path or project path is required")
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
