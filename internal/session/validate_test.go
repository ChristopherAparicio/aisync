package session

import (
	"fmt"
	"testing"
	"time"
)

func TestValidate_emptySession(t *testing.T) {
	sess := &Session{ID: "empty-sess"}
	result := Validate(sess)

	if !result.Valid {
		t.Error("empty session should be valid")
	}
	if result.MessageCount != 0 {
		t.Errorf("expected 0 messages, got %d", result.MessageCount)
	}
	if len(result.Issues) != 0 {
		t.Errorf("expected 0 issues, got %d", len(result.Issues))
	}
}

func TestValidate_healthySession(t *testing.T) {
	sess := &Session{
		ID: "healthy-sess",
		Messages: []Message{
			{
				ID:        "u1",
				Role:      RoleUser,
				Content:   "Hello",
				Timestamp: time.Now(),
			},
			{
				ID:        "a1",
				Role:      RoleAssistant,
				Content:   "Hi! Let me write a file.",
				Timestamp: time.Now(),
				ToolCalls: []ToolCall{
					{ID: "tool1", Name: "Write", State: ToolStateCompleted, Output: "File written"},
				},
			},
			{
				ID:        "u2",
				Role:      RoleUser,
				Content:   "Thanks!",
				Timestamp: time.Now(),
			},
			{
				ID:        "a2",
				Role:      RoleAssistant,
				Content:   "You're welcome!",
				Timestamp: time.Now(),
			},
		},
	}

	result := Validate(sess)
	if !result.Valid {
		t.Errorf("healthy session should be valid, got %d errors", result.ErrorCount)
		for _, issue := range result.Issues {
			t.Logf("  issue: %s — %s", issue.Type, issue.Description)
		}
	}
	if result.ErrorCount != 0 {
		t.Errorf("expected 0 errors, got %d", result.ErrorCount)
	}
	if result.SuggestedRewindTo != 0 {
		t.Errorf("expected no rewind suggestion, got %d", result.SuggestedRewindTo)
	}
}

func TestValidate_orphanToolUse_endOfConversation(t *testing.T) {
	// Simulates: assistant calls a tool but session ends before tool_result
	sess := &Session{
		ID: "orphan-end",
		Messages: []Message{
			{
				ID:        "u1",
				Role:      RoleUser,
				Content:   "Write a file",
				Timestamp: time.Now(),
			},
			{
				ID:        "a1",
				Role:      RoleAssistant,
				Content:   "I'll write it now",
				Timestamp: time.Now(),
				ToolCalls: []ToolCall{
					{ID: "toolu_01Y8R", Name: "Write", State: ToolStatePending},
				},
			},
			// NO user message with tool_result — session ends here
		},
	}

	result := Validate(sess)

	if result.Valid {
		t.Error("session with orphan tool_use should NOT be valid")
	}
	if result.ErrorCount < 1 {
		t.Errorf("expected at least 1 error, got %d", result.ErrorCount)
	}

	// Check the orphan tool_use issue
	orphans := result.IssuesByType(IssueOrphanToolUse)
	if len(orphans) == 0 {
		t.Fatal("expected orphan_tool_use issue")
	}
	if orphans[0].ToolCallID != "toolu_01Y8R" {
		t.Errorf("expected tool ID toolu_01Y8R, got %s", orphans[0].ToolCallID)
	}
	if orphans[0].ToolName != "Write" {
		t.Errorf("expected tool name Write, got %s", orphans[0].ToolName)
	}
	if orphans[0].MessageIndex != 1 {
		t.Errorf("expected message index 1, got %d", orphans[0].MessageIndex)
	}
	if orphans[0].RewindTo != 1 {
		t.Errorf("expected rewind to 1, got %d", orphans[0].RewindTo)
	}
	if result.SuggestedRewindTo != 1 {
		t.Errorf("expected suggested rewind to 1, got %d", result.SuggestedRewindTo)
	}

	// Also check pending tool call warning
	pending := result.IssuesByType(IssuePendingToolCall)
	if len(pending) == 0 {
		t.Error("expected pending_tool_call warning")
	}
}

