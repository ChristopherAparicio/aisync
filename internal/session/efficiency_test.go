package session

import (
	"math"
	"testing"
)

func TestComputeModelEfficiency_BasicMetrics(t *testing.T) {
	ms := &ModelSaturation{
		Model:             "claude-sonnet-4-20250514",
		TotalInputTokens:  1_000_000,
		TotalOutputTokens: 150_000,
		TotalCost:         3.50,
		TotalToolCalls:    200,
		TotalToolErrors:   10,
		TotalMessages:     500,
		AvgPeakPct:        50, // well-sized
	}

	ComputeModelEfficiency(ms)

	// Error rate: 10/200 * 100 = 5%
	if math.Abs(ms.ErrorRate-5.0) > 0.1 {
		t.Errorf("ErrorRate = %.2f, want ~5.0", ms.ErrorRate)
	}

	// Output/input ratio: 150K/1M * 100 = 15%
	if math.Abs(ms.AvgOutputRatio-15.0) > 0.1 {
		t.Errorf("AvgOutputRatio = %.2f, want ~15.0", ms.AvgOutputRatio)
	}

	// CostPer1KOutput: 3.50 / (150000 * 0.95) * 1000 ≈ 0.0245
	if ms.CostPer1KOutput < 0.02 || ms.CostPer1KOutput > 0.03 {
		t.Errorf("CostPer1KOutput = %.4f, want ~0.0245", ms.CostPer1KOutput)
	}

	// OutputPerDollar: (150000 * 0.95) / 3.50 ≈ 40714
	if ms.OutputPerDollar < 35000 || ms.OutputPerDollar > 45000 {
		t.Errorf("OutputPerDollar = %.0f, want ~40714", ms.OutputPerDollar)
	}

	// Grade should be decent (B or A) — 50% utilization, 15% output ratio, low error rate
	if ms.EfficiencyGrade != "A" && ms.EfficiencyGrade != "B" {
		t.Errorf("Grade = %s (score=%d), want A or B", ms.EfficiencyGrade, ms.EfficiencyScore)
	}
}

func TestComputeModelEfficiency_ZeroCost(t *testing.T) {
	ms := &ModelSaturation{
		TotalOutputTokens: 100_000,
		TotalCost:         0, // no cost data
		TotalToolCalls:    50,
		TotalToolErrors:   0,
		TotalInputTokens:  500_000,
		AvgPeakPct:        40,
	}

	ComputeModelEfficiency(ms)

	if ms.CostPer1KOutput != 0 {
		t.Errorf("CostPer1KOutput = %.4f, want 0 (no cost)", ms.CostPer1KOutput)
	}
	if ms.OutputPerDollar != 0 {
		t.Errorf("OutputPerDollar = %.0f, want 0 (no cost)", ms.OutputPerDollar)
	}
	if ms.ErrorRate != 0 {
		t.Errorf("ErrorRate = %.2f, want 0", ms.ErrorRate)
	}
}

func TestComputeModelEfficiency_HighErrorRate(t *testing.T) {
	ms := &ModelSaturation{
		TotalInputTokens:  500_000,
		TotalOutputTokens: 50_000,
		TotalCost:         2.00,
		TotalToolCalls:    100,
		TotalToolErrors:   30, // 30% error rate
		AvgPeakPct:        85, // tight/saturated
	}

	ComputeModelEfficiency(ms)

	// Error rate: 30%
	if math.Abs(ms.ErrorRate-30.0) > 0.1 {
		t.Errorf("ErrorRate = %.2f, want 30.0", ms.ErrorRate)
	}

	// High errors + tight context = low score
	if ms.EfficiencyScore > 50 {
		t.Errorf("Score = %d, want ≤50 for high-error saturated model", ms.EfficiencyScore)
	}

	if ms.EfficiencyGrade == "A" || ms.EfficiencyGrade == "B" {
		t.Errorf("Grade = %s, want C/D/F for poor efficiency", ms.EfficiencyGrade)
	}
}

func TestComputeModelEfficiency_OversizedModel(t *testing.T) {
	ms := &ModelSaturation{
		TotalInputTokens:  200_000,
		TotalOutputTokens: 30_000,
		TotalCost:         5.00, // expensive
		TotalToolCalls:    50,
		TotalToolErrors:   0,
		AvgPeakPct:        8, // massively oversized
	}

	ComputeModelEfficiency(ms)

	// Context utilization very low
	if ms.WastedCapacityPct != 0 {
		// WastedCapacityPct is set elsewhere, not by ComputeModelEfficiency
	}

	// Score should be middling — good error rate but oversized context and expensive
	if ms.EfficiencyScore > 60 {
		t.Errorf("Score = %d, want ≤60 for oversized model", ms.EfficiencyScore)
	}
}

func TestComputeModelEfficiency_NoToolCalls(t *testing.T) {
	ms := &ModelSaturation{
		TotalInputTokens:  100_000,
		TotalOutputTokens: 20_000,
		TotalCost:         0.50,
		TotalToolCalls:    0,
		TotalToolErrors:   0,
		AvgPeakPct:        45,
	}

	ComputeModelEfficiency(ms)

	if ms.ErrorRate != 0 {
		t.Errorf("ErrorRate = %.2f, want 0 (no tool calls)", ms.ErrorRate)
	}
	// No errors = full error discipline points
	if ms.EfficiencyScore < 40 {
		t.Errorf("Score = %d, want ≥40 (no errors, decent utilization)", ms.EfficiencyScore)
	}
}

func TestContextUtilizationScore(t *testing.T) {
	tests := []struct {
		pct  float64
		want float64
	}{
		{50, 25}, // ideal
		{40, 25}, // ideal boundary
		{60, 25}, // ideal boundary
		{35, 20}, // good
		{65, 20}, // good
		{25, 15}, // oversized
		{75, 15}, // tight
		{15, 10}, // quite oversized
		{85, 10}, // nearing saturation
		{5, 5},   // massively oversized
		{95, 5},  // saturated
	}

	for _, tt := range tests {
		got := contextUtilizationScore(tt.pct)
		if got != tt.want {
			t.Errorf("contextUtilizationScore(%.0f%%) = %.0f, want %.0f", tt.pct, got, tt.want)
		}
	}
}

func TestScoreToGrade(t *testing.T) {
	tests := []struct {
		score int
		want  string
	}{
		{100, "A"}, {80, "A"}, {79, "B"}, {65, "B"},
		{64, "C"}, {50, "C"}, {49, "D"}, {35, "D"},
		{34, "F"}, {0, "F"},
	}
	for _, tt := range tests {
		got := scoreToGrade(tt.score)
		if got != tt.want {
			t.Errorf("scoreToGrade(%d) = %s, want %s", tt.score, got, tt.want)
		}
	}
}

func TestClamp(t *testing.T) {
	tests := []struct {
		v, min, max, want float64
	}{
		{5, 0, 10, 5},
		{-1, 0, 10, 0},
		{15, 0, 10, 10},
		{0, 0, 25, 0},
		{25, 0, 25, 25},
	}
	for _, tt := range tests {
		got := clamp(tt.v, tt.min, tt.max)
		if got != tt.want {
			t.Errorf("clamp(%.0f, %.0f, %.0f) = %.0f, want %.0f", tt.v, tt.min, tt.max, got, tt.want)
		}
	}
}
