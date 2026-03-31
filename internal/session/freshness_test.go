package session

import (
	"testing"
	"time"
)

func TestAnalyzeFreshness_NoCompaction(t *testing.T) {
	now := time.Now()
	msgs := []Message{
		{Role: RoleUser, InputTokens: 5000, Timestamp: now},
		{Role: RoleAssistant, InputTokens: 10000, OutputTokens: 2000, Timestamp: now.Add(30 * time.Second),
			ToolCalls: []ToolCall{{Name: "bash", State: ToolStateCompleted}}},
		{Role: RoleUser, InputTokens: 12000, Timestamp: now.Add(60 * time.Second)},
		{Role: RoleAssistant, InputTokens: 15000, OutputTokens: 3000, Timestamp: now.Add(90 * time.Second),
			ToolCalls: []ToolCall{{Name: "edit", State: ToolStateCompleted}}},
	}

	result := AnalyzeFreshness(msgs, CompactionSummary{})

	if result.CompactionCount != 0 {
		t.Errorf("CompactionCount = %d, want 0", result.CompactionCount)
	}
	if len(result.Phases) != 1 {
		t.Fatalf("Phases = %d, want 1 (single pre-compaction phase)", len(result.Phases))
	}
	if result.Phases[0].Phase != 0 {
		t.Errorf("Phase[0].Phase = %d, want 0", result.Phases[0].Phase)
	}
	if result.Phases[0].MessageCount != 4 {
		t.Errorf("Phase[0].MessageCount = %d, want 4", result.Phases[0].MessageCount)
	}
	if result.Phases[0].ErrorRate != 0 {
		t.Errorf("Phase[0].ErrorRate = %.1f, want 0", result.Phases[0].ErrorRate)
	}
}

func TestAnalyzeFreshness_WithOneCompaction(t *testing.T) {
	now := time.Now()
	msgs := []Message{
		// Phase 0: pre-compaction (msgs 0-3)
		{Role: RoleUser, InputTokens: 5000, Timestamp: now},
		{Role: RoleAssistant, InputTokens: 10000, OutputTokens: 2000, Timestamp: now.Add(30 * time.Second),
			ToolCalls: []ToolCall{{Name: "bash", State: ToolStateCompleted}}},
		{Role: RoleUser, InputTokens: 12000, Timestamp: now.Add(60 * time.Second)},
		{Role: RoleAssistant, InputTokens: 80000, OutputTokens: 3000, Timestamp: now.Add(90 * time.Second),
			ToolCalls: []ToolCall{{Name: "edit", State: ToolStateCompleted}}},
		// Compaction happens here (between msg 3 and 4)
		// Phase 1: post-compaction (msgs 4-7)
		{Role: RoleUser, InputTokens: 10000, Timestamp: now.Add(120 * time.Second)},
		{Role: RoleAssistant, InputTokens: 15000, OutputTokens: 1000, Timestamp: now.Add(150 * time.Second),
			ToolCalls: []ToolCall{{Name: "bash", State: ToolStateError}}},
		{Role: RoleAssistant, InputTokens: 16000, OutputTokens: 1200, Timestamp: now.Add(180 * time.Second),
			ToolCalls: []ToolCall{{Name: "bash", State: ToolStateCompleted}}}, // retry
		{Role: RoleAssistant, InputTokens: 18000, OutputTokens: 1500, Timestamp: now.Add(210 * time.Second),
			ToolCalls: []ToolCall{{Name: "edit", State: ToolStateError}}},
	}

	compactions := CompactionSummary{
		TotalCompactions: 1,
		Events: []CompactionEvent{
			{BeforeMessageIdx: 3, AfterMessageIdx: 4, TokensLost: 65000},
		},
	}

	result := AnalyzeFreshness(msgs, compactions)

	if result.CompactionCount != 1 {
		t.Errorf("CompactionCount = %d, want 1", result.CompactionCount)
	}
	if len(result.Phases) != 2 {
		t.Fatalf("Phases = %d, want 2", len(result.Phases))
	}

	// Phase 0: pre-compaction should have 0% errors (all completed).
	if result.Phases[0].ErrorRate != 0 {
		t.Errorf("Phase[0].ErrorRate = %.1f, want 0", result.Phases[0].ErrorRate)
	}

	// Phase 1: post-compaction should have higher error rate.
	if result.Phases[1].ErrorRate <= 0 {
		t.Error("Phase[1].ErrorRate should be > 0 (has tool errors)")
	}
	if result.Phases[1].ToolErrors != 2 {
		t.Errorf("Phase[1].ToolErrors = %d, want 2", result.Phases[1].ToolErrors)
	}

	// Error rate should grow.
	if result.ErrorRateGrowth <= 0 {
		t.Errorf("ErrorRateGrowth = %.1f, want > 0", result.ErrorRateGrowth)
	}

	// Recommendation should mention compaction.
	if result.Recommendation == "" {
		t.Error("Recommendation should not be empty")
	}
}

