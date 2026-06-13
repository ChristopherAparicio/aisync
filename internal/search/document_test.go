package search_test

import (
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/search"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestDocumentFromSession(t *testing.T) {
	t.Run("FilterCorrectness", func(t *testing.T) {
		sess := &session.Session{
			ID: "ses_test_filter",
			Messages: []session.Message{
				{Role: session.RoleUser, Content: "how to add auth"},
				{Role: session.RoleAssistant, Content: "sure I'll help"},
				{
					Role:    session.RoleAssistant,
					Content: "",
					ToolCalls: []session.ToolCall{
						{Name: "bash", Input: "ls -la", Output: "file.go"},
					},
				},
				{
					Role:                session.RoleAssistant,
					Content:             "## Goal auth\nSetup OAuth",
					IsCompactionSummary: true,
				},
			},
		}

		doc := search.DocumentFromSession(sess, 50000)

		if !strings.Contains(doc.Content, "how to add auth") {
			t.Errorf("Content must contain user message; got: %q", doc.Content)
		}
		if !strings.Contains(doc.Content, "## Goal auth") {
			t.Errorf("Content must contain compaction summary; got: %q", doc.Content)
		}
		if strings.Contains(doc.Content, "sure I'll help") {
			t.Errorf("Content must NOT contain assistant text; got: %q", doc.Content)
		}
		if strings.Contains(doc.Content, "ls -la") {
			t.Errorf("Content must NOT contain tool input; got: %q", doc.Content)
		}
		if strings.Contains(doc.Content, "file.go") {
			t.Errorf("Content must NOT contain tool output; got: %q", doc.Content)
		}
		if !strings.Contains(doc.ToolNames, "bash") {
			t.Errorf("ToolNames must contain tool name 'bash'; got: %q", doc.ToolNames)
		}
	})

	t.Run("NonOpenCode", func(t *testing.T) {
		// Sessions without IsCompactionSummary (e.g. Claude Code, non-OpenCode providers)
		// should index user messages only and not crash.
		sess := &session.Session{
			ID: "ses_test_nonopencode",
			Messages: []session.Message{
				{Role: session.RoleUser, Content: "configure nginx"},
				{Role: session.RoleAssistant, Content: "here is the config"},
			},
		}

		doc := search.DocumentFromSession(sess, 50000)

		if doc.Content != "configure nginx" {
			t.Errorf("Content must equal user message only; got: %q", doc.Content)
		}
	})

	t.Run("MaxContentLenRespected", func(t *testing.T) {
		sess := &session.Session{
			ID: "ses_test_maxlen",
			Messages: []session.Message{
				{Role: session.RoleUser, Content: "hello world this is a long message"},
			},
		}

		const limit = 10
		doc := search.DocumentFromSession(sess, limit)

		if len(doc.Content) > limit {
			t.Errorf("Content length %d exceeds maxContentLen %d; got: %q", len(doc.Content), limit, doc.Content)
		}
	})
}
