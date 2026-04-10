package diagnostic

import "fmt"

// ── Image detectors ─────────────────────────────────────────────────────────

func detectExpensiveScreenshots(r *InspectReport) []Problem {
	img := r.Images
	if img == nil || img.ToolReadImages < 5 || img.AvgTurnsInCtx < 10 {
		return nil
	}
	cost := img.EstImageCost
	if cost < 1.0 {
		return nil
	}
	sev := SeverityLow
	if cost > 20 {
		sev = SeverityHigh
	} else if cost > 5 {
		sev = SeverityMedium
	}
	return []Problem{{
		ID:       ProblemExpensiveScreenshots,
		Severity: sev,
		Category: CategoryImages,
		Title:    "Expensive screenshots in context",
		Observation: fmt.Sprintf("%d images read via tool, avg %.0f assistant turns in context before compaction.",
			img.ToolReadImages, img.AvgTurnsInCtx),
		Impact: fmt.Sprintf("~%.1fM billed input tokens from images, est. $%.2f at $3/M input.",
			float64(img.TotalBilledTok)/1_000_000, cost),
		Metric:     cost,
		MetricUnit: "USD",
	}}
}

func detectOversizedScreenshots(r *InspectReport) []Problem {
	img := r.Images
	if img == nil || img.SimctlCaptures < 3 {
		return nil
	}
	// If sips resizes are present but at 1000px, images are ~1500 tokens.
	// At 500px they'd be ~500 tokens. Check if there are captures without resizes.
	if img.SipsResizes > 0 && img.SipsResizes >= img.SimctlCaptures*8/10 {
		// Most screenshots are resized — this is the "resized but still too big" case.
		// Only flag if there are many images (>20) since the impact is proportional.
		if img.ToolReadImages < 20 {
			return nil
		}
		savedTokens := int64(img.ToolReadImages) * 1000 // could save ~1000 tokens per image
		savedCost := float64(savedTokens) * float64(img.AvgTurnsInCtx) * 3.0 / 1_000_000
		if savedCost < 2.0 {
			return nil
		}
		return []Problem{{
			ID:       ProblemOversizedScreenshots,
			Severity: SeverityMedium,
			Category: CategoryImages,
			Title:    "Screenshots resized but resolution still high",
			Observation: fmt.Sprintf("%d screenshots resized via sips. Current estimated tokens: ~1500/image.",
				img.SipsResizes),
			Impact: fmt.Sprintf("Reducing from 1000px to 500px max dimension would save ~%dK tokens/image, est. $%.2f total.",
				savedTokens/1000/int64(img.ToolReadImages), savedCost),
			Metric:     savedCost,
			MetricUnit: "USD",
		}}
	}
	return nil
}

func detectUnresizedScreenshots(r *InspectReport) []Problem {
	img := r.Images
	if img == nil || img.SimctlCaptures < 3 {
		return nil
	}
	// Screenshots taken but not resized — raw retina screenshots are 2-4x larger
	unresized := img.SimctlCaptures - img.SipsResizes
	if unresized <= 0 {
		return nil
	}
	ratio := float64(unresized) / float64(img.SimctlCaptures)
	if ratio < 0.3 {
		return nil
	}
	return []Problem{{
		ID:       ProblemUnresizedScreenshots,
		Severity: SeverityMedium,
		Category: CategoryImages,
		Title:    "Screenshots taken without resize",
		Observation: fmt.Sprintf("%d of %d screenshots (%0.f%%) captured without sips resize. Raw retina screenshots are ~3000-6000 tokens.",
			unresized, img.SimctlCaptures, ratio*100),
		Impact:     fmt.Sprintf("Resizing to 500px would reduce tokens by ~75%% per unresized image."),
		Metric:     float64(unresized),
		MetricUnit: "count",
	}}
}

// ── Compaction detectors ────────────────────────────────────────────────────

func detectFrequentCompaction(r *InspectReport) []Problem {
	c := r.Compaction
	if c == nil || c.Count < 3 || c.PerUserMsg < 0.15 {
		return nil
	}
	sev := SeverityLow
	if c.PerUserMsg > 0.3 {
		sev = SeverityHigh
	} else if c.PerUserMsg > 0.2 {
		sev = SeverityMedium
	}
	return []Problem{{
		ID:       ProblemFrequentCompaction,
		Severity: sev,
		Category: CategoryCompaction,
		Title:    "Frequent context compaction",
		Observation: fmt.Sprintf("%d compactions over %d user messages (%.3f per user message). %s tokens lost total.",
			c.Count, r.UserMsgs, c.PerUserMsg, fmtInt(c.TotalTokensLost)),
		Impact: fmt.Sprintf("Each compaction discards context and costs tokens to rebuild. %d compactions × avg rebuild cost.",
			c.Count),
		Metric:     c.PerUserMsg,
		MetricUnit: "ratio",
	}}
}