func TestAnalyzeFreshness_MultipleCompactions(t *testing.T) {
	msgs := make([]Message, 30)
	for i := range msgs {
		if i%2 == 0 {
			msgs[i] = Message{Role: RoleUser, InputTokens: 5000 + i*1000}
		} else {
			msgs[i] = Message{
				Role: RoleAssistant, InputTokens: 10000 + i*2000, OutputTokens: 1000,
				ToolCalls: []ToolCall{{Name: "bash", State: ToolStateCompleted}},
			}
		}
	}
	// Add some errors in later phases.
	msgs[21].ToolCalls = []ToolCall{{Name: "bash", State: ToolStateError}}
	msgs[25].ToolCalls = []ToolCall{{Name: "bash", State: ToolStateError}}
	msgs[27].ToolCalls = []ToolCall{{Name: "bash", State: ToolStateError}}

	compactions := CompactionSummary{
		TotalCompactions: 3,
		Events: []CompactionEvent{
			{BeforeMessageIdx: 9, AfterMessageIdx: 10, TokensLost: 50000},
			{BeforeMessageIdx: 19, AfterMessageIdx: 20, TokensLost: 40000},
			{BeforeMessageIdx: 24, AfterMessageIdx: 25, TokensLost: 30000},
		},
	}

	result := AnalyzeFreshness(msgs, compactions)

	if result.CompactionCount != 3 {
		t.Errorf("CompactionCount = %d, want 3", result.CompactionCount)
	}
	if len(result.Phases) != 4 {
		t.Fatalf("Phases = %d, want 4 (pre + 3 post-compaction)", len(result.Phases))
	}

	// Recommendation should mention multiple compactions.
	if result.Recommendation == "" {
		t.Error("Recommendation should not be empty")
	}
}

func TestAnalyzeFreshness_EmptySession(t *testing.T) {
	result := AnalyzeFreshness(nil, CompactionSummary{})
	if result.TotalMessages != 0 {
		t.Errorf("TotalMessages = %d, want 0", result.TotalMessages)
	}
	if len(result.Phases) != 0 {
		t.Errorf("Phases = %d, want 0", len(result.Phases))
	}
}

func TestAnalyzeFreshness_OptimalMessageIdx(t *testing.T) {
	// Build a session where early messages have high output ratio, later messages have low.
	msgs := make([]Message, 20)
	for i := range msgs {
		msgs[i] = Message{
			Role:         RoleAssistant,
			InputTokens:  10000,
			OutputTokens: 5000 - i*200, // decreasing output over time
		}
	}
	// First messages have 50% output ratio, last have ~10%.

	result := AnalyzeFreshness(msgs, CompactionSummary{})

	// Optimal should be in the first half where output ratio is highest.
	if result.OptimalMessageIdx == 0 {
		t.Error("OptimalMessageIdx should be > 0")
	}
	if result.OptimalMessageIdx > 10 {
		t.Errorf("OptimalMessageIdx = %d, expected early in session", result.OptimalMessageIdx)
	}
}

