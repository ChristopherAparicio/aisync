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
	Timestamp     time.Time      `json:"timestamp"`
	ID            string         `json:"id"`
	Content       string         `json:"content"`
	Model         string         `json:"model,omitempty"`
	ProviderID    string         `json:"provider_id,omitempty"` // e.g. "anthropic", "amazon-bedrock", "opencode"
	Thinking      string         `json:"thinking,omitempty"`
	Role          MessageRole    `json:"role"`
	ToolCalls     []ToolCall     `json:"tool_calls,omitempty"`
	Images        []ImageMeta    `json:"images,omitempty"`         // images included in this message
	ContentBlocks []ContentBlock `json:"content_blocks,omitempty"` // structured content blocks (text, image, etc.)
	InputTokens   int            `json:"input_tokens,omitempty"`
	OutputTokens  int            `json:"output_tokens,omitempty"`
	ProviderCost  float64        `json:"provider_cost,omitempty"` // actual cost reported by provider (0 = unknown/subscription)
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
	BucketStart    time.Time    `json:"bucket_start"`
	BucketEnd      time.Time    `json:"bucket_end"`
	Granularity    string       `json:"granularity"` // "1h" or "1d"
	ProjectPath    string       `json:"project_path,omitempty"`
	Provider       ProviderName `json:"provider,omitempty"`
	InputTokens    int          `json:"input_tokens"`
	OutputTokens   int          `json:"output_tokens"`
	ImageTokens    int          `json:"image_tokens"`
	SessionCount   int          `json:"session_count"`
	MessageCount   int          `json:"message_count"`
	ToolCallCount  int          `json:"tool_call_count"`
	ToolErrorCount int          `json:"tool_error_count"`
	ImageCount     int          `json:"image_count"`
	UserMsgCount   int          `json:"user_msg_count"`   // messages from user (human interaction indicator)
	AssistMsgCount int          `json:"assist_msg_count"` // messages from assistant
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
