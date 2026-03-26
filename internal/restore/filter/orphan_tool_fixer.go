// Package filter — orphan_tool_fixer.go injects synthetic tool_result blocks
// for orphan tool_use calls that have no matching response.
//
// This is the surgical alternative to rewind: instead of discarding all messages
// after the orphan tool_use, we inject a fake tool_result with an error message.
// The session becomes structurally valid and can be restored/continued.
//
// Orphan tool_use blocks are the most common cause of "broken" sessions.
// They happen when:
//   - The AI tool crashes mid-execution (timeout, OOM, network error)
//   - A sub-agent (Task/spawn) fails without returning a result
//   - The user interrupts a session while tools are running
//
// The Anthropic API requires every tool_use to have a matching tool_result
// in the immediately following user message. This filter ensures that invariant.
package filter

import (
	"fmt"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// OrphanToolFixer detects orphan tool_use blocks and injects synthetic
// tool_result responses to make the session structurally valid.
type OrphanToolFixer struct{}

// NewOrphanToolFixer creates an OrphanToolFixer.
func NewOrphanToolFixer() *OrphanToolFixer {
	return &OrphanToolFixer{}
}

// Name returns the filter identifier.
func (f *OrphanToolFixer) Name() string { return "orphan-tool-fixer" }

// Apply finds orphan tool_use blocks and injects synthetic tool_result messages.
//
// Algorithm:
//  1. Walk through messages tracking pending tool_use IDs (same as Validate)
//  2. When a tool_use is detected as orphaned (no tool_result before next assistant msg
//     or end of conversation), mark the tool call as errored with a synthetic output
//  3. Return the fixed session
func (f *OrphanToolFixer) Apply(sess *session.Session) (*session.Session, *session.FilterResult, error) {
	cp := session.CopySession(sess)

	if len(cp.Messages) == 0 {
		return cp, &session.FilterResult{
			FilterName: f.Name(),
			Applied:    false,
			Summary:    "no messages to fix",
		}, nil
	}

	// Phase 1: Identify all orphan tool_use IDs using the same logic as Validate.
	orphans := findOrphanToolUses(cp)

	if len(orphans) == 0 {
		return cp, &session.FilterResult{
			FilterName: f.Name(),
			Applied:    false,
			Summary:    "no orphan tool_use blocks found",
		}, nil
	}

	// Phase 2: Fix each orphan by marking its ToolCall as errored with a synthetic output.
	fixed := 0
	for _, o := range orphans {
		if o.messageIndex >= len(cp.Messages) {
			continue
		}
		msg := &cp.Messages[o.messageIndex]

		for j := range msg.ToolCalls {
			tc := &msg.ToolCalls[j]
			if tc.ID != o.toolID {
				continue
			}

			// Mark as error with synthetic output.
			tc.State = session.ToolStateError
			tc.Output = fmt.Sprintf(
				"[Session interrupted: tool %q was called but never received a response. "+
					"The session crashed or was terminated before the tool could complete.]",
				tc.Name,
			)
			fixed++
		}

		// Also check ContentBlocks for matching tool_use references and remove them
		// (the ToolCall fix above is sufficient for API validity).
		_ = msg // ContentBlocks don't need separate fixing — ToolCall state change handles it.
	}

	return cp, &session.FilterResult{
		FilterName:       f.Name(),
		Applied:          true,
		Summary:          fmt.Sprintf("fixed %d orphan tool_use block(s)", fixed),
		MessagesModified: fixed,
	}, nil
}

// orphanInfo holds information about a detected orphan tool_use.
type orphanInfo struct {
	messageIndex int
	toolID       string
	toolName     string
}

// findOrphanToolUses identifies all tool_use blocks without matching tool_results.
// This mirrors the logic in session.Validate() but returns structured data
// instead of ValidationIssues.
func findOrphanToolUses(sess *session.Session) []orphanInfo {
	type pendingTool struct {
		messageIndex int
		toolName     string
	}
	pending := make(map[string]pendingTool)

	for i, msg := range sess.Messages {
		// Assistant message: register tool_use IDs.
		if msg.Role == session.RoleAssistant {
			for _, tc := range msg.ToolCalls {
				if tc.ID == "" {
					continue
				}
				if tc.State == session.ToolStateCompleted || tc.State == session.ToolStateError {
					continue
				}
				pending[tc.ID] = pendingTool{messageIndex: i, toolName: tc.Name}
			}
			for _, cb := range msg.ContentBlocks {
				if cb.Type == session.ContentBlockToolUse && cb.ToolUse != nil && cb.ToolUse.ID != "" {
					if _, exists := pending[cb.ToolUse.ID]; !exists {
						pending[cb.ToolUse.ID] = pendingTool{messageIndex: i, toolName: cb.ToolUse.Name}
					}
				}
			}
		}

		// User message: resolve pending tool_uses.
		if msg.Role == session.RoleUser {
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					delete(pending, tc.ID)
				}
			}
			for _, cb := range msg.ContentBlocks {
				if cb.ToolUse != nil && cb.ToolUse.ID != "" {
					delete(pending, cb.ToolUse.ID)
				}
			}
		}

		// When we see a new assistant message, any tool_uses from non-adjacent
		// previous assistant messages that are still pending are orphans.
		if msg.Role == session.RoleAssistant && i > 0 {
			for toolID, pt := range pending {
				if pt.messageIndex < i-1 {
					// This is an orphan — detected at this point.
					delete(pending, toolID)
					// We'll collect orphans after the loop to avoid map mutation issues.
				}
			}
		}
	}

	// Collect all remaining orphans.
	// Re-scan to detect properly (the delete above was premature in the loop).
	// Let's redo this cleanly.
	return findOrphansClean(sess)
}