func TestAggregateFreshness_Basic(t *testing.T) {
	results := []SessionFreshness{
		{
			CompactionCount:   1,
			TotalMessages:     50,
			ErrorRateGrowth:   40,
			RetryRateGrowth:   20,
			OutputRatioDecay:  15,
			OptimalMessageIdx: 30,
			Phases: []FreshnessPhase{
				{Phase: 0, ErrorRate: 5, RetryRate: 2, OutputRatio: 20},
				{Phase: 1, ErrorRate: 10, RetryRate: 5, OutputRatio: 15},
			},
		},
		{
			CompactionCount:   2,
			TotalMessages:     80,
			ErrorRateGrowth:   60,
			RetryRateGrowth:   30,
			OutputRatioDecay:  25,
			OptimalMessageIdx: 40,
			Phases: []FreshnessPhase{
				{Phase: 0, ErrorRate: 3, RetryRate: 1, OutputRatio: 25},
				{Phase: 1, ErrorRate: 8, RetryRate: 4, OutputRatio: 18},
				{Phase: 2, ErrorRate: 15, RetryRate: 8, OutputRatio: 12},
			},
		},
		{
			CompactionCount:   0,
			TotalMessages:     20,
			OptimalMessageIdx: 10,
			Phases: []FreshnessPhase{
				{Phase: 0, ErrorRate: 2, RetryRate: 0, OutputRatio: 30},
			},
		},
	}

	agg := AggregateFreshness(results)

	if agg.TotalSessions != 3 {
		t.Errorf("TotalSessions = %d, want 3", agg.TotalSessions)
	}
	if agg.SessionsWithCompaction != 2 {
		t.Errorf("SessionsWithCompaction = %d, want 2", agg.SessionsWithCompaction)
	}
	if agg.AvgCompactionsPerSession < 1.4 || agg.AvgCompactionsPerSession > 1.6 {
		t.Errorf("AvgCompactionsPerSession = %.1f, want ~1.5", agg.AvgCompactionsPerSession)
	}
	// Avg error rate growth should be ~50 (avg of 40 and 60).
	if agg.AvgErrorRateGrowth < 45 || agg.AvgErrorRateGrowth > 55 {
		t.Errorf("AvgErrorRateGrowth = %.1f, want ~50", agg.AvgErrorRateGrowth)
	}
	// Should have depth stats.
	if len(agg.ByCompactionCount) == 0 {
		t.Error("ByCompactionCount should not be empty")
	}
	// Recommendation should be present.
	if agg.Recommendation == "" {
		t.Error("Recommendation should not be empty")
	}
}

func TestAggregateFreshness_Empty(t *testing.T) {
	agg := AggregateFreshness(nil)
	if agg.TotalSessions != 0 {
		t.Errorf("TotalSessions = %d, want 0", agg.TotalSessions)
	}
}

func TestAggregateFreshness_DepthStats(t *testing.T) {
	results := []SessionFreshness{
		{
			CompactionCount: 1,
			TotalMessages:   50,
			Phases: []FreshnessPhase{
				{Phase: 0, ErrorRate: 5, OutputRatio: 20, StartMsg: 0, EndMsg: 25},
				{Phase: 1, ErrorRate: 12, OutputRatio: 14, StartMsg: 25, EndMsg: 50},
			},
		},
		{
			CompactionCount: 1,
			TotalMessages:   40,
			Phases: []FreshnessPhase{
				{Phase: 0, ErrorRate: 3, OutputRatio: 22, StartMsg: 0, EndMsg: 20},
				{Phase: 1, ErrorRate: 8, OutputRatio: 16, StartMsg: 20, EndMsg: 40},
			},
		},
	}

	agg := AggregateFreshness(results)

	// Should have depth 0 and depth 1 stats.
	if len(agg.ByCompactionCount) < 2 {
		t.Fatalf("ByCompactionCount = %d, want >= 2", len(agg.ByCompactionCount))
	}

	// Depth 0 should have 2 sessions, error rate ~4%.
	depth0 := agg.ByCompactionCount[0]
	if depth0.Depth != 0 {
		t.Errorf("ByCompactionCount[0].Depth = %d, want 0", depth0.Depth)
	}
	if depth0.SessionCount != 2 {
		t.Errorf("Depth 0 SessionCount = %d, want 2", depth0.SessionCount)
	}
	if depth0.AvgErrorRate < 3.5 || depth0.AvgErrorRate > 4.5 {
		t.Errorf("Depth 0 AvgErrorRate = %.1f, want ~4", depth0.AvgErrorRate)
	}

	// Depth 1 should have 2 sessions, error rate ~10%.
	depth1 := agg.ByCompactionCount[1]
	if depth1.Depth != 1 {
		t.Errorf("ByCompactionCount[1].Depth = %d, want 1", depth1.Depth)
	}
	if depth1.AvgErrorRate < 9.0 || depth1.AvgErrorRate > 11.0 {
		t.Errorf("Depth 1 AvgErrorRate = %.1f, want ~10", depth1.AvgErrorRate)
	}
}

func TestFreshnessRecommendation_NoCompaction(t *testing.T) {
	r := buildFreshnessRecommendation(SessionFreshness{CompactionCount: 0, TotalMessages: 10})
	if r == "" {
		t.Error("Recommendation should not be empty")
	}
}

func TestFreshnessRecommendation_HighErrorGrowth(t *testing.T) {
	r := buildFreshnessRecommendation(SessionFreshness{
		CompactionCount: 1,
		ErrorRateGrowth: 60,
		Phases:          []FreshnessPhase{{Phase: 0, MessageCount: 25}},
	})
	if r == "" {
		t.Error("Recommendation should not be empty")
	}
}
