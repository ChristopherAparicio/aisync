package service

import (
	"context"
	"log"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// BackfillResult contains the results of a remote_url backfill operation.
type BackfillResult struct {
	Candidates int // total sessions with empty remote_url
	Updated    int // sessions successfully updated
	Skipped    int // sessions where git remote couldn't be resolved
}

// BackfillRemoteURLs resolves and persists git remote URLs for sessions
// that have an empty remote_url. This fixes the worktree deduplication issue
// where OpenCode worktree sessions appear as separate "projects" because
// their remote_url was never populated.
//
// For each candidate session, it creates a temporary git.Client from the
// session's ProjectPath and attempts `git remote get-url origin`.
func (s *SessionService) BackfillRemoteURLs(ctx context.Context) (*BackfillResult, error) {
	candidates, err := s.store.ListSessionsWithEmptyRemoteURL(0) // 0 = all
	if err != nil {
		return nil, err
	}

	result := &BackfillResult{Candidates: len(candidates)}

	// Group by project_path to avoid redundant git calls.
	pathToURL := make(map[string]string)

	for _, c := range candidates {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		// Check cache first (same project_path = same remote).
		remoteURL, cached := pathToURL[c.ProjectPath]
		if !cached {
			remoteURL = resolveRemoteURLForPath(c.ProjectPath)
			pathToURL[c.ProjectPath] = remoteURL
		}

		if remoteURL == "" {
			result.Skipped++
			continue
		}

		if err := s.store.UpdateRemoteURL(c.ID, remoteURL); err != nil {
			log.Printf("[backfill] failed to update session %s: %v", c.ID, err)
			result.Skipped++
			continue
		}
		result.Updated++
	}

	return result, nil
}

// ForkDetectionResult contains the results of a batch fork detection operation.
type ForkDetectionResult struct {
	SessionsScanned int
	ForksDetected   int
	RelationsSaved  int
}

// DetectForksBatch runs fork detection on all sessions grouped by branch
// and persists the results to the session_forks table.
func (s *SessionService) DetectForksBatch(ctx context.Context) (*ForkDetectionResult, error) {
	// List all sessions.
	summaries, err := s.store.List(session.ListOptions{All: true})
	if err != nil {
		return nil, err
	}

	result := &ForkDetectionResult{SessionsScanned: len(summaries)}

	// Group sessions by branch+project for fork detection.
	type groupKey struct {
		projectPath string
		branch      string
	}
	groups := make(map[groupKey][]session.ID)
	for _, sm := range summaries {
		if sm.Branch == "" {
			continue
		}
		key := groupKey{projectPath: sm.ProjectPath, branch: sm.Branch}
		groups[key] = append(groups[key], sm.ID)
	}

	// For each group with 2+ sessions, load full sessions and detect forks.
	for _, ids := range groups {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if len(ids) < 2 {
			continue
		}

		var sessions []*session.Session
		for _, id := range ids {
			sess, err := s.store.Get(id)
			if err != nil {
				continue
			}
			sessions = append(sessions, sess)
		}

		if len(sessions) < 2 {
			continue
		}

		relations := session.DetectForks(sessions)
		for _, rel := range relations {
			if err := s.store.SaveForkRelation(rel); err != nil {
				log.Printf("[fork-detect] failed to save relation: %v", err)
				continue
			}
			result.RelationsSaved++
		}
		result.ForksDetected += len(relations)
	}

	return result, nil
}
