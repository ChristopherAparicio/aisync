package analysis

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// SessionToolExecutor implements ToolExecutor by reading directly from a
// pre-loaded session. This avoids repeated Store queries during the agentic
// loop and ensures the LLM can only access data for one session.
//
// This lives in the analysis package (not the anthropic adapter) so the
// service layer can construct it without importing the adapter package.
type SessionToolExecutor struct {
	sess *session.Session
}

// NewSessionToolExecutor creates a ToolExecutor scoped to the given session.
func NewSessionToolExecutor(sess *session.Session) *SessionToolExecutor {
	return &SessionToolExecutor{sess: sess}
}

// ── Tool implementations ──

// GetMessages returns messages within the given index range [from, to] inclusive.
func (e *SessionToolExecutor) GetMessages(from, to int) (json.RawMessage, error) {
	msgs := e.sess.Messages
	if len(msgs) == 0 {
		return json.Marshal([]toolMessageResult{})
	}

	// Clamp bounds.
	if from < 0 {
		from = 0
	}
	if to >= len(msgs) {
		to = len(msgs) - 1
	}
	if from > to {
		return json.Marshal([]toolMessageResult{})
	}

	var results []toolMessageResult
	for i := from; i <= to; i++ {
		m := msgs[i]
		content := m.Content
		// Truncate very long messages to avoid blowing up the context.
		if len(content) > maxToolContentLen {
			content = content[:maxToolContentLen] + fmt.Sprintf("... [truncated, %d total chars]", len(m.Content))
		}
		results = append(results, toolMessageResult{
			Index:        i,
			Role:         string(m.Role),
			Content:      content,
			Model:        m.Model,
			ToolCalls:    len(m.ToolCalls),
			InputTokens:  m.InputTokens,
			OutputTokens: m.OutputTokens,
		})
	}

	return json.Marshal(results)
}

// GetToolCalls returns tool calls matching the optional filter.
func (e *SessionToolExecutor) GetToolCalls(filter ToolCallFilter) (json.RawMessage, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultToolCallLimit
	}

	var results []toolCallResult
	for i, msg := range e.sess.Messages {
		for _, tc := range msg.ToolCalls {
			// Apply filters.
			if filter.Name != "" && !strings.Contains(strings.ToLower(tc.Name), strings.ToLower(filter.Name)) {
				continue
			}
			if filter.State != "" && string(tc.State) != filter.State {
				continue
			}

			input := tc.Input
			if len(input) > maxToolInputLen {
				input = input[:maxToolInputLen] + "... [truncated]"
			}
			output := tc.Output
			if len(output) > maxToolOutputLen {
				output = output[:maxToolOutputLen] + "... [truncated]"
			}

			results = append(results, toolCallResult{
				MessageIndex: i,
				ID:           tc.ID,
				Name:         tc.Name,
				State:        string(tc.State),
				Input:        input,
				Output:       output,
				DurationMs:   tc.DurationMs,
			})

			if len(results) >= limit {
				return json.Marshal(results)
			}
		}
	}

	return json.Marshal(results)
}

// SearchMessages searches message content for a pattern.
func (e *SessionToolExecutor) SearchMessages(pattern string, limit int) (json.RawMessage, error) {
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if pattern == "" {
		return json.Marshal([]toolSearchResult{})
	}

	lowerPattern := strings.ToLower(pattern)
	var results []toolSearchResult

	for i, msg := range e.sess.Messages {
		if strings.Contains(strings.ToLower(msg.Content), lowerPattern) {
			content := msg.Content
			if len(content) > maxToolContentLen {
				content = content[:maxToolContentLen] + fmt.Sprintf("... [truncated, %d total chars]", len(msg.Content))
			}
			results = append(results, toolSearchResult{
				Index:   i,
				Role:    string(msg.Role),
				Content: content,
			})
			if len(results) >= limit {
				break
			}
		}
	}

	return json.Marshal(results)
}

// GetCompactionDetails returns detailed compaction analysis.
func (e *SessionToolExecutor) GetCompactionDetails() (json.RawMessage, error) {
	summary := session.DetectCompactions(e.sess.Messages, 0.000015) // $15/M input rate

	var events []toolCompactionEvent
	for _, evt := range summary.Events {
		events = append(events, toolCompactionEvent{
			BeforeMessageIdx:  evt.BeforeMessageIdx,
			AfterMessageIdx:   evt.AfterMessageIdx,
			BeforeInputTokens: evt.BeforeInputTokens,
			AfterInputTokens:  evt.AfterInputTokens,
			TokensLost:        evt.TokensLost,
			DropPercent:       evt.DropPercent,
			CacheInvalidated:  evt.CacheInvalidated,
			IsCascade:         evt.IsCascade,
			MergedLegs:        evt.MergedLegs,
			RebuildCost:       evt.RebuildCost,
		})
	}

	result := toolCompactionResult{
		TotalCompactions:      summary.TotalCompactions,
		CascadeCount:          summary.CascadeCount,
		TotalTokensLost:       summary.TotalTokensLost,
		AvgDropPercent:        summary.AvgDropPercent,
		MedianDropPercent:     summary.MedianDropPercent,
		CompactionsPerUserMsg: summary.CompactionsPerUserMessage,
		SawtoothCycles:        summary.SawtoothCycles,
		AvgMessagesToFill:     summary.AvgMessagesToFill,
		AvgRecoveryTokens:     summary.AvgRecoveryTokens,
		TotalRebuildCost:      summary.TotalRebuildCost,
		LastQuartileRate:      summary.LastQuartileCompactionRate,
		DetectionCoverage:     summary.DetectionCoverage,
		MessagesWithTokenData: summary.MessagesWithTokenData,
		Events:                events,
	}

	return json.Marshal(result)
}

