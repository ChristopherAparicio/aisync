package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── aisync_recommendations ──

// TestHandleRecommendations_StoredEmpty covers the default path (read from
// store) when the store has no recommendations yet — should succeed with an
// empty slice, not error.
func TestHandleRecommendations_StoredEmpty(t *testing.T) {
	h, _ := newTestHandlers(t)

	req := callToolReq("aisync_recommendations", map[string]any{
		"project_path": "/nonexistent",
	})

	result, err := h.handleRecommendations(context.Background(), req)
	if err != nil {
		t.Fatalf("handleRecommendations error: %v", err)
	}

	text := requireTextResult(t, result)

	var got recommendationsResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if got.Source != "stored" {
		t.Errorf("expected source=stored, got %q", got.Source)
	}
	if got.Total != 0 {
		t.Errorf("expected total=0, got %d", got.Total)
	}
	if got.Stats == nil {
		t.Error("expected non-nil stats when reading from store")
	}
	if got.Fresh != nil {
		t.Error("expected fresh slice to be nil in stored mode")
	}
}

// TestHandleRecommendations_StoredNoStore ensures we fail gracefully when
// the store is nil. The user gets a clear "use fresh=true" hint.
func TestHandleRecommendations_StoredNoStore(t *testing.T) {
	h, _ := newTestHandlers(t)
	h.store = nil // simulate store unavailable

	req := callToolReq("aisync_recommendations", map[string]any{})

	result, err := h.handleRecommendations(context.Background(), req)
	if err != nil {
		t.Fatalf("handleRecommendations error: %v", err)
	}

	errText := requireErrorResult(t, result)
	if !strings.Contains(errText, "store unavailable") {
		t.Errorf("expected 'store unavailable' in error, got: %s", errText)
	}
}

// TestHandleRecommendations_FreshEmpty exercises the expensive regen path.
// With no sessions in the store, GenerateRecommendations should return an
// empty slice — not error.
func TestHandleRecommendations_FreshEmpty(t *testing.T) {
	h, _ := newTestHandlers(t)

	req := callToolReq("aisync_recommendations", map[string]any{
		"fresh":        true,
		"project_path": "/nonexistent",
	})

	result, err := h.handleRecommendations(context.Background(), req)
	if err != nil {
		t.Fatalf("handleRecommendations error: %v", err)
	}

	text := requireTextResult(t, result)

	var got recommendationsResult
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if got.Source != "fresh" {
		t.Errorf("expected source=fresh, got %q", got.Source)
	}
	if got.Stored != nil {
		t.Error("expected stored slice to be nil in fresh mode")
	}
	if got.Stats != nil {
		t.Error("expected stats to be nil in fresh mode")
	}
}

// TestFilterRecommendationsByPriority verifies the tiny helper that
// narrows a fresh rec slice to a single priority level.
func TestFilterRecommendationsByPriority(t *testing.T) {
	recs := []session.Recommendation{
		{Priority: "high", Title: "a"},
		{Priority: "medium", Title: "b"},
		{Priority: "high", Title: "c"},
		{Priority: "low", Title: "d"},
	}

	got := filterRecommendationsByPriority(recs, "high")
	if len(got) != 2 {
		t.Fatalf("expected 2 high-priority recs, got %d", len(got))
	}
	for _, r := range got {
		if r.Priority != "high" {
			t.Errorf("expected priority=high, got %q", r.Priority)
		}
	}

	if got := filterRecommendationsByPriority(recs, "missing"); len(got) != 0 {
		t.Errorf("expected 0 recs for missing priority, got %d", len(got))
	}
}

// ── aisync_diagnose ──

func TestHandleDiagnose(t *testing.T) {
	h, svc := newTestHandlers(t)
	sess := seedSession(t, svc, "diagnose-test")

	req := callToolReq("aisync_diagnose", map[string]any{
		"session_id": string(sess.ID),
	})

	result, err := h.handleDiagnose(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDiagnose error: %v", err)
	}

	text := requireTextResult(t, result)

	var report session.DiagnosisReport
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}

	// Quick scan must always populate the verdict status — one of the three
	// canonical grades defined by ComputeVerdict ("healthy"/"degraded"/"broken").
	switch report.Verdict.Status {
	case "healthy", "degraded", "broken":
		// expected
	default:
		t.Errorf("expected verdict status in {healthy,degraded,broken}, got %q", report.Verdict.Status)
	}

	// HealthScore.Total is bounded [0,100]; a zero score is legal for a minimal
	// seeded session, so we only assert the bounds here.
	if report.HealthScore.Total < 0 || report.HealthScore.Total > 100 {
		t.Errorf("expected HealthScore.Total in [0,100], got %d", report.HealthScore.Total)
	}

	// Verdict.Score should mirror HealthScore.Total (ComputeVerdict copies it).
	if report.Verdict.Score != report.HealthScore.Total {
		t.Errorf("expected verdict.score == health_score.total, got %d vs %d",
			report.Verdict.Score, report.HealthScore.Total)
	}
}

func TestHandleDiagnose_MissingID(t *testing.T) {
	h, _ := newTestHandlers(t)

	req := callToolReq("aisync_diagnose", map[string]any{})

	result, err := h.handleDiagnose(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDiagnose error: %v", err)
	}

	errText := requireErrorResult(t, result)
	if !strings.Contains(errText, "session_id is required") {
		t.Errorf("expected 'session_id is required' in error, got: %s", errText)
	}
}

func TestHandleDiagnose_NotFound(t *testing.T) {
	h, _ := newTestHandlers(t)

	req := callToolReq("aisync_diagnose", map[string]any{
		"session_id": "ses_0000000000000000000000000000",
	})

	result, err := h.handleDiagnose(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDiagnose error: %v", err)
	}

	// Expect error (loading session fails).
	if !result.IsError {
		t.Error("expected error result for nonexistent session")
	}
}

// ── aisync_skill_observation ──

// TestHandleSkillObservation_NoRegistry covers the defensive branch when
// the handler was constructed without a RegistryService — must error with
// a clear "registry service unavailable" message.
func TestHandleSkillObservation_NoRegistry(t *testing.T) {
	h, svc := newTestHandlers(t)
	sess := seedSession(t, svc, "skillobs-no-registry")

	// newTestHandlers does not wire a registry — confirm that defaults to nil.
	if h.registrySvc != nil {
		t.Fatal("test helper unexpectedly wired a registrySvc")
	}

	req := callToolReq("aisync_skill_observation", map[string]any{
		"session_id": string(sess.ID),
	})

	result, err := h.handleSkillObservation(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSkillObservation error: %v", err)
	}

	errText := requireErrorResult(t, result)
	if !strings.Contains(errText, "registry service unavailable") {
		t.Errorf("expected 'registry service unavailable' in error, got: %s", errText)
	}
}

func TestHandleSkillObservation_MissingID(t *testing.T) {
	h, _ := newTestHandlers(t)

	req := callToolReq("aisync_skill_observation", map[string]any{})

	result, err := h.handleSkillObservation(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSkillObservation error: %v", err)
	}

	errText := requireErrorResult(t, result)
	if !strings.Contains(errText, "session_id is required") {
		t.Errorf("expected 'session_id is required' in error, got: %s", errText)
	}
}