func TestValidate_orphanToolUse_midConversation(t *testing.T) {
	// Simulates the real error: assistant tool_use at msg 3,
	// but instead of tool_result, another assistant message appears at msg 5
	sess := &Session{
		ID: "orphan-mid",
		Messages: []Message{
			{ID: "u1", Role: RoleUser, Content: "Start", Timestamp: time.Now()},
			{
				ID: "a1", Role: RoleAssistant, Content: "Working...",
				Timestamp: time.Now(),
				ToolCalls: []ToolCall{
					{ID: "toolu_ABC", Name: "Bash", State: ToolStatePending},
				},
			},
			// Missing user tool_result here!
			{ID: "u2", Role: RoleUser, Content: "Continue", Timestamp: time.Now()},
			{ID: "a2", Role: RoleAssistant, Content: "OK", Timestamp: time.Now()},
		},
	}

	result := Validate(sess)

	if result.Valid {
		t.Error("should not be valid — orphan tool_use in middle of conversation")
	}

	orphans := result.IssuesByType(IssueOrphanToolUse)
	if len(orphans) == 0 {
		t.Fatal("expected orphan_tool_use issue")
	}
	if orphans[0].ToolCallID != "toolu_ABC" {
		t.Errorf("expected tool ID toolu_ABC, got %s", orphans[0].ToolCallID)
	}
}

func TestValidate_consecutiveRoles(t *testing.T) {
	sess := &Session{
		ID: "consec-roles",
		Messages: []Message{
			{ID: "u1", Role: RoleUser, Content: "Hello", Timestamp: time.Now()},
			{ID: "a1", Role: RoleAssistant, Content: "Hi", Timestamp: time.Now()},
			{ID: "a2", Role: RoleAssistant, Content: "Also...", Timestamp: time.Now()}, // consecutive assistant
		},
	}

	result := Validate(sess)

	consec := result.IssuesByType(IssueConsecutiveRoles)
	if len(consec) == 0 {
		t.Fatal("expected consecutive_roles issue")
	}
	if consec[0].MessageIndex != 2 {
		t.Errorf("expected message index 2, got %d", consec[0].MessageIndex)
	}
	if consec[0].Severity != SeverityWarning {
		t.Errorf("expected warning severity, got %s", consec[0].Severity)
	}
}

func TestValidate_emptyMessage(t *testing.T) {
	sess := &Session{
		ID: "empty-msg",
		Messages: []Message{
			{ID: "u1", Role: RoleUser, Content: "Hello", Timestamp: time.Now()},
			{ID: "a1", Role: RoleAssistant, Timestamp: time.Now()}, // empty
		},
	}

	result := Validate(sess)

	empty := result.IssuesByType(IssueEmptyMessage)
	if len(empty) == 0 {
		t.Fatal("expected empty_message issue")
	}
	if empty[0].MessageIndex != 1 {
		t.Errorf("expected message index 1, got %d", empty[0].MessageIndex)
	}
}

func TestValidate_missingTimestamp(t *testing.T) {
	sess := &Session{
		ID: "no-ts",
		Messages: []Message{
			{ID: "u1", Role: RoleUser, Content: "Hello"}, // zero timestamp
		},
	}

	result := Validate(sess)

	ts := result.IssuesByType(IssueMissingTimestamp)
	if len(ts) == 0 {
		t.Fatal("expected missing_timestamp issue")
	}
	if ts[0].Severity != SeverityInfo {
		t.Errorf("expected info severity, got %s", ts[0].Severity)
	}
}

