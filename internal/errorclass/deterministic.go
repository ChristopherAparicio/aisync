// Package errorclass provides ErrorClassifier implementations.
//
// DeterministicClassifier uses pattern matching on HTTP status codes,
// error message strings, and known provider error patterns to classify
// errors without requiring an LLM. It is fast, reliable, and handles
// the vast majority of real-world errors.
package errorclass

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// DeterministicClassifier classifies errors using pattern matching.
// It checks HTTP status codes first, then falls back to message patterns.
// Implements session.ErrorClassifier.
type DeterministicClassifier struct{}

// NewDeterministicClassifier creates a new deterministic classifier.
func NewDeterministicClassifier() *DeterministicClassifier {
	return &DeterministicClassifier{}
}

// Name returns the classifier identifier.
func (c *DeterministicClassifier) Name() string {
	return "deterministic"
}

// Classify applies deterministic rules to classify the error.
// It fills in Category, Source, Message, and Confidence on the returned error.
func (c *DeterministicClassifier) Classify(err session.SessionError) session.SessionError {
	// Already classified? Don't override.
	if err.Category != "" && err.Category != session.ErrorCategoryUnknown {
		return err
	}

	// Step 1: Classify by HTTP status code (most reliable signal).
	if err.HTTPStatus > 0 {
		err = classifyByHTTPStatus(err)
		return err
	}

	// Step 2: Classify by source — tool errors with no HTTP status.
	if err.Source == session.ErrorSourceTool || err.ToolName != "" {
		err = classifyToolError(err)
		return err
	}

	// Step 3: Classify by raw error message patterns.
	err = classifyByMessage(err)
	return err
}

// ── HTTP status code classification ──

func classifyByHTTPStatus(err session.SessionError) session.SessionError {
	err.Confidence = "high"
	err.Source = session.ErrorSourceProvider

	switch {
	case err.HTTPStatus == 429:
		err.Category = session.ErrorCategoryRateLimit
		err.Message = summarizeRateLimit(err)
		err.IsRetryable = true

	case err.HTTPStatus == 401 || err.HTTPStatus == 403:
		err.Category = session.ErrorCategoryAuthError
		err.Message = "Authentication or authorization failure"
		err.IsRetryable = false

	case err.HTTPStatus == 400:
		err = classifyBadRequest(err)

	case err.HTTPStatus == 404:
		err.Category = session.ErrorCategoryValidation
		err.Message = "Resource not found (model or endpoint)"
		err.IsRetryable = false

	case err.HTTPStatus == 408 || err.HTTPStatus == 504:
		err.Category = session.ErrorCategoryNetworkError
		err.Message = "Request timeout"
		err.IsRetryable = true

	case err.HTTPStatus == 413:
		err.Category = session.ErrorCategoryContextOverflow
		err.Message = "Request payload too large (context overflow)"
		err.IsRetryable = false

	case err.HTTPStatus == 529:
		// Anthropic-specific: overloaded
		err.Category = session.ErrorCategoryProviderError
		err.Message = "Provider overloaded"
		err.IsRetryable = true

	case err.HTTPStatus >= 500:
		err.Category = session.ErrorCategoryProviderError
		err.Message = "Provider internal server error"
		err.IsRetryable = true

	default:
		err.Category = session.ErrorCategoryUnknown
		err.Message = "HTTP error " + strconv.Itoa(err.HTTPStatus)
		err.Confidence = "low"
	}

	// Enrich with rate limit header info.
	err = enrichFromHeaders(err)

	return err
}

// classifyBadRequest further classifies 400 errors by examining the message.
func classifyBadRequest(err session.SessionError) session.SessionError {
	raw := strings.ToLower(err.RawError)

	switch {
	case strings.Contains(raw, "context") && (strings.Contains(raw, "length") || strings.Contains(raw, "window") || strings.Contains(raw, "exceed")):
		err.Category = session.ErrorCategoryContextOverflow
		err.Message = "Context window exceeded"
		err.IsRetryable = false

	case strings.Contains(raw, "content") && strings.Contains(raw, "empty"):
		err.Category = session.ErrorCategoryValidation
		err.Message = "Empty content block in message"
		err.IsRetryable = false

	case strings.Contains(raw, "invalid") || strings.Contains(raw, "malformed"):
		err.Category = session.ErrorCategoryValidation
		err.Message = "Invalid request format"
		err.IsRetryable = false

	default:
		err.Category = session.ErrorCategoryValidation
		err.Message = "Bad request"
		err.IsRetryable = false
	}

	return err
}

// summarizeRateLimit builds a human-readable message from rate limit headers.
func summarizeRateLimit(err session.SessionError) string {
	msg := "Rate limit exceeded"

	if utilization, ok := err.Headers["anthropic-ratelimit-unified-7d-utilization"]; ok {
		msg += " (7d utilization: " + utilization + ")"
	}
	if overage, ok := err.Headers["anthropic-ratelimit-unified-overage-status"]; ok {
		msg += " [overage: " + overage + "]"
	}

	return msg
}

