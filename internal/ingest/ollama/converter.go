// Package ollama converts Ollama's native /api/chat conversation format
// into aisync's universal IngestRequest. This keeps Ollama-specific parsing
// in an adapter layer, so the service domain stays provider-agnostic.
package ollama

import (
	"encoding/json"
	"fmt"

	"github.com/ChristopherAparicio/aisync/internal/service"
)

// ── Ollama native types ──

// Request wraps an Ollama conversation with optional metadata.
// The Conversation field mirrors the messages array from Ollama's /api/chat.
type Request struct {
	// Metadata.
	ProjectPath string `json:"project_path,omitempty"`
	Agent       string `json:"agent,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Summary     string `json:"summary,omitempty"`
	SessionID   string `json:"session_id,omitempty"`

	// Ollama fields.
	Model           string    `json:"model"`                       // e.g. "qwen3-coder:30b"
	Conversation    []Message `json:"conversation"`                // Ollama chat messages
	PromptEvalCount int       `json:"prompt_eval_count,omitempty"` // input token count
	EvalCount       int       `json:"eval_count,omitempty"`        // output token count
	TotalDuration   int64     `json:"total_duration,omitempty"`    // nanoseconds
	EvalDuration    int64     `json:"eval_duration,omitempty"`     // nanoseconds
}

// Message is a single message in an Ollama conversation.
// It mirrors Ollama's chat message format.
type Message struct {
	Role      string     `json:"role"`                 // "user", "assistant", "system", "tool"
	Content   string     `json:"content"`              // message text
	ToolCalls []ToolCall `json:"tool_calls,omitempty"` // assistant tool invocations
	ToolName  string     `json:"tool_name,omitempty"`  // only for role="tool": name of the called tool
}

// ToolCall is an Ollama tool call within an assistant message.
type ToolCall struct {
	Type     string       `json:"type,omitempty"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall is the function details within a tool call.
type FunctionCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// ── Conversion ──

// Convert transforms an Ollama native request into an aisync IngestRequest.
// It handles:
//   - Mapping user/assistant/system messages with their roles
//   - Extracting tool_calls from assistant messages
//   - Matching role="tool" messages back to their parent tool call outputs
//   - Setting token counts from prompt_eval_count and eval_count
//   - Setting model on each assistant message
//   - Auto-setting provider to "ollama"
func Convert(req Request) (service.IngestRequest, error) {
	if len(req.Conversation) == 0 {
		return service.IngestRequest{}, fmt.Errorf("conversation must contain at least one message")
	}

	var messages []service.IngestMessage

	// pendingToolCalls tracks tool calls from the most recent assistant message
	// so we can match tool results back to them.
	// Key: tool name → index in the parent message's ToolCalls slice.
	type toolRef struct {
		msgIdx int // index into messages slice
		tcIdx  int // index into ToolCalls slice of that message
	}
	pendingTools := make(map[string][]toolRef)

	for _, m := range req.Conversation {
		switch m.Role {
		case "user", "system":
			messages = append(messages, service.IngestMessage{
				Role:    m.Role,
				Content: m.Content,
			})

		case "assistant":
			msg := service.IngestMessage{
				Role:    "assistant",
				Content: m.Content,
				Model:   req.Model,
			}

			// Clear pending tool calls for a new assistant turn.
			pendingTools = make(map[string][]toolRef)

			for _, tc := range m.ToolCalls {
				input, err := argumentsToString(tc.Function.Arguments)
				if err != nil {
					return service.IngestRequest{}, fmt.Errorf("serializing tool arguments for %q: %w", tc.Function.Name, err)
				}
				tcIdx := len(msg.ToolCalls)
				msg.ToolCalls = append(msg.ToolCalls, service.IngestToolCall{
					Name:  tc.Function.Name,
					Input: input,
				})
				// Track this tool call so we can match tool results later.
				msgIdx := len(messages) // this message's future index
				pendingTools[tc.Function.Name] = append(pendingTools[tc.Function.Name], toolRef{
					msgIdx: msgIdx,
					tcIdx:  tcIdx,
				})
			}

			messages = append(messages, msg)

		case "tool":
			// Match tool result back to the pending tool call.
			name := m.ToolName
			if name == "" {
				// If tool_name is not set, we can't match — skip gracefully.
				continue
			}

			refs, ok := pendingTools[name]
			if !ok || len(refs) == 0 {
				// No pending tool call with this name — treat as an orphan.
				// Add it as a standalone message so data isn't lost.
				messages = append(messages, service.IngestMessage{
					Role:    "tool",
					Content: m.Content,
				})
				continue
			}

			// Pop the first pending ref for this tool name.
			ref := refs[0]
			pendingTools[name] = refs[1:]
			if len(pendingTools[name]) == 0 {
				delete(pendingTools, name)
			}

			// Set the output on the matched tool call.
			messages[ref.msgIdx].ToolCalls[ref.tcIdx].Output = m.Content

		default:
			// Unknown role — pass through as-is.
			messages = append(messages, service.IngestMessage{
				Role:    m.Role,
				Content: m.Content,
			})
		}
	}

	// Distribute tokens: set on the first assistant message (or last if only user messages).
	for i := range messages {
		if messages[i].Role == "assistant" {
			messages[i].InputTokens = req.PromptEvalCount
			messages[i].OutputTokens = req.EvalCount
			break
		}
	}

	return service.IngestRequest{
		Provider:    "ollama",
		Messages:    messages,
		Agent:       req.Agent,
		ProjectPath: req.ProjectPath,
		Branch:      req.Branch,
		Summary:     req.Summary,
		SessionID:   req.SessionID,
	}, nil
}

// DurationNsToMs converts nanoseconds to milliseconds.
func DurationNsToMs(ns int64) int {
	return int(ns / 1_000_000)
}

// argumentsToString serializes a map of arguments to a JSON string.
// If the map is nil or empty, returns an empty string.
func argumentsToString(args map[string]any) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	data, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
