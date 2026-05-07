package service

import (
	"context"
	"fmt"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Tag operations ──
//
// These methods provide manual user-defined tagging on top of the automatic
// session_type classification. Tags are arbitrary lowercase identifiers
// attached to sessions for free-form organization (e.g. "urgent", "blocked",
// "wip", "review-needed").
//
// Storage: the session_tags join table (PK on session_id+tag, FK cascade-deletes).

// AddTags attaches one or more tags to a session. Duplicates are silently
// ignored, empty tags are dropped, and inputs are normalized (lowercased,
// whitespace-trimmed). Returns the number of newly inserted (tag, session)
// pairs — a value of 0 means all the tags were already present.
func (s *SessionService) AddTags(_ context.Context, id session.ID, tags []string) (int, error) {
	if id == "" {
		return 0, fmt.Errorf("AddTags: empty session id")
	}
	// Verify the session exists (FK on session_tags would fail otherwise,
	// but a clear error is friendlier than a SQLite constraint message).
	if _, err := s.store.Get(id); err != nil {
		return 0, fmt.Errorf("session %s: %w", id, err)
	}
	return s.store.AddTags(id, tags)
}

// RemoveTags detaches one or more tags from a session. Tags absent from
// the session are silently ignored. Returns the number of tags actually
// removed.
func (s *SessionService) RemoveTags(_ context.Context, id session.ID, tags []string) (int, error) {
	if id == "" {
		return 0, fmt.Errorf("RemoveTags: empty session id")
	}
	return s.store.RemoveTags(id, tags)
}

// GetSessionTags returns the tags attached to a session, sorted alphabetically.
// An empty slice (not nil) is returned when the session has no tags.
func (s *SessionService) GetSessionTags(_ context.Context, id session.ID) ([]string, error) {
	if id == "" {
		return nil, fmt.Errorf("GetSessionTags: empty session id")
	}
	tags, err := s.store.GetTags(id)
	if err != nil {
		return nil, err
	}
	if tags == nil {
		tags = []string{}
	}
	return tags, nil
}

// ListAllTags returns every distinct tag in use across all sessions, with
// the count of sessions carrying each one. Sorted by descending count then
// alphabetical tag name.
func (s *SessionService) ListAllTags(_ context.Context) ([]session.TagCount, error) {
	out, err := s.store.ListAllTags()
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []session.TagCount{}
	}
	return out, nil
}

// ── Resolving the "current" session ──

// ResolveCurrentSessionID returns the session ID to operate on when the user
// runs `aisync tag <tag>` without providing an explicit session ID. The
// strategy is:
//
//  1. If a project path can be detected (cwd-based), look up the most recent
//     session for that project + the current git branch (when available).
//  2. If no branch can be resolved, fall back to the most recent session in
//     the project regardless of branch.
//
// Returns ErrNoCurrentSession when no candidate can be found, so callers
// can produce a friendly "no current session — pass an explicit id" error.
func (s *SessionService) ResolveCurrentSessionID(_ context.Context, projectPath string) (session.ID, error) {
	if projectPath == "" {
		return "", ErrNoCurrentSession
	}

	// Try branch-scoped lookup first when git context is available.
	if s.git != nil {
		if branch, err := s.git.CurrentBranch(); err == nil && branch != "" {
			if sess, err := s.store.GetLatestByBranch(projectPath, branch); err == nil && sess != nil {
				return sess.ID, nil
			}
		}
	}

	// Fall back to most-recent in project, any branch.
	summaries, err := s.store.List(session.ListOptions{
		ProjectPath: projectPath,
		All:         true,
	})
	if err != nil {
		return "", fmt.Errorf("list project sessions: %w", err)
	}
	if len(summaries) == 0 {
		return "", ErrNoCurrentSession
	}
	// store.List sorts by created_at DESC.
	return summaries[0].ID, nil
}

// ErrNoCurrentSession is returned when no session can be inferred for the
// current working directory + branch. CLI commands should fall back to
// asking the user for an explicit session ID.
var ErrNoCurrentSession = fmt.Errorf("no current session found for project + branch")
