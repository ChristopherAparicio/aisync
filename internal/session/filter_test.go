package session

import (
	"fmt"
	"testing"
)

// mockFilter is a test filter that records calls and optionally modifies messages.
type mockFilter struct {
	name       string
	removeIdx  int  // if >= 0, remove message at this index
	shouldFail bool // if true, return an error
}

func (f *mockFilter) Name() string { return f.name }

func (f *mockFilter) Apply(sess *Session) (*Session, *FilterResult, error) {
	if f.shouldFail {
		return nil, nil, fmt.Errorf("mock filter error")
	}

	cp := CopySession(sess)

	if f.removeIdx >= 0 && f.removeIdx < len(cp.Messages) {
		cp.Messages = append(cp.Messages[:f.removeIdx], cp.Messages[f.removeIdx+1:]...)
		return cp, &FilterResult{
			FilterName:      f.name,
			Applied:         true,
			Summary:         "removed 1 message",
			MessagesRemoved: 1,
		}, nil
	}

	return cp, &FilterResult{
		FilterName: f.name,
		Applied:    false,
		Summary:    "no changes",
	}, nil
}

func TestApplyFilters_empty(t *testing.T) {
	sess := &Session{ID: "test", Messages: []Message{{Content: "hello"}}}
	result, results, err := ApplyFilters(sess, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
	if result != sess {
		t.Error("with no filters, should return same session")
	}
}

func TestApplyFilters_chain(t *testing.T) {
	sess := &Session{
		ID: "test",
		Messages: []Message{
			{Content: "msg-0"},
			{Content: "msg-1"},
			{Content: "msg-2"},
		},
	}

	filters := []SessionFilter{
		&mockFilter{name: "noop", removeIdx: -1},
		&mockFilter{name: "remove-last", removeIdx: 2},
	}

	result, results, err := ApplyFilters(sess, filters)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Applied {
		t.Error("first filter should not have applied")
	}
	if !results[1].Applied {
		t.Error("second filter should have applied")
	}

	if len(result.Messages) != 2 {
		t.Errorf("expected 2 messages after filtering, got %d", len(result.Messages))
	}

	// Original should be unchanged.
	if len(sess.Messages) != 3 {
		t.Error("original session should not be modified")
	}
}

func TestApplyFilters_errorStopsChain(t *testing.T) {
	sess := &Session{ID: "test", Messages: []Message{{Content: "hello"}}}

	filters := []SessionFilter{
		&mockFilter{name: "fail", shouldFail: true},
		&mockFilter{name: "never-reached", removeIdx: 0},
	}

	_, _, err := ApplyFilters(sess, filters)
	if err == nil {
		t.Fatal("expected error from failing filter")
	}
}

func TestCopySession_deepCopy(t *testing.T) {
	original := &Session{
		ID:      "test",
		Summary: "original",
		Messages: []Message{
			{
				Content: "msg-0",
				ToolCalls: []ToolCall{
					{ID: "tc-1", Name: "read", Output: "data"},
				},
				ContentBlocks: []ContentBlock{
					{Type: ContentBlockText, Text: "block-1"},
				},
				Images: []ImageMeta{
					{MediaType: "image/png"},
				},
			},
		},
		FileChanges: []FileChange{{FilePath: "file.go", ChangeType: ChangeModified}},
		Links:       []Link{{LinkType: LinkBranch, Ref: "main"}},
	}

	cp := CopySession(original)

	// Modify the copy.
	cp.Summary = "modified"
	cp.Messages[0].Content = "changed"
	cp.Messages[0].ToolCalls[0].Output = "changed"
	cp.Messages[0].ContentBlocks[0].Text = "changed"
	cp.FileChanges[0].FilePath = "changed.go"

	// Original should be unchanged.
	if original.Summary != "original" {
		t.Error("CopySession: Summary was aliased")
	}
	if original.Messages[0].Content != "msg-0" {
		t.Error("CopySession: Message.Content was aliased")
	}
	if original.Messages[0].ToolCalls[0].Output != "data" {
		t.Error("CopySession: ToolCall.Output was aliased")
	}
	if original.Messages[0].ContentBlocks[0].Text != "block-1" {
		t.Error("CopySession: ContentBlock.Text was aliased")
	}
	if original.FileChanges[0].FilePath != "file.go" {
		t.Error("CopySession: FileChange was aliased")
	}
}