// GetErrorDetails returns detailed information about tool call errors.
func (e *SessionToolExecutor) GetErrorDetails(limit int) (json.RawMessage, error) {
	if limit <= 0 {
		limit = defaultErrorLimit
	}

	var results []toolErrorDetail
	for i, msg := range e.sess.Messages {
		for _, tc := range msg.ToolCalls {
			if tc.State != session.ToolStateError {
				continue
			}

			input := tc.Input
			if len(input) > maxToolInputLen {
				input = input[:maxToolInputLen] + "... [truncated]"
			}
			output := tc.Output
			if len(output) > maxToolOutputLen {
				output = output[:maxToolOutputLen] + "... [truncated]"
			}

			results = append(results, toolErrorDetail{
				MessageIndex: i,
				ToolID:       tc.ID,
				ToolName:     tc.Name,
				Input:        input,
				ErrorOutput:  output,
				DurationMs:   tc.DurationMs,
			})

			if len(results) >= limit {
				return json.Marshal(results)
			}
		}
	}

	return json.Marshal(results)
}

// GetTokenTimeline returns per-message token usage over the session lifetime.
func (e *SessionToolExecutor) GetTokenTimeline() (json.RawMessage, error) {
	var timeline []toolTokenEntry

	for i, msg := range e.sess.Messages {
		// Only include messages with token data to avoid noise.
		if msg.InputTokens == 0 && msg.OutputTokens == 0 {
			continue
		}
		timeline = append(timeline, toolTokenEntry{
			Index:            i,
			Role:             string(msg.Role),
			InputTokens:      msg.InputTokens,
			OutputTokens:     msg.OutputTokens,
			CacheReadTokens:  msg.CacheReadTokens,
			CacheWriteTokens: msg.CacheWriteTokens,
			ToolCallCount:    len(msg.ToolCalls),
		})
	}

	return json.Marshal(timeline)
}

// ── Constants ──

const (
	maxToolContentLen    = 2000 // max chars per message content in tool results
	maxToolInputLen      = 1000 // max chars for tool call input
	maxToolOutputLen     = 1000 // max chars for tool call output
	defaultToolCallLimit = 50
	defaultSearchLimit   = 20
	defaultErrorLimit    = 20
)

// ── Result types (JSON-serializable, prefixed with "tool" to avoid conflicts) ──

type toolMessageResult struct {
	Index        int    `json:"index"`
	Role         string `json:"role"`
	Content      string `json:"content"`
	Model        string `json:"model,omitempty"`
	ToolCalls    int    `json:"tool_calls"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
}

type toolCallResult struct {
	MessageIndex int    `json:"message_index"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	State        string `json:"state"`
	Input        string `json:"input"`
	Output       string `json:"output,omitempty"`
	DurationMs   int    `json:"duration_ms,omitempty"`
}

type toolSearchResult struct {
	Index   int    `json:"index"`
	Role    string `json:"role"`
	Content string `json:"content"`
}

type toolCompactionResult struct {
	TotalCompactions      int                   `json:"total_compactions"`
	CascadeCount          int                   `json:"cascade_count"`
	TotalTokensLost       int                   `json:"total_tokens_lost"`
	AvgDropPercent        float64               `json:"avg_drop_percent"`
	MedianDropPercent     float64               `json:"median_drop_percent"`
	CompactionsPerUserMsg float64               `json:"compactions_per_user_msg"`
	SawtoothCycles        int                   `json:"sawtooth_cycles"`
	AvgMessagesToFill     int                   `json:"avg_messages_to_fill"`
	AvgRecoveryTokens     int                   `json:"avg_recovery_tokens"`
	TotalRebuildCost      float64               `json:"total_rebuild_cost_usd"`
	LastQuartileRate      float64               `json:"last_quartile_rate"`
	DetectionCoverage     string                `json:"detection_coverage"`
	MessagesWithTokenData int                   `json:"messages_with_token_data"`
	Events                []toolCompactionEvent `json:"events"`
}

type toolCompactionEvent struct {
	BeforeMessageIdx  int     `json:"before_message_idx"`
	AfterMessageIdx   int     `json:"after_message_idx"`
	BeforeInputTokens int     `json:"before_input_tokens"`
	AfterInputTokens  int     `json:"after_input_tokens"`
	TokensLost        int     `json:"tokens_lost"`
	DropPercent       float64 `json:"drop_percent"`
	CacheInvalidated  bool    `json:"cache_invalidated"`
	IsCascade         bool    `json:"is_cascade,omitempty"`
	MergedLegs        int     `json:"merged_legs,omitempty"`
	RebuildCost       float64 `json:"rebuild_cost_usd"`
}

type toolErrorDetail struct {
	MessageIndex int    `json:"message_index"`
	ToolID       string `json:"tool_id"`
	ToolName     string `json:"tool_name"`
	Input        string `json:"input"`
	ErrorOutput  string `json:"error_output"`
	DurationMs   int    `json:"duration_ms,omitempty"`
}

type toolTokenEntry struct {
	Index            int    `json:"index"`
	Role             string `json:"role"`
	InputTokens      int    `json:"input_tokens"`
	OutputTokens     int    `json:"output_tokens"`
	CacheReadTokens  int    `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int    `json:"cache_write_tokens,omitempty"`
	ToolCallCount    int    `json:"tool_call_count,omitempty"`
}