func detectContextNearLimit(r *InspectReport) []Problem {
	c := r.Compaction
	if c == nil || c.Count < 2 || c.AvgBeforeTokens == 0 {
		return nil
	}
	// If average context before compaction is >160K, the agent is consistently hitting the ceiling
	if c.AvgBeforeTokens < 150000 {
		return nil
	}
	sev := SeverityMedium
	if c.AvgBeforeTokens > 180000 {
		sev = SeverityHigh
	}
	return []Problem{{
		ID:       ProblemContextNearLimit,
		Severity: sev,
		Category: CategoryCompaction,
		Title:    "Context consistently near window limit",
		Observation: fmt.Sprintf("Average context at compaction: %sK tokens. %d of %d compactions occurred above 150K tokens.",
			fmtK(c.AvgBeforeTokens), countAbove(c.Events, 150000), c.Count),
		Impact:     "Context near the window limit reduces cache efficiency and increases compaction frequency.",
		Metric:     float64(c.AvgBeforeTokens),
		MetricUnit: "tokens",
	}}
}

func detectCompactionAccelerating(r *InspectReport) []Problem {
	c := r.Compaction
	if c == nil || c.Count < 6 {
		return nil
	}
	// Compare first-half rate vs last-quartile rate
	if c.LastQuartileRate <= c.PerUserMsg*1.3 {
		return nil // not accelerating
	}
	return []Problem{{
		ID:       ProblemCompactionAccelerating,
		Severity: SeverityMedium,
		Category: CategoryCompaction,
		Title:    "Compaction rate accelerating in later session",
		Observation: fmt.Sprintf("Overall compaction rate: %.3f/user_msg. Last quartile rate: %.3f/user_msg (%.0f%% higher).",
			c.PerUserMsg, c.LastQuartileRate, (c.LastQuartileRate/c.PerUserMsg-1)*100),
		Impact:     "Accelerating compaction rate indicates growing context pressure, possibly from accumulated tool output or conversation length.",
		Metric:     c.LastQuartileRate,
		MetricUnit: "ratio",
	}}
}

// ── Command detectors ───────────────────────────────────────────────────────

func detectVerboseCommands(r *InspectReport) []Problem {
	cmd := r.Commands
	if cmd == nil || cmd.TotalOutputBytes < 100_000 {
		return nil
	}
	// Find the top command by output
	if len(cmd.TopByOutput) == 0 {
		return nil
	}
	top := cmd.TopByOutput[0]
	if top.TotalBytes < 50_000 {
		return nil
	}
	sev := SeverityLow
	if cmd.TotalOutputBytes > 1_000_000 {
		sev = SeverityHigh
	} else if cmd.TotalOutputBytes > 500_000 {
		sev = SeverityMedium
	}
	return []Problem{{
		ID:       ProblemVerboseCommands,
		Severity: sev,
		Category: CategoryCommands,
		Title:    "Verbose command output consuming context",
		Observation: fmt.Sprintf("Total command output: %s across %d commands. Top command: '%s' with %s across %d invocations.",
			fmtBytes(cmd.TotalOutputBytes), cmd.TotalCommands, top.Command, fmtBytes(top.TotalBytes), top.Invocations),
		Impact:     fmt.Sprintf("Est. %s tokens consumed by command output.", fmtTok(cmd.TotalOutputTok)),
		Metric:     float64(cmd.TotalOutputBytes),
		MetricUnit: "bytes",
	}}
}

func detectRepeatedCommands(r *InspectReport) []Problem {
	cmd := r.Commands
	if cmd == nil || cmd.TotalCommands < 10 || cmd.RepeatedRatio < 0.3 {
		return nil
	}
	duplicates := cmd.TotalCommands - cmd.UniqueCommands
	sev := SeverityLow
	if cmd.RepeatedRatio > 0.5 {
		sev = SeverityMedium
	}
	return []Problem{{
		ID:       ProblemRepeatedCommands,
		Severity: sev,
		Category: CategoryCommands,
		Title:    "High command repetition rate",
		Observation: fmt.Sprintf("%d of %d commands (%.0f%%) are duplicates. %d unique commands.",
			duplicates, cmd.TotalCommands, cmd.RepeatedRatio*100, cmd.UniqueCommands),
		Impact:     "Repeated commands consume tokens for both input (sending the command) and output (receiving identical results).",
		Metric:     cmd.RepeatedRatio,
		MetricUnit: "ratio",
	}}
}

