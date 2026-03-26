package service

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ComputeTokenBucketsRequest configures the token usage computation.
type ComputeTokenBucketsRequest struct {
	Granularity string // "1h" or "1d"
	Incremental bool   // if true, only compute since last run
}

// ComputeTokenBucketsResult contains the computation result.
type ComputeTokenBucketsResult struct {
	BucketsWritten     int
	ToolBucketsWritten int
	SessionsScanned    int
	MessagesScanned    int
	Duration           time.Duration
}

// buildForkPointMap loads all fork relations and builds a map of
// fork session ID → fork point (message index where the fork diverges).
// Messages before the fork point are shared with the original and should
// be skipped during token accounting to avoid double-counting.
func (s *SessionService) buildForkPointMap() map[session.ID]int {
	rels, err := s.store.ListAllForkRelations()
	if err != nil {
		log.Printf("[dedup] failed to load fork relations: %v", err)
		return nil
	}
	if len(rels) == 0 {
		return nil
	}

	m := make(map[session.ID]int, len(rels))
	for _, rel := range rels {
		// If a session appears in multiple fork relations (fork of a fork),
		// keep the maximum fork point (most messages to skip).
		if existing, ok := m[rel.ForkID]; !ok || rel.ForkPoint > existing {
			m[rel.ForkID] = rel.ForkPoint
		}
	}
	log.Printf("[dedup] loaded %d fork relations, %d sessions have dedup offsets", len(rels), len(m))
	return m
}

