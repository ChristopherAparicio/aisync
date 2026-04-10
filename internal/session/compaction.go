package session

import (
	"math"
	"sort"
)

// CompactionThreshold defines the primary drop ratio threshold.
// A drop from 100K to 54K = ratio 0.54 < 0.55 → triggers detection.
// Relaxed from 0.50 to capture 45–50% drops observed in production (Section 8.1).
const CompactionThreshold = 0.55

// CompactionSecondaryThreshold is the secondary threshold for large absolute drops.
// Triggers when drop ratio < 0.65 AND absolute delta > CompactionMinAbsoluteDrop
// AND cache invalidation is confirmed. Captures 35–65% drops with significant absolute delta.
const CompactionSecondaryThreshold = 0.65

// CompactionMinAbsoluteDrop is the minimum absolute token delta required for the
// secondary trigger. Prevents false positives from small drops in large contexts.
const CompactionMinAbsoluteDrop = 40000

// CompactionMinBaseline is the minimum input tokens on the "before" message for
// compaction detection to apply. Avoids false positives on small conversations.
const CompactionMinBaseline = 10000

// CompactionCascadeWindow is the maximum message gap between consecutive detected
// drops to merge them into a single 2-pass cascade event.
const CompactionCascadeWindow = 3

// DetectCompactions scans messages for compaction events using a token-drop heuristic.
//
// Detection uses two thresholds:
//   - Primary: drop ratio < 0.55 with baseline > 10K tokens (catches single-pass 45%+ drops)
//   - Secondary: drop ratio < 0.65 AND absolute delta > 40K AND cache invalidated
//     (catches 35–65% drops with large absolute delta, confirmed by cache reset)
//
// After initial detection, consecutive events within CompactionCascadeWindow messages
// are merged into single cascade events (e.g. 168K → 105K → 68K becomes one 59.5% event).
//
// The inputRate parameter is the cost per input token (e.g. $15/M = 0.000015) for
// estimating the rebuild cost. Pass 0 to skip cost estimation.
func DetectCompactions(messages []Message, inputRate float64) CompactionSummary {
	var summary CompactionSummary
	var prevInput int
	var prevMsgIdx int
	var prevCacheRead int
	var lastCompactionIdx int
	var messagesWithTokenData int
	var totalAssistantMessages int
	var userMessageCount int

	// Collect user message indices for last-quartile rate computation.
	var userMsgIndices []int

	// Phase 1: detect raw compaction events.
	var rawEvents []CompactionEvent

	for i, msg := range messages {
		if msg.Role == RoleUser {
			userMessageCount++
			userMsgIndices = append(userMsgIndices, i)
		}
		if msg.Role == RoleAssistant {
			totalAssistantMessages++
		}

		// Only consider messages with token data — typically assistant messages.
		if msg.InputTokens == 0 {
			continue
		}
		messagesWithTokenData++

		inputTokens := msg.InputTokens

		if prevInput > CompactionMinBaseline {
			dropRatio := float64(inputTokens) / float64(prevInput)
			absoluteDrop := prevInput - inputTokens

			// Check cache invalidation.
			cacheInvalidated := false
			if prevCacheRead > 1000 && msg.CacheReadTokens < prevCacheRead/5 {
				cacheInvalidated = true
			}

			triggered := false
			// Primary threshold: ratio-based.
			if dropRatio < CompactionThreshold {
				triggered = true
			}
			// Secondary threshold: large absolute drop with cache confirmation.
			if !triggered && dropRatio < CompactionSecondaryThreshold &&
				absoluteDrop > CompactionMinAbsoluteDrop && cacheInvalidated {
				triggered = true
			}

			if triggered {
				tokensLost := prevInput - inputTokens
				dropPct := (1.0 - dropRatio) * 100
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

				rawEvents = append(rawEvents, event)
				lastCompactionIdx = i
			}
		}

		prevInput = inputTokens
		prevMsgIdx = i
		prevCacheRead = msg.CacheReadTokens
	}

	// Phase 2: merge 2-pass cascades.
	summary.Events = mergeCascades(rawEvents)

	// Count cascades.
	for _, e := range summary.Events {
		if e.IsCascade {
			summary.CascadeCount++
		}
	}

	// Phase 3: compute summary stats.
	summary.TotalCompactions = len(summary.Events)
	summary.MessagesWithTokenData = messagesWithTokenData

	// Detection coverage.
	if messagesWithTokenData == 0 {
		summary.DetectionCoverage = "none"
	} else if totalAssistantMessages > 0 && float64(messagesWithTokenData)/float64(totalAssistantMessages) < 0.5 {
		summary.DetectionCoverage = "partial"
	} else {
		summary.DetectionCoverage = "full"
	}

	if summary.TotalCompactions == 0 {
		return summary
	}

	// Sum tokens lost and rebuild cost from merged events.
	summary.TotalTokensLost = 0
	summary.TotalRebuildCost = 0
	for _, e := range summary.Events {
		summary.TotalTokensLost += e.TokensLost
		summary.TotalRebuildCost += e.RebuildCost
	}

	// Compute average drop percent.
	var totalDrop float64
	for _, e := range summary.Events {
		totalDrop += e.DropPercent
	}
	summary.AvgDropPercent = totalDrop / float64(summary.TotalCompactions)

	// Compute median drop percent.
	summary.MedianDropPercent = medianFloat64(summary.Events)

	// Sawtooth cycle detection.
	summary.SawtoothCycles = summary.TotalCompactions

	// Compaction rate per user message.
	if userMessageCount > 0 {
		summary.CompactionsPerUserMessage = float64(summary.TotalCompactions) / float64(userMessageCount)
	}

	// Last-quartile compaction rate.
	summary.LastQuartileCompactionRate = computeLastQuartileRate(summary.Events, userMsgIndices, messages)

	// Compute average messages-to-fill and recovery stats.
	computeFillAndRecoveryStats(messages, &summary, lastCompactionIdx)

	return summary
}

