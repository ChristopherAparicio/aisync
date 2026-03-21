// Package llm implements the analysis.Analyzer port using the internal llm.Client.
// It builds a data-rich prompt from session statistics and asks the LLM to produce
// a structured JSON AnalysisReport with problems, recommendations, and skill suggestions.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/llm"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// systemPrompt is the system instruction for the analysis LLM call.
const systemPrompt = `You are a senior AI coding session analyst. Given detailed statistics about an AI coding session
(tool usage, error rates, message patterns, capabilities, MCP servers), produce a structured JSON analysis report.

Your response must be a valid JSON object with these fields:

{
  "score": <integer 0-100>,
  "summary": "<one-paragraph assessment>",
  "problems": [
    {
      "severity": "<low|medium|high>",
      "description": "<what went wrong>",
      "message_start": <optional 1-based message index>,
      "message_end": <optional 1-based message index>,
      "tool_name": "<optional tool name>"
    }
  ],
  "recommendations": [
    {
      "category": "<skill|config|workflow|tool>",
      "title": "<short heading>",
      "description": "<detailed explanation>",
      "priority": <1-5, 1=highest>
    }
  ],
  "skill_suggestions": [
    {
      "name": "<proposed skill identifier>",
      "description": "<what it would do>",
      "trigger": "<when to activate>",
      "content": "<optional draft content>"
    }
  ]
}

Scoring guidelines:
- 80-100: Excellent. Minimal wasted tokens, focused tool usage, clean conversation flow.
- 60-79: Good. Minor inefficiencies but generally well-structured.
- 40-59: Fair. Noticeable waste — retry loops, excessive reads, or bloated contexts.
- 20-39: Poor. Significant token waste from retries, hallucination recovery, or unfocused exploration.
- 0-19: Very poor. Most tokens wasted on failed attempts or circular conversation.

Focus on actionable findings:
- Identify retry loops, repeated file reads, unused tool calls, error cascades
- Suggest skills that could automate repetitive patterns
- Recommend configuration changes (e.g., adjusting context size, enabling caching)
- Flag workflow improvements (e.g., breaking tasks into smaller commits)

Respond ONLY with valid JSON, no markdown fences, no explanation.`

// Analyzer implements analysis.Analyzer using the internal llm.Client port.
type Analyzer struct {
	client llm.Client
	model  string // optional model override
}

// AnalyzerConfig configures the LLM analyzer.
type AnalyzerConfig struct {
	// Client is the LLM client to use for completions (required).
	Client llm.Client

	// Model is an optional model override (e.g., "sonnet", "opus").
	// Empty means the adapter picks its default.
	Model string
}

// NewAnalyzer creates a new LLM-based analyzer.
func NewAnalyzer(cfg AnalyzerConfig) *Analyzer {
	return &Analyzer{
		client: cfg.Client,
		model:  cfg.Model,
	}
}

// Name returns the adapter identifier.
func (a *Analyzer) Name() analysis.AdapterName {
	return analysis.AdapterLLM
}

// Analyze examines a session and returns an analysis report.
func (a *Analyzer) Analyze(ctx context.Context, req analysis.AnalyzeRequest) (*analysis.AnalysisReport, error) {
	if a.client == nil {
		return nil, fmt.Errorf("LLM client is nil")
	}

	if len(req.Session.Messages) == 0 {
		return nil, fmt.Errorf("session has no messages to analyze")
	}

	prompt := BuildAnalysisPrompt(req)

	model := a.model
	resp, err := a.client.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: systemPrompt,
		UserPrompt:   prompt,
		Model:        model,
		MaxTokens:    4096,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM analysis completion: %w", err)
	}

	var report analysis.AnalysisReport
	if jsonErr := json.Unmarshal([]byte(resp.Content), &report); jsonErr != nil {
		return nil, fmt.Errorf("parsing LLM analysis response: %w (raw: %.200s)", jsonErr, resp.Content)
	}

	// Clamp score to valid range.
	if report.Score < 0 {
		report.Score = 0
	}
	if report.Score > 100 {
		report.Score = 100
	}

	if err := report.Validate(); err != nil {
		return nil, fmt.Errorf("LLM produced invalid report: %w", err)
	}

	return &report, nil
}

