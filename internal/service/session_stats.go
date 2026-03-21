package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Stats ──

// StatsRequest contains inputs for computing statistics.
type StatsRequest struct {
	ProjectPath     string
	Branch          string
	Provider        session.ProviderName
	OwnerID         string // filter by session owner (empty = no filter)
	SessionType     string // filter by session type (feature, bug, etc.)
	ProjectCategory string // filter by project category (backend, frontend, etc.)
	All             bool
	IncludeTools    bool // if true, aggregate tool usage across sessions
}

// BranchStats holds aggregated stats per branch.
type BranchStats struct {
	Branch       string
	TotalTokens  int
	TotalCost    float64 // API-equivalent cost in USD (estimated from token rates)
	ActualCost   float64 // actual cost reported by providers (0 for subscriptions)
	SessionCount int
}

// TypeStats holds aggregated stats per session type.
type TypeStats struct {
	Type         string `json:"type"`
	SessionCount int    `json:"session_count"`
	TotalTokens  int    `json:"total_tokens"`
	TotalErrors  int    `json:"total_errors"`
	AvgTokens    int    `json:"avg_tokens"`
}

// StatsResult contains aggregated statistics.
type StatsResult struct {
	TotalSessions int
	TotalMessages int
	TotalTokens   int
	TotalCost     float64 // API-equivalent cost in USD (estimated from token rates)
	ActualCost    float64 // actual cost reported by providers (0 for subscriptions)
	Savings       float64 // TotalCost - ActualCost
	BillingType   string  // "subscription", "api", "mixed", or ""

	// Fork deduplication
	ForkCount        int     `json:"fork_count,omitempty"`        // number of detected forks
	DeduplicatedCost float64 `json:"deduplicated_cost,omitempty"` // cost after removing fork overlap
	ForkSavings      float64 `json:"fork_savings,omitempty"`      // cost saved by deduplication

	PerBranch   []*BranchStats
	PerProvider map[session.ProviderName]int
	PerType     []TypeStats          `json:"per_type,omitempty"` // stats grouped by session_type
	TopFiles    []FileEntry          // sorted by count descending, max 10
	ToolStats   *AggregatedToolStats `json:"tool_stats,omitempty"` // populated when IncludeTools is true
}

// AggregatedToolStats holds tool usage aggregated across multiple sessions.
type AggregatedToolStats struct {
	Tools      []session.ToolUsageEntry `json:"tools"`
	TotalCalls int                      `json:"total_calls"`
	TotalCost  session.Cost             `json:"total_cost,omitempty"`
	Warning    string                   `json:"warning,omitempty"` // set if any session used compact/summary mode
}

// FileEntry is a file path with its touch count.
type FileEntry struct {
	Path  string
	Count int
}

// EstimateCost computes the cost breakdown for a session.
func (s *SessionService) EstimateCost(ctx context.Context, idOrSHA string) (*session.CostEstimate, error) {
	sess, err := s.Get(idOrSHA)
	if err != nil {
		return nil, err
	}
	return s.pricing.SessionCost(sess), nil
}

