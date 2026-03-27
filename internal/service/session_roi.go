package service

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// AgentROIAnalysis computes per-agent ROI metrics for a project.
func (s *SessionService) AgentROIAnalysis(ctx context.Context, projectPath string, since time.Time) (*session.AgentROI, error) {
	listOpts := session.ListOptions{All: true}
	if !since.IsZero() {
		listOpts.Since = since
	}
	summaries, err := s.store.List(listOpts)
	if err != nil {
		return nil, err
	}

	type agentAcc struct {
		sessions   int
		messages   int
		tokens     int
		toolCalls  int
		errors     int
		completed  int
		satPeakSum float64
		satCount   int
	}
	byAgent := make(map[string]*agentAcc)

	for _, sm := range summaries {
		if projectPath != "" && sm.ProjectPath != projectPath {
			continue
		}

		agent := sm.Agent
		if agent == "" {
			agent = "unknown"
		}
		acc, ok := byAgent[agent]
		if !ok {
			acc = &agentAcc{}
			byAgent[agent] = acc
		}
		acc.sessions++
		acc.messages += sm.MessageCount
		acc.tokens += sm.TotalTokens
		acc.toolCalls += sm.ToolCallCount
		acc.errors += sm.ErrorCount

		// Completion detection from status or summary prefix.
		if sm.Status == "completed" || sm.Status == "review" {
			acc.completed++
		}
	}

	// Optional: compute per-agent saturation (lightweight — use session-level peak).
	// We reuse the same data from ContextSaturation to avoid double loading.
	satResult, satErr := s.ContextSaturation(ctx, projectPath, since)
	agentSaturation := make(map[string][]float64) // agent → list of peak %
	if satErr == nil && satResult != nil {
		for _, ws := range satResult.WorstSessions {
			// We don't have agent on SessionSaturation, so skip for now.
			_ = ws
		}
		// Use model average as fallback.
		for agent, acc := range byAgent {
			if acc.sessions > 0 {
				agentSaturation[agent] = append(agentSaturation[agent], satResult.AvgPeakSaturation)
			}
		}
	}

	result := &session.AgentROI{}

	for agent, acc := range byAgent {
		entry := session.AgentROIEntry{
			Agent:          agent,
			SessionCount:   acc.sessions,
			MessageCount:   acc.messages,
			TotalTokens:    acc.tokens,
			ToolCallCount:  acc.toolCalls,
			ErrorCount:     acc.errors,
			CompletedCount: acc.completed,
		}

		// Derived metrics.
		if acc.sessions > 0 {
			const blendedRate = 3.0 / 1_000_000
			entry.EstimatedCost = float64(acc.tokens) * blendedRate
			entry.AvgCostPerSession = entry.EstimatedCost / float64(acc.sessions)
			entry.AvgMessages = float64(acc.messages) / float64(acc.sessions)
			entry.CompletionRate = float64(acc.completed) / float64(acc.sessions) * 100
			if acc.messages > 0 {
				entry.TokensPerMessage = acc.tokens / acc.messages
			}
		}
		if acc.toolCalls > 0 {
			entry.ErrorRate = float64(acc.errors) / float64(acc.toolCalls) * 100
		}

		// Saturation average for this agent.
		if peaks, ok := agentSaturation[agent]; ok && len(peaks) > 0 {
			var sum float64
			for _, p := range peaks {
				sum += p
			}
			entry.AvgPeakSaturation = sum / float64(len(peaks))
		}

		// Composite ROI score (0-100).
		entry.ROIScore = computeROIScore(entry)
		entry.ROIGrade = roiGrade(entry.ROIScore)

		result.Agents = append(result.Agents, entry)
	}

	// Sort by ROI score descending.
	sort.Slice(result.Agents, func(i, j int) bool {
		return result.Agents[i].ROIScore > result.Agents[j].ROIScore
	})

	return result, nil
}

// computeROIScore calculates a composite 0-100 score.
// Higher is better: low cost + low errors + high completion + good context usage.
func computeROIScore(e session.AgentROIEntry) int {
	// Component scores (each 0-25 points, total 100).

	// 1. Error rate score (0-25): 0% errors = 25, 10%+ = 0
	errorScore := math.Max(0, 25-e.ErrorRate*2.5)

	// 2. Completion rate score (0-25): 100% completion = 25
	completionScore := e.CompletionRate / 100 * 25

	// 3. Cost efficiency score (0-25): lower avg cost = higher score.
	// $0 = 25, $50 = 12.5, $100+ = 0
	costScore := math.Max(0, 25-e.AvgCostPerSession*0.25)

	// 4. Context efficiency score (0-25): lower saturation = higher score.
	// 0% saturation = 25, 100% = 0
	contextScore := math.Max(0, 25-e.AvgPeakSaturation*0.25)

	total := int(errorScore + completionScore + costScore + contextScore)
	if total > 100 {
		total = 100
	}
	return total
}

