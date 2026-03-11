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
	ExportedAt      time.Time    `json:"exported_at"`
	CreatedAt       time.Time    `json:"created_at"`
	ProjectPath     string       `json:"project_path"`
	ExportedBy      string       `json:"exported_by,omitempty"`
	ParentID        ID           `json:"parent_id,omitempty"`
	OwnerID         ID           `json:"owner_id,omitempty"`
	StorageMode     StorageMode  `json:"storage_mode"`
	Summary         string       `json:"summary,omitempty"`
	ID              ID           `json:"id"`
	Provider        ProviderName `json:"provider"`
	Agent           string       `json:"agent"`
	Branch          string       `json:"branch,omitempty"`
	CommitSHA       string       `json:"commit_sha,omitempty"`
	Messages        []Message    `json:"messages,omitempty"`
	Children        []Session    `json:"children,omitempty"`
	Links           []Link       `json:"links,omitempty"`
	FileChanges     []FileChange `json:"file_changes,omitempty"`
	TokenUsage      TokenUsage   `json:"token_usage"`
	ForkedAtMessage int          `json:"forked_at_message,omitempty"` // 1-based message index where this session was forked (via rewind)
	Version         int          `json:"version"`
}

// Summary is a lightweight representation of a session for listings.
type Summary struct {
	CreatedAt    time.Time    `json:"created_at"`
	ID           ID           `json:"id"`
	ParentID     ID           `json:"parent_id,omitempty"`
	OwnerID      ID           `json:"owner_id,omitempty"`
	Provider     ProviderName `json:"provider"`
	Agent        string       `json:"agent"`
	Branch       string       `json:"branch,omitempty"`
	Summary      string       `json:"summary,omitempty"`
	MessageCount int          `json:"message_count"`
	TotalTokens  int          `json:"total_tokens"`
}

// Message represents a single message in an AI conversation.
type Message struct {
	Timestamp    time.Time   `json:"timestamp"`
	ID           string      `json:"id"`
	Content      string      `json:"content"`
	Model        string      `json:"model,omitempty"`
	Thinking     string      `json:"thinking,omitempty"`
	Role         MessageRole `json:"role"`
	ToolCalls    []ToolCall  `json:"tool_calls,omitempty"`
	InputTokens  int         `json:"input_tokens,omitempty"`
	OutputTokens int         `json:"output_tokens,omitempty"`
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

// FileChange records a file touched during a session.
type FileChange struct {
	FilePath   string     `json:"file_path"`
	ChangeType ChangeType `json:"change_type"`
}

// Link connects a session to a git object (branch, commit, PR).
type Link struct {
	LinkType LinkType `json:"link_type"`
	Ref      string   `json:"ref"`
}

// TokenUsage tracks token consumption for a session.
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// Cost represents a monetary amount in a given currency.
type Cost struct {
	InputCost  float64 `json:"input_cost"`
	OutputCost float64 `json:"output_cost"`
	TotalCost  float64 `json:"total_cost"`
	Currency   string  `json:"currency"` // always "USD"
}

// CostEstimate is the full cost breakdown for a session.
type CostEstimate struct {
	TotalCost     Cost        `json:"total_cost"`
	PerModel      []ModelCost `json:"per_model"`
	UnknownModels []string    `json:"unknown_models,omitempty"` // models without pricing data
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

// ListOptions controls session listing queries.
type ListOptions struct {
	ProjectPath string
	Branch      string
	Provider    ProviderName
	All         bool
}

// SearchQuery defines criteria for searching sessions.
// All fields are optional — an empty query returns all sessions (paginated).
// Filters are combined with AND logic.
type SearchQuery struct {
	// Keyword performs a case-insensitive text search across summary and message content.
	// In "contain" mode this uses SQL LIKE; in "fulltext" mode it uses SQLite FTS5.
	Keyword string

	// Filters narrow results by exact match on structured fields.
	ProjectPath string
	Branch      string
	Provider    ProviderName
	OwnerID     ID

	// Time range filters (inclusive). Zero values are ignored.
	Since time.Time
	Until time.Time

	// Pagination
	Limit  int // 0 means use default (50)
	Offset int
}

// SearchResult holds a page of search results with metadata.
type SearchResult struct {
	Sessions   []Summary `json:"sessions"`
	TotalCount int       `json:"total_count"`
	Limit      int       `json:"limit"`
	Offset     int       `json:"offset"`
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

	// Projection
	Projected30d float64 `json:"projected_30d"` // estimated cost for the next 30 days
	Projected90d float64 `json:"projected_90d"` // estimated cost for the next 90 days
	TrendPerDay  float64 `json:"trend_per_day"` // daily cost trend (positive = increasing)
	TrendDir     string  `json:"trend_dir"`     // "increasing", "decreasing", or "stable"

	// Model recommendations
	ModelBreakdown []ModelForecast `json:"model_breakdown"` // per-model cost breakdown + recommendation
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
