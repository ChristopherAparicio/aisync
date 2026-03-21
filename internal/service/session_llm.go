package service

import (
	"fmt"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── LLM helpers ──

// buildSessionTranscript builds a prompt-friendly transcript from session messages.
// It includes the first 3 and last 5 user messages plus file changes to fit context.
func buildSessionTranscript(sess *session.Session) string {
	if len(sess.Messages) == 0 {
		return ""
	}

	var b strings.Builder

	// Header
	b.WriteString(fmt.Sprintf("Session: %s\n", sess.ID))
	b.WriteString(fmt.Sprintf("Provider: %s\n", sess.Provider))
	if sess.Branch != "" {
		b.WriteString(fmt.Sprintf("Branch: %s\n", sess.Branch))
	}
	if sess.Summary != "" {
		b.WriteString(fmt.Sprintf("Summary: %s\n", sess.Summary))
	}
	b.WriteString("\n")

	// File changes
	if len(sess.FileChanges) > 0 {
		b.WriteString("Files changed:\n")
		for _, fc := range sess.FileChanges {
			b.WriteString(fmt.Sprintf("  - %s (%s)\n", fc.FilePath, fc.ChangeType))
		}
		b.WriteString("\n")
	}

	// Messages — truncation strategy:
	// If ≤ 20 messages, include all. Otherwise first 3 + last 5 user/assistant messages.
	messages := sess.Messages
	if len(messages) > 20 {
		var selected []session.Message
		selected = append(selected, messages[:3]...)
		// Last 5 messages
		start := len(messages) - 5
		if start < 3 {
			start = 3
		}
		selected = append(selected, messages[start:]...)
		messages = selected
		b.WriteString(fmt.Sprintf("(showing %d of %d messages)\n\n", len(messages), len(sess.Messages)))
	}

	b.WriteString("Conversation:\n")
	for _, msg := range messages {
		b.WriteString(fmt.Sprintf("[%s] %s\n", msg.Role, truncate(msg.Content, 2000)))
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				b.WriteString(fmt.Sprintf("  tool:%s → %s\n", tc.Name, truncate(tc.Output, 500)))
			}
		}
	}

	return b.String()
}

// truncate shortens a string to maxLen characters, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
