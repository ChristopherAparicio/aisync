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
