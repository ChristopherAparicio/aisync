package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/llm"
	"github.com/ChristopherAparicio/aisync/internal/pricing"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Summarize ──

// summarizeSystemPrompt is the system instruction for AI session summarization.
const summarizeSystemPrompt = `You are a technical session analyzer. Given an AI coding session transcript,
produce a structured JSON summary with these fields:
- intent: What the user was trying to accomplish (1 sentence)
- outcome: What was actually achieved (1 sentence)
- decisions: Key technical decisions made (array of short strings)
- friction: Problems or difficulties encountered (array of short strings)
- open_items: Things left unfinished or needing follow-up (array of short strings)

Respond ONLY with valid JSON, no markdown fences, no explanation.`

// SummarizeRequest contains inputs for summarizing a session.
type SummarizeRequest struct {
	Session *session.Session // the session to summarize
	Model   string           // optional — override default model
}

// SummarizeResult contains the AI-generated summary.
type SummarizeResult struct {
	Summary    session.StructuredSummary
	OneLine    string // compact "Intent: Outcome" string
	Model      string // model that produced the summary
	TokensUsed int    // total tokens consumed
}

// Summarize generates an AI-powered structured summary for a session.
// Returns an error if LLM is not configured or the LLM call fails.
func (s *SessionService) Summarize(ctx context.Context, req SummarizeRequest) (*SummarizeResult, error) {
	if s.llm == nil {
		return nil, fmt.Errorf("AI summarization requires an LLM client (set summarize.enabled or use --summarize)")
	}
	if req.Session == nil {
		return nil, fmt.Errorf("session is required for summarization")
	}

	userPrompt := buildSessionTranscript(req.Session)
	if userPrompt == "" {
		return nil, fmt.Errorf("session has no messages to summarize")
	}

	resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: summarizeSystemPrompt,
		UserPrompt:   userPrompt,
		Model:        req.Model,
		MaxTokens:    1024,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM summarize: %w", err)
	}

	var summary session.StructuredSummary
	if jsonErr := json.Unmarshal([]byte(resp.Content), &summary); jsonErr != nil {
		return nil, fmt.Errorf("parsing LLM summary response: %w (raw: %s)", jsonErr, truncate(resp.Content, 200))
	}

	return &SummarizeResult{
		Summary:    summary,
		OneLine:    summary.OneLine(),
		Model:      resp.Model,
		TokensUsed: resp.InputTokens + resp.OutputTokens,
	}, nil
}

// ── Explain ──

// explainSystemPrompt is the system instruction for AI session explanation.
const explainSystemPrompt = `You are a technical analyst. Given an AI coding session transcript,
write a clear explanation of what happened during this session.
Cover: what was the goal, what approach was taken, what files were changed,
what decisions were made and why, and what the outcome was.
Write for a developer who is taking over this branch.`

// explainShortSystemPrompt produces a brief explanation.
const explainShortSystemPrompt = `You are a technical analyst. Given an AI coding session transcript,
write a brief 2-3 sentence summary of what happened. Focus on the goal and outcome.`

// ExplainRequest contains inputs for explaining a session.
type ExplainRequest struct {
	SessionID session.ID
	Model     string // optional — override default model
	Short     bool   // if true, produce a brief explanation
}

// ExplainResult contains the AI-generated explanation.
type ExplainResult struct {
	Explanation string
	SessionID   session.ID
	Model       string
	TokensUsed  int
}

// Explain generates an AI-powered natural language explanation of a session.
// The result is ephemeral — it is NOT stored.
func (s *SessionService) Explain(ctx context.Context, req ExplainRequest) (*ExplainResult, error) {
	if s.llm == nil {
		return nil, fmt.Errorf("AI explanation requires an LLM client")
	}

	sess, err := s.store.Get(req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("loading session: %w", err)
	}

	userPrompt := buildSessionTranscript(sess)
	if userPrompt == "" {
		return nil, fmt.Errorf("session has no messages to explain")
	}

	systemPrompt := explainSystemPrompt
	if req.Short {
		systemPrompt = explainShortSystemPrompt
	}

	resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		Model:        req.Model,
		MaxTokens:    4096,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM explain: %w", err)
	}

	return &ExplainResult{
		Explanation: resp.Content,
		SessionID:   sess.ID,
		Model:       resp.Model,
		TokensUsed:  resp.InputTokens + resp.OutputTokens,
	}, nil
}

// ── AnalyzeEfficiency ──

