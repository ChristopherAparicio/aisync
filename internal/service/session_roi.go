package service

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
)

// AgentROIAnalysis computes per-agent ROI metrics for a project.
//
// Reads from the materialized session_analytics table (CQRS read model)
// instead of loading all sessions. The per-agent saturation is computed
// inline from PeakSaturationPct — no recursive ContextSaturation() call.
func (s *SessionService) AgentROIAnalysis(ctx context.Context, projectPath string, since time.Time) (*session.AgentROI, error) {
	rows, err := s.store.QueryAnalytics(session.AnalyticsFilter{
		ProjectPath:      projectPath,
		Since:            since,
		MinSchemaVersion: session.AnalyticsSchemaVersion,
	})
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

	for _, ar := range rows {
		agent := ar.Agent
		if agent == "" {
			agent = "unknown"
		}
		acc, ok := byAgent[agent]
		if !ok {
			acc = &agentAcc{}
			byAgent[agent] = acc
		}
		acc.sessions++
		acc.messages += ar.MessageCount
		acc.tokens += ar.TotalTokens
		acc.toolCalls += ar.ToolCallCount
		acc.errors += ar.ErrorCount

		// Completion detection from status.
		if ar.Status == "completed" || ar.Status == "review" {
			acc.completed++
		}

		// Per-agent saturation from pre-computed PeakSaturationPct.
		if ar.PeakSaturationPct > 0 {
			acc.satPeakSum += ar.PeakSaturationPct
			acc.satCount++
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

		// Saturation average for this agent (computed inline, no recursive call).
		if acc.satCount > 0 {
			entry.AvgPeakSaturation = acc.satPeakSum / float64(acc.satCount)
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
		loadCount       int
		sessions        map[string]bool // session IDs where loaded
		totalErrors     int
		totalTools      int
		totalTokens     int  // sum of EstimatedTokens from all loads
		tokensPopulated bool // true if at least one load had EstimatedTokens > 0
	}
	bySkill := make(map[string]*skillAcc)

	// Tally session-wide totals and collect IDs for the batch event fetch.
	// Fix #8: previously issued one GetSessionEvents() call per session (N+1). Now
	// we fetch all skill_load events for the whole project in a single SQL query,
	// filtered at the DB level so we don't transfer unrelated event payloads.
	var totalErrorsAll, totalToolsAll int
	ids := make([]session.ID, 0, len(projectSessions))
	summaryByID := make(map[session.ID]session.Summary, len(projectSessions))
	for _, sm := range projectSessions {
		totalErrorsAll += sm.ErrorCount
		totalToolsAll += sm.ToolCallCount
		ids = append(ids, sm.ID)
		summaryByID[sm.ID] = sm
	}

	// Single batched query filtered to skill_load events only.
	eventsBySession, evtErr := s.store.GetSessionEventsBatch(ids, sessionevent.EventSkillLoad)
	if evtErr != nil {
		// Best-effort: an event-store error shouldn't break ROI analysis — fall back
		// to an empty map (caller will see zero skills, same as a project with no loads).
		eventsBySession = map[session.ID][]sessionevent.Event{}
	}

	for sid, events := range eventsBySession {
		sm, ok := summaryByID[sid]
		if !ok {
			continue
		}
		for _, evt := range events {
			// SQL filter already narrows to skill_load, but defensively guard the payload.
			if evt.Type != sessionevent.EventSkillLoad || evt.SkillLoad == nil {
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
			if evt.SkillLoad.EstimatedTokens > 0 {
				acc.totalTokens += evt.SkillLoad.EstimatedTokens
				acc.tokensPopulated = true
			}
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

		// Context tokens per skill load: use measured data if available, else fallback to ~2000.
		if acc.tokensPopulated && acc.loadCount > 0 {
			entry.ContextTokens = acc.totalTokens / acc.loadCount // average tokens per load
			entry.TotalTokenCost = acc.totalTokens
		} else {
			entry.ContextTokens = 2000 // rough estimate
			entry.TotalTokenCost = entry.ContextTokens * entry.LoadCount
		}

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
