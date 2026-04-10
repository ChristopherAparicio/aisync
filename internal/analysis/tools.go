// Package analysis defines port-level types for agentic tool use.
//
// The ToolExecutor interface decouples the Anthropic adapter (which drives
// the agentic loop) from the concrete storage implementation. The service
// layer constructs a ToolExecutor that is pre-scoped to a session, so the
// LLM analyst cannot accidentally access data from other sessions.
package analysis

import "encoding/json"

// ToolExecutor is the port for executing investigation tools during an
// agentic analysis loop. Implementations are pre-scoped to a specific
// session ID — all tools automatically return data for that session only.
//
// Each method corresponds to an action the LLM can request via the
// single query_session tool. The adapter dispatches to the matching
// method based on the "action" field in the tool input.
type ToolExecutor interface {
	// GetMessages returns messages within the given index range [from, to] inclusive.
	// If from/to are out of bounds they are clamped. Returns JSON-serializable data.
	GetMessages(from, to int) (json.RawMessage, error)

	// GetToolCalls returns tool calls matching the optional filter.
	// Filter fields: name (tool name glob), state ("error", "completed"), limit.
	GetToolCalls(filter ToolCallFilter) (json.RawMessage, error)

	// SearchMessages searches message content for a pattern (case-insensitive substring).
	// Returns matching messages with their indices.
	SearchMessages(pattern string, limit int) (json.RawMessage, error)

	// GetCompactionDetails returns detailed compaction analysis for the session.
	GetCompactionDetails() (json.RawMessage, error)

	// GetErrorDetails returns detailed error information for tool calls that failed.
	GetErrorDetails(limit int) (json.RawMessage, error)

	// GetTokenTimeline returns per-message token usage over the session lifetime.
	// Useful for identifying token spikes and compaction patterns.
	GetTokenTimeline() (json.RawMessage, error)
}

// ToolCallFilter controls which tool calls are returned by GetToolCalls.
type ToolCallFilter struct {
	// Name filters by tool name (case-insensitive substring match). Empty = all.
	Name string `json:"name,omitempty"`

	// State filters by tool call state ("error", "completed"). Empty = all.
	State string `json:"state,omitempty"`

	// Limit caps the number of results. 0 = default (50).
	Limit int `json:"limit,omitempty"`
}

// ToolDefinition describes a tool available to the LLM analyst.
// These are sent in the Anthropic API request's "tools" array.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// AnalystToolName is the single tool name used for all session investigation actions.
const AnalystToolName = "query_session"

// AnalystTools returns the tool definitions for the LLM analyst.
// A single polymorphic tool is used instead of multiple tools to minimize
// token overhead (~200 tokens vs ~1000 for 6 separate tool definitions).
// The "action" parameter selects which investigation to run.
func AnalystTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Name: AnalystToolName,
			Description: `Query the session being analyzed. Use this to investigate specific aspects of the session conversation, tool usage, errors, token patterns, and compaction events. The "action" parameter selects what data to retrieve.

Actions:
- get_messages: Read messages by index range (0-based inclusive). Params: from, to.
- get_tool_calls: List tool invocations with optional filters. Params: name (substring), state ("error"/"completed"), limit.
- search_messages: Search message content by keyword. Params: pattern, limit.
- get_compaction_details: Full compaction analysis (events, cascades, recovery). No params.
- get_error_details: Tool call error details. Params: limit.
- get_token_timeline: Per-message token usage timeline. No params.`,
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {
						"type": "string",
						"enum": ["get_messages", "get_tool_calls", "search_messages", "get_compaction_details", "get_error_details", "get_token_timeline"],
						"description": "Which investigation to run."
					},
					"from": {
						"type": "integer",
						"description": "Start message index (get_messages). 0-based, inclusive."
					},
					"to": {
						"type": "integer",
						"description": "End message index (get_messages). 0-based, inclusive."
					},
					"name": {
						"type": "string",
						"description": "Tool name filter (get_tool_calls). Case-insensitive substring."
					},
					"state": {
						"type": "string",
						"enum": ["error", "completed"],
						"description": "Tool call state filter (get_tool_calls)."
					},
					"pattern": {
						"type": "string",
						"description": "Search pattern (search_messages). Case-insensitive substring."
					},
					"limit": {
						"type": "integer",
						"description": "Max results (get_tool_calls, search_messages, get_error_details)."
					}
				},
				"required": ["action"]
			}`),
		},
	}
}