// efficiencySystemPrompt is the system instruction for AI efficiency analysis.
const efficiencySystemPrompt = `You are a senior AI coding efficiency analyst. Given statistics about an AI coding session
(token usage, tool call breakdown, message patterns, timing), produce a structured JSON efficiency report with these fields:
- score: integer 0-100 (100 = perfectly efficient, 0 = extremely wasteful)
- summary: one-paragraph assessment of the session's efficiency
- strengths: array of short strings describing what went well
- issues: array of short strings describing inefficiencies found
- suggestions: array of actionable improvement recommendations
- patterns: array of detected anti-patterns (e.g., "retry loops", "over-reading", "redundant writes", "large context")

Scoring guidelines:
- 80-100: Excellent. Minimal wasted tokens, focused tool usage, clean conversation flow.
- 60-79: Good. Some minor inefficiencies but generally well-structured.
- 40-59: Fair. Noticeable waste — retry loops, excessive reads, or bloated contexts.
- 20-39: Poor. Significant token waste from retries, hallucination recovery, or unfocused exploration.
- 0-19: Very poor. Most tokens wasted on failed attempts or circular conversation.

Respond ONLY with valid JSON, no markdown fences, no explanation.`

// EfficiencyRequest contains inputs for analyzing session efficiency.
type EfficiencyRequest struct {
	SessionID session.ID // the session to analyze
	Model     string     // optional — override default model
}

// EfficiencyResult contains the AI-generated efficiency report.
type EfficiencyResult struct {
	Report     session.EfficiencyReport
	SessionID  session.ID
	Model      string
	TokensUsed int
}

// AnalyzeEfficiency generates an LLM-powered efficiency analysis for a session.
// It computes tool usage stats, token distribution, and message patterns,
// then asks the LLM to evaluate overall efficiency.
func (s *SessionService) AnalyzeEfficiency(ctx context.Context, req EfficiencyRequest) (*EfficiencyResult, error) {
	if s.llm == nil {
		return nil, fmt.Errorf("efficiency analysis requires an LLM client")
	}

	sess, err := s.store.Get(req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("loading session: %w", err)
	}

	if len(sess.Messages) == 0 {
		return nil, fmt.Errorf("session has no messages to analyze")
	}

	prompt := buildEfficiencyPrompt(sess, s.pricing)

	resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: efficiencySystemPrompt,
		UserPrompt:   prompt,
		Model:        req.Model,
		MaxTokens:    2048,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM efficiency analysis: %w", err)
	}

	var report session.EfficiencyReport
	if jsonErr := json.Unmarshal([]byte(resp.Content), &report); jsonErr != nil {
		return nil, fmt.Errorf("parsing LLM efficiency response: %w (raw: %s)", jsonErr, truncate(resp.Content, 200))
	}

	// Clamp score to valid range.
	if report.Score < 0 {
		report.Score = 0
	}
	if report.Score > 100 {
		report.Score = 100
	}

	return &EfficiencyResult{
		Report:     report,
		SessionID:  sess.ID,
		Model:      resp.Model,
		TokensUsed: resp.InputTokens + resp.OutputTokens,
	}, nil
}