func roiGrade(score int) string {
	switch {
	case score >= 85:
		return "A"
	case score >= 70:
		return "B"
	case score >= 55:
		return "C"
	case score >= 40:
		return "D"
	default:
		return "F"
	}
}

// SkillROIAnalysis computes per-skill ROI metrics for a project.
func (s *SessionService) SkillROIAnalysis(ctx context.Context, projectPath string, since time.Time) (*session.SkillROI, error) {
	listOpts := session.ListOptions{All: true}
	if !since.IsZero() {
		listOpts.Since = since
	}
	summaries, err := s.store.List(listOpts)
	if err != nil {
		return nil, err
	}

	// Filter to project.
	var projectSessions []session.Summary
	for _, sm := range summaries {
		if projectPath != "" && sm.ProjectPath != projectPath {
			continue
		}
		projectSessions = append(projectSessions, sm)
	}
	totalSessions := len(projectSessions)
	if totalSessions == 0 {
		return &session.SkillROI{}, nil
	}

	// Query skill events from session_events table.
	// We need: which skills were loaded in which sessions.
	if s.store == nil {
		return &session.SkillROI{}, nil
	}

	type skillAcc struct {
		loadCount   int
		sessions    map[string]bool // session IDs where loaded
		totalErrors int
		totalTools  int
	}
	bySkill := make(map[string]*skillAcc)

	// Walk sessions and check for skill events.
	var totalErrorsAll, totalToolsAll int
	for _, sm := range projectSessions {
		totalErrorsAll += sm.ErrorCount
		totalToolsAll += sm.ToolCallCount

		// Load events for this session to find skill loads.
		events, evtErr := s.store.GetSessionEvents(sm.ID)
		if evtErr != nil {
			continue
		}
		for _, evt := range events {
			if evt.Type != "skill_load" || evt.SkillLoad == nil {
				continue
			}
			name := evt.SkillLoad.SkillName
			if name == "" {
				continue
			}
			acc, ok := bySkill[name]
			if !ok {
				acc = &skillAcc{sessions: make(map[string]bool)}
				bySkill[name] = acc
			}
			acc.loadCount++
			acc.sessions[string(sm.ID)] = true
			acc.totalErrors += sm.ErrorCount
			acc.totalTools += sm.ToolCallCount
		}
	}

	// Build per-skill error rates.
	// Sessions with skill loaded vs without.
	result := &session.SkillROI{}
	for name, acc := range bySkill {
		entry := session.SkillROIEntry{
			Name:          name,
			LoadCount:     acc.loadCount,
			SessionCount:  len(acc.sessions),
			TotalSessions: totalSessions,
		}
		if totalSessions > 0 {
			entry.UsagePercent = float64(entry.SessionCount) / float64(totalSessions) * 100
		}

		// Estimate context tokens per skill load (~2000 tokens average for a skill prompt).
		entry.ContextTokens = 2000 // rough estimate
		entry.TotalTokenCost = entry.ContextTokens * entry.LoadCount

		// Error rate comparison: with vs without this skill.
		if acc.totalTools > 0 {
			entry.ErrorRateWith = float64(acc.totalErrors) / float64(acc.totalTools) * 100
		}
		otherErrors := totalErrorsAll - acc.totalErrors
		otherTools := totalToolsAll - acc.totalTools
		if otherTools > 0 {
			entry.ErrorRateWithout = float64(otherErrors) / float64(otherTools) * 100
		}
		entry.ErrorDelta = entry.ErrorRateWith - entry.ErrorRateWithout

		// Ghost detection: loaded but very low usage relative to sessions.
		if entry.LoadCount > 0 && entry.UsagePercent < 5 {
			entry.IsGhost = true
			entry.Verdict = "ghost"
		} else if entry.ErrorDelta > 5 {
			entry.Verdict = "harmful"
		} else if entry.UsagePercent >= 50 {
			entry.Verdict = "valuable"
		} else {
			entry.Verdict = "neutral"
		}

		result.Skills = append(result.Skills, entry)
	}

	sort.Slice(result.Skills, func(i, j int) bool {
		return result.Skills[i].LoadCount > result.Skills[j].LoadCount
	})

	return result, nil
}