func TestValidate_toolUseResolvedByToolCalls(t *testing.T) {
	// Tool call was completed (State=completed) — should NOT be orphan
	sess := &Session{
		ID: "resolved-tc",
		Messages: []Message{
			{ID: "u1", Role: RoleUser, Content: "Do it", Timestamp: time.Now()},
			{
				ID: "a1", Role: RoleAssistant, Content: "Writing...",
				Timestamp: time.Now(),
				ToolCalls: []ToolCall{
					{ID: "toolu_OK", Name: "Write", State: ToolStateCompleted, Output: "Done"},
				},
			},
			{ID: "u2", Role: RoleUser, Content: "Great", Timestamp: time.Now()},
		},
	}

	result := Validate(sess)

	orphans := result.IssuesByType(IssueOrphanToolUse)
	if len(orphans) != 0 {
		t.Errorf("completed tool calls should not be orphans, got %d issues", len(orphans))
		for _, o := range orphans {
			t.Logf("  orphan: %s", o.Description)
		}
	}
}

func TestValidate_multipleOrphans_suggestsEarliestRewind(t *testing.T) {
	sess := &Session{
		ID: "multi-orphan",
		Messages: []Message{
			{ID: "u1", Role: RoleUser, Content: "Start", Timestamp: time.Now()},
			{
				ID: "a1", Role: RoleAssistant, Content: "First tool",
				Timestamp: time.Now(),
				ToolCalls: []ToolCall{
					{ID: "tool1", Name: "Read", State: ToolStatePending},
				},
			},
			{ID: "u2", Role: RoleUser, Content: "OK", Timestamp: time.Now()}, // no tool_result
			{
				ID: "a2", Role: RoleAssistant, Content: "Second tool",
				Timestamp: time.Now(),
				ToolCalls: []ToolCall{
					{ID: "tool2", Name: "Write", State: ToolStatePending},
				},
			},
			// No tool_result for tool2 either
		},
	}

	result := Validate(sess)

	if result.Valid {
		t.Error("should not be valid with multiple orphans")
	}

	// SuggestedRewindTo should be the earliest broken point
	if result.SuggestedRewindTo != 1 {
		t.Errorf("expected suggested rewind to 1 (earliest orphan), got %d", result.SuggestedRewindTo)
	}
}

func TestValidate_contentBlockToolUse(t *testing.T) {
	// Tool use tracked via ContentBlocks instead of ToolCalls
	sess := &Session{
		ID: "cb-tool-use",
		Messages: []Message{
			{ID: "u1", Role: RoleUser, Content: "Do something", Timestamp: time.Now()},
			{
				ID: "a1", Role: RoleAssistant, Content: "Running...",
				Timestamp: time.Now(),
				ContentBlocks: []ContentBlock{
					{Type: ContentBlockText, Text: "I'll run a command"},
					{Type: ContentBlockToolUse, ToolUse: &ToolCallRef{ID: "toolu_CB1", Name: "Bash"}},
				},
				// No ToolCalls entry — only in ContentBlocks
			},
			// Session ends without tool_result
		},
	}

	result := Validate(sess)

	if result.Valid {
		t.Error("should detect orphan from ContentBlocks")
	}

	orphans := result.IssuesByType(IssueOrphanToolUse)
	if len(orphans) == 0 {
		t.Fatal("expected orphan from ContentBlock tool_use")
	}
	if orphans[0].ToolCallID != "toolu_CB1" {
		t.Errorf("expected tool ID toolu_CB1, got %s", orphans[0].ToolCallID)
	}
}

func TestValidate_FirstErrorIndex(t *testing.T) {
	result := &ValidationResult{
		Issues: []ValidationIssue{
			{Severity: SeverityWarning, MessageIndex: 2},
			{Severity: SeverityError, MessageIndex: 5},
			{Severity: SeverityError, MessageIndex: 8},
		},
	}

	idx := result.FirstErrorIndex()
	if idx != 5 {
		t.Errorf("expected first error at index 5, got %d", idx)
	}
}

func TestValidate_FirstErrorIndex_noErrors(t *testing.T) {
	result := &ValidationResult{
		Issues: []ValidationIssue{
			{Severity: SeverityWarning, MessageIndex: 2},
		},
	}

	idx := result.FirstErrorIndex()
	if idx != -1 {
		t.Errorf("expected -1 when no errors, got %d", idx)
	}
}