// buildEfficiencyPrompt constructs a data-rich prompt for the LLM efficiency analysis.
// It includes token breakdown, tool call statistics, message patterns, and timing.
func buildEfficiencyPrompt(sess *session.Session, calc *pricing.Calculator) string {
	var b strings.Builder

	// Header
	b.WriteString(fmt.Sprintf("Session: %s\n", sess.ID))
	b.WriteString(fmt.Sprintf("Provider: %s\n", sess.Provider))
	if sess.Branch != "" {
		b.WriteString(fmt.Sprintf("Branch: %s\n", sess.Branch))
	}
	b.WriteString(fmt.Sprintf("Messages: %d\n", len(sess.Messages)))
	b.WriteString(fmt.Sprintf("Tokens: input=%d output=%d total=%d\n",
		sess.TokenUsage.InputTokens, sess.TokenUsage.OutputTokens, sess.TokenUsage.TotalTokens))
	b.WriteString("\n")

	// Cost estimate
	if calc != nil {
		est := calc.SessionCost(sess)
		if est.TotalCost.TotalCost > 0 {
			b.WriteString(fmt.Sprintf("Estimated cost: $%.4f (input=$%.4f output=$%.4f)\n\n",
				est.TotalCost.TotalCost, est.TotalCost.InputCost, est.TotalCost.OutputCost))
		}
	}

	// Message role distribution
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

	// Tool call breakdown
	if totalToolCalls > 0 {
		b.WriteString(fmt.Sprintf("Tool calls: %d total, %d errors (%.0f%% error rate)\n",
			totalToolCalls, errorToolCalls,
			safePercent(errorToolCalls, totalToolCalls)))

		// Per-tool summary
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
				inTok, outTok := tc.InputTokens, tc.OutputTokens
				if inTok == 0 && len(tc.Input) > 0 {
					inTok = estimateTokens(tc.Input)
				}
				if outTok == 0 && len(tc.Output) > 0 {
					outTok = estimateTokens(tc.Output)
				}
				agg.inputTok += inTok
				agg.outputTok += outTok
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

	// Conversation flow patterns (detect potential retries)
	b.WriteString("Conversation flow (last 20 messages):\n")
	start := 0
	if len(sess.Messages) > 20 {
		start = len(sess.Messages) - 20
	}
	for i := start; i < len(sess.Messages); i++ {
		msg := &sess.Messages[i]
		toolCount := len(msg.ToolCalls)
		tokens := msg.InputTokens + msg.OutputTokens
		b.WriteString(fmt.Sprintf("  [%d] %s tokens=%d tools=%d content_len=%d\n",
			i+1, msg.Role, tokens, toolCount, len(msg.Content)))
	}

	// File changes
	if len(sess.FileChanges) > 0 {
		b.WriteString(fmt.Sprintf("\nFiles changed: %d\n", len(sess.FileChanges)))
		limit := len(sess.FileChanges)
		if limit > 15 {
			limit = 15
		}
		for _, fc := range sess.FileChanges[:limit] {
			b.WriteString(fmt.Sprintf("  %s (%s)\n", fc.FilePath, fc.ChangeType))
		}
		if len(sess.FileChanges) > 15 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(sess.FileChanges)-15))
		}
	}

	return b.String()
}

// safePercent computes (part/total)*100, returning 0 if total is 0.
func safePercent(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}

// ── Rewind ──

// RewindRequest contains inputs for rewinding a session.
type RewindRequest struct {
	SessionID session.ID // the session to rewind
	AtMessage int        // truncate at this message index (1-based, inclusive)
}

// RewindResult contains the outcome of a rewind operation.
type RewindResult struct {
	NewSession      *session.Session
	OriginalID      session.ID
	TruncatedAt     int // message index where the session was truncated
	MessagesRemoved int // number of messages discarded
}

// Rewind creates a new session that is a fork of an existing session,
// truncated at the given message index. The original session is never modified.
func (s *SessionService) Rewind(ctx context.Context, req RewindRequest) (*RewindResult, error) {
	original, err := s.store.Get(req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("loading session: %w", err)
	}

	if len(original.Messages) == 0 {
		return nil, fmt.Errorf("session has no messages to rewind")
	}

	if req.AtMessage < 1 || req.AtMessage > len(original.Messages) {
		return nil, fmt.Errorf("message index %d out of range [1, %d]", req.AtMessage, len(original.Messages))
	}

	// Create the rewound session (fork)
	rewound := &session.Session{
		ID:              session.NewID(),
		Provider:        original.Provider,
		Agent:           original.Agent,
		Branch:          original.Branch,
		CommitSHA:       original.CommitSHA,
		ProjectPath:     original.ProjectPath,
		StorageMode:     original.StorageMode,
		OwnerID:         original.OwnerID,
		ParentID:        original.ID,
		ForkedAtMessage: req.AtMessage,
		Messages:        make([]session.Message, req.AtMessage),
		FileChanges:     append([]session.FileChange(nil), original.FileChanges...), // deep copy
		Links:           append([]session.Link(nil), original.Links...),             // deep copy
		Summary:         fmt.Sprintf("Rewind of %s at message %d", original.ID, req.AtMessage),
	}
	copy(rewound.Messages, original.Messages[:req.AtMessage])

	// Recalculate token usage from truncated messages
	var inputTokens, outputTokens int
	for _, msg := range rewound.Messages {
		inputTokens += msg.InputTokens
		outputTokens += msg.OutputTokens
	}
	rewound.TokenUsage = session.TokenUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  inputTokens + outputTokens,
	}

	s.stampCosts(rewound)
	if err := s.store.Save(rewound); err != nil {
		return nil, fmt.Errorf("saving rewound session: %w", err)
	}
	s.stampAnalytics(rewound)

	return &RewindResult{
		NewSession:      rewound,
		OriginalID:      original.ID,
		TruncatedAt:     req.AtMessage,
		MessagesRemoved: len(original.Messages) - req.AtMessage,
	}, nil
}
