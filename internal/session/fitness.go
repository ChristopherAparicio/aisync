package session

import (
	"fmt"
	"sort"
)

// ModelFitnessProfile describes how well a model performs for a specific task type.
type ModelFitnessProfile struct {
	Model        string `json:"model"`
	TaskType     string `json:"task_type"`     // session type: "feature", "bug", "refactor", etc.
	SessionCount int    `json:"session_count"` // sessions matching this (model, task_type) pair

	// Efficiency metrics.
	AvgCost         float64 `json:"avg_cost"`          // average session cost ($)
	AvgMessages     float64 `json:"avg_messages"`      // average message count per session
	AvgTokens       int     `json:"avg_tokens"`        // average total tokens per session
	AvgOutputTokens int     `json:"avg_output_tokens"` // average output tokens per session
	ErrorRate       float64 `json:"error_rate"`        // tool errors / tool calls * 100 (0-100)
	RetryRate       float64 `json:"retry_rate"`        // sessions with retries / total * 100

	// Composite fitness score (0-100, higher = more fit for this task type).
	FitnessScore int    `json:"fitness_score"`
	FitnessGrade string `json:"fitness_grade"` // "A", "B", "C", "D", "F"
}

// TaskTypeProfile aggregates all models used for a given task type,
// ranked by fitness score.
type TaskTypeProfile struct {
	TaskType     string                `json:"task_type"`
	SessionCount int                   `json:"session_count"` // total sessions of this type
	Models       []ModelFitnessProfile `json:"models"`        // ranked by fitness score descending
	BestModel    string                `json:"best_model"`    // top-ranked model for this task type
	BestScore    int                   `json:"best_score"`    // score of the best model
}

// FitnessAnalysis is the aggregate result of model fitness profiling.
type FitnessAnalysis struct {
	TotalSessions   int               `json:"total_sessions"`  // sessions with valid (model, type) pairs
	TaskTypes       []TaskTypeProfile `json:"task_types"`      // per-task-type model rankings
	Recommendations []string          `json:"recommendations"` // actionable recommendations
}

// SessionFitnessData captures per-session data needed for fitness analysis.
type SessionFitnessData struct {
	Model         string
	SessionType   string
	TotalTokens   int
	OutputTokens  int
	MessageCount  int
	ToolCalls     int
	ToolErrors    int
	EstimatedCost float64
	HasRetries    bool // true if session has retry patterns
}

// AnalyzeModelFitness computes per-task-type model rankings from session data.
// This is a pure domain function with no side effects.
func AnalyzeModelFitness(data []SessionFitnessData) FitnessAnalysis {
	result := FitnessAnalysis{}

	if len(data) == 0 {
		return result
	}

	// Filter out sessions without valid model + type.
	var valid []SessionFitnessData
	for _, d := range data {
		if d.Model != "" && d.SessionType != "" && d.SessionType != "other" {
			valid = append(valid, d)
		}
	}

	if len(valid) == 0 {
		return result
	}

	result.TotalSessions = len(valid)

	// Group by (task_type, model).
	type groupKey struct {
		taskType string
		model    string
	}
	type groupAgg struct {
		sessions    int
		totalCost   float64
		totalMsgs   int
		totalTokens int64
		totalOutput int64
		toolCalls   int
		toolErrors  int
		retryCount  int
	}
	groups := make(map[groupKey]*groupAgg)
	taskSessions := make(map[string]int) // task_type → total sessions

	for _, d := range valid {
		key := groupKey{taskType: d.SessionType, model: d.Model}
		g, ok := groups[key]
		if !ok {
			g = &groupAgg{}
			groups[key] = g
		}
		g.sessions++
		g.totalCost += d.EstimatedCost
		g.totalMsgs += d.MessageCount
		g.totalTokens += int64(d.TotalTokens)
		g.totalOutput += int64(d.OutputTokens)
		g.toolCalls += d.ToolCalls
		g.toolErrors += d.ToolErrors
		if d.HasRetries {
			g.retryCount++
		}
		taskSessions[d.SessionType]++
	}

	// Build profiles and compute fitness scores.
	taskModels := make(map[string][]ModelFitnessProfile)

	for key, g := range groups {
		var errorRate float64
		if g.toolCalls > 0 {
			errorRate = float64(g.toolErrors) / float64(g.toolCalls) * 100
		}
		retryRate := float64(g.retryCount) / float64(g.sessions) * 100

		profile := ModelFitnessProfile{
			Model:           key.model,
			TaskType:        key.taskType,
			SessionCount:    g.sessions,
			AvgCost:         g.totalCost / float64(g.sessions),
			AvgMessages:     float64(g.totalMsgs) / float64(g.sessions),
			AvgTokens:       int(g.totalTokens / int64(g.sessions)),
			AvgOutputTokens: int(g.totalOutput / int64(g.sessions)),
			ErrorRate:       errorRate,
			RetryRate:       retryRate,
		}

		profile.FitnessScore = computeFitnessScore(profile)
		profile.FitnessGrade = scoreToGrade(profile.FitnessScore)

		taskModels[key.taskType] = append(taskModels[key.taskType], profile)
	}

	// Build TaskTypeProfiles sorted by task type.
	taskTypes := make([]string, 0, len(taskModels))
	for tt := range taskModels {
		taskTypes = append(taskTypes, tt)
	}
	sort.Strings(taskTypes)

	for _, tt := range taskTypes {
		models := taskModels[tt]
		// Sort by fitness score descending.
		sort.Slice(models, func(i, j int) bool {
			return models[i].FitnessScore > models[j].FitnessScore
		})

		ttp := TaskTypeProfile{
			TaskType:     tt,
			SessionCount: taskSessions[tt],
			Models:       models,
		}
		if len(models) > 0 {
			ttp.BestModel = models[0].Model
			ttp.BestScore = models[0].FitnessScore
		}
		result.TaskTypes = append(result.TaskTypes, ttp)
	}

	// Sort TaskTypes by session count descending (most common first).
	sort.Slice(result.TaskTypes, func(i, j int) bool {
		return result.TaskTypes[i].SessionCount > result.TaskTypes[j].SessionCount
	})

	// Generate recommendations.
	result.Recommendations = buildFitnessRecommendations(result.TaskTypes)

	return result
}

