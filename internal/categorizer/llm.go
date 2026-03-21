package categorizer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ClassifyResult is the output of LLM-based project classification.
type ClassifyResult struct {
	Category   string  `json:"category"`   // detected project category
	Confidence float64 `json:"confidence"` // 0.0-1.0
	Reasoning  string  `json:"reasoning"`  // short explanation
}

// ClassifyProject determines the project category using an LLM analyzer.
// It sends the session summary, user messages, and file changes to the LLM
// and asks it to pick from the available categories.
//
// This is the LLM fallback — called only when the file heuristic returns "".
// Returns ("", 0, "") if classification fails (best-effort).
func ClassifyProject(ctx context.Context, analyzer analysis.Analyzer, sess *session.Session, categories []string) ClassifyResult {
	if len(categories) == 0 {
		categories = session.DefaultProjectCategories
	}

	prompt := buildProjectPrompt(sess, categories)

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
		return ClassifyResult{Reasoning: fmt.Sprintf("classification failed: %v", err)}
	}

	return parseProjectResult(report.Summary, categories)
}

// buildProjectPrompt creates the project classification prompt.
func buildProjectPrompt(sess *session.Session, categories []string) string {
	var b strings.Builder

	b.WriteString("Classify this software PROJECT into ONE of these categories:\n")
	for _, cat := range categories {
		b.WriteString(fmt.Sprintf("  - %s\n", cat))
	}
	b.WriteString("\n")

	b.WriteString("Context about the project (from an AI coding session):\n\n")

	if sess.Summary != "" {
		b.WriteString(fmt.Sprintf("Session summary: %s\n\n", sess.Summary))
	}

	if sess.Branch != "" {
		b.WriteString(fmt.Sprintf("Branch: %s\n", sess.Branch))
	}

	// File changes give strong signals about the project type.
	if len(sess.FileChanges) > 0 {
		b.WriteString("\nFiles changed in this session:\n")
		limit := len(sess.FileChanges)
		if limit > 20 {
			limit = 20
		}
		for i := 0; i < limit; i++ {
			b.WriteString(fmt.Sprintf("  - %s (%s)\n", sess.FileChanges[i].FilePath, sess.FileChanges[i].ChangeType))
		}
		if len(sess.FileChanges) > 20 {
			b.WriteString(fmt.Sprintf("  ... and %d more files\n", len(sess.FileChanges)-20))
		}
	}

	// First few user messages give context about what kind of project this is.
	b.WriteString("\nFirst user messages:\n")
	count := 0
	for i := range sess.Messages {
		if sess.Messages[i].Role != session.RoleUser {
			continue
		}
		content := sess.Messages[i].Content
		if len(content) > 300 {
			content = content[:300] + "..."
		}
		b.WriteString(fmt.Sprintf("[%d] %s\n", count+1, content))
		count++
		if count >= 5 {
			break
		}
	}

	b.WriteString("\nIMPORTANT: Classify the PROJECT type, not the session task.\n")
	b.WriteString("For example: if someone fixes a bug in a Go API → the project is \"backend\", not \"bug\".\n\n")
	b.WriteString("Respond with ONLY a JSON object in the summary field:\n")
	b.WriteString(`{"category": "<one of the categories above>", "confidence": <0.0-1.0>, "reasoning": "<one sentence>"}`)
	b.WriteString("\n")

	return b.String()
}

// parseProjectResult extracts a ClassifyResult from the LLM's summary output.
func parseProjectResult(raw string, validCategories []string) ClassifyResult {
	raw = strings.TrimSpace(raw)

	// Try direct JSON parse.
	var result ClassifyResult
	if err := json.Unmarshal([]byte(raw), &result); err == nil {
		if isValidCategory(result.Category, validCategories) {
			return result
		}
	}

	// Try extracting JSON from the raw text.
	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			if err := json.Unmarshal([]byte(raw[start:end+1]), &result); err == nil {
				if isValidCategory(result.Category, validCategories) {
					return result
				}
			}
		}
	}

	// Fallback: look for a category word in the raw text.
	lower := strings.ToLower(raw)
	for _, cat := range validCategories {
		if strings.Contains(lower, cat) {
			return ClassifyResult{Category: cat, Confidence: 0.5, Reasoning: "extracted from raw text"}
		}
	}

	return ClassifyResult{Reasoning: "could not parse classification"}
}

func isValidCategory(cat string, valid []string) bool {
	for _, v := range valid {
		if v == cat {
			return true
		}
	}
	return false
}
