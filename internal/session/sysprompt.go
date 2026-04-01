package session

import "fmt"

// SystemPromptEstimate estimates the system prompt token count for a session.
//
// System prompts (CLAUDE.md, AGENTS.md, MCP tool descriptions, etc.) are NOT
// stored in session data. The best proxy is:
//
//	Method A: CacheWriteTokens on the first assistant message — the initial
//	cache write contains the system prompt + first user message.
//
//	Method B: InputTokens on the first assistant message minus an estimate
//	of the first user message content.
//
// Returns 0 if the session has no usable data.
func SystemPromptEstimate(msgs []Message) int {
	if len(msgs) == 0 {
		return 0
	}

	// Find the first assistant message.
	var firstAssist *Message
	var firstUserContentTokens int
	for i := range msgs {
		if msgs[i].Role == RoleUser && firstUserContentTokens == 0 {
			firstUserContentTokens = roughTokenEstimate(msgs[i].Content)
		}
		if msgs[i].Role == RoleAssistant {
			firstAssist = &msgs[i]
			break
		}
	}

	if firstAssist == nil {
		return 0
	}

	// Method A: CacheWriteTokens on first assistant message.
	// This is the most reliable proxy — the first cache write contains
	// the system prompt + first user content.
	if firstAssist.CacheWriteTokens > 0 {
		estimate := firstAssist.CacheWriteTokens - firstUserContentTokens
		if estimate < 0 {
			estimate = firstAssist.CacheWriteTokens
		}
		return estimate
	}

	// Method B: InputTokens on first assistant message minus user content estimate.
	// Less reliable but works when cache data isn't available.
	if firstAssist.InputTokens > 0 && firstUserContentTokens > 0 {
		estimate := firstAssist.InputTokens - firstUserContentTokens
		if estimate < 500 { // minimum sanity check — system prompts are always >500 tokens
			return 0
		}
		return estimate
	}

	// Fallback: use raw InputTokens if it's the first message
	// (likely contains mostly system prompt).
	if firstAssist.InputTokens > 500 {
		return firstAssist.InputTokens
	}

	return 0
}

// roughTokenEstimate gives a rough token count from text content.
// Uses the common ~4 characters per token approximation.
func roughTokenEstimate(content string) int {
	if len(content) == 0 {
		return 0
	}
	return len(content) / 4
}

// SystemPromptImpact analyzes the relationship between system prompt size
// and session quality metrics.
type SystemPromptImpact struct {
	// Per-session data points.
	TotalSessions int `json:"total_sessions"`  // sessions with valid estimates
	AvgEstimate   int `json:"avg_estimate"`    // average system prompt size (tokens)
	MinEstimate   int `json:"min_estimate"`    // smallest prompt seen
	MaxEstimate   int `json:"max_estimate"`    // largest prompt seen
	MedianEst     int `json:"median_estimate"` // median prompt size

	// Cost impact.
	AvgPromptCostPct  float64 `json:"avg_prompt_cost_pct"` // avg % of session input tokens consumed by system prompt
	TotalPromptTokens int     `json:"total_prompt_tokens"` // total system prompt tokens across all sessions

	// Size buckets for distribution analysis.
	SmallCount  int `json:"small_count"`  // sessions with < 3K tokens prompt
	MediumCount int `json:"medium_count"` // sessions with 3K-8K tokens prompt
	LargeCount  int `json:"large_count"`  // sessions with > 8K tokens prompt

	// Quality correlation (per size bucket).
	SmallErrorRate  float64 `json:"small_error_rate"`  // avg error rate for small prompts
	MediumErrorRate float64 `json:"medium_error_rate"` // avg error rate for medium prompts
	LargeErrorRate  float64 `json:"large_error_rate"`  // avg error rate for large prompts

	SmallRetryRate  float64 `json:"small_retry_rate"`  // avg retry rate for small prompts
	MediumRetryRate float64 `json:"medium_retry_rate"` // avg retry rate for medium prompts
	LargeRetryRate  float64 `json:"large_retry_rate"`  // avg retry rate for large prompts

	// Trend: change in prompt size over time.
	Trend string `json:"trend"` // "growing", "stable", "shrinking"

	// Recommendation.
	Recommendation string `json:"recommendation"`
}

// SessionPromptData captures system prompt info and quality metrics for one session.
type SessionPromptData struct {
	PromptTokens int
	TotalInput   int
	ErrorRate    float64 // tool errors / tool calls * 100
	RetryRate    float64 // retry messages / total messages * 100
	CreatedAt    int64   // epoch seconds for trend analysis
}

