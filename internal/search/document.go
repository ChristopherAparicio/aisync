package search

import (
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// MaxContentLength is the default max characters for indexed message content.
const MaxContentLength = 50000

// DocumentFromSession converts a session into a searchable Document.
func DocumentFromSession(sess *session.Session, maxContentLen int) Document {
	if maxContentLen <= 0 {
		maxContentLen = MaxContentLength
	}

	doc := Document{
		ID:              string(sess.ID),
		Summary:         sess.Summary,
		ProjectPath:     sess.ProjectPath,
		RemoteURL:       sess.RemoteURL,
		Branch:          sess.Branch,
		Agent:           sess.Agent,
		Provider:        string(sess.Provider),
		SessionType:     sess.SessionType,
		ProjectCategory: sess.ProjectCategory,
		CreatedAt:       sess.CreatedAt,
		TotalTokens:     sess.TokenUsage.TotalTokens,
		MessageCount:    len(sess.Messages),
		ErrorCount:      len(sess.Errors),
	}

	// Index only user messages and compaction summaries.
	// Tool names (not content) are still collected as a lightweight index signal.
	// See ai5-search-v2 plan: dropping tool inputs/outputs eliminates BM25 term
	// inflation from echo content while preserving the 6.6x signal density gain.
	var contentParts []string
	var toolNames []string
	toolSeen := make(map[string]bool)
	totalLen := 0

	for _, msg := range sess.Messages {
		if totalLen >= maxContentLen {
			break
		}

		// Include: non-empty user messages
		shouldIndex := msg.Role == session.RoleUser && strings.TrimSpace(msg.Content) != ""
		// Include: compaction summaries (regardless of role)
		shouldIndex = shouldIndex || msg.IsCompactionSummary

		if shouldIndex && msg.Content != "" {
			remaining := maxContentLen - totalLen
			text := msg.Content
			if len(text) > remaining {
				text = text[:remaining]
			}
			contentParts = append(contentParts, text)
			totalLen += len(text)
		}

		// Collect unique tool names (lightweight signal — no content).
		for _, tc := range msg.ToolCalls {
			if !toolSeen[tc.Name] {
				toolSeen[tc.Name] = true
				toolNames = append(toolNames, tc.Name)
			}
			// Tool inputs/outputs intentionally excluded from Content.
			// Discovery of file paths via tool calls is covered by aisync blame
			// and the file_changes table. See ai5-search-v2 plan guardrails.
		}
	}

	doc.Content = strings.Join(contentParts, "\n")
	doc.ToolNames = strings.Join(toolNames, " ")
	return doc
}