// ToolUsage computes per-tool token usage breakdown for a session.
// If tool calls don't have token data, it estimates from content size (~4 chars/token).
func (s *SessionService) ToolUsage(ctx context.Context, idOrSHA string) (*session.ToolUsageStats, error) {
	sess, err := s.Get(idOrSHA)
	if err != nil {
		return nil, err
	}

	type toolAgg struct {
		calls        int
		inputTokens  int
		outputTokens int
		totalDur     int
		errorCount   int
	}

	perTool := make(map[string]*toolAgg)
	totalCalls := 0

	for i := range sess.Messages {
		msg := &sess.Messages[i]
		for j := range msg.ToolCalls {
			tc := &msg.ToolCalls[j]
			totalCalls++

			agg, ok := perTool[tc.Name]
			if !ok {
				agg = &toolAgg{}
				perTool[tc.Name] = agg
			}
			agg.calls++

			// Use explicit token data if available, otherwise estimate from content size.
			inTok := tc.InputTokens
			outTok := tc.OutputTokens
			if inTok == 0 && len(tc.Input) > 0 {
				inTok = estimateTokens(tc.Input)
			}
			if outTok == 0 && len(tc.Output) > 0 {
				outTok = estimateTokens(tc.Output)
			}
			agg.inputTokens += inTok
			agg.outputTokens += outTok

			if tc.DurationMs > 0 {
				agg.totalDur += tc.DurationMs
			}
			if tc.State == session.ToolStateError {
				agg.errorCount++
			}
		}
	}

	// Build sorted result.
	names := make([]string, 0, len(perTool))
	for name := range perTool {
		names = append(names, name)
	}
	sort.Strings(names)

	var grandTotal int
	entries := make([]session.ToolUsageEntry, 0, len(names))
	for _, name := range names {
		agg := perTool[name]
		total := agg.inputTokens + agg.outputTokens
		grandTotal += total

		entry := session.ToolUsageEntry{
			Name:         name,
			Calls:        agg.calls,
			InputTokens:  agg.inputTokens,
			OutputTokens: agg.outputTokens,
			TotalTokens:  total,
			ErrorCount:   agg.errorCount,
		}
		if agg.calls > 0 && agg.totalDur > 0 {
			entry.AvgDuration = agg.totalDur / agg.calls
		}
		entries = append(entries, entry)
	}

	// Compute percentages.
	for i := range entries {
		if grandTotal > 0 {
			entries[i].Percentage = float64(entries[i].TotalTokens) / float64(grandTotal) * 100
		}
	}

	// Sort by total tokens descending (most expensive first).
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].TotalTokens > entries[j].TotalTokens
	})

	// Optionally compute cost per tool.
	var totalCost session.Cost
	if s.pricing != nil {
		for i := range entries {
			e := &entries[i]
			// Use session's primary model for tool cost estimation.
			model := primaryModel(sess)
			if model != "" {
				e.Cost = s.pricing.MessageCost(model, e.InputTokens, e.OutputTokens)
				totalCost.InputCost += e.Cost.InputCost
				totalCost.OutputCost += e.Cost.OutputCost
				totalCost.TotalCost += e.Cost.TotalCost
			}
		}
		totalCost.Currency = "USD"
	}

	result := &session.ToolUsageStats{
		Tools:      entries,
		TotalCalls: totalCalls,
		TotalCost:  totalCost,
	}

	// Warn when storage mode limits tool call data fidelity.
	if sess.StorageMode == session.StorageModeCompact || sess.StorageMode == session.StorageModeSummary {
		result.Warning = fmt.Sprintf("session was captured in %q mode — tool call data may be incomplete; use --mode full for accurate tool accounting", sess.StorageMode)
	}

	return result, nil
}

// estimateTokens roughly estimates token count from text length.
// Uses the common heuristic of ~4 characters per token for English/JSON text.
func estimateTokens(text string) int {
	n := len(text) / 4
	if n == 0 && len(text) > 0 {
		n = 1
	}
	return n
}

// primaryModel returns the most-used model in a session (by message count).
func primaryModel(sess *session.Session) string {
	counts := make(map[string]int)
	for i := range sess.Messages {
		if m := sess.Messages[i].Model; m != "" {
			counts[m]++
		}
	}
	var best string
	var bestCount int
	for m, c := range counts {
		if c > bestCount {
			best = m
			bestCount = c
		}
	}
	return best
}

