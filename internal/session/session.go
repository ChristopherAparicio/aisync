// Package session contains the shared types for aisync.
// This package has zero business logic — it is a shared vocabulary that every
// other package imports. Think of it as Vacuum's model/ or gh CLI's shared types.
//
// No interfaces live here. Interfaces are defined by the packages that own the
// abstraction (provider.Provider, storage.Store).
package session

import "time"

// Session represents a captured AI coding session.
type Session struct {
	ExportedAt      time.Time      `json:"exported_at"`
	CreatedAt       time.Time      `json:"created_at"`
	ProjectPath     string         `json:"project_path"`
	RemoteURL       string         `json:"remote_url,omitempty"` // git remote origin URL (e.g. "github.com/org/repo")
	ExportedBy      string         `json:"exported_by,omitempty"`
	ParentID        ID             `json:"parent_id,omitempty"`
	OwnerID         ID             `json:"owner_id,omitempty"`
	StorageMode     StorageMode    `json:"storage_mode"`
	Summary         string         `json:"summary,omitempty"`
	ID              ID             `json:"id"`
	Provider        ProviderName   `json:"provider"`
	Agent           string         `json:"agent"`
	Branch          string         `json:"branch,omitempty"`
	CommitSHA       string         `json:"commit_sha,omitempty"`
	Messages        []Message      `json:"messages,omitempty"`
	Children        []Session      `json:"children,omitempty"`
	Links           []Link         `json:"links,omitempty"`
	FileChanges     []FileChange   `json:"file_changes,omitempty"`
	TokenUsage      TokenUsage     `json:"token_usage"`
	SessionType     string         `json:"session_type,omitempty"`      // classification tag: feature, bug, refactor, etc.
	ProjectCategory string         `json:"project_category,omitempty"`  // project-level category: backend, frontend, ops, etc.
	ForkedAtMessage int            `json:"forked_at_message,omitempty"` // 1-based message index where this session was forked (via rewind)
	Status          SessionStatus  `json:"status,omitempty"`            // lifecycle status: active, idle, archived
	Errors          []SessionError `json:"errors,omitempty"`            // structured errors extracted from the session
	Version         int            `json:"version"`
	SourceUpdatedAt int64          `json:"-"` // source provider's last-updated timestamp (epoch ms); not serialized
}

// Summary is a lightweight representation of a session for listings.
type Summary struct {
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at,omitempty"` // last update from source provider
	ID              ID            `json:"id"`
	ParentID        ID            `json:"parent_id,omitempty"`
	OwnerID         ID            `json:"owner_id,omitempty"`
	Provider        ProviderName  `json:"provider"`
	Agent           string        `json:"agent"`
	Branch          string        `json:"branch,omitempty"`
	ProjectPath     string        `json:"project_path,omitempty"`
	RemoteURL       string        `json:"remote_url,omitempty"` // git remote origin URL (e.g. "github.com/org/repo")
	Summary         string        `json:"summary,omitempty"`
	SessionType     string        `json:"session_type,omitempty"`     // classification tag
	ProjectCategory string        `json:"project_category,omitempty"` // project-level category
	Status          SessionStatus `json:"status,omitempty"`           // lifecycle: active, idle, archived
	MessageCount    int           `json:"message_count"`
	TotalTokens     int           `json:"total_tokens"`
	ToolCallCount   int           `json:"tool_call_count"` // total tool invocations
	ErrorCount      int           `json:"error_count"`     // tool calls with state=error
}

// Message represents a single message in an AI conversation.
type Message struct {
	Timestamp        time.Time      `json:"timestamp"`
	ID               string         `json:"id"`
	Content          string         `json:"content"`
	Model            string         `json:"model,omitempty"`
	ProviderID       string         `json:"provider_id,omitempty"` // e.g. "anthropic", "amazon-bedrock", "opencode"
	Thinking         string         `json:"thinking,omitempty"`
	Role             MessageRole    `json:"role"`
	ToolCalls        []ToolCall     `json:"tool_calls,omitempty"`
	Images           []ImageMeta    `json:"images,omitempty"`         // images included in this message
	ContentBlocks    []ContentBlock `json:"content_blocks,omitempty"` // structured content blocks (text, image, etc.)
	InputTokens      int            `json:"input_tokens,omitempty"`
	OutputTokens     int            `json:"output_tokens,omitempty"`
	CacheReadTokens  int            `json:"cache_read_tokens,omitempty"`  // tokens read from prompt cache (cheaper)
	CacheWriteTokens int            `json:"cache_write_tokens,omitempty"` // tokens written to prompt cache (more expensive)
	ProviderCost     float64        `json:"provider_cost,omitempty"`      // actual cost reported by provider (0 = unknown/subscription)
}

// ContentBlock represents a structured content block within a message.
// This preserves the rich structure from provider APIs (Claude content blocks,
// OpenCode parts) instead of flattening everything to plain text.
type ContentBlock struct {
	Type     ContentBlockType `json:"type"`
	Text     string           `json:"text,omitempty"`     // for "text" type
	Image    *ImageMeta       `json:"image,omitempty"`    // for "image" type
	ToolUse  *ToolCallRef     `json:"tool_use,omitempty"` // for "tool_use" type
	Thinking string           `json:"thinking,omitempty"` // for "thinking" type
}

// ContentBlockType identifies the type of content block.
type ContentBlockType string

const (
	ContentBlockText     ContentBlockType = "text"
	ContentBlockImage    ContentBlockType = "image"
	ContentBlockToolUse  ContentBlockType = "tool_use"
	ContentBlockThinking ContentBlockType = "thinking"
)

// ImageMeta stores metadata about an image included in a message.
// We store metadata only (not the actual image data) to keep session size manageable.
type ImageMeta struct {
	MediaType      string `json:"media_type"`                // e.g. "image/png", "image/jpeg"
	Width          int    `json:"width,omitempty"`           // image width in pixels (if known)
	Height         int    `json:"height,omitempty"`          // image height in pixels (if known)
	SizeBytes      int    `json:"size_bytes,omitempty"`      // original size in bytes (from base64 length)
	TokensEstimate int    `json:"tokens_estimate,omitempty"` // estimated tokens for this image
	Source         string `json:"source,omitempty"`          // "base64", "url", "file"
	FileName       string `json:"file_name,omitempty"`       // original filename if available
}

