package session

// SessionFreshness captures how session quality degrades over its lifetime.
// Compactions are the primary quality degradation signal: after compaction,
// the model loses context fidelity. Each subsequent compaction makes it worse.
type SessionFreshness struct {
	// Per-phase breakdown: stats are measured between compaction events.
	// Phase 0 = before first compaction, Phase 1 = after first, etc.
	Phases []FreshnessPhase `json:"phases,omitempty"`

	// Quality degradation metrics.
	ErrorRateGrowth   float64 `json:"error_rate_growth"`   // % increase in error rate after first compaction
	RetryRateGrowth   float64 `json:"retry_rate_growth"`   // % increase in retry rate after first compaction
	OutputRatioDecay  float64 `json:"output_ratio_decay"`  // % decrease in output/input ratio after first compaction
	OptimalMessageIdx int     `json:"optimal_message_idx"` // estimated message index where quality peaks
	CompactionCount   int     `json:"compaction_count"`    // total compactions in this session
	TotalMessages     int     `json:"total_messages"`
	Recommendation    string  `json:"recommendation"` // actionable guidance
}

// FreshnessPhase captures quality stats for a segment of the session
// bounded by compaction events.
type FreshnessPhase struct {
	Phase         int     `json:"phase"`           // 0 = pre-compaction, 1 = after first, 2 = after second, etc.
	StartMsg      int     `json:"start_msg"`       // first message index in this phase
	EndMsg        int     `json:"end_msg"`         // last message index (exclusive)
	MessageCount  int     `json:"message_count"`   // messages in this phase
	ErrorRate     float64 `json:"error_rate"`      // tool errors / tool calls * 100 (0-100)
	RetryRate     float64 `json:"retry_rate"`      // retry messages / total messages * 100
	OutputRatio   float64 `json:"output_ratio"`    // avg output tokens / input tokens * 100
	AvgOutputToks int     `json:"avg_output_toks"` // avg output tokens per assistant message
	ToolCalls     int     `json:"tool_calls"`
	ToolErrors    int     `json:"tool_errors"`
}

// FreshnessAggregate summarizes freshness across multiple sessions.
type FreshnessAggregate struct {
	TotalSessions            int     `json:"total_sessions"`
	SessionsWithCompaction   int     `json:"sessions_with_compaction"`
	AvgCompactionsPerSession float64 `json:"avg_compactions_per_session"` // among compacted sessions
	AvgErrorRateGrowth       float64 `json:"avg_error_rate_growth"`
	AvgRetryRateGrowth       float64 `json:"avg_retry_rate_growth"`
	AvgOutputRatioDecay      float64 `json:"avg_output_ratio_decay"`
	AvgOptimalMessageIdx     int     `json:"avg_optimal_message_idx"`
	Recommendation           string  `json:"recommendation"`

	// Per-compaction-count buckets: how quality changes at each compaction depth.
	ByCompactionCount []CompactionDepthStats `json:"by_compaction_count,omitempty"`
}

// CompactionDepthStats shows quality at a given compaction depth (0, 1, 2, 3+).
type CompactionDepthStats struct {
	Depth        int     `json:"depth"`         // 0 = pre-compaction, 1 = after 1st, 2 = after 2nd, 3 = after 3+
	SessionCount int     `json:"session_count"` // sessions that reached this depth
	AvgErrorRate float64 `json:"avg_error_rate"`
	AvgRetryRate float64 `json:"avg_retry_rate"`
	AvgOutputRat float64 `json:"avg_output_ratio"`
}

