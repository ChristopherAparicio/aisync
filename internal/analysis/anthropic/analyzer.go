// Package anthropic implements the analysis.Analyzer port using the Anthropic
// Messages API directly via HTTP. No dependency on the `claude` CLI binary.
//
// This adapter is for users with an Anthropic API key (subscription or pay-as-you-go).
// It calls POST https://api.anthropic.com/v1/messages with the session analysis prompt.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	llmadapter "github.com/ChristopherAparicio/aisync/internal/analysis/llm"
)

// Default configuration values.
const (
	DefaultBaseURL = "https://api.anthropic.com"
	DefaultModel   = "claude-sonnet-4-20250514"
	DefaultTimeout = 120 * time.Second
	apiVersion     = "2023-06-01"
)

// Config configures the Anthropic analysis adapter.
type Config struct {
	// APIKey is the Anthropic API key. If empty, falls back to ANTHROPIC_API_KEY env var.
	APIKey string

	// BaseURL is the API base URL. Defaults to DefaultBaseURL.
	// Override for testing or proxy setups.
	BaseURL string

	// Model is the model to use (e.g. "claude-sonnet-4-20250514", "claude-haiku-4-20250514").
	// Defaults to DefaultModel.
	Model string

	// Timeout is the HTTP request timeout.
	// Defaults to DefaultTimeout (120s).
	Timeout time.Duration

	// HTTPClient is an optional custom HTTP client (useful for testing).
	HTTPClient *http.Client
}

// Analyzer implements analysis.Analyzer using the Anthropic Messages API.
type Analyzer struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

// NewAnalyzer creates a new Anthropic-based analyzer.
// Returns an error if no API key is available (config or env).
func NewAnalyzer(cfg Config) (*Analyzer, error) {
	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("anthropic API key required: set analysis.api_key or ANTHROPIC_API_KEY env var")
	}

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
		apiKey:  apiKey,
		baseURL: baseURL,
		model:   model,
		client:  client,
	}, nil
}

// Name returns the adapter identifier.
func (a *Analyzer) Name() analysis.AdapterName {
	return analysis.AdapterAnthropic
}

// Analyze examines a session by calling the Anthropic Messages API.
func (a *Analyzer) Analyze(ctx context.Context, req analysis.AnalyzeRequest) (*analysis.AnalysisReport, error) {
	if len(req.Session.Messages) == 0 {
		return nil, fmt.Errorf("session has no messages to analyze")
	}

	prompt := llmadapter.BuildAnalysisPrompt(req)

	// Build the Messages API request.
	msgReq := messagesRequest{
		Model:     a.model,
		MaxTokens: 4096,
		System:    systemPrompt,
		Messages: []message{
			{Role: "user", Content: prompt},
		},
	}

	body, err := json.Marshal(msgReq)
	if err != nil {
		return nil, fmt.Errorf("marshaling anthropic request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating anthropic request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", apiVersion)

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling anthropic /v1/messages: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("anthropic /v1/messages returned %d: %s", resp.StatusCode, string(respBody))
	}

	var msgResp messagesResponse
	if err := json.NewDecoder(resp.Body).Decode(&msgResp); err != nil {
		return nil, fmt.Errorf("decoding anthropic response: %w", err)
	}

	// Extract text content from the response.
	content := extractText(msgResp.Content)
	if content == "" {
		return nil, fmt.Errorf("anthropic returned empty text content")
	}

	// Parse the JSON analysis report.
	var report analysis.AnalysisReport
	if err := json.Unmarshal([]byte(content), &report); err != nil {
		// Try extracting JSON from markdown fences (models sometimes ignore instructions).
		extracted := extractJSON(content)
		if extracted == "" {
			return nil, fmt.Errorf("parsing anthropic response: %w (raw: %.200s)", err, content)
		}
		if jsonErr := json.Unmarshal([]byte(extracted), &report); jsonErr != nil {
			return nil, fmt.Errorf("parsing extracted JSON: %w (raw: %.200s)", jsonErr, extracted)
		}
	}

	// Clamp score.
	if report.Score < 0 {
		report.Score = 0
	}
	if report.Score > 100 {
		report.Score = 100
	}

	if err := report.Validate(); err != nil {
		return nil, fmt.Errorf("anthropic produced invalid report: %w", err)
	}

	return &report, nil
}

// extractText concatenates all text blocks from the response content.
func extractText(content []contentBlock) string {
	var text string
	for _, block := range content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	return text
}

// extractJSON tries to find a JSON object in a string (same logic as ollama adapter).
func extractJSON(s string) string {
	// Look for ```json ... ``` fences.
	if start := indexOf(s, "```json"); start >= 0 {
		content := s[start+7:]
		if end := indexOf(content, "```"); end >= 0 {
			return trimSpace(content[:end])
		}
	}
	// Brace matching.
	first := indexOf(s, "{")
	if first < 0 {
		return ""
	}
	last := lastIndexOf(s, "}")
	if last <= first {
		return ""
	}
	return s[first : last+1]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func lastIndexOf(s, sub string) int {
	for i := len(s) - len(sub); i >= 0; i-- {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// ── Anthropic API types ──

type messagesRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type messagesResponse struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
	Model   string         `json:"model"`
	Usage   usage          `json:"usage"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// systemPrompt is identical to the LLM/Ollama adapters for output consistency.
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