// Stats computes aggregated statistics across sessions.
func (s *SessionService) Stats(req StatsRequest) (*StatsResult, error) {
	listOpts := session.ListOptions{
		ProjectPath: req.ProjectPath,
		All:         true,
		OwnerID:     session.ID(req.OwnerID),
	}

	if req.Branch != "" {
		listOpts.Branch = req.Branch
		listOpts.All = false
	}
	if req.Provider != "" {
		listOpts.Provider = req.Provider
	}
	if req.SessionType != "" {
		listOpts.SessionType = req.SessionType
	}
	if req.ProjectCategory != "" {
		listOpts.ProjectCategory = req.ProjectCategory
	}

	summaries, err := s.store.List(listOpts)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}

	result := &StatsResult{
		PerProvider: make(map[session.ProviderName]int),
	}

	perBranch := make(map[string]*BranchStats)
	perType := make(map[string]*TypeStats)
	fileCounts := make(map[string]int)

	// Tool aggregation state (used when IncludeTools is true).
	type toolAgg struct {
		calls, errors, inputTok, outputTok, totalDur int
	}
	var perTool map[string]*toolAgg
	var hasCompactSessions bool
	if req.IncludeTools {
		perTool = make(map[string]*toolAgg)
	}

	for _, sm := range summaries {
		result.TotalSessions++
		result.TotalTokens += sm.TotalTokens
		result.TotalMessages += sm.MessageCount

		// Per-type
		if sm.SessionType != "" {
			ts, ok := perType[sm.SessionType]
			if !ok {
				ts = &TypeStats{Type: sm.SessionType}
				perType[sm.SessionType] = ts
			}
			ts.SessionCount++
			ts.TotalTokens += sm.TotalTokens
			ts.TotalErrors += sm.ErrorCount
		}

		// Per-branch
		bs, ok := perBranch[sm.Branch]
		if !ok {
			bs = &BranchStats{Branch: sm.Branch}
			perBranch[sm.Branch] = bs
		}
		bs.SessionCount++
		bs.TotalTokens += sm.TotalTokens

		// Per-provider
		result.PerProvider[sm.Provider]++

		// File changes + cost + tool usage (requires loading full session)
		full, getErr := s.store.Get(sm.ID)
		if getErr == nil {
			for _, fc := range full.FileChanges {
				fileCounts[fc.FilePath]++
			}

			// Cost estimation (dual: API-equivalent + actual)
			est := s.pricing.SessionCost(full)
			sessionCost := est.TotalCost.TotalCost
			result.TotalCost += sessionCost
			bs.TotalCost += sessionCost

			actualCost := est.Breakdown.ActualCost.TotalCost
			result.ActualCost += actualCost
			bs.ActualCost += actualCost

			// Tool aggregation
			if req.IncludeTools {
				if full.StorageMode == session.StorageModeCompact || full.StorageMode == session.StorageModeSummary {
					hasCompactSessions = true
				}
				for i := range full.Messages {
					for j := range full.Messages[i].ToolCalls {
						tc := &full.Messages[i].ToolCalls[j]
						agg, exists := perTool[tc.Name]
						if !exists {
							agg = &toolAgg{}
							perTool[tc.Name] = agg
						}
						agg.calls++
						inTok, outTok := tc.InputTokens, tc.OutputTokens
						if inTok == 0 && len(tc.Input) > 0 {
							inTok = estimateTokens(tc.Input)
						}
						if outTok == 0 && len(tc.Output) > 0 {
							outTok = estimateTokens(tc.Output)
						}
						agg.inputTok += inTok
						agg.outputTok += outTok
						if tc.DurationMs > 0 {
							agg.totalDur += tc.DurationMs
						}
						if tc.State == session.ToolStateError {
							agg.errors++
						}
					}
				}
			}
		}
	}

	// Compute savings and billing type.
	result.Savings = result.TotalCost - result.ActualCost
	if result.ActualCost > 0 && result.Savings > 0 {
		result.BillingType = "mixed"
	} else if result.ActualCost > 0 {
		result.BillingType = "api"
	} else {
		result.BillingType = "subscription"
	}

	// Sort branches by token count descending
	branchList := make([]*BranchStats, 0, len(perBranch))
	for _, bs := range perBranch {
		branchList = append(branchList, bs)
	}
	sort.Slice(branchList, func(i, j int) bool {
		return branchList[i].TotalTokens > branchList[j].TotalTokens
	})
	result.PerBranch = branchList

	// Per-type stats
	typeList := make([]TypeStats, 0, len(perType))
	for _, ts := range perType {
		if ts.SessionCount > 0 {
			ts.AvgTokens = ts.TotalTokens / ts.SessionCount
		}
		typeList = append(typeList, *ts)
	}
	sort.Slice(typeList, func(i, j int) bool {
		return typeList[i].SessionCount > typeList[j].SessionCount
	})
	result.PerType = typeList

	// Top files (up to 10)
	files := make([]FileEntry, 0, len(fileCounts))
	for path, count := range fileCounts {
		files = append(files, FileEntry{Path: path, Count: count})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Count > files[j].Count
	})
	if len(files) > 10 {
		files = files[:10]
	}
	result.TopFiles = files

	// Build aggregated tool stats.
	if req.IncludeTools && len(perTool) > 0 {
		names := make([]string, 0, len(perTool))
		for name := range perTool {
			names = append(names, name)
		}
		sort.Strings(names)

		var grandTotal, totalCalls int
		entries := make([]session.ToolUsageEntry, 0, len(names))
		for _, name := range names {
			agg := perTool[name]
			total := agg.inputTok + agg.outputTok
			grandTotal += total
			totalCalls += agg.calls

			entry := session.ToolUsageEntry{
				Name:         name,
				Calls:        agg.calls,
				InputTokens:  agg.inputTok,
				OutputTokens: agg.outputTok,
				TotalTokens:  total,
				ErrorCount:   agg.errors,
			}
			if agg.calls > 0 && agg.totalDur > 0 {
				entry.AvgDuration = agg.totalDur / agg.calls
			}
			entries = append(entries, entry)
		}

		// Compute percentages.
		for i := range entries {
			if grandTotal > 0 {
				entries[i].Percentage = float64(entries[i].TotalTokens) / float64(grandTotal) * 100
			}
		}

		// Sort by total tokens descending.
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].TotalTokens > entries[j].TotalTokens
		})

		aggStats := &AggregatedToolStats{
			Tools:      entries,
			TotalCalls: totalCalls,
		}
		if hasCompactSessions {
			aggStats.Warning = "some sessions were captured in compact/summary mode — tool data may be incomplete"
		}
		result.ToolStats = aggStats
	}

	// Fork deduplication: subtract shared tokens from cost estimate.
	dedupInput, dedupOutput, dedupErr := s.store.GetTotalDeduplication()
	if dedupErr == nil && (dedupInput > 0 || dedupOutput > 0) {
		// Estimate cost of duplicated tokens (use average model price).
		avgInputRate := 3.0   // $3/1M (Claude Sonnet 4 rate as default)
		avgOutputRate := 15.0 // $15/1M
		if result.TotalCost > 0 && result.TotalTokens > 0 {
			// Use actual blended rate from this set of sessions.
			avgInputRate = result.TotalCost / float64(result.TotalTokens) * 1_000_000
			avgOutputRate = avgInputRate * 5 // approximate input:output cost ratio
		}
		forkCostSaved := float64(dedupInput)*avgInputRate/1_000_000 +
			float64(dedupOutput)*avgOutputRate/1_000_000
		result.ForkSavings = forkCostSaved
		result.DeduplicatedCost = result.TotalCost - forkCostSaved
		if result.DeduplicatedCost < 0 {
			result.DeduplicatedCost = 0
		}
	} else {
		result.DeduplicatedCost = result.TotalCost
	}

	return result, nil
}