// AnalyzeFreshness computes quality degradation metrics for a single session.
// It segments the session into phases separated by compaction events and measures
// how error rate, retry rate, and output ratio change across phases.
func AnalyzeFreshness(messages []Message, compactions CompactionSummary) SessionFreshness {
	result := SessionFreshness{
		CompactionCount: compactions.TotalCompactions,
		TotalMessages:   len(messages),
	}

	if len(messages) < 2 {
		return result
	}

	// Identify retry messages for retry rate calculation.
	retryMsgs := identifyRetryMessages(messages)

	// Build phase boundaries from compaction events.
	// Phase 0 starts at message 0, each compaction event starts a new phase.
	var boundaries []int // message indices where new phases start
	boundaries = append(boundaries, 0)
	for _, ce := range compactions.Events {
		if ce.AfterMessageIdx > 0 && ce.AfterMessageIdx < len(messages) {
			boundaries = append(boundaries, ce.AfterMessageIdx)
		}
	}
	boundaries = append(boundaries, len(messages)) // sentinel

	// Compute stats for each phase.
	for p := 0; p < len(boundaries)-1; p++ {
		start := boundaries[p]
		end := boundaries[p+1]

		phase := FreshnessPhase{
			Phase:    p,
			StartMsg: start,
			EndMsg:   end,
		}

		var totalOutput, totalInput int
		var assistMsgs int
		var retryCount int

		for i := start; i < end; i++ {
			msg := &messages[i]
			phase.MessageCount++

			if msg.Role == RoleAssistant {
				assistMsgs++
				totalOutput += msg.OutputTokens
				totalInput += msg.InputTokens
			}

			for j := range msg.ToolCalls {
				phase.ToolCalls++
				if msg.ToolCalls[j].State == ToolStateError {
					phase.ToolErrors++
				}
			}

			if retryMsgs[i] {
				retryCount++
			}
		}

		if phase.ToolCalls > 0 {
			phase.ErrorRate = float64(phase.ToolErrors) / float64(phase.ToolCalls) * 100
		}
		if phase.MessageCount > 0 {
			phase.RetryRate = float64(retryCount) / float64(phase.MessageCount) * 100
		}
		if totalInput > 0 {
			phase.OutputRatio = float64(totalOutput) / float64(totalInput) * 100
		}
		if assistMsgs > 0 {
			phase.AvgOutputToks = totalOutput / assistMsgs
		}

		result.Phases = append(result.Phases, phase)
	}

	// Compute quality degradation: compare phase 0 (pre-compaction) vs phase 1+ (post-compaction).
	if len(result.Phases) >= 2 {
		phase0 := result.Phases[0]
		// Aggregate all post-compaction phases.
		var postErrors, postCalls, postRetries, postMsgs int
		var postOutput, postInput int
		for _, ph := range result.Phases[1:] {
			postErrors += ph.ToolErrors
			postCalls += ph.ToolCalls
			postRetries += int(float64(ph.MessageCount) * ph.RetryRate / 100)
			postMsgs += ph.MessageCount
			postOutput += ph.AvgOutputToks * (ph.EndMsg - ph.StartMsg)
			postInput += int(float64(ph.OutputRatio) / 100 * float64(ph.AvgOutputToks) * float64(ph.EndMsg-ph.StartMsg))
		}

		var postErrorRate, postRetryRate, postOutputRatio float64
		if postCalls > 0 {
			postErrorRate = float64(postErrors) / float64(postCalls) * 100
		}
		if postMsgs > 0 {
			postRetryRate = float64(postRetries) / float64(postMsgs) * 100
		}
		if postInput > 0 {
			postOutputRatio = float64(postOutput) / float64(postInput) * 100
		} else if len(result.Phases) > 1 {
			// Use average of post-compaction phase output ratios.
			var sum float64
			for _, ph := range result.Phases[1:] {
				sum += ph.OutputRatio
			}
			postOutputRatio = sum / float64(len(result.Phases)-1)
		}

		// Error rate growth: how much worse is post-compaction vs pre-compaction?
		if phase0.ErrorRate > 0 {
			result.ErrorRateGrowth = ((postErrorRate - phase0.ErrorRate) / phase0.ErrorRate) * 100
		} else if postErrorRate > 0 {
			result.ErrorRateGrowth = 100 // went from 0% to >0%
		}

		// Retry rate growth.
		if phase0.RetryRate > 0 {
			result.RetryRateGrowth = ((postRetryRate - phase0.RetryRate) / phase0.RetryRate) * 100
		} else if postRetryRate > 0 {
			result.RetryRateGrowth = 100
		}

		// Output ratio decay (negative = output ratio decreased, which is worse).
		if phase0.OutputRatio > 0 {
			result.OutputRatioDecay = ((phase0.OutputRatio - postOutputRatio) / phase0.OutputRatio) * 100
		}
	}

	// Find optimal message index: the message where output/input ratio peaks.
	// Use a sliding window of 10 messages.
	const windowSize = 10
	if len(messages) >= windowSize {
		bestRatio := 0.0
		bestIdx := 0
		for start := 0; start <= len(messages)-windowSize; start++ {
			var winOutput, winInput int
			for i := start; i < start+windowSize; i++ {
				winOutput += messages[i].OutputTokens
				winInput += messages[i].InputTokens
			}
			if winInput > 0 {
				ratio := float64(winOutput) / float64(winInput)
				if ratio > bestRatio {
					bestRatio = ratio
					bestIdx = start + windowSize/2
				}
			}
		}
		result.OptimalMessageIdx = bestIdx
	}

	// Generate recommendation.
	result.Recommendation = buildFreshnessRecommendation(result)

	return result
}

