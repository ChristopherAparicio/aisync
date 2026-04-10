// Package anthropic implements the analysis.Analyzer port using the Anthropic
// Messages API directly via HTTP. No dependency on the `claude` CLI binary.
//
// This adapter is for users with an Anthropic API key (subscription or pay-as-you-go).
// It calls POST https://api.anthropic.com/v1/messages with the session analysis prompt.
//
// When a ToolExecutor is provided in the AnalyzeRequest, this adapter operates
// in agentic mode: it sends tool definitions to the model, then runs a multi-turn
// loop executing tools and feeding results back until the model produces a final
// text response or the iteration cap is reached.
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

	// MaxToolIterations is the hard cap on agentic loop iterations.
	// Prevents runaway token consumption if the model keeps calling tools.
	MaxToolIterations = 10
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

	// MaxIterations overrides the default max agentic loop iterations.
	// 0 uses MaxToolIterations.
	MaxIterations int
}

// Analyzer implements analysis.Analyzer using the Anthropic Messages API.
type Analyzer struct {
	apiKey        string
	baseURL       string
	model         string
	client        *http.Client
	maxIterations int
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
	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = MaxToolIterations
	}

	return &Analyzer{
		apiKey:        apiKey,
		baseURL:       baseURL,
		model:         model,
		client:        client,
		maxIterations: maxIter,
	}, nil
}

// Name returns the adapter identifier.
func (a *Analyzer) Name() analysis.AdapterName {
	return analysis.AdapterAnthropic
}

// Analyze examines a session by calling the Anthropic Messages API.
// When req.ToolExecutor is non-nil, operates in agentic mode with tool use.
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
			{Role: "user", Content: []contentBlock{{Type: "text", Text: prompt}}},
		},
	}

	// Attach tools if executor is available (agentic mode).
	if req.ToolExecutor != nil {
		tools := analysis.AnalystTools()
		for _, t := range tools {
			msgReq.Tools = append(msgReq.Tools, toolDefinition{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
	}

	// Agentic loop: call API, execute tools, repeat until end_turn or max iterations.
	var finalContent string
	for iteration := 0; iteration <= a.maxIterations; iteration++ {
		resp, err := a.callAPI(ctx, msgReq)
		if err != nil {
			return nil, err
		}

		// Check if this is a tool_use response.
		if resp.StopReason == "tool_use" && req.ToolExecutor != nil && iteration < a.maxIterations {
			// Append the assistant's response (with tool_use blocks) to messages.
			msgReq.Messages = append(msgReq.Messages, message{
				Role:    "assistant",
				Content: resp.Content,
			})

			// Execute each tool call and build tool_result blocks.
			var toolResults []contentBlock
			for _, block := range resp.Content {
				if block.Type != "tool_use" {
					continue
				}
				resultText, isError := dispatchTool(req.ToolExecutor, block.Name, block.Input)
				toolResults = append(toolResults, contentBlock{
					Type:      "tool_result",
					ToolUseID: block.ID,
					Content:   resultText,
					IsError:   isError,
				})
			}

			// Append tool results as a user message (Anthropic API convention).
			msgReq.Messages = append(msgReq.Messages, message{
				Role:    "user",
				Content: toolResults,
			})

			continue // Next iteration of the agentic loop.
		}

		// Not a tool_use response (or max iterations reached) — extract final text.
		finalContent = extractText(resp.Content)
		break
	}

	if finalContent == "" {
		return nil, fmt.Errorf("anthropic returned empty text content after %d iterations", a.maxIterations)
	}

	return parseReport(finalContent)
}

// callAPI sends a single request to the Anthropic Messages API and returns the parsed response.
func (a *Analyzer) callAPI(ctx context.Context, msgReq messagesRequest) (*messagesResponse, error) {
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

	return &msgResp, nil
}

// parseReport extracts and validates an analysis report from the model's text output.
func parseReport(content string) (*analysis.AnalysisReport, error) {
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
//
// These types model the Anthropic Messages API request/response format.
// Content is represented as []contentBlock to support both text and tool_use
// content types in the same message (required for the agentic loop).

type messagesRequest struct {
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	System    string           `json:"system,omitempty"`
	Messages  []message        `json:"messages"`
	Tools     []toolDefinition `json:"tools,omitempty"`
}

// message represents a single message in the conversation.
// Content is always []contentBlock for both request and response.
type message struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type messagesResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []contentBlock `json:"content"`
	Model      string         `json:"model"`
	StopReason string         `json:"stop_reason"`
	Usage      usage          `json:"usage"`
}

// contentBlock represents a single content element within a message.
// Multiple types are supported: text, tool_use, tool_result.
type contentBlock struct {
	// Common field.
	Type string `json:"type"`

	// For type="text": the text content.
	Text string `json:"text,omitempty"`

	// For type="tool_use": the tool call details.
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// For type="tool_result": the result of a tool call.
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type toolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// systemPrompt reuses the canonical prompt from the llm adapter for consistency.
var systemPrompt = llmadapter.SystemPrompt
