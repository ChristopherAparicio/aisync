package session

import (
	"testing"
)

func TestSystemPromptEstimate_EmptyMessages(t *testing.T) {
	got := SystemPromptEstimate(nil)
	if got != 0 {
		t.Errorf("SystemPromptEstimate(nil) = %d, want 0", got)
	}
}

func TestSystemPromptEstimate_NoAssistant(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "hello", InputTokens: 5000},
	}
	got := SystemPromptEstimate(msgs)
	if got != 0 {
		t.Errorf("SystemPromptEstimate = %d, want 0 (no assistant msg)", got)
	}
}

func TestSystemPromptEstimate_MethodA_CacheWrite(t *testing.T) {
	// Method A: CacheWriteTokens on the first assistant message.
	msgs := []Message{
		{Role: RoleUser, Content: "Implement a login form", InputTokens: 500},
		{Role: RoleAssistant, InputTokens: 12000, OutputTokens: 1000, CacheWriteTokens: 10000},
	}
	got := SystemPromptEstimate(msgs)

	// "Implement a login form" → ~22 chars / 4 = 5 tokens for user content
	// Expected: 10000 - 5 = 9995
	userEstimate := roughTokenEstimate("Implement a login form")
	want := 10000 - userEstimate
	if got != want {
		t.Errorf("SystemPromptEstimate (MethodA) = %d, want %d", got, want)
	}
}

func TestSystemPromptEstimate_MethodA_CacheWriteSmallerThanUser(t *testing.T) {
	// Edge case: CacheWriteTokens < user estimate (shouldn't happen but protect).
	msgs := []Message{
		{Role: RoleUser, Content: "A really long user message that somehow exceeds the cache write tokens in this contrived test case scenario which should not normally happen in production", InputTokens: 500},
		{Role: RoleAssistant, InputTokens: 12000, OutputTokens: 1000, CacheWriteTokens: 10},
	}
	got := SystemPromptEstimate(msgs)
	// When estimate would be negative, fallback to raw CacheWriteTokens.
	if got != 10 {
		t.Errorf("SystemPromptEstimate = %d, want 10 (fallback to CacheWriteTokens)", got)
	}
}

func TestSystemPromptEstimate_MethodB_InputMinusUser(t *testing.T) {
	// Method B: No CacheWriteTokens, use InputTokens - userEstimate.
	msgs := []Message{
		{Role: RoleUser, Content: "Fix the bug", InputTokens: 500},
		{Role: RoleAssistant, InputTokens: 8000, OutputTokens: 1000, CacheWriteTokens: 0},
	}
	got := SystemPromptEstimate(msgs)

	userEstimate := roughTokenEstimate("Fix the bug")
	want := 8000 - userEstimate
	if got != want {
		t.Errorf("SystemPromptEstimate (MethodB) = %d, want %d", got, want)
	}
}

func TestSystemPromptEstimate_MethodB_TooSmall(t *testing.T) {
	// Method B: result < 500 → return 0 (sanity check).
	msgs := []Message{
		{Role: RoleUser, Content: "hello world", InputTokens: 500},
		{Role: RoleAssistant, InputTokens: 600, OutputTokens: 100, CacheWriteTokens: 0},
	}
	got := SystemPromptEstimate(msgs)
	// 600 - 2 ("hello world" = 11 chars / 4 = 2) = 598 which is > 500, so actually passes.
	// Let's use a case where it's definitely < 500.
	msgs2 := []Message{
		{Role: RoleUser, Content: "A relatively long message that when subtracted leaves very little room for a system prompt", InputTokens: 500},
		{Role: RoleAssistant, InputTokens: 520, OutputTokens: 100, CacheWriteTokens: 0},
	}
	got2 := SystemPromptEstimate(msgs2)
	// 520 - 22 = 498 < 500 → return 0
	if got2 != 0 {
		t.Errorf("SystemPromptEstimate (MethodB too small) = %d, want 0", got2)
	}
	_ = got // suppress unused
}

func TestSystemPromptEstimate_Fallback_RawInput(t *testing.T) {
	// No user messages before assistant, CacheWriteTokens=0.
	msgs := []Message{
		{Role: RoleAssistant, InputTokens: 6000, OutputTokens: 500, CacheWriteTokens: 0},
	}
	got := SystemPromptEstimate(msgs)
	if got != 6000 {
		t.Errorf("SystemPromptEstimate (fallback) = %d, want 6000", got)
	}
}

func TestSystemPromptEstimate_Fallback_TooSmall(t *testing.T) {
	// Raw InputTokens <= 500 → return 0.
	msgs := []Message{
		{Role: RoleAssistant, InputTokens: 300, OutputTokens: 100, CacheWriteTokens: 0},
	}
	got := SystemPromptEstimate(msgs)
	if got != 0 {
		t.Errorf("SystemPromptEstimate (fallback too small) = %d, want 0", got)
	}
}

