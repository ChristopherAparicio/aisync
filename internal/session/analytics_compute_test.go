package session

import (
	"math"
	"testing"
	"time"
)

// ─── Test fake for AnalyticsPricingLookup ───────────────────────────
// fakePricing is a tiny in-memory implementation of AnalyticsPricingLookup
// used throughout the tests. It supports per-model price lookup and fixed
// total/actual cost values. Zero-value fakePricing returns (zero, false) for
// every lookup, which exercises the "pricing nil or unknown model" branches.

type fakePricing struct {
	prices     map[string]AnalyticsModelPrice
	totalCost  float64 // returned by TotalCost regardless of session
	actualCost float64 // returned by ActualCost regardless of session
}

func (f *fakePricing) LookupPrice(model string) (AnalyticsModelPrice, bool) {
	if f == nil || f.prices == nil {
		return AnalyticsModelPrice{}, false
	}
	mp, ok := f.prices[model]
	return mp, ok
}

func (f *fakePricing) TotalCost(_ *Session) float64 {
	if f == nil {
		return 0
	}
	return f.totalCost
}

func (f *fakePricing) ActualCost(_ *Session) float64 {
	if f == nil {
		return 0
	}
	return f.actualCost
}

// sonnetPricing is a realistic fake preloaded with Claude Sonnet 4.
func sonnetPricing() *fakePricing {
	return &fakePricing{
		prices: map[string]AnalyticsModelPrice{
			"claude-sonnet-4": {
				MaxInputTokens: 200_000,
				InputPerMToken: 3.0, // $3 per 1M input tokens
			},
		},
		totalCost:  1.23,
		actualCost: 0.95,
	}
}

// ─── Tests ──────────────────────────────────────────────────────────

func TestComputeAnalytics_emptySession(t *testing.T) {
	sess := &Session{ID: "s1"}
	a := ComputeAnalytics(sess, nil, 0)

	if a.SessionID != "s1" {
		t.Errorf("SessionID: want s1, got %s", a.SessionID)
	}
	if a.SchemaVersion != AnalyticsSchemaVersion {
		t.Errorf("SchemaVersion: want %d, got %d", AnalyticsSchemaVersion, a.SchemaVersion)
	}
	if a.ComputedAt.IsZero() {
		t.Error("ComputedAt should be set")
	}
	if a.PeakInputTokens != 0 || a.PeakSaturationPct != 0 {
		t.Errorf("empty session should have zero peak data, got peak=%d sat=%.2f", a.PeakInputTokens, a.PeakSaturationPct)
	}
	if a.HasCompaction {
		t.Error("empty session must not report compaction")
	}
	// Rich structs should all be nil for an empty session (no messages to analyze).
	if a.WasteBreakdown != nil {
		t.Error("WasteBreakdown should be nil for empty session")
	}
	if a.Freshness != nil {
		t.Error("Freshness should be nil for empty session")
	}
	if a.Overload != nil {
		t.Error("Overload should be nil for empty session")
	}
	if a.ForecastInput != nil {
		t.Error("ForecastInput should be nil for empty session")
	}
}

func TestComputeAnalytics_schemaVersionStamped(t *testing.T) {
	sess := &Session{
		ID: "s1",
		Messages: []Message{
			{Role: RoleUser, InputTokens: 1000},
			{Role: RoleAssistant, InputTokens: 5000, Model: "claude-sonnet-4"},
		},
	}
	a := ComputeAnalytics(sess, nil, 0)
	if a.SchemaVersion != AnalyticsSchemaVersion {
		t.Errorf("SchemaVersion: want %d, got %d", AnalyticsSchemaVersion, a.SchemaVersion)
	}
	// ComputedAt should be within the last second.
	if time.Since(a.ComputedAt) > time.Second {
		t.Errorf("ComputedAt is stale: %v", a.ComputedAt)
	}
}