// ComputeTokenBuckets scans sessions and pre-computes token usage per time bucket.
//
// Buckets are keyed by (bucket_start, project_path, provider, llm_backend), so
// tokens from different LLM backends (e.g. "anthropic" vs "amazon-bedrock") are
// tracked separately. Each bucket also includes estimated and actual cost.
//
// Fork deduplication: for sessions that are forks (appear as fork_id in
// session_forks), messages before the fork_point are skipped to avoid
// double-counting the shared prefix with the original session.
//
// When Incremental=true, only processes sessions captured after the last compute.
func (s *SessionService) ComputeTokenBuckets(ctx context.Context, req ComputeTokenBucketsRequest) (*ComputeTokenBucketsResult, error) {
	start := time.Now()
	gran := req.Granularity
	if gran == "" {
		gran = "1h"
	}

	// Determine the time window for incremental mode.
	var since time.Time
	if req.Incremental {
		lastCompute, _ := s.store.GetLastBucketComputeTime(gran)
		if !lastCompute.IsZero() {
			since = lastCompute.Add(-1 * time.Hour) // overlap by 1h for safety
		}
	}

	// List all sessions (with time filter for incremental).
	listOpts := session.ListOptions{All: true}
	if !since.IsZero() {
		listOpts.Since = since
	}
	summaries, err := s.store.List(listOpts)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}

	// Load fork dedup map: fork session ID → first message index to count.
	forkPoints := s.buildForkPointMap()

	// Bucket key: bucket_start + project_path + provider + llm_backend.
	type bucketKey struct {
		start       time.Time
		projectPath string
		provider    string
		llmBackend  string
	}
	buckets := make(map[bucketKey]*session.TokenUsageBucket)

	// Tool bucket key: bucket_start + project_path + tool_name + tool_category.
	type toolBucketKey struct {
		start        time.Time
		projectPath  string
		toolName     string
		toolCategory string
	}
	toolBuckets := make(map[toolBucketKey]*session.ToolUsageBucket)

	// Track which sessions have been counted per bucket key (avoid double-counting).
	sessionCounted := make(map[bucketKey]map[session.ID]bool)

	result := &ComputeTokenBucketsResult{
		SessionsScanned: len(summaries),
	}

	var dedupSkipped int // messages skipped due to fork dedup

	for _, sm := range summaries {
		// Load full session to access per-message timestamps and tokens.
		sess, getErr := s.store.Get(sm.ID)
		if getErr != nil {
			continue
		}

		// Determine the fork dedup offset for this session.
		// Messages at index < forkOffset are shared with the original and skipped.
		forkOffset := 0
		if forkPoints != nil {
			if fp, isFork := forkPoints[sess.ID]; isFork {
				forkOffset = fp
			}
		}

		// Compute per-model cost for this session (needed for per-message cost estimation).
		var estimate *session.CostEstimate
		if s.pricing != nil {
			estimate = s.pricing.SessionCost(sess)
		}

		// Build a per-model cost-per-token map for distributing costs to individual messages.
		modelCostRate := make(map[string]float64) // model → cost per output token
		if estimate != nil {
			for _, mc := range estimate.PerModel {
				if mc.OutputTokens > 0 {
					modelCostRate[mc.Model] = mc.Cost.TotalCost / float64(mc.OutputTokens)
				} else if mc.InputTokens > 0 {
					modelCostRate[mc.Model] = mc.Cost.TotalCost / float64(mc.InputTokens)
				}
			}
		}

		for i := range sess.Messages {
			msg := &sess.Messages[i]
			if msg.Timestamp.IsZero() {
				continue
			}

			// Fork dedup: skip messages in the shared prefix.
			if i < forkOffset {
				dedupSkipped++
				continue
			}

			// Determine the bucket start for this message.
			var bucketStart time.Time
			switch gran {
			case "1d":
				bucketStart = time.Date(msg.Timestamp.Year(), msg.Timestamp.Month(), msg.Timestamp.Day(), 0, 0, 0, 0, msg.Timestamp.Location())
			default: // "1h"
				bucketStart = msg.Timestamp.Truncate(time.Hour)
			}

			// Use per-message ProviderID as the LLM backend key.
			// Falls back to empty string if not available.
			llmBackend := msg.ProviderID

			key := bucketKey{
				start:       bucketStart,
				projectPath: sess.ProjectPath,
				provider:    string(sess.Provider),
				llmBackend:  llmBackend,
			}

			b, ok := buckets[key]
			if !ok {
				b = &session.TokenUsageBucket{
					BucketStart: bucketStart,
					Granularity: gran,
					ProjectPath: sess.ProjectPath,
					Provider:    sess.Provider,
					LLMBackend:  llmBackend,
				}
				buckets[key] = b
			}

			b.InputTokens += msg.InputTokens
			b.OutputTokens += msg.OutputTokens
			b.CacheReadTokens += msg.CacheReadTokens
			b.CacheWriteTokens += msg.CacheWriteTokens
			b.MessageCount++
			result.MessagesScanned++

			// Accumulate actual cost from provider-reported data.
			if msg.ProviderCost > 0 {
				b.ActualCost += msg.ProviderCost
			}

			// Estimate API-equivalent cost for this message using the model's rate.
			if rate, hasRate := modelCostRate[msg.Model]; hasRate {
				msgTokens := msg.OutputTokens
				if msgTokens == 0 {
					msgTokens = msg.InputTokens
				}
				b.EstimatedCost += float64(msgTokens) * rate
			}

			// Count by role (human vs agent indicator).
			switch msg.Role {
			case session.RoleUser:
				b.UserMsgCount++
			case session.RoleAssistant:
				b.AssistMsgCount++
			}

			// Count tool calls and errors + feed per-tool buckets.
			for j := range msg.ToolCalls {
				tc := &msg.ToolCalls[j]
				b.ToolCallCount++
				if tc.State == session.ToolStateError {
					b.ToolErrorCount++
				}

				// Per-tool bucket (always daily for tool-level granularity).
				toolDay := time.Date(msg.Timestamp.Year(), msg.Timestamp.Month(), msg.Timestamp.Day(), 0, 0, 0, 0, msg.Timestamp.Location())
				toolCat := session.ClassifyTool(tc.Name)
				tbKey := toolBucketKey{
					start:        toolDay,
					projectPath:  sess.ProjectPath,
					toolName:     tc.Name,
					toolCategory: toolCat,
				}
				tb, tbOK := toolBuckets[tbKey]
				if !tbOK {
					tb = &session.ToolUsageBucket{
						BucketStart:  toolDay,
						Granularity:  "1d",
						ProjectPath:  sess.ProjectPath,
						ToolName:     tc.Name,
						ToolCategory: toolCat,
					}
					toolBuckets[tbKey] = tb
				}
				tb.CallCount++
				if tc.State == session.ToolStateError {
					tb.ErrorCount++
				}
				tb.TotalDuration += tc.DurationMs

				// Estimate tool token usage.
				inTok := tc.InputTokens
				if inTok == 0 {
					inTok = estimateTokens(tc.Input)
				}
				outTok := tc.OutputTokens
				if outTok == 0 {
					outTok = estimateTokens(tc.Output)
				}
				tb.InputTokens += inTok
				tb.OutputTokens += outTok

				// Estimate tool cost using the message's model rate.
				if rate, hasRate := modelCostRate[msg.Model]; hasRate {
					tb.EstimatedCost += float64(inTok+outTok) * rate
				}
			}

			// Count images and image tokens.
			for _, img := range msg.Images {
				b.ImageTokens += img.TokensEstimate
				b.ImageCount++
			}

			// Count this session in the bucket (once per session per bucket key).
			if sessionCounted[key] == nil {
				sessionCounted[key] = make(map[session.ID]bool)
			}
			if !sessionCounted[key][sess.ID] {
				sessionCounted[key][sess.ID] = true
				b.SessionCount++
			}
		}
	}

	if dedupSkipped > 0 {
		log.Printf("[token_usage] fork dedup: skipped %d shared-prefix messages", dedupSkipped)
	}

	// Persist all buckets.
	for _, b := range buckets {
		if err := s.store.UpsertTokenBucket(*b); err != nil {
			log.Printf("[token_usage] error upserting bucket: %v", err)
		}
		result.BucketsWritten++
	}

	// Persist all tool buckets.
	for _, tb := range toolBuckets {
		if err := s.store.UpsertToolBucket(*tb); err != nil {
			log.Printf("[tool_usage] error upserting tool bucket: %v", err)
		}
		result.ToolBucketsWritten++
	}

	result.Duration = time.Since(start)
	return result, nil
}

