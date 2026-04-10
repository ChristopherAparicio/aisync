package session

import (
	"time"
)

// AnalyticsPricingLookup is the narrow pricing interface ComputeAnalytics
// depends on. It is a subset of *pricing.Calculator, declared here so the
// session package does not import pricing (which would create a cycle:
// pricing already imports session).
//
// Callers pass a real *pricing.Calculator, which satisfies this interface via
// its Lookup and SessionCost methods. Tests can pass a tiny fake.
type AnalyticsPricingLookup interface {
	// Lookup returns the ModelPrice row for a model identifier. The returned
	// value is opaque to this package — only MaxInputTokens and InputPerMToken
	// (via AnalyticsModelPrice) are read.
	LookupPrice(model string) (AnalyticsModelPrice, bool)

	// TotalCost returns the API-equivalent total cost for a session, mirroring
	// pricing.Calculator.SessionCost(sess).TotalCost.TotalCost.
	TotalCost(sess *Session) float64

	// ActualCost returns the provider-reported cost for a session, mirroring
	// pricing.Calculator.SessionCost(sess).Breakdown.ActualCost.TotalCost.
	ActualCost(sess *Session) float64
}

// AnalyticsModelPrice is the subset of pricing.ModelPrice that ComputeAnalytics
// actually reads. Kept tiny on purpose — we do not want the session package to
// know about tiered pricing, cache pricing differentials, etc.
type AnalyticsModelPrice struct {
	MaxInputTokens int
	InputPerMToken float64
}