func TestComputeAnalytics_flatCacheFieldsFromTokenUsage(t *testing.T) {
	sess := &Session{
		ID: "s1",
		TokenUsage: TokenUsage{
			InputTokens: 123_456,
			CacheRead:   50_000,
			CacheWrite:  10_000,
			TotalTokens: 123_456,
		},
		Messages: []Message{
			{Role: RoleAssistant, InputTokens: 1000, Model: "claude-sonnet-4"},
		},
	}
	a := ComputeAnalytics(sess, nil, 0)
	if a.CacheReadTokens != 50_000 {
		t.Errorf("CacheReadTokens: want 50000, got %d", a.CacheReadTokens)
	}
	if a.CacheWriteTokens != 10_000 {
		t.Errorf("CacheWriteTokens: want 10000, got %d", a.CacheWriteTokens)
	}
	if a.InputTokens != 123_456 {
		t.Errorf("InputTokens: want 123456, got %d", a.InputTokens)
	}
}

func TestComputeAnalytics_peakAndDominantModelSkipsFirstMessage(t *testing.T) {
	// ContextSaturation and ComputeAnalytics both start the peak loop at i=1,
	// which deliberately skips message 0. We verify that behaviour here.
	sess := &Session{
		ID: "s1",
		Messages: []Message{
			// Message 0: huge token count — must be SKIPPED.
			{Role: RoleAssistant, InputTokens: 999_999, Model: "claude-sonnet-4"},
			// Message 1: actual peak we should detect.
			{Role: RoleAssistant, InputTokens: 50_000, Model: "claude-sonnet-4"},
			{Role: RoleAssistant, InputTokens: 30_000, Model: "claude-sonnet-4"},
		},
	}
	a := ComputeAnalytics(sess, nil, 0)
	if a.PeakInputTokens != 50_000 {
		t.Errorf("PeakInputTokens: want 50000 (first message skipped), got %d", a.PeakInputTokens)
	}
	if a.DominantModel != "claude-sonnet-4" {
		t.Errorf("DominantModel: want claude-sonnet-4, got %q", a.DominantModel)
	}
}

func TestComputeAnalytics_dominantModelPicksMostFrequentAssistant(t *testing.T) {
	sess := &Session{
		ID: "s1",
		Messages: []Message{
			{Role: RoleUser, InputTokens: 100},
			{Role: RoleAssistant, InputTokens: 10_000, Model: "gpt-4o"},
			{Role: RoleAssistant, InputTokens: 15_000, Model: "claude-sonnet-4"},
			{Role: RoleAssistant, InputTokens: 20_000, Model: "claude-sonnet-4"},
			{Role: RoleAssistant, InputTokens: 25_000, Model: "claude-sonnet-4"},
		},
	}
	a := ComputeAnalytics(sess, nil, 0)
	if a.DominantModel != "claude-sonnet-4" {
		t.Errorf("DominantModel: want claude-sonnet-4 (3 vs 1), got %q", a.DominantModel)
	}
}

func TestComputeAnalytics_peakSaturationWithPricing(t *testing.T) {
	sess := &Session{
		ID: "s1",
		Messages: []Message{
			{Role: RoleAssistant, InputTokens: 10_000, Model: "claude-sonnet-4"},
			{Role: RoleAssistant, InputTokens: 150_000, Model: "claude-sonnet-4"},
		},
	}
	a := ComputeAnalytics(sess, sonnetPricing(), 0)

	if a.MaxContextWindow != 200_000 {
		t.Errorf("MaxContextWindow: want 200000, got %d", a.MaxContextWindow)
	}
	// 150000 / 200000 = 75%
	wantSat := 75.0
	if math.Abs(a.PeakSaturationPct-wantSat) > 0.01 {
		t.Errorf("PeakSaturationPct: want %.2f, got %.2f", wantSat, a.PeakSaturationPct)
	}
}

