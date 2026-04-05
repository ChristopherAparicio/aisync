package sessionevent

import (
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── SessionEventSummary (micro view) ───────────────────────────────────────

// SessionEventSummary provides a complete breakdown of events for a single session.
// This is the micro view — everything about one session at a glance.
//
// It is computed from a list of events, not persisted.
type SessionEventSummary struct {
	SessionID session.ID `json:"session_id"`

	// Totals
	TotalEvents    int `json:"total_events"`
	ToolCallCount  int `json:"tool_call_count"`
	SkillLoadCount int `json:"skill_load_count"`
	CommandCount   int `json:"command_count"`
	ErrorCount     int `json:"error_count"`
	ImageCount     int `json:"image_count"`

	// Compaction
	CompactionCount int `json:"compaction_count"` // number of context compaction events

	// Tool breakdown
	UniqueToolCount int            `json:"unique_tool_count"` // distinct tool names
	ToolBreakdown   map[string]int `json:"tool_breakdown"`    // tool name -> call count

	// MCP server breakdown (computed from ToolCategory on ToolCallDetail)
	MCPServerBreakdown map[string]int `json:"mcp_server_breakdown,omitempty"` // server name -> call count

	// Skill breakdown
	SkillsLoaded        []string       `json:"skills_loaded"`         // ordered list of loaded skills
	SkillBreakdown      map[string]int `json:"skill_breakdown"`       // skill name -> load count
	SkillTokenBreakdown map[string]int `json:"skill_token_breakdown"` // skill name -> estimated tokens consumed
	TotalSkillTokens    int            `json:"total_skill_tokens"`    // sum of all skill tokens

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
// This is a pure function — no side effects, no persistence.
func NewSessionEventSummary(sessionID session.ID, events []Event) SessionEventSummary {
	s := SessionEventSummary{
		SessionID:           sessionID,
		ToolBreakdown:       make(map[string]int),
		MCPServerBreakdown:  make(map[string]int),
		SkillBreakdown:      make(map[string]int),
		SkillTokenBreakdown: make(map[string]int),
		CommandBreakdown:    make(map[string]int),
		ErrorByCategory:     make(map[session.ErrorCategory]int),
		ErrorBySource:       make(map[session.ErrorSource]int),
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

				// Track MCP server breakdown from classified tool category.
				if server := session.MCPServerName(e.ToolCall.ToolCategory); server != "" {
					s.MCPServerBreakdown[server]++
				}
			}

		case EventSkillLoad:
			s.SkillLoadCount++
			if e.SkillLoad != nil {
				if !skillsSeen[e.SkillLoad.SkillName] {
					skillsSeen[e.SkillLoad.SkillName] = true
					s.SkillsLoaded = append(s.SkillsLoaded, e.SkillLoad.SkillName)
				}
				s.SkillBreakdown[e.SkillLoad.SkillName]++
				if e.SkillLoad.EstimatedTokens > 0 {
					s.SkillTokenBreakdown[e.SkillLoad.SkillName] += e.SkillLoad.EstimatedTokens
					s.TotalSkillTokens += e.SkillLoad.EstimatedTokens
				}
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

		case EventCompaction:
			s.CompactionCount++

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
