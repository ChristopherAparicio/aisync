// Package session — validate.go implements conversation integrity checks.
//
// The Anthropic Messages API enforces strict structural rules on conversations:
//   - Every assistant tool_use block MUST be followed by a user tool_result block.
//   - Messages must alternate user ↔ assistant (with some exceptions).
//   - tool_result IDs must match a preceding tool_use ID.
//
// When these rules are violated (e.g. due to crashes, timeouts, or bugs in the
// coding assistant), the session becomes "corrupted" — the API refuses further
// messages with errors like:
//
//	messages.57: `tool_use` ids were found without `tool_result` blocks immediately after
//
// Validate() detects these structural issues and returns actionable diagnostics
// including the exact message index where a rewind would fix the problem.
package session

import "fmt"

// ValidationSeverity indicates how severe a validation issue is.
type ValidationSeverity string

const (
	// SeverityError means the session is broken and cannot be continued.
	SeverityError ValidationSeverity = "error"
	// SeverityWarning means there's a potential issue but the session might still work.
	SeverityWarning ValidationSeverity = "warning"
	// SeverityInfo is informational (e.g. unusual patterns).
	SeverityInfo ValidationSeverity = "info"
)

// ValidationIssueType categorizes the kind of structural problem.
type ValidationIssueType string

const (
	// IssueOrphanToolUse: a tool_use block has no matching tool_result.
	// This is the most common cause of "broken" sessions.
	IssueOrphanToolUse ValidationIssueType = "orphan_tool_use"

	// IssueOrphanToolResult: a tool_result references a tool_use ID that doesn't exist.
	IssueOrphanToolResult ValidationIssueType = "orphan_tool_result"

	// IssuePendingToolCall: a ToolCall is still in "pending" state (never completed).
	IssuePendingToolCall ValidationIssueType = "pending_tool_call"

	// IssueConsecutiveRoles: two consecutive messages have the same role
	// (e.g. two assistant messages in a row without a user message between them).
	IssueConsecutiveRoles ValidationIssueType = "consecutive_roles"

	// IssueEmptyMessage: a message has no content, no tool calls, and no thinking.
	IssueEmptyMessage ValidationIssueType = "empty_message"

	// IssueMissingTimestamp: a message has a zero timestamp.
	IssueMissingTimestamp ValidationIssueType = "missing_timestamp"
)

// ValidationIssue describes a single structural problem in a session.
type ValidationIssue struct {
	// Type categorizes the problem.
	Type ValidationIssueType `json:"type"`

	// Severity indicates how critical this issue is.
	Severity ValidationSeverity `json:"severity"`

	// MessageIndex is the 0-based index of the problematic message.
	// For IssueOrphanToolUse, this is the assistant message containing the tool_use.
	MessageIndex int `json:"message_index"`

	// MessageNumber is the 1-based human-readable message number (MessageIndex + 1).
	MessageNumber int `json:"message_number"`

	// ToolCallID is the tool_use/tool_result ID involved (empty if not applicable).
	ToolCallID string `json:"tool_call_id,omitempty"`

	// ToolName is the tool name involved (empty if not applicable).
	ToolName string `json:"tool_name,omitempty"`

	// Description is a human-readable explanation of the issue.
	Description string `json:"description"`

	// RewindTo is the suggested 1-based message index to rewind to in order to fix
	// this issue. Zero means no rewind suggestion (e.g. for informational issues).
	RewindTo int `json:"rewind_to,omitempty"`
}

// ValidationResult is the outcome of validating a session's message structure.
type ValidationResult struct {
	// SessionID is the session that was validated.
	SessionID ID `json:"session_id"`

	// Valid is true when no errors were found (warnings/info don't count).
	Valid bool `json:"valid"`

	// Issues contains all detected problems, ordered by message index.
	Issues []ValidationIssue `json:"issues,omitempty"`

	// ErrorCount is the number of error-severity issues.
	ErrorCount int `json:"error_count"`

	// WarningCount is the number of warning-severity issues.
	WarningCount int `json:"warning_count"`

	// MessageCount is the total number of messages in the session.
	MessageCount int `json:"message_count"`

	// SuggestedRewindTo is the earliest message index (1-based) that would fix
	// ALL error-severity issues. Zero means no rewind needed.
	SuggestedRewindTo int `json:"suggested_rewind_to,omitempty"`
}

