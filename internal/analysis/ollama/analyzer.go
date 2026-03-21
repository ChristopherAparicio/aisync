// Package ollama implements the analysis.Analyzer port using Ollama's /api/chat endpoint.
// It sends a structured system prompt with session data and asks the model to return
// a JSON AnalysisReport. Uses Ollama's native `format: "json"` mode to guarantee valid JSON.
//
// This adapter is designed for local GPU inference — models like qwen3:30b, llama3.1,
// or deepseek-coder can run on a developer's machine without API costs.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	llmadapter "github.com/ChristopherAparicio/aisync/internal/analysis/llm"
)

// Default configuration values.
const (
	DefaultBaseURL = "http://localhost:11434"
	DefaultModel   = "qwen3:30b"
	DefaultTimeout = 10 * time.Minute // local models on single GPU can be very slow
)

// Config configures the Ollama analysis adapter.
type Config struct {
	// BaseURL is the Ollama API base URL (e.g. "http://localhost:11434").
	// Defaults to DefaultBaseURL.
	BaseURL string

	// Model is the Ollama model to use (e.g. "qwen3:30b", "llama3.1:8b").
	// Defaults to DefaultModel.
	Model string

	// Timeout is the HTTP request timeout. Local models can be slow on first load.
	// Defaults to DefaultTimeout (120s).
	Timeout time.Duration

	// HTTPClient is an optional custom HTTP client (useful for testing).
	// If nil, a default client with the configured timeout is used.
	HTTPClient *http.Client
}

// Analyzer implements analysis.Analyzer using Ollama's /api/chat endpoint.
type Analyzer struct {
	baseURL string
	model   string
	client  *http.Client
}

// NewAnalyzer creates a new Ollama-based analyzer.
func NewAnalyzer(cfg Config) *Analyzer {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	model := cfg.Model
	if model == "" {
		model = DefaultModel
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	return &Analyzer{
		baseURL: baseURL,
		model:   model,
		client:  client,
	}
}

// Name returns the adapter identifier.
func (a *Analyzer) Name() analysis.AdapterName {
	return analysis.AdapterOllama
}

// Analyze examines a session by sending its data to Ollama and parsing the JSON response.
func (a *Analyzer) Analyze(ctx context.Context, req analysis.AnalyzeRequest) (*analysis.AnalysisReport, error) {
	if len(req.Session.Messages) == 0 {
		return nil, fmt.Errorf("session has no messages to analyze")
	}

	// Reuse the same prompt builder as the LLM adapter for consistency.
	prompt := llmadapter.BuildAnalysisPrompt(req)

	// Build the Ollama /api/chat request.
	chatReq := chatRequest{
		Model:  a.model,
		Stream: false,
		Format: "json",
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		},
	}

	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshaling ollama request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating ollama request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling ollama /api/chat: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ollama /api/chat returned %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decoding ollama response: %w", err)
	}

	content := chatResp.Message.Content
	if content == "" {
		return nil, fmt.Errorf("ollama returned empty response")
	}

	// Parse the JSON analysis report from the model's response.
	report, err := parseReport(content)
	if err != nil {
		return nil, fmt.Errorf("parsing ollama analysis response: %w", err)
	}

	return report, nil
}

// parseReport extracts and validates an AnalysisReport from raw model output.
// Handles cases where the model wraps JSON in markdown fences or adds preamble.
func parseReport(raw string) (*analysis.AnalysisReport, error) {
	// Try direct JSON parse first.
	var report analysis.AnalysisReport
	if err := json.Unmarshal([]byte(raw), &report); err == nil {
		return clampAndValidate(&report)
	}

	// Try to extract JSON from markdown fences or other wrappers.
	extracted := extractJSON(raw)
	if extracted == "" {
		return nil, fmt.Errorf("no JSON found in response (raw: %.200s)", raw)
	}

	if err := json.Unmarshal([]byte(extracted), &report); err != nil {
		return nil, fmt.Errorf("invalid JSON after extraction: %w (raw: %.200s)", err, extracted)
	}

	return clampAndValidate(&report)
}

// clampAndValidate ensures the report has a valid score range and passes validation.
func clampAndValidate(report *analysis.AnalysisReport) (*analysis.AnalysisReport, error) {
	if report.Score < 0 {
		report.Score = 0
	}
	if report.Score > 100 {
		report.Score = 100
	}
	if err := report.Validate(); err != nil {
		return nil, fmt.Errorf("model produced invalid report: %w", err)
	}
	return report, nil
}

// extractJSON tries to find a JSON object in a string that may contain
// markdown fences, preamble text, or other wrapping.
func extractJSON(s string) string {
	// Look for ```json ... ``` fences.
	if start := indexOf(s, "```json"); start >= 0 {
		content := s[start+7:]
		if end := indexOf(content, "```"); end >= 0 {
			return trimSpace(content[:end])
		}
	}
	// Look for generic ``` ... ``` fences.
	if start := indexOf(s, "```"); start >= 0 {
		content := s[start+3:]
		if end := indexOf(content, "```"); end >= 0 {
			return trimSpace(content[:end])
		}
	}
	// Brace matching: find first { and last }.
	first := indexOf(s, "{")
	if first < 0 {
		return ""
	}
	last := lastIndexOf(s, "}")
	if last < 0 || last <= first {
		return ""
	}
	return s[first : last+1]
}

// indexOf returns the index of the first occurrence of sub in s, or -1.
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// lastIndexOf returns the index of the last occurrence of sub in s, or -1.
func lastIndexOf(s, sub string) int {
	for i := len(s) - len(sub); i >= 0; i-- {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// trimSpace removes leading/trailing whitespace and newlines.
func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// ── Ollama API types ──

// chatRequest is the request body for POST /api/chat.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Format   string        `json:"format,omitempty"` // "json" for structured output
}

// chatMessage is a single message in the Ollama chat conversation.
type chatMessage struct {
	Role    string `json:"role"` // "system", "user", "assistant"
	Content string `json:"content"`
}

// chatResponse is the response body from POST /api/chat (non-streaming).
type chatResponse struct {
	Model           string      `json:"model"`
	CreatedAt       string      `json:"created_at"`
	Message         chatMessage `json:"message"`
	Done            bool        `json:"done"`
	DoneReason      string      `json:"done_reason,omitempty"`
	TotalDuration   int64       `json:"total_duration,omitempty"` // nanoseconds
	PromptEvalCount int         `json:"prompt_eval_count,omitempty"`
	EvalCount       int         `json:"eval_count,omitempty"`
}

// systemPrompt is the system instruction for the Ollama analysis call.
// Same schema as the LLM adapter for consistent output format.
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
