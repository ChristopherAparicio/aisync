package session

import (
	"testing"
)

// ── BuildErrorTimeline ──

func TestBuildErrorTimeline_noErrors(t *testing.T) {
	sess := &Session{Messages: make([]Message, 20)}
	entries := BuildErrorTimeline(sess)
	if entries != nil {
		t.Errorf("expected nil for session without errors, got %d entries", len(entries))
	}
}

func TestBuildErrorTimeline_positionsErrors(t *testing.T) {
	sess := &Session{
		Messages: make([]Message, 40),
		Errors: []SessionError{
			{MessageIndex: 5, Category: ErrorCategoryToolError},
			{MessageIndex: 35, Category: ErrorCategoryContextOverflow},
		},
	}
	sess.Messages[5].Role = "assistant"
	sess.Messages[35].Role = "assistant"

	entries := BuildErrorTimeline(sess)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].Phase != "early" {
		t.Errorf("msg 5 of 40 should be early, got %q", entries[0].Phase)
	}
	if entries[1].Phase != "late" {
		t.Errorf("msg 35 of 40 should be late, got %q", entries[1].Phase)
	}
	if entries[0].MessageRole != "assistant" {
		t.Errorf("expected role assistant, got %q", entries[0].MessageRole)
	}
}

func TestBuildErrorTimeline_detectsEscalation(t *testing.T) {
	sess := &Session{
		Messages: make([]Message, 20),
		Errors: []SessionError{
			{MessageIndex: 10, Category: ErrorCategoryToolError},
			{MessageIndex: 11, Category: ErrorCategoryToolError}, // same → escalation
			{MessageIndex: 12, Category: ErrorCategoryRateLimit}, // different → not escalation
		},
	}

	entries := BuildErrorTimeline(sess)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].IsEscalation {
		t.Error("first error should not be escalation")
	}
	if !entries[1].IsEscalation {
		t.Error("second error (same category) should be escalation")
	}
	if entries[2].IsEscalation {
		t.Error("third error (different category) should not be escalation")
	}
}

// ── classifyPhase ──

func TestClassifyPhase(t *testing.T) {
	tests := []struct {
		msgIndex int
		total    int
		want     string
	}{
		{0, 100, "early"},
		{24, 100, "early"},
		{25, 100, "middle"},
		{74, 100, "middle"},
		{75, 100, "late"},
		{99, 100, "late"},
		{0, 0, "early"},
	}
	for _, tt := range tests {
		got := classifyPhase(tt.msgIndex, tt.total)
		if got != tt.want {
			t.Errorf("classifyPhase(%d, %d) = %q, want %q", tt.msgIndex, tt.total, got, tt.want)
		}
	}
}

// ── AnalyzePhases ──

func TestAnalyzePhases_tooShort(t *testing.T) {
	sess := &Session{Messages: make([]Message, 3)}
	pa := AnalyzePhases(sess)
	if pa.Pattern != "too-short" {
		t.Errorf("expected too-short, got %q", pa.Pattern)
	}
}

func TestAnalyzePhases_healthy(t *testing.T) {
	msgs := make([]Message, 40)
	for i := range msgs {
		msgs[i].InputTokens = 1000
		msgs[i].OutputTokens = 500
		// No tool errors.
	}
	sess := &Session{Messages: msgs}
	pa := AnalyzePhases(sess)
	if pa.Pattern != "healthy" {
		t.Errorf("expected healthy, got %q", pa.Pattern)
	}
	if pa.TurningPoint != 0 {
		t.Errorf("expected no turning point, got %d", pa.TurningPoint)
	}
}

func TestAnalyzePhases_cleanStartLateCrash(t *testing.T) {
	msgs := make([]Message, 40)
	for i := range msgs {
		msgs[i].InputTokens = 1000
		msgs[i].OutputTokens = 500
	}
	// Add lots of tool errors in the last quarter (msgs 30-39).
	for i := 30; i < 40; i++ {
		msgs[i].ToolCalls = []ToolCall{
			{Name: "bash", State: ToolStateError},
			{Name: "bash", State: ToolStateError},
			{Name: "bash", State: ToolStateError},
		}
	}
	sess := &Session{Messages: msgs}
	pa := AnalyzePhases(sess)
	if pa.Pattern != "clean-start-late-crash" {
		t.Errorf("expected clean-start-late-crash, got %q", pa.Pattern)
	}
	if pa.TurningPoint == 0 {
		t.Error("expected a turning point > 0")
	}
}

func TestAnalyzePhases_errorFromStart(t *testing.T) {
	msgs := make([]Message, 40)
	for i := range msgs {
		msgs[i].InputTokens = 1000
		msgs[i].OutputTokens = 500
		msgs[i].ToolCalls = []ToolCall{
			{Name: "bash", State: ToolStateError},
			{Name: "bash", State: ToolStateError},
		}
	}
	sess := &Session{Messages: msgs}
	pa := AnalyzePhases(sess)
	if pa.Pattern != "error-from-start" && pa.Pattern != "steady-decline" {
		t.Errorf("expected error-from-start or steady-decline, got %q", pa.Pattern)
	}
}

// ── BuildToolReport ──

func TestBuildToolReport_noTools(t *testing.T) {
	sess := &Session{Messages: make([]Message, 5)}
	report := BuildToolReport(sess)
	if report.TotalCalls != 0 {
		t.Errorf("expected 0 calls, got %d", report.TotalCalls)
	}
	if len(report.Tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(report.Tools))
	}
}

