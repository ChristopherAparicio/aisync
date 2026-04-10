package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
)

// querySessionInput is the unified input structure for the query_session tool.
// The "action" field selects which investigation to run; remaining fields are
// action-specific parameters (ignored when not relevant to the chosen action).
type querySessionInput struct {
	Action  string `json:"action"`
	From    int    `json:"from"`
	To      int    `json:"to"`
	Name    string `json:"name"`
	State   string `json:"state"`
	Pattern string `json:"pattern"`
	Limit   int    `json:"limit"`
}

// dispatchTool routes a tool call to the correct ToolExecutor method.
// The tool name is always "query_session"; routing is based on the "action"
// field inside the input JSON. Returns the tool result as a string (JSON)
// and whether it is an error.
func dispatchTool(executor analysis.ToolExecutor, toolName string, input json.RawMessage) (string, bool) {
	if toolName != analysis.AnalystToolName {
		return fmt.Sprintf("unknown tool: %s", toolName), true
	}

	var params querySessionInput
	if err := json.Unmarshal(input, &params); err != nil {
		return fmt.Sprintf("invalid input: %v", err), true
	}

	var result json.RawMessage
	var err error

	switch params.Action {
	case "get_messages":
		result, err = executor.GetMessages(params.From, params.To)

	case "get_tool_calls":
		filter := analysis.ToolCallFilter{
			Name:  params.Name,
			State: params.State,
			Limit: params.Limit,
		}
		result, err = executor.GetToolCalls(filter)

	case "search_messages":
		result, err = executor.SearchMessages(params.Pattern, params.Limit)

	case "get_compaction_details":
		result, err = executor.GetCompactionDetails()

	case "get_error_details":
		result, err = executor.GetErrorDetails(params.Limit)

	case "get_token_timeline":
		result, err = executor.GetTokenTimeline()

	default:
		return fmt.Sprintf("unknown action: %q", params.Action), true
	}

	if err != nil {
		return fmt.Sprintf("tool error: %v", err), true
	}

	return string(result), false
}
