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
	FilesGrouped []FileBlame                // populated only in multi-file mode (grouped by file)
	ProjectFiles []session.ProjectFileEntry // populated only in project-view mode
	Restored     *RestoreResult             // non-nil only when Restore=true
	FilePath     string
}

// FileBlame groups the sessions that touched one file. Sessions is ordered most-recent first
// and is empty when no session touched the file.
type FileBlame struct {
	File     string               `json:"file"`
	Sessions []session.BlameEntry `json:"sessions"`
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
			FilePaths: expandBlameCandidates(req.FilePaths, req.ProjectPath),
			Branch:    req.Branch,
			Provider:  req.Provider,
		}
		entries, err := s.store.GetSessionsByFile(query)
		if err != nil {
			return nil, fmt.Errorf("blame lookup: %w", err)
		}
		return &BlameResult{FilesGrouped: groupBlameByFile(req.FilePaths, entries, req.All, req.ProjectPath)}, nil
	}

	// Single-file mode (original behavior).
	if req.FilePath == "" {
		return nil, fmt.Errorf("file path or project path is required")
	}

	query := session.BlameQuery{
		FilePaths: session.BlameMatchCandidates(req.FilePath, req.ProjectPath),
		Branch:    req.Branch,
		Provider:  req.Provider,
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

// groupBlameByFile buckets entries by their FilePath and returns one FileBlame per requested
// file, preserving the requested order. Entries arrive most-recent-first from the store, so each
// bucket stays in that order. Requested files with no entries yield an empty Sessions slice; when
// all is false only the most recent session per file is kept.
func groupBlameByFile(files []string, entries []session.BlameEntry, all bool, projectRoot string) []FileBlame {
	buckets := make(map[string][]session.BlameEntry, len(files))
	for _, e := range entries {
		key := session.NormalizeFilePath(e.FilePath, projectRoot)
		buckets[key] = append(buckets[key], e)
	}

	grouped := make([]FileBlame, 0, len(files))
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		key := session.NormalizeFilePath(f, projectRoot)
		if seen[key] {
			continue
		}
		seen[key] = true

		sessions := buckets[key]
		if sessions == nil {
			sessions = []session.BlameEntry{} // emit [] not null for untouched files in JSON
		}
		if !all && len(sessions) > 1 {
			sessions = sessions[:1]
		}
		grouped = append(grouped, FileBlame{File: f, Sessions: sessions})
	}
	return grouped
}

// expandBlameCandidates de-duplicates the relative+absolute match candidates of
// every requested file so one lookup finds rows whatever form they were stored in.
func expandBlameCandidates(files []string, projectRoot string) []string {
	seen := make(map[string]bool, len(files)*2)
	out := make([]string, 0, len(files)*2)
	for _, f := range files {
		for _, c := range session.BlameMatchCandidates(f, projectRoot) {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}
