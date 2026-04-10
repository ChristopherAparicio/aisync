package diagnostic

import (
	"fmt"
	"math"
	"sort"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// TrendComparison compares the current session's diagnostic metrics against
// the project's historical baseline. All fields are factual observations —
// no recommendations or prescriptions.
type TrendComparison struct {
	// BaselineSessions is the number of historical sessions used for comparison.
	BaselineSessions int `json:"baseline_sessions"`

	// BaselineDays is the number of days the baseline spans.
	BaselineDays int `json:"baseline_days"`

	// Metrics contains the per-metric comparisons.
	Metrics []TrendMetric `json:"metrics"`

	// Anomalies are metrics that are significantly above baseline (≥ 2x or ≥ 1.5x depending on type).
	Anomalies []TrendMetric `json:"anomalies,omitempty"`
}

// TrendMetric is a single metric comparison between the current session and baseline.
type TrendMetric struct {
	Name      string  `json:"name"`       // e.g. "compaction_rate", "error_rate", "estimated_cost"
	Label     string  `json:"label"`      // human-readable label
	Current   float64 `json:"current"`    // current session value
	Baseline  float64 `json:"baseline"`   // project average
	Ratio     float64 `json:"ratio"`      // current / baseline (>1 = above baseline)
	Unit      string  `json:"unit"`       // "ratio", "count", "tokens", "USD", "pct"
	Narrative string  `json:"narrative"`  // factual comparison sentence
	IsAnomaly bool    `json:"is_anomaly"` // true if significantly above baseline
}

// CompareTrend compares the current session's diagnostic report against
// a set of historical analytics rows from the same project. Returns nil
// if there are fewer than 3 baseline sessions (not enough data for
// meaningful comparison).
func CompareTrend(report *InspectReport, history []session.AnalyticsRow) *TrendComparison {
	if len(history) < 3 {
		return nil
	}

	tc := &TrendComparison{
		BaselineSessions: len(history),
	}

	// Compute baseline days span.
	if len(history) > 1 {
		sort.Slice(history, func(i, j int) bool {
			return history[i].CreatedAt.Before(history[j].CreatedAt)
		})
		first := history[0].CreatedAt
		last := history[len(history)-1].CreatedAt
		tc.BaselineDays = int(last.Sub(first).Hours()/24) + 1
	}

	// Compute baseline averages.
	var (
		sumCost          float64
		sumCompactions   float64
		sumInputTokens   float64
		sumCacheReadPct  float64
		sumErrorCount    float64
		sumMsgCount      float64
		sumToolCallCount float64
		sumPeakSat       float64
		sessWithTokens   int
	)

	for _, row := range history {
		sumCost += row.EstimatedCost
		sumCompactions += float64(row.CompactionCount)
		sumInputTokens += float64(row.InputTokens)
		sumErrorCount += float64(row.ErrorCount)
		sumMsgCount += float64(row.MessageCount)
		sumToolCallCount += float64(row.ToolCallCount)
		sumPeakSat += row.PeakSaturationPct
		if row.InputTokens > 0 {
			sessWithTokens++
			if row.InputTokens > 0 {
				sumCacheReadPct += float64(row.CacheReadTokens) / float64(row.InputTokens) * 100
			}
		}
	}

	n := float64(len(history))

	avgCost := sumCost / n
	avgCompactions := sumCompactions / n
	avgInputTokens := sumInputTokens / n
	avgCacheReadPct := 0.0
	if sessWithTokens > 0 {
		avgCacheReadPct = sumCacheReadPct / float64(sessWithTokens)
	}
	avgErrorRate := 0.0
	if sumToolCallCount > 0 {
		avgErrorRate = sumErrorCount / sumToolCallCount
	}
	avgMsgCount := sumMsgCount / n
	avgPeakSat := sumPeakSat / n

	// Current session values.
	var (
		curCost         float64
		curCompactions  float64
		curInputTokens  float64
		curCacheReadPct float64
		curErrorRate    float64
		curMsgCount     float64
		curPeakSat      float64
	)

	if report.Tokens != nil {
		curCost = report.Tokens.EstCost
		curInputTokens = float64(report.Tokens.Input)
		curCacheReadPct = report.Tokens.CachePct
	}
	if report.Compaction != nil {
		curCompactions = float64(report.Compaction.Count)
	}
	if report.ToolErrors != nil && report.ToolErrors.TotalToolCalls > 0 {
		curErrorRate = float64(report.ToolErrors.ErrorCount) / float64(report.ToolErrors.TotalToolCalls)
	}
	curMsgCount = float64(report.Messages)

	// Compute peak saturation from compaction data if available.
	if report.Compaction != nil && report.Compaction.AvgBeforeTokens > 0 {
		// Use avg context at compaction as a proxy for peak saturation.
		// Without the model's window size here, express as raw tokens.
		curPeakSat = float64(report.Compaction.AvgBeforeTokens)
	}

	// Build metrics.
	metrics := []TrendMetric{
		buildMetric("estimated_cost", "Estimated cost", curCost, avgCost, "USD", 2.0),
		buildMetric("compaction_count", "Compaction events", curCompactions, avgCompactions, "count", 1.5),
		buildMetric("input_tokens", "Input tokens", curInputTokens, avgInputTokens, "tokens", 2.0),
		buildMetric("cache_read_pct", "Cache read %", curCacheReadPct, avgCacheReadPct, "pct", 0), // no anomaly for cache (lower is worse)
		buildMetric("error_rate", "Tool error rate", curErrorRate, avgErrorRate, "ratio", 2.0),
		buildMetric("message_count", "Messages", curMsgCount, avgMsgCount, "count", 2.0),
	}

	// Only add peak saturation if we have comparable data.
	if curPeakSat > 0 && avgPeakSat > 0 {
		metrics = append(metrics,
			buildMetric("peak_saturation", "Peak context saturation %", curPeakSat, avgPeakSat, "pct", 1.5))
	}

	tc.Metrics = metrics

	// Collect anomalies.
	for _, m := range metrics {
		if m.IsAnomaly {
			tc.Anomalies = append(tc.Anomalies, m)
		}
	}

	return tc
}

// buildMetric creates a TrendMetric with a factual narrative.
// anomalyThreshold is the ratio above which the metric is flagged as an anomaly.
// Use 0 to never flag as anomaly (e.g. for metrics where lower is worse).
func buildMetric(name, label string, current, baseline float64, unit string, anomalyThreshold float64) TrendMetric {
	ratio := 0.0
	if baseline > 0 {
		ratio = current / baseline
	}

	narrative := buildNarrative(label, current, baseline, ratio, unit)

	isAnomaly := false
	if anomalyThreshold > 0 && baseline > 0 && ratio >= anomalyThreshold {
		isAnomaly = true
	}

	return TrendMetric{
		Name:      name,
		Label:     label,
		Current:   roundTo(current, 4),
		Baseline:  roundTo(baseline, 4),
		Ratio:     roundTo(ratio, 2),
		Unit:      unit,
		Narrative: narrative,
		IsAnomaly: isAnomaly,
	}
}

// buildNarrative produces a factual comparison sentence.
func buildNarrative(label string, current, baseline, ratio float64, unit string) string {
	if baseline == 0 {
		return fmt.Sprintf("%s: %s (no baseline data)", label, fmtMetricValue(current, unit))
	}

	direction := "at baseline"
	if ratio > 1.05 {
		direction = fmt.Sprintf("%.2f\u00d7 above baseline", ratio)
	} else if ratio < 0.95 {
		direction = fmt.Sprintf("%.2f\u00d7 below baseline", ratio)
	}

	return fmt.Sprintf("%s: %s (this session) vs %s avg (project baseline) \u2014 %s",
		label,
		fmtMetricValue(current, unit),
		fmtMetricValue(baseline, unit),
		direction,
	)
}

// fmtMetricValue formats a numeric value with its unit.
func fmtMetricValue(v float64, unit string) string {
	switch unit {
	case "USD":
		return fmt.Sprintf("$%.2f", v)
	case "tokens":
		if v >= 1_000_000 {
			return fmt.Sprintf("%.1fM", v/1_000_000)
		}
		if v >= 1_000 {
			return fmt.Sprintf("%.1fK", v/1_000)
		}
		return fmt.Sprintf("%.0f", v)
	case "pct":
		return fmt.Sprintf("%.1f%%", v)
	case "ratio":
		return fmt.Sprintf("%.3f", v)
	case "count":
		return fmt.Sprintf("%.0f", v)
	default:
		return fmt.Sprintf("%.2f", v)
	}
}

// roundTo rounds a float to n decimal places.
func roundTo(v float64, n int) float64 {
	pow := math.Pow(10, float64(n))
	return math.Round(v*pow) / pow
}