func TestBuildToolReport_countsAndSorts(t *testing.T) {
	sess := &Session{
		Messages: []Message{
			{ToolCalls: []ToolCall{
				{Name: "read", State: ToolStateCompleted},
				{Name: "bash", State: ToolStateError},
				{Name: "bash", State: ToolStateCompleted},
			}},
			{ToolCalls: []ToolCall{
				{Name: "bash", State: ToolStateError},
				{Name: "write", State: ToolStateCompleted},
			}},
		},
	}
	report := BuildToolReport(sess)

	if report.TotalCalls != 5 {
		t.Errorf("expected 5 total calls, got %d", report.TotalCalls)
	}
	if report.TotalErrors != 2 {
		t.Errorf("expected 2 total errors, got %d", report.TotalErrors)
	}

	// bash should be first (most errors).
	if len(report.Tools) < 1 || report.Tools[0].Name != "bash" {
		t.Errorf("expected bash first (most errors), got %v", report.Tools)
	}
	if report.Tools[0].Calls != 3 {
		t.Errorf("bash should have 3 calls, got %d", report.Tools[0].Calls)
	}
	if report.Tools[0].Errors != 2 {
		t.Errorf("bash should have 2 errors, got %d", report.Tools[0].Errors)
	}
}

// ── ComputeVerdict ──

func TestComputeVerdict_healthy(t *testing.T) {
	hs := HealthScore{Total: 85, Grade: "A"}
	ol := OverloadAnalysis{Verdict: "healthy"}
	pa := PhaseAnalysis{Pattern: "healthy"}

	v := ComputeVerdict(hs, ol, pa)
	if v.Status != "healthy" {
		t.Errorf("expected healthy, got %q", v.Status)
	}
	if v.Score != 85 {
		t.Errorf("expected score 85, got %d", v.Score)
	}
}

func TestComputeVerdict_broken(t *testing.T) {
	hs := HealthScore{Total: 25, Grade: "F"}
	ol := OverloadAnalysis{Verdict: "overloaded", Reason: "output ratio declined 60%"}
	pa := PhaseAnalysis{Pattern: "clean-start-late-crash", TurningPoint: 42}

	v := ComputeVerdict(hs, ol, pa)
	if v.Status != "broken" {
		t.Errorf("expected broken, got %q", v.Status)
	}
	if v.Score != 25 {
		t.Errorf("expected score 25, got %d", v.Score)
	}
	if v.OneLiner == "" {
		t.Error("expected non-empty one-liner")
	}
}

func TestComputeVerdict_degraded(t *testing.T) {
	hs := HealthScore{Total: 55, Grade: "C"}
	ol := OverloadAnalysis{Verdict: "declining", Reason: "error rate increased by 12pp"}
	pa := PhaseAnalysis{Pattern: "intermittent"}

	v := ComputeVerdict(hs, ol, pa)
	if v.Status != "degraded" {
		t.Errorf("expected degraded, got %q", v.Status)
	}
}

// ── ComputeRestoreAdvice ──

func TestComputeRestoreAdvice_healthySession(t *testing.T) {
	report := &DiagnosisReport{
		Verdict: DiagnosisVerdict{Status: "healthy"},
	}
	advice := ComputeRestoreAdvice(report)
	if advice != nil {
		t.Error("expected nil advice for healthy session")
	}
}

func TestComputeRestoreAdvice_withTurningPoint(t *testing.T) {
	report := &DiagnosisReport{
		Verdict:      DiagnosisVerdict{Status: "broken"},
		Phases:       PhaseAnalysis{TurningPoint: 35},
		ErrorSummary: SessionErrorSummary{TotalErrors: 8},
		ErrorTimeline: []ErrorTimelineEntry{
			{Error: SessionError{Category: ErrorCategoryToolError, ToolCallID: "tc-1"}},
		},
	}
	advice := ComputeRestoreAdvice(report)
	if advice == nil {
		t.Fatal("expected restore advice")
	}
	if advice.RecommendedRewindTo != 33 {
		t.Errorf("expected rewind to 33 (35-2), got %d", advice.RecommendedRewindTo)
	}
	// Should suggest both --clean-errors and --fix-orphans.
	hasClean, hasOrphans := false, false
	for _, f := range advice.SuggestedFilters {
		if f == "--clean-errors" {
			hasClean = true
		}
		if f == "--fix-orphans" {
			hasOrphans = true
		}
	}
	if !hasClean {
		t.Error("expected --clean-errors suggestion")
	}
	if !hasOrphans {
		t.Error("expected --fix-orphans suggestion")
	}
}

func TestComputeRestoreAdvice_noRewind(t *testing.T) {
	report := &DiagnosisReport{
		Verdict:      DiagnosisVerdict{Status: "degraded"},
		Phases:       PhaseAnalysis{TurningPoint: 0},
		ErrorSummary: SessionErrorSummary{TotalErrors: 3},
	}
	advice := ComputeRestoreAdvice(report)
	if advice == nil {
		t.Fatal("expected advice (has errors)")
	}
	if advice.RecommendedRewindTo != 0 {
		t.Errorf("expected no rewind, got %d", advice.RecommendedRewindTo)
	}
}

// ── sortToolReportEntries ──

func TestSortToolReportEntries(t *testing.T) {
	entries := []ToolReportEntry{
		{Name: "read", Calls: 30, Errors: 0},
		{Name: "bash", Calls: 10, Errors: 5},
		{Name: "write", Calls: 20, Errors: 2},
	}
	sortToolReportEntries(entries)
	if entries[0].Name != "bash" {
		t.Errorf("expected bash first (most errors), got %q", entries[0].Name)
	}
	if entries[1].Name != "write" {
		t.Errorf("expected write second, got %q", entries[1].Name)
	}
	if entries[2].Name != "read" {
		t.Errorf("expected read last (0 errors), got %q", entries[2].Name)
	}
}
