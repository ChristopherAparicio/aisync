package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/pricing"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Cost Forecasting ──

// ForecastRequest contains inputs for cost forecasting.
type ForecastRequest struct {
	ProjectPath string // optional — limit to this project
	Branch      string // optional — limit to this branch
	Period      string // "daily" or "weekly" (default: "weekly")
	Days        int    // look-back window in days (default: 90)
}

// Forecast analyzes historical session costs and projects future spending.
// It buckets sessions by time period, applies linear regression for trend,
// and recommends cheaper model alternatives.
func (s *SessionService) Forecast(ctx context.Context, req ForecastRequest) (*session.ForecastResult, error) {
	period := req.Period
	if period == "" {
		period = "weekly"
	}
	if period != "daily" && period != "weekly" {
		return nil, fmt.Errorf("period must be 'daily' or 'weekly', got %q", period)
	}

	lookbackDays := req.Days
	if lookbackDays <= 0 {
		lookbackDays = 90
	}

	now := time.Now().UTC()
	since := now.AddDate(0, 0, -lookbackDays)

	// Query all sessions in the time window.
	summaries, err := s.store.List(session.ListOptions{
		ProjectPath: req.ProjectPath,
		Branch:      req.Branch,
		All:         req.Branch == "" && req.ProjectPath == "",
	})
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	// Filter to time window.
	var filtered []session.Summary
	for _, sm := range summaries {
		if !sm.CreatedAt.Before(since) {
			filtered = append(filtered, sm)
		}
	}

	if len(filtered) == 0 {
		return &session.ForecastResult{
			Period:   period,
			TrendDir: "stable",
		}, nil
	}

	// Load full sessions for cost calculation + model breakdown.
	var loaded []sessionCostEntry
	globalModels := make(map[string]*forecastModelAgg)

	for _, sm := range filtered {
		full, getErr := s.store.Get(sm.ID)
		if getErr != nil {
			continue
		}
		estimate := s.pricing.SessionCost(full)
		loaded = append(loaded, sessionCostEntry{
			createdAt: full.CreatedAt,
			cost:      estimate.TotalCost.TotalCost,
			tokens:    full.TokenUsage.TotalTokens,
		})
		for _, mc := range estimate.PerModel {
			g, ok := globalModels[mc.Model]
			if !ok {
				g = &forecastModelAgg{}
				globalModels[mc.Model] = g
			}
			g.cost += mc.Cost.TotalCost
			g.tokens += mc.InputTokens + mc.OutputTokens
			g.count++
		}
	}

	// Build time buckets.
	bucketDuration := 7 * 24 * time.Hour // weekly
	if period == "daily" {
		bucketDuration = 24 * time.Hour
	}

	buckets := buildCostBuckets(loaded, since, now, bucketDuration)

	// Compute totals.
	var totalCost float64
	for _, b := range buckets {
		totalCost += b.Cost
	}

	avgPerBucket := 0.0
	if len(buckets) > 0 {
		avgPerBucket = totalCost / float64(len(buckets))
	}

	// Linear regression on bucket costs for trend.
	trendPerDay, trendDir := computeTrend(buckets, bucketDuration)

	// Project forward.
	projected30d := math.Max(0, avgPerBucket*30.0/bucketDuration.Hours()*24+trendPerDay*15) // avg + mid-point trend
	projected90d := math.Max(0, avgPerBucket*90.0/bucketDuration.Hours()*24+trendPerDay*45)

	// Model breakdown with recommendations.
	modelBreakdown := buildModelBreakdown(globalModels, totalCost, s.pricing)

	return &session.ForecastResult{
		Period:         period,
		Buckets:        buckets,
		TotalCost:      totalCost,
		AvgPerBucket:   avgPerBucket,
		SessionCount:   len(loaded),
		Projected30d:   math.Round(projected30d*10000) / 10000, // round to 4 decimals
		Projected90d:   math.Round(projected90d*10000) / 10000,
		TrendPerDay:    math.Round(trendPerDay*10000) / 10000,
		TrendDir:       trendDir,
		ModelBreakdown: modelBreakdown,
	}, nil
}

// buildCostBuckets groups session costs into time buckets.
func buildCostBuckets(sessions []sessionCostEntry, start, end time.Time, bucketDur time.Duration) []session.CostBucket {
	if len(sessions) == 0 {
		return nil
	}

	// Determine bucket boundaries.
	var buckets []session.CostBucket
	for t := start; t.Before(end); t = t.Add(bucketDur) {
		bucketEnd := t.Add(bucketDur)
		if bucketEnd.After(end) {
			bucketEnd = end
		}
		buckets = append(buckets, session.CostBucket{
			Start: t,
			End:   bucketEnd,
		})
	}

	// Assign sessions to buckets.
	for _, sc := range sessions {
		for i := range buckets {
			if !sc.createdAt.Before(buckets[i].Start) && sc.createdAt.Before(buckets[i].End) {
				buckets[i].Cost += sc.cost
				buckets[i].Tokens += sc.tokens
				buckets[i].SessionCount++
				break
			}
		}
	}

	return buckets
}

// sessionCostEntry is a lightweight struct for bucket building.
type sessionCostEntry struct {
	createdAt time.Time
	cost      float64
	tokens    int
}

// forecastModelAgg accumulates per-model cost data for forecasting.
type forecastModelAgg struct {
	cost   float64
	tokens int
	count  int
}

// computeTrend applies simple linear regression on bucket costs to determine the trend.
// Returns the daily cost change and a direction string.
func computeTrend(buckets []session.CostBucket, bucketDur time.Duration) (float64, string) {
	n := len(buckets)
	if n < 2 {
		return 0, "stable"
	}

	// Linear regression: y = a + b*x, where x is bucket index, y is cost.
	var sumX, sumY, sumXY, sumX2 float64
	for i, b := range buckets {
		x := float64(i)
		y := b.Cost
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	nf := float64(n)
	denom := nf*sumX2 - sumX*sumX
	if denom == 0 {
		return 0, "stable"
	}

	slope := (nf*sumXY - sumX*sumY) / denom // cost change per bucket
	daysPerBucket := bucketDur.Hours() / 24.0
	trendPerDay := slope / daysPerBucket

	dir := "stable"
	// Only flag as increasing/decreasing if the change is >5% of the average.
	avg := sumY / nf
	if avg > 0 && math.Abs(slope)/avg > 0.05 {
		if trendPerDay > 0 {
			dir = "increasing"
		} else {
			dir = "decreasing"
		}
	}

	return trendPerDay, dir
}

// buildModelBreakdown creates per-model cost data with savings recommendations.
func buildModelBreakdown(models map[string]*forecastModelAgg, totalCost float64, calc *pricing.Calculator) []session.ModelForecast {
	entries := make([]session.ModelForecast, 0, len(models))

	for model, agg := range models {
		share := 0.0
		if totalCost > 0 {
			share = (agg.cost / totalCost) * 100
		}

		var rec string
		if altModel, savings, ok := calc.CheaperAlternative(model); ok && savings > 0.1 {
			rec = fmt.Sprintf("Switch to %s to save ~%.0f%%", altModel, savings*100)
		}

		entries = append(entries, session.ModelForecast{
			Model:          model,
			Cost:           agg.cost,
			Tokens:         agg.tokens,
			SessionCount:   agg.count,
			Share:          math.Round(share*10) / 10,
			Recommendation: rec,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Cost > entries[j].Cost // most expensive first
	})

	return entries
}
