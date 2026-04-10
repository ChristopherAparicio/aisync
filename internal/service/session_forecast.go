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

// treemapModelAgg accumulates backend×model cost data for treemap visualization.
type treemapModelAgg struct {
	cost         float64
	tokens       int
	sessionCount int
}

// ── Cost Forecasting ──

// ForecastRequest contains inputs for cost forecasting.
type ForecastRequest struct {
	ProjectPath string // optional — limit to this project
	Branch      string // optional — limit to this branch
	Period      string // "daily" or "weekly" (default: "weekly")
	Days        int    // look-back window in days (default: 90)
}

// Forecast analyzes historical session costs and projects future spending.
//
// Reads from the materialized session_analytics table (CQRS read model)
// instead of loading all full session payloads. Per-session cost data
// (estimated, actual, deduplicated, fork offset) is pre-computed by
// stampAnalytics(). Per-backend and per-model breakdowns are approximated
// from the dominant backend/model of each session — a reasonable
// approximation since >95% of sessions use a single backend and model.
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

	// Query pre-computed analytics rows instead of loading full sessions.
	filter := session.AnalyticsFilter{
		ProjectPath:      req.ProjectPath,
		Since:            since,
		MinSchemaVersion: session.AnalyticsSchemaVersion,
	}
	analyticsRows, err := s.store.QueryAnalytics(filter)
	if err != nil {
		return nil, fmt.Errorf("querying analytics for forecast: %w", err)
	}

	// Branch filter: QueryAnalytics doesn't filter by branch, so we do it
	// in-memory. This is cheap since we're iterating already-light rows.
	if req.Branch != "" {
		var branchFiltered []session.AnalyticsRow
		for _, ar := range analyticsRows {
			if ar.Branch == req.Branch {
				branchFiltered = append(branchFiltered, ar)
			}
		}
		analyticsRows = branchFiltered
	}

	if len(analyticsRows) == 0 {
		return &session.ForecastResult{
			Period:   period,
			TrendDir: "stable",
		}, nil
	}

	// Track per-backend aggregates.
	type backendAgg struct {
		totalTokens   int
		estimatedCost float64
		actualCost    float64
		sessionCount  int
	}
	perBackend := make(map[string]*backendAgg)

	var loaded []sessionCostEntry
	var apiOnly []sessionCostEntry
	globalModels := make(map[string]*forecastModelAgg)
	treemapAgg := make(map[string]map[string]*treemapModelAgg)

	for _, ar := range analyticsRows {
		// Use DeduplicatedCost if the session is a fork (fork-adjusted),
		// otherwise use the full EstimatedCost.
		adjustedCost := ar.EstimatedCost
		if ar.ForkOffset > 0 && ar.DeduplicatedCost > 0 {
			adjustedCost = ar.DeduplicatedCost
		}

		entry := sessionCostEntry{
			createdAt:  ar.CreatedAt,
			cost:       adjustedCost,
			actualCost: ar.ActualCost,
			tokens:     ar.TotalTokens,
		}
		loaded = append(loaded, entry)

		// Track API-only entries (sessions with actual provider cost).
		if ar.ActualCost > 0 {
			apiOnly = append(apiOnly, sessionCostEntry{
				createdAt: ar.CreatedAt,
				cost:      ar.ActualCost,
				tokens:    ar.TotalTokens,
			})
		}

		// Per-backend stats: each session attributed to its dominant backend.
		backend := ar.Backend
		if backend == "" {
			backend = "(unknown)"
		}
		agg, ok := perBackend[backend]
		if !ok {
			agg = &backendAgg{}
			perBackend[backend] = agg
		}
		agg.totalTokens += ar.TotalTokens
		agg.estimatedCost += adjustedCost
		agg.actualCost += ar.ActualCost
		agg.sessionCount++

		// Per-model and treemap: each session attributed to dominant model.
		model := ar.DominantModel
		if model == "" {
			model = "(unknown)"
		}
		g, gOk := globalModels[model]
		if !gOk {
			g = &forecastModelAgg{}
			globalModels[model] = g
		}
		g.cost += adjustedCost
		g.tokens += ar.TotalTokens
		g.count++

		// Treemap: backend × model.
		if treemapAgg[backend] == nil {
			treemapAgg[backend] = make(map[string]*treemapModelAgg)
		}
		bma, bmOk := treemapAgg[backend][model]
		if !bmOk {
			bma = &treemapModelAgg{}
			treemapAgg[backend][model] = bma
		}
		bma.cost += adjustedCost
		bma.tokens += ar.TotalTokens
		bma.sessionCount++
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

	// Project forward (API-equivalent — all sessions).
	projected30d := math.Max(0, avgPerBucket*30.0/bucketDuration.Hours()*24+trendPerDay*15)
	projected90d := math.Max(0, avgPerBucket*90.0/bucketDuration.Hours()*24+trendPerDay*45)

	// API-only projections (real spend — only sessions with actual provider costs).
	var apiProjected30d, apiProjected90d, apiTrendPerDay float64
	var apiTrendDir string
	if len(apiOnly) > 0 {
		apiBuckets := buildCostBuckets(apiOnly, since, now, bucketDuration)
		var apiTotal float64
		for _, b := range apiBuckets {
			apiTotal += b.Cost
		}
		apiAvg := 0.0
		if len(apiBuckets) > 0 {
			apiAvg = apiTotal / float64(len(apiBuckets))
		}
		apiTrendPerDay, apiTrendDir = computeTrend(apiBuckets, bucketDuration)
		apiProjected30d = math.Max(0, apiAvg*30.0/bucketDuration.Hours()*24+apiTrendPerDay*15)
		apiProjected90d = math.Max(0, apiAvg*90.0/bucketDuration.Hours()*24+apiTrendPerDay*45)
	} else {
		apiTrendDir = "stable"
	}

	// Subscription monthly total from config.
	var subscriptionMonthly float64
	if s.cfg != nil {
		for _, cost := range s.cfg.GetSubscriptionCosts() {
			subscriptionMonthly += cost
		}
	}

	// Build per-backend cost summaries.
	var backendCosts []session.BackendCostSummary
	for backend, agg := range perBackend {
		bcs := session.BackendCostSummary{
			Backend:       backend,
			BillingType:   "auto",
			TotalTokens:   agg.totalTokens,
			EstimatedCost: math.Round(agg.estimatedCost*10000) / 10000,
			ActualCost:    math.Round(agg.actualCost*10000) / 10000,
			SessionCount:  agg.sessionCount,
		}
		// Resolve billing type from config.
		if s.cfg != nil {
			bt := s.cfg.ResolveBillingType(backend)
			bcs.BillingType = bt
			if bc := s.cfg.GetBackendBilling(backend); bc != nil {
				bcs.PlanName = bc.PlanName
				bcs.MonthlyCost = bc.MonthlyCost
			}
		}
		// Infer billing type if "auto": if actual cost > 0, it's API; otherwise subscription.
		if bcs.BillingType == "auto" {
			if bcs.ActualCost > 0 {
				bcs.BillingType = "api"
			} else {
				bcs.BillingType = "subscription"
			}
		}
		backendCosts = append(backendCosts, bcs)
	}

	// Sort backends: API first (real cost), then subscription, then free.
	sort.Slice(backendCosts, func(i, j int) bool {
		order := map[string]int{"api": 0, "subscription": 1, "free": 2}
		oi, oj := order[backendCosts[i].BillingType], order[backendCosts[j].BillingType]
		if oi != oj {
			return oi < oj
		}
		return backendCosts[i].ActualCost > backendCosts[j].ActualCost
	})

	// Model breakdown with recommendations.
	modelBreakdown := buildModelBreakdown(globalModels, totalCost, s.pricing)

	totalReal30d := subscriptionMonthly + math.Round(apiProjected30d*10000)/10000

	return &session.ForecastResult{
		Period:       period,
		Buckets:      buckets,
		TotalCost:    totalCost,
		AvgPerBucket: avgPerBucket,
		SessionCount: len(loaded),
		Projected30d: math.Round(projected30d*10000) / 10000,
		Projected90d: math.Round(projected90d*10000) / 10000,
		TrendPerDay:  math.Round(trendPerDay*10000) / 10000,
		TrendDir:     trendDir,

		// API-only projections
		APIProjected30d: math.Round(apiProjected30d*10000) / 10000,
		APIProjected90d: math.Round(apiProjected90d*10000) / 10000,
		APITrendPerDay:  math.Round(apiTrendPerDay*10000) / 10000,
		APITrendDir:     apiTrendDir,

		// Subscription costs
		SubscriptionMonthly: subscriptionMonthly,
		TotalReal30d:        totalReal30d,

		// Per-backend breakdown
		BackendCosts: backendCosts,

		ModelBreakdown: modelBreakdown,

		// Cost treemap: backend → model hierarchy
		Treemap: buildTreemapNodes(treemapAgg),
	}, nil
}

// buildTreemapNodes converts the backend×model aggregation map into treemap nodes.
func buildTreemapNodes(agg map[string]map[string]*treemapModelAgg) []session.CostTreemapNode {
	if len(agg) == 0 {
		return nil
	}

	data := make(map[string]map[string]session.CostTreemapNode)
	for backend, models := range agg {
		data[backend] = make(map[string]session.CostTreemapNode)
		for model, bma := range models {
			data[backend][model] = session.CostTreemapNode{
				Name:         model,
				Cost:         bma.cost,
				Tokens:       bma.tokens,
				SessionCount: bma.sessionCount,
			}
		}
	}

	return session.BuildCostTreemap(data)
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
	createdAt  time.Time
	cost       float64 // API-equivalent cost
	actualCost float64 // provider-reported cost (0 for subscription)
	tokens     int
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
