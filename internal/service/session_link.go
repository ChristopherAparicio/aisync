package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Link ──

// LinkRequest contains inputs for linking a session.
type LinkRequest struct {
	SessionID   session.ID // empty = resolve from branch
	ProjectPath string
	Branch      string
	PRNumber    int
	CommitSHA   string
	AutoDetect  bool // auto-detect PR from branch
}

// LinkResult contains the outcome of a link operation.
type LinkResult struct {
	SessionID session.ID
	PRNumber  int    // only if a PR was linked
	CommitSHA string // only if a commit was linked
}

// Link associates a session with a PR, commit, or other git object.
func (s *SessionService) Link(req LinkRequest) (*LinkResult, error) {
	if req.PRNumber == 0 && req.CommitSHA == "" && !req.AutoDetect {
		return nil, fmt.Errorf("specify a PR number, commit SHA, or auto-detect")
	}

	// Resolve session ID
	sessionID := req.SessionID
	if sessionID == "" {
		sess, err := s.store.GetLatestByBranch(req.ProjectPath, req.Branch)
		if err != nil {
			return nil, fmt.Errorf("no session found for branch %q: %w", req.Branch, err)
		}
		sessionID = sess.ID
	}

	// Auto-detect PR from branch
	prNumber := req.PRNumber
	if req.AutoDetect && prNumber == 0 {
		if s.platform == nil {
			return nil, fmt.Errorf("platform not available for PR auto-detection")
		}
		pr, err := s.platform.GetPRForBranch(req.Branch)
		if err != nil {
			return nil, fmt.Errorf("no open PR found for branch %q: %w", req.Branch, err)
		}
		prNumber = pr.Number
	}

	result := &LinkResult{SessionID: sessionID}

	// Add PR link
	if prNumber > 0 {
		link := session.Link{
			LinkType: session.LinkPR,
			Ref:      strconv.Itoa(prNumber),
		}
		if err := s.store.AddLink(sessionID, link); err != nil {
			return nil, fmt.Errorf("linking to PR #%d: %w", prNumber, err)
		}
		result.PRNumber = prNumber
	}

	// Add commit link
	if req.CommitSHA != "" {
		link := session.Link{
			LinkType: session.LinkCommit,
			Ref:      req.CommitSHA,
		}
		if err := s.store.AddLink(sessionID, link); err != nil {
			return nil, fmt.Errorf("linking to commit %s: %w", req.CommitSHA, err)
		}
		result.CommitSHA = req.CommitSHA
	}

	if result.PRNumber == 0 && result.CommitSHA == "" {
		return nil, fmt.Errorf("no links were added")
	}

	return result, nil
}

// ── Comment ──

// AisyncMarker is the HTML comment used to identify aisync PR comments for idempotent updates.
const AisyncMarker = "<!-- aisync -->"

// CommentRequest contains inputs for posting a PR comment.
type CommentRequest struct {
	SessionID   session.ID // empty = resolve from branch or PR
	ProjectPath string
	Branch      string
	PRNumber    int // 0 = auto-detect
}

// CommentResult contains the outcome of a comment operation.
type CommentResult struct {
	PRNumber int
	Updated  bool // true if an existing comment was updated, false if new
}

// Comment posts or updates a PR comment with an AI session summary.
func (s *SessionService) Comment(req CommentRequest) (*CommentResult, error) {
	if s.platform == nil {
		return nil, fmt.Errorf("platform not available: cannot post PR comments")
	}

	// Determine PR number
	prNumber := req.PRNumber
	if prNumber == 0 {
		pr, err := s.platform.GetPRForBranch(req.Branch)
		if err != nil {
			return nil, fmt.Errorf("no open PR found for branch %q (use --pr to specify): %w", req.Branch, err)
		}
		prNumber = pr.Number
	}

	// Find session
	sess, err := s.resolveSessionForComment(req, prNumber)
	if err != nil {
		return nil, err
	}

	// Build comment body
	body := BuildCommentBody(sess)

	// Check for existing aisync comment (idempotent update)
	comments, err := s.platform.ListComments(prNumber)
	if err != nil {
		return nil, fmt.Errorf("listing PR comments: %w", err)
	}

	var existingID int64
	for _, c := range comments {
		if strings.Contains(c.Body, AisyncMarker) {
			existingID = c.ID
			break
		}
	}

	updated := false
	if existingID > 0 {
		if updateErr := s.platform.UpdateComment(existingID, body); updateErr != nil {
			return nil, fmt.Errorf("updating comment: %w", updateErr)
		}
		updated = true
	} else {
		if addErr := s.platform.AddComment(prNumber, body); addErr != nil {
			return nil, fmt.Errorf("adding comment: %w", addErr)
		}
	}

	return &CommentResult{
		PRNumber: prNumber,
		Updated:  updated,
	}, nil
}

func (s *SessionService) resolveSessionForComment(req CommentRequest, prNumber int) (*session.Session, error) {
	if req.SessionID != "" {
		return s.store.Get(req.SessionID)
	}

	// Try PR link first
	summaries, lookupErr := s.store.GetByLink(session.LinkPR, strconv.Itoa(prNumber))
	if lookupErr == nil && len(summaries) > 0 {
		return s.store.Get(summaries[0].ID)
	}

	// Fall back to branch
	return s.store.GetLatestByBranch(req.ProjectPath, req.Branch)
}

// BuildCommentBody creates the Markdown comment body from a session.
// Exported so it can be used by the CLI for display purposes.
func BuildCommentBody(sess *session.Session) string {
	var b strings.Builder

	b.WriteString(AisyncMarker)
	b.WriteString("\n## AI Session Summary\n\n")
	b.WriteString(fmt.Sprintf("**Session:** `%s`\n", sess.ID))
	b.WriteString(fmt.Sprintf("**Provider:** %s\n", sess.Provider))
	b.WriteString(fmt.Sprintf("**Branch:** %s\n", sess.Branch))

	if sess.Summary != "" {
		b.WriteString(fmt.Sprintf("\n### Summary\n\n%s\n", sess.Summary))
	}

	if sess.TokenUsage.TotalTokens > 0 {
		b.WriteString("\n### Token Usage\n\n")
		b.WriteString("| Metric | Count |\n")
		b.WriteString("|--------|-------|\n")
		b.WriteString(fmt.Sprintf("| Input  | %d |\n", sess.TokenUsage.InputTokens))
		b.WriteString(fmt.Sprintf("| Output | %d |\n", sess.TokenUsage.OutputTokens))
		b.WriteString(fmt.Sprintf("| Total  | %d |\n", sess.TokenUsage.TotalTokens))
	}

	b.WriteString(fmt.Sprintf("\n**Messages:** %d\n", len(sess.Messages)))

	if len(sess.FileChanges) > 0 {
		b.WriteString("\n### Files Changed\n\n")
		for _, fc := range sess.FileChanges {
			b.WriteString(fmt.Sprintf("- `%s` (%s)\n", fc.FilePath, fc.ChangeType))
		}
	}

	b.WriteString("\n---\n*Posted by [aisync](https://github.com/ChristopherAparicio/aisync)*\n")

	return b.String()
}