// ListProjects returns all distinct projects, grouped by git remote URL
// (for git repos) or by project path (for non-git projects).
func (s *SessionService) ListProjects(_ context.Context) ([]session.ProjectGroup, error) {
	return s.store.ListProjects()
}

// ── Trend Monitoring ──

// TrendRequest specifies parameters for trend analysis.
type TrendRequest struct {
	SessionType string               // filter by session type (optional)
	Provider    session.ProviderName // filter by provider (optional)
	Period      time.Duration        // comparison period (e.g. 7*24h for weekly)
}

// TrendPeriodStats holds aggregated metrics for a single time period.
type TrendPeriodStats struct {
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	SessionCount int       `json:"session_count"`
	TotalTokens  int       `json:"total_tokens"`
	TotalErrors  int       `json:"total_errors"`
	AvgTokens    int       `json:"avg_tokens"`
	AvgErrors    float64   `json:"avg_errors"`
}

// TrendResult compares current vs previous period.
type TrendResult struct {
	Current  TrendPeriodStats `json:"current"`
	Previous TrendPeriodStats `json:"previous"`
	Delta    TrendDelta       `json:"delta"`
}

// TrendDelta shows the change between periods.
type TrendDelta struct {
	SessionCountChange  int     `json:"session_count_change"` // absolute change
	TokensChange        int     `json:"tokens_change"`        // absolute change
	ErrorsChange        int     `json:"errors_change"`        // absolute change
	TokensChangePercent float64 `json:"tokens_change_pct"`    // percentage change
	ErrorsChangePercent float64 `json:"errors_change_pct"`    // percentage change
	Verdict             string  `json:"verdict"`              // "improving", "stable", "degrading"
}

