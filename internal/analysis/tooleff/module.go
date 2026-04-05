// Package tooleff implements the tool_efficiency analysis module.
// It uses an LLM to evaluate each tool call's usefulness in a session.
package tooleff

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/llm"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// Module implements analysis.AnalysisModule for tool efficiency evaluation.
type Module struct {
	client llm.Client
}

// NewModule creates a new tool efficiency module with the given LLM client.
func NewModule(client llm.Client) *Module {
	return &Module{client: client}
}

// Name returns the module identifier.
func (m *Module) Name() analysis.ModuleName {
	return analysis.ModuleToolEfficiency
}

// Analyze evaluates tool call efficiency for the given session.
func (m *Module) Analyze(ctx context.Context, req analysis.ModuleRequest) (*analysis.ModuleResult, error) {
	if m.client == nil {
		return nil, fmt.Errorf("tool efficiency module requires an LLM client")
	}

	calls := collectToolCalls(req.Session)
	if len(calls) == 0 {
		// No tool calls — return a trivial result.
		report := analysis.ToolEfficiencyReport{
			Summary:      "No tool calls in this session.",
			OverallScore: 100,
		}
		payload, _ := json.Marshal(report)
		return &analysis.ModuleResult{
			Module:  analysis.ModuleToolEfficiency,
			Payload: payload,
		}, nil
	}

	prompt := buildToolEfficiencyPrompt(req.Session, calls)

	start := time.Now()
	resp, err := m.client.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: toolEfficiencySystemPrompt,
		UserPrompt:   prompt,
		MaxTokens:    4096,
	})
	durationMs := int(time.Since(start).Milliseconds())
	if durationMs == 0 {
		durationMs = 1 // sub-millisecond calls still took time
	}

	if err != nil {
		return &analysis.ModuleResult{
			Module:     analysis.ModuleToolEfficiency,
			DurationMs: durationMs,
			Error:      fmt.Sprintf("LLM call failed: %v", err),
		}, nil
	}

	var report analysis.ToolEfficiencyReport
	content := cleanJSON(resp.Content)
	if jsonErr := json.Unmarshal([]byte(content), &report); jsonErr != nil {
		return &analysis.ModuleResult{
			Module:     analysis.ModuleToolEfficiency,
			DurationMs: durationMs,
			TokensUsed: resp.InputTokens + resp.OutputTokens,
			Error:      fmt.Sprintf("failed to parse LLM response: %v", jsonErr),
		}, nil
	}

	// Compute aggregate stats.
	for _, eval := range report.ToolEvaluations {
		switch eval.Usefulness {
		case "useful":
			report.UsefulCalls++
		case "redundant", "wasteful":
			report.RedundantCalls++
		}
	}

	// Clamp score.
	if report.OverallScore < 0 {
		report.OverallScore = 0
	}
	if report.OverallScore > 100 {
		report.OverallScore = 100
	}

	payload, _ := json.Marshal(report)
	return &analysis.ModuleResult{
		Module:     analysis.ModuleToolEfficiency,
		Payload:    payload,
		TokensUsed: resp.InputTokens + resp.OutputTokens,
		DurationMs: durationMs,
	}, nil
}

// ── Internal ──

// toolCallInfo is a flattened tool call with its message context.
type toolCallInfo struct {
	MessageIndex int
	ToolCall     session.ToolCall
	PrevContent  string // previous message content (for context)
	NextContent  string // next message content (to see if result was used)
}

// collectToolCalls extracts tool calls with surrounding context.
func collectToolCalls(sess session.Session) []toolCallInfo {
	var calls []toolCallInfo
	for i, msg := range sess.Messages {
		for _, tc := range msg.ToolCalls {
			info := toolCallInfo{
				MessageIndex: i,
				ToolCall:     tc,
			}
			if i > 0 {
				info.PrevContent = truncateStr(sess.Messages[i-1].Content, 200)
			}
			if i+1 < len(sess.Messages) {
				info.NextContent = truncateStr(sess.Messages[i+1].Content, 200)
			}
			calls = append(calls, info)
		}
	}
	return calls
}

