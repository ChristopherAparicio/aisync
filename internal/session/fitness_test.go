package session

import (
	"testing"
)

func TestAnalyzeModelFitness_Empty(t *testing.T) {
	result := AnalyzeModelFitness(nil)
	if result.TotalSessions != 0 {
		t.Errorf("TotalSessions = %d, want 0", result.TotalSessions)
	}
	if len(result.TaskTypes) != 0 {
		t.Errorf("TaskTypes = %d, want 0", len(result.TaskTypes))
	}
}

func TestAnalyzeModelFitness_NoValidData(t *testing.T) {
	data := []SessionFitnessData{
		{Model: "", SessionType: "feature"},   // no model
		{Model: "opus", SessionType: ""},      // no type
		{Model: "opus", SessionType: "other"}, // "other" is excluded
	}
	result := AnalyzeModelFitness(data)
	if result.TotalSessions != 0 {
		t.Errorf("TotalSessions = %d, want 0 (no valid pairs)", result.TotalSessions)
	}
}

func TestAnalyzeModelFitness_SingleModelSingleType(t *testing.T) {
	data := []SessionFitnessData{
		{Model: "claude-opus-4", SessionType: "feature", TotalTokens: 100000, OutputTokens: 10000, MessageCount: 30, ToolCalls: 20, ToolErrors: 2, EstimatedCost: 0.50},
		{Model: "claude-opus-4", SessionType: "feature", TotalTokens: 120000, OutputTokens: 12000, MessageCount: 35, ToolCalls: 25, ToolErrors: 1, EstimatedCost: 0.60},
	}
	result := AnalyzeModelFitness(data)

	if result.TotalSessions != 2 {
		t.Errorf("TotalSessions = %d, want 2", result.TotalSessions)
	}
	if len(result.TaskTypes) != 1 {
		t.Errorf("TaskTypes = %d, want 1", len(result.TaskTypes))
	}
	if result.TaskTypes[0].TaskType != "feature" {
		t.Errorf("TaskType = %q, want %q", result.TaskTypes[0].TaskType, "feature")
	}
	if result.TaskTypes[0].BestModel != "claude-opus-4" {
		t.Errorf("BestModel = %q, want %q", result.TaskTypes[0].BestModel, "claude-opus-4")
	}
	if result.TaskTypes[0].Models[0].SessionCount != 2 {
		t.Errorf("SessionCount = %d, want 2", result.TaskTypes[0].Models[0].SessionCount)
	}
}

func TestAnalyzeModelFitness_MultipleModels(t *testing.T) {
	data := []SessionFitnessData{
		// Opus: expensive, low errors
		{Model: "claude-opus-4", SessionType: "bug", TotalTokens: 100000, OutputTokens: 15000, MessageCount: 20, ToolCalls: 30, ToolErrors: 1, EstimatedCost: 1.00},
		{Model: "claude-opus-4", SessionType: "bug", TotalTokens: 110000, OutputTokens: 14000, MessageCount: 22, ToolCalls: 28, ToolErrors: 0, EstimatedCost: 1.10},
		{Model: "claude-opus-4", SessionType: "bug", TotalTokens: 105000, OutputTokens: 16000, MessageCount: 21, ToolCalls: 32, ToolErrors: 2, EstimatedCost: 1.05},
		// Sonnet: cheaper, moderate errors
		{Model: "claude-sonnet-4", SessionType: "bug", TotalTokens: 80000, OutputTokens: 10000, MessageCount: 25, ToolCalls: 35, ToolErrors: 5, EstimatedCost: 0.30},
		{Model: "claude-sonnet-4", SessionType: "bug", TotalTokens: 85000, OutputTokens: 11000, MessageCount: 28, ToolCalls: 40, ToolErrors: 6, EstimatedCost: 0.35},
		{Model: "claude-sonnet-4", SessionType: "bug", TotalTokens: 82000, OutputTokens: 10500, MessageCount: 26, ToolCalls: 38, ToolErrors: 4, EstimatedCost: 0.32},
	}
	result := AnalyzeModelFitness(data)

	if result.TotalSessions != 6 {
		t.Errorf("TotalSessions = %d, want 6", result.TotalSessions)
	}
	if len(result.TaskTypes) != 1 {
		t.Errorf("TaskTypes = %d, want 1", len(result.TaskTypes))
	}

	tp := result.TaskTypes[0]
	if len(tp.Models) != 2 {
		t.Fatalf("Models = %d, want 2", len(tp.Models))
	}

	// Both models should have valid fitness scores.
	for _, m := range tp.Models {
		if m.FitnessScore <= 0 || m.FitnessScore > 100 {
			t.Errorf("Model %s FitnessScore = %d, want 1-100", m.Model, m.FitnessScore)
		}
		if m.FitnessGrade == "" {
			t.Errorf("Model %s FitnessGrade is empty", m.Model)
		}
	}

	// Models should be sorted by fitness score descending.
	if tp.Models[0].FitnessScore < tp.Models[1].FitnessScore {
		t.Errorf("Models not sorted by fitness: %d < %d", tp.Models[0].FitnessScore, tp.Models[1].FitnessScore)
	}
	if tp.BestModel != tp.Models[0].Model {
		t.Errorf("BestModel = %q, want %q", tp.BestModel, tp.Models[0].Model)
	}
}