// ToolCallRef is a lightweight reference to a tool call within a content block.
type ToolCallRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ToolCall represents a tool invocation with its lifecycle.
type ToolCall struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Input        string    `json:"input"`
	Output       string    `json:"output,omitempty"`
	State        ToolState `json:"state"`
	DurationMs   int       `json:"duration_ms,omitempty"`
	InputTokens  int       `json:"input_tokens,omitempty"`  // estimated tokens consumed by this tool's input
	OutputTokens int       `json:"output_tokens,omitempty"` // estimated tokens consumed by this tool's output
}

// TokenUsageBucket aggregates token consumption for a specific time window.
// Buckets are pre-computed (nightly) and stored for fast dashboard queries.
type TokenUsageBucket struct {
	BucketStart      time.Time    `json:"bucket_start"`
	BucketEnd        time.Time    `json:"bucket_end"`
	Granularity      string       `json:"granularity"` // "1h" or "1d"
	ProjectPath      string       `json:"project_path,omitempty"`
	Provider         ProviderName `json:"provider,omitempty"`
	LLMBackend       string       `json:"llm_backend,omitempty"` // LLM backend identifier (e.g. "anthropic", "amazon-bedrock")
	InputTokens      int          `json:"input_tokens"`
	OutputTokens     int          `json:"output_tokens"`
	ImageTokens      int          `json:"image_tokens"`
	CacheReadTokens  int          `json:"cache_read_tokens"`  // tokens read from prompt cache
	CacheWriteTokens int          `json:"cache_write_tokens"` // tokens written to prompt cache
	SessionCount     int          `json:"session_count"`
	MessageCount     int          `json:"message_count"`
	ToolCallCount    int          `json:"tool_call_count"`
	ToolErrorCount   int          `json:"tool_error_count"`
	ImageCount       int          `json:"image_count"`
	UserMsgCount     int          `json:"user_msg_count"`   // messages from user (human interaction indicator)
	AssistMsgCount   int          `json:"assist_msg_count"` // messages from assistant
	EstimatedCost    float64      `json:"estimated_cost"`   // API-equivalent cost (computed from token rates)
	ActualCost       float64      `json:"actual_cost"`      // actual cost reported by provider (0 for subscription)
}

// ToolUsageBucket aggregates per-tool usage for a time window.
// Buckets are keyed by (bucket_start, granularity, project_path, tool_name, tool_category).
type ToolUsageBucket struct {
	BucketStart   time.Time `json:"bucket_start"`
	BucketEnd     time.Time `json:"bucket_end"`
	Granularity   string    `json:"granularity"` // "1h" or "1d"
	ProjectPath   string    `json:"project_path,omitempty"`
	ToolName      string    `json:"tool_name"`                // e.g. "bash", "Read", "notionApi_API-post-search"
	ToolCategory  string    `json:"tool_category"`            // "builtin", "mcp:notion", "mcp:sentry", "mcp:langfuse", ...
	CallCount     int       `json:"call_count"`               // number of tool invocations
	InputTokens   int       `json:"input_tokens"`             // estimated tokens consumed by tool inputs
	OutputTokens  int       `json:"output_tokens"`            // estimated tokens consumed by tool outputs
	ErrorCount    int       `json:"error_count"`              // tool calls that ended in error state
	TotalDuration int       `json:"total_duration_ms"`        // cumulative execution time in ms
	EstimatedCost float64   `json:"estimated_cost,omitempty"` // cost attributed to this tool's token usage
}

// ToolCostSummary aggregates tool costs across sessions for a project/time range.
type ToolCostSummary struct {
	// Per-tool breakdown (sorted by cost descending)
	Tools []ToolCostEntry `json:"tools"`

	// Per-MCP-server aggregation (e.g. "notion" → sum of all notion tools)
	MCPServers []MCPServerCost `json:"mcp_servers,omitempty"`

	// Per-agent aggregation
	Agents []AgentCostEntry `json:"agents,omitempty"`

	// Totals
	TotalCalls    int     `json:"total_calls"`
	TotalTokens   int     `json:"total_tokens"`
	TotalCost     float64 `json:"total_cost"`
	TotalMCPCalls int     `json:"total_mcp_calls"`
	TotalMCPCost  float64 `json:"total_mcp_cost"`
}

// ToolCostEntry is a single tool's aggregated cost data.
type ToolCostEntry struct {
	Name           string  `json:"name"`
	Category       string  `json:"category"` // "builtin" or "mcp:server"
	CallCount      int     `json:"call_count"`
	InputTokens    int     `json:"input_tokens"`
	OutputTokens   int     `json:"output_tokens"`
	TotalTokens    int     `json:"total_tokens"`
	ErrorCount     int     `json:"error_count"`
	AvgDuration    int     `json:"avg_duration_ms"`
	TotalDuration  int     `json:"total_duration_ms"`
	EstimatedCost  float64 `json:"estimated_cost"`
	AvgCostPerCall float64 `json:"avg_cost_per_call"` // estimated_cost / call_count
}

// MCPServerCost aggregates all tools from a single MCP server.
type MCPServerCost struct {
	Server         string  `json:"server"`     // e.g. "notion", "sentry", "langfuse"
	ToolCount      int     `json:"tool_count"` // distinct tools from this server
	CallCount      int     `json:"call_count"`
	TotalTokens    int     `json:"total_tokens"`
	ErrorCount     int     `json:"error_count"`
	EstimatedCost  float64 `json:"estimated_cost"`
	AvgCostPerCall float64 `json:"avg_cost_per_call"`
}

