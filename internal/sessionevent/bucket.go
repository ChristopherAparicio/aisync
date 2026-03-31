package sessionevent

import (
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── EventBucket (macro view) ───────────────────────────────────────────────

// EventBucket aggregates event counts for a specific time window and project.
// This is the macro view — one row per (hour, project, provider) combination.
//
// EventBucket is a read model optimized for dashboard queries. It is computed
// from raw events by the BucketAggregator and persisted for fast retrieval.
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

	// Counters — MCP servers (aggregated from classified tool categories)
	TopMCPServers map[string]int `json:"top_mcp_servers,omitempty"` // MCP server name -> call count

	// Counters — skills
	SkillLoadCount int            `json:"skill_load_count"`       // total skill loads
	UniqueSkills   int            `json:"unique_skills"`          // distinct skill names
	TopSkills      map[string]int `json:"top_skills,omitempty"`   // skill name -> count
	SkillTokens    map[string]int `json:"skill_tokens,omitempty"` // skill name -> total estimated tokens

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

	// Counters — compaction
	CompactionCount int `json:"compaction_count,omitempty"` // context compaction events
}

// ── BucketQuery ────────────────────────────────────────────────────────────

// BucketQuery filters bucket aggregations for retrieval.
type BucketQuery struct {
	ProjectPath string               `json:"project_path,omitempty"` // filter by project
	RemoteURL   string               `json:"remote_url,omitempty"`   // filter by git remote
	Provider    session.ProviderName `json:"provider,omitempty"`     // filter by provider
	Granularity string               `json:"granularity"`            // "1h" or "1d"
	Since       time.Time            `json:"since"`                  // start of time range
	Until       time.Time            `json:"until"`                  // end of time range
}
