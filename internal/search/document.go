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

	// Concatenate message content (user + assistant) for full-text indexing.
	var contentParts []string
	var toolNames []string
	toolSeen := make(map[string]bool)
	totalLen := 0

	for _, msg := range sess.Messages {
		if totalLen >= maxContentLen {
			break
		}
		if msg.Content != "" {
			remaining := maxContentLen - totalLen
			text := msg.Content
			if len(text) > remaining {
				text = text[:remaining]
			}
			contentParts = append(contentParts, text)
			totalLen += len(text)
		}

		// Collect unique tool names.
		for _, tc := range msg.ToolCalls {
			if !toolSeen[tc.Name] {
				toolSeen[tc.Name] = true
				toolNames = append(toolNames, tc.Name)
			}
		}
	}

	doc.Content = strings.Join(contentParts, "\n")
	doc.ToolNames = strings.Join(toolNames, " ")
	return doc
}
