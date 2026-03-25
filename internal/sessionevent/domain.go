// Package sessionevent implements structured event extraction and aggregation
// for AI coding sessions.
//
// Events are first-class entities that capture discrete actions occurring
// during a session (tool calls, skill loads, agent detection, errors, commands,
// image usage). They are extracted deterministically from session messages at
// capture time and persisted for querying.
//
// Two query patterns are supported:
//   - Micro view: all events for a single session (GetSessionEvents)
//   - Macro view: aggregated buckets across a project over time (QueryBuckets)
//
// No interfaces live in this package — they are defined by the packages that
// own the abstraction (store, service).
package sessionevent

import (
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── EventType ──

// EventType classifies an event by what happened.
type EventType string

// Known event types.
const (
	EventToolCall       EventType = "tool_call"       // a tool was invoked (any tool)
	EventSkillLoad      EventType = "skill_load"      // a skill was loaded/invoked
	EventAgentDetection EventType = "agent_detection" // the agent/provider was detected
	EventError          EventType = "error"           // an error occurred (tool or provider)
	EventCommand        EventType = "command"         // a bash/shell command was executed
	EventImageUsage     EventType = "image_usage"     // an image was included in a message
)

var allEventTypes = []EventType{
	EventToolCall,
	EventSkillLoad,
	EventAgentDetection,
	EventError,
	EventCommand,
	EventImageUsage,
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

// ── Event ──

// Event is a structured event extracted from a session.
// Each event captures a discrete action with enough metadata to aggregate
// into buckets (macro view) or inspect individually (micro view).
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

	// Event-specific payload — exactly one of these is populated depending on Type.
	ToolCall  *ToolCallDetail  `json:"tool_call,omitempty"`
	SkillLoad *SkillLoadDetail `json:"skill_load,omitempty"`
	AgentInfo *AgentDetail     `json:"agent_info,omitempty"`
	Error     *ErrorDetail     `json:"error,omitempty"`
	Command   *CommandDetail   `json:"command,omitempty"`
	Image     *ImageDetail     `json:"image,omitempty"`
}

// ── Event Detail Payloads ──

// ToolCallDetail captures metadata about a single tool invocation.
type ToolCallDetail struct {
	ToolName     string            `json:"tool_name"`               // e.g. "bash", "file_edit", "Read"
	ToolCallID   string            `json:"tool_call_id,omitempty"`  // provider's tool call ID
	State        session.ToolState `json:"state"`                   // completed, error, etc.
	DurationMs   int               `json:"duration_ms,omitempty"`   // execution time
	InputTokens  int               `json:"input_tokens,omitempty"`  // tokens consumed by input
	OutputTokens int               `json:"output_tokens,omitempty"` // tokens consumed by output
}

// SkillLoadDetail captures metadata about a skill being loaded.
type SkillLoadDetail struct {
	SkillName  string `json:"skill_name"`             // e.g. "replay-tester", "opencode-sessions"
	LoadMethod string `json:"load_method,omitempty"`  // "tool_call" or "content_tag"
	ToolCallID string `json:"tool_call_id,omitempty"` // tool call that loaded it (if via tool)
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

// ── EventBucket (macro view) ──

// EventBucket aggregates event counts for a specific time window and project.
// This is the macro view — one row per (hour, project, provider) combination.
type EventBucket struct {
	// Time window
	BucketStart time.Time `json:"bucket_start"`
	BucketEnd   time.Time `json:"bucket_end"`
	Granularity string    `json:"granularity"` // "1h" or "1d"

	// Grouping keys
	ProjectPath string               `json:"project_path,omitempty"`
	RemoteURL   string               `json:"remote_url,omitempty"`
	Provider    session.ProviderName `json:"provider,omitempty"`

	// Counters — tool calls
	ToolCallCount  int            `json:"tool_call_count"`     // total tool invocations
	ToolErrorCount int            `json:"tool_error_count"`    // tool calls with state=error
	UniqueTools    int            `json:"unique_tools"`        // distinct tool names
	TopTools       map[string]int `json:"top_tools,omitempty"` // top tool names -> count

	// Counters — skills
	SkillLoadCount int            `json:"skill_load_count"`     // total skill loads
	UniqueSkills   int            `json:"unique_skills"`        // distinct skill names
	TopSkills      map[string]int `json:"top_skills,omitempty"` // skill name -> count

	// Counters — agents/providers
	SessionCount   int            `json:"session_count"`             // sessions in this bucket
	AgentBreakdown map[string]int `json:"agent_breakdown,omitempty"` // agent name -> session count

	// Counters — commands
	CommandCount      int            `json:"command_count"`          // total bash/shell commands
	CommandErrorCount int            `json:"command_error_count"`    // commands that errored
	TopCommands       map[string]int `json:"top_commands,omitempty"` // base command -> count

	// Counters — errors
	ErrorCount      int                           `json:"error_count"` // total errors
	ErrorByCategory map[session.ErrorCategory]int `json:"error_by_category,omitempty"`

	// Counters — images
	ImageCount  int `json:"image_count"`            // total images
	ImageTokens int `json:"image_tokens,omitempty"` // estimated tokens for images
}

// ── SessionEventSummary (micro view) ──

// SessionEventSummary provides a complete breakdown of events for a single session.
// This is the micro view — everything about one session.
type SessionEventSummary struct {
	SessionID session.ID `json:"session_id"`

	// Totals
	TotalEvents    int `json:"total_events"`
	ToolCallCount  int `json:"tool_call_count"`
	SkillLoadCount int `json:"skill_load_count"`
	CommandCount   int `json:"command_count"`
	ErrorCount     int `json:"error_count"`
	ImageCount     int `json:"image_count"`

	// Tool breakdown
	UniqueToolCount int            `json:"unique_tool_count"` // distinct tool names
	ToolBreakdown   map[string]int `json:"tool_breakdown"`    // tool name -> call count

	// Skill breakdown
	SkillsLoaded   []string       `json:"skills_loaded"`   // ordered list of loaded skills
	SkillBreakdown map[string]int `json:"skill_breakdown"` // skill name -> load count

	// Command breakdown
	UniqueCommandCount int            `json:"unique_command_count"`
	CommandBreakdown   map[string]int `json:"command_breakdown"` // base command -> count
	CommandErrorCount  int            `json:"command_error_count"`

	// Error breakdown
	ErrorByCategory map[session.ErrorCategory]int `json:"error_by_category"`
	ErrorBySource   map[session.ErrorSource]int   `json:"error_by_source"`

	// Agent info
	Provider session.ProviderName `json:"provider"`
	Agent    string               `json:"agent"`
	Models   []string             `json:"models,omitempty"` // distinct models used

	// Timing
	FirstEventAt time.Time `json:"first_event_at,omitempty"`
	LastEventAt  time.Time `json:"last_event_at,omitempty"`
}

// NewSessionEventSummary computes a summary from a list of events.
func NewSessionEventSummary(sessionID session.ID, events []Event) SessionEventSummary {
	s := SessionEventSummary{
		SessionID:        sessionID,
		ToolBreakdown:    make(map[string]int),
		SkillBreakdown:   make(map[string]int),
		CommandBreakdown: make(map[string]int),
		ErrorByCategory:  make(map[session.ErrorCategory]int),
		ErrorBySource:    make(map[session.ErrorSource]int),
	}

	toolsSeen := make(map[string]bool)
	skillsSeen := make(map[string]bool)
	commandsSeen := make(map[string]bool)
	modelsSeen := make(map[string]bool)

	for _, e := range events {
		s.TotalEvents++

		// Track time range.
		if s.FirstEventAt.IsZero() || e.OccurredAt.Before(s.FirstEventAt) {
			s.FirstEventAt = e.OccurredAt
		}
		if e.OccurredAt.After(s.LastEventAt) {
			s.LastEventAt = e.OccurredAt
		}

		switch e.Type {
		case EventToolCall:
			s.ToolCallCount++
			if e.ToolCall != nil {
				s.ToolBreakdown[e.ToolCall.ToolName]++
				toolsSeen[e.ToolCall.ToolName] = true
			}

		case EventSkillLoad:
			s.SkillLoadCount++
			if e.SkillLoad != nil {
				if !skillsSeen[e.SkillLoad.SkillName] {
					skillsSeen[e.SkillLoad.SkillName] = true
					s.SkillsLoaded = append(s.SkillsLoaded, e.SkillLoad.SkillName)
				}
				s.SkillBreakdown[e.SkillLoad.SkillName]++
			}

		case EventCommand:
			s.CommandCount++
			if e.Command != nil {
				s.CommandBreakdown[e.Command.BaseCommand]++
				commandsSeen[e.Command.BaseCommand] = true
				if e.Command.State == session.ToolStateError {
					s.CommandErrorCount++
				}
			}

		case EventError:
			s.ErrorCount++
			if e.Error != nil {
				s.ErrorByCategory[e.Error.Category]++
				s.ErrorBySource[e.Error.Source]++
			}

		case EventImageUsage:
			s.ImageCount++

		case EventAgentDetection:
			if e.AgentInfo != nil {
				s.Provider = e.AgentInfo.Provider
				s.Agent = e.AgentInfo.Agent
				if e.AgentInfo.Model != "" && !modelsSeen[e.AgentInfo.Model] {
					modelsSeen[e.AgentInfo.Model] = true
					s.Models = append(s.Models, e.AgentInfo.Model)
				}
			}
		}
	}

	s.UniqueToolCount = len(toolsSeen)
	s.UniqueCommandCount = len(commandsSeen)

	return s
}

// ── Query types ──

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

// BucketQuery filters bucket aggregations for retrieval.
type BucketQuery struct {
	ProjectPath string               `json:"project_path,omitempty"` // filter by project
	RemoteURL   string               `json:"remote_url,omitempty"`   // filter by git remote
	Provider    session.ProviderName `json:"provider,omitempty"`     // filter by provider
	Granularity string               `json:"granularity"`            // "1h" or "1d"
	Since       time.Time            `json:"since"`                  // start of time range
	Until       time.Time            `json:"until"`                  // end of time range
}
