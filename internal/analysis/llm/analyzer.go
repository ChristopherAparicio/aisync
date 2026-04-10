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

// SystemPrompt is the system instruction for the analysis LLM call.
// Exported so other adapters (anthropic, ollama) can reuse the same prompt
// to ensure consistent output format across all analysis backends.
const SystemPrompt = `You are a senior AI coding session analyst. You receive two types of data:

1. RAW SESSION DATA: Message samples, tool call breakdowns, file changes, capabilities.
2. DETERMINISTIC DIAGNOSTIC REPORT: Pre-computed by aisync's diagnostic pipeline with threshold-based
   problem detection. This includes token economy stats, image costs, compaction rates, tool error
   patterns, and named problems with quantified impact.

Your job is to SYNTHESIZE both data sources into an actionable analysis:
- Use the diagnostic report as grounding facts — do NOT contradict its numbers.
- Add context the deterministic pipeline cannot: WHY patterns occurred, whether the user's intent
  was achieved despite waste, and what workflow changes would prevent recurrence.
- For each detected problem, provide a concrete, provider-aware recommendation.
- Identify problems the deterministic pipeline may have missed (e.g., semantic issues like
  the agent misunderstanding requirements, or strategic issues like wrong decomposition approach).

Your response must be a valid JSON object with these fields:

{
  "score": <integer 0-100>,
  "summary": "<one-paragraph assessment that references key diagnostic findings>",
  "problems": [
    {
      "severity": "<low|medium|high>",
      "description": "<what went wrong — reference diagnostic problem IDs when applicable>",
      "message_start": <optional 1-based message index>,
      "message_end": <optional 1-based message index>,
      "tool_name": "<optional tool name>"
    }
  ],
  "recommendations": [
    {
      "category": "<skill|config|workflow|tool>",
      "title": "<short heading>",
      "description": "<detailed, concrete explanation — include specific commands, config changes, or skill content>",
      "priority": <1-5, 1=highest>
    }
  ],
  "skill_suggestions": [
    {
      "name": "<proposed skill identifier>",
      "description": "<what it would do>",
      "trigger": "<when to activate>",
      "content": "<draft skill content or agent instructions that would prevent the detected problems>"
    }
  ]
}

Scoring guidelines (adjusted by diagnostic findings):
- 80-100: Excellent. No high-severity diagnostic problems. Minimal wasted tokens.
- 60-79: Good. Only low-severity diagnostic problems. Minor inefficiencies.
- 40-59: Fair. Medium-severity problems detected. Noticeable token waste.
- 20-39: Poor. High-severity problems (e.g., expensive-screenshots, tool-error-loops). Significant waste.
- 0-19: Very poor. Multiple high-severity problems. Most tokens wasted.

Recommendation priorities:
- Priority 1: Addresses high-severity diagnostic problems (e.g., $80+ image waste).
- Priority 2: Addresses medium-severity problems or multiple low-severity ones.
- Priority 3: Workflow improvements not captured by diagnostics.
- Priority 4-5: Nice-to-have optimizations.

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
		SystemPrompt: SystemPrompt,
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

	// ── Deterministic diagnostic results ──
	// Pre-computed by the diagnostic pipeline (internal/diagnostic). These are
	// factual, threshold-based findings that the LLM should use as grounding
	// data for its analysis — not re-derive from raw messages.
	if d := req.Diagnostic; d != nil {
		b.WriteString("=== DETERMINISTIC DIAGNOSTIC REPORT ===\n")
		b.WriteString("(Pre-computed by aisync's diagnostic pipeline. Use these as grounding facts.)\n\n")

		// Token economy.
		b.WriteString("Token economy:\n")
		b.WriteString(fmt.Sprintf("  Input: %d  Output: %d  Image: %d\n", d.InputTokens, d.OutputTokens, d.ImageTokens))
		b.WriteString(fmt.Sprintf("  Cache read: %.1f%%  I/O ratio: %.1f:1\n", d.CacheReadPct, d.InputOutputRatio))
		if d.EstimatedCost > 0 {
			b.WriteString(fmt.Sprintf("  Estimated cost: $%.2f\n", d.EstimatedCost))
		}
		b.WriteString("\n")

		// Images (only if present).
		if d.ToolReadImages > 0 || d.InlineImages > 0 {
			b.WriteString("Image analysis:\n")
			b.WriteString(fmt.Sprintf("  Inline images: %d  Tool-read images: %d\n", d.InlineImages, d.ToolReadImages))
			if d.SimctlCaptures > 0 {
				b.WriteString(fmt.Sprintf("  Simulator captures: %d  Sips resizes: %d\n", d.SimctlCaptures, d.SipsResizes))
			}
			if d.ImageBilledTok > 0 {
				b.WriteString(fmt.Sprintf("  Billed image tokens: %d  Est. image cost: $%.2f\n", d.ImageBilledTok, d.ImageCost))
				b.WriteString(fmt.Sprintf("  Avg turns in context before compaction: %.1f\n", d.AvgTurnsInCtx))
			}
			b.WriteString("\n")
		}

		// Compaction.
		if d.CompactionCount > 0 {
			b.WriteString("Compaction analysis:\n")
			b.WriteString(fmt.Sprintf("  Compactions: %d  Cascades: %d\n", d.CompactionCount, d.CascadeCount))
			b.WriteString(fmt.Sprintf("  Rate: %.3f per user message\n", d.CompactionsPerUser))
			if d.MedianInterval > 0 {
				b.WriteString(fmt.Sprintf("  Median interval: %d messages\n", d.MedianInterval))
			}
			if d.AvgBeforeTokens > 0 {
				b.WriteString(fmt.Sprintf("  Avg tokens before compaction: %d\n", d.AvgBeforeTokens))
			}
			b.WriteString("\n")
		}

		// Tool errors.
		if d.TotalToolCalls > 0 {
			b.WriteString("Tool error analysis:\n")
			b.WriteString(fmt.Sprintf("  Total tool calls: %d  Errors: %d  Rate: %.1f%%\n",
				d.TotalToolCalls, d.ErrorToolCalls, d.ToolErrorRate))
			if d.MaxConsecErrors > 1 {
				b.WriteString(fmt.Sprintf("  Max consecutive errors: %d\n", d.MaxConsecErrors))
			}
			b.WriteString("\n")
		}

		// Behavioral patterns.
		hasPatterns := d.CorrectionCount > 0 || d.WriteWithoutReadCount > 0 || d.GlobStormCount > 0 || d.LongestUnguided > 5
		if hasPatterns {
			b.WriteString("Behavioral patterns:\n")
			if d.CorrectionCount > 0 {
				b.WriteString(fmt.Sprintf("  User corrections: %d\n", d.CorrectionCount))
			}
			if d.WriteWithoutReadCount > 0 {
				b.WriteString(fmt.Sprintf("  Write-without-read (yolo edits): %d\n", d.WriteWithoutReadCount))
			}
			if d.GlobStormCount > 0 {
				b.WriteString(fmt.Sprintf("  Glob/search storms (>5 consecutive): %d\n", d.GlobStormCount))
			}
			if d.LongestUnguided > 5 {
				b.WriteString(fmt.Sprintf("  Longest unguided assistant run: %d messages\n", d.LongestUnguided))
			}
			b.WriteString("\n")
		}

		// Detected problems — the key section.
		if len(d.Problems) > 0 {
			b.WriteString(fmt.Sprintf("DETECTED PROBLEMS (%d):\n", len(d.Problems)))
			for i, p := range d.Problems {
				b.WriteString(fmt.Sprintf("  %d. [%s/%s] %s\n", i+1, p.Severity, p.Category, p.Title))
				b.WriteString(fmt.Sprintf("     Observation: %s\n", p.Observation))
				if p.Impact != "" {
					b.WriteString(fmt.Sprintf("     Impact: %s\n", p.Impact))
				}
			}
			b.WriteString("\n")
		} else {
			b.WriteString("No deterministic problems detected.\n\n")
		}

		b.WriteString("=== END DIAGNOSTIC REPORT ===\n\n")
	}

	return b.String()
}