// QueryTokenUsage retrieves pre-computed token buckets for display.
type QueryTokenUsageRequest struct {
	Granularity string    // "1h" or "1d"
	Since       time.Time // start of range
	Until       time.Time // end of range
	ProjectPath string    // filter by project (empty = all)
}

func (s *SessionService) QueryTokenUsage(ctx context.Context, req QueryTokenUsageRequest) ([]session.TokenUsageBucket, error) {
	gran := req.Granularity
	if gran == "" {
		gran = "1h"
	}
	return s.store.QueryTokenBuckets(gran, req.Since, req.Until, req.ProjectPath)
}

// ToolCostSummary computes per-tool and per-MCP-server cost aggregation.
// This queries the pre-computed tool_usage_buckets.
func (s *SessionService) ToolCostSummary(ctx context.Context, projectPath string, since, until time.Time) (*session.ToolCostSummary, error) {
	buckets, err := s.store.QueryToolBuckets("1d", since, until, projectPath)
	if err != nil {
		return nil, fmt.Errorf("querying tool buckets: %w", err)
	}

	// Aggregate by tool name.
	type toolAgg struct {
		category      string
		calls         int
		inputTokens   int
		outputTokens  int
		errorCount    int
		totalDuration int
		estimatedCost float64
	}
	byTool := make(map[string]*toolAgg)

	for _, b := range buckets {
		agg, ok := byTool[b.ToolName]
		if !ok {
			agg = &toolAgg{category: b.ToolCategory}
			byTool[b.ToolName] = agg
		}
		agg.calls += b.CallCount
		agg.inputTokens += b.InputTokens
		agg.outputTokens += b.OutputTokens
		agg.errorCount += b.ErrorCount
		agg.totalDuration += b.TotalDuration
		agg.estimatedCost += b.EstimatedCost
	}

	// Build per-tool entries.
	summary := &session.ToolCostSummary{}
	for name, agg := range byTool {
		totalTokens := agg.inputTokens + agg.outputTokens
		entry := session.ToolCostEntry{
			Name:          name,
			Category:      agg.category,
			CallCount:     agg.calls,
			InputTokens:   agg.inputTokens,
			OutputTokens:  agg.outputTokens,
			TotalTokens:   totalTokens,
			ErrorCount:    agg.errorCount,
			TotalDuration: agg.totalDuration,
			EstimatedCost: agg.estimatedCost,
		}
		if agg.calls > 0 {
			entry.AvgDuration = agg.totalDuration / agg.calls
			entry.AvgCostPerCall = agg.estimatedCost / float64(agg.calls)
		}
		summary.Tools = append(summary.Tools, entry)
		summary.TotalCalls += agg.calls
		summary.TotalTokens += totalTokens
		summary.TotalCost += agg.estimatedCost

		if session.MCPServerName(agg.category) != "" {
			summary.TotalMCPCalls += agg.calls
			summary.TotalMCPCost += agg.estimatedCost
		}
	}

	// Sort tools by estimated cost descending.
	sort.Slice(summary.Tools, func(i, j int) bool {
		return summary.Tools[i].EstimatedCost > summary.Tools[j].EstimatedCost
	})

	// Aggregate by MCP server.
	type serverAgg struct {
		toolCount     int
		calls         int
		totalTokens   int
		errorCount    int
		estimatedCost float64
		tools         map[string]bool
	}
	byServer := make(map[string]*serverAgg)
	for _, entry := range summary.Tools {
		server := session.MCPServerName(entry.Category)
		if server == "" {
			continue
		}
		sa, ok := byServer[server]
		if !ok {
			sa = &serverAgg{tools: make(map[string]bool)}
			byServer[server] = sa
		}
		if !sa.tools[entry.Name] {
			sa.tools[entry.Name] = true
			sa.toolCount++
		}
		sa.calls += entry.CallCount
		sa.totalTokens += entry.TotalTokens
		sa.errorCount += entry.ErrorCount
		sa.estimatedCost += entry.EstimatedCost
	}
	for server, sa := range byServer {
		msc := session.MCPServerCost{
			Server:        server,
			ToolCount:     sa.toolCount,
			CallCount:     sa.calls,
			TotalTokens:   sa.totalTokens,
			ErrorCount:    sa.errorCount,
			EstimatedCost: sa.estimatedCost,
		}
		if sa.calls > 0 {
			msc.AvgCostPerCall = sa.estimatedCost / float64(sa.calls)
		}
		summary.MCPServers = append(summary.MCPServers, msc)
	}
	sort.Slice(summary.MCPServers, func(i, j int) bool {
		return summary.MCPServers[i].EstimatedCost > summary.MCPServers[j].EstimatedCost
	})

	return summary, nil
}