// Trends compares session metrics between the current period and the previous one.
// Example: period=7 days → compares this week vs last week.
func (s *SessionService) Trends(_ context.Context, req TrendRequest) (*TrendResult, error) {
	period := req.Period
	if period == 0 {
		period = 7 * 24 * time.Hour // default: weekly
	}

	now := time.Now()
	currentStart := now.Add(-period)
	previousStart := currentStart.Add(-period)

	current, err := s.periodStats(req, currentStart, now)
	if err != nil {
		return nil, fmt.Errorf("current period: %w", err)
	}

	previous, err := s.periodStats(req, previousStart, currentStart)
	if err != nil {
		return nil, fmt.Errorf("previous period: %w", err)
	}

	delta := TrendDelta{
		SessionCountChange: current.SessionCount - previous.SessionCount,
		TokensChange:       current.TotalTokens - previous.TotalTokens,
		ErrorsChange:       current.TotalErrors - previous.TotalErrors,
	}

	if previous.TotalTokens > 0 {
		delta.TokensChangePercent = float64(delta.TokensChange) / float64(previous.TotalTokens) * 100
	}
	if previous.TotalErrors > 0 {
		delta.ErrorsChangePercent = float64(delta.ErrorsChange) / float64(previous.TotalErrors) * 100
	}

	// Determine verdict.
	if delta.ErrorsChange < 0 && delta.TokensChange <= 0 {
		delta.Verdict = "improving"
	} else if delta.ErrorsChange > 0 || delta.TokensChangePercent > 20 {
		delta.Verdict = "degrading"
	} else {
		delta.Verdict = "stable"
	}

	return &TrendResult{
		Current:  *current,
		Previous: *previous,
		Delta:    delta,
	}, nil
}

// periodStats computes aggregated stats for sessions in [from, to).
func (s *SessionService) periodStats(req TrendRequest, from, to time.Time) (*TrendPeriodStats, error) {
	summaries, err := s.store.List(session.ListOptions{
		All:         true,
		SessionType: req.SessionType,
		Provider:    req.Provider,
		Since:       from,
		Until:       to,
	})
	if err != nil {
		return nil, err
	}

	stats := &TrendPeriodStats{
		From: from,
		To:   to,
	}
	for _, sm := range summaries {
		stats.SessionCount++
		stats.TotalTokens += sm.TotalTokens
		stats.TotalErrors += sm.ErrorCount
	}
	if stats.SessionCount > 0 {
		stats.AvgTokens = stats.TotalTokens / stats.SessionCount
		stats.AvgErrors = float64(stats.TotalErrors) / float64(stats.SessionCount)
	}

	return stats, nil
}