// AggregateFreshness combines freshness results from multiple sessions.
func AggregateFreshness(results []SessionFreshness) FreshnessAggregate {
	agg := FreshnessAggregate{
		TotalSessions: len(results),
	}

	if len(results) == 0 {
		return agg
	}

	var (
		compactedCount                                 int
		totalCompactions                               int
		errorGrowthSum, retryGrowthSum, outputDecaySum float64
		optimalMsgSum                                  int
		growthCount                                    int // sessions with meaningful growth data
	)

	// Collect per-phase stats by compaction depth.
	depthStats := make(map[int]*struct {
		count     int
		errorSum  float64
		retrySum  float64
		outputSum float64
	})

	for _, r := range results {
		if r.CompactionCount > 0 {
			compactedCount++
			totalCompactions += r.CompactionCount
			if r.ErrorRateGrowth != 0 || r.RetryRateGrowth != 0 || r.OutputRatioDecay != 0 {
				errorGrowthSum += r.ErrorRateGrowth
				retryGrowthSum += r.RetryRateGrowth
				outputDecaySum += r.OutputRatioDecay
				growthCount++
			}
		}
		if r.OptimalMessageIdx > 0 {
			optimalMsgSum += r.OptimalMessageIdx
		}

		// Collect depth stats from phases.
		for _, ph := range r.Phases {
			depth := ph.Phase
			if depth > 3 {
				depth = 3 // bucket 3+ together
			}
			ds, ok := depthStats[depth]
			if !ok {
				ds = &struct {
					count     int
					errorSum  float64
					retrySum  float64
					outputSum float64
				}{}
				depthStats[depth] = ds
			}
			ds.count++
			ds.errorSum += ph.ErrorRate
			ds.retrySum += ph.RetryRate
			ds.outputSum += ph.OutputRatio
		}
	}

	agg.SessionsWithCompaction = compactedCount
	if compactedCount > 0 {
		agg.AvgCompactionsPerSession = float64(totalCompactions) / float64(compactedCount)
	}
	if growthCount > 0 {
		agg.AvgErrorRateGrowth = errorGrowthSum / float64(growthCount)
		agg.AvgRetryRateGrowth = retryGrowthSum / float64(growthCount)
		agg.AvgOutputRatioDecay = outputDecaySum / float64(growthCount)
	}
	if len(results) > 0 {
		agg.AvgOptimalMessageIdx = optimalMsgSum / len(results)
	}

	// Build per-depth stats.
	for depth := 0; depth <= 3; depth++ {
		ds, ok := depthStats[depth]
		if !ok {
			continue
		}
		entry := CompactionDepthStats{
			Depth:        depth,
			SessionCount: ds.count,
		}
		if ds.count > 0 {
			entry.AvgErrorRate = ds.errorSum / float64(ds.count)
			entry.AvgRetryRate = ds.retrySum / float64(ds.count)
			entry.AvgOutputRat = ds.outputSum / float64(ds.count)
		}
		agg.ByCompactionCount = append(agg.ByCompactionCount, entry)
	}

	// Aggregate recommendation.
	agg.Recommendation = buildAggregateRecommendation(agg)

	return agg
}

