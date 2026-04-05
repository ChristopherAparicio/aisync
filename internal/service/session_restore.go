package service

import (
	"fmt"

	restoresvc "github.com/ChristopherAparicio/aisync/internal/restore"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Restore ──

// RestoreRequest contains inputs for a restore operation.
type RestoreRequest struct {
	ProjectPath  string
	Branch       string
	Agent        string
	SessionID    session.ID
	ProviderName session.ProviderName
	AsContext    bool
	PRNumber     int    // if > 0, look up session linked to this PR
	FilePath     string // if set, find the most recent session that touched this file

	// Worktree creates a detached git worktree at the session's commit SHA.
	// The worktree name is auto-generated (e.g. ".worktrees/pr-42-fix-auth").
	Worktree bool

	// DryRun previews the restore without writing anything to disk.
	// When true, the restore pipeline runs up to (but not including) the
	// actual file write / provider import step and returns a DryRunPreview.
	DryRun bool

	// Filters is a chain of SessionFilter strategies applied before restore.
	// Each filter transforms the session (e.g. clean errors, redact secrets).
	Filters []session.SessionFilter
}

// RestoreResult contains the output of a restore operation.
type RestoreResult struct {
	Session       *session.Session
	Method        string // "native", "converted", or "context"
	ContextPath   string
	WorktreePath  string                 // only set if Worktree was requested
	FilterResults []session.FilterResult // results from each applied filter
	DryRun        *DryRunPreview         // only set if DryRun was requested
}

// DryRunPreview contains a preview of what a restore would do without actually doing it.
type DryRunPreview struct {
	SessionID     session.ID             `json:"session_id"`
	Provider      session.ProviderName   `json:"provider"`
	Branch        string                 `json:"branch"`
	Summary       string                 `json:"summary"`
	Method        string                 `json:"method"` // "native", "converted", or "context"
	MessageCount  int                    `json:"message_count"`
	ToolCallCount int                    `json:"tool_call_count"`
	ErrorCount    int                    `json:"error_count"`
	InputTokens   int                    `json:"input_tokens"`
	OutputTokens  int                    `json:"output_tokens"`
	TotalTokens   int                    `json:"total_tokens"`
	FileChanges   int                    `json:"file_changes"`
	FilterResults []session.FilterResult `json:"filter_results,omitempty"`
}

// Restore looks up a session and imports it into a target provider.
//
// Session resolution order (first match wins):
//  1. Explicit SessionID
//  2. --pr N → PullRequestStore.GetSessionsForPR (rich metadata, replaces old GetByLink)
//  3. --file path → GetSessionsByFile, take most recent
//  4. Default: latest session on current branch
func (s *SessionService) Restore(req RestoreRequest) (*RestoreResult, error) {
	sessionID := req.SessionID

	// ── PR-based lookup ──
	if req.PRNumber > 0 && sessionID == "" {
		sid, err := s.resolveSessionFromPR(req.PRNumber)
		if err != nil {
			return nil, err
		}
		sessionID = sid
	}

	// ── File-based lookup ──
	if req.FilePath != "" && sessionID == "" {
		sid, err := s.resolveSessionFromFile(req.FilePath, req.ProjectPath)
		if err != nil {
			return nil, err
		}
		sessionID = sid
	}

	svc := restoresvc.NewServiceWithConverter(s.registry, s.store, s.converter)

	result, err := svc.Restore(restoresvc.Request{
		ProjectPath:  req.ProjectPath,
		Branch:       req.Branch,
		SessionID:    sessionID,
		ProviderName: req.ProviderName,
		Agent:        req.Agent,
		AsContext:    req.AsContext,
		DryRun:       req.DryRun,
		Filters:      req.Filters,
	})
	if err != nil {
		return nil, err
	}

	out := &RestoreResult{
		Session:       result.Session,
		Method:        result.Method,
		ContextPath:   result.ContextPath,
		FilterResults: result.FilterResults,
	}

	// If dry-run, build preview and return early (no worktree creation).
	if result.DryRun != nil {
		out.DryRun = &DryRunPreview{
			SessionID:     result.DryRun.SessionID,
			Provider:      result.DryRun.Provider,
			Branch:        result.DryRun.Branch,
			Summary:       result.DryRun.Summary,
			Method:        result.DryRun.Method,
			MessageCount:  result.DryRun.MessageCount,
			ToolCallCount: result.DryRun.ToolCallCount,
			ErrorCount:    result.DryRun.ErrorCount,
			InputTokens:   result.DryRun.InputTokens,
			OutputTokens:  result.DryRun.OutputTokens,
			TotalTokens:   result.DryRun.TotalTokens,
			FileChanges:   result.DryRun.FileChanges,
			FilterResults: result.DryRun.FilterResults,
		}
		return out, nil
	}

	// ── Worktree creation ──
	if req.Worktree && result.Session != nil && result.Session.CommitSHA != "" {
		wtPath, wtErr := s.createWorktree(result.Session, req.PRNumber)
		if wtErr != nil {
			return nil, fmt.Errorf("creating worktree: %w", wtErr)
		}
		out.WorktreePath = wtPath
	}

	return out, nil
}

// resolveSessionFromPR uses the PullRequestStore to find sessions linked to a PR.
// It resolves the repo owner/name from config defaults if available.
func (s *SessionService) resolveSessionFromPR(prNumber int) (session.ID, error) {
	owner, repo := s.resolveRepo()
	if owner == "" || repo == "" {
		return "", fmt.Errorf("no session linked to PR #%d: GitHub default_owner/default_repo not configured", prNumber)
	}

	summaries, err := s.store.GetSessionsForPR(owner, repo, prNumber)
	if err != nil {
		return "", fmt.Errorf("no session linked to PR #%d: %w", prNumber, err)
	}
	if len(summaries) == 0 {
		return "", fmt.Errorf("no session linked to PR #%d", prNumber)
	}
	// GetSessionsForPR returns sessions ordered by created_at DESC → first is most recent.
	return summaries[0].ID, nil
}

// resolveSessionFromFile finds the most recent session that touched the given file.
func (s *SessionService) resolveSessionFromFile(filePath, projectPath string) (session.ID, error) {
	entries, err := s.store.GetSessionsByFile(session.BlameQuery{
		FilePath: filePath,
	})
	if err != nil {
		return "", fmt.Errorf("no session found for file %q: %w", filePath, err)
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("no session found that touched file %q", filePath)
	}
	// GetSessionsByFile returns entries ordered by created_at DESC → first is most recent.
	return entries[0].SessionID, nil
}

// resolveRepo returns the GitHub owner and repo name from config.
func (s *SessionService) resolveRepo() (string, string) {
	if s.cfg == nil {
		return "", ""
	}
	return s.cfg.GetGitHubDefaultOwner(), s.cfg.GetGitHubDefaultRepo()
}

// createWorktree creates a git worktree at the session's commit SHA.
// Returns the worktree path or an error.
func (s *SessionService) createWorktree(sess *session.Session, prNumber int) (string, error) {
	if s.git == nil {
		return "", fmt.Errorf("git client unavailable — cannot create worktree")
	}

	// Generate a descriptive worktree name.
	name := fmt.Sprintf("restore-%s", sess.ID[:8])
	if prNumber > 0 {
		name = fmt.Sprintf("pr-%d-%s", prNumber, sess.ID[:8])
	}
	wtPath := fmt.Sprintf(".worktrees/%s", name)

	if err := s.git.WorktreeAdd(wtPath, sess.CommitSHA); err != nil {
		return "", err
	}
	return wtPath, nil
}