// enrichFromHeaders adds contextual information from provider response headers.
func enrichFromHeaders(err session.SessionError) session.SessionError {
	if len(err.Headers) == 0 {
		return err
	}

	// Check for rate limit overage info (Anthropic-specific).
	if reason, ok := err.Headers["anthropic-ratelimit-unified-overage-disabled-reason"]; ok {
		if reason == "out_of_credits" {
			err.Message += " [out of overage credits]"
		}
	}

	// Extract request ID for support.
	if reqID, ok := err.Headers["request-id"]; ok && err.RequestID == "" {
		err.RequestID = reqID
	}

	// Extract upstream service time as duration.
	if svcTime, ok := err.Headers["x-envoy-upstream-service-time"]; ok && err.DurationMs == 0 {
		if ms, parseErr := strconv.Atoi(svcTime); parseErr == nil {
			err.DurationMs = ms
		}
	}

	return err
}

// ── Tool error classification ──

// toolErrorPatterns maps regex patterns to error categories.
var toolErrorPatterns = []struct {
	pattern  *regexp.Regexp
	category session.ErrorCategory
	message  string
}{
	{
		pattern:  regexp.MustCompile(`(?i)permission denied`),
		category: session.ErrorCategoryAuthError,
		message:  "Permission denied",
	},
	{
		pattern:  regexp.MustCompile(`(?i)no such file or directory`),
		category: session.ErrorCategoryToolError,
		message:  "File not found",
	},
	{
		pattern:  regexp.MustCompile(`(?i)command not found`),
		category: session.ErrorCategoryToolError,
		message:  "Command not found",
	},
	{
		pattern:  regexp.MustCompile(`(?i)syntax error`),
		category: session.ErrorCategoryToolError,
		message:  "Syntax error",
	},
	{
		pattern:  regexp.MustCompile(`(?i)(ECONNREFUSED|ECONNRESET|ETIMEDOUT|EHOSTUNREACH|EAI_AGAIN)`),
		category: session.ErrorCategoryNetworkError,
		message:  "Network connection error",
	},
	{
		pattern:  regexp.MustCompile(`(?i)out of memory|OOM|cannot allocate memory`),
		category: session.ErrorCategoryToolError,
		message:  "Out of memory",
	},
	{
		pattern:  regexp.MustCompile(`(?i)disk.*full|no space left`),
		category: session.ErrorCategoryToolError,
		message:  "Disk full",
	},
	{
		pattern:  regexp.MustCompile(`(?i)exit (code|status) [1-9]`),
		category: session.ErrorCategoryToolError,
		message:  "Command failed with non-zero exit code",
	},
}

func classifyToolError(err session.SessionError) session.SessionError {
	err.Source = session.ErrorSourceTool
	err.Confidence = "medium"

	raw := err.RawError
	if raw == "" {
		raw = err.Message
	}

	for _, p := range toolErrorPatterns {
		if p.pattern.MatchString(raw) {
			err.Category = p.category
			err.Message = p.message
			err.Confidence = "high"
			return err
		}
	}

	// Default: generic tool error.
	err.Category = session.ErrorCategoryToolError
	if err.Message == "" {
		err.Message = "Tool execution failed"
	}
	return err
}

// ── Message-based classification (fallback) ──

// messagePatterns maps string patterns to error categories.
var messagePatterns = []struct {
	contains []string
	category session.ErrorCategory
	source   session.ErrorSource
	message  string
}{
	{
		contains: []string{"the operation was aborted"},
		category: session.ErrorCategoryAborted,
		source:   session.ErrorSourceClient,
		message:  "Operation aborted",
	},
	{
		contains: []string{"internal server error"},
		category: session.ErrorCategoryProviderError,
		source:   session.ErrorSourceProvider,
		message:  "Provider internal server error",
	},
	{
		contains: []string{"rate limit", "too many requests"},
		category: session.ErrorCategoryRateLimit,
		source:   session.ErrorSourceProvider,
		message:  "Rate limit exceeded",
	},
	{
		contains: []string{"context length", "context window", "maximum context", "token limit"},
		category: session.ErrorCategoryContextOverflow,
		source:   session.ErrorSourceProvider,
		message:  "Context window exceeded",
	},
	{
		contains: []string{"unauthorized", "invalid api key", "invalid x-api-key"},
		category: session.ErrorCategoryAuthError,
		source:   session.ErrorSourceProvider,
		message:  "Authentication failure",
	},
	{
		contains: []string{"timeout", "timed out", "deadline exceeded"},
		category: session.ErrorCategoryNetworkError,
		source:   session.ErrorSourceProvider,
		message:  "Request timeout",
	},
	{
		contains: []string{"connection refused", "connection reset", "dns resolution"},
		category: session.ErrorCategoryNetworkError,
		source:   session.ErrorSourceClient,
		message:  "Network connection failure",
	},
}

func classifyByMessage(err session.SessionError) session.SessionError {
	raw := strings.ToLower(err.RawError)
	if raw == "" {
		raw = strings.ToLower(err.Message)
	}
	if raw == "" {
		err.Category = session.ErrorCategoryUnknown
		err.Source = session.ErrorSourceClient
		err.Confidence = "low"
		return err
	}

	for _, p := range messagePatterns {
		for _, substr := range p.contains {
			if strings.Contains(raw, substr) {
				err.Category = p.category
				err.Source = p.source
				err.Message = p.message
				err.Confidence = "medium"
				return err
			}
		}
	}

	// No pattern matched.
	err.Category = session.ErrorCategoryUnknown
	if err.Source == "" {
		err.Source = session.ErrorSourceClient
	}
	err.Confidence = "low"
	if err.Message == "" {
		err.Message = "Unclassified error"
	}
	return err
}