// AgentCostEntry aggregates costs for a single agent type.
type AgentCostEntry struct {
	Agent             string  `json:"agent"` // e.g. "coder", "explore", "general", "build"
	SessionCount      int     `json:"session_count"`
	MessageCount      int     `json:"message_count"`
	TotalTokens       int     `json:"total_tokens"`
	ToolCallCount     int     `json:"tool_call_count"`
	EstimatedCost     float64 `json:"estimated_cost"`
	AvgCostPerSession float64 `json:"avg_cost_per_session"`
}

// MCPProjectMatrix is a cross-project view of MCP server usage.
// It shows which MCP servers are used by which projects with cost data.
type MCPProjectMatrix struct {
	// All distinct MCP server names found (columns).
	Servers []string `json:"servers"`

	// Per-project rows, each with per-server cost data.
	Projects []MCPProjectRow `json:"projects"`

	// Per-server totals across all projects.
	ServerTotals map[string]MCPProjectCell `json:"server_totals"`

	// Grand total.
	TotalCost  float64 `json:"total_cost"`
	TotalCalls int     `json:"total_calls"`
}

// MCPProjectRow represents one project's MCP usage across all servers.
type MCPProjectRow struct {
	ProjectPath string                    `json:"project_path"`
	DisplayName string                    `json:"display_name"` // basename of project path
	Cells       map[string]MCPProjectCell `json:"cells"`        // server name → cell
	TotalCost   float64                   `json:"total_cost"`   // sum across all servers
	TotalCalls  int                       `json:"total_calls"`
}

// MCPProjectCell is a single (project × MCP server) intersection.
type MCPProjectCell struct {
	CallCount     int     `json:"call_count"`
	TotalTokens   int     `json:"total_tokens"`
	ErrorCount    int     `json:"error_count"`
	EstimatedCost float64 `json:"estimated_cost"`
}

// CacheEfficiency summarizes prompt cache usage and waste.
type CacheEfficiency struct {
	// Aggregate stats
	TotalInputTokens int     `json:"total_input_tokens"`
	TotalCacheRead   int     `json:"total_cache_read"`
	TotalCacheWrite  int     `json:"total_cache_write"`
	CacheHitRate     float64 `json:"cache_hit_rate"`    // 0-100, cache_read / input_tokens
	EstimatedSavings float64 `json:"estimated_savings"` // $ saved by cache hits vs full price
	EstimatedWaste   float64 `json:"estimated_waste"`   // $ wasted on cache misses after gaps

	// Activity stats
	TotalSessions     int     `json:"total_sessions"`
	SessionsWithMiss  int     `json:"sessions_with_miss"`   // sessions that had at least 1 cache miss
	TotalCacheMisses  int     `json:"total_cache_misses"`   // total messages after gaps > TTL
	AvgGapMinutes     float64 `json:"avg_gap_minutes"`      // average gap between messages (all sessions)
	AvgMissGapMinutes float64 `json:"avg_miss_gap_minutes"` // average gap for cache-miss messages only

	// Sessions with worst cache efficiency
	WorstSessions []CacheMissSession `json:"worst_sessions,omitempty"`
}

// CacheMissSession identifies a session with significant cache misses.
type CacheMissSession struct {
	ID             ID      `json:"id"`
	Summary        string  `json:"summary"`
	CacheHitRate   float64 `json:"cache_hit_rate"`   // 0-100
	CacheMissCount int     `json:"cache_miss_count"` // number of messages after gaps > 5min
	WastedTokens   int     `json:"wasted_tokens"`    // tokens that could have been cached
	WastedCost     float64 `json:"wasted_cost"`      // $ cost of those wasted tokens
	LongestGapMins int     `json:"longest_gap_mins"` // longest gap between messages in minutes
}

// BudgetStatus represents the current spending status against a budget limit.
type BudgetStatus struct {
	ProjectName string `json:"project_name"`
	ProjectPath string `json:"project_path,omitempty"`
	RemoteURL   string `json:"remote_url,omitempty"`

	// Monthly budget
	MonthlyLimit   float64 `json:"monthly_limit"`           // configured limit ($)
	MonthlySpent   float64 `json:"monthly_spent"`           // estimated cost this month ($)
	MonthlyPercent float64 `json:"monthly_percent"`         // 0-100
	MonthlyAlert   string  `json:"monthly_alert,omitempty"` // "", "warning", "exceeded"

	// Daily budget
	DailyLimit   float64 `json:"daily_limit,omitempty"`
	DailySpent   float64 `json:"daily_spent"`
	DailyPercent float64 `json:"daily_percent"`
	DailyAlert   string  `json:"daily_alert,omitempty"` // "", "warning", "exceeded"

	// Metadata
	AlertThreshold float64 `json:"alert_threshold"` // % at which to warn (e.g. 80)
	SessionCount   int     `json:"session_count"`   // sessions this month
	DaysRemaining  int     `json:"days_remaining"`  // days left in the month
	ProjectedMonth float64 `json:"projected_month"` // projected spend by end of month at current rate
}

// ContextSaturation reports how close sessions get to their model's context window limit.
// High saturation correlates with compaction, degraded quality, and wasted tokens.
type ContextSaturation struct {
	// Global stats
	TotalSessions     int     `json:"total_sessions"`      // sessions analyzed
	SessionsAbove80   int     `json:"sessions_above_80"`   // sessions that reached >80% of context window
	SessionsAbove90   int     `json:"sessions_above_90"`   // sessions that reached >90% (compaction imminent)
	SessionsCompacted int     `json:"sessions_compacted"`  // sessions with detected compaction events
	AvgPeakSaturation float64 `json:"avg_peak_saturation"` // average peak saturation across all sessions (0-100)

	// Per-model breakdown
	Models []ModelSaturation `json:"models,omitempty"`

	// Sessions with highest saturation (top 10 worst offenders)
	WorstSessions []SessionSaturation `json:"worst_sessions,omitempty"`
}

