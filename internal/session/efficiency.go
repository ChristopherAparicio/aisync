package session

import "math"

// ComputeModelEfficiency computes derived efficiency metrics on a ModelSaturation
// after accumulation fields (TotalInputTokens, TotalOutputTokens, TotalCost,
// TotalToolErrors, TotalToolCalls) have been populated.
//
// It fills: ErrorRate, CostPer1KOutput, OutputPerDollar, AvgOutputRatio,
// EfficiencyScore, and EfficiencyGrade.
func ComputeModelEfficiency(ms *ModelSaturation) {
	// Error rate: tool errors / tool calls.
	if ms.TotalToolCalls > 0 {
		ms.ErrorRate = float64(ms.TotalToolErrors) / float64(ms.TotalToolCalls) * 100
	}

	// Cost per 1K useful output tokens.
	// "Useful" output excludes error-overhead: we scale output by (1 - error_rate/100)
	// to approximate wasted output from failed tool calls.
	usefulOutput := float64(ms.TotalOutputTokens)
	if ms.ErrorRate > 0 && ms.TotalToolCalls > 0 {
		// Reduce by error proportion — errors cause wasted context + retries.
		usefulOutput *= (1 - ms.ErrorRate/100)
	}
	if usefulOutput < 1 {
		usefulOutput = 1 // avoid division by zero
	}

	if ms.TotalCost > 0 {
		ms.CostPer1KOutput = ms.TotalCost / usefulOutput * 1000
		ms.OutputPerDollar = usefulOutput / ms.TotalCost
	}

	// Average output/input ratio (productivity measure).
	if ms.TotalInputTokens > 0 {
		ms.AvgOutputRatio = float64(ms.TotalOutputTokens) / float64(ms.TotalInputTokens) * 100
	}

	// Composite efficiency score (0-100).
	ms.EfficiencyScore = computeEfficiencyScore(ms)
	ms.EfficiencyGrade = scoreToGrade(ms.EfficiencyScore)
}

// computeEfficiencyScore produces a 0-100 composite score.
//
// Components (each 0-25, summed):
//  1. Context utilization (25 pts): "well-sized" scores highest, oversized/saturated score lower
//  2. Output productivity (25 pts): higher output/input ratio = more productive
//  3. Error discipline (25 pts): lower error rate = better
//  4. Cost efficiency (25 pts): higher output-per-dollar = better (log-scaled)
func computeEfficiencyScore(ms *ModelSaturation) int {
	var total float64

	// 1. Context utilization: sweet spot is 30-70% (well-sized).
	// Uses AvgPeakPct.
	utilizationScore := contextUtilizationScore(ms.AvgPeakPct)
	total += utilizationScore

	// 2. Output productivity: output/input ratio.
	// Typical range: 5-30% for coding assistants.
	// Scale: 0% → 0, 15%+ → 25.
	prodScore := clamp(ms.AvgOutputRatio/15.0*25.0, 0, 25)
	total += prodScore

	// 3. Error discipline: 0% errors → 25, 20%+ errors → 0.
	errScore := clamp(25.0-ms.ErrorRate*25.0/20.0, 0, 25)
	total += errScore

	// 4. Cost efficiency: log-scaled output-per-dollar.
	// Typical range: 10K-500K tokens/dollar depending on model.
	// Use log10 scaling: 10K → 10, 100K → 17.5, 1M → 25.
	costScore := 0.0
	if ms.OutputPerDollar > 0 {
		logVal := math.Log10(ms.OutputPerDollar)
		// Scale: log10(10000)=4 → 0, log10(1000000)=6 → 25
		costScore = clamp((logVal-4.0)*12.5, 0, 25)
	}
	total += costScore

	return int(math.Round(total))
}

// contextUtilizationScore converts AvgPeakPct into a 0-25 score.
// Sweet spot is 40-60% utilization (full 25). Falls off outside that range.
func contextUtilizationScore(avgPeakPct float64) float64 {
	switch {
	case avgPeakPct >= 40 && avgPeakPct <= 60:
		return 25 // ideal range
	case avgPeakPct >= 30 && avgPeakPct < 40:
		return 20 // good, slightly under-utilized
	case avgPeakPct >= 60 && avgPeakPct <= 70:
		return 20 // good, getting warm
	case avgPeakPct >= 20 && avgPeakPct < 30:
		return 15 // oversized
	case avgPeakPct > 70 && avgPeakPct <= 80:
		return 15 // tight
	case avgPeakPct >= 10 && avgPeakPct < 20:
		return 10 // quite oversized
	case avgPeakPct > 80 && avgPeakPct <= 90:
		return 10 // nearing saturation
	case avgPeakPct < 10:
		return 5 // massively oversized
	default: // > 90
		return 5 // saturated
	}
}

// scoreToGrade converts a 0-100 score to a letter grade.
func scoreToGrade(score int) string {
	switch {
	case score >= 80:
		return "A"
	case score >= 65:
		return "B"
	case score >= 50:
		return "C"
	case score >= 35:
		return "D"
	default:
		return "F"
	}
}

// clamp restricts v to [min, max].
func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
