package session

import (
	"math"
	"sort"
)

// SaturationForecast predicts when sessions will hit context window limits.
// It aggregates token growth data from historical sessions to forecast
// messages-to-saturation for each model.
type SaturationForecast struct {
	// Per-model forecast data.
	Models []ModelSaturationForecast `json:"models,omitempty"`

	// Distribution histogram: messages before first compaction.
	// Bucket ranges: "1-20", "21-40", "41-60", etc.
	CompactionHistogram []HistogramBucket `json:"compaction_histogram,omitempty"`

	// Global averages across all models.
	AvgMsgsToCompaction   int     `json:"avg_msgs_to_compaction"`   // average messages before first compaction (0 = no data)
	AvgTokenGrowthPerMsg  int     `json:"avg_token_growth_per_msg"` // average token growth per message
	SessionsWithForecast  int     `json:"sessions_with_forecast"`   // sessions that had enough data for forecast
	SessionsWithCompacted int     `json:"sessions_with_compacted"`  // sessions that were actually compacted
	AvgPeakUtilization    float64 `json:"avg_peak_utilization"`     // average peak % of context window reached
}

// ModelSaturationForecast provides saturation prediction for a specific model.
type ModelSaturationForecast struct {
	Model          string `json:"model"`
	MaxInputTokens int    `json:"max_input_tokens"` // context window size

	// Observed data.
	SessionCount          int     `json:"session_count"`
	CompactedCount        int     `json:"compacted_count"`          // sessions that hit compaction
	AvgMsgsToCompacted    int     `json:"avg_msgs_to_compacted"`    // average messages before first compaction (only compacted sessions)
	MedianMsgsToCompacted int     `json:"median_msgs_to_compacted"` // median messages before first compaction
	AvgTokenGrowthPerMsg  int     `json:"avg_token_growth_per_msg"` // average token growth per message across sessions
	AvgPeakUtilization    float64 `json:"avg_peak_utilization"`     // average peak % of context window

	// Forecast: predicted messages until thresholds.
	PredictedMsgsTo80  int `json:"predicted_msgs_to_80"`  // predicted messages to reach 80% (degraded zone)
	PredictedMsgsTo100 int `json:"predicted_msgs_to_100"` // predicted messages to reach 100% (compaction)

	// Recommendation based on data.
	Recommendation string `json:"recommendation"` // actionable guidance
}

// HistogramBucket is a single bucket in the messages-to-compaction histogram.
type HistogramBucket struct {
	Label string `json:"label"` // e.g. "1-20", "21-40"
	Count int    `json:"count"` // number of sessions in this bucket
}

// SessionForecastInput holds per-session data needed for forecast computation.
type SessionForecastInput struct {
	Model                string
	MaxInputTokens       int
	MessageCount         int
	PeakInputTokens      int
	MsgAtFirstCompaction int // 0 if no compaction detected
	TokenGrowthPerMsg    int // average token delta per message
}

// histogramBucketSize defines the width of each histogram bucket.
const histogramBucketSize = 20

// ForecastSaturation computes saturation forecasts from session data.
// This is a pure function — no service dependencies.
func ForecastSaturation(sessions []SessionForecastInput) SaturationForecast {
	var result SaturationForecast

	if len(sessions) == 0 {
		return result
	}

	// Per-model accumulator.
	type modelAcc struct {
		model          string
		maxInputTokens int
		sessionCount   int
		compactedCount int
		msgsToCompact  []int     // messages before first compaction (only compacted sessions)
		tokenGrowths   []int     // per-session average token growth
		peakPcts       []float64 // per-session peak utilization %
	}
	modelMap := make(map[string]*modelAcc)

	var allMsgsToCompact []int
	var totalGrowth int64
	var growthCount int

	for _, s := range sessions {
		if s.MaxInputTokens == 0 || s.MessageCount < 2 {
			continue
		}
		result.SessionsWithForecast++

		acc, ok := modelMap[s.Model]
		if !ok {
			acc = &modelAcc{
				model:          s.Model,
				maxInputTokens: s.MaxInputTokens,
			}
			modelMap[s.Model] = acc
		}
		acc.sessionCount++

		// Track peak utilization.
		peakPct := float64(s.PeakInputTokens) / float64(s.MaxInputTokens) * 100
		if peakPct > 100 {
			peakPct = 100
		}
		acc.peakPcts = append(acc.peakPcts, peakPct)

		// Track token growth rate.
		if s.TokenGrowthPerMsg > 0 {
			acc.tokenGrowths = append(acc.tokenGrowths, s.TokenGrowthPerMsg)
			totalGrowth += int64(s.TokenGrowthPerMsg)
			growthCount++
		}

		// Track compaction.
		if s.MsgAtFirstCompaction > 0 {
			acc.compactedCount++
			acc.msgsToCompact = append(acc.msgsToCompact, s.MsgAtFirstCompaction)
			allMsgsToCompact = append(allMsgsToCompact, s.MsgAtFirstCompaction)
			result.SessionsWithCompacted++
		}
	}

	// Compute global averages.
	if growthCount > 0 {
		result.AvgTokenGrowthPerMsg = int(totalGrowth / int64(growthCount))
	}
	if len(allMsgsToCompact) > 0 {
		result.AvgMsgsToCompaction = meanInt(allMsgsToCompact)
	}

	// Compute per-model forecasts.
	for _, acc := range modelMap {
		mf := ModelSaturationForecast{
			Model:          acc.model,
			MaxInputTokens: acc.maxInputTokens,
			SessionCount:   acc.sessionCount,
			CompactedCount: acc.compactedCount,
		}

		// Average peak utilization.
		if len(acc.peakPcts) > 0 {
			var sum float64
			for _, p := range acc.peakPcts {
				sum += p
			}
			mf.AvgPeakUtilization = sum / float64(len(acc.peakPcts))
			result.AvgPeakUtilization += sum // accumulate for global average
		}

		// Messages to first compaction (only compacted sessions).
		if len(acc.msgsToCompact) > 0 {
			mf.AvgMsgsToCompacted = meanInt(acc.msgsToCompact)
			mf.MedianMsgsToCompacted = medianInt(acc.msgsToCompact)
		}

		// Average token growth per message.
		if len(acc.tokenGrowths) > 0 {
			mf.AvgTokenGrowthPerMsg = meanInt(acc.tokenGrowths)
		}

		// Predict messages to reach thresholds using linear extrapolation.
		if mf.AvgTokenGrowthPerMsg > 0 && acc.maxInputTokens > 0 {
			target80 := int(float64(acc.maxInputTokens) * 0.80)
			target100 := acc.maxInputTokens
			mf.PredictedMsgsTo80 = target80 / mf.AvgTokenGrowthPerMsg
			mf.PredictedMsgsTo100 = target100 / mf.AvgTokenGrowthPerMsg

			// Cap predictions at reasonable bounds.
			if mf.PredictedMsgsTo80 > 10000 {
				mf.PredictedMsgsTo80 = 0 // unreasonable — treat as "won't reach"
			}
			if mf.PredictedMsgsTo100 > 10000 {
				mf.PredictedMsgsTo100 = 0
			}
		}

		// Generate recommendation.
		mf.Recommendation = forecastRecommendation(mf)

		result.Models = append(result.Models, mf)
	}

	// Sort models by compacted count descending (most problematic first).
	sort.Slice(result.Models, func(i, j int) bool {
		if result.Models[i].CompactedCount != result.Models[j].CompactedCount {
			return result.Models[i].CompactedCount > result.Models[j].CompactedCount
		}
		return result.Models[i].AvgPeakUtilization > result.Models[j].AvgPeakUtilization
	})

	// Finalize global average peak utilization.
	if result.SessionsWithForecast > 0 {
		result.AvgPeakUtilization /= float64(result.SessionsWithForecast)
	}

	// Build histogram of messages before first compaction.
	result.CompactionHistogram = buildCompactionHistogram(allMsgsToCompact)

	return result
}