// AgentCostSummary computes per-agent cost aggregation for a project.
// This walks sessions directly (agents are session-level, not tool-level).
func (s *SessionService) AgentCostSummary(ctx context.Context, projectPath string, since, until time.Time) ([]session.AgentCostEntry, error) {
	listOpts := session.ListOptions{All: true}
	if !since.IsZero() {
		listOpts.Since = since
	}
	summaries, err := s.store.List(listOpts)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}

	type agentAgg struct {
		sessions  int
		messages  int
		tokens    int
		toolCalls int
		cost      float64
	}
	byAgent := make(map[string]*agentAgg)

	for _, sm := range summaries {
		// Filter by project if specified.
		if projectPath != "" && sm.ProjectPath != projectPath {
			continue
		}
		// Filter by time range.
		if !until.IsZero() && sm.CreatedAt.After(until) {
			continue
		}

		agent := sm.Agent
		if agent == "" {
			agent = "unknown"
		}
		agg, ok := byAgent[agent]
		if !ok {
			agg = &agentAgg{}
			byAgent[agent] = agg
		}
		agg.sessions++
		agg.messages += sm.MessageCount
		agg.tokens += sm.TotalTokens
		agg.toolCalls += sm.ToolCallCount

		// Estimate cost from tokens (blended rate).
		const blendedRatePerToken = 3.0 / 1_000_000 // ~$3/M tokens
		agg.cost += float64(sm.TotalTokens) * blendedRatePerToken
	}

	var entries []session.AgentCostEntry
	for agent, agg := range byAgent {
		entry := session.AgentCostEntry{
			Agent:         agent,
			SessionCount:  agg.sessions,
			MessageCount:  agg.messages,
			TotalTokens:   agg.tokens,
			ToolCallCount: agg.toolCalls,
			EstimatedCost: agg.cost,
		}
		if agg.sessions > 0 {
			entry.AvgCostPerSession = agg.cost / float64(agg.sessions)
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].EstimatedCost > entries[j].EstimatedCost
	})
	return entries, nil
}