// buildFreshnessRecommendation generates actionable guidance for a single session.
func buildFreshnessRecommendation(f SessionFreshness) string {
	if f.CompactionCount == 0 {
		if f.TotalMessages > 100 {
			return "Long session without compaction — context still fresh but monitor for degradation."
		}
		return "No compaction detected — session context is fresh."
	}

	if f.ErrorRateGrowth > 50 {
		return "Error rate increased significantly after compaction. Consider starting a fresh session after " +
			formatMsgCount(f.Phases[0].MessageCount) + " messages."
	}
	if f.OutputRatioDecay > 30 {
		return "Output quality dropped after compaction. Session is past its productive peak — start fresh."
	}
	if f.CompactionCount >= 3 {
		return "Multiple compactions detected. Quality degrades with each compaction — start a new session."
	}
	if f.CompactionCount >= 2 {
		return "Two compactions detected — session approaching diminishing returns."
	}
	return "One compaction detected — monitor for quality changes."
}

// buildAggregateRecommendation generates cross-session guidance.
func buildAggregateRecommendation(agg FreshnessAggregate) string {
	if agg.SessionsWithCompaction == 0 {
		return "No sessions experienced compaction — context stays fresh across all sessions."
	}

	compactedPct := float64(agg.SessionsWithCompaction) / float64(agg.TotalSessions) * 100
	if compactedPct < 10 {
		return "Few sessions reach compaction. Session lengths are generally appropriate."
	}

	if agg.AvgErrorRateGrowth > 40 {
		if agg.AvgOptimalMessageIdx > 0 {
			return "Error rate increases ~" + formatPctInt(agg.AvgErrorRateGrowth) +
				"% after compaction. Optimal session length: ~" +
				formatMsgCount(agg.AvgOptimalMessageIdx) + " messages."
		}
		return "Error rate increases ~" + formatPctInt(agg.AvgErrorRateGrowth) + "% after compaction. Start fresh sessions sooner."
	}

	if agg.AvgOutputRatioDecay > 20 {
		return "Output quality drops ~" + formatPctInt(agg.AvgOutputRatioDecay) + "% after compaction. Consider shorter sessions."
	}

	return "Compaction affects " + formatPctInt(compactedPct) + "% of sessions. " +
		"Average " + formatFloat1(agg.AvgCompactionsPerSession) + " compactions per affected session."
}

// formatMsgCount formats a message count for recommendations.
func formatMsgCount(n int) string {
	if n >= 100 {
		return "~" + formatPctInt(float64(n))
	}
	return formatPctInt(float64(n))
}

// formatPctInt formats a float as a rounded integer string.
func formatPctInt(v float64) string {
	if v < 0 {
		v = -v
	}
	return formatInt(int(v + 0.5))
}

// formatFloat1 formats a float with 1 decimal place.
func formatFloat1(v float64) string {
	if v == float64(int(v)) {
		return formatInt(int(v))
	}
	s := make([]byte, 0, 8)
	i := int(v)
	d := int((v-float64(i))*10 + 0.5)
	if d >= 10 {
		i++
		d = 0
	}
	s = append(s, []byte(formatInt(i))...)
	s = append(s, '.')
	s = append(s, byte('0'+d))
	return string(s)
}

// formatInt formats an integer as a string without imports.
func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	digits := make([]byte, 0, 10)
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	if neg {
		digits = append(digits, '-')
	}
	// Reverse.
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}
