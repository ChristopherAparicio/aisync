package categorizer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ForkReasonResult holds the LLM-generated reason for a fork.
type ForkReasonResult struct {
	Reason     string  `json:"reason"`     // one-sentence summary of why the fork was created
	Confidence float64 `json:"confidence"` // 0.0-1.0
}

// AnalyzeForkReason uses an LLM to summarize why a session was forked.
// It sends the last few messages before the fork point and the first few
// messages after to understand the intention shift.
//
// Returns a one-sentence reason like "Switched to API documentation" or
// "Pivoted to fix phone extraction bug".
func AnalyzeForkReason(ctx context.Context, analyzer analysis.Analyzer, forkSession *session.Session, forkPoint int) ForkReasonResult {
	prompt := buildForkReasonPrompt(forkSession, forkPoint)

	report, err := analyzer.Analyze(ctx, analysis.AnalyzeRequest{
		Session: session.Session{
			ID:       forkSession.ID,
			Provider: forkSession.Provider,
			Messages: []session.Message{
				{Role: session.RoleUser, Content: prompt},
			},
			TokenUsage: session.TokenUsage{TotalTokens: 100},
		},
	})
	if err != nil {
		return ForkReasonResult{Reason: extractFallbackReason(forkSession, forkPoint)}
	}

	return parseForkReasonResult(report.Summary, forkSession, forkPoint)
}

// buildForkReasonPrompt creates a prompt for the LLM to analyze the fork reason.
func buildForkReasonPrompt(sess *session.Session, forkPoint int) string {
	var b strings.Builder

	b.WriteString("Analyze why this AI coding session was forked (branched off) at a specific point.\n\n")

	if sess.Summary != "" {
		b.WriteString(fmt.Sprintf("Session title: %s\n", sess.Summary))
	}

	// Show last 3 messages BEFORE fork (the context before divergence).
	b.WriteString("\n--- Messages BEFORE the fork point ---\n")
	start := forkPoint - 3
	if start < 0 {
		start = 0
	}
	for i := start; i < forkPoint && i < len(sess.Messages); i++ {
		msg := &sess.Messages[i]
		content := msg.Content
		if len(content) > 300 {
			content = content[:297] + "..."
		}
		b.WriteString(fmt.Sprintf("[%s] %s\n", msg.Role, content))
	}

	// Show first 3 messages AFTER fork (the new direction).
	b.WriteString("\n--- Messages AFTER the fork point (new direction) ---\n")
	count := 0
	for i := forkPoint; i < len(sess.Messages) && count < 3; i++ {
		msg := &sess.Messages[i]
		content := msg.Content
		if len(content) > 300 {
			content = content[:297] + "..."
		}
		b.WriteString(fmt.Sprintf("[%s] %s\n", msg.Role, content))
		count++
	}

	b.WriteString("\nSummarize in ONE sentence why the user forked the session at this point.\n")
	b.WriteString("Focus on: what changed in the user's intention or focus.\n\n")
	b.WriteString("Respond with ONLY a JSON object in the summary field:\n")
	b.WriteString(`{"reason": "<one sentence>", "confidence": <0.0-1.0>}`)
	b.WriteString("\n")

	return b.String()
}

// parseForkReasonResult extracts a ForkReasonResult from the LLM output.
func parseForkReasonResult(raw string, sess *session.Session, forkPoint int) ForkReasonResult {
	raw = strings.TrimSpace(raw)

	var result ForkReasonResult
	if err := json.Unmarshal([]byte(raw), &result); err == nil && result.Reason != "" {
		return result
	}

	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			if err := json.Unmarshal([]byte(raw[start:end+1]), &result); err == nil && result.Reason != "" {
				return result
			}
		}
	}

	// Fallback: use the raw text as reason.
	if len(raw) > 200 {
		raw = raw[:197] + "..."
	}
	if raw != "" {
		return ForkReasonResult{Reason: raw, Confidence: 0.3}
	}

	return ForkReasonResult{Reason: extractFallbackReason(sess, forkPoint)}
}

// extractFallbackReason extracts a reason from the first user message after fork.
func extractFallbackReason(sess *session.Session, forkPoint int) string {
	for i := forkPoint; i < len(sess.Messages); i++ {
		if sess.Messages[i].Role == session.RoleUser && sess.Messages[i].Content != "" {
			content := sess.Messages[i].Content
			if len(content) > 150 {
				content = content[:147] + "..."
			}
			return content
		}
	}
	return "Unknown reason"
}