func TestAnalyzeModelFitness_MultipleTaskTypes(t *testing.T) {
	data := []SessionFitnessData{
		{Model: "opus", SessionType: "feature", TotalTokens: 100000, OutputTokens: 10000, MessageCount: 30, ToolCalls: 20, ToolErrors: 2, EstimatedCost: 0.50},
		{Model: "opus", SessionType: "feature", TotalTokens: 100000, OutputTokens: 10000, MessageCount: 30, ToolCalls: 20, ToolErrors: 2, EstimatedCost: 0.50},
		{Model: "opus", SessionType: "bug", TotalTokens: 80000, OutputTokens: 8000, MessageCount: 15, ToolCalls: 10, ToolErrors: 1, EstimatedCost: 0.30},
		{Model: "opus", SessionType: "refactor", TotalTokens: 90000, OutputTokens: 9000, MessageCount: 20, ToolCalls: 15, ToolErrors: 0, EstimatedCost: 0.40},
	}
	result := AnalyzeModelFitness(data)

	if len(result.TaskTypes) != 3 {
		t.Errorf("TaskTypes = %d, want 3", len(result.TaskTypes))
	}

	// Sorted by session count descending, feature has 2 sessions.
	if result.TaskTypes[0].TaskType != "feature" {
		t.Errorf("First task type = %q, want %q (most sessions)", result.TaskTypes[0].TaskType, "feature")
	}
}

func TestAnalyzeModelFitness_ErrorRateImpact(t *testing.T) {
	data := []SessionFitnessData{
		// Model A: 0% error rate
		{Model: "model-a", SessionType: "feature", TotalTokens: 100000, OutputTokens: 15000, MessageCount: 20, ToolCalls: 30, ToolErrors: 0, EstimatedCost: 0.50},
		{Model: "model-a", SessionType: "feature", TotalTokens: 100000, OutputTokens: 15000, MessageCount: 20, ToolCalls: 30, ToolErrors: 0, EstimatedCost: 0.50},
		{Model: "model-a", SessionType: "feature", TotalTokens: 100000, OutputTokens: 15000, MessageCount: 20, ToolCalls: 30, ToolErrors: 0, EstimatedCost: 0.50},
		// Model B: 50% error rate (same everything else)
		{Model: "model-b", SessionType: "feature", TotalTokens: 100000, OutputTokens: 15000, MessageCount: 20, ToolCalls: 30, ToolErrors: 15, EstimatedCost: 0.50},
		{Model: "model-b", SessionType: "feature", TotalTokens: 100000, OutputTokens: 15000, MessageCount: 20, ToolCalls: 30, ToolErrors: 15, EstimatedCost: 0.50},
		{Model: "model-b", SessionType: "feature", TotalTokens: 100000, OutputTokens: 15000, MessageCount: 20, ToolCalls: 30, ToolErrors: 15, EstimatedCost: 0.50},
	}
	result := AnalyzeModelFitness(data)

	tp := result.TaskTypes[0]
	// Model A (0% errors) should rank higher than Model B (50% errors).
	if tp.Models[0].Model != "model-a" {
		t.Errorf("Best model = %q, want model-a (lower error rate)", tp.Models[0].Model)
	}
	if tp.Models[0].FitnessScore <= tp.Models[1].FitnessScore {
		t.Errorf("Model A score %d should be > Model B score %d", tp.Models[0].FitnessScore, tp.Models[1].FitnessScore)
	}
}