func TestComputeAnalytics_peakSaturationCappedAt100(t *testing.T) {
	// Tokens briefly exceed the window just before compaction — must cap at 100.
	sess := &Session{
		ID: "s1",
		Messages: []Message{
			{Role: RoleAssistant, InputTokens: 10_000, Model: "claude-sonnet-4"},
			{Role: RoleAssistant, InputTokens: 210_000, Model: "claude-sonnet-4"},
		},
	}
	a := ComputeAnalytics(sess, sonnetPricing(), 0)
	if a.PeakSaturationPct != 100 {
		t.Errorf("PeakSaturationPct: want 100 (capped), got %.2f", a.PeakSaturationPct)
	}
}

func TestComputeAnalytics_noSaturationWithoutPricing(t *testing.T) {
	sess := &Session{
		ID: "s1",
		Messages: []Message{
			{Role: RoleAssistant, InputTokens: 10_000, Model: "some-unknown-model"},
			{Role: RoleAssistant, InputTokens: 50_000, Model: "some-unknown-model"},
		},
	}
	a := ComputeAnalytics(sess, sonnetPricing(), 0)
	if a.MaxContextWindow != 0 {
		t.Errorf("MaxContextWindow should be 0 for unknown model, got %d", a.MaxContextWindow)
	}
	if a.PeakSaturationPct != 0 {
		t.Errorf("PeakSaturationPct should be 0 without context window, got %.2f", a.PeakSaturationPct)
	}
}

func TestComputeAnalytics_compactionDetected(t *testing.T) {
	// Same sawtooth pattern as compaction_test.go, validated against
	// DetectCompactions directly.
	sess := &Session{
		ID:       "s1",
		Messages: msgs(20_000, 50_000, 100_000, 12_000, 40_000, 90_000, 8_000, 25_000),
	}
	// Set all messages to same model so DominantModel is set (enables pricing lookup).
	for i := range sess.Messages {
		sess.Messages[i].Model = "claude-sonnet-4"
	}
	a := ComputeAnalytics(sess, sonnetPricing(), 0)

	if !a.HasCompaction {
		t.Error("HasCompaction should be true")
	}
	if a.CompactionCount != 2 {
		t.Errorf("CompactionCount: want 2, got %d", a.CompactionCount)
	}
	if a.CompactionWastedTokens == 0 {
		t.Error("CompactionWastedTokens should be > 0")
	}
	if a.CompactionDropPct == 0 {
		t.Error("CompactionDropPct should be > 0 when compaction detected")
	}
}

func TestComputeAnalytics_cacheMissGapDetection(t *testing.T) {
	// Build a session where the gap between two consecutive assistant
	// messages exceeds the 5-minute cache TTL. The second assistant message
	// should be counted as a cache miss and its InputTokens added to
	// CacheWastedTokens.
	base := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	sess := &Session{
		ID: "s1",
		Messages: []Message{
			{Role: RoleUser, Timestamp: base, InputTokens: 100},
			{Role: RoleAssistant, Timestamp: base.Add(10 * time.Second), InputTokens: 5_000, Model: "claude-sonnet-4"},
			// 10-minute gap → cache miss
			{Role: RoleAssistant, Timestamp: base.Add(10 * time.Minute), InputTokens: 7_500, Model: "claude-sonnet-4"},
		},
	}
	a := ComputeAnalytics(sess, nil, 0)

	if a.CacheMissCount != 1 {
		t.Errorf("CacheMissCount: want 1, got %d", a.CacheMissCount)
	}
	if a.CacheWastedTokens != 7_500 {
		t.Errorf("CacheWastedTokens: want 7500, got %d", a.CacheWastedTokens)
	}
	if a.LongestGapMins < 9 || a.LongestGapMins > 11 {
		t.Errorf("LongestGapMins: want ~10, got %d", a.LongestGapMins)
	}
	if a.SessionAvgGapMins <= 0 {
		t.Errorf("SessionAvgGapMins should be > 0, got %.2f", a.SessionAvgGapMins)
	}
}