// forecastRecommendation generates actionable guidance for a model based on forecast data.
func forecastRecommendation(mf ModelSaturationForecast) string {
	compactedPct := 0.0
	if mf.SessionCount > 0 {
		compactedPct = float64(mf.CompactedCount) / float64(mf.SessionCount) * 100
	}

	switch {
	case compactedPct > 50:
		if mf.MedianMsgsToCompacted > 0 {
			return "Majority of sessions hit compaction. Consider splitting tasks at ~" +
				itoa(mf.MedianMsgsToCompacted) + " messages."
		}
		return "Majority of sessions hit compaction. Consider splitting tasks or using a larger context model."
	case compactedPct > 20:
		if mf.PredictedMsgsTo80 > 0 {
			return "Sessions approach degraded zone around message " +
				itoa(mf.PredictedMsgsTo80) + ". Plan task boundaries accordingly."
		}
		return "Frequent compaction. Consider breaking complex tasks into smaller sessions."
	case mf.AvgPeakUtilization > 70:
		return "Context usage is high. Monitor for increasing compaction in longer sessions."
	case mf.AvgPeakUtilization < 20:
		return "Context is underutilized. A smaller, cheaper model may be sufficient."
	default:
		return "Context usage is balanced. Current model is well-sized for typical workloads."
	}
}

// buildCompactionHistogram creates bucketed distribution of messages-to-first-compaction.
func buildCompactionHistogram(msgsToCompact []int) []HistogramBucket {
	if len(msgsToCompact) == 0 {
		return nil
	}

	// Find the max value to determine bucket count.
	maxVal := 0
	for _, v := range msgsToCompact {
		if v > maxVal {
			maxVal = v
		}
	}

	// Create buckets: 1-20, 21-40, 41-60, ...
	numBuckets := (maxVal / histogramBucketSize) + 1
	// Cap at a reasonable number to avoid UI overflow.
	if numBuckets > 25 {
		numBuckets = 25
	}

	buckets := make([]HistogramBucket, numBuckets)
	for i := range buckets {
		low := i*histogramBucketSize + 1
		high := (i + 1) * histogramBucketSize
		buckets[i].Label = itoa(low) + "-" + itoa(high)
	}

	// Fill buckets.
	for _, v := range msgsToCompact {
		idx := (v - 1) / histogramBucketSize
		if idx >= numBuckets {
			idx = numBuckets - 1 // last bucket catches overflow
		}
		if idx >= 0 {
			buckets[idx].Count++
		}
	}

	// Remove trailing empty buckets.
	for len(buckets) > 0 && buckets[len(buckets)-1].Count == 0 {
		buckets = buckets[:len(buckets)-1]
	}

	return buckets
}

// meanInt computes the mean of a slice of ints.
func meanInt(vals []int) int {
	if len(vals) == 0 {
		return 0
	}
	var sum int64
	for _, v := range vals {
		sum += int64(v)
	}
	return int(math.Round(float64(sum) / float64(len(vals))))
}

// medianInt computes the median of a slice of ints.
func medianInt(vals []int) int {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]int, len(vals))
	copy(sorted, vals)
	sort.Ints(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}

// itoa converts an int to string without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte(n%10) + '0'
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
