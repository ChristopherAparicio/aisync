// Package tagger classifies AI coding sessions by type (feature, bug, refactor, etc.)
// using a lightweight LLM call. It examines the first N user messages and the session
// summary to determine the session's purpose.
//
// The tagger reuses the analysis.Analyzer port — any configured LLM adapter
// (Ollama, Anthropic, OpenCode, Claude CLI) can be used for classification.
package tagger

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// DefaultMaxMessages is the number of user messages to send to the classifier.
const DefaultMaxMessages = 10

// Result is the output of session classification.
type Result struct {
	Tag        string  `json:"tag"`        // the classified session type
	Confidence float64 `json:"confidence"` // 0.0-1.0
	Reasoning  string  `json:"reasoning"`  // short explanation
}

// Classify determines the session type using an LLM analyzer.
// It sends the first maxMessages user messages + summary to the LLM
// and asks it to pick from the available tags.
//
// Returns ("other", 0, "") if classification fails (best-effort).
func Classify(ctx context.Context, analyzer analysis.Analyzer, sess *session.Session, tags []string, maxMessages int) Result {
	if maxMessages <= 0 {
		maxMessages = DefaultMaxMessages
	}
	if len(tags) == 0 {
		tags = session.DefaultSessionTypes
	}

	prompt := buildClassifyPrompt(sess, tags, maxMessages)

	// Use the analyzer's Analyze method with a synthetic session containing
	// just the classification prompt. This is a lightweight call.
	report, err := analyzer.Analyze(ctx, analysis.AnalyzeRequest{
		Session: session.Session{
			ID:       sess.ID,
			Provider: sess.Provider,
			Agent:    sess.Agent,
			Branch:   sess.Branch,
			Messages: []session.Message{
				{Role: session.RoleUser, Content: prompt},
			},
			TokenUsage: session.TokenUsage{TotalTokens: 100},
		},
	})
	if err != nil {
		return Result{Tag: "other", Confidence: 0, Reasoning: fmt.Sprintf("classification failed: %v", err)}
	}

	// The LLM returns an AnalysisReport — we extract the tag from the summary field.
	// The prompt instructs the LLM to return JSON in the summary.
	return parseClassifyResult(report.Summary, tags)
}

// ClassifyDirect calls the analyzer with a purpose-built prompt that returns
// a simple JSON classification instead of a full analysis report.
// This is more efficient than Classify() but requires the analyzer to handle
// the non-standard response format.
func ClassifyDirect(ctx context.Context, analyzer analysis.Analyzer, sess *session.Session, tags []string, maxMessages int) Result {
	// For now, delegate to Classify. In future, this could use a simpler
	// LLM call that doesn't go through the analysis prompt/report format.
	return Classify(ctx, analyzer, sess, tags, maxMessages)
}

// buildClassifyPrompt creates the classification prompt from session data.
func buildClassifyPrompt(sess *session.Session, tags []string, maxMessages int) string {
	var b strings.Builder

	b.WriteString("Classify this AI coding session into ONE of these types:\n")
	for _, tag := range tags {
		b.WriteString(fmt.Sprintf("  - %s\n", tag))
	}
	b.WriteString("\n")

	if sess.Summary != "" {
		b.WriteString(fmt.Sprintf("Session summary: %s\n\n", sess.Summary))
	}

	b.WriteString("User messages (first messages of the session):\n")
	count := 0
	for i := range sess.Messages {
		if sess.Messages[i].Role != session.RoleUser {
			continue
		}
		content := sess.Messages[i].Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		b.WriteString(fmt.Sprintf("[%d] %s\n", count+1, content))
		count++
		if count >= maxMessages {
			break
		}
	}

	b.WriteString("\nRespond with ONLY a JSON object in the summary field:\n")
	b.WriteString(`{"tag": "<one of the types above>", "confidence": <0.0-1.0>, "reasoning": "<one sentence>"}`)
	b.WriteString("\n")

	return b.String()
}

// parseClassifyResult extracts a Result from the LLM's summary output.
func parseClassifyResult(raw string, validTags []string) Result {
	raw = strings.TrimSpace(raw)

	// Try direct JSON parse.
	var result Result
	if err := json.Unmarshal([]byte(raw), &result); err == nil {
		if isValidTag(result.Tag, validTags) {
			return result
		}
	}

	// Try extracting JSON from the raw text.
	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			if err := json.Unmarshal([]byte(raw[start:end+1]), &result); err == nil {
				if isValidTag(result.Tag, validTags) {
					return result
				}
			}
		}
	}

	// Fallback: look for a tag word in the raw text.
	lower := strings.ToLower(raw)
	for _, tag := range validTags {
		if strings.Contains(lower, tag) {
			return Result{Tag: tag, Confidence: 0.5, Reasoning: "extracted from raw text"}
		}
	}

	return Result{Tag: "other", Confidence: 0, Reasoning: "could not parse classification"}
}

func isValidTag(tag string, valid []string) bool {
	for _, v := range valid {
		if v == tag {
			return true
		}
	}
	return false
}
