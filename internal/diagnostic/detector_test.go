package diagnostic

import (
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Helpers ─────────────────────────────────────────────────────────────────

// makeReport builds a minimal InspectReport with the given overrides.
// Sections are nil by default; callers populate the ones they need.
func makeReport() *InspectReport {
	return &InspectReport{
		SessionID: "ses_test",
		Provider:  "opencode",
		Agent:     "test",
		Messages:  100,
		UserMsgs:  20,
		AsstMsgs:  80,
	}
}

// assertProblem checks that exactly one problem with the given ID was returned.
func assertProblem(t *testing.T, problems []Problem, id ProblemID) Problem {
	t.Helper()
	var found []Problem
	for _, p := range problems {
		if p.ID == id {
			found = append(found, p)
		}
	}
	if len(found) == 0 {
		t.Fatalf("expected problem %q but none detected", id)
	}
	if len(found) > 1 {
		t.Fatalf("expected 1 problem %q but found %d", id, len(found))
	}
	return found[0]
}

// assertNoProblem checks that no problem with the given ID was returned.
func assertNoProblem(t *testing.T, problems []Problem, id ProblemID) {
	t.Helper()
	for _, p := range problems {
		if p.ID == id {
			t.Fatalf("expected no problem %q but found one: %s", id, p.Title)
		}
	}
}

// ── Image detectors ─────────────────────────────────────────────────────────

func TestDetectExpensiveScreenshots_triggers(t *testing.T) {
	r := makeReport()
	r.Images = &ImageSection{
		ToolReadImages: 50,
		AvgTurnsInCtx:  80,
		TotalBilledTok: 25_000_000,
		EstImageCost:   75.0,
	}
	problems := detectExpensiveScreenshots(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	p := problems[0]
	if p.ID != ProblemExpensiveScreenshots {
		t.Errorf("wrong ID: %s", p.ID)
	}
	if p.Severity != SeverityHigh {
		t.Errorf("expected high severity for $75 cost, got %s", p.Severity)
	}
}

func TestDetectExpensiveScreenshots_belowThreshold(t *testing.T) {
	r := makeReport()
	r.Images = &ImageSection{
		ToolReadImages: 3, // too few
		AvgTurnsInCtx:  5, // too few turns
		TotalBilledTok: 100,
		EstImageCost:   0.01,
	}
	problems := detectExpensiveScreenshots(r)
	if len(problems) != 0 {
		t.Errorf("expected no problem, got %d", len(problems))
	}
}

func TestDetectExpensiveScreenshots_mediumSeverity(t *testing.T) {
	r := makeReport()
	r.Images = &ImageSection{
		ToolReadImages: 10,
		AvgTurnsInCtx:  20,
		TotalBilledTok: 5_000_000,
		EstImageCost:   10.0,
	}
	problems := detectExpensiveScreenshots(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityMedium {
		t.Errorf("expected medium severity for $10, got %s", problems[0].Severity)
	}
}

func TestDetectOversizedScreenshots_resizedButMany(t *testing.T) {
	r := makeReport()
	r.Images = &ImageSection{
		ToolReadImages: 100,
		SimctlCaptures: 100,
		SipsResizes:    95,
		AvgTurnsInCtx:  50,
		TotalBilledTok: 15_000_000,
		EstImageCost:   45.0,
	}
	problems := detectOversizedScreenshots(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].ID != ProblemOversizedScreenshots {
		t.Errorf("wrong ID: %s", problems[0].ID)
	}
}

func TestDetectOversizedScreenshots_tooFewCaptures(t *testing.T) {
	r := makeReport()
	r.Images = &ImageSection{
		SimctlCaptures: 2, // below threshold of 3
	}
	problems := detectOversizedScreenshots(r)
	if len(problems) != 0 {
		t.Errorf("expected no problem, got %d", len(problems))
	}
}

func TestDetectUnresizedScreenshots_triggers(t *testing.T) {
	r := makeReport()
	r.Images = &ImageSection{
		SimctlCaptures: 10,
		SipsResizes:    3, // 7 out of 10 unresized = 70%
	}
	problems := detectUnresizedScreenshots(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].ID != ProblemUnresizedScreenshots {
		t.Errorf("wrong ID: %s", problems[0].ID)
	}
}

func TestDetectUnresizedScreenshots_allResized(t *testing.T) {
	r := makeReport()
	r.Images = &ImageSection{
		SimctlCaptures: 10,
		SipsResizes:    10,
	}
	problems := detectUnresizedScreenshots(r)
	if len(problems) != 0 {
		t.Errorf("expected no problem, got %d", len(problems))
	}
}

// ── Compaction detectors ────────────────────────────────────────────────────

func TestDetectFrequentCompaction_triggers(t *testing.T) {
	r := makeReport()
	r.Compaction = &CompactionSection{
		Count:           10,
		PerUserMsg:      0.25,
		TotalTokensLost: 500_000,
	}
	problems := detectFrequentCompaction(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityMedium {
		t.Errorf("expected medium for 0.25 rate, got %s", problems[0].Severity)
	}
}

func TestDetectFrequentCompaction_highSeverity(t *testing.T) {
	r := makeReport()
	r.Compaction = &CompactionSection{
		Count:           15,
		PerUserMsg:      0.35,
		TotalTokensLost: 1_000_000,
	}
	problems := detectFrequentCompaction(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityHigh {
		t.Errorf("expected high for 0.35 rate, got %s", problems[0].Severity)
	}
}

func TestDetectFrequentCompaction_belowThreshold(t *testing.T) {
	r := makeReport()
	r.Compaction = &CompactionSection{
		Count:      5,
		PerUserMsg: 0.10, // below 0.15
	}
	problems := detectFrequentCompaction(r)
	if len(problems) != 0 {
		t.Errorf("expected no problem, got %d", len(problems))
	}
}

func TestDetectContextNearLimit_triggers(t *testing.T) {
	r := makeReport()
	r.Compaction = &CompactionSection{
		Count:           10,
		AvgBeforeTokens: 170_000,
		Events: []CompactionView{
			{BeforeTokens: 160_000}, {BeforeTokens: 180_000},
			{BeforeTokens: 170_000}, {BeforeTokens: 155_000},
		},
	}
	problems := detectContextNearLimit(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityMedium {
		t.Errorf("expected medium for 170K, got %s", problems[0].Severity)
	}
}

func TestDetectContextNearLimit_highSeverity(t *testing.T) {
	r := makeReport()
	r.Compaction = &CompactionSection{
		Count:           5,
		AvgBeforeTokens: 195_000,
		Events:          []CompactionView{{BeforeTokens: 195_000}},
	}
	problems := detectContextNearLimit(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityHigh {
		t.Errorf("expected high for 195K, got %s", problems[0].Severity)
	}
}

func TestDetectContextNearLimit_belowThreshold(t *testing.T) {
	r := makeReport()
	r.Compaction = &CompactionSection{
		Count:           5,
		AvgBeforeTokens: 120_000, // below 150K
	}
	problems := detectContextNearLimit(r)
	if len(problems) != 0 {
		t.Errorf("expected no problem, got %d", len(problems))
	}
}

func TestDetectCompactionAccelerating_triggers(t *testing.T) {
	r := makeReport()
	r.Compaction = &CompactionSection{
		Count:            10,
		PerUserMsg:       0.20,
		LastQuartileRate: 0.30, // 50% higher
	}
	problems := detectCompactionAccelerating(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
}

func TestDetectCompactionAccelerating_notAccelerating(t *testing.T) {
	r := makeReport()
	r.Compaction = &CompactionSection{
		Count:            10,
		PerUserMsg:       0.20,
		LastQuartileRate: 0.22, // only 10% higher, below 1.3x
	}
	problems := detectCompactionAccelerating(r)
	if len(problems) != 0 {
		t.Errorf("expected no problem, got %d", len(problems))
	}
}

// ── Command detectors ───────────────────────────────────────────────────────

func TestDetectVerboseCommands_triggers(t *testing.T) {
	r := makeReport()
	r.Commands = &CommandSection{
		TotalOutputBytes: 800_000,
		TotalOutputTok:   200_000,
		TotalCommands:    50,
		TopByOutput: []CommandEntry{
			{Command: "go", TotalBytes: 300_000, Invocations: 20},
		},
	}
	problems := detectVerboseCommands(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityMedium {
		t.Errorf("expected medium for 800KB, got %s", problems[0].Severity)
	}
}

func TestDetectVerboseCommands_highSeverity(t *testing.T) {
	r := makeReport()
	r.Commands = &CommandSection{
		TotalOutputBytes: 2_000_000,
		TotalOutputTok:   500_000,
		TotalCommands:    100,
		TopByOutput: []CommandEntry{
			{Command: "cat", TotalBytes: 1_500_000, Invocations: 50},
		},
	}
	problems := detectVerboseCommands(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityHigh {
		t.Errorf("expected high for 2MB, got %s", problems[0].Severity)
	}
}

func TestDetectVerboseCommands_belowThreshold(t *testing.T) {
	r := makeReport()
	r.Commands = &CommandSection{
		TotalOutputBytes: 50_000, // below 100K
		TotalCommands:    10,
	}
	problems := detectVerboseCommands(r)
	if len(problems) != 0 {
		t.Errorf("expected no problem, got %d", len(problems))
	}
}

func TestDetectRepeatedCommands_triggers(t *testing.T) {
	r := makeReport()
	r.Commands = &CommandSection{
		TotalCommands:  50,
		UniqueCommands: 20,
		RepeatedRatio:  0.60, // 60% duplicates
	}
	problems := detectRepeatedCommands(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityMedium {
		t.Errorf("expected medium for 60%%, got %s", problems[0].Severity)
	}
}

func TestDetectRepeatedCommands_belowThreshold(t *testing.T) {
	r := makeReport()
	r.Commands = &CommandSection{
		TotalCommands:  50,
		UniqueCommands: 40,
		RepeatedRatio:  0.20, // below 0.30
	}
	problems := detectRepeatedCommands(r)
	if len(problems) != 0 {
		t.Errorf("expected no problem, got %d", len(problems))
	}
}

func TestDetectLongRunningCommands_triggers(t *testing.T) {
	r := makeReport()
	r.Commands = &CommandSection{
		TopByOutput: []CommandEntry{
			{Command: "build", AvgDuration: 45_000}, // 45s
			{Command: "test", AvgDuration: 60_000},  // 60s
			{Command: "ls", AvgDuration: 100},       // fast
		},
	}
	problems := detectLongRunningCommands(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
}

func TestDetectLongRunningCommands_allFast(t *testing.T) {
	r := makeReport()
	r.Commands = &CommandSection{
		TopByOutput: []CommandEntry{
			{Command: "ls", AvgDuration: 100},
			{Command: "cat", AvgDuration: 50},
		},
	}
	problems := detectLongRunningCommands(r)
	if len(problems) != 0 {
		t.Errorf("expected no problem, got %d", len(problems))
	}
}

// ── Token detectors ─────────────────────────────────────────────────────────

func TestDetectLowCacheUtilization_triggers(t *testing.T) {
	r := makeReport()
	r.Tokens = &TokenSection{
		Input:      1_000_000,
		CacheRead:  200_000,
		CacheWrite: 50_000,
		CachePct:   20.0,
	}
	problems := detectLowCacheUtilization(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityMedium {
		t.Errorf("expected medium for 20%%, got %s", problems[0].Severity)
	}
}

func TestDetectLowCacheUtilization_veryLow(t *testing.T) {
	r := makeReport()
	r.Tokens = &TokenSection{
		Input:      1_000_000,
		CacheRead:  100_000,
		CacheWrite: 10_000,
		CachePct:   10.0,
	}
	problems := detectLowCacheUtilization(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityHigh {
		t.Errorf("expected high for 10%%, got %s", problems[0].Severity)
	}
}

func TestDetectLowCacheUtilization_goodCache(t *testing.T) {
	r := makeReport()
	r.Tokens = &TokenSection{
		Input:      1_000_000,
		CacheRead:  800_000,
		CacheWrite: 100_000,
		CachePct:   80.0,
	}
	problems := detectLowCacheUtilization(r)
	if len(problems) != 0 {
		t.Errorf("expected no problem, got %d", len(problems))
	}
}

func TestDetectLowCacheUtilization_noProvider(t *testing.T) {
	// Provider doesn't report cache metrics at all
	r := makeReport()
	r.Tokens = &TokenSection{
		Input:      1_000_000,
		CacheRead:  0,
		CacheWrite: 0,
		CachePct:   0,
	}
	problems := detectLowCacheUtilization(r)
	if len(problems) != 0 {
		t.Errorf("expected no problem for no-cache provider, got %d", len(problems))
	}
}

func TestDetectHighInputRatio_triggers(t *testing.T) {
	r := makeReport()
	r.Tokens = &TokenSection{
		Input:            50_000_000,
		Output:           200_000,
		InputOutputRatio: 250,
	}
	problems := detectHighInputRatio(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityMedium {
		t.Errorf("expected medium for 250:1, got %s", problems[0].Severity)
	}
}

func TestDetectHighInputRatio_extreme(t *testing.T) {
	r := makeReport()
	r.Tokens = &TokenSection{
		Input:            500_000_000,
		Output:           500_000,
		InputOutputRatio: 1000,
	}
	problems := detectHighInputRatio(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityHigh {
		t.Errorf("expected high for 1000:1, got %s", problems[0].Severity)
	}
}

func TestDetectHighInputRatio_normal(t *testing.T) {
	r := makeReport()
	r.Tokens = &TokenSection{
		Input:            1_000_000,
		Output:           50_000,
		InputOutputRatio: 20,
	}
	problems := detectHighInputRatio(r)
	if len(problems) != 0 {
		t.Errorf("expected no problem, got %d", len(problems))
	}
}

func TestDetectContextThrashing_triggers(t *testing.T) {
	r := makeReport()
	r.Compaction = &CompactionSection{
		Count:          8,
		IntervalMedian: 15, // rapid cycling
	}
	problems := detectContextThrashing(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityHigh {
		t.Errorf("expected high severity, got %s", problems[0].Severity)
	}
}

func TestDetectContextThrashing_normalInterval(t *testing.T) {
	r := makeReport()
	r.Compaction = &CompactionSection{
		Count:          8,
		IntervalMedian: 50, // healthy
	}
	problems := detectContextThrashing(r)
	if len(problems) != 0 {
		t.Errorf("expected no problem, got %d", len(problems))
	}
}

// ── Tool error detectors ────────────────────────────────────────────────────

func TestDetectToolErrorLoops_triggers(t *testing.T) {
	r := makeReport()
	r.ToolErrors = &ToolErrorSection{
		ErrorLoops: []ErrorLoop{
			{ToolName: "edit", ErrorCount: 4, TotalTokens: 500_000},
			{ToolName: "bash", ErrorCount: 3, TotalTokens: 300_000},
		},
		ConsecutiveMax: 4,
	}
	problems := detectToolErrorLoops(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].ID != ProblemToolErrorLoops {
		t.Errorf("wrong ID: %s", problems[0].ID)
	}
}

func TestDetectToolErrorLoops_noLoops(t *testing.T) {
	r := makeReport()
	r.ToolErrors = &ToolErrorSection{
		ErrorLoops: nil,
	}
	problems := detectToolErrorLoops(r)
	if len(problems) != 0 {
		t.Errorf("expected no problem, got %d", len(problems))
	}
}

func TestDetectToolErrorLoops_manyLoopsHighSeverity(t *testing.T) {
	r := makeReport()
	r.ToolErrors = &ToolErrorSection{
		ErrorLoops: []ErrorLoop{
			{ToolName: "edit", ErrorCount: 5, TotalTokens: 500_000},
			{ToolName: "bash", ErrorCount: 4, TotalTokens: 300_000},
			{ToolName: "write", ErrorCount: 3, TotalTokens: 200_000},
			{ToolName: "read", ErrorCount: 3, TotalTokens: 100_000},
		},
		ConsecutiveMax: 5,
	}
	problems := detectToolErrorLoops(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityHigh {
		t.Errorf("expected high for 4 loops, got %s", problems[0].Severity)
	}
}

func TestDetectAbandonedToolCalls_triggers(t *testing.T) {
	r := makeReport()
	r.ToolErrors = &ToolErrorSection{
		TotalToolCalls: 100,
		ErrorCount:     25,
		ErrorRate:      0.25,
	}
	problems := detectAbandonedToolCalls(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityMedium {
		t.Errorf("expected medium for 25%%, got %s", problems[0].Severity)
	}
}

func TestDetectAbandonedToolCalls_extreme(t *testing.T) {
	r := makeReport()
	r.ToolErrors = &ToolErrorSection{
		TotalToolCalls: 50,
		ErrorCount:     20,
		ErrorRate:      0.40,
	}
	problems := detectAbandonedToolCalls(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityHigh {
		t.Errorf("expected high for 40%%, got %s", problems[0].Severity)
	}
}

func TestDetectAbandonedToolCalls_lowRate(t *testing.T) {
	r := makeReport()
	r.ToolErrors = &ToolErrorSection{
		TotalToolCalls: 100,
		ErrorCount:     5,
		ErrorRate:      0.05,
	}
	problems := detectAbandonedToolCalls(r)
	if len(problems) != 0 {
		t.Errorf("expected no problem, got %d", len(problems))
	}
}

// ── Behavioral pattern detectors ────────────────────────────────────────────

func TestDetectYoloEditing_triggers(t *testing.T) {
	r := makeReport()
	r.Patterns = &PatternSection{
		WriteWithoutReadCount: 25,
	}
	problems := detectYoloEditing(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityMedium {
		t.Errorf("expected medium for 25 files, got %s", problems[0].Severity)
	}
}

func TestDetectYoloEditing_fewFiles(t *testing.T) {
	r := makeReport()
	r.Patterns = &PatternSection{
		WriteWithoutReadCount: 3, // below threshold of 5
	}
	problems := detectYoloEditing(r)
	if len(problems) != 0 {
		t.Errorf("expected no problem, got %d", len(problems))
	}
}

func TestDetectExcessiveGlobbing_triggers(t *testing.T) {
	r := makeReport()
	r.Patterns = &PatternSection{
		GlobStormCount: 3,
	}
	problems := detectExcessiveGlobbing(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
}

func TestDetectExcessiveGlobbing_none(t *testing.T) {
	r := makeReport()
	r.Patterns = &PatternSection{
		GlobStormCount: 0,
	}
	problems := detectExcessiveGlobbing(r)
	if len(problems) != 0 {
		t.Errorf("expected no problem, got %d", len(problems))
	}
}

func TestDetectConversationDrift_corrections(t *testing.T) {
	r := makeReport()
	r.Patterns = &PatternSection{
		UserCorrectionCount: 8,
		LongestRunLength:    15,
	}
	problems := detectConversationDrift(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
}

func TestDetectConversationDrift_longRuns(t *testing.T) {
	r := makeReport()
	r.Patterns = &PatternSection{
		UserCorrectionCount: 2,
		LongestRunLength:    60,
	}
	problems := detectConversationDrift(r)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].Severity != SeverityMedium {
		t.Errorf("expected medium for 60 longest run, got %s", problems[0].Severity)
	}
}

func TestDetectConversationDrift_noIssue(t *testing.T) {
	r := makeReport()
	r.Patterns = &PatternSection{
		UserCorrectionCount: 2,
		LongestRunLength:    10,
	}
	problems := detectConversationDrift(r)
	if len(problems) != 0 {
		t.Errorf("expected no problem, got %d", len(problems))
	}
}

// ── Integration: RunModules ──────────────────────────────────────────────────

func TestRunModules_allSectionsNil(t *testing.T) {
	r := makeReport()
	sess := &session.Session{}
	modules := DefaultModules()
	problems, _ := RunModules(modules, sess, r)
	if len(problems) != 0 {
		t.Errorf("expected 0 problems with nil sections, got %d", len(problems))
	}
}

func TestRunModules_multipleDetected(t *testing.T) {
	// Build a session that activates the images module
	sess := &session.Session{
		Messages: []session.Message{
			{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
				{Name: "mcp_read", Input: `{"filePath": "/tmp/screenshot.png"}`},
			}},
		},
	}
	r := makeReport()
	r.Images = &ImageSection{
		ToolReadImages: 50,
		AvgTurnsInCtx:  80,
		TotalBilledTok: 25_000_000,
		EstImageCost:   75.0,
	}
	r.Tokens = &TokenSection{
		Input:            500_000_000,
		Output:           500_000,
		InputOutputRatio: 1000,
		CacheRead:        100_000,
		CacheWrite:       10_000,
		CachePct:         10.0,
	}
	r.Compaction = &CompactionSection{
		Count:          8,
		IntervalMedian: 15,
	}
	r.Patterns = &PatternSection{
		WriteWithoutReadCount: 25,
		GlobStormCount:        3,
	}

	modules := DefaultModules()
	problems, _ := RunModules(modules, sess, r)
	if len(problems) < 5 {
		t.Errorf("expected at least 5 problems, got %d", len(problems))
	}

	// Verify specific problems are present
	assertProblem(t, problems, ProblemExpensiveScreenshots)
	assertProblem(t, problems, ProblemHighInputRatio)
	assertProblem(t, problems, ProblemLowCacheUtilization)
	assertProblem(t, problems, ProblemContextThrashing)
	assertProblem(t, problems, ProblemYoloEditing)
	assertProblem(t, problems, ProblemExcessiveGlobbing)
}

// ── Format helpers ──────────────────────────────────────────────────────────

func TestFmtInt(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1_500, "1.5K"},
		{1_500_000, "1.5M"},
	}
	for _, tc := range cases {
		got := fmtInt(tc.in)
		if got != tc.want {
			t.Errorf("fmtInt(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFmtTok(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{500, "500"},
		{1_200, "1.2K"},
		{2_500_000, "2.5M"},
	}
	for _, tc := range cases {
		got := fmtTok(tc.in)
		if got != tc.want {
			t.Errorf("fmtTok(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFmtBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1_500, "1.5 KB"},
		{2_500_000, "2.5 MB"},
	}
	for _, tc := range cases {
		got := fmtBytes(tc.in)
		if got != tc.want {
			t.Errorf("fmtBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCountAbove(t *testing.T) {
	events := []CompactionView{
		{BeforeTokens: 100_000},
		{BeforeTokens: 160_000},
		{BeforeTokens: 180_000},
		{BeforeTokens: 140_000},
	}
	if got := countAbove(events, 150_000); got != 2 {
		t.Errorf("countAbove(150K) = %d, want 2", got)
	}
}

func TestJoinMax(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	got := JoinMax(items, 3)
	want := "a; b; c (+2 more)"
	if got != want {
		t.Errorf("JoinMax = %q, want %q", got, want)
	}

	got = JoinMax(items[:2], 3)
	want = "a; b"
	if got != want {
		t.Errorf("JoinMax = %q, want %q", got, want)
	}
}