// ModelSaturation aggregates saturation stats for a single model.
type ModelSaturation struct {
	Model          string  `json:"model"`
	MaxInputTokens int     `json:"max_input_tokens"` // context window size
	SessionCount   int     `json:"session_count"`    // sessions using this model
	AvgPeakPct     float64 `json:"avg_peak_pct"`     // average peak saturation (0-100)
	MaxPeakPct     float64 `json:"max_peak_pct"`     // highest peak saturation seen
	CompactedCount int     `json:"compacted_count"`  // sessions that hit compaction
	Above80Count   int     `json:"above_80_count"`   // sessions >80% saturation

	// Context efficiency analysis (3.1)
	EfficiencyVerdict string  `json:"efficiency_verdict"`  // "oversized", "well-sized", "tight", "saturated"
	AvgPeakTokens     int     `json:"avg_peak_tokens"`     // average peak tokens across sessions
	WastedCapacityPct float64 `json:"wasted_capacity_pct"` // 100 - AvgPeakPct (unused context window %)
}

// SessionSaturation captures the peak context usage for a single session.
type SessionSaturation struct {
	ID              ID      `json:"id"`
	Summary         string  `json:"summary"`
	Model           string  `json:"model"`
	MaxInputTokens  int     `json:"max_input_tokens"`  // model's context window
	PeakInputTokens int     `json:"peak_input_tokens"` // highest InputTokens seen
	PeakSaturation  float64 `json:"peak_saturation"`   // PeakInputTokens / MaxInputTokens * 100
	MessageCount    int     `json:"message_count"`
	WasCompacted    bool    `json:"was_compacted"` // true if compaction was detected
}

// SaturationCurve provides per-message context usage data for a single session.
// Used to visualize how context fills up message by message.
type SaturationCurve struct {
	SessionID      ID                `json:"session_id"`
	Model          string            `json:"model"`
	MaxInputTokens int               `json:"max_input_tokens"` // context window size
	InitOverhead   int               `json:"init_overhead"`    // tokens in first message (system prompt, skills, etc.)
	PeakTokens     int               `json:"peak_tokens"`      // highest cumulative input tokens
	PeakPercent    float64           `json:"peak_percent"`     // peak as % of context window
	WasCompacted   bool              `json:"was_compacted"`
	MsgAtDegraded  int               `json:"msg_at_degraded"` // message # when hitting degraded zone (0 = never)
	MsgAtCritical  int               `json:"msg_at_critical"` // message # when hitting critical zone (0 = never)
	Points         []SaturationPoint `json:"points"`
}

// SaturationPoint is a single message's contribution to context saturation.
type SaturationPoint struct {
	MessageIndex int     `json:"msg_index"`
	Role         string  `json:"role"`         // "user", "assistant", "system"
	InputTokens  int     `json:"input_tokens"` // this message's input tokens (cumulative from API)
	Percent      float64 `json:"percent"`      // input_tokens / max_input_tokens * 100
	Zone         string  `json:"zone"`         // "optimal", "degraded", "critical"
	Delta        int     `json:"delta"`        // change from previous message
	Label        string  `json:"label"`        // short description (e.g. "User msg", "Bash output +8K")
}

// FileChange records a file touched during a session.
type FileChange struct {
	FilePath   string     `json:"file_path"`
	ChangeType ChangeType `json:"change_type"`
}

// AgentROI measures the effectiveness of an AI agent across sessions.
type AgentROI struct {
	// Per-agent entries sorted by ROI score descending.
	Agents []AgentROIEntry `json:"agents"`
}

// AgentROIEntry holds ROI metrics for a single agent.
type AgentROIEntry struct {
	Agent         string  `json:"agent"`
	SessionCount  int     `json:"session_count"`
	MessageCount  int     `json:"message_count"`
	TotalTokens   int     `json:"total_tokens"`
	ToolCallCount int     `json:"tool_call_count"`
	ErrorCount    int     `json:"error_count"`
	EstimatedCost float64 `json:"estimated_cost"`

	// Derived metrics
	AvgCostPerSession float64 `json:"avg_cost_per_session"`
	AvgMessages       float64 `json:"avg_messages"`        // messages per session
	ErrorRate         float64 `json:"error_rate"`          // 0-100, errors / tool_calls
	CompletionRate    float64 `json:"completion_rate"`     // 0-100, completed / total sessions
	CompletedCount    int     `json:"completed_count"`     // sessions with [DONE] or [COMMIT]
	AvgPeakSaturation float64 `json:"avg_peak_saturation"` // 0-100, context usage
	TokensPerMessage  int     `json:"tokens_per_message"`  // avg tokens per message

	// Composite score (0-100, higher = better ROI)
	ROIScore int    `json:"roi_score"`
	ROIGrade string `json:"roi_grade"` // "A", "B", "C", "D", "F"
}

// SkillROI measures the effectiveness of skills loaded in sessions.
type SkillROI struct {
	Skills []SkillROIEntry `json:"skills"`
}

// SkillROIEntry holds ROI metrics for a single skill.
type SkillROIEntry struct {
	Name           string  `json:"name"`
	LoadCount      int     `json:"load_count"`       // times this skill was loaded
	SessionCount   int     `json:"session_count"`    // distinct sessions where loaded
	TotalSessions  int     `json:"total_sessions"`   // total sessions in scope (for usage %)
	UsagePercent   float64 `json:"usage_percent"`    // load_count / total_sessions * 100
	ContextTokens  int     `json:"context_tokens"`   // estimated tokens added to context per load
	TotalTokenCost int     `json:"total_token_cost"` // total tokens consumed by this skill across all loads

	// Effectiveness signals
	IsGhost          bool    `json:"is_ghost"`           // loaded but appears to have no impact
	ErrorRateWith    float64 `json:"error_rate_with"`    // error rate when skill is loaded
	ErrorRateWithout float64 `json:"error_rate_without"` // error rate when skill is NOT loaded
	ErrorDelta       float64 `json:"error_delta"`        // positive = skill increases errors

	Verdict string `json:"verdict"` // "valuable", "neutral", "ghost", "harmful"
}