// ComputeAnalytics runs the pure domain computation that produces a
// session-scoped materialized read-model row.
//
// This function is the **single source of truth** for every per-session
// derived metric used by the four catastrophic hot paths (Forecast,
// CacheEfficiency, ContextSaturation, AgentROIAnalysis). It is deterministic,
// has no I/O, does not mutate sess, and is safe to call from any goroutine.
//
// The caller (typically the service-layer stampAnalytics hook) is responsible
// for persisting the returned Analytics via storage.Store.UpsertSessionAnalytics.
//
// Behaviour-preservation contract
// ───────────────────────────────
// The logic below is a direct line-for-line extraction of the per-session
// work that ContextSaturation, CacheEfficiency, and Forecast currently do
// inline on every dashboard hit. The extraction must not change any output
// numbers — downstream tests over the aggregate functions (AggregateWaste,
// AggregateFreshness, AnalyzeSystemPromptImpact, AnalyzeModelFitness,
// ForecastSaturation) would regress if this function diverged. When in
// doubt, preserve the existing behaviour even if it looks sub-optimal — the
// place to fix it is in a follow-up PR with its own test coverage.
//
// Parameters
// ──────────
//   - sess: the full session (with Messages loaded). Must not be nil.
//   - pricing: pricing lookup facade. Pass nil to skip cost-dependent fields.
//   - forkOffset: message index at which this session diverged from its
//     parent (0 if not a fork). Caller obtains this from a single
//     ListAllForkRelations() call cached across the batch.
func ComputeAnalytics(sess *Session, pricing AnalyticsPricingLookup, forkOffset int) Analytics {
	a := Analytics{
		SessionID:     sess.ID,
		SchemaVersion: AnalyticsSchemaVersion,
		ComputedAt:    time.Now().UTC(),
		ForkOffset:    forkOffset,
		EstimatedCost: sess.EstimatedCost,
		ActualCost:    sess.ActualCost,
	}

	// Flat cache-efficiency aggregates from the denormalized TokenUsage.
	// These are cheap because they live on the session header itself.
	a.CacheReadTokens = sess.TokenUsage.CacheRead
	a.CacheWriteTokens = sess.TokenUsage.CacheWrite
	a.InputTokens = sess.TokenUsage.InputTokens

	if len(sess.Messages) == 0 {
		return a
	}

	// ── Peak input + dominant model ────────────────────────────────
	// Mirrors ContextSaturation main loop (session_usage.go lines 685-707).
	// Note: ContextSaturation starts at index 1 here (skips message 0). We
	// replicate that exactly to stay behaviour-preserving.
	var peakInput int
	modelCounts := make(map[string]int, 4)
	for i := 1; i < len(sess.Messages); i++ {
		msg := &sess.Messages[i]
		if msg.InputTokens > peakInput {
			peakInput = msg.InputTokens
		}
		if msg.Model != "" && msg.Role == RoleAssistant {
			modelCounts[msg.Model]++
		}
	}
	a.PeakInputTokens = peakInput

	var dominantModel string
	var maxCount int
	for model, count := range modelCounts {
		if count > maxCount {
			dominantModel = model
			maxCount = count
		}
	}
	a.DominantModel = dominantModel

	// ── Context window + input rate from pricing catalog ───────────
	var maxInputTokens int
	var inputRate float64
	if pricing != nil && dominantModel != "" {
		if mp, ok := pricing.LookupPrice(dominantModel); ok {
			maxInputTokens = mp.MaxInputTokens
			inputRate = mp.InputPerMToken / 1_000_000
		}
	}
	a.MaxContextWindow = maxInputTokens

	// ── Compaction detection (reuse domain helper) ─────────────────
	compactions := DetectCompactions(sess.Messages, inputRate)
	a.HasCompaction = compactions.TotalCompactions > 0
	a.CompactionCount = compactions.TotalCompactions
	a.CompactionWastedTokens = compactions.TotalTokensLost
	if compactions.TotalCompactions > 0 {
		a.CompactionDropPct = compactions.AvgDropPercent
	}

	// ── Peak saturation % (only computable if we know the context window) ──
	if maxInputTokens > 0 && peakInput > 0 {
		sat := float64(peakInput) / float64(maxInputTokens) * 100
		if sat > 100 {
			sat = 100 // cap — tokens can briefly exceed the window before compaction
		}
		a.PeakSaturationPct = sat
	}

	// ── Backend detection ──────────────────────────────────────────
	// For Forecast's per-backend aggregation. We pick the first non-empty
	// ProviderID we encounter (sessions are single-backend in practice; if
	// they ever aren't, Forecast builds the treemap from per-message data
	// anyway, so this column is just a hint).
	for i := range sess.Messages {
		if pid := sess.Messages[i].ProviderID; pid != "" {
			a.Backend = pid
			break
		}
	}

	// ── Cache-miss gap analysis ────────────────────────────────────
	// Mirrors CacheEfficiency per-session loop (session_usage.go 1114-1141).
	const cacheTTLMinutes = 5
	var longestGap int
	var sessionGapSum float64
	var sessionGapCount int
	var prevTime time.Time
	for _, msg := range sess.Messages {
		if msg.Timestamp.IsZero() {
			continue
		}
		if !prevTime.IsZero() {
			gapMins := msg.Timestamp.Sub(prevTime).Minutes()
			sessionGapSum += gapMins
			sessionGapCount++

			if msg.Role == RoleAssistant && int(gapMins) > cacheTTLMinutes {
				a.CacheMissCount++
				a.CacheWastedTokens += msg.InputTokens
				if int(gapMins) > longestGap {
					longestGap = int(gapMins)
				}
			}
		}
		prevTime = msg.Timestamp
	}
	a.LongestGapMins = longestGap
	if sessionGapCount > 0 {
		a.SessionAvgGapMins = sessionGapSum / float64(sessionGapCount)
	}

	// ── Fork deduplication ─────────────────────────────────────────
	// For sessions that are forks, subtract the shared prefix from the
	// headline EstimatedCost so downstream aggregates don't double-count.
	// Mirrors Forecast's adjustedCost calculation (session_forecast.go 128-153).
	if forkOffset > 0 && sess.TokenUsage.TotalTokens > 0 {
		var sharedTokens int
		for k := 0; k < forkOffset && k < len(sess.Messages); k++ {
			m := &sess.Messages[k]
			sharedTokens += m.InputTokens + m.OutputTokens
		}
		if sharedTokens > 0 {
			// Proportional scaling: (sharedTokens / total) * estimatedCost.
			sharedCostFrac := float64(sharedTokens) / float64(sess.TokenUsage.TotalTokens)
			dedup := a.EstimatedCost - (a.EstimatedCost * sharedCostFrac)
			if dedup < 0 {
				dedup = 0
			}
			a.DeduplicatedCost = dedup
		} else {
			a.DeduplicatedCost = a.EstimatedCost
		}
	} else {
		a.DeduplicatedCost = a.EstimatedCost
	}

	// ── Rich per-session analyses (JSON blobs) ─────────────────────
	// These structs each walk the message array once. We store them on the
	// Analytics value so the read path can deserialize them and feed the
	// existing Aggregate* helpers verbatim.

	// (1) Token waste classification.
	waste := ClassifyTokenWaste(sess.Messages, compactions)
	a.WasteBreakdown = &waste
	a.TotalWastedTokens = waste.Retry.Tokens + waste.Compaction.Tokens + waste.CacheMiss.Tokens + waste.IdleContext.Tokens

	// (2) Session freshness analysis.
	fresh := AnalyzeFreshness(sess.Messages, compactions)
	a.Freshness = &fresh

	// (3) Overload detection.
	over := DetectOverload(sess.Messages)
	a.Overload = &over

	// (4) System prompt impact input. This is an *input* struct consumed by
	// AnalyzeSystemPromptImpact at read time. We populate the four fields
	// that AnalyzeSystemPromptImpact reads, using the same computation
	// ContextSaturation does inline (session_usage.go 838-880).
	promptEst := SystemPromptEstimate(sess.Messages)
	if promptEst > 0 {
		var totalInput, toolCalls, toolErrors, retryMsgs int
		for mi := range sess.Messages {
			totalInput += sess.Messages[mi].InputTokens
			for tj := range sess.Messages[mi].ToolCalls {
				toolCalls++
				if sess.Messages[mi].ToolCalls[tj].State == ToolStateError {
					toolErrors++
				}
			}
			// Retry counting: assistant messages whose previous assistant
			// had a tool error. Mirrors session_usage.go 853-864 exactly.
			if mi > 1 && sess.Messages[mi].Role == RoleAssistant {
				for prev := mi - 1; prev >= 0; prev-- {
					if sess.Messages[prev].Role == RoleAssistant {
						for _, tc := range sess.Messages[prev].ToolCalls {
							if tc.State == ToolStateError {
								retryMsgs++
							}
						}
						break
					}
				}
			}
		}
		var errRate, retryRate float64
		if toolCalls > 0 {
			errRate = float64(toolErrors) / float64(toolCalls) * 100
		}
		if len(sess.Messages) > 0 {
			retryRate = float64(retryMsgs) / float64(len(sess.Messages)) * 100
		}
		a.PromptData = &SessionPromptData{
			PromptTokens: promptEst,
			TotalInput:   totalInput,
			ErrorRate:    errRate,
			RetryRate:    retryRate,
			CreatedAt:    sess.CreatedAt.Unix(),
		}
	}

	// (5) Model fitness data (input to AnalyzeModelFitness).
	// Only populate when we have a dominant model AND a usable session type.
	// Mirrors session_usage.go 882-923.
	if dominantModel != "" && sess.SessionType != "" {
		var fitToolCalls, fitToolErrors, fitOutputTokens int
		var fitHasRetries bool
		for mi := range sess.Messages {
			fitOutputTokens += sess.Messages[mi].OutputTokens
			for tj := range sess.Messages[mi].ToolCalls {
				fitToolCalls++
				if sess.Messages[mi].ToolCalls[tj].State == ToolStateError {
					fitToolErrors++
				}
			}
			if mi > 0 && sess.Messages[mi].Role == RoleAssistant && sess.Messages[mi-1].Role == RoleAssistant {
				for _, tc := range sess.Messages[mi-1].ToolCalls {
					if tc.State == ToolStateError {
						fitHasRetries = true
						break
					}
				}
			}
		}
		var fitCost float64
		if pricing != nil {
			fitCost = pricing.TotalCost(sess)
		}
		a.FitnessData = &SessionFitnessData{
			Model:         dominantModel,
			SessionType:   sess.SessionType,
			TotalTokens:   peakInput, // use peak as proxy for total context used
			OutputTokens:  fitOutputTokens,
			MessageCount:  len(sess.Messages),
			ToolCalls:     fitToolCalls,
			ToolErrors:    fitToolErrors,
			EstimatedCost: fitCost,
			HasRetries:    fitHasRetries,
		}
	}

	// (6) Forecast input (input to ForecastSaturation).
	// Mirrors session_usage.go 796-830.
	var tokenGrowthPerMsg int
	if len(sess.Messages) > 2 {
		var prevTokens int
		var totalDelta int64
		var deltaCount int
		for _, m := range sess.Messages {
			if m.InputTokens == 0 {
				continue
			}
			if prevTokens > 0 && m.InputTokens > prevTokens {
				totalDelta += int64(m.InputTokens - prevTokens)
				deltaCount++
			}
			prevTokens = m.InputTokens
		}
		if deltaCount > 0 {
			tokenGrowthPerMsg = int(totalDelta / int64(deltaCount))
		}
	}
	var msgAtFirstCompaction int
	if compactions.TotalCompactions > 0 && len(compactions.Events) > 0 {
		msgAtFirstCompaction = compactions.Events[0].BeforeMessageIdx
	}
	a.ForecastInput = &SessionForecastInput{
		Model:                dominantModel,
		MaxInputTokens:       maxInputTokens,
		MessageCount:         len(sess.Messages),
		PeakInputTokens:      peakInput,
		MsgAtFirstCompaction: msgAtFirstCompaction,
		TokenGrowthPerMsg:    tokenGrowthPerMsg,
	}

	// ── Per-session agent rollup ───────────────────────────────────
	// The current session model carries a single Agent string per session
	// (the CLI/framework that created it, e.g. "coder"). We materialize one
	// AgentUsage row per session keyed on that name. If sub-agent dispatch
	// tracking lands later, this is the place to expand the per-agent
	// attribution loop.
	agentName := sess.Agent
	if agentName == "" {
		agentName = "unknown"
	}
	// Tool-call error count for this session — used as the agent's error tally.
	var agentErrors int
	for mi := range sess.Messages {
		for tj := range sess.Messages[mi].ToolCalls {
			if sess.Messages[mi].ToolCalls[tj].State == ToolStateError {
				agentErrors++
			}
		}
	}
	a.TotalAgentInvocations = len(sess.Messages)
	a.UniqueAgentsUsed = 1
	a.AgentTokens = sess.TokenUsage.TotalTokens
	a.AgentCost = a.EstimatedCost
	a.AgentUsage = []AgentUsage{
		{
			AgentName:   agentName,
			Invocations: len(sess.Messages),
			Tokens:      sess.TokenUsage.TotalTokens,
			Cost:        a.EstimatedCost,
			Errors:      agentErrors,
		},
	}

	return a
}
