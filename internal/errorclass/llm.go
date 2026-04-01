// Package errorclass — llm.go implements LLM-powered error classification.
//
// LLMClassifier uses a language model to classify errors that deterministic
// pattern matching cannot handle. It sends the raw error text and context
// to the LLM, which returns a structured classification.
//
// This classifier is designed to be used as a fallback behind the
// DeterministicClassifier via the CompositeClassifier.
package errorclass

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/llm"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// LLMClassifier classifies errors by sending them to a language model.
// It implements session.ErrorClassifier.
type LLMClassifier struct {
	client  llm.Client
	timeout time.Duration
	logger  *slog.Logger
}

// LLMClassifierConfig holds dependencies for creating an LLMClassifier.
type LLMClassifierConfig struct {
	Client  llm.Client    // required — the LLM client to use
	Timeout time.Duration // optional — defaults to 30s
	Logger  *slog.Logger  // optional — defaults to slog.Default()
}

// NewLLMClassifier creates a new LLM-based error classifier.
func NewLLMClassifier(cfg LLMClassifierConfig) *LLMClassifier {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &LLMClassifier{
		client:  cfg.Client,
		timeout: timeout,
		logger:  logger,
	}
}

// Name returns the classifier identifier.
func (c *LLMClassifier) Name() string {
	return "llm"
}

// llmClassificationResponse is the expected JSON response from the LLM.
type llmClassificationResponse struct {
	Category    string `json:"category"`
	Source      string `json:"source"`
	Message     string `json:"message"`
	IsRetryable bool   `json:"is_retryable"`
}

// systemPrompt instructs the LLM to classify errors.
const systemPrompt = `You are an error classifier for AI coding assistant sessions.
Given an error's raw text, tool name, HTTP status, and provider name, classify it into exactly one category.

Valid categories: provider_error, rate_limit, context_overflow, auth_error, validation, tool_error, network_error, aborted, unknown
Valid sources: provider, tool, client

Respond with ONLY a JSON object (no markdown, no explanation):
{"category": "...", "source": "...", "message": "short human-readable summary", "is_retryable": true/false}

Rules:
- "provider_error": server errors from LLM providers (5xx, overloaded, internal errors)
- "rate_limit": rate limiting, quota exceeded, too many requests
- "context_overflow": context window/token limit exceeded
- "auth_error": authentication or authorization failures, permission denied
- "validation": bad request, invalid input, malformed payload
- "tool_error": tool execution failures (bash, file operations, compilation errors)
- "network_error": connection issues, timeouts, DNS failures
- "aborted": user or system cancelled the operation
- "unknown": only if truly unclassifiable
- "message" should be a concise 3-8 word summary
- If the error looks retryable (transient), set is_retryable to true`

// Classify sends the error to the LLM for classification.
// On any failure (LLM error, parse error, timeout), it returns the error
// unchanged with category "unknown" — it never panics or propagates LLM errors.
func (c *LLMClassifier) Classify(err session.SessionError) session.SessionError {
	// Already classified? Don't override.
	if err.Category != "" && err.Category != session.ErrorCategoryUnknown {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	userPrompt := buildUserPrompt(err)

	resp, llmErr := c.client.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		MaxTokens:    200,
	})
	if llmErr != nil {
		c.logger.Warn("LLM error classification failed",
			"error_id", err.ID,
			"llm_error", llmErr,
		)
		// Graceful fallback: keep as unknown.
		err.Category = session.ErrorCategoryUnknown
		err.Confidence = "low"
		return err
	}

	// Parse the LLM response.
	classified := c.parseResponse(resp.Content, err)
	return classified
}

// buildUserPrompt creates the prompt sent to the LLM.
func buildUserPrompt(err session.SessionError) string {
	var b strings.Builder
	b.WriteString("Classify this error:\n\n")

	if err.RawError != "" {
		// Truncate very long raw errors to avoid context overflow.
		raw := err.RawError
		if len(raw) > 2000 {
			raw = raw[:2000] + "... [truncated]"
		}
		fmt.Fprintf(&b, "Raw error: %s\n", raw)
	}
	if err.Message != "" {
		fmt.Fprintf(&b, "Message: %s\n", err.Message)
	}
	if err.ToolName != "" {
		fmt.Fprintf(&b, "Tool: %s\n", err.ToolName)
	}
	if err.HTTPStatus > 0 {
		fmt.Fprintf(&b, "HTTP status: %d\n", err.HTTPStatus)
	}
	if err.ProviderName != "" {
		fmt.Fprintf(&b, "Provider: %s\n", err.ProviderName)
	}
	if err.Source != "" {
		fmt.Fprintf(&b, "Source: %s\n", err.Source)
	}

	return b.String()
}

// parseResponse extracts the classification from the LLM's JSON response.
func (c *LLMClassifier) parseResponse(content string, original session.SessionError) session.SessionError {
	// Strip markdown code fences if present.
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var resp llmClassificationResponse
	if err := json.Unmarshal([]byte(content), &resp); err != nil {
		c.logger.Warn("LLM response parse failed",
			"error_id", original.ID,
			"content", content,
			"parse_error", err,
		)
		original.Category = session.ErrorCategoryUnknown
		original.Confidence = "low"
		return original
	}

	// Validate and apply the classification.
	cat := session.ErrorCategory(resp.Category)
	if !cat.Valid() {
		c.logger.Warn("LLM returned invalid category",
			"error_id", original.ID,
			"category", resp.Category,
		)
		original.Category = session.ErrorCategoryUnknown
		original.Confidence = "low"
		return original
	}

	src := session.ErrorSource(resp.Source)
	if !src.Valid() {
		// Use the original source if LLM returns invalid source.
		src = original.Source
		if src == "" {
			src = session.ErrorSourceClient
		}
	}

	original.Category = cat
	original.Source = src
	original.IsRetryable = resp.IsRetryable
	original.Confidence = "medium" // LLM classifications get medium confidence
	if resp.Message != "" {
		original.Message = resp.Message
	}

	return original
}
