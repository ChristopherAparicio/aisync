package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/diagnostic"
)

func TestHandleInspectSession(t *testing.T) {
	h, svc := newTestHandlers(t)
	sess := seedSession(t, svc, "inspect-test")

	req := callToolReq("aisync_inspect_session", map[string]any{
		"session_id": string(sess.ID),
	})

	result, err := h.handleInspectSession(context.Background(), req)
	if err != nil {
		t.Fatalf("handleInspectSession error: %v", err)
	}

	text := requireTextResult(t, result)

	var got inspectResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if got.Report == nil {
		t.Fatal("expected non-nil report")
	}
	if got.Report.SessionID != string(sess.ID) {
		t.Errorf("session_id = %q, want %q", got.Report.SessionID, sess.ID)
	}
	if got.Report.Tokens == nil {
		t.Error("expected non-nil tokens section")
	}
	// Fixes should be nil when generate_fix is not requested
	if got.Fixes != nil {
		t.Error("expected nil fixes when generate_fix is false")
	}
}

func TestHandleInspectSession_WithFixes(t *testing.T) {
	h, svc := newTestHandlers(t)
	sess := seedSession(t, svc, "inspect-fix-test")

	req := callToolReq("aisync_inspect_session", map[string]any{
		"session_id":   string(sess.ID),
		"generate_fix": true,
	})

	result, err := h.handleInspectSession(context.Background(), req)
	if err != nil {
		t.Fatalf("handleInspectSession error: %v", err)
	}

	text := requireTextResult(t, result)

	var got inspectResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if got.Report == nil {
		t.Fatal("expected non-nil report")
	}
	if got.Fixes == nil {
		t.Fatal("expected non-nil fixes when generate_fix is true")
	}
	if got.Fixes.SessionID != string(sess.ID) {
		t.Errorf("fixes session_id = %q, want %q", got.Fixes.SessionID, sess.ID)
	}
}

func TestHandleInspectSession_SectionFilter(t *testing.T) {
	h, svc := newTestHandlers(t)
	sess := seedSession(t, svc, "inspect-section-test")

	req := callToolReq("aisync_inspect_session", map[string]any{
		"session_id": string(sess.ID),
		"section":    "tokens",
	})

	result, err := h.handleInspectSession(context.Background(), req)
	if err != nil {
		t.Fatalf("handleInspectSession error: %v", err)
	}

	text := requireTextResult(t, result)

	var got inspectResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if got.Report == nil {
		t.Fatal("expected non-nil report")
	}
	// Tokens should be populated
	if got.Report.Tokens == nil {
		t.Error("expected non-nil tokens section when section=tokens")
	}
	// Other sections should be nil
	if got.Report.Images != nil {
		t.Error("expected nil images when section=tokens")
	}
	if got.Report.Compaction != nil {
		t.Error("expected nil compaction when section=tokens")
	}
	if got.Report.Commands != nil {
		t.Error("expected nil commands when section=tokens")
	}
	if got.Report.ToolErrors != nil {
		t.Error("expected nil tool errors when section=tokens")
	}
	if got.Report.Patterns != nil {
		t.Error("expected nil patterns when section=tokens")
	}
}

func TestHandleInspectSession_NotFound(t *testing.T) {
	h, _ := newTestHandlers(t)

	req := callToolReq("aisync_inspect_session", map[string]any{
		"session_id": "nonexistent-session",
	})

	result, err := h.handleInspectSession(context.Background(), req)
	if err != nil {
		t.Fatalf("handleInspectSession error: %v", err)
	}

	errText := requireErrorResult(t, result)
	if errText == "" {
		t.Error("expected non-empty error text")
	}
}

func TestHandleInspectSession_MissingID(t *testing.T) {
	h, _ := newTestHandlers(t)

	req := callToolReq("aisync_inspect_session", map[string]any{})

	result, err := h.handleInspectSession(context.Background(), req)
	if err != nil {
		t.Fatalf("handleInspectSession error: %v", err)
	}

	errText := requireErrorResult(t, result)
	if errText == "" {
		t.Error("expected non-empty error for missing session_id")
	}
}

func TestFilterSection(t *testing.T) {
	report := &diagnostic.InspectReport{
		SessionID:  "test-123",
		Provider:   "claude-code",
		Messages:   10,
		Tokens:     &diagnostic.TokenSection{Input: 1000},
		Images:     &diagnostic.ImageSection{InlineImages: 5},
		Compaction: &diagnostic.CompactionSection{Count: 2},
		Commands:   &diagnostic.CommandSection{TotalCommands: 3},
		ToolErrors: &diagnostic.ToolErrorSection{ErrorCount: 1},
		Patterns:   &diagnostic.PatternSection{GlobStormCount: 1},
		Problems:   []diagnostic.Problem{{Title: "test problem"}},
	}

	tests := []struct {
		section    string
		checkField func(*diagnostic.InspectReport) bool
		desc       string
	}{
		{"tokens", func(r *diagnostic.InspectReport) bool { return r.Tokens != nil && r.Images == nil }, "tokens only"},
		{"images", func(r *diagnostic.InspectReport) bool { return r.Images != nil && r.Tokens == nil }, "images only"},
		{"compactions", func(r *diagnostic.InspectReport) bool { return r.Compaction != nil && r.Tokens == nil }, "compactions only"},
		{"commands", func(r *diagnostic.InspectReport) bool { return r.Commands != nil && r.Tokens == nil }, "commands only"},
		{"errors", func(r *diagnostic.InspectReport) bool { return r.ToolErrors != nil && r.Tokens == nil }, "errors only"},
		{"patterns", func(r *diagnostic.InspectReport) bool { return r.Patterns != nil && r.Tokens == nil }, "patterns only"},
		{"problems", func(r *diagnostic.InspectReport) bool { return len(r.Problems) > 0 && r.Tokens == nil }, "problems only"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			filtered := filterSection(report, tt.section)
			if !tt.checkField(filtered) {
				t.Errorf("filterSection(%q) failed check", tt.section)
			}
			// Identity fields should always be preserved
			if filtered.SessionID != "test-123" {
				t.Errorf("session_id lost after filter")
			}
			if filtered.Messages != 10 {
				t.Errorf("messages lost after filter")
			}
		})
	}

	// Unknown section returns full report
	full := filterSection(report, "unknown")
	if full.Tokens == nil || full.Images == nil {
		t.Error("unknown section should return full report")
	}
}