// Validate checks a session's message structure for integrity issues.
//
// It detects:
//   - Orphan tool_use blocks (assistant calls a tool but no tool_result follows)
//   - Orphan tool_result blocks (user returns a result for an unknown tool_use)
//   - Pending tool calls (ToolCall.State == "pending" — never got a response)
//   - Consecutive same-role messages (violates alternation rule)
//   - Empty messages (no content, no tool calls)
//   - Missing timestamps
//
// The result includes a SuggestedRewindTo value — the earliest safe point
// before any error-severity issues. Use this with Rewind() to auto-fix.
func Validate(sess *Session) *ValidationResult {
	result := &ValidationResult{
		SessionID:    sess.ID,
		Valid:        true,
		MessageCount: len(sess.Messages),
	}

	if len(sess.Messages) == 0 {
		return result
	}

	// Track tool_use IDs that need a tool_result response.
	// Key: tool_use ID, Value: (messageIndex, toolName)
	type pendingTool struct {
		messageIndex int
		toolName     string
	}
	pendingToolUses := make(map[string]pendingTool)

	// All tool_use IDs seen (for orphan tool_result detection).
	allToolUseIDs := make(map[string]bool)

	var prevRole MessageRole

	for i, msg := range sess.Messages {
		msgNum := i + 1 // 1-based

		// ── Check: empty message ──
		if msg.Content == "" && msg.Thinking == "" && len(msg.ToolCalls) == 0 && len(msg.ContentBlocks) == 0 {
			result.Issues = append(result.Issues, ValidationIssue{
				Type:          IssueEmptyMessage,
				Severity:      SeverityWarning,
				MessageIndex:  i,
				MessageNumber: msgNum,
				Description:   fmt.Sprintf("Message %d (%s) is empty — no content, tool calls, or thinking", msgNum, msg.Role),
			})
		}

		// ── Check: missing timestamp ──
		if msg.Timestamp.IsZero() {
			result.Issues = append(result.Issues, ValidationIssue{
				Type:          IssueMissingTimestamp,
				Severity:      SeverityInfo,
				MessageIndex:  i,
				MessageNumber: msgNum,
				Description:   fmt.Sprintf("Message %d has no timestamp", msgNum),
			})
		}

		// ── Check: consecutive same-role messages ──
		if i > 0 && msg.Role == prevRole && msg.Role != "" {
			result.Issues = append(result.Issues, ValidationIssue{
				Type:          IssueConsecutiveRoles,
				Severity:      SeverityWarning,
				MessageIndex:  i,
				MessageNumber: msgNum,
				Description:   fmt.Sprintf("Message %d (%s) follows another %s message — breaks alternation rule", msgNum, msg.Role, prevRole),
				RewindTo:      i, // rewind to just before this message
			})
		}
		prevRole = msg.Role

		// ── Assistant message: register tool_use IDs ──
		if msg.Role == RoleAssistant {
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					allToolUseIDs[tc.ID] = true

					// If the tool call already has a result (completed or error),
					// it was resolved during parsing — don't track as pending.
					if tc.State == ToolStateCompleted || tc.State == ToolStateError {
						continue
					}

					pendingToolUses[tc.ID] = pendingTool{
						messageIndex: i,
						toolName:     tc.Name,
					}
				}

				// Check for permanently pending tool calls
				if tc.State == ToolStatePending {
					result.Issues = append(result.Issues, ValidationIssue{
						Type:          IssuePendingToolCall,
						Severity:      SeverityWarning,
						MessageIndex:  i,
						MessageNumber: msgNum,
						ToolCallID:    tc.ID,
						ToolName:      tc.Name,
						Description:   fmt.Sprintf("Message %d: tool call %q (%s) is still pending — never received a result", msgNum, tc.Name, tc.ID),
						RewindTo:      i, // rewind to before this message
					})
				}
			}

			// Also check ContentBlocks for tool_use references
			for _, cb := range msg.ContentBlocks {
				if cb.Type == ContentBlockToolUse && cb.ToolUse != nil {
					if cb.ToolUse.ID != "" {
						if _, exists := pendingToolUses[cb.ToolUse.ID]; !exists {
							// Only add if not already tracked via ToolCalls
							pendingToolUses[cb.ToolUse.ID] = pendingTool{
								messageIndex: i,
								toolName:     cb.ToolUse.Name,
							}
							allToolUseIDs[cb.ToolUse.ID] = true
						}
					}
				}
			}
		}

		// ── User message: resolve pending tool_uses ──
		if msg.Role == RoleUser {
			// Check ToolCalls on this user message (some providers store results here)
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					delete(pendingToolUses, tc.ID)
				}
			}

			// Check ContentBlocks for tool_result references
			for _, cb := range msg.ContentBlocks {
				if cb.ToolUse != nil && cb.ToolUse.ID != "" {
					if !allToolUseIDs[cb.ToolUse.ID] {
						result.Issues = append(result.Issues, ValidationIssue{
							Type:          IssueOrphanToolResult,
							Severity:      SeverityWarning,
							MessageIndex:  i,
							MessageNumber: msgNum,
							ToolCallID:    cb.ToolUse.ID,
							Description:   fmt.Sprintf("Message %d: tool_result references unknown tool_use ID %s", msgNum, cb.ToolUse.ID),
						})
					}
					delete(pendingToolUses, cb.ToolUse.ID)
				}
			}
		}

		// ── After a user message: check if previous assistant tool_uses are resolved ──
		// The Anthropic API requires tool_results IMMEDIATELY after the tool_use message.
		// If we see another assistant message and there are still pending tool_uses from
		// a PREVIOUS assistant message, those are orphans.
		if msg.Role == RoleAssistant && i > 0 {
			// Find tool_uses from previous assistant messages (NOT the current one)
			for toolID, pt := range pendingToolUses {
				if pt.messageIndex < i-1 { // from a non-adjacent previous assistant message
					result.Issues = append(result.Issues, ValidationIssue{
						Type:          IssueOrphanToolUse,
						Severity:      SeverityError,
						MessageIndex:  pt.messageIndex,
						MessageNumber: pt.messageIndex + 1,
						ToolCallID:    toolID,
						ToolName:      pt.toolName,
						Description: fmt.Sprintf(
							"Message %d: tool_use %q (%s) has no matching tool_result — detected at message %d",
							pt.messageIndex+1, pt.toolName, toolID, msgNum,
						),
						RewindTo: pt.messageIndex, // rewind to before the broken tool_use
					})
					delete(pendingToolUses, toolID)
				}
			}
		}
	}

	// ── Final check: any tool_uses still pending at end of conversation? ──
	// At this point, pendingToolUses only contains tools that were NOT completed/errored
	// during parsing (we skip completed/error tools when registering).
	for toolID, pt := range pendingToolUses {
		result.Issues = append(result.Issues, ValidationIssue{
			Type:          IssueOrphanToolUse,
			Severity:      SeverityError,
			MessageIndex:  pt.messageIndex,
			MessageNumber: pt.messageIndex + 1,
			ToolCallID:    toolID,
			ToolName:      pt.toolName,
			Description: fmt.Sprintf(
				"Message %d: tool_use %q (%s) never received a tool_result (end of conversation)",
				pt.messageIndex+1, pt.toolName, toolID,
			),
			RewindTo: pt.messageIndex, // rewind to before this message
		})
	}

	// ── Compute summary stats ──
	earliestRewind := 0
	for _, issue := range result.Issues {
		switch issue.Severity {
		case SeverityError:
			result.ErrorCount++
			result.Valid = false
			if issue.RewindTo > 0 && (earliestRewind == 0 || issue.RewindTo < earliestRewind) {
				earliestRewind = issue.RewindTo
			}
		case SeverityWarning:
			result.WarningCount++
		}
	}
	result.SuggestedRewindTo = earliestRewind

	return result
}

// FirstErrorIndex returns the 0-based message index of the first error-severity issue,
// or -1 if there are no errors. This is useful for auto-fix: rewind to this index.
func (r *ValidationResult) FirstErrorIndex() int {
	for _, issue := range r.Issues {
		if issue.Severity == SeverityError {
			return issue.MessageIndex
		}
	}
	return -1
}

// IssuesByType returns all issues of the given type.
func (r *ValidationResult) IssuesByType(t ValidationIssueType) []ValidationIssue {
	var filtered []ValidationIssue
	for _, issue := range r.Issues {
		if issue.Type == t {
			filtered = append(filtered, issue)
		}
	}
	return filtered
}