// Recommendation is an auto-generated actionable insight.
type Recommendation struct {
	Type     string `json:"type"`              // "agent_error", "agent_cost", "skill_ghost", "cache_miss", "context_saturation", "budget"
	Priority string `json:"priority"`          // "high", "medium", "low"
	Icon     string `json:"icon"`              // emoji for display
	Title    string `json:"title"`             // short headline
	Message  string `json:"message"`           // detailed explanation
	Impact   string `json:"impact"`            // estimated impact (e.g. "$120/mo savings")
	Project  string `json:"project,omitempty"` // which project (empty = global)
	Agent    string `json:"agent,omitempty"`   // which agent (if applicable)
	Skill    string `json:"skill,omitempty"`   // which skill (if applicable)
}

// HealthScore is a composite quality score for a single session (0-100).
type HealthScore struct {
	Total           int    `json:"total"`            // composite score 0-100
	Grade           string `json:"grade"`            // "A", "B", "C", "D", "F"
	ErrorScore      int    `json:"error_score"`      // 0-30: fewer errors = higher
	SaturationScore int    `json:"saturation_score"` // 0-25: lower context usage = higher
	CacheScore      int    `json:"cache_score"`      // 0-20: higher cache hit = higher
	CompletionScore int    `json:"completion_score"` // 0-15: completed session = higher
	EfficiencyScore int    `json:"efficiency_score"` // 0-10: fewer tokens per message = higher
}

// ComputeHealthScore calculates the health score for a session summary.
// Lightweight: uses only Summary fields, no full session load needed.
func ComputeHealthScore(sm Summary) HealthScore {
	var hs HealthScore

	// 1. Error score (0-30): 0 errors = 30, >10% error rate = 0.
	if sm.ToolCallCount > 0 {
		errorRate := float64(sm.ErrorCount) / float64(sm.ToolCallCount) * 100
		score := 30.0 - errorRate*3
		if score < 0 {
			score = 0
		}
		hs.ErrorScore = int(score)
	} else if sm.ErrorCount == 0 {
		hs.ErrorScore = 30
	}

	// 2. Saturation score (0-25): proxy from total tokens (no context window in Summary).
	tokenK := sm.TotalTokens / 1000
	switch {
	case tokenK < 50:
		hs.SaturationScore = 25
	case tokenK < 100:
		hs.SaturationScore = 20
	case tokenK < 200:
		hs.SaturationScore = 15
	case tokenK < 500:
		hs.SaturationScore = 8
	default:
		hs.SaturationScore = 0
	}

	// 3. Cache score (0-20): default 15 (refined with full session data).
	hs.CacheScore = 15

	// 4. Completion score (0-15).
	switch sm.Status {
	case "completed":
		hs.CompletionScore = 15
	case "review":
		hs.CompletionScore = 12
	case "active":
		hs.CompletionScore = 8
	default:
		hs.CompletionScore = 5
	}

	// 5. Efficiency score (0-10): tokens per message.
	if sm.MessageCount > 0 {
		tokPerMsg := sm.TotalTokens / sm.MessageCount
		switch {
		case tokPerMsg < 5000:
			hs.EfficiencyScore = 10
		case tokPerMsg < 15000:
			hs.EfficiencyScore = 7
		case tokPerMsg < 30000:
			hs.EfficiencyScore = 4
		default:
			hs.EfficiencyScore = 1
		}
	}

	hs.Total = hs.ErrorScore + hs.SaturationScore + hs.CacheScore + hs.CompletionScore + hs.EfficiencyScore
	if hs.Total > 100 {
		hs.Total = 100
	}

	switch {
	case hs.Total >= 85:
		hs.Grade = "A"
	case hs.Total >= 70:
		hs.Grade = "B"
	case hs.Total >= 55:
		hs.Grade = "C"
	case hs.Total >= 40:
		hs.Grade = "D"
	default:
		hs.Grade = "F"
	}

	return hs
}

// Link connects a session to a git object (branch, commit, PR).
type Link struct {
	LinkType LinkType `json:"link_type"`
	Ref      string   `json:"ref"`
}

// SessionLink connects two sessions together (e.g. delegation, continuation).
type SessionLink struct {
	CreatedAt       time.Time       `json:"created_at"`
	ID              ID              `json:"id"`
	SourceSessionID ID              `json:"source_session_id"` // the session that initiated the link
	TargetSessionID ID              `json:"target_session_id"` // the session being linked to
	LinkType        SessionLinkType `json:"link_type"`         // e.g. "delegated_to", "related"
	Description     string          `json:"description,omitempty"`
}

// TokenUsage tracks token consumption for a session.
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
	ImageTokens  int `json:"image_tokens,omitempty"` // tokens consumed by images (subset of InputTokens)
	CacheRead    int `json:"cache_read,omitempty"`   // cache read tokens (subset of InputTokens)
	CacheWrite   int `json:"cache_write,omitempty"`  // cache write/creation tokens (subset of InputTokens)
}

// ImageStats aggregates image usage across a session.
type ImageStats struct {
	Count       int `json:"count"`                  // total images in the session
	TotalBytes  int `json:"total_bytes,omitempty"`  // total size of all images
	TotalTokens int `json:"total_tokens,omitempty"` // estimated tokens for all images
}

// CommandStats tracks shell/bash command usage in a session.
type CommandStats struct {
	TotalCommands int            `json:"total_commands"`           // total bash/shell tool calls
	ByCommand     map[string]int `json:"by_command,omitempty"`     // count per base command (e.g. "git": 5, "ls": 3)
	ErrorCommands int            `json:"error_commands,omitempty"` // commands that returned errors
}

// Cost represents a monetary amount in a given currency.
type Cost struct {
	InputCost  float64 `json:"input_cost"`
	OutputCost float64 `json:"output_cost"`
	TotalCost  float64 `json:"total_cost"`
	Currency   string  `json:"currency"` // always "USD"
}

// BillingType indicates how a session was billed.
type BillingType string

const (
	BillingAPI          BillingType = "api"          // pay-per-token (e.g. Bedrock, direct API key)
	BillingSubscription BillingType = "subscription" // flat-rate subscription (e.g. Claude Max, OpenCode)
	BillingMixed        BillingType = "mixed"        // mix of API and subscription within one session
)

