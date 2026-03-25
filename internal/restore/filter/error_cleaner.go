// Package filter implements SessionFilter strategies for the restore workflow.
package filter

import (
	"fmt"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ErrorCleaner replaces tool call errors with a compact summary message.
// Instead of restoring raw error output (stack traces, verbose error messages),
// it replaces each error tool call's output with a short summary like:
//
//	[Error in <tool_name>: <first line of error>]
//
// This reduces noise when restoring sessions and keeps the conversation context
// clean for the AI to continue from.
type ErrorCleaner struct {
	// MaxErrorLen caps the error summary length. 0 = use default (120).
	MaxErrorLen int
}

// NewErrorCleaner creates an ErrorCleaner with default settings.
func NewErrorCleaner() *ErrorCleaner {
	return &ErrorCleaner{MaxErrorLen: 120}
}

// Name returns the filter identifier.
func (f *ErrorCleaner) Name() string { return "error-cleaner" }

// Apply replaces error tool call outputs with compact summaries.
func (f *ErrorCleaner) Apply(sess *session.Session) (*session.Session, *session.FilterResult, error) {
	cp := session.CopySession(sess)
	maxLen := f.MaxErrorLen
	if maxLen <= 0 {
		maxLen = 120
	}

	modified := 0
	for i := range cp.Messages {
		for j := range cp.Messages[i].ToolCalls {
			tc := &cp.Messages[i].ToolCalls[j]
			if tc.State != session.ToolStateError {
				continue
			}
			if tc.Output == "" {
				continue
			}

			// Build a compact summary from the first line of the error.
			firstLine := firstLineOf(tc.Output)
			if len(firstLine) > maxLen {
				firstLine = firstLine[:maxLen-3] + "..."
			}
			tc.Output = fmt.Sprintf("[Error in %s: %s]", tc.Name, firstLine)
			modified++
		}
	}

	if modified == 0 {
		return cp, &session.FilterResult{
			FilterName: f.Name(),
			Applied:    false,
			Summary:    "no error tool calls found",
		}, nil
	}

	return cp, &session.FilterResult{
		FilterName:       f.Name(),
		Applied:          true,
		Summary:          fmt.Sprintf("cleaned %d error tool call(s)", modified),
		MessagesModified: modified,
	}, nil
}

// firstLineOf extracts the first non-empty line from text.
func firstLineOf(s string) string {
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}
	return s
}
