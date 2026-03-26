package service

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// BackfillResult contains the results of a remote_url backfill operation.
type BackfillResult struct {
	Candidates int // total sessions with empty remote_url
	Updated    int // sessions successfully updated
	Skipped    int // sessions where git remote couldn't be resolved
}

// worktreeHashRe matches OpenCode worktree paths:
//   - .local/share/opencode/worktree/{hash}/{branch}
//   - .opencode-worktrees/{project}/{branch}
var worktreeHashRe = regexp.MustCompile(
	`/\.local/share/opencode/worktree/([a-f0-9]{40})/` +
		`|/\.opencode-worktrees/([^/]+)/`,
)

// extractWorktreeKey returns a grouping key for worktree paths.
// For standard OpenCode worktrees, it returns the 40-char hash.
// For .opencode-worktrees, it returns the project name.
// Returns empty string for non-worktree paths.
func extractWorktreeKey(projectPath string) string {
	matches := worktreeHashRe.FindStringSubmatch(projectPath)
	if matches == nil {
		return ""
	}
	// Group 1 = hash from .local/share/opencode/worktree/{hash}/
	if matches[1] != "" {
		return "hash:" + matches[1]
	}
	// Group 2 = project name from .opencode-worktrees/{project}/
	if matches[2] != "" {
		return "wt:" + matches[2]
	}
	return ""
}

// BackfillRemoteURLs resolves and persists git remote URLs for sessions
// that have an empty remote_url. This fixes the worktree deduplication issue
// where OpenCode worktree sessions appear as separate "projects" because
// their remote_url was never populated.
//
// Resolution strategy (in order):
//  1. Direct git: run `git remote get-url origin` in the session's ProjectPath
//  2. Sibling worktree: if the path matches a worktree pattern, find other sessions
//     with the same worktree hash/key that already have a remote_url
func (s *SessionService) BackfillRemoteURLs(ctx context.Context) (*BackfillResult, error) {
	candidates, err := s.store.ListSessionsWithEmptyRemoteURL(0) // 0 = all
	if err != nil {
		return nil, err
	}

	result := &BackfillResult{Candidates: len(candidates)}

	// Group by project_path to avoid redundant git calls.
	pathToURL := make(map[string]string)

	// Phase 1: resolve via git directly.
	var unresolved []session.BackfillCandidate
	for _, c := range candidates {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		remoteURL, cached := pathToURL[c.ProjectPath]
		if !cached {
			remoteURL = resolveRemoteURLForPath(c.ProjectPath)
			pathToURL[c.ProjectPath] = remoteURL
		}

		if remoteURL != "" {
			if err := s.store.UpdateRemoteURL(c.ID, remoteURL); err != nil {
				log.Printf("[backfill] failed to update session %s: %v", c.ID, err)
				result.Skipped++
				continue
			}
			result.Updated++
			continue
		}

		unresolved = append(unresolved, c)
	}

	// Phase 2: resolve via sibling worktree matching.
	// Build a map of worktree key → known remote_url from ALL sessions in the DB.
	if len(unresolved) > 0 {
		siblingMap := s.buildWorktreeSiblingMap()

		for _, c := range unresolved {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}

			wtKey := extractWorktreeKey(c.ProjectPath)
			if wtKey == "" {
				result.Skipped++
				continue
			}

			remoteURL, found := siblingMap[wtKey]
			if !found || remoteURL == "" {
				result.Skipped++
				continue
			}

			if err := s.store.UpdateRemoteURL(c.ID, remoteURL); err != nil {
				log.Printf("[backfill] failed to update session %s (sibling): %v", c.ID, err)
				result.Skipped++
				continue
			}
			result.Updated++
			log.Printf("[backfill] resolved %s via sibling worktree → %s", c.ID, remoteURL)
		}
	}

	return result, nil
}

