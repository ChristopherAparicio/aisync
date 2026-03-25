package filter

import (
	"fmt"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// EmptyMessage removes messages that have no meaningful content:
// no text content, no tool calls, no thinking, no content blocks, and no images.
// This cleans up sessions that have ghost/placeholder messages from crashes
// or incomplete tool execution.
type EmptyMessage struct{}

// NewEmptyMessage creates an EmptyMessage filter.
func NewEmptyMessage() *EmptyMessage {
	return &EmptyMessage{}
}

// Name returns the filter identifier.
func (f *EmptyMessage) Name() string { return "empty-message" }

// Apply removes empty messages from the session.
func (f *EmptyMessage) Apply(sess *session.Session) (*session.Session, *session.FilterResult, error) {
	cp := session.CopySession(sess)

	var kept []session.Message
	removed := 0

	for _, msg := range cp.Messages {
		if isEmpty(msg) {
			removed++
			continue
		}
		kept = append(kept, msg)
	}

	if removed == 0 {
		return cp, &session.FilterResult{
			FilterName: f.Name(),
			Applied:    false,
			Summary:    "no empty messages found",
		}, nil
	}

	cp.Messages = kept

	return cp, &session.FilterResult{
		FilterName:      f.Name(),
		Applied:         true,
		Summary:         fmt.Sprintf("removed %d empty message(s)", removed),
		MessagesRemoved: removed,
	}, nil
}

// isEmpty checks if a message has no meaningful content.
func isEmpty(msg session.Message) bool {
	// Has text content?
	if strings.TrimSpace(msg.Content) != "" {
		return false
	}

	// Has thinking?
	if strings.TrimSpace(msg.Thinking) != "" {
		return false
	}

	// Has tool calls?
	if len(msg.ToolCalls) > 0 {
		return false
	}

	// Has content blocks with content?
	for _, cb := range msg.ContentBlocks {
		if strings.TrimSpace(cb.Text) != "" {
			return false
		}
		if strings.TrimSpace(cb.Thinking) != "" {
			return false
		}
		if cb.Image != nil {
			return false
		}
		if cb.ToolUse != nil {
			return false
		}
	}

	// Has images?
	if len(msg.Images) > 0 {
		return false
	}

	return true
}