// CostBreakdown provides dual cost view: API-equivalent vs actual.
type CostBreakdown struct {
	APICost     Cost        `json:"api_cost"`     // what it would cost at public API token rates
	ActualCost  Cost        `json:"actual_cost"`  // what was actually charged (from provider_cost fields)
	Savings     Cost        `json:"savings"`      // APICost - ActualCost
	BillingType BillingType `json:"billing_type"` // "api", "subscription", or "mixed"
}

// CostEstimate is the full cost breakdown for a session.
type CostEstimate struct {
	TotalCost     Cost          `json:"total_cost"`
	Breakdown     CostBreakdown `json:"breakdown"`
	PerModel      []ModelCost   `json:"per_model"`
	UnknownModels []string      `json:"unknown_models,omitempty"` // models without pricing data
}

// ModelCost groups cost by model within a session.
type ModelCost struct {
	Model        string `json:"model"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	Cost         Cost   `json:"cost"`
	MessageCount int    `json:"message_count"`
}

// ToolUsageStats is the aggregated tool usage breakdown for a session.
type ToolUsageStats struct {
	Tools      []ToolUsageEntry `json:"tools"`
	TotalCalls int              `json:"total_calls"`
	TotalCost  Cost             `json:"total_cost,omitempty"` // populated when pricing is available
	Warning    string           `json:"warning,omitempty"`    // non-empty when data may be incomplete (e.g. compact mode)
}

// ToolUsageEntry aggregates token usage and call count for a single tool name.
type ToolUsageEntry struct {
	Name         string  `json:"name"`
	Calls        int     `json:"calls"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	TotalTokens  int     `json:"total_tokens"`
	AvgDuration  int     `json:"avg_duration_ms,omitempty"` // average duration in ms
	ErrorCount   int     `json:"error_count"`
	Cost         Cost    `json:"cost,omitempty"`
	Percentage   float64 `json:"percentage"` // share of total tokens (0-100)
}

// EfficiencyReport is an LLM-generated analysis of session efficiency.
type EfficiencyReport struct {
	Score       int      `json:"score"`       // 0-100 efficiency score
	Summary     string   `json:"summary"`     // one-paragraph assessment
	Strengths   []string `json:"strengths"`   // what went well
	Issues      []string `json:"issues"`      // inefficiencies found
	Suggestions []string `json:"suggestions"` // actionable improvements
	Patterns    []string `json:"patterns"`    // detected patterns (retry loops, over-reading, etc.)
}

// ProjectGroup represents a project (grouping key for sessions).
// Projects are grouped primarily by git remote URL (e.g. "github.com/org/repo"),
// then by provider-specific project path for non-git projects.
// BackfillCandidate represents a session that needs its remote_url resolved.
// Used by the backfill task to efficiently batch-resolve git remotes.
type BackfillCandidate struct {
	ID          ID     `json:"id"`
	ProjectPath string `json:"project_path"`
}

type ProjectGroup struct {
	RemoteURL    string       `json:"remote_url,omitempty"` // normalized git remote URL (empty if not a git repo)
	ProjectPath  string       `json:"project_path"`         // local filesystem path
	Provider     ProviderName `json:"provider"`             // dominant provider for this project
	Category     string       `json:"category,omitempty"`   // project-level category: backend, frontend, ops, etc.
	SessionCount int          `json:"session_count"`        // total sessions
	TotalTokens  int          `json:"total_tokens"`         // aggregated tokens
	DisplayName  string       `json:"display_name"`         // human-friendly label (e.g. "org/repo" or folder name)
}

// ListOptions controls session listing queries.
type ListOptions struct {
	ProjectPath     string
	RemoteURL       string // filter by normalized git remote URL (e.g. "github.com/org/repo")
	Branch          string
	Provider        ProviderName
	SessionType     string // filter by session type (e.g. "bug", "feature")
	ProjectCategory string // filter by project category (e.g. "backend", "frontend")
	OwnerID         ID     // filter by session owner (empty = no filter)
	Since           time.Time
	Until           time.Time
	All             bool
}

// SearchQuery defines criteria for searching sessions.
// All fields are optional — an empty query returns all sessions (paginated).
// Filters are combined with AND logic.
type SearchQuery struct {
	// Keyword performs a case-insensitive text search across summary and message content.
	// In "contain" mode this uses SQL LIKE; in "fulltext" mode it uses SQLite FTS5.
	Keyword string

	// Filters narrow results by exact match on structured fields.
	ProjectPath     string
	RemoteURL       string // filter by normalized git remote URL
	Branch          string
	Provider        ProviderName
	OwnerID         ID
	SessionType     string        // filter by session type (e.g. "bug", "feature")
	ProjectCategory string        // filter by project category (e.g. "backend", "frontend")
	Status          SessionStatus // filter by lifecycle status ("active", "idle", "archived"); empty = no filter
	HasErrors       *bool         // nil = no filter, true = error_count > 0, false = error_count = 0

	// Time range filters (inclusive). Zero values are ignored.
	Since time.Time
	Until time.Time

	// Pagination
	Limit  int // 0 means use default (50)
	Offset int
}

// SearchResult holds a page of search results with metadata.
type SearchResult struct {
	Sessions     []Summary      `json:"sessions"`
	VoiceResults []VoiceSummary `json:"voice_results,omitempty"` // populated only when voice=true
	TotalCount   int            `json:"total_count"`
	Limit        int            `json:"limit"`
	Offset       int            `json:"offset"`
}

// VoiceSummary is a compact, TTS-optimized representation of a session.
// No markdown, no code blocks, plain text only.
type VoiceSummary struct {
	ID      ID     `json:"id"`
	Summary string `json:"summary"`          // 1-2 sentences max, plain text
	TimeAgo string `json:"time_ago"`         // human-readable: "2 hours ago", "yesterday"
	Agent   string `json:"agent,omitempty"`  // e.g. "jarvis"
	Branch  string `json:"branch,omitempty"` // e.g. "main"
}