// AnalyzeSystemPromptImpact computes the impact of system prompt sizes on
// session quality. This is a pure domain function.
func AnalyzeSystemPromptImpact(data []SessionPromptData) SystemPromptImpact {
	result := SystemPromptImpact{}

	if len(data) == 0 {
		result.Recommendation = "No session data available for system prompt analysis."
		return result
	}

	// Filter out sessions with no valid estimate.
	var valid []SessionPromptData
	for _, d := range data {
		if d.PromptTokens > 0 {
			valid = append(valid, d)
		}
	}

	if len(valid) == 0 {
		result.Recommendation = "No sessions have system prompt estimates. Cache write data may not be available."
		return result
	}

	result.TotalSessions = len(valid)

	// Compute basic stats.
	var sumTokens int
	var sumCostPct float64
	estimates := make([]int, 0, len(valid))

	result.MinEstimate = valid[0].PromptTokens
	result.MaxEstimate = valid[0].PromptTokens

	for _, d := range valid {
		estimates = append(estimates, d.PromptTokens)
		sumTokens += d.PromptTokens

		if d.PromptTokens < result.MinEstimate {
			result.MinEstimate = d.PromptTokens
		}
		if d.PromptTokens > result.MaxEstimate {
			result.MaxEstimate = d.PromptTokens
		}

		if d.TotalInput > 0 {
			sumCostPct += float64(d.PromptTokens) / float64(d.TotalInput) * 100
		}
	}

	result.AvgEstimate = sumTokens / len(valid)
	result.TotalPromptTokens = sumTokens
	result.AvgPromptCostPct = sumCostPct / float64(len(valid))
	result.MedianEst = medianInt(estimates)

	// Size bucket analysis.
	var smallErrors, medErrors, largeErrors float64
	var smallRetries, medRetries, largeRetries float64
	var smallN, medN, largeN int

	for _, d := range valid {
		switch {
		case d.PromptTokens < 3000:
			result.SmallCount++
			smallErrors += d.ErrorRate
			smallRetries += d.RetryRate
			smallN++
		case d.PromptTokens <= 8000:
			result.MediumCount++
			medErrors += d.ErrorRate
			medRetries += d.RetryRate
			medN++
		default:
			result.LargeCount++
			largeErrors += d.ErrorRate
			largeRetries += d.RetryRate
			largeN++
		}
	}

	if smallN > 0 {
		result.SmallErrorRate = smallErrors / float64(smallN)
		result.SmallRetryRate = smallRetries / float64(smallN)
	}
	if medN > 0 {
		result.MediumErrorRate = medErrors / float64(medN)
		result.MediumRetryRate = medRetries / float64(medN)
	}
	if largeN > 0 {
		result.LargeErrorRate = largeErrors / float64(largeN)
		result.LargeRetryRate = largeRetries / float64(largeN)
	}

	// Trend analysis: compare first half vs second half (by time).
	result.Trend = computePromptTrend(valid)

	// Generate recommendation.
	result.Recommendation = buildPromptRecommendation(result)

	return result
}

// computePromptTrend determines if system prompt sizes are growing, stable, or shrinking.
func computePromptTrend(data []SessionPromptData) string {
	if len(data) < 4 {
		return "stable" // not enough data
	}

	// Sort-free approach: compare first quarter vs last quarter by creation time.
	// Data should already be somewhat time-ordered from the service layer.
	quarter := len(data) / 4
	if quarter == 0 {
		quarter = 1
	}

	var earlySum, lateSum int
	var earlyN, lateN int
	for i := 0; i < quarter; i++ {
		earlySum += data[i].PromptTokens
		earlyN++
	}
	for i := len(data) - quarter; i < len(data); i++ {
		lateSum += data[i].PromptTokens
		lateN++
	}

	if earlyN == 0 || lateN == 0 {
		return "stable"
	}

	earlyAvg := float64(earlySum) / float64(earlyN)
	lateAvg := float64(lateSum) / float64(lateN)

	if earlyAvg == 0 {
		return "stable"
	}

	changePct := (lateAvg - earlyAvg) / earlyAvg * 100
	switch {
	case changePct > 20:
		return "growing"
	case changePct < -20:
		return "shrinking"
	default:
		return "stable"
	}
}

// buildPromptRecommendation generates actionable advice based on prompt analysis.
func buildPromptRecommendation(impact SystemPromptImpact) string {
	if impact.TotalSessions == 0 {
		return "No data available."
	}

	// Large prompts with higher error rates.
	if impact.LargeCount > 0 && impact.LargeErrorRate > impact.SmallErrorRate*1.2 && impact.SmallCount > 0 {
		return "Sessions with large system prompts (>8K tokens) have higher error rates. " +
			"Consider trimming CLAUDE.md or splitting MCP server configurations to reduce prompt size."
	}

	// Growing trend.
	if impact.Trend == "growing" && impact.AvgEstimate > 5000 {
		return "System prompt size is growing over time (currently averaging " +
			formatTokenCount(impact.AvgEstimate) + " tokens). " +
			"This increases input cost per session. Review CLAUDE.md for unnecessary instructions."
	}

	// Very large average.
	if impact.AvgEstimate > 8000 {
		return "Average system prompt is " + formatTokenCount(impact.AvgEstimate) + " tokens — " +
			"this consumes " + formatPct(impact.AvgPromptCostPct) + "% of input tokens per session. " +
			"Consider reducing MCP tool descriptions or simplifying CLAUDE.md."
	}

	// Cost percentage high.
	if impact.AvgPromptCostPct > 30 {
		return "System prompts account for " + formatPct(impact.AvgPromptCostPct) + "% of input tokens on average. " +
			"Reducing prompt size could lower per-session cost significantly."
	}

	return "System prompt size is within normal range (" + formatTokenCount(impact.AvgEstimate) +
		" tokens average). No action needed."
}

// formatTokenCount formats a token count for display (e.g. "3.2K").
func formatTokenCount(tokens int) string {
	if tokens >= 1000 {
		k := float64(tokens) / 1000
		if k == float64(int(k)) {
			return fmt.Sprintf("%dK", int(k))
		}
		return fmt.Sprintf("%.1fK", k)
	}
	return fmt.Sprintf("%d", tokens)
}

// formatPct formats a percentage for display.
func formatPct(pct float64) string {
	if pct == float64(int(pct)) {
		return fmt.Sprintf("%d", int(pct))
	}
	return fmt.Sprintf("%.1f", pct)
}