func TestRoughTokenEstimate(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"abcd", 1},        // 4 chars / 4
		{"hello world", 2}, // 11 chars / 4 = 2
		{"a", 0},           // 1 char / 4 = 0
	}
	for _, tt := range tests {
		got := roughTokenEstimate(tt.input)
		if got != tt.want {
			t.Errorf("roughTokenEstimate(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

// --- AnalyzeSystemPromptImpact tests ---

func TestAnalyzeSystemPromptImpact_Empty(t *testing.T) {
	result := AnalyzeSystemPromptImpact(nil)
	if result.TotalSessions != 0 {
		t.Errorf("TotalSessions = %d, want 0", result.TotalSessions)
	}
	if result.Recommendation == "" {
		t.Error("Recommendation should not be empty for nil input")
	}
}

func TestAnalyzeSystemPromptImpact_AllZero(t *testing.T) {
	data := []SessionPromptData{
		{PromptTokens: 0, TotalInput: 10000},
		{PromptTokens: 0, TotalInput: 20000},
	}
	result := AnalyzeSystemPromptImpact(data)
	if result.TotalSessions != 0 {
		t.Errorf("TotalSessions = %d, want 0 (all zero prompts filtered)", result.TotalSessions)
	}
}

func TestAnalyzeSystemPromptImpact_BasicStats(t *testing.T) {
	data := []SessionPromptData{
		{PromptTokens: 2000, TotalInput: 50000, ErrorRate: 5, RetryRate: 2, CreatedAt: 1000},
		{PromptTokens: 4000, TotalInput: 60000, ErrorRate: 8, RetryRate: 4, CreatedAt: 2000},
		{PromptTokens: 6000, TotalInput: 80000, ErrorRate: 3, RetryRate: 1, CreatedAt: 3000},
	}
	result := AnalyzeSystemPromptImpact(data)

	if result.TotalSessions != 3 {
		t.Errorf("TotalSessions = %d, want 3", result.TotalSessions)
	}
	if result.AvgEstimate != 4000 { // (2000+4000+6000)/3
		t.Errorf("AvgEstimate = %d, want 4000", result.AvgEstimate)
	}
	if result.MinEstimate != 2000 {
		t.Errorf("MinEstimate = %d, want 2000", result.MinEstimate)
	}
	if result.MaxEstimate != 6000 {
		t.Errorf("MaxEstimate = %d, want 6000", result.MaxEstimate)
	}
	if result.MedianEst != 4000 {
		t.Errorf("MedianEst = %d, want 4000", result.MedianEst)
	}
	if result.TotalPromptTokens != 12000 {
		t.Errorf("TotalPromptTokens = %d, want 12000", result.TotalPromptTokens)
	}
}

func TestAnalyzeSystemPromptImpact_SizeBuckets(t *testing.T) {
	data := []SessionPromptData{
		{PromptTokens: 1000, TotalInput: 50000, ErrorRate: 10, RetryRate: 5, CreatedAt: 1},   // small
		{PromptTokens: 2500, TotalInput: 50000, ErrorRate: 8, RetryRate: 3, CreatedAt: 2},    // small
		{PromptTokens: 5000, TotalInput: 80000, ErrorRate: 6, RetryRate: 4, CreatedAt: 3},    // medium
		{PromptTokens: 7000, TotalInput: 90000, ErrorRate: 4, RetryRate: 2, CreatedAt: 4},    // medium
		{PromptTokens: 10000, TotalInput: 100000, ErrorRate: 12, RetryRate: 8, CreatedAt: 5}, // large
	}
	result := AnalyzeSystemPromptImpact(data)

	if result.SmallCount != 2 {
		t.Errorf("SmallCount = %d, want 2", result.SmallCount)
	}
	if result.MediumCount != 2 {
		t.Errorf("MediumCount = %d, want 2", result.MediumCount)
	}
	if result.LargeCount != 1 {
		t.Errorf("LargeCount = %d, want 1", result.LargeCount)
	}

	// Small error rate: (10+8)/2 = 9
	wantSmallErr := 9.0
	if result.SmallErrorRate != wantSmallErr {
		t.Errorf("SmallErrorRate = %.1f, want %.1f", result.SmallErrorRate, wantSmallErr)
	}
	// Medium error rate: (6+4)/2 = 5
	wantMedErr := 5.0
	if result.MediumErrorRate != wantMedErr {
		t.Errorf("MediumErrorRate = %.1f, want %.1f", result.MediumErrorRate, wantMedErr)
	}
	// Large error rate: 12
	if result.LargeErrorRate != 12.0 {
		t.Errorf("LargeErrorRate = %.1f, want 12.0", result.LargeErrorRate)
	}
}

func TestAnalyzeSystemPromptImpact_CostPct(t *testing.T) {
	data := []SessionPromptData{
		{PromptTokens: 5000, TotalInput: 50000, CreatedAt: 1},  // 10%
		{PromptTokens: 10000, TotalInput: 50000, CreatedAt: 2}, // 20%
	}
	result := AnalyzeSystemPromptImpact(data)

	wantPct := 15.0 // (10+20)/2
	if result.AvgPromptCostPct != wantPct {
		t.Errorf("AvgPromptCostPct = %.1f, want %.1f", result.AvgPromptCostPct, wantPct)
	}
}

// --- Trend tests ---

func TestComputePromptTrend_Growing(t *testing.T) {
	data := []SessionPromptData{
		{PromptTokens: 1000, CreatedAt: 1},
		{PromptTokens: 1200, CreatedAt: 2},
		{PromptTokens: 2000, CreatedAt: 3},
		{PromptTokens: 2500, CreatedAt: 4},
		{PromptTokens: 3000, CreatedAt: 5},
		{PromptTokens: 3200, CreatedAt: 6},
		{PromptTokens: 4000, CreatedAt: 7},
		{PromptTokens: 5000, CreatedAt: 8},
	}
	got := computePromptTrend(data)
	if got != "growing" {
		t.Errorf("computePromptTrend = %q, want %q", got, "growing")
	}
}

func TestComputePromptTrend_Stable(t *testing.T) {
	data := []SessionPromptData{
		{PromptTokens: 5000, CreatedAt: 1},
		{PromptTokens: 5100, CreatedAt: 2},
		{PromptTokens: 4900, CreatedAt: 3},
		{PromptTokens: 5000, CreatedAt: 4},
		{PromptTokens: 5200, CreatedAt: 5},
		{PromptTokens: 4800, CreatedAt: 6},
		{PromptTokens: 5000, CreatedAt: 7},
		{PromptTokens: 5100, CreatedAt: 8},
	}
	got := computePromptTrend(data)
	if got != "stable" {
		t.Errorf("computePromptTrend = %q, want %q", got, "stable")
	}
}

func TestComputePromptTrend_Shrinking(t *testing.T) {
	data := []SessionPromptData{
		{PromptTokens: 8000, CreatedAt: 1},
		{PromptTokens: 7500, CreatedAt: 2},
		{PromptTokens: 6000, CreatedAt: 3},
		{PromptTokens: 5000, CreatedAt: 4},
		{PromptTokens: 4000, CreatedAt: 5},
		{PromptTokens: 3500, CreatedAt: 6},
		{PromptTokens: 3000, CreatedAt: 7},
		{PromptTokens: 2000, CreatedAt: 8},
	}
	got := computePromptTrend(data)
	if got != "shrinking" {
		t.Errorf("computePromptTrend = %q, want %q", got, "shrinking")
	}
}

func TestComputePromptTrend_TooFewSessions(t *testing.T) {
	data := []SessionPromptData{
		{PromptTokens: 1000},
		{PromptTokens: 5000},
	}
	got := computePromptTrend(data)
	if got != "stable" {
		t.Errorf("computePromptTrend(2 items) = %q, want %q", got, "stable")
	}
}

// --- Recommendation tests ---

func TestBuildPromptRecommendation_LargeWithHighErrors(t *testing.T) {
	impact := SystemPromptImpact{
		TotalSessions:  10,
		LargeCount:     3,
		SmallCount:     4,
		SmallErrorRate: 5.0,
		LargeErrorRate: 8.0, // 8 > 5*1.2=6 → triggers large-prompt warning
		AvgEstimate:    6000,
	}
	got := buildPromptRecommendation(impact)
	if got == "" {
		t.Error("recommendation should not be empty")
	}
	// Should mention trimming or reducing.
	if !containsAll(got, "large system prompts", "error rate") {
		t.Errorf("recommendation doesn't mention large prompts + error rate: %s", got)
	}
}

func TestBuildPromptRecommendation_GrowingTrend(t *testing.T) {
	impact := SystemPromptImpact{
		TotalSessions: 10,
		Trend:         "growing",
		AvgEstimate:   6000,
	}
	got := buildPromptRecommendation(impact)
	if !containsAll(got, "growing") {
		t.Errorf("recommendation doesn't mention growing trend: %s", got)
	}
}

func TestBuildPromptRecommendation_VeryLargeAvg(t *testing.T) {
	impact := SystemPromptImpact{
		TotalSessions:    10,
		AvgEstimate:      12000,
		AvgPromptCostPct: 25,
		Trend:            "stable",
	}
	got := buildPromptRecommendation(impact)
	if !containsAll(got, "12K") {
		t.Errorf("recommendation doesn't mention 12K size: %s", got)
	}
}

func TestBuildPromptRecommendation_NormalRange(t *testing.T) {
	impact := SystemPromptImpact{
		TotalSessions:    10,
		AvgEstimate:      3000,
		AvgPromptCostPct: 10,
		Trend:            "stable",
	}
	got := buildPromptRecommendation(impact)
	if !containsAll(got, "normal range") {
		t.Errorf("recommendation doesn't say normal range: %s", got)
	}
}

// --- Format helpers ---

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{500, "500"},
		{1000, "1K"},
		{1500, "1.5K"},
		{10000, "10K"},
		{12345, "12.3K"},
	}
	for _, tt := range tests {
		got := formatTokenCount(tt.input)
		if got != tt.want {
			t.Errorf("formatTokenCount(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatPct(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{10.0, "10"},
		{15.5, "15.5"},
		{0.0, "0"},
	}
	for _, tt := range tests {
		got := formatPct(tt.input)
		if got != tt.want {
			t.Errorf("formatPct(%.1f) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// containsAll checks if s contains all the given substrings.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
