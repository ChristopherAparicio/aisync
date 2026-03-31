package session

import (
	"math"
	"sort"
)

// CompactionThreshold defines the minimum input token drop ratio (as fraction) to
// consider a compaction event. A drop from 100K to 50K is a 0.50 ratio — anything
// below this threshold triggers detection.
const CompactionThreshold = 0.50

// CompactionMinBaseline is the minimum input tokens on the "before" message for
// compaction detection to apply. Avoids false positives on small conversations.
const CompactionMinBaseline = 10000

// DetectCompactions scans messages for compaction events using a token-drop heuristic.
//
// A compaction is detected when consecutive messages show a >50% drop in input tokens
// with a baseline above 10K tokens. This works because:
//   - InputTokens on each message represents the cumulative context sent to the model
//   - When the provider compacts, the context is summarized and the token count drops significantly
//   - Median observed drop in production is ~92% (context resets almost completely)
//
// Secondary confirmation via cache invalidation: if the post-compaction message has
// cache_read_tokens ~0 while the pre-compaction message had high cache_read, this
// confirms the compaction (entire conversation is now new content).
//
// The inputRate parameter is the cost per input token (e.g. $15/M = 0.000015) for
// estimating the rebuild cost. Pass 0 to skip cost estimation.
func DetectCompactions(messages []Message, inputRate float64) CompactionSummary {
	var summary CompactionSummary
	var prevInput int
	var prevMsgIdx int
	var prevCacheRead int
	var lastCompactionIdx int // track for sawtooth cycle detection

	for i, msg := range messages {
		// Only consider messages with token data — typically assistant messages.
		if msg.InputTokens == 0 {
			continue
		}

		inputTokens := msg.InputTokens

		if prevInput > CompactionMinBaseline {
			dropRatio := float64(inputTokens) / float64(prevInput)
			if dropRatio < CompactionThreshold {
				tokensLost := prevInput - inputTokens
				dropPct := (1.0 - dropRatio) * 100

				// Check cache invalidation: cache_read should drop significantly after compaction.
				cacheInvalidated := false
				if prevCacheRead > 1000 && msg.CacheReadTokens < prevCacheRead/5 {
					cacheInvalidated = true
				}

				// Estimate rebuild cost: the tokens lost will need to be re-sent as new input.
				rebuildCost := float64(tokensLost) * inputRate

				event := CompactionEvent{
					BeforeMessageIdx:  prevMsgIdx,
					AfterMessageIdx:   i,
					BeforeInputTokens: prevInput,
					AfterInputTokens:  inputTokens,
					TokensLost:        tokensLost,
					DropPercent:       dropPct,
					CacheInvalidated:  cacheInvalidated,
					RebuildCost:       rebuildCost,
				}

				summary.Events = append(summary.Events, event)
				summary.TotalTokensLost += tokensLost
				summary.TotalRebuildCost += rebuildCost

				lastCompactionIdx = i
			}
		}

		prevInput = inputTokens
		prevMsgIdx = i
		prevCacheRead = msg.CacheReadTokens
	}

	summary.TotalCompactions = len(summary.Events)
	if summary.TotalCompactions == 0 {
		return summary
	}

	// Compute average drop percent.
	var totalDrop float64
	for _, e := range summary.Events {
		totalDrop += e.DropPercent
	}
	summary.AvgDropPercent = totalDrop / float64(summary.TotalCompactions)

	// Compute median drop percent.
	summary.MedianDropPercent = medianFloat64(summary.Events)

	// Sawtooth cycle detection: a "cycle" is fill → compact → rebuild.
	// Count as number of compaction events (each marks the end of one fill cycle).
	summary.SawtoothCycles = summary.TotalCompactions

	// Compute average messages-to-fill (messages between start/previous-compaction and this one).
	computeFillAndRecoveryStats(messages, &summary, lastCompactionIdx)

	return summary
}

// medianFloat64 computes the median DropPercent from compaction events.
func medianFloat64(events []CompactionEvent) float64 {
	n := len(events)
	if n == 0 {
		return 0
	}
	sorted := make([]float64, n)
	for i, e := range events {
		sorted[i] = e.DropPercent
	}
	sort.Float64s(sorted)
	if n%2 == 0 {
		return (sorted[n/2-1] + sorted[n/2]) / 2
	}
	return sorted[n/2]
}

// computeFillAndRecoveryStats calculates average messages before compaction
// and average recovery tokens after compaction.
func computeFillAndRecoveryStats(messages []Message, summary *CompactionSummary, _ int) {
	if summary.TotalCompactions == 0 {
		return
	}

	// Messages to fill: gap between compaction events (or start of session).
	var totalMsgsBefore int
	prevCompactionMsgIdx := 0
	for _, e := range summary.Events {
		totalMsgsBefore += e.BeforeMessageIdx - prevCompactionMsgIdx
		prevCompactionMsgIdx = e.AfterMessageIdx
	}
	summary.AvgMessagesToFill = totalMsgsBefore / summary.TotalCompactions

	// Recovery tokens: how much context is rebuilt after each compaction.
	// Look for the peak tokens reached before the next compaction (or end of session).
	var totalRecovery int
	for i, e := range summary.Events {
		peakAfter := e.AfterInputTokens
		// Scan messages after compaction until next compaction or end.
		nextCompactionIdx := len(messages)
		if i+1 < len(summary.Events) {
			nextCompactionIdx = summary.Events[i+1].BeforeMessageIdx
		}
		for j := e.AfterMessageIdx; j < nextCompactionIdx && j < len(messages); j++ {
			if messages[j].InputTokens > peakAfter {
				peakAfter = messages[j].InputTokens
			}
		}
		totalRecovery += peakAfter - e.AfterInputTokens
	}
	summary.AvgRecoveryTokens = totalRecovery / summary.TotalCompactions

	// Round costs.
	summary.TotalRebuildCost = math.Round(summary.TotalRebuildCost*10000) / 10000
}