// BuildAnalysisPrompt constructs a data-rich prompt for session analysis.
// It is exported so other adapters (e.g., opencode) can reuse the same prompt format.
func BuildAnalysisPrompt(req analysis.AnalyzeRequest) string {
	sess := &req.Session
	var b strings.Builder

	// ── Header ──
	b.WriteString(fmt.Sprintf("Session: %s\n", sess.ID))
	b.WriteString(fmt.Sprintf("Provider: %s\n", sess.Provider))
	if sess.Agent != "" {
		b.WriteString(fmt.Sprintf("Agent: %s\n", sess.Agent))
	}
	if sess.Branch != "" {
		b.WriteString(fmt.Sprintf("Branch: %s\n", sess.Branch))
	}
	b.WriteString(fmt.Sprintf("Messages: %d\n", len(sess.Messages)))
	b.WriteString(fmt.Sprintf("Tokens: input=%d output=%d total=%d\n",
		sess.TokenUsage.InputTokens, sess.TokenUsage.OutputTokens, sess.TokenUsage.TotalTokens))
	b.WriteString("\n")

	// ── Analysis trigger context ──
	if req.ErrorThreshold > 0 {
		b.WriteString(fmt.Sprintf("Analysis triggered because error rate exceeded %.0f%% threshold.\n", req.ErrorThreshold))
	}
	b.WriteString("\n")

	// ── Message role distribution ──
	roleCounts := make(map[session.MessageRole]int)
	var totalToolCalls, errorToolCalls int
	for i := range sess.Messages {
		msg := &sess.Messages[i]
		roleCounts[msg.Role]++
		for j := range msg.ToolCalls {
			totalToolCalls++
			if msg.ToolCalls[j].State == session.ToolStateError {
				errorToolCalls++
			}
		}
	}
	b.WriteString("Message distribution:\n")
	for role, count := range roleCounts {
		b.WriteString(fmt.Sprintf("  %s: %d\n", role, count))
	}
	b.WriteString("\n")

	// ── Tool call breakdown ──
	if totalToolCalls > 0 {
		errorRate := float64(0)
		if totalToolCalls > 0 {
			errorRate = float64(errorToolCalls) / float64(totalToolCalls) * 100
		}
		b.WriteString(fmt.Sprintf("Tool calls: %d total, %d errors (%.1f%% error rate)\n",
			totalToolCalls, errorToolCalls, errorRate))

		type toolAgg struct {
			calls, errors, inputTok, outputTok, totalDur int
		}
		perTool := make(map[string]*toolAgg)
		for i := range sess.Messages {
			for j := range sess.Messages[i].ToolCalls {
				tc := &sess.Messages[i].ToolCalls[j]
				agg, ok := perTool[tc.Name]
				if !ok {
					agg = &toolAgg{}
					perTool[tc.Name] = agg
				}
				agg.calls++
				agg.inputTok += tc.InputTokens
				agg.outputTok += tc.OutputTokens
				agg.totalDur += tc.DurationMs
				if tc.State == session.ToolStateError {
					agg.errors++
				}
			}
		}

		b.WriteString("\nPer-tool breakdown:\n")
		for name, agg := range perTool {
			b.WriteString(fmt.Sprintf("  %s: calls=%d tokens=%d errors=%d",
				name, agg.calls, agg.inputTok+agg.outputTok, agg.errors))
			if agg.calls > 0 && agg.totalDur > 0 {
				b.WriteString(fmt.Sprintf(" avg_duration=%dms", agg.totalDur/agg.calls))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// ── Intent: first 5 user messages (what the user wanted) ──
	b.WriteString("First user messages (intent):\n")
	userCount := 0
	for i := range sess.Messages {
		if sess.Messages[i].Role == session.RoleUser && sess.Messages[i].Content != "" {
			content := sess.Messages[i].Content
			if len(content) > 300 {
				content = content[:297] + "..."
			}
			b.WriteString(fmt.Sprintf("  [msg#%d] %s\n", i, content))
			userCount++
			if userCount >= 5 {
				break
			}
		}
	}
	b.WriteString("\n")

	// ── Outcome: last 10 messages summary (what was accomplished) ──
	b.WriteString("Last 10 messages (outcome):\n")
	start := 0
	if len(sess.Messages) > 10 {
		start = len(sess.Messages) - 10
	}
	for i := start; i < len(sess.Messages); i++ {
		msg := &sess.Messages[i]
		toolCount := len(msg.ToolCalls)
		content := msg.Content
		if len(content) > 200 {
			content = content[:197] + "..."
		}
		b.WriteString(fmt.Sprintf("  [%d] %s tools=%d: %s\n",
			i+1, msg.Role, toolCount, content))
	}
	b.WriteString("\n")

	// ── Error messages (if any) — up to 5 ──
	errorMsgCount := 0
	for i := range sess.Messages {
		for j := range sess.Messages[i].ToolCalls {
			tc := &sess.Messages[i].ToolCalls[j]
			if tc.State == session.ToolStateError && errorMsgCount < 5 {
				if errorMsgCount == 0 {
					b.WriteString("Sample error messages:\n")
				}
				output := tc.Output
				if len(output) > 200 {
					output = output[:197] + "..."
				}
				b.WriteString(fmt.Sprintf("  [msg#%d] %s: %s\n", i, tc.Name, output))
				errorMsgCount++
			}
		}
	}
	if errorMsgCount > 0 {
		b.WriteString("\n")
	}

	// ── File changes ──
	if len(sess.FileChanges) > 0 {
		b.WriteString(fmt.Sprintf("Files changed: %d\n", len(sess.FileChanges)))
		limit := len(sess.FileChanges)
		if limit > 20 {
			limit = 20
		}
		for _, fc := range sess.FileChanges[:limit] {
			b.WriteString(fmt.Sprintf("  %s (%s)\n", fc.FilePath, fc.ChangeType))
		}
		if len(sess.FileChanges) > 20 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(sess.FileChanges)-20))
		}
		b.WriteString("\n")
	}

	// ── Available capabilities ──
	if len(req.Capabilities) > 0 {
		b.WriteString("Available project capabilities:\n")
		for _, cap := range req.Capabilities {
			b.WriteString(fmt.Sprintf("  [%s] %s", cap.Kind, cap.Name))
			if cap.Description != "" {
				b.WriteString(fmt.Sprintf(": %s", cap.Description))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// ── MCP Servers ──
	if len(req.MCPServers) > 0 {
		b.WriteString("Configured MCP servers:\n")
		for _, srv := range req.MCPServers {
			status := "enabled"
			if !srv.Enabled {
				status = "disabled"
			}
			b.WriteString(fmt.Sprintf("  %s (%s, %s)\n", srv.Name, srv.Type, status))
		}
		b.WriteString("\n")
	}

	return b.String()
}