func TestAnalyzeModelFitness_RetryRateImpact(t *testing.T) {
	data := []SessionFitnessData{
		// Model A: no retries
		{Model: "model-a", SessionType: "bug", TotalTokens: 100000, OutputTokens: 15000, MessageCount: 20, ToolCalls: 20, ToolErrors: 0, EstimatedCost: 0.50, HasRetries: false},
		{Model: "model-a", SessionType: "bug", TotalTokens: 100000, OutputTokens: 15000, MessageCount: 20, ToolCalls: 20, ToolErrors: 0, EstimatedCost: 0.50, HasRetries: false},
		{Model: "model-a", SessionType: "bug", TotalTokens: 100000, OutputTokens: 15000, MessageCount: 20, ToolCalls: 20, ToolErrors: 0, EstimatedCost: 0.50, HasRetries: false},
		// Model B: all sessions have retries
		{Model: "model-b", SessionType: "bug", TotalTokens: 100000, OutputTokens: 15000, MessageCount: 20, ToolCalls: 20, ToolErrors: 0, EstimatedCost: 0.50, HasRetries: true},
		{Model: "model-b", SessionType: "bug", TotalTokens: 100000, OutputTokens: 15000, MessageCount: 20, ToolCalls: 20, ToolErrors: 0, EstimatedCost: 0.50, HasRetries: true},
		{Model: "model-b", SessionType: "bug", TotalTokens: 100000, OutputTokens: 15000, MessageCount: 20, ToolCalls: 20, ToolErrors: 0, EstimatedCost: 0.50, HasRetries: true},
	}
	result := AnalyzeModelFitness(data)

	tp := result.TaskTypes[0]
	if tp.Models[0].Model != "model-a" {
		t.Errorf("Best model = %q, want model-a (no retries)", tp.Models[0].Model)
	}
}

func TestComputeFitnessScore_PerfectProfile(t *testing.T) {
	p := ModelFitnessProfile{
		ErrorRate:       0,
		RetryRate:       0,
		AvgCost:         0.001,
		AvgOutputTokens: 10000,
		AvgTokens:       33333, // ~30% output ratio
	}
	score := computeFitnessScore(p)
	if score < 80 {
		t.Errorf("Perfect profile score = %d, want >= 80", score)
	}
}

func TestComputeFitnessScore_TerribleProfile(t *testing.T) {
	p := ModelFitnessProfile{
		ErrorRate:       25, // very high
		RetryRate:       100,
		AvgCost:         10.0,
		AvgOutputTokens: 1000,
		AvgTokens:       1000000, // 0.1% output ratio
	}
	score := computeFitnessScore(p)
	if score > 20 {
		t.Errorf("Terrible profile score = %d, want <= 20", score)
	}
}

func TestComputeFitnessScore_Bounds(t *testing.T) {
	// Score should always be 0-100.
	tests := []ModelFitnessProfile{
		{ErrorRate: 0, RetryRate: 0, AvgCost: 0, AvgOutputTokens: 0, AvgTokens: 0},
		{ErrorRate: 100, RetryRate: 100, AvgCost: 100, AvgOutputTokens: 1, AvgTokens: 1000000},
	}
	for i, p := range tests {
		score := computeFitnessScore(p)
		if score < 0 || score > 100 {
			t.Errorf("test %d: score = %d, want 0-100", i, score)
		}
	}
}

func TestLog10Approx(t *testing.T) {
	tests := []struct {
		input float64
		want  float64 // approximate
		delta float64
	}{
		{1, 0, 0.2},
		{10, 1, 0.2},
		{100, 2, 0.2},
		{1000, 3, 0.2},
		{0.1, -1, 0.2},
	}
	for _, tt := range tests {
		got := log10Approx(tt.input)
		diff := got - tt.want
		if diff < 0 {
			diff = -diff
		}
		if diff > tt.delta {
			t.Errorf("log10Approx(%v) = %.2f, want ~%.2f (±%.1f)", tt.input, got, tt.want, tt.delta)
		}
	}
}