func detectLongRunningCommands(r *InspectReport) []Problem {
	cmd := r.Commands
	if cmd == nil {
		return nil
	}
	var longOnes []string
	for _, c := range cmd.TopByOutput {
		if c.AvgDuration > 30000 { // >30 seconds average
			longOnes = append(longOnes, fmt.Sprintf("%s (avg %ds)", c.Command, c.AvgDuration/1000))
		}
	}
	if len(longOnes) == 0 {
		return nil
	}
	return []Problem{{
		ID:       ProblemLongRunningCommands,
		Severity: SeverityLow,
		Category: CategoryCommands,
		Title:    "Long-running commands detected",
		Observation: fmt.Sprintf("%d commands with avg duration >30s: %s",
			len(longOnes), JoinMax(longOnes, 3)),
		Impact:     "Long-running commands block the agent and may timeout, wasting the tokens already spent on context.",
		Metric:     float64(len(longOnes)),
		MetricUnit: "count",
	}}
}

// ── Token detectors ─────────────────────────────────────────────────────────

func detectLowCacheUtilization(r *InspectReport) []Problem {
	t := r.Tokens
	if t == nil || t.Input < 100000 {
		return nil
	}
	// Low cache means re-processing the same context repeatedly
	if t.CachePct >= 50 {
		return nil
	}
	// Provider must support caching (check if any cache data exists)
	if t.CacheRead == 0 && t.CacheWrite == 0 {
		return nil // provider doesn't report cache metrics
	}
	sev := SeverityLow
	if t.CachePct < 20 {
		sev = SeverityHigh
	} else if t.CachePct < 35 {
		sev = SeverityMedium
	}
	return []Problem{{
		ID:       ProblemLowCacheUtilization,
		Severity: sev,
		Category: CategoryTokens,
		Title:    "Low prompt cache utilization",
		Observation: fmt.Sprintf("Cache read: %.1f%% of input tokens (%s of %s). Cache write: %s.",
			t.CachePct, fmtTok(int64(t.CacheRead)), fmtTok(int64(t.Input)), fmtTok(int64(t.CacheWrite))),
		Impact:     "Low cache utilization means the model re-processes unchanged context on every turn, paying full input price instead of cache price.",
		Metric:     t.CachePct,
		MetricUnit: "percent",
	}}
}

func detectHighInputRatio(r *InspectReport) []Problem {
	t := r.Tokens
	if t == nil || t.Output == 0 || t.Input < 100000 {
		return nil
	}
	if t.InputOutputRatio < 100 {
		return nil
	}
	sev := SeverityLow
	if t.InputOutputRatio > 500 {
		sev = SeverityHigh
	} else if t.InputOutputRatio > 200 {
		sev = SeverityMedium
	}
	return []Problem{{
		ID:       ProblemHighInputRatio,
		Severity: sev,
		Category: CategoryTokens,
		Title:    "Very high input/output token ratio",
		Observation: fmt.Sprintf("Input/output ratio: %.0f:1 (%s input, %s output). The model processes %.0f× more tokens than it generates.",
			t.InputOutputRatio, fmtTok(int64(t.Input)), fmtTok(int64(t.Output)), t.InputOutputRatio),
		Impact:     "High input ratio indicates the context is dominated by historical conversation that could be compacted or split into sub-sessions.",
		Metric:     t.InputOutputRatio,
		MetricUnit: "ratio",
	}}
}

func detectContextThrashing(r *InspectReport) []Problem {
	c := r.Compaction
	if c == nil || c.Count < 4 {
		return nil
	}
	// Context thrashing: small intervals between compactions (<30 messages)
	if c.IntervalMedian == 0 || c.IntervalMedian > 30 {
		return nil
	}
	return []Problem{{
		ID:       ProblemContextThrashing,
		Severity: SeverityHigh,
		Category: CategoryTokens,
		Title:    "Context thrashing — rapid fill/compact cycles",
		Observation: fmt.Sprintf("Median interval between compactions: %d messages. Context fills and compacts rapidly.",
			c.IntervalMedian),
		Impact: fmt.Sprintf("%d compactions with median gap of %d messages. Each cycle loses tokens and degrades response quality.",
			c.Count, c.IntervalMedian),
		Metric:     float64(c.IntervalMedian),
		MetricUnit: "messages",
	}}
}

// ── Tool error detectors ────────────────────────────────────────────────────