// BlameEntry represents one session that touched a file.
type BlameEntry struct {
	CreatedAt  time.Time    `json:"created_at"`
	SessionID  ID           `json:"session_id"`
	OwnerID    ID           `json:"owner_id,omitempty"`
	Provider   ProviderName `json:"provider"`
	Branch     string       `json:"branch"`
	Summary    string       `json:"summary,omitempty"`
	ChangeType ChangeType   `json:"change_type"`
}

// BlameQuery contains parameters for a blame lookup.
type BlameQuery struct {
	FilePath string       // required — relative to project root
	Branch   string       // optional filter
	Provider ProviderName // optional filter
	Limit    int          // 0 = no limit (all sessions); >0 = cap results
}

// SecretMatch represents a single secret detected in content.
type SecretMatch struct {
	// Type is the category of secret (e.g., "AWS_ACCESS_KEY", "GITHUB_TOKEN").
	Type string `json:"type"`

	// Value is the detected secret value.
	Value string `json:"value"`

	// StartPos is the byte offset where the secret starts in the content.
	StartPos int `json:"start_pos"`

	// EndPos is the byte offset where the secret ends in the content.
	EndPos int `json:"end_pos"`
}

// PullRequest represents a PR/MR on a code hosting platform.
type PullRequest struct {
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	URL        string    `json:"url"`
	Title      string    `json:"title"`
	Branch     string    `json:"branch"`
	BaseBranch string    `json:"base_branch"`
	State      string    `json:"state"` // "open", "closed", "merged"
	Author     string    `json:"author"`
	Number     int       `json:"number"`
}

// PRComment represents a comment on a pull request.
type PRComment struct {
	CreatedAt time.Time `json:"created_at"`
	Body      string    `json:"body"`
	Author    string    `json:"author"`
	ID        int64     `json:"id"`
}

// StructuredSummary is an AI-generated structured analysis of a session.
// It breaks down a session into intent, outcome, decisions, friction, and open items
// for richer understanding than a one-line summary.
type StructuredSummary struct {
	Intent    string   `json:"intent"`     // what the user was trying to do
	Outcome   string   `json:"outcome"`    // what was achieved
	Decisions []string `json:"decisions"`  // key technical decisions made
	Friction  []string `json:"friction"`   // problems or difficulties encountered
	OpenItems []string `json:"open_items"` // things left unfinished or needing follow-up
}

// OneLine returns a compact one-line summary in the form "Intent: Outcome".
func (s StructuredSummary) OneLine() string {
	if s.Intent == "" && s.Outcome == "" {
		return ""
	}
	if s.Outcome == "" {
		return s.Intent
	}
	if s.Intent == "" {
		return s.Outcome
	}
	return s.Intent + ": " + s.Outcome
}

// SessionObjective is a persisted, rich description of what a session accomplished.
// It combines the StructuredSummary (intent/outcome/decisions) with the Explain output
// (narrative description). This is stored in a separate table and computed asynchronously
// after capture — either via PostCapture hook or scheduled task.
type SessionObjective struct {
	SessionID    ID                `json:"session_id"`
	Summary      StructuredSummary `json:"summary"`       // intent, outcome, decisions, friction, open_items
	ExplainShort string            `json:"explain_short"` // 2-3 sentence narrative
	ExplainFull  string            `json:"explain_full"`  // detailed paragraph (optional, costs more tokens)
	ComputedAt   time.Time         `json:"computed_at"`
}

// SessionTreeNode represents a session in a tree structure.
// Root nodes have no ParentID; child nodes were forked (e.g. via rewind).
type SessionTreeNode struct {
	Summary  Summary           `json:"summary"`
	Children []SessionTreeNode `json:"children,omitempty"`
	IsFork   bool              `json:"is_fork,omitempty"` // true if this session shares a message prefix with a sibling
}

// DiffResult holds a side-by-side comparison between two sessions.
type DiffResult struct {
	Left         DiffSide     `json:"left"`
	Right        DiffSide     `json:"right"`
	TokenDelta   TokenDelta   `json:"token_delta"`
	CostDelta    CostDelta    `json:"cost_delta,omitempty"`
	FileDiff     FileDiff     `json:"file_diff"`
	ToolDiff     ToolDiff     `json:"tool_diff,omitempty"`
	MessageDelta MessageDelta `json:"message_delta"`
}

// DiffSide holds summary metadata for one side of a diff.
type DiffSide struct {
	ID           ID           `json:"id"`
	Provider     ProviderName `json:"provider"`
	Branch       string       `json:"branch,omitempty"`
	Summary      string       `json:"summary,omitempty"`
	MessageCount int          `json:"message_count"`
	TotalTokens  int          `json:"total_tokens"`
	StorageMode  StorageMode  `json:"storage_mode"`
}

// TokenDelta shows the difference in token usage.
type TokenDelta struct {
	InputDelta  int `json:"input_delta"`  // right - left
	OutputDelta int `json:"output_delta"` // right - left
	TotalDelta  int `json:"total_delta"`  // right - left
}

// CostDelta shows the difference in estimated cost.
type CostDelta struct {
	LeftCost  float64 `json:"left_cost"`
	RightCost float64 `json:"right_cost"`
	Delta     float64 `json:"delta"` // right - left
	Currency  string  `json:"currency"`
}

// FileDiff groups files into shared, left-only, and right-only.
type FileDiff struct {
	Shared    []string `json:"shared"`               // files touched by both sessions
	LeftOnly  []string `json:"left_only,omitempty"`  // files only in left session
	RightOnly []string `json:"right_only,omitempty"` // files only in right session
}

// ToolDiff compares tool usage between two sessions.
type ToolDiff struct {
	Entries []ToolDiffEntry `json:"entries"`
}

// ToolDiffEntry compares a single tool across two sessions.
type ToolDiffEntry struct {
	Name       string `json:"name"`
	LeftCalls  int    `json:"left_calls"`
	RightCalls int    `json:"right_calls"`
	CallsDelta int    `json:"calls_delta"` // right - left
}

