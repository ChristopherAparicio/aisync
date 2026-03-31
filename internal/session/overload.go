package session

// OverloadAnalysis detects model overload signals within a single session.
// Overload occurs when a model's response quality degrades as the context fills —
// shorter answers, more errors, repeated tool calls.
type OverloadAnalysis struct {
	// Overall verdict.
	IsOverloaded bool   `json:"is_overloaded"` // true if significant overload detected
	Verdict      string `json:"verdict"`       // "healthy", "declining", "overloaded"
	InflectionAt int    `json:"inflection_at"` // message index where decline starts (0 = none)
	Reason       string `json:"reason"`        // human-readable explanation
	HealthScore  int    `json:"health_score"`  // 0-100 (100 = healthy)

	// Signal details.
	EarlyOutputRatio float64 `json:"early_output_ratio"` // output/input ratio in first half (%)
	LateOutputRatio  float64 `json:"late_output_ratio"`  // output/input ratio in second half (%)
	OutputRatioDecay float64 `json:"output_ratio_decay"` // % decline (positive = declining)

	EarlyErrorRate  float64 `json:"early_error_rate"`  // tool error rate in first half (%)
	LateErrorRate   float64 `json:"late_error_rate"`   // tool error rate in second half (%)
	ErrorRateGrowth float64 `json:"error_rate_growth"` // increase in error rate (percentage points)

	RetryCount    int `json:"retry_count"` // consecutive duplicate tool call sequences detected
	TotalMessages int `json:"total_messages"`
}

// overloadWindow accumulates signals for a half of the session.
type overloadWindow struct {
	inputTokens  int64
	outputTokens int64
	toolCalls    int
	toolErrors   int
	messages     int
}

// DetectOverload analyzes a session's messages for signs of model overload.
//
// Overload signals:
//  1. Declining output/input ratio: model gives shorter answers as context fills
//  2. Increasing error rate: more tool call failures in the second half
//  3. Tool call retries: same tool called consecutively (3+ times = retry)
//
// The session is split at the midpoint. Signals from the first half (early)
// are compared against the second half (late).
//
// Minimum session size: 10 messages (too few to detect trends).
func DetectOverload(messages []Message) OverloadAnalysis {
	result := OverloadAnalysis{
		TotalMessages: len(messages),
		Verdict:       "healthy",
		HealthScore:   100,
	}

	if len(messages) < 10 {
		return result
	}

	mid := len(messages) / 2

	// Accumulate signals for each half.
	var early, late overloadWindow
	for i := range messages {
		msg := &messages[i]
		w := &early
		if i >= mid {
			w = &late
		}
		w.messages++
		w.inputTokens += int64(msg.InputTokens)
		w.outputTokens += int64(msg.OutputTokens)
		for j := range msg.ToolCalls {
			w.toolCalls++
			if msg.ToolCalls[j].State == ToolStateError {
				w.toolErrors++
			}
		}
	}

	// Signal 1: Output/input ratio decline.
	if early.inputTokens > 0 {
		result.EarlyOutputRatio = float64(early.outputTokens) / float64(early.inputTokens) * 100
	}
	if late.inputTokens > 0 {
		result.LateOutputRatio = float64(late.outputTokens) / float64(late.inputTokens) * 100
	}
	if result.EarlyOutputRatio > 0 {
		result.OutputRatioDecay = (result.EarlyOutputRatio - result.LateOutputRatio) / result.EarlyOutputRatio * 100
	}

	// Signal 2: Error rate increase.
	if early.toolCalls > 0 {
		result.EarlyErrorRate = float64(early.toolErrors) / float64(early.toolCalls) * 100
	}
	if late.toolCalls > 0 {
		result.LateErrorRate = float64(late.toolErrors) / float64(late.toolCalls) * 100
	}
	result.ErrorRateGrowth = result.LateErrorRate - result.EarlyErrorRate

	// Signal 3: Tool call retries (same tool called 3+ times consecutively).
	result.RetryCount = detectToolRetries(messages)

	// Compute health score (deductions from 100).
	score := 100.0

	// Output ratio decay: >30% decline is concerning, >50% is bad.
	if result.OutputRatioDecay > 50 {
		score -= 30
	} else if result.OutputRatioDecay > 30 {
		score -= 15
	} else if result.OutputRatioDecay > 15 {
		score -= 5
	}

	// Error rate increase: >10pp increase is concerning, >20pp is bad.
	if result.ErrorRateGrowth > 20 {
		score -= 30
	} else if result.ErrorRateGrowth > 10 {
		score -= 15
	} else if result.ErrorRateGrowth > 5 {
		score -= 5
	}

	// Tool retries: each retry sequence costs points.
	retryPenalty := float64(result.RetryCount) * 5
	if retryPenalty > 25 {
		retryPenalty = 25
	}
	score -= retryPenalty

	// Late error rate alone is a signal (even without increase).
	if result.LateErrorRate > 30 {
		score -= 15
	} else if result.LateErrorRate > 15 {
		score -= 5
	}

	if score < 0 {
		score = 0
	}
	result.HealthScore = int(score)

	// Determine verdict and inflection point.
	switch {
	case score < 40:
		result.IsOverloaded = true
		result.Verdict = "overloaded"
	case score < 70:
		result.Verdict = "declining"
	default:
		result.Verdict = "healthy"
	}

	// Find inflection point: the message where output ratio starts declining.
	if result.OutputRatioDecay > 15 || result.ErrorRateGrowth > 5 {
		result.InflectionAt = findInflectionPoint(messages)
	}

	// Build human-readable reason.
	result.Reason = buildOverloadReason(result)

	return result
}