const toolEfficiencySystemPrompt = `You are an AI session efficiency analyst specializing in tool usage patterns.
Given a sequence of tool calls from an AI coding session with their inputs, outputs, and surrounding conversation context,
evaluate each tool call's efficiency.

For each tool call, judge:
- "useful": The tool returned relevant information that advanced the task
- "partial": The tool returned some useful info but also unnecessary data
- "redundant": The same information was already available or the call was unnecessary
- "wasteful": The call failed, returned irrelevant results, or was a retry of a previous failed attempt

Also identify patterns like:
- Retry loops (same tool called repeatedly after errors)
- Redundant reads (reading the same file multiple times)
- Unnecessary exploration (reading files that aren't used)
- Over-fetching (reading entire files when only a section was needed)

Respond ONLY with valid JSON matching this structure:
{
  "tool_evaluations": [{"index": 0, "tool_name": "read", "usefulness": "useful", "reason": "Read config file needed for implementation"}],
  "summary": "Brief overall assessment",
  "overall_score": 75,
  "patterns": ["pattern description"]
}

Keep evaluations concise (1 sentence each). Focus on the most impactful calls.
If there are more than 30 tool calls, evaluate only the most significant ones (errors, large outputs, repeated calls).
Respond ONLY with valid JSON, no markdown fences.`

// buildToolEfficiencyPrompt builds the user prompt with tool call data.
func buildToolEfficiencyPrompt(sess session.Session, calls []toolCallInfo) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Session: %s (%d messages, %d tool calls)\n", sess.ID, len(sess.Messages), len(calls)))
	if sess.Summary != "" {
		b.WriteString(fmt.Sprintf("Summary: %s\n", sess.Summary))
	}
	b.WriteString("\n")

	// Limit to 40 tool calls to avoid blowing up context.
	limit := len(calls)
	if limit > 40 {
		limit = 40
	}

	b.WriteString("Tool calls:\n")
	for i := 0; i < limit; i++ {
		tc := calls[i]
		b.WriteString(fmt.Sprintf("\n[%d] msg=%d tool=%s state=%s",
			i, tc.MessageIndex, tc.ToolCall.Name, tc.ToolCall.State))
		if tc.ToolCall.DurationMs > 0 {
			b.WriteString(fmt.Sprintf(" duration=%dms", tc.ToolCall.DurationMs))
		}
		if tc.ToolCall.InputTokens > 0 || tc.ToolCall.OutputTokens > 0 {
			b.WriteString(fmt.Sprintf(" tokens=%din/%dout", tc.ToolCall.InputTokens, tc.ToolCall.OutputTokens))
		}
		b.WriteString("\n")

		if tc.ToolCall.Input != "" {
			b.WriteString(fmt.Sprintf("  Input: %s\n", truncateStr(tc.ToolCall.Input, 300)))
		}
		if tc.ToolCall.Output != "" {
			b.WriteString(fmt.Sprintf("  Output: %s\n", truncateStr(tc.ToolCall.Output, 300)))
		}
		if tc.PrevContent != "" {
			b.WriteString(fmt.Sprintf("  Context before: %s\n", tc.PrevContent))
		}
		if tc.NextContent != "" {
			b.WriteString(fmt.Sprintf("  Context after: %s\n", tc.NextContent))
		}
	}

	if len(calls) > limit {
		b.WriteString(fmt.Sprintf("\n... and %d more tool calls (omitted for brevity)\n", len(calls)-limit))
	}

	return b.String()
}

// cleanJSON strips markdown code fences from LLM responses.
func cleanJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
	}
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
	}
	return strings.TrimSpace(s)
}

// truncateStr truncates a string to maxLen, appending "..." if truncated.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