func TestComputeAnalytics_noCacheMissForGapUnderTTL(t *testing.T) {
	base := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	sess := &Session{
		ID: "s1",
		Messages: []Message{
			{Role: RoleAssistant, Timestamp: base, InputTokens: 5_000, Model: "claude-sonnet-4"},
			{Role: RoleAssistant, Timestamp: base.Add(2 * time.Minute), InputTokens: 7_500, Model: "claude-sonnet-4"},
			{Role: RoleAssistant, Timestamp: base.Add(4 * time.Minute), InputTokens: 9_000, Model: "claude-sonnet-4"},
		},
	}
	a := ComputeAnalytics(sess, nil, 0)

	if a.CacheMissCount != 0 {
		t.Errorf("CacheMissCount: want 0 (all gaps <5min), got %d", a.CacheMissCount)
	}
	if a.CacheWastedTokens != 0 {
		t.Errorf("CacheWastedTokens: want 0, got %d", a.CacheWastedTokens)
	}
}

func TestComputeAnalytics_backendFromFirstProviderID(t *testing.T) {
	sess := &Session{
		ID: "s1",
		Messages: []Message{
			{Role: RoleUser, InputTokens: 100},
			{Role: RoleAssistant, InputTokens: 5_000, Model: "claude-sonnet-4", ProviderID: "anthropic"},
			{Role: RoleAssistant, InputTokens: 6_000, Model: "claude-sonnet-4", ProviderID: "anthropic"},
		},
	}
	a := ComputeAnalytics(sess, nil, 0)
	if a.Backend != "anthropic" {
		t.Errorf("Backend: want anthropic, got %q", a.Backend)
	}
}

func TestComputeAnalytics_backendEmptyWhenNoProviderID(t *testing.T) {
	sess := &Session{
		ID: "s1",
		Messages: []Message{
			{Role: RoleAssistant, InputTokens: 5_000, Model: "claude-sonnet-4"},
		},
	}
	a := ComputeAnalytics(sess, nil, 0)
	if a.Backend != "" {
		t.Errorf("Backend: want empty, got %q", a.Backend)
	}
}

func TestComputeAnalytics_forkDeduplicationProportional(t *testing.T) {
	// Fork at message 2. Shared prefix = messages [0, 1] = 10K + 20K = 30K tokens.
	// Total = 30K + 40K + 50K = 120K. Shared fraction = 30/120 = 25%.
	// EstimatedCost = $10 → DeduplicatedCost = $10 - $10*0.25 = $7.50.
	sess := &Session{
		ID:            "s1",
		EstimatedCost: 10.00,
		TokenUsage:    TokenUsage{TotalTokens: 120_000},
		Messages: []Message{
			{Role: RoleUser, InputTokens: 10_000},
			{Role: RoleAssistant, InputTokens: 20_000, OutputTokens: 0, Model: "claude-sonnet-4"},
			{Role: RoleAssistant, InputTokens: 40_000, OutputTokens: 0, Model: "claude-sonnet-4"},
			{Role: RoleAssistant, InputTokens: 50_000, OutputTokens: 0, Model: "claude-sonnet-4"},
		},
	}
	a := ComputeAnalytics(sess, nil, 2)

	if a.ForkOffset != 2 {
		t.Errorf("ForkOffset: want 2, got %d", a.ForkOffset)
	}
	if math.Abs(a.DeduplicatedCost-7.50) > 0.001 {
		t.Errorf("DeduplicatedCost: want 7.50, got %.4f", a.DeduplicatedCost)
	}
}

func TestComputeAnalytics_noForkMeansDedupEqualsEstimated(t *testing.T) {
	sess := &Session{
		ID:            "s1",
		EstimatedCost: 5.00,
		TokenUsage:    TokenUsage{TotalTokens: 100_000},
		Messages: []Message{
			{Role: RoleAssistant, InputTokens: 50_000, Model: "claude-sonnet-4"},
			{Role: RoleAssistant, InputTokens: 60_000, Model: "claude-sonnet-4"},
		},
	}
	a := ComputeAnalytics(sess, nil, 0)

	if a.ForkOffset != 0 {
		t.Errorf("ForkOffset: want 0, got %d", a.ForkOffset)
	}
	if math.Abs(a.DeduplicatedCost-5.00) > 0.001 {
		t.Errorf("DeduplicatedCost: want 5.00 (= EstimatedCost), got %.4f", a.DeduplicatedCost)
	}
}