func TestLog10Approx_Zero(t *testing.T) {
	got := log10Approx(0)
	if got != 0 {
		t.Errorf("log10Approx(0) = %f, want 0", got)
	}
}

func TestBuildFitnessRecommendations_NoModels(t *testing.T) {
	profiles := []TaskTypeProfile{
		{TaskType: "feature", Models: []ModelFitnessProfile{{Model: "opus", FitnessScore: 80}}},
	}
	recs := buildFitnessRecommendations(profiles)
	// Only 1 model — no comparison possible.
	if len(recs) != 0 {
		t.Errorf("Recommendations = %d, want 0 (only 1 model)", len(recs))
	}
}

func TestBuildFitnessRecommendations_SignificantGap(t *testing.T) {
	profiles := []TaskTypeProfile{
		{TaskType: "bug", Models: []ModelFitnessProfile{
			{Model: "opus", FitnessScore: 85, SessionCount: 10, AvgCost: 1.00},
			{Model: "sonnet", FitnessScore: 55, SessionCount: 5, AvgCost: 0.30},
		}},
	}
	recs := buildFitnessRecommendations(profiles)
	if len(recs) == 0 {
		t.Error("Expected at least 1 recommendation for 30-point gap")
	}
}

func TestBuildFitnessRecommendations_CostSaving(t *testing.T) {
	profiles := []TaskTypeProfile{
		{TaskType: "feature", Models: []ModelFitnessProfile{
			{Model: "opus", FitnessScore: 80, SessionCount: 10, AvgCost: 1.00},
			{Model: "haiku", FitnessScore: 75, SessionCount: 5, AvgCost: 0.10}, // 90% cheaper, similar quality
		}},
	}
	recs := buildFitnessRecommendations(profiles)
	found := false
	for _, r := range recs {
		if containsAll(r, "cheaper", "haiku") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected cost-saving recommendation mentioning haiku: %v", recs)
	}
}

func TestBuildFitnessRecommendations_MaxFive(t *testing.T) {
	// Create many task types to trigger >5 recommendations.
	var profiles []TaskTypeProfile
	types := []string{"feature", "bug", "refactor", "exploration", "review", "devops"}
	for _, tt := range types {
		profiles = append(profiles, TaskTypeProfile{
			TaskType: tt,
			Models: []ModelFitnessProfile{
				{Model: "opus", FitnessScore: 90, SessionCount: 5, AvgCost: 1.00},
				{Model: "bad-model", FitnessScore: 20, SessionCount: 5, AvgCost: 0.80, ErrorRate: 25},
			},
		})
	}
	recs := buildFitnessRecommendations(profiles)
	if len(recs) > 5 {
		t.Errorf("Recommendations = %d, want <= 5 (capped)", len(recs))
	}
}

func TestAnalyzeModelFitness_AvgMetrics(t *testing.T) {
	data := []SessionFitnessData{
		{Model: "opus", SessionType: "feature", TotalTokens: 100000, OutputTokens: 10000, MessageCount: 30, ToolCalls: 20, ToolErrors: 4, EstimatedCost: 0.50},
		{Model: "opus", SessionType: "feature", TotalTokens: 200000, OutputTokens: 20000, MessageCount: 40, ToolCalls: 30, ToolErrors: 6, EstimatedCost: 1.00},
	}
	result := AnalyzeModelFitness(data)
	m := result.TaskTypes[0].Models[0]

	// Avg cost: (0.50 + 1.00) / 2 = 0.75
	if m.AvgCost != 0.75 {
		t.Errorf("AvgCost = %f, want 0.75", m.AvgCost)
	}
	// Avg messages: (30 + 40) / 2 = 35
	if m.AvgMessages != 35 {
		t.Errorf("AvgMessages = %f, want 35", m.AvgMessages)
	}
	// Avg tokens: (100000 + 200000) / 2 = 150000
	if m.AvgTokens != 150000 {
		t.Errorf("AvgTokens = %d, want 150000", m.AvgTokens)
	}
	// Error rate: (4+6) / (20+30) * 100 = 20%
	if m.ErrorRate != 20 {
		t.Errorf("ErrorRate = %f, want 20", m.ErrorRate)
	}
}