// buildWorktreeSiblingMap builds a map of worktree key → remote_url
// by scanning all sessions that HAVE a remote_url and extracting their worktree key.
func (s *SessionService) buildWorktreeSiblingMap() map[string]string {
	// List all sessions (we only need ones with remote_url).
	summaries, err := s.store.List(session.ListOptions{All: true})
	if err != nil {
		return nil
	}

	m := make(map[string]string)
	for _, sm := range summaries {
		if sm.RemoteURL == "" {
			continue
		}
		key := extractWorktreeKey(sm.ProjectPath)
		if key == "" {
			continue
		}
		// First remote_url wins for each key (they should all be the same).
		if _, exists := m[key]; !exists {
			m[key] = sm.RemoteURL
		}
	}

	// Also match .opencode-worktrees by project name:
	// If a session has path like .opencode-worktrees/backend/... and another session
	// with remote_url has "backend" as the last path component, match them.
	for _, sm := range summaries {
		if sm.RemoteURL == "" {
			continue
		}
		// Extract basename from project_path.
		lastSlash := strings.LastIndex(sm.ProjectPath, "/")
		if lastSlash < 0 {
			continue
		}
		basename := sm.ProjectPath[lastSlash+1:]
		if basename == "" {
			continue
		}
		wtKey := "wt:" + basename
		if _, exists := m[wtKey]; !exists {
			m[wtKey] = sm.RemoteURL
		}
	}

	return m
}

// ForkDetectionResult contains the results of a batch fork detection operation.
type ForkDetectionResult struct {
	SessionsScanned int
	ForksDetected   int
	RelationsSaved  int
}

// rewindRe matches session summaries like "Rewind of ses_XXX at message N".
var rewindRe = regexp.MustCompile(`(?i)rewind\s+of\s+(ses_\w+)\s+at\s+message\s+(\d+)`)

// DetectForksBatch runs fork detection on all sessions grouped by project
// and persists the results to the session_forks table.
//
// Two detection strategies:
//  1. Content-hash comparison: groups sessions by project path (+ branch if set)
//     and compares message hashes to find shared prefixes.
//  2. Rewind detection: parses "Rewind of ses_XXX at message N" from summaries
//     and creates fork relations with the specified fork point.
func (s *SessionService) DetectForksBatch(ctx context.Context) (*ForkDetectionResult, error) {
	// List all sessions.
	summaries, err := s.store.List(session.ListOptions{All: true})
	if err != nil {
		return nil, err
	}

	result := &ForkDetectionResult{SessionsScanned: len(summaries)}

	// Build a lookup of session ID → summary for rewind detection.
	summaryByID := make(map[session.ID]session.Summary, len(summaries))
	for _, sm := range summaries {
		summaryByID[sm.ID] = sm
	}

	// ── Phase 1: Rewind detection (parse summary text) ──
	rewindOriginals := make(map[session.ID]bool) // sessions that are rewind targets
	rewindForks := make(map[session.ID]bool)     // sessions that are rewinds

	for _, sm := range summaries {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		matches := rewindRe.FindStringSubmatch(sm.Summary)
		if matches == nil {
			continue
		}
		originalID := session.ID(matches[1])
		forkPoint, _ := strconv.Atoi(matches[2])

		// Verify the original session exists.
		if _, ok := summaryByID[originalID]; !ok {
			continue
		}

		// Load the original to compute shared tokens.
		orig, getErr := s.store.Get(originalID)
		if getErr != nil {
			continue
		}

		var sharedInput, sharedOutput int
		for k := 0; k < forkPoint && k < len(orig.Messages); k++ {
			sharedInput += orig.Messages[k].InputTokens
			sharedOutput += orig.Messages[k].OutputTokens
		}

		rel := session.ForkRelation{
			OriginalID:         originalID,
			ForkID:             sm.ID,
			ForkPoint:          forkPoint,
			SharedMessages:     forkPoint,
			OverlapRatio:       1.0, // rewinds are exact copies up to fork point
			Reason:             fmt.Sprintf("Rewind at message %d", forkPoint),
			ForkContext:        sm.Summary,
			SharedInputTokens:  sharedInput,
			SharedOutputTokens: sharedOutput,
		}
		if err := s.store.SaveForkRelation(rel); err != nil {
			log.Printf("[fork-detect] failed to save rewind relation: %v", err)
			continue
		}
		result.RelationsSaved++
		result.ForksDetected++
		rewindOriginals[originalID] = true
		rewindForks[sm.ID] = true
	}

	// ── Phase 2: Content-hash fork detection ──
	// Group sessions by project path. Sessions with a branch are also
	// grouped by project+branch for tighter comparison within branches,
	// but branchless sessions are grouped by project path only.
	type groupKey struct {
		projectPath string
		branch      string
	}
	groups := make(map[groupKey][]session.ID)
	for _, sm := range summaries {
		// Skip sessions already identified as rewind forks (they're handled above).
		if rewindForks[sm.ID] {
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