func TestComputeAnalytics_forkWithZeroSharedTokensFallsBackToEstimated(t *testing.T) {
	sess := &Session{
		ID:            "s1",
		EstimatedCost: 8.00,
		TokenUsage:    TokenUsage{TotalTokens: 50_000},
		Messages: []Message{
			// Shared prefix has zero tokens — defensive case.
			{Role: RoleUser, InputTokens: 0, OutputTokens: 0},
			{Role: RoleAssistant, InputTokens: 50_000, OutputTokens: 0, Model: "claude-sonnet-4"},
		},
	}
	a := ComputeAnalytics(sess, nil, 1)

	if a.ForkOffset != 1 {
		t.Errorf("ForkOffset: want 1, got %d", a.ForkOffset)
	}
	if math.Abs(a.DeduplicatedCost-8.00) > 0.001 {
		t.Errorf("DeduplicatedCost: want 8.00 (fallback), got %.4f", a.DeduplicatedCost)
	}
}

func TestComputeAnalytics_richStructsPopulatedForRealisticSession(t *testing.T) {
	// A minimally realistic session: user + assistant + cache write on first
	// assistant message (so SystemPromptEstimate returns > 0), plus a tool call
	// error to exercise the retry counting logic.
	base := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	sess := &Session{
		ID:          "s1",
		SessionType: "feature",
		CreatedAt:   base,
		TokenUsage:  TokenUsage{TotalTokens: 100_000, InputTokens: 80_000, OutputTokens: 20_000},
		Messages: []Message{
			{Role: RoleUser, Timestamp: base, Content: "write a function", InputTokens: 100},
			{
				Role:             RoleAssistant,
				Timestamp:        base.Add(10 * time.Second),
				InputTokens:      15_000,
				OutputTokens:     2_000,
				CacheWriteTokens: 12_000, // triggers SystemPromptEstimate > 0
				Model:            "claude-sonnet-4",
				ProviderID:       "anthropic",
				ToolCalls:        []ToolCall{{Name: "bash", State: ToolStateError}},
			},
			{
				Role:         RoleAssistant,
				Timestamp:    base.Add(20 * time.Second),
				InputTokens:  25_000,
				OutputTokens: 3_000,
				Model:        "claude-sonnet-4",
				ProviderID:   "anthropic",
				ToolCalls:    []ToolCall{{Name: "edit", State: ToolStateCompleted}},
			},
		},
	}

	a := ComputeAnalytics(sess, sonnetPricing(), 0)

	// All 6 rich-struct pointers should be non-nil for this session.
	if a.WasteBreakdown == nil {
		t.Error("WasteBreakdown should be non-nil")
	}
	if a.Freshness == nil {
		t.Error("Freshness should be non-nil")
	}
	if a.Overload == nil {
		t.Error("Overload should be non-nil")
	}
	if a.PromptData == nil {
		t.Fatal("PromptData should be non-nil (CacheWriteTokens=12000 on first assistant)")
	}
	if a.FitnessData == nil {
		t.Fatal("FitnessData should be non-nil (has dominantModel + sessionType)")
	}
	if a.ForecastInput == nil {
		t.Fatal("ForecastInput should be non-nil")
	}

	// PromptData sanity: PromptTokens must be > 0 because CacheWriteTokens is set.
	if a.PromptData.PromptTokens <= 0 {
		t.Errorf("PromptData.PromptTokens: want >0, got %d", a.PromptData.PromptTokens)
	}
	if a.PromptData.CreatedAt != sess.CreatedAt.Unix() {
		t.Errorf("PromptData.CreatedAt: want %d, got %d", sess.CreatedAt.Unix(), a.PromptData.CreatedAt)
	}
	if a.PromptData.TotalInput <= 0 {
		t.Errorf("PromptData.TotalInput: want >0, got %d", a.PromptData.TotalInput)
	}

	// FitnessData sanity.
	if a.FitnessData.Model != "claude-sonnet-4" {
		t.Errorf("FitnessData.Model: want claude-sonnet-4, got %q", a.FitnessData.Model)
	}
	if a.FitnessData.SessionType != "feature" {
		t.Errorf("FitnessData.SessionType: want feature, got %q", a.FitnessData.SessionType)
	}
	if a.FitnessData.ToolCalls != 2 {
		t.Errorf("FitnessData.ToolCalls: want 2, got %d", a.FitnessData.ToolCalls)
	}
	if a.FitnessData.ToolErrors != 1 {
		t.Errorf("FitnessData.ToolErrors: want 1, got %d", a.FitnessData.ToolErrors)
	}
	if a.FitnessData.MessageCount != 3 {
		t.Errorf("FitnessData.MessageCount: want 3, got %d", a.FitnessData.MessageCount)
	}
	// EstimatedCost comes from the pricing fake's TotalCost.
	if math.Abs(a.FitnessData.EstimatedCost-1.23) > 0.001 {
		t.Errorf("FitnessData.EstimatedCost: want 1.23, got %.4f", a.FitnessData.EstimatedCost)
	}

	// ForecastInput sanity.
	if a.ForecastInput.Model != "claude-sonnet-4" {
		t.Errorf("ForecastInput.Model: want claude-sonnet-4, got %q", a.ForecastInput.Model)
	}
	if a.ForecastInput.MaxInputTokens != 200_000 {
		t.Errorf("ForecastInput.MaxInputTokens: want 200000, got %d", a.ForecastInput.MaxInputTokens)
	}
	if a.ForecastInput.MessageCount != 3 {
		t.Errorf("ForecastInput.MessageCount: want 3, got %d", a.ForecastInput.MessageCount)
	}
	// Peak is based on messages after index 0, so 25000.
	if a.ForecastInput.PeakInputTokens != 25_000 {
		t.Errorf("ForecastInput.PeakInputTokens: want 25000, got %d", a.ForecastInput.PeakInputTokens)
	}

	// TotalWastedTokens is the sum of the four waste categories from the breakdown.
	if a.TotalWastedTokens < 0 {
		t.Errorf("TotalWastedTokens should be >=0, got %d", a.TotalWastedTokens)
	}
}