// MessageDelta shows where two sessions diverge in their message sequence.
type MessageDelta struct {
	CommonPrefix int `json:"common_prefix"` // number of messages that are identical at the start
	LeftAfter    int `json:"left_after"`    // messages in left after common prefix
	RightAfter   int `json:"right_after"`   // messages in right after common prefix
}

// OffTopicResult holds the analysis of sessions on a branch,
// scoring each session's file relevance to the branch's overall topic.
type OffTopicResult struct {
	Branch   string          `json:"branch"`
	Sessions []OffTopicEntry `json:"sessions"`
	TopFiles []string        `json:"top_files"` // most common files across all sessions on this branch
	Total    int             `json:"total"`     // total sessions analyzed
	OffTopic int             `json:"off_topic"` // count of sessions flagged as off-topic
}

// OffTopicEntry scores a single session's relevance to the branch topic.
type OffTopicEntry struct {
	ID         ID           `json:"id"`
	Provider   ProviderName `json:"provider"`
	Summary    string       `json:"summary,omitempty"`
	Files      []string     `json:"files"`        // files this session touched
	Overlap    float64      `json:"overlap"`      // 0.0–1.0: fraction of this session's files that overlap with other sessions
	IsOffTopic bool         `json:"is_off_topic"` // true when overlap is below threshold
	CreatedAt  time.Time    `json:"created_at"`
}

// ForecastResult holds cost forecasting data computed from historical sessions.
type ForecastResult struct {
	// Historical data
	Period       string       `json:"period"`         // bucketing period: "daily" or "weekly"
	Buckets      []CostBucket `json:"buckets"`        // historical cost per time bucket
	TotalCost    float64      `json:"total_cost"`     // total historical cost (USD)
	AvgPerBucket float64      `json:"avg_per_bucket"` // average cost per bucket
	SessionCount int          `json:"session_count"`  // total sessions analyzed

	// Projection (API-equivalent — includes both subscription and API sessions)
	Projected30d float64 `json:"projected_30d"` // estimated cost for the next 30 days
	Projected90d float64 `json:"projected_90d"` // estimated cost for the next 90 days
	TrendPerDay  float64 `json:"trend_per_day"` // daily cost trend (positive = increasing)
	TrendDir     string  `json:"trend_dir"`     // "increasing", "decreasing", or "stable"

	// API-only projections (real spend — only sessions with actual provider costs)
	APIProjected30d float64 `json:"api_projected_30d,omitempty"` // projected API cost for 30 days
	APIProjected90d float64 `json:"api_projected_90d,omitempty"` // projected API cost for 90 days
	APITrendPerDay  float64 `json:"api_trend_per_day,omitempty"` // daily API cost trend
	APITrendDir     string  `json:"api_trend_dir,omitempty"`     // API trend direction

	// Fixed subscription costs (from config)
	SubscriptionMonthly float64 `json:"subscription_monthly,omitempty"` // total monthly subscription cost
	TotalReal30d        float64 `json:"total_real_30d,omitempty"`       // subscription + API projected 30d

	// Per-backend cost summary
	BackendCosts []BackendCostSummary `json:"backend_costs,omitempty"` // per LLM backend breakdown

	// Model recommendations
	ModelBreakdown []ModelForecast `json:"model_breakdown"` // per-model cost breakdown + recommendation
}

// BackendCostSummary aggregates cost data for a single LLM backend (e.g. "anthropic", "amazon-bedrock").
type BackendCostSummary struct {
	Backend       string  `json:"backend"`                // e.g. "anthropic", "amazon-bedrock"
	BillingType   string  `json:"billing_type"`           // "subscription", "api", "free"
	PlanName      string  `json:"plan_name,omitempty"`    // e.g. "Claude Max"
	MonthlyCost   float64 `json:"monthly_cost,omitempty"` // fixed subscription cost per month
	MessageCount  int     `json:"message_count"`          // total messages via this backend
	TotalTokens   int     `json:"total_tokens"`           // total tokens (input + output)
	EstimatedCost float64 `json:"estimated_cost"`         // API-equivalent cost
	ActualCost    float64 `json:"actual_cost"`            // actual cost reported by provider
	SessionCount  int     `json:"session_count"`          // sessions using this backend
}

// CostBucket holds cost data for a time period.
type CostBucket struct {
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	Cost         float64   `json:"cost"`
	Tokens       int       `json:"tokens"`
	SessionCount int       `json:"session_count"`
}

// ModelForecast holds per-model cost data and a savings recommendation.
type ModelForecast struct {
	Model          string  `json:"model"`
	Cost           float64 `json:"cost"`                     // total cost for this model
	Tokens         int     `json:"tokens"`                   // total tokens for this model
	SessionCount   int     `json:"session_count"`            // sessions using this model
	Share          float64 `json:"share"`                    // percentage of total cost (0-100)
	Recommendation string  `json:"recommendation,omitempty"` // e.g. "Switch to Sonnet to save ~60%"
}

// User represents an aisync user, identified by their git identity.
type User struct {
	CreatedAt time.Time `json:"created_at"`
	ID        ID        `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Source    string    `json:"source"` // "git", "config", "api"
}

// UserPreferences stores per-user dashboard and UI preferences as JSON.
// When UserID is empty, this represents the global defaults (anonymous/shared).
type UserPreferences struct {
	UpdatedAt time.Time            `json:"updated_at"`
	UserID    ID                   `json:"user_id,omitempty"` // empty = global defaults
	Dashboard DashboardPreferences `json:"dashboard"`
}

// DashboardPreferences holds dashboard-specific UI settings.
type DashboardPreferences struct {
	PageSize  int      `json:"page_size,omitempty"`  // 0 = use system default (25)
	Columns   []string `json:"columns,omitempty"`    // empty = use system defaults
	SortBy    string   `json:"sort_by,omitempty"`    // empty = "created_at"
	SortOrder string   `json:"sort_order,omitempty"` // empty = "desc"
}
