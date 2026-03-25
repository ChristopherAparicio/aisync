package filter

import (
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestErrorCleaner_Name(t *testing.T) {
	f := NewErrorCleaner()
	if f.Name() != "error-cleaner" {
		t.Errorf("Name() = %q, want %q", f.Name(), "error-cleaner")
	}
}

func TestErrorCleaner_noErrors(t *testing.T) {
	f := NewErrorCleaner()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{
				Content: "hello",
				ToolCalls: []session.ToolCall{
					{ID: "tc-1", Name: "read", State: session.ToolStateCompleted, Output: "file contents"},
				},
			},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fr.Applied {
		t.Error("filter should not have applied (no errors)")
	}

	// Output should be unchanged.
	if result.Messages[0].ToolCalls[0].Output != "file contents" {
		t.Error("output should be unchanged")
	}
}

func TestErrorCleaner_cleansErrors(t *testing.T) {
	f := NewErrorCleaner()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{
				Content: "run tests",
				ToolCalls: []session.ToolCall{
					{
						ID:    "tc-1",
						Name:  "bash",
						State: session.ToolStateError,
						Output: `Error: command failed
exit code 1
FAIL github.com/example/pkg [build failed]
compilation error at line 42
more error details
stack trace follows...`,
					},
					{
						ID:     "tc-2",
						Name:   "read",
						State:  session.ToolStateCompleted,
						Output: "normal output",
					},
				},
			},
		},
	}

	result, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fr.Applied {
		t.Error("filter should have applied")
	}
	if fr.MessagesModified != 1 {
		t.Errorf("MessagesModified = %d, want 1", fr.MessagesModified)
	}

	// Error tool call should be cleaned.
	errorTC := result.Messages[0].ToolCalls[0]
	if !strings.HasPrefix(errorTC.Output, "[Error in bash:") {
		t.Errorf("error output should start with [Error in bash:, got %q", errorTC.Output)
	}
	if strings.Contains(errorTC.Output, "stack trace follows") {
		t.Error("error output should not contain full stack trace")
	}

	// Non-error tool call should be unchanged.
	if result.Messages[0].ToolCalls[1].Output != "normal output" {
		t.Error("non-error tool call should be unchanged")
	}
}

func TestErrorCleaner_emptyErrorOutput(t *testing.T) {
	f := NewErrorCleaner()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{
				ToolCalls: []session.ToolCall{
					{ID: "tc-1", Name: "bash", State: session.ToolStateError, Output: ""},
				},
			},
		},
	}

	_, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty error output should not count as modified.
	if fr.Applied {
		t.Error("filter should not apply for empty error outputs")
	}
}

func TestErrorCleaner_truncatesLongErrors(t *testing.T) {
	f := &ErrorCleaner{MaxErrorLen: 30}
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{
				ToolCalls: []session.ToolCall{
					{
						ID:     "tc-1",
						Name:   "bash",
						State:  session.ToolStateError,
						Output: "this is a very long error message that should be truncated to fit within the max length",
					},
				},
			},
		},
	}

	result, _, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.Messages[0].ToolCalls[0].Output
	// The error summary should contain the tool name and be truncated.
	if !strings.Contains(output, "bash") {
		t.Errorf("error summary should contain tool name, got %q", output)
	}
	if !strings.Contains(output, "...") {
		t.Errorf("long error should be truncated with ..., got %q", output)
	}
}

func TestErrorCleaner_doesNotModifyOriginal(t *testing.T) {
	f := NewErrorCleaner()
	original := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{
				ToolCalls: []session.ToolCall{
					{ID: "tc-1", Name: "bash", State: session.ToolStateError, Output: "big error\nwith details"},
				},
			},
		},
	}

	originalOutput := original.Messages[0].ToolCalls[0].Output
	_, _, err := f.Apply(original)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if original.Messages[0].ToolCalls[0].Output != originalOutput {
		t.Error("Apply should not modify the original session")
	}
}

func TestErrorCleaner_multipleErrors(t *testing.T) {
	f := NewErrorCleaner()
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{
				ToolCalls: []session.ToolCall{
					{ID: "tc-1", Name: "bash", State: session.ToolStateError, Output: "error 1"},
					{ID: "tc-2", Name: "write", State: session.ToolStateError, Output: "error 2"},
				},
			},
			{
				ToolCalls: []session.ToolCall{
					{ID: "tc-3", Name: "read", State: session.ToolStateError, Output: "error 3"},
				},
			},
		},
	}

	_, fr, err := f.Apply(sess)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fr.MessagesModified != 3 {
		t.Errorf("MessagesModified = %d, want 3", fr.MessagesModified)
	}
}
