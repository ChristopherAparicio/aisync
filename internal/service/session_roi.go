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
//
// Reads session-level error/tool counts from the session_analytics CQRS
// read model (flat columns only); falls back to List() when the read
// model is empty. Per-skill event data still comes from session_events.
func (s *SessionService) SkillROIAnalysis(ctx context.Context, projectPath string, since time.Time) (*session.SkillROI, error) {
	if s.store == nil {
		return &session.SkillROI{}, nil
	}

	type sessionFacts struct {
		ID            session.ID
		ProjectPath   string
		ErrorCount    int
		ToolCallCount int
	}

	var projectSessions []sessionFacts
	rows, err := s.store.QueryAnalytics(session.AnalyticsFilter{
		ProjectPath: projectPath,
		Since:       since,
		SkipBlobs:   true,
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		listOpts := session.ListOptions{All: true}
		if !since.IsZero() {
			listOpts.Since = since
		}
		summaries, err := s.store.List(listOpts)
		if err != nil {
			return nil, err
		}
		for _, sm := range summaries {
			if projectPath != "" && sm.ProjectPath != projectPath {
				continue
			}
			projectSessions = append(projectSessions, sessionFacts{
				ID:            sm.ID,
				ProjectPath:   sm.ProjectPath,
				ErrorCount:    sm.ErrorCount,
				ToolCallCount: sm.ToolCallCount,
			})
		}
	} else {
		for _, row := range rows {
			projectSessions = append(projectSessions, sessionFacts{
				ID:            row.SessionID,
				ProjectPath:   row.ProjectPath,
				ErrorCount:    row.ErrorCount,
				ToolCallCount: row.ToolCallCount,
			})
		}
	}

	totalSessions := len(projectSessions)
	if totalSessions == 0 {
		return &session.SkillROI{}, nil
	}

	type skillAcc struct {
		loadCount       int
		sessions        map[string]bool
		totalErrors     int
		totalTools      int
		totalTokens     int
		tokensPopulated bool
	}
	bySkill := make(map[string]*skillAcc)

	// Fix #8: batched event fetch — one SQL query for all skill_load events
	// across the whole project, instead of N GetSessionEvents() calls.
	var totalErrorsAll, totalToolsAll int
	ids := make([]session.ID, 0, len(projectSessions))
	factsByID := make(map[session.ID]sessionFacts, len(projectSessions))
	for _, sf := range projectSessions {
		totalErrorsAll += sf.ErrorCount
		totalToolsAll += sf.ToolCallCount
		ids = append(ids, sf.ID)
		factsByID[sf.ID] = sf
	}

	// Single batched query filtered to skill_load events only.
	eventsBySession, evtErr := s.store.GetSessionEventsBatch(ids, sessionevent.EventSkillLoad)
	if evtErr != nil {
		// Best-effort: an event-store error shouldn't break ROI analysis — fall back
		// to an empty map (caller will see zero skills, same as a project with no loads).
		eventsBySession = map[session.ID][]sessionevent.Event{}
	}

	for sid, events := range eventsBySession {
		sf, ok := factsByID[sid]
		if !ok {
			continue
		}
		for _, evt := range events {
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
			acc.sessions[string(sf.ID)] = true
			acc.totalErrors += sf.ErrorCount
			acc.totalTools += sf.ToolCallCount
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
