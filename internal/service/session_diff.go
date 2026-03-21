package service

import (
	"context"
	"fmt"
	"sort"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Diff ──

// DiffRequest contains inputs for comparing two sessions.
type DiffRequest struct {
	LeftID  string // session ID or commit SHA
	RightID string // session ID or commit SHA
}

// Diff compares two sessions side-by-side and returns a structured diff.
// It computes token deltas, cost deltas, file overlap, tool usage comparison,
// and identifies where the message sequences diverge.
func (s *SessionService) Diff(ctx context.Context, req DiffRequest) (*session.DiffResult, error) {
	if req.LeftID == "" || req.RightID == "" {
		return nil, fmt.Errorf("both left and right session IDs are required")
	}

	left, err := s.Get(req.LeftID)
	if err != nil {
		return nil, fmt.Errorf("left session: %w", err)
	}

	right, err := s.Get(req.RightID)
	if err != nil {
		return nil, fmt.Errorf("right session: %w", err)
	}

	result := &session.DiffResult{
		Left:         buildDiffSide(left),
		Right:        buildDiffSide(right),
		TokenDelta:   computeTokenDelta(left, right),
		FileDiff:     computeFileDiff(left, right),
		ToolDiff:     computeToolDiff(left, right),
		MessageDelta: computeMessageDelta(left, right),
	}

	// Cost delta (uses pricing calculator).
	leftCost := s.pricing.SessionCost(left)
	rightCost := s.pricing.SessionCost(right)
	result.CostDelta = session.CostDelta{
		LeftCost:  leftCost.TotalCost.TotalCost,
		RightCost: rightCost.TotalCost.TotalCost,
		Delta:     rightCost.TotalCost.TotalCost - leftCost.TotalCost.TotalCost,
		Currency:  "USD",
	}

	return result, nil
}

// buildDiffSide extracts summary metadata from a session for one side of a diff.
func buildDiffSide(sess *session.Session) session.DiffSide {
	return session.DiffSide{
		ID:           sess.ID,
		Provider:     sess.Provider,
		Branch:       sess.Branch,
		Summary:      sess.Summary,
		MessageCount: len(sess.Messages),
		TotalTokens:  sess.TokenUsage.TotalTokens,
		StorageMode:  sess.StorageMode,
	}
}

// computeTokenDelta calculates the difference in token usage between two sessions.
func computeTokenDelta(left, right *session.Session) session.TokenDelta {
	return session.TokenDelta{
		InputDelta:  right.TokenUsage.InputTokens - left.TokenUsage.InputTokens,
		OutputDelta: right.TokenUsage.OutputTokens - left.TokenUsage.OutputTokens,
		TotalDelta:  right.TokenUsage.TotalTokens - left.TokenUsage.TotalTokens,
	}
}

// computeFileDiff groups file changes into shared, left-only, and right-only.
func computeFileDiff(left, right *session.Session) session.FileDiff {
	leftFiles := make(map[string]struct{}, len(left.FileChanges))
	for _, fc := range left.FileChanges {
		leftFiles[fc.FilePath] = struct{}{}
	}

	rightFiles := make(map[string]struct{}, len(right.FileChanges))
	for _, fc := range right.FileChanges {
		rightFiles[fc.FilePath] = struct{}{}
	}

	var shared, leftOnly, rightOnly []string

	for f := range leftFiles {
		if _, ok := rightFiles[f]; ok {
			shared = append(shared, f)
		} else {
			leftOnly = append(leftOnly, f)
		}
	}
	for f := range rightFiles {
		if _, ok := leftFiles[f]; !ok {
			rightOnly = append(rightOnly, f)
		}
	}

	// Sort for deterministic output.
	sort.Strings(shared)
	sort.Strings(leftOnly)
	sort.Strings(rightOnly)

	return session.FileDiff{
		Shared:    shared,
		LeftOnly:  leftOnly,
		RightOnly: rightOnly,
	}
}

// computeToolDiff compares tool usage between two sessions.
func computeToolDiff(left, right *session.Session) session.ToolDiff {
	leftTools := countToolCalls(left)
	rightTools := countToolCalls(right)

	// Collect all tool names.
	allTools := make(map[string]struct{})
	for name := range leftTools {
		allTools[name] = struct{}{}
	}
	for name := range rightTools {
		allTools[name] = struct{}{}
	}

	// Sort tool names for deterministic output.
	names := make([]string, 0, len(allTools))
	for name := range allTools {
		names = append(names, name)
	}
	sort.Strings(names)

	entries := make([]session.ToolDiffEntry, 0, len(names))
	for _, name := range names {
		lc := leftTools[name]
		rc := rightTools[name]
		entries = append(entries, session.ToolDiffEntry{
			Name:       name,
			LeftCalls:  lc,
			RightCalls: rc,
			CallsDelta: rc - lc,
		})
	}

	return session.ToolDiff{Entries: entries}
}

// countToolCalls returns a map of tool name → call count for a session.
func countToolCalls(sess *session.Session) map[string]int {
	counts := make(map[string]int)
	for i := range sess.Messages {
		for j := range sess.Messages[i].ToolCalls {
			counts[sess.Messages[i].ToolCalls[j].Name]++
		}
	}
	return counts
}

// computeMessageDelta finds the common prefix length and counts remaining messages.
// Two messages are considered identical when they share the same role and content.
func computeMessageDelta(left, right *session.Session) session.MessageDelta {
	minLen := len(left.Messages)
	if len(right.Messages) < minLen {
		minLen = len(right.Messages)
	}

	commonPrefix := 0
	for i := 0; i < minLen; i++ {
		lm := &left.Messages[i]
		rm := &right.Messages[i]
		if lm.Role != rm.Role || lm.Content != rm.Content {
			break
		}
		commonPrefix++
	}

	return session.MessageDelta{
		CommonPrefix: commonPrefix,
		LeftAfter:    len(left.Messages) - commonPrefix,
		RightAfter:   len(right.Messages) - commonPrefix,
	}
}
