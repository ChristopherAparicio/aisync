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

	// Load fork dedup map: fork session ID → fork point.
	forkPoints := s.buildForkPointMap()

	// Track per-backend aggregates.
	type backendAgg struct {
		messageCount  int
		totalTokens   int
		estimatedCost float64
		actualCost    float64
		sessionCount  int
	}
	perBackend := make(map[string]*backendAgg)

	// Load full sessions for cost calculation + model breakdown.
	var loaded []sessionCostEntry
	var apiOnly []sessionCostEntry // entries with actual provider cost (API billing)
	globalModels := make(map[string]*forecastModelAgg)

	// Backend × model cross-tabulation for treemap visualization.
	treemapAgg := make(map[string]map[string]*treemapModelAgg)

	// Batch-fetch all full sessions in a single query (3 SQL statements total)
	// instead of N×3 queries from a per-session Get() loop. Missing sessions are
	// silently absent from the result map and the loop below treats them as skips.
	filteredIDs := make([]session.ID, 0, len(filtered))
	for _, sm := range filtered {
		filteredIDs = append(filteredIDs, sm.ID)
	}
	fullSessions, batchErr := s.store.GetBatch(filteredIDs)
	if batchErr != nil {
		return nil, fmt.Errorf("batch loading sessions for forecast: %w", batchErr)
	}

	for _, sm := range filtered {
		full, ok := fullSessions[sm.ID]
		if !ok {
			continue
		}
		estimate := s.pricing.SessionCost(full)

		// Determine fork dedup offset for this session.
		forkOffset := 0
		if forkPoints != nil {
			if fp, isFork := forkPoints[full.ID]; isFork {
				forkOffset = fp
			}
		}

		// If this is a fork session, subtract shared prefix tokens/cost from the estimate.
		adjustedCost := estimate.TotalCost.TotalCost
		adjustedActual := estimate.Breakdown.ActualCost.TotalCost
		adjustedTokens := full.TokenUsage.TotalTokens
		if forkOffset > 0 {
			var sharedTokens int
			var sharedCost float64
			var sharedActual float64
			for k := 0; k < forkOffset && k < len(full.Messages); k++ {
				msg := &full.Messages[k]
				sharedTokens += msg.InputTokens + msg.OutputTokens
				sharedActual += msg.ProviderCost
			}
			// Estimate shared cost proportionally: (sharedTokens/totalTokens) × totalCost.
			if full.TokenUsage.TotalTokens > 0 {
				sharedCost = adjustedCost * float64(sharedTokens) / float64(full.TokenUsage.TotalTokens)
			}
			adjustedCost -= sharedCost
			adjustedActual -= sharedActual
			adjustedTokens -= sharedTokens
			if adjustedCost < 0 {
				adjustedCost = 0
			}
			if adjustedActual < 0 {
				adjustedActual = 0
			}
			if adjustedTokens < 0 {
				adjustedTokens = 0
			}
		}

		entry := sessionCostEntry{
			createdAt:  full.CreatedAt,
			cost:       adjustedCost,
			actualCost: adjustedActual,
			tokens:     adjustedTokens,
		}
		loaded = append(loaded, entry)

		// Track API-only entries (sessions with actual provider cost).
		if entry.actualCost > 0 {
			apiOnly = append(apiOnly, sessionCostEntry{
				createdAt: entry.createdAt,
				cost:      entry.actualCost,
				tokens:    entry.tokens,
			})
		}

		// Aggregate per-backend stats from message-level ProviderID.
		sessionBackends := make(map[string]bool)                 // track unique backends per session
		sessionBackendModels := make(map[string]map[string]bool) // track unique backend-model per session
		for i := range full.Messages {
			// Fork dedup: skip shared prefix messages.
			if i < forkOffset {
				continue
			}
			msg := &full.Messages[i]
			backend := msg.ProviderID
			if backend == "" {
				continue
			}
			agg, ok := perBackend[backend]
			if !ok {
				agg = &backendAgg{}
				perBackend[backend] = agg
			}
			agg.messageCount++
			agg.totalTokens += msg.InputTokens + msg.OutputTokens
			agg.actualCost += msg.ProviderCost
			if !sessionBackends[backend] {
				sessionBackends[backend] = true
				agg.sessionCount++
			}

			// Track backend→model mapping for treemap cross-tabulation.
			model := msg.Model
			if model != "" {
				if sessionBackendModels[backend] == nil {
					sessionBackendModels[backend] = make(map[string]bool)
				}
				sessionBackendModels[backend][model] = true
			}
		}

		// Also adjust model breakdown — scale down proportionally for forks.
		costScale := 1.0
		if forkOffset > 0 && estimate.TotalCost.TotalCost > 0 {
			costScale = adjustedCost / estimate.TotalCost.TotalCost
		}

		// Determine dominant backend for this session (for treemap cross-tab).
		// Each model may appear in one backend within a session.
		modelBackend := make(map[string]string) // model → backend
		for backend, models := range sessionBackendModels {
			for model := range models {
				modelBackend[model] = backend
			}
		}

		for _, mc := range estimate.PerModel {
			g, ok := globalModels[mc.Model]
			if !ok {
				g = &forecastModelAgg{}
				globalModels[mc.Model] = g
			}
			scaledCost := mc.Cost.TotalCost * costScale
			scaledTokens := int(float64(mc.InputTokens+mc.OutputTokens) * costScale)
			g.cost += scaledCost
			g.tokens += scaledTokens
			g.count++

			// Cross-tabulate into treemap: backend × model.
			backend := modelBackend[mc.Model]
			if backend == "" {
				backend = "(unknown)"
			}
			if treemapAgg[backend] == nil {
				treemapAgg[backend] = make(map[string]*treemapModelAgg)
			}
			bma, bmOk := treemapAgg[backend][mc.Model]
			if !bmOk {
				bma = &treemapModelAgg{}
				treemapAgg[backend][mc.Model] = bma
			}
			bma.cost += scaledCost
			bma.tokens += scaledTokens
			bma.sessionCount++
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
			Backend:      backend,
			BillingType:  "auto",
			MessageCount: agg.messageCount,
			TotalTokens:  agg.totalTokens,
			ActualCost:   math.Round(agg.actualCost*10000) / 10000,
			SessionCount: agg.sessionCount,
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

	// Compute estimated cost per backend from model breakdown.
	// We distribute model cost to backends based on token share.
	if totalCost > 0 {
		for i := range backendCosts {
			// Rough estimate: backend's share of total tokens × total estimated cost.
			totalTokensAll := 0
			for _, bc := range backendCosts {
				totalTokensAll += bc.TotalTokens
			}
			if totalTokensAll > 0 {
				backendCosts[i].EstimatedCost = math.Round(totalCost*float64(backendCosts[i].TotalTokens)/float64(totalTokensAll)*10000) / 10000
			}
		}
	}

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
