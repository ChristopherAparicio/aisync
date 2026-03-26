package filter

import (
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestOrphanToolFixer_Name(t *testing.T) {
	f := NewOrphanToolFixer()
	if f.Name() != "orphan-tool-fixer" {
		t.Errorf("Name() = %q, want %q", f.Name(), "orphan-tool-fixer")
	}
}

func TestOrphanToolFixer_noOrphans(t *testing.T) {
	f := NewOrphanToolFixer()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "hello"},
			{Role: session.RoleAssistant, Content: "hi", ToolCalls: []session.ToolCall{
				{ID: "tc-1", Name: "read", State: session.ToolStateCompleted, Output: "ok"},
			}},
			{Role: session.RoleUser, Content: "thanks"},
		},
	}

	_, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fr.Applied {
		t.Error("filter should not have applied (no orphans)")
	}
}

func TestOrphanToolFixer_fixesOrphanAtEnd(t *testing.T) {
	f := NewOrphanToolFixer()
	// Orphan at end: assistant calls a tool, conversation ends without tool_result.
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "do something"},
			{Role: session.RoleAssistant, Content: "", ToolCalls: []session.ToolCall{
				{ID: "tc-orphan", Name: "write", State: session.ToolStatePending},
			}},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fr.Applied {
		t.Fatal("filter should have applied (orphan at end)")
	}
	if fr.MessagesModified != 1 {
		t.Errorf("MessagesModified = %d, want 1", fr.MessagesModified)
	}

	// The tool call should now be errored.
	tc := result.Messages[1].ToolCalls[0]
	if tc.State != session.ToolStateError {
		t.Errorf("state = %q, want %q", tc.State, session.ToolStateError)
	}
	if !strings.Contains(tc.Output, "Session interrupted") {
		t.Errorf("output = %q, want to contain 'Session interrupted'", tc.Output)
	}
	if !strings.Contains(tc.Output, "write") {
		t.Errorf("output should mention tool name 'write'")
	}
}

func TestOrphanToolFixer_fixesOrphanMidConversation(t *testing.T) {
	f := NewOrphanToolFixer()
	// Orphan mid-conversation: assistant calls tool, then another assistant message
	// comes without the tool_result in between.
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "start"},
			{Role: session.RoleAssistant, Content: "", ToolCalls: []session.ToolCall{
				{ID: "tc-1", Name: "task", State: session.ToolStatePending},
			}},
			// Missing user message with tool_result!
			{Role: session.RoleUser, Content: "continue"},
			{Role: session.RoleAssistant, Content: "ok continuing"},
			{Role: session.RoleUser, Content: "done"},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fr.Applied {
		t.Fatal("filter should have applied")
	}
	if fr.MessagesModified != 1 {
		t.Errorf("MessagesModified = %d, want 1", fr.MessagesModified)
	}

	// The orphan should be fixed.
	tc := result.Messages[1].ToolCalls[0]
	if tc.State != session.ToolStateError {
		t.Errorf("state = %q, want error", tc.State)
	}

	// Validate the fixed session.
	vr := session.Validate(result)
	orphanIssues := vr.IssuesByType(session.IssueOrphanToolUse)
	if len(orphanIssues) != 0 {
		t.Errorf("fixed session still has %d orphan_tool_use issues", len(orphanIssues))
	}
}

func TestOrphanToolFixer_doesNotTouchCompleted(t *testing.T) {
	f := NewOrphanToolFixer()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "hello"},
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{ID: "tc-1", Name: "read", State: session.ToolStateCompleted, Output: "contents"},
			}},
			{Role: session.RoleUser, Content: "next"},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fr.Applied {
		t.Error("should not apply — no orphans (tool already completed)")
	}
	// Output should be preserved.
	if result.Messages[1].ToolCalls[0].Output != "contents" {
		t.Error("should not modify completed tool call output")
	}
}

func TestOrphanToolFixer_multipleOrphans(t *testing.T) {
	f := NewOrphanToolFixer()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "go"},
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{ID: "tc-1", Name: "task", State: session.ToolStatePending},
				{ID: "tc-2", Name: "write", State: session.ToolStatePending},
			}},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fr.Applied {
		t.Fatal("should have applied")
	}
	if fr.MessagesModified != 2 {
		t.Errorf("MessagesModified = %d, want 2", fr.MessagesModified)
	}

	for _, tc := range result.Messages[1].ToolCalls {
		if tc.State != session.ToolStateError {
			t.Errorf("tool %q state = %q, want error", tc.Name, tc.State)
		}
	}
}

func TestOrphanToolFixer_emptySession(t *testing.T) {
	f := NewOrphanToolFixer()
	sess := &session.Session{ID: "empty"}

	_, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fr.Applied {
		t.Error("should not apply on empty session")
	}
}