func TestComputeAnalytics_promptDataNilWhenNoSystemPromptEstimate(t *testing.T) {
	// No CacheWriteTokens and no usable user content → SystemPromptEstimate returns 0.
	sess := &Session{
		ID: "s1",
		Messages: []Message{
			{Role: RoleAssistant, InputTokens: 100, Model: "claude-sonnet-4"}, // no user msg, no cache write
		},
	}
	a := ComputeAnalytics(sess, nil, 0)
	if a.PromptData != nil {
		t.Errorf("PromptData should be nil when SystemPromptEstimate returns 0, got %+v", a.PromptData)
	}
}

func TestComputeAnalytics_fitnessDataNilWhenNoSessionType(t *testing.T) {
	sess := &Session{
		ID:          "s1",
		SessionType: "", // empty — fitness should not be populated
		Messages: []Message{
			{Role: RoleAssistant, InputTokens: 10_000, Model: "claude-sonnet-4"},
			{Role: RoleAssistant, InputTokens: 15_000, Model: "claude-sonnet-4"},
		},
	}
	a := ComputeAnalytics(sess, sonnetPricing(), 0)
	if a.FitnessData != nil {
		t.Errorf("FitnessData should be nil when SessionType is empty, got %+v", a.FitnessData)
	}
}

func TestComputeAnalytics_fitnessDataNilWhenNoDominantModel(t *testing.T) {
	sess := &Session{
		ID:          "s1",
		SessionType: "feature",
		Messages: []Message{
			// Only user messages — no assistant, so no dominant model.
			{Role: RoleUser, InputTokens: 100},
			{Role: RoleUser, InputTokens: 200},
		},
	}
	a := ComputeAnalytics(sess, sonnetPricing(), 0)
	if a.FitnessData != nil {
		t.Errorf("FitnessData should be nil when dominant model is empty, got %+v", a.FitnessData)
	}
}

