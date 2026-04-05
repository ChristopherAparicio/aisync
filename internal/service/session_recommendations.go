package service

import (
	"context"
	"fmt"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// GenerateRecommendations analyzes project data and produces actionable insights.
// This is fully deterministic (no LLM) — based on rules applied to existing metrics.
func (s *SessionService) GenerateRecommendations(ctx context.Context, projectPath string) ([]session.Recommendation, error) {
	since90d := time.Now().AddDate(0, 0, -90)
	var recs []session.Recommendation

	// ── Agent-based recommendations ──
	agentROI, err := s.AgentROIAnalysis(ctx, projectPath, since90d)
	if err == nil && agentROI != nil {
		for _, a := range agentROI.Agents {
			if a.SessionCount < 3 {
				continue // skip agents with too few sessions
			}

			// High error rate.
			if a.ErrorRate > 5.0 {
				recs = append(recs, session.Recommendation{
					Type:     "agent_error",
					Priority: "high",
					Icon:     "⚠️",
					Title:    fmt.Sprintf("Agent '%s' has high error rate", a.Agent),
					Message:  fmt.Sprintf("%.1f%% of tool calls fail (vs ~2%% average). Review its system prompt, instructions, or consider using a different agent for this task.", a.ErrorRate),
					Impact:   fmt.Sprintf("%d errors across %d sessions", a.ErrorCount, a.SessionCount),
					Agent:    a.Agent,
				})
			}

			// Extremely expensive sessions.
			if a.AvgCostPerSession > 100 {
				recs = append(recs, session.Recommendation{
					Type:     "agent_cost",
					Priority: "high",
					Icon:     "💰",
					Title:    fmt.Sprintf("Agent '%s' costs $%.0f/session average", a.Agent, a.AvgCostPerSession),
					Message:  fmt.Sprintf("With %.0f messages on average, sessions are very long. Consider splitting tasks into smaller sessions or using a cheaper model for simple subtasks.", a.AvgMessages),
					Impact:   fmt.Sprintf("~$%.0f total across %d sessions", a.EstimatedCost, a.SessionCount),
					Agent:    a.Agent,
				})
			}

			// Very long sessions (many messages).
			if a.AvgMessages > 100 && a.AvgCostPerSession <= 100 {
				recs = append(recs, session.Recommendation{
					Type:     "agent_long",
					Priority: "medium",
					Icon:     "📏",
					Title:    fmt.Sprintf("Agent '%s' sessions average %.0f messages", a.Agent, a.AvgMessages),
					Message:  "Long sessions degrade quality as context fills up. Consider breaking tasks into focused sub-sessions.",
					Agent:    a.Agent,
				})
			}

			// Low completion rate.
			if a.CompletionRate < 20 && a.SessionCount >= 10 {
				recs = append(recs, session.Recommendation{
					Type:     "agent_completion",
					Priority: "medium",
					Icon:     "🏁",
					Title:    fmt.Sprintf("Agent '%s' has %.0f%% completion rate", a.Agent, a.CompletionRate),
					Message:  fmt.Sprintf("Only %d of %d sessions reached [DONE] or [COMMIT]. Sessions may be abandoned or tasks too complex for one session.", a.CompletedCount, a.SessionCount),
					Agent:    a.Agent,
				})
			}

			// Great agent — recognition.
			if a.ROIScore >= 85 && a.SessionCount >= 10 {
				recs = append(recs, session.Recommendation{
					Type:     "agent_star",
					Priority: "low",
					Icon:     "⭐",
					Title:    fmt.Sprintf("Agent '%s' is highly efficient (ROI: %s)", a.Agent, a.ROIGrade),
					Message:  fmt.Sprintf("Low error rate (%.1f%%), good cost ($%.2f/session), strong completion. Consider using it as a template for other agents.", a.ErrorRate, a.AvgCostPerSession),
					Agent:    a.Agent,
				})
			}
		}
	}

	// ── Skill-based recommendations ──
	skillROI, err := s.SkillROIAnalysis(ctx, projectPath, since90d)
	if err == nil && skillROI != nil {
		for _, sk := range skillROI.Skills {
			// Ghost skill.
			if sk.IsGhost {
				recs = append(recs, session.Recommendation{
					Type:     "skill_ghost",
					Priority: "medium",
					Icon:     "👻",
					Title:    fmt.Sprintf("Skill '%s' appears unused", sk.Name),
					Message:  fmt.Sprintf("Loaded %d times but only in %.0f%% of sessions. If it's not needed, removing it saves ~%d tokens per session.", sk.LoadCount, sk.UsagePercent, sk.ContextTokens),
					Impact:   fmt.Sprintf("~%dK tokens/session savings", sk.ContextTokens/1000),
					Skill:    sk.Name,
				})
			}

			// Skill that increases errors.
			if sk.ErrorDelta > 3.0 && sk.LoadCount >= 5 {
				recs = append(recs, session.Recommendation{
					Type:     "skill_harmful",
					Priority: "high",
					Icon:     "🔴",
					Title:    fmt.Sprintf("Skill '%s' correlates with higher errors", sk.Name),
					Message:  fmt.Sprintf("Error rate is %.1f%% higher when this skill is loaded (%.1f%% vs %.1f%% without). The skill's instructions may be confusing the agent.", sk.ErrorDelta, sk.ErrorRateWith, sk.ErrorRateWithout),
					Skill:    sk.Name,
				})
			}
		}
	}

	// ── Cache efficiency recommendations ──
	since7d := time.Now().AddDate(0, 0, -7)
	cacheEff, err := s.CacheEfficiency(ctx, projectPath, since7d)
	if err == nil && cacheEff != nil {
		if cacheEff.SessionsWithMiss > 0 && cacheEff.EstimatedWaste > 5.0 {
			recs = append(recs, session.Recommendation{
				Type:     "cache_miss",
				Priority: "medium",
				Icon:     "⏱️",
				Title:    fmt.Sprintf("%d sessions had cache misses this week", cacheEff.SessionsWithMiss),
				Message:  fmt.Sprintf("%.0f cache misses caused $%.2f in wasted tokens. The prompt cache expires after ~5 min of inactivity. Respond within 5 min or start a fresh session instead of resuming.", float64(cacheEff.TotalCacheMisses), cacheEff.EstimatedWaste),
				Impact:   fmt.Sprintf("$%.2f/week waste", cacheEff.EstimatedWaste),
			})
		}
	}

	// ── Context saturation recommendations ──
	satResult, err := s.ContextSaturation(ctx, projectPath, since90d)
	if err == nil && satResult != nil {
		if satResult.SessionsAbove80 > 5 {
			pct := float64(satResult.SessionsAbove80) / float64(satResult.TotalSessions) * 100
			recs = append(recs, session.Recommendation{
				Type:     "context_saturation",
				Priority: "high",
				Icon:     "🔴",
				Title:    fmt.Sprintf("%.0f%% of sessions exceed 80%% context window", pct),
				Message:  fmt.Sprintf("%d sessions reached the degraded zone (>80%% of context). Quality drops significantly at high saturation. Split long tasks into focused sub-sessions.", satResult.SessionsAbove80),
				Impact:   fmt.Sprintf("%d sessions affected", satResult.SessionsAbove80),
			})
		}
		if satResult.SessionsCompacted > 0 {
			recs = append(recs, session.Recommendation{
				Type:     "compaction",
				Priority: "medium",
				Icon:     "📦",
				Title:    fmt.Sprintf("%d sessions triggered compaction", satResult.SessionsCompacted),
				Message:  "Compaction summarizes the conversation to free context, but loses detail. These sessions were too long for their model's context window.",
			})
		}

		// ── Token waste recommendations (from satResult.TokenWaste) ──
		if tw := satResult.TokenWaste; tw != nil && tw.TotalTokens > 0 {
			// High retry waste: >15% of tokens are retries.
			if tw.Retry.Percent > 15 {
				recs = append(recs, session.Recommendation{
					Type:     "token_waste_retry",
					Priority: "high",
					Icon:     "🔄",
					Title:    fmt.Sprintf("%.0f%% of tokens are retry waste", tw.Retry.Percent),
					Message:  fmt.Sprintf("%dK tokens spent retrying failed tool calls. This indicates agents are hitting errors and re-attempting the same operations. Review error patterns and fix underlying tool issues.", tw.Retry.Tokens/1000),
					Impact:   fmt.Sprintf("%dK tokens wasted on retries", tw.Retry.Tokens/1000),
				})
			}
			// High compaction waste: >20% of tokens lost to compaction.
			if tw.Compaction.Percent > 20 {
				recs = append(recs, session.Recommendation{
					Type:     "token_waste_compaction",
					Priority: "medium",
					Icon:     "⚡",
					Title:    fmt.Sprintf("%.0f%% of tokens lost to compaction", tw.Compaction.Percent),
					Message:  fmt.Sprintf("%dK tokens discarded during context compaction. Sessions are too long — split tasks to avoid hitting the context window limit.", tw.Compaction.Tokens/1000),
					Impact:   fmt.Sprintf("%dK tokens lost", tw.Compaction.Tokens/1000),
				})
			}
			// High cache miss waste: >10% of tokens are cache misses.
			if tw.CacheMiss.Percent > 10 {
				recs = append(recs, session.Recommendation{
					Type:     "token_waste_cache",
					Priority: "medium",
					Icon:     "💨",
					Title:    fmt.Sprintf("%.0f%% of tokens are cache miss overhead", tw.CacheMiss.Percent),
					Message:  "Long pauses between messages cause the prompt cache to expire (5 min TTL). Respond quickly or start a fresh session instead of resuming after a break.",
					Impact:   fmt.Sprintf("%dK tokens wasted", tw.CacheMiss.Tokens/1000),
				})
			}
			// Low overall productivity: <60% productive tokens.
			if tw.ProductivePct < 60 && tw.TotalTokens > 100000 {
				recs = append(recs, session.Recommendation{
					Type:     "token_waste_low_productivity",
					Priority: "high",
					Icon:     "📉",
					Title:    fmt.Sprintf("Only %.0f%% of tokens are productive", tw.ProductivePct),
					Message:  fmt.Sprintf("%.0f%% of token spend goes to waste (retries, compaction, cache misses, idle context). Review the waste breakdown to identify the largest contributor.", tw.WastePct),
					Impact:   fmt.Sprintf("%.0f%% waste = %dK tokens", tw.WastePct, (tw.TotalTokens-tw.Productive.Tokens)/1000),
				})
			}
		}

		// ── Model fitness recommendations (from satResult.Fitness) ──
		if fit := satResult.Fitness; fit != nil {
			for _, rec := range fit.Recommendations {
				recs = append(recs, session.Recommendation{
					Type:     "model_fitness",
					Priority: "medium",
					Icon:     "🔬",
					Title:    "Model fitness insight",
					Message:  rec,
				})
			}
		}

		// ── Freshness / diminishing returns (from satResult.Freshness) ──
		if fr := satResult.Freshness; fr != nil && fr.SessionsWithCompaction > 0 {
			// High error rate growth after compaction.
			if fr.AvgErrorRateGrowth > 50 {
				recs = append(recs, session.Recommendation{
					Type:     "freshness_error_growth",
					Priority: "high",
					Icon:     "📈",
					Title:    fmt.Sprintf("Error rate grows %.0f%% after compaction", fr.AvgErrorRateGrowth),
					Message:  "Sessions that reach compaction see a significant spike in errors afterward. The model loses important context during compaction. Keep sessions under the optimal length.",
					Impact:   fmt.Sprintf("Optimal session length: ~%d messages", fr.AvgOptimalMessageIdx),
				})
			}
			// Significant output quality decay.
			if fr.AvgOutputRatioDecay > 30 {
				recs = append(recs, session.Recommendation{
					Type:     "freshness_output_decay",
					Priority: "medium",
					Icon:     "📉",
					Title:    fmt.Sprintf("Output quality drops %.0f%% after compaction", fr.AvgOutputRatioDecay),
					Message:  "The output-to-input ratio declines significantly after compaction — the model produces less useful content per token spent. Start fresh sessions before quality degrades.",
				})
			}
			// Optimal session length recommendation.
			if fr.AvgOptimalMessageIdx > 0 && fr.AvgOptimalMessageIdx < 50 {
				recs = append(recs, session.Recommendation{
					Type:     "freshness_optimal_length",
					Priority: "low",
					Icon:     "📏",
					Title:    fmt.Sprintf("Optimal session length: ~%d messages", fr.AvgOptimalMessageIdx),
					Message:  fmt.Sprintf("Analysis shows session quality peaks around message %d before diminishing returns set in. Consider splitting longer tasks.", fr.AvgOptimalMessageIdx),
				})
			}
		}

		// ── System prompt impact (from satResult.PromptImpact) ──
		if pi := satResult.PromptImpact; pi != nil && pi.TotalSessions > 0 {
			// Very large system prompts.
			if pi.AvgEstimate > 8000 {
				recs = append(recs, session.Recommendation{
					Type:     "prompt_large",
					Priority: "medium",
					Icon:     "📜",
					Title:    fmt.Sprintf("System prompts average %dK tokens", pi.AvgEstimate/1000),
					Message:  fmt.Sprintf("Large system prompts (CLAUDE.md, MCP configs, skills) consume %.0f%% of input tokens before the first user message. Consider trimming unused instructions or splitting into conditional sections.", pi.AvgPromptCostPct),
					Impact:   fmt.Sprintf("%.0f%% of input cost is system prompt", pi.AvgPromptCostPct),
				})
			}
			// Growing prompt size trend.
			if pi.Trend == "growing" {
				recs = append(recs, session.Recommendation{
					Type:     "prompt_growing",
					Priority: "low",
					Icon:     "📈",
					Title:    "System prompt size is growing",
					Message:  "Your system prompts have been getting larger over time. Each addition (new skill, new instructions) adds cumulative context cost. Periodically audit and trim.",
				})
			}
		}

		// ── Model efficiency verdicts (from satResult.Models) ──
		for _, m := range satResult.Models {
			if m.SessionCount < 5 {
				continue
			}
			// Oversized model: using a large context model but barely filling it.
			if m.EfficiencyVerdict == "oversized" && m.AvgPeakPct < 20 {
				recs = append(recs, session.Recommendation{
					Type:     "model_oversized",
					Priority: "medium",
					Icon:     "🐘",
					Title:    fmt.Sprintf("Model '%s' is oversized for your usage", m.Model),
					Message:  fmt.Sprintf("Average peak saturation is only %.0f%% — you're paying for a large context window you don't use. Consider a smaller, cheaper model for this workload.", m.AvgPeakPct),
					Impact:   fmt.Sprintf("%d sessions using <20%% of context", m.SessionCount),
				})
			}
			// Saturated model: consistently hitting context limits.
			if m.EfficiencyVerdict == "saturated" {
				recs = append(recs, session.Recommendation{
					Type:     "model_saturated",
					Priority: "high",
					Icon:     "🔴",
					Title:    fmt.Sprintf("Model '%s' consistently saturates its context", m.Model),
					Message:  fmt.Sprintf("Average peak saturation is %.0f%%. Sessions are hitting the context limit, causing quality degradation and compaction. Split tasks or use a model with a larger context window.", m.AvgPeakPct),
				})
			}
		}
	}

	// ── Budget recommendations ──
	budgets, err := s.BudgetStatus(ctx)
	if err == nil {
		for _, bs := range budgets {
			if projectPath != "" && bs.ProjectPath != projectPath {
				continue
			}
			if bs.MonthlyAlert == "warning" {
				recs = append(recs, session.Recommendation{
					Type:     "budget_warning",
					Priority: "high",
					Icon:     "💸",
					Title:    fmt.Sprintf("%s at %.0f%% of monthly budget", bs.ProjectName, bs.MonthlyPercent),
					Message:  fmt.Sprintf("$%.0f spent of $%.0f limit with %d days remaining. Projected: $%.0f by end of month.", bs.MonthlySpent, bs.MonthlyLimit, bs.DaysRemaining, bs.ProjectedMonth),
					Impact:   fmt.Sprintf("$%.0f over budget risk", bs.ProjectedMonth-bs.MonthlyLimit),
					Project:  bs.ProjectName,
				})
			}
			if bs.MonthlyAlert == "exceeded" {
				recs = append(recs, session.Recommendation{
					Type:     "budget_exceeded",
					Priority: "high",
					Icon:     "🚨",
					Title:    fmt.Sprintf("%s exceeded monthly budget", bs.ProjectName),
					Message:  fmt.Sprintf("$%.0f spent of $%.0f limit (%.0f%%). Consider pausing non-essential sessions.", bs.MonthlySpent, bs.MonthlyLimit, bs.MonthlyPercent),
					Project:  bs.ProjectName,
				})
			}
		}
	}

	// Sort by priority: high > medium > low.
	priorityOrder := map[string]int{"high": 0, "medium": 1, "low": 2}
	for i := 0; i < len(recs); i++ {
		for j := i + 1; j < len(recs); j++ {
			if priorityOrder[recs[j].Priority] < priorityOrder[recs[i].Priority] {
				recs[i], recs[j] = recs[j], recs[i]
			}
		}
	}

	return recs, nil
}