func detectToolErrorLoops(r *InspectReport) []Problem {
	te := r.ToolErrors
	if te == nil || len(te.ErrorLoops) == 0 {
		return nil
	}
	totalWasted := 0
	for _, l := range te.ErrorLoops {
		totalWasted += l.TotalTokens
	}
	sev := SeverityMedium
	if len(te.ErrorLoops) > 3 || te.ConsecutiveMax > 5 {
		sev = SeverityHigh
	}
	return []Problem{{
		ID:       ProblemToolErrorLoops,
		Severity: sev,
		Category: CategoryToolErrors,
		Title:    "Tool error retry loops detected",
		Observation: fmt.Sprintf("%d error loops detected (3+ consecutive failures on same tool). Longest: %d consecutive errors.",
			len(te.ErrorLoops), te.ConsecutiveMax),
		Impact: fmt.Sprintf("Est. %s tokens consumed in failed tool calls during loops.",
			fmtTok(int64(totalWasted))),
		Metric:     float64(len(te.ErrorLoops)),
		MetricUnit: "count",
	}}
}

func detectAbandonedToolCalls(r *InspectReport) []Problem {
	te := r.ToolErrors
	if te == nil || te.ErrorRate < 0.15 || te.TotalToolCalls < 20 {
		return nil
	}
	sev := SeverityLow
	if te.ErrorRate > 0.3 {
		sev = SeverityHigh
	} else if te.ErrorRate > 0.2 {
		sev = SeverityMedium
	}
	return []Problem{{
		ID:       ProblemAbandonedToolCalls,
		Severity: sev,
		Category: CategoryToolErrors,
		Title:    "High tool call error rate",
		Observation: fmt.Sprintf("%d of %d tool calls (%.1f%%) resulted in errors.",
			te.ErrorCount, te.TotalToolCalls, te.ErrorRate*100),
		Impact:     "Failed tool calls consume context tokens without producing useful work.",
		Metric:     te.ErrorRate,
		MetricUnit: "ratio",
	}}
}

// ── Behavioral pattern detectors ────────────────────────────────────────────

func detectYoloEditing(r *InspectReport) []Problem {
	p := r.Patterns
	if p == nil || p.WriteWithoutReadCount < 5 {
		return nil
	}
	sev := SeverityLow
	if p.WriteWithoutReadCount > 20 {
		sev = SeverityMedium
	}
	return []Problem{{
		ID:       ProblemYoloEditing,
		Severity: sev,
		Category: CategoryPatterns,
		Title:    "Files edited without prior read",
		Observation: fmt.Sprintf("%d files were edited without being read first in this session.",
			p.WriteWithoutReadCount),
		Impact:     "Editing without reading risks overwriting content and may cause the agent to retry when edits fail on unexpected content.",
		Metric:     float64(p.WriteWithoutReadCount),
		MetricUnit: "count",
	}}
}

func detectExcessiveGlobbing(r *InspectReport) []Problem {
	p := r.Patterns
	if p == nil || p.GlobStormCount == 0 {
		return nil
	}
	return []Problem{{
		ID:       ProblemExcessiveGlobbing,
		Severity: SeverityLow,
		Category: CategoryPatterns,
		Title:    "Glob/search storms without action",
		Observation: fmt.Sprintf("%d sequences of 6+ consecutive search/glob calls without acting on results.",
			p.GlobStormCount),
		Impact:     "Each search call consumes context tokens. Long search sequences without action indicate the agent is exploring without a clear target.",
		Metric:     float64(p.GlobStormCount),
		MetricUnit: "count",
	}}
}

func detectConversationDrift(r *InspectReport) []Problem {
	p := r.Patterns
	if p == nil {
		return nil
	}
	// Detect based on user corrections + long runs
	if p.UserCorrectionCount < 5 && p.LongestRunLength < 30 {
		return nil
	}
	var observations []string
	if p.UserCorrectionCount >= 5 {
		observations = append(observations, fmt.Sprintf("%d consecutive user messages (corrections/clarifications)", p.UserCorrectionCount))
	}
	if p.LongestRunLength >= 30 {
		observations = append(observations, fmt.Sprintf("longest assistant run: %d messages without user input", p.LongestRunLength))
	}
	sev := SeverityLow
	if p.UserCorrectionCount > 10 || p.LongestRunLength > 50 {
		sev = SeverityMedium
	}
	return []Problem{{
		ID:          ProblemConversationDrift,
		Severity:    sev,
		Category:    CategoryPatterns,
		Title:       "Conversation drift indicators",
		Observation: JoinMax(observations, 3),
		Impact:      "Frequent corrections and long unguided runs correlate with context inefficiency and compaction.",
		Metric:      float64(p.UserCorrectionCount + p.LongestRunLength),
		MetricUnit:  "composite",
	}}
}