func TestComputeAnalytics_agentUsageSingleRow(t *testing.T) {
	sess := &Session{
		ID:         "s1",
		Agent:      "coder",
		TokenUsage: TokenUsage{TotalTokens: 12_345},
		Messages: []Message{
			{Role: RoleAssistant, InputTokens: 1000, Model: "claude-sonnet-4",
				ToolCalls: []ToolCall{
					{Name: "bash", State: ToolStateError},
					{Name: "edit", State: ToolStateCompleted},
				},
			},
			{Role: RoleAssistant, InputTokens: 2000, Model: "claude-sonnet-4",
				ToolCalls: []ToolCall{{Name: "bash", State: ToolStateError}},
			},
		},
		EstimatedCost: 2.50,
	}
	a := ComputeAnalytics(sess, nil, 0)

	if len(a.AgentUsage) != 1 {
		t.Fatalf("AgentUsage: want 1 row, got %d", len(a.AgentUsage))
	}
	row := a.AgentUsage[0]
	if row.AgentName != "coder" {
		t.Errorf("AgentName: want coder, got %q", row.AgentName)
	}
	if row.Invocations != 2 {
		t.Errorf("Invocations: want 2 (messages), got %d", row.Invocations)
	}
	if row.Tokens != 12_345 {
		t.Errorf("Tokens: want 12345, got %d", row.Tokens)
	}
	if math.Abs(row.Cost-2.50) > 0.001 {
		t.Errorf("Cost: want 2.50, got %.4f", row.Cost)
	}
	if row.Errors != 2 {
		t.Errorf("Errors: want 2, got %d", row.Errors)
	}

	// Scalar rollups.
	if a.UniqueAgentsUsed != 1 {
		t.Errorf("UniqueAgentsUsed: want 1, got %d", a.UniqueAgentsUsed)
	}
	if a.TotalAgentInvocations != 2 {
		t.Errorf("TotalAgentInvocations: want 2, got %d", a.TotalAgentInvocations)
	}
	if a.AgentTokens != 12_345 {
		t.Errorf("AgentTokens: want 12345, got %d", a.AgentTokens)
	}
	if math.Abs(a.AgentCost-2.50) > 0.001 {
		t.Errorf("AgentCost: want 2.50, got %.4f", a.AgentCost)
	}
}

func TestComputeAnalytics_agentUsageUnknownFallback(t *testing.T) {
	sess := &Session{
		ID:    "s1",
		Agent: "", // empty → fallback to "unknown"
		Messages: []Message{
			{Role: RoleAssistant, InputTokens: 100, Model: "claude-sonnet-4"},
		},
	}
	a := ComputeAnalytics(sess, nil, 0)

	if len(a.AgentUsage) != 1 {
		t.Fatalf("AgentUsage: want 1 row, got %d", len(a.AgentUsage))
	}
	if a.AgentUsage[0].AgentName != "unknown" {
		t.Errorf("AgentName: want unknown, got %q", a.AgentUsage[0].AgentName)
	}
}

func TestComputeAnalytics_estimatedAndActualCostCopiedFromSession(t *testing.T) {
	sess := &Session{
		ID:            "s1",
		EstimatedCost: 9.99,
		ActualCost:    7.77,
		Messages: []Message{
			{Role: RoleAssistant, InputTokens: 1000, Model: "claude-sonnet-4"},
		},
	}
	a := ComputeAnalytics(sess, nil, 0)
	if math.Abs(a.EstimatedCost-9.99) > 0.001 {
		t.Errorf("EstimatedCost: want 9.99, got %.4f", a.EstimatedCost)
	}
	if math.Abs(a.ActualCost-7.77) > 0.001 {
		t.Errorf("ActualCost: want 7.77, got %.4f", a.ActualCost)
	}
}