// computeFitnessScore computes a 0-100 composite score for a model-task pair.
//
// Components (equal weight, 25 pts each):
//  1. Error discipline: low error rate → high score
//  2. Retry discipline: low retry rate → high score
//  3. Cost efficiency: lower cost per output token → higher score (log-scaled)
//  4. Productivity: higher output/total token ratio → higher score
func computeFitnessScore(p ModelFitnessProfile) int {
	// 1. Error discipline (25 pts): 0% errors → 25, >20% → 0
	errScore := 25.0
	if p.ErrorRate > 0 {
		errScore = 25.0 * (1.0 - p.ErrorRate/20.0)
		if errScore < 0 {
			errScore = 0
		}
	}

	// 2. Retry discipline (25 pts): 0% retries → 25, 100% → 0
	retryScore := 25.0 * (1.0 - p.RetryRate/100.0)
	if retryScore < 0 {
		retryScore = 0
	}

	// 3. Cost efficiency (25 pts): based on cost per 1K output tokens.
	// Lower is better. Use logarithmic scale.
	costScore := 15.0 // default mid-range if no data
	if p.AvgOutputTokens > 0 && p.AvgCost > 0 {
		costPer1K := p.AvgCost / float64(p.AvgOutputTokens) * 1000
		// $0.001/1K output → 25 pts; $1.00/1K output → ~5 pts
		// Score = 25 - 3.3*log10(costPer1K*1000)
		if costPer1K > 0 {
			// Scale: $0.001 → 25, $0.01 → 20, $0.1 → 15, $1.0 → 10, $10 → 5
			costScore = 25.0 - 5.0*log10Approx(costPer1K*1000)
			if costScore < 0 {
				costScore = 0
			}
			if costScore > 25 {
				costScore = 25
			}
		}
	}

	// 4. Productivity (25 pts): output/total ratio. Higher = more productive.
	prodScore := 15.0 // default mid-range
	if p.AvgTokens > 0 {
		ratio := float64(p.AvgOutputTokens) / float64(p.AvgTokens) * 100
		// 30% output ratio → 25 pts, 0% → 0 pts
		prodScore = ratio * 25.0 / 30.0
		if prodScore > 25 {
			prodScore = 25
		}
		if prodScore < 0 {
			prodScore = 0
		}
	}

	score := int(errScore + retryScore + costScore + prodScore)
	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}
	return score
}

// log10Approx provides a rough log base 10 approximation using natural log.
// log10(x) = ln(x) / ln(10) ≈ ln(x) / 2.302585
func log10Approx(x float64) float64 {
	if x <= 0 {
		return 0
	}
	// Use the property: log10(x) ≈ (integer part from digit count) + fractional
	// Simple iterative approach for small values.
	result := 0.0
	for x >= 10 {
		x /= 10
		result++
	}
	for x < 1 {
		x *= 10
		result--
	}
	// Linear interpolation between 1 and 10.
	result += (x - 1) / 9.0
	return result
}

// buildFitnessRecommendations generates actionable recommendations from task profiles.
func buildFitnessRecommendations(profiles []TaskTypeProfile) []string {
	var recs []string

	for _, tp := range profiles {
		if len(tp.Models) < 2 {
			continue // need at least 2 models to compare
		}

		best := tp.Models[0]
		worst := tp.Models[len(tp.Models)-1]

		// Recommend model switch if score gap is significant.
		if best.FitnessScore-worst.FitnessScore >= 20 && worst.SessionCount >= 3 {
			recs = append(recs, fmt.Sprintf(
				"For %s tasks, %s (score %d) outperforms %s (score %d). Consider switching — %d sessions could benefit.",
				tp.TaskType, best.Model, best.FitnessScore, worst.Model, worst.FitnessScore, worst.SessionCount,
			))
		}

		// Recommend cost saving if a cheaper model has similar quality.
		if len(tp.Models) >= 2 {
			for i := 1; i < len(tp.Models); i++ {
				alt := tp.Models[i]
				if alt.AvgCost < best.AvgCost*0.5 && best.FitnessScore-alt.FitnessScore <= 10 {
					savingsPct := (1 - alt.AvgCost/best.AvgCost) * 100
					recs = append(recs, fmt.Sprintf(
						"For %s tasks, %s is %.0f%% cheaper than %s with similar quality (score %d vs %d).",
						tp.TaskType, alt.Model, savingsPct, best.Model, alt.FitnessScore, best.FitnessScore,
					))
					break // only first cost saving rec per task type
				}
			}
		}

		// Highlight high error rates.
		for _, m := range tp.Models {
			if m.ErrorRate > 15 && m.SessionCount >= 3 {
				recs = append(recs, fmt.Sprintf(
					"%s has a %.0f%% error rate for %s tasks (%d sessions). Consider using %s instead (score %d).",
					m.Model, m.ErrorRate, tp.TaskType, m.SessionCount, best.Model, best.FitnessScore,
				))
			}
		}
	}

	// Cap at 5 recommendations.
	if len(recs) > 5 {
		recs = recs[:5]
	}
	return recs
}