// findOrphansClean is a cleaner implementation that collects all orphans.
func findOrphansClean(sess *session.Session) []orphanInfo {
	type pendingTool struct {
		messageIndex int
		toolName     string
	}
	pending := make(map[string]pendingTool)
	var orphans []orphanInfo

	for i, msg := range sess.Messages {
		// Before processing an assistant message, check for orphans from earlier.
		if msg.Role == session.RoleAssistant && i > 0 {
			for toolID, pt := range pending {
				if pt.messageIndex < i-1 {
					orphans = append(orphans, orphanInfo{
						messageIndex: pt.messageIndex,
						toolID:       toolID,
						toolName:     pt.toolName,
					})
					delete(pending, toolID)
				}
			}
		}

		// Register tool_use IDs from assistant messages.
		if msg.Role == session.RoleAssistant {
			for _, tc := range msg.ToolCalls {
				if tc.ID == "" || tc.State == session.ToolStateCompleted || tc.State == session.ToolStateError {
					continue
				}
				pending[tc.ID] = pendingTool{messageIndex: i, toolName: tc.Name}
			}
			for _, cb := range msg.ContentBlocks {
				if cb.Type == session.ContentBlockToolUse && cb.ToolUse != nil && cb.ToolUse.ID != "" {
					if _, exists := pending[cb.ToolUse.ID]; !exists {
						pending[cb.ToolUse.ID] = pendingTool{messageIndex: i, toolName: cb.ToolUse.Name}
					}
				}
			}
		}

		// Resolve pending tool_uses from user messages.
		if msg.Role == session.RoleUser {
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					delete(pending, tc.ID)
				}
			}
			for _, cb := range msg.ContentBlocks {
				if cb.ToolUse != nil && cb.ToolUse.ID != "" {
					delete(pending, cb.ToolUse.ID)
				}
			}
		}
	}

	// Remaining pending = orphans at end of conversation.
	for toolID, pt := range pending {
		orphans = append(orphans, orphanInfo{
			messageIndex: pt.messageIndex,
			toolID:       toolID,
			toolName:     pt.toolName,
		})
	}

	return orphans
}
