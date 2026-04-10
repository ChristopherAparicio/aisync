package session

import "time"

// AnalyticsSchemaVersion is the current version of the session_analytics row
// format. Bump this whenever the ComputeAnalytics formula changes in a way
// that would produce different numbers for the same input. The scheduler
// backfill task uses this to force-rebuild outdated rows.
const AnalyticsSchemaVersion = 1

// Analytics holds the pre-computed per-session metrics that feed the four
// catastrophic hot paths (Forecast, CacheEfficiency, ContextSaturation,
// AgentROI). It is a materialized read model: one row per session, written in
// the same transaction as Save() via the service-layer stampAnalytics() hook,
// never mutated afterward (sessions are append-only; a replay creates a new
// session ID).
//
// The fields below are deliberately flat primitives so the row can be loaded
// and aggregated with plain SQL SUM/AVG/MAX, without any JSON decode or Go
// post-processing. The previous architecture paid ~12-14 seconds per dashboard
// hit to recompute these values from full session payloads; this struct
// eliminates that cost by computing them once at write time.
//
// Per-agent rollups live in a sibling AgentUsage list because the cardinality
// of agents-per-session is small (~1-10) but unbounded — exploding them into
// columns would be wrong.
type Analytics struct {
	SessionID ID `json:"session_id"`

	// ── ContextSaturation contributions ────────────────────────────
	// These feed ContextSaturation's global aggregates: SessionsAbove80,
	// SessionsAbove90, SessionsCompacted, AvgPeakSaturation, per-model breakdown.
	PeakInputTokens        int     `json:"peak_input_tokens"`
	DominantModel          string  `json:"dominant_model"`
	MaxContextWindow       int     `json:"max_context_window"`  // from pricing catalog lookup
	PeakSaturationPct      float64 `json:"peak_saturation_pct"` // 0-100
	HasCompaction          bool    `json:"has_compaction"`
	CompactionCount        int     `json:"compaction_count"`
	CompactionDropPct      float64 `json:"compaction_drop_pct"`      // average drop across events in this session
	CompactionWastedTokens int     `json:"compaction_wasted_tokens"` // tokens lost to compaction

	// ── CacheEfficiency contributions ──────────────────────────────
	// These feed CacheEfficiency's TotalInputTokens / TotalCacheRead /
	// CacheHitRate / EstimatedWaste / etc.
	CacheReadTokens   int     `json:"cache_read_tokens"`
	CacheWriteTokens  int     `json:"cache_write_tokens"`
	InputTokens       int     `json:"input_tokens"`
	CacheMissCount    int     `json:"cache_miss_count"`
	CacheWastedTokens int     `json:"cache_wasted_tokens"`
	LongestGapMins    int     `json:"longest_gap_mins"`
	SessionAvgGapMins float64 `json:"session_avg_gap_mins"` // avg gap between consecutive messages in this session

	// ── Forecast / cost breakdown contributions ────────────────────
	// Backend is one of "claude", "openai", etc. — used by Forecast's per-backend
	// aggregation and the treemap visualization. EstimatedCost and ActualCost
	// are duplicated from sessions.estimated_cost/actual_cost for a join-free
	// read path. ForkOffset + DeduplicatedCost support fork-dedup subtraction.
	Backend          string  `json:"backend"`
	EstimatedCost    float64 `json:"estimated_cost"`
	ActualCost       float64 `json:"actual_cost"`
	ForkOffset       int     `json:"fork_offset"`       // 0 if not a fork, otherwise message index at which the fork diverged
	DeduplicatedCost float64 `json:"deduplicated_cost"` // cost of messages AFTER ForkOffset (what "this session" actually added)

	// ── Per-session agent rollups (feeds AgentROI) ─────────────────
	TotalAgentInvocations int     `json:"total_agent_invocations"`
	UniqueAgentsUsed      int     `json:"unique_agents_used"`
	AgentTokens           int     `json:"agent_tokens"`
	AgentCost             float64 `json:"agent_cost"`
	TotalWastedTokens     int     `json:"total_wasted_tokens"`

	// AgentUsage is the per-agent breakdown for this session. Persisted in a
	// sibling session_agent_usage table, but carried on the Analytics value
	// for round-trip convenience.
	AgentUsage []AgentUsage `json:"agent_usage,omitempty"`

	// ── Rich per-session analyses (JSON blobs in storage) ──────────
	// These pointers hold the outputs of the domain helpers that walk the
	// message array. Each one is ~1-2KB when serialized. They are stored as
	// JSON text columns on session_analytics and deserialized on read by the
	// hot-path handlers, which then feed them back into the existing
	// Aggregate* helpers (AggregateWaste, AggregateFreshness, etc.) verbatim.
	//
	// Why pointers: zero-value = "not computed" (used by older schema
	// versions or transient in-memory structs before ComputeAnalytics runs).
	// Non-nil pointer = authoritative result for this session.
	WasteBreakdown *TokenWasteBreakdown  `json:"waste_breakdown,omitempty"`
	Freshness      *SessionFreshness     `json:"freshness,omitempty"`
	Overload       *OverloadAnalysis     `json:"overload,omitempty"`
	PromptData     *SessionPromptData    `json:"prompt_data,omitempty"`
	FitnessData    *SessionFitnessData   `json:"fitness_data,omitempty"`
	ForecastInput  *SessionForecastInput `json:"forecast_input,omitempty"`

	// ── Housekeeping ───────────────────────────────────────────────
	SchemaVersion int       `json:"schema_version"`
	ComputedAt    time.Time `json:"computed_at"`
}

// AgentUsage is the per-agent contribution within a single session.
type AgentUsage struct {
	AgentName   string  `json:"agent_name"`
	Invocations int     `json:"invocations"`
	Tokens      int     `json:"tokens"`
	Cost        float64 `json:"cost"`
	Errors      int     `json:"errors"`
}

// AnalyticsFilter narrows the Analytics rows returned by QueryAnalytics.
// All fields are optional; zero values mean "no filter on this dimension".
// This is the read-path equivalent of ListOptions but scoped to the
// materialized session_analytics table.
type AnalyticsFilter struct {
	ProjectPath string
	Since       time.Time
	Until       time.Time
	Backend     string
	// MinSchemaVersion filters out rows older than the current formula.
	// When zero, all rows are returned regardless of schema version (useful
	// for the backfill task which wants to find the oldest).
	MinSchemaVersion int
}

// AnalyticsRow is the hydrated result of a QueryAnalytics call. It pairs
// the per-session Analytics with the session-level metadata the handlers
// need for bucketing (project path, creation time) without a second query.
// The extra fields (Summary, Agent, MessageCount, etc.) are cheap to JOIN
// from the sessions table and avoid a second round-trip for WorstSessions
// display and AgentROI grouping.
type AnalyticsRow struct {
	Analytics
	ProjectPath   string        `json:"project_path"`
	CreatedAt     time.Time     `json:"created_at"`
	Branch        string        `json:"branch"`
	Summary       string        `json:"summary"`
	Agent         string        `json:"agent"`
	MessageCount  int           `json:"message_count"`
	TotalTokens   int           `json:"total_tokens"`
	ToolCallCount int           `json:"tool_call_count"`
	ErrorCount    int           `json:"error_count"`
	SessionType   string        `json:"session_type"`
	Status        SessionStatus `json:"status"`
}
