// Package sessionevent implements structured event extraction and aggregation
// for AI coding sessions.
//
// Events are first-class domain entities that capture discrete actions occurring
// during a session (tool calls, skill loads, agent detection, errors, commands,
// image usage, compaction). They are extracted deterministically from session
// messages at capture time and persisted for querying.
//
// # Architecture
//
// Domain types (this file + bucket.go) define the shared vocabulary:
//   - Event, EventType, detail payloads — the event aggregate root
//   - EventBucket, BucketQuery — the macro view aggregate
//
// Application logic lives in separate files:
//   - Processor   — deterministic event extraction from session messages
//   - Aggregator  — bucket computation from raw events
//   - Service     — orchestration (extract → persist → aggregate)
//
// Persistence ports (store.go) are defined in this package because the domain
// owns the abstraction. Infrastructure adapters (SQLite) implement them.
//
// # Two query patterns
//
//   - Micro view: all events for a single session (GetSessionEvents)
//   - Macro view: aggregated buckets across a project over time (QueryBuckets)
package sessionevent

import (
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── EventType ──────────────────────────────────────────────────────────────

// EventType classifies an event by what happened.
// It is a closed set of known event types — new types require a code change.
type EventType string

// Known event types.
const (
	EventToolCall       EventType = "tool_call"       // a tool was invoked (builtin or MCP)
	EventSkillLoad      EventType = "skill_load"      // a skill was loaded/invoked
	EventAgentDetection EventType = "agent_detection" // the agent/provider was detected
	EventError          EventType = "error"           // an error occurred (tool or provider)
	EventCommand        EventType = "command"         // a bash/shell command was executed
	EventImageUsage     EventType = "image_usage"     // an image was included in a message
	EventCompaction     EventType = "compaction"      // context window compaction detected
)

var allEventTypes = []EventType{
	EventToolCall,
	EventSkillLoad,
	EventAgentDetection,
	EventError,
	EventCommand,
	EventImageUsage,
	EventCompaction,
}

// Valid reports whether t is a known event type.
func (t EventType) Valid() bool {
	for _, v := range allEventTypes {
		if t == v {
			return true
		}
	}
	return false
}

// String returns the string representation.
func (t EventType) String() string {
	return string(t)
}

// ── Event (Aggregate Root) ─────────────────────────────────────────────────

// Event is a structured event extracted from a session.
// Each event captures a discrete action with enough metadata to aggregate
// into buckets (macro view) or inspect individually (micro view).
//
// Exactly one detail payload is populated depending on Type.
// This is a union type — consumers must check Type before accessing payloads.
type Event struct {
	// Identity
	ID        string     `json:"id"`         // unique event ID (UUID)
	SessionID session.ID `json:"session_id"` // owning session

	// Classification
	Type EventType `json:"type"` // what happened

	// Context — where in the session this event occurred
	MessageIndex int       `json:"message_index"`        // 0-based position in session messages
	MessageID    string    `json:"message_id,omitempty"` // provider's message ID
	OccurredAt   time.Time `json:"occurred_at"`          // when the event happened

	// Project context — denormalized from session for bucket aggregation
	ProjectPath string               `json:"project_path,omitempty"` // local filesystem path
	RemoteURL   string               `json:"remote_url,omitempty"`   // normalized git remote URL
	Provider    session.ProviderName `json:"provider,omitempty"`     // claude-code, opencode, etc.
	Agent       string               `json:"agent,omitempty"`        // agent name

	// Event-specific payload — exactly one is populated depending on Type.
	ToolCall   *ToolCallDetail   `json:"tool_call,omitempty"`
	SkillLoad  *SkillLoadDetail  `json:"skill_load,omitempty"`
	AgentInfo  *AgentDetail      `json:"agent_info,omitempty"`
	Error      *ErrorDetail      `json:"error,omitempty"`
	Command    *CommandDetail    `json:"command,omitempty"`
	Image      *ImageDetail      `json:"image,omitempty"`
	Compaction *CompactionDetail `json:"compaction,omitempty"`
}

// ── Event Detail Payloads ──────────────────────────────────────────────────
//
// Each detail struct captures type-specific metadata.
// They are value objects — no identity, immutable after creation.

// ToolCallDetail captures metadata about a single tool invocation.
type ToolCallDetail struct {
	ToolName     string            `json:"tool_name"`               // e.g. "bash", "file_edit", "Read"
	ToolCategory string            `json:"tool_category"`           // "builtin" or "mcp:<server>" (from ClassifyTool)
	ToolCallID   string            `json:"tool_call_id,omitempty"`  // provider's tool call ID
	State        session.ToolState `json:"state"`                   // completed, error, etc.
	DurationMs   int               `json:"duration_ms,omitempty"`   // execution time
	InputTokens  int               `json:"input_tokens,omitempty"`  // tokens consumed by input
	OutputTokens int               `json:"output_tokens,omitempty"` // tokens consumed by output
}

// SkillLoadDetail captures metadata about a skill being loaded.
type SkillLoadDetail struct {
	SkillName       string `json:"skill_name"`                 // e.g. "replay-tester", "opencode-sessions"
	LoadMethod      string `json:"load_method,omitempty"`      // "tool_call" or "content_tag"
	ToolCallID      string `json:"tool_call_id,omitempty"`     // tool call that loaded it (if via tool)
	EstimatedTokens int    `json:"estimated_tokens,omitempty"` // estimated context tokens consumed (~len/4)
	ContentBytes    int    `json:"content_bytes,omitempty"`    // raw byte size of skill content
}

// AgentDetail captures the detected agent/provider for the session.
type AgentDetail struct {
	Provider session.ProviderName `json:"provider"`        // claude-code, opencode, cursor, etc.
	Agent    string               `json:"agent"`           // agent name (e.g. "jarvis", "claude")
	Model    string               `json:"model,omitempty"` // primary model used
}

// ErrorDetail captures a session error event.
type ErrorDetail struct {
	Category   session.ErrorCategory `json:"category"`              // provider_error, rate_limit, tool_error, etc.
	Source     session.ErrorSource   `json:"source"`                // provider, tool, client
	Message    string                `json:"message"`               // human-readable error message
	ToolName   string                `json:"tool_name,omitempty"`   // tool that failed
	HTTPStatus int                   `json:"http_status,omitempty"` // HTTP status code
}

// CommandDetail captures a bash/shell command execution.
type CommandDetail struct {
	BaseCommand string            `json:"base_command"`           // e.g. "git", "npm", "ls"
	FullCommand string            `json:"full_command,omitempty"` // full command string (truncated)
	ToolCallID  string            `json:"tool_call_id,omitempty"`
	State       session.ToolState `json:"state"` // completed, error
	DurationMs  int               `json:"duration_ms,omitempty"`
}

// ImageDetail captures an image usage event.
type ImageDetail struct {
	MediaType      string `json:"media_type"` // e.g. "image/png"
	SizeBytes      int    `json:"size_bytes,omitempty"`
	TokensEstimate int    `json:"tokens_estimate,omitempty"`
	Source         string `json:"source,omitempty"` // "base64", "url", "file"
}

// CompactionDetail captures a context window compaction event.
//
// Compaction occurs when the AI provider summarizes the conversation to free
// context window space. It is detected via token-drop heuristic: a >50% drop
// in input tokens between consecutive assistant messages, confirmed by cache
// invalidation (cache_read_tokens drops to ~0).
type CompactionDetail struct {
	// Positions in the message stream where compaction was detected.
	BeforeMessageIdx int `json:"before_message_idx"` // last message before compaction
	AfterMessageIdx  int `json:"after_message_idx"`  // first message after compaction

	// Token measurements around the compaction boundary.
	BeforeInputTokens int     `json:"before_input_tokens"` // input tokens on the "before" message
	AfterInputTokens  int     `json:"after_input_tokens"`  // input tokens on the "after" message
	DropRatio         float64 `json:"drop_ratio"`          // AfterInputTokens / BeforeInputTokens (e.g. 0.3 = 70% drop)

	// Cache invalidation signal — confirms the compaction hypothesis.
	// After compaction, the entire conversation is new content.
	CacheInvalidated bool `json:"cache_invalidated"` // true if cache_read dropped to ~0

	// Model context — useful for saturation analysis.
	Model string `json:"model,omitempty"` // model that was active when compaction occurred
}

// ── Query types ────────────────────────────────────────────────────────────

// EventQuery filters events for retrieval.
type EventQuery struct {
	SessionID   session.ID           `json:"session_id,omitempty"`   // filter by session
	ProjectPath string               `json:"project_path,omitempty"` // filter by project
	RemoteURL   string               `json:"remote_url,omitempty"`   // filter by git remote
	Type        EventType            `json:"type,omitempty"`         // filter by event type
	Provider    session.ProviderName `json:"provider,omitempty"`     // filter by provider
	Since       time.Time            `json:"since,omitempty"`        // start of time range
	Until       time.Time            `json:"until,omitempty"`        // end of time range
	Limit       int                  `json:"limit,omitempty"`        // 0 = no limit
	Offset      int                  `json:"offset,omitempty"`
}