func TestComputeAnalytics_tokenGrowthPerMessage(t *testing.T) {
	// Messages with growing input tokens should produce a positive token growth rate.
	// Deltas: 10K→20K (+10K), 20K→35K (+15K), 35K→55K (+20K). Avg = 15K.
	sess := &Session{
		ID: "s1",
		Messages: []Message{
			{Role: RoleAssistant, InputTokens: 10_000, Model: "claude-sonnet-4"},
			{Role: RoleAssistant, InputTokens: 20_000, Model: "claude-sonnet-4"},
			{Role: RoleAssistant, InputTokens: 35_000, Model: "claude-sonnet-4"},
			{Role: RoleAssistant, InputTokens: 55_000, Model: "claude-sonnet-4"},
		},
	}
	a := ComputeAnalytics(sess, sonnetPricing(), 0)

	if a.ForecastInput == nil {
		t.Fatal("ForecastInput should be non-nil")
	}
	if a.ForecastInput.TokenGrowthPerMsg != 15_000 {
		t.Errorf("TokenGrowthPerMsg: want 15000, got %d", a.ForecastInput.TokenGrowthPerMsg)
	}
}

func TestComputeAnalytics_forecastMsgAtFirstCompaction(t *testing.T) {
	sess := &Session{
		ID:       "s1",
		Messages: msgs(20_000, 50_000, 100_000, 12_000, 40_000),
	}
	for i := range sess.Messages {
		sess.Messages[i].Model = "claude-sonnet-4"
	}
	a := ComputeAnalytics(sess, sonnetPricing(), 0)

	if a.ForecastInput == nil {
		t.Fatal("ForecastInput should be non-nil")
	}
	// First compaction: 100K → 12K at message idx 2 → 3. BeforeMessageIdx = 2.
	if a.ForecastInput.MsgAtFirstCompaction != 2 {
		t.Errorf("MsgAtFirstCompaction: want 2, got %d", a.ForecastInput.MsgAtFirstCompaction)
	}
}

func TestComputeAnalytics_doesNotMutateSession(t *testing.T) {
	// ComputeAnalytics must be pure — verify it does not mutate the session.
	sess := &Session{
		ID:            "s1",
		EstimatedCost: 1.00,
		Agent:         "coder",
		TokenUsage:    TokenUsage{TotalTokens: 10_000, InputTokens: 8_000, CacheRead: 4_000},
		Messages: []Message{
			{Role: RoleUser, InputTokens: 100},
			{Role: RoleAssistant, InputTokens: 5_000, Model: "claude-sonnet-4"},
		},
	}

	// Snapshot a few fields we care about.
	origCost := sess.EstimatedCost
	origAgent := sess.Agent
	origMsgCount := len(sess.Messages)
	origTotalTokens := sess.TokenUsage.TotalTokens
	origFirstInput := sess.Messages[0].InputTokens

	_ = ComputeAnalytics(sess, sonnetPricing(), 0)

	if sess.EstimatedCost != origCost {
		t.Errorf("EstimatedCost mutated: %v → %v", origCost, sess.EstimatedCost)
	}
	if sess.Agent != origAgent {
		t.Errorf("Agent mutated: %v → %v", origAgent, sess.Agent)
	}
	if len(sess.Messages) != origMsgCount {
		t.Errorf("Messages len mutated: %d → %d", origMsgCount, len(sess.Messages))
	}
	if sess.TokenUsage.TotalTokens != origTotalTokens {
		t.Errorf("TokenUsage.TotalTokens mutated")
	}
	if sess.Messages[0].InputTokens != origFirstInput {
		t.Errorf("Messages[0].InputTokens mutated")
	}
}