func TestValidate_IssuesByType(t *testing.T) {
	result := &ValidationResult{
		Issues: []ValidationIssue{
			{Type: IssueOrphanToolUse},
			{Type: IssueEmptyMessage},
			{Type: IssueOrphanToolUse},
			{Type: IssueConsecutiveRoles},
		},
	}

	orphans := result.IssuesByType(IssueOrphanToolUse)
	if len(orphans) != 2 {
		t.Errorf("expected 2 orphan_tool_use issues, got %d", len(orphans))
	}
}

func TestValidate_realWorldScenario_message57(t *testing.T) {
	// Reproduce the real error the user saw:
	// "messages.57: tool_use ids were found without tool_result blocks immediately after"
	//
	// Build a conversation where message 57 (index 56) is an assistant with tool_use
	// and the next message is NOT a user tool_result.
	messages := make([]Message, 60)
	now := time.Now()

	for i := 0; i < 60; i++ {
		messages[i] = Message{
			ID:        fmt.Sprintf("msg-%d", i),
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		}
		if i%2 == 0 {
			messages[i].Role = RoleUser
			messages[i].Content = fmt.Sprintf("User message %d", i)
		} else {
			messages[i].Role = RoleAssistant
			messages[i].Content = fmt.Sprintf("Assistant message %d", i)
		}
	}

	// Message 56 (0-indexed) = message 57 (1-indexed) — assistant with tool_use
	// But it's index 56 which is even, so it would be user. Let's make it odd.
	// Actually, let's just put the tool_use at index 57 (0-based) = message 58 (1-based)
	// and make sure there's no tool_result at index 58.

	// Make index 55 an assistant with tool_use (message 56, 1-based)
	messages[55] = Message{
		ID:        "msg-broken",
		Role:      RoleAssistant,
		Content:   "I'll edit the workflow file",
		Timestamp: now.Add(55 * time.Minute),
		ToolCalls: []ToolCall{
			{ID: "toolu_01Y8RHuuTm1YtrAR49d5zZz6", Name: "Write", State: ToolStatePending},
		},
	}
	// Make index 56 a user message WITHOUT tool_result (just regular text)
	messages[56] = Message{
		ID:        "msg-56-user",
		Role:      RoleUser,
		Content:   "Continue",
		Timestamp: now.Add(56 * time.Minute),
	}
	// Make index 57 an assistant message — this triggers the orphan detection
	messages[57] = Message{
		ID:        "msg-57-assistant",
		Role:      RoleAssistant,
		Content:   "Sure, continuing...",
		Timestamp: now.Add(57 * time.Minute),
	}

	sess := &Session{
		ID:       "real-world-57",
		Messages: messages,
	}

	result := Validate(sess)

	if result.Valid {
		t.Error("session with orphan tool_use at message 56 should NOT be valid")
	}

	orphans := result.IssuesByType(IssueOrphanToolUse)
	if len(orphans) == 0 {
		t.Fatal("expected at least one orphan_tool_use issue")
	}

	// Find the specific orphan for our broken tool
	var found bool
	for _, o := range orphans {
		if o.ToolCallID == "toolu_01Y8RHuuTm1YtrAR49d5zZz6" {
			found = true
			if o.MessageNumber != 56 { // 1-based
				t.Errorf("expected message number 56, got %d", o.MessageNumber)
			}
			if o.ToolName != "Write" {
				t.Errorf("expected tool name Write, got %s", o.ToolName)
			}
			t.Logf("Found orphan: %s", o.Description)
			break
		}
	}
	if !found {
		t.Error("did not find orphan for toolu_01Y8RHuuTm1YtrAR49d5zZz6")
		for _, o := range orphans {
			t.Logf("  orphan: %s (tool=%s)", o.ToolCallID, o.ToolName)
		}
	}
}