// mergeCascades merges consecutive compaction events that are within
// CompactionCascadeWindow messages of each other into single cascade events.
// E.g. 168K → 105K (event 1) and 105K → 68K (event 2, ≤3 msgs later)
// become a single event: 168K → 68K with IsCascade=true, MergedLegs=2.
func mergeCascades(events []CompactionEvent) []CompactionEvent {
	if len(events) <= 1 {
		return events
	}

	var merged []CompactionEvent
	i := 0
	for i < len(events) {
		current := events[i]
		legs := 1

		// Merge consecutive events within the cascade window.
		// A cascade is incremental compaction: 168K → 105K → 68K, where the
		// context does NOT recover between passes. We verify this by checking
		// that the next event's "before" tokens haven't grown significantly
		// beyond the current event's "after" tokens. A sawtooth pattern (full
		// recovery between compactions) will have next.Before >> current.After.
		for i+1 < len(events) {
			next := events[i+1]
			gap := next.BeforeMessageIdx - current.AfterMessageIdx

			// Recovery check: if context grew more than 50% above the last
			// compaction's "after" level, this is a sawtooth recovery — not a
			// cascade continuation. current.AfterInputTokens is kept up-to-date
			// as legs are merged.
			recovered := float64(next.BeforeInputTokens) > float64(current.AfterInputTokens)*1.5

			if gap <= CompactionCascadeWindow && !recovered {
				// Merge: keep the first event's "before" and the last event's "after".
				current.AfterMessageIdx = next.AfterMessageIdx
				current.AfterInputTokens = next.AfterInputTokens
				current.TokensLost = current.BeforeInputTokens - next.AfterInputTokens
				current.DropPercent = (1.0 - float64(next.AfterInputTokens)/float64(current.BeforeInputTokens)) * 100
				current.RebuildCost = current.RebuildCost + next.RebuildCost
				// Cache invalidation: true if any leg confirmed it.
				if next.CacheInvalidated {
					current.CacheInvalidated = true
				}
				legs++
				i++
			} else {
				break
			}
		}

		if legs > 1 {
			current.IsCascade = true
			current.MergedLegs = legs
		}
		merged = append(merged, current)
		i++
	}
	return merged
}

// computeLastQuartileRate computes the compaction rate over only the last 25%
// of user messages. Detects end-of-session acceleration.
func computeLastQuartileRate(events []CompactionEvent, userMsgIndices []int, _ []Message) float64 {
	if len(userMsgIndices) < 4 || len(events) == 0 {
		return 0
	}

	// Find the message index that marks the start of the last 25% of user messages.
	q3Start := userMsgIndices[len(userMsgIndices)*3/4]

	// Count compactions that occurred after q3Start.
	compactionsInQ4 := 0
	for _, e := range events {
		if e.AfterMessageIdx >= q3Start {
			compactionsInQ4++
		}
	}

	userMsgsInQ4 := len(userMsgIndices) - len(userMsgIndices)*3/4
	if userMsgsInQ4 == 0 {
		return 0
	}

	return float64(compactionsInQ4) / float64(userMsgsInQ4)
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