// detectToolRetries counts sequences where the same tool is called 3+ times
// consecutively, indicating retry behavior.
func detectToolRetries(messages []Message) int {
	retries := 0
	var lastTool string
	streak := 0

	for i := range messages {
		for j := range messages[i].ToolCalls {
			tc := &messages[i].ToolCalls[j]
			if tc.Name == lastTool {
				streak++
				if streak == 3 { // count at the threshold
					retries++
				}
			} else {
				lastTool = tc.Name
				streak = 1
			}
		}
	}
	return retries
}

// findInflectionPoint identifies the message index where output productivity
// starts declining. Uses a sliding window of 5 messages.
func findInflectionPoint(messages []Message) int {
	const windowSize = 5
	if len(messages) < windowSize*2 {
		return len(messages) / 2
	}

	// Compute output/input ratio for each window.
	type windowRatio struct {
		idx   int
		ratio float64
	}
	var ratios []windowRatio

	for i := 0; i <= len(messages)-windowSize; i++ {
		var totalIn, totalOut int64
		for j := i; j < i+windowSize; j++ {
			totalIn += int64(messages[j].InputTokens)
			totalOut += int64(messages[j].OutputTokens)
		}
		ratio := 0.0
		if totalIn > 0 {
			ratio = float64(totalOut) / float64(totalIn)
		}
		ratios = append(ratios, windowRatio{idx: i + windowSize/2, ratio: ratio})
	}

	if len(ratios) < 3 {
		return len(messages) / 2
	}

	// Find the point of maximum sustained decline.
	// Look for the first window where ratio drops >20% from its peak.
	var peakRatio float64
	var peakIdx int
	for i, wr := range ratios {
		if wr.ratio > peakRatio {
			peakRatio = wr.ratio
			peakIdx = i
		}
	}

	if peakRatio == 0 {
		return len(messages) / 2
	}

	for i := peakIdx + 1; i < len(ratios); i++ {
		decline := (peakRatio - ratios[i].ratio) / peakRatio
		if decline > 0.20 {
			return ratios[i].idx
		}
	}

	return 0 // no clear inflection found
}

// buildOverloadReason creates a human-readable explanation of the overload analysis.
func buildOverloadReason(a OverloadAnalysis) string {
	if a.Verdict == "healthy" {
		return "Session productivity remained stable throughout"
	}

	parts := make([]string, 0, 3)

	if a.OutputRatioDecay > 15 {
		parts = append(parts, "output productivity declined "+itoa(int(a.OutputRatioDecay))+"% in second half")
	}
	if a.ErrorRateGrowth > 5 {
		parts = append(parts, "error rate increased by "+itoa(int(a.ErrorRateGrowth))+"pp")
	}
	if a.RetryCount > 0 {
		parts = append(parts, itoa(a.RetryCount)+" tool retry sequences detected")
	}
	if a.LateErrorRate > 20 && a.ErrorRateGrowth <= 5 {
		parts = append(parts, "high late-session error rate ("+itoa(int(a.LateErrorRate))+"%)")
	}

	if len(parts) == 0 {
		return "Moderate quality signals detected"
	}

	reason := parts[0]
	for i := 1; i < len(parts); i++ {
		reason += "; " + parts[i]
	}
	return reason
}