// CacheEfficiency computes prompt cache usage statistics and identifies waste.
//
// The prompt cache TTL is ~5 minutes. If a user doesn't respond within that window,
// the next assistant message pays full price for all input tokens instead of the
// discounted cache-read rate (typically 10x cheaper).
//
// This method walks sessions to detect cache miss patterns (gaps > 5 min between
// messages) and estimates the cost waste.
func (s *SessionService) CacheEfficiency(ctx context.Context, projectPath string, since time.Time) (*session.CacheEfficiency, error) {
	const cacheTTLMinutes = 5
	// Pricing differential: cache read is ~10x cheaper than raw input.
	// For Opus: raw=$15/M, cache_read=$1.50/M → savings = $13.50/M per cache hit token.
	// We use a conservative average across models.
	const savingsPerMToken = 10.0 // $ saved per M tokens when cache is hit

	listOpts := session.ListOptions{All: true}
	if !since.IsZero() {
		listOpts.Since = since
	}
	summaries, err := s.store.List(listOpts)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}

	result := &session.CacheEfficiency{}

	type sessionCacheInfo struct {
		id             session.ID
		summary        string
		cacheRead      int
		cacheWrite     int
		inputTokens    int
		cacheMissCount int
		wastedTokens   int
		longestGapMins int
	}
	var sessionInfos []sessionCacheInfo
	var totalGapSum, missGapSum float64
	var totalGapCount, missGapCount int

	for _, sm := range summaries {
		if projectPath != "" && sm.ProjectPath != projectPath {
			continue
		}

		sess, getErr := s.store.Get(sm.ID)
		if getErr != nil {
			continue
		}

		info := sessionCacheInfo{
			id:      sess.ID,
			summary: sess.Summary,
		}

		// Accumulate session-level cache stats from TokenUsage.
		info.cacheRead = sess.TokenUsage.CacheRead
		info.cacheWrite = sess.TokenUsage.CacheWrite
		info.inputTokens = sess.TokenUsage.InputTokens

		result.TotalCacheRead += info.cacheRead
		result.TotalCacheWrite += info.cacheWrite
		result.TotalInputTokens += info.inputTokens

		// Detect cache miss gaps: find messages where the gap to the previous
		// message exceeds the cache TTL.
		var prevTime time.Time
		var sessionGapSum float64
		var sessionGapCount int
		for _, msg := range sess.Messages {
			if msg.Timestamp.IsZero() {
				continue
			}
			if !prevTime.IsZero() {
				gapMins := msg.Timestamp.Sub(prevTime).Minutes()
				sessionGapSum += gapMins
				sessionGapCount++

				if msg.Role == session.RoleAssistant && int(gapMins) > cacheTTLMinutes {
					info.cacheMissCount++
					missGapSum += gapMins
					missGapCount++
					// All input tokens for this message were uncached.
					info.wastedTokens += msg.InputTokens
					if int(gapMins) > info.longestGapMins {
						info.longestGapMins = int(gapMins)
					}
				}
			}
			prevTime = msg.Timestamp
		}
		totalGapSum += sessionGapSum
		totalGapCount += sessionGapCount

		if info.inputTokens > 0 || info.cacheMissCount > 0 {
			sessionInfos = append(sessionInfos, info)
		}
		if info.cacheMissCount > 0 {
			result.SessionsWithMiss++
		}
		result.TotalCacheMisses += info.cacheMissCount
		result.TotalSessions++
	}

	// Compute overall cache hit rate.
	if result.TotalInputTokens > 0 {
		result.CacheHitRate = float64(result.TotalCacheRead) / float64(result.TotalInputTokens) * 100
	}

	// Average gaps.
	if totalGapCount > 0 {
		result.AvgGapMinutes = totalGapSum / float64(totalGapCount)
	}
	if missGapCount > 0 {
		result.AvgMissGapMinutes = missGapSum / float64(missGapCount)
	}

	// Estimate savings from cache hits.
	result.EstimatedSavings = float64(result.TotalCacheRead) * savingsPerMToken / 1_000_000

	// Sort sessions by wasted cost to find worst offenders.
	sort.Slice(sessionInfos, func(i, j int) bool {
		return sessionInfos[i].wastedTokens > sessionInfos[j].wastedTokens
	})

	// Top 10 worst sessions.
	limit := 10
	if len(sessionInfos) < limit {
		limit = len(sessionInfos)
	}
	totalWaste := 0.0
	for _, info := range sessionInfos {
		wastedCost := float64(info.wastedTokens) * savingsPerMToken / 1_000_000
		totalWaste += wastedCost
	}
	result.EstimatedWaste = totalWaste

	for _, info := range sessionInfos[:limit] {
		if info.cacheMissCount == 0 {
			continue
		}
		hitRate := 0.0
		if info.inputTokens > 0 {
			hitRate = float64(info.cacheRead) / float64(info.inputTokens) * 100
		}
		wastedCost := float64(info.wastedTokens) * savingsPerMToken / 1_000_000
		result.WorstSessions = append(result.WorstSessions, session.CacheMissSession{
			ID:             info.id,
			Summary:        info.summary,
			CacheHitRate:   hitRate,
			CacheMissCount: info.cacheMissCount,
			WastedTokens:   info.wastedTokens,
			WastedCost:     wastedCost,
			LongestGapMins: info.longestGapMins,
		})
	}

	return result, nil
}
