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

	case err.HTTPStatus == 412:
		// Fireworks (and some OpenAI-compatible gateways) emit 412
		// PRECONDITION_FAILED when an account is suspended for unpaid
		// invoices or hit its monthly spending cap — semantically a
		// billing/quota issue, mapped to rate_limit.
		err.Category = session.ErrorCategoryRateLimit
		err.Message = "Account precondition failed (billing/quota)"
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
//
// Order matters: specific patterns (image dimensions, cache point, empty content)
// must be matched BEFORE the generic "invalid/malformed" fallback, because most
// provider 400 errors wrap their message in a JSON envelope like
// {"error":{"type":"invalid_request_error",...}} which would otherwise match
// the generic "invalid" substring first and lose the specific cause.
func classifyBadRequest(err session.SessionError) session.SessionError {
	raw := strings.ToLower(err.RawError)

	switch {
	case strings.Contains(raw, "context") && (strings.Contains(raw, "length") || strings.Contains(raw, "window") || strings.Contains(raw, "exceed")):
		err.Category = session.ErrorCategoryContextOverflow
		err.Message = "Context window exceeded"
		err.IsRetryable = false

	// Image-specific validation errors (Anthropic: max 8000px per dimension
	// for single-image requests, 2000px for many-image requests).
	case strings.Contains(raw, "image") && strings.Contains(raw, "dimension") && strings.Contains(raw, "exceed"):
		err.Category = session.ErrorCategoryValidation
		err.Message = "Image exceeds maximum pixel dimensions"
		err.IsRetryable = false

	// Cache control errors (Anthropic: invalid cache_control placement in
	// messages, e.g. trying to cache a non-existent content block).
	case strings.Contains(raw, "cache") && (strings.Contains(raw, "invalid cache point") || strings.Contains(raw, "nothing available to cache")):
		err.Category = session.ErrorCategoryValidation
		err.Message = "Invalid cache_control placement"
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
// Order matters: specific patterns (OAuth, quota, config) must precede
// the generic "unauthorized" auth pattern, and the broad bare "aborted"
// alias sits with its abort cluster so verbatim provider strings still match.
var messagePatterns = []struct {
	contains []string
	category session.ErrorCategory
	source   session.ErrorSource
	message  string
}{
	{
		// Anthropic/OpenAI/Bedrock emit verbatim "Aborted" on mid-stream cancel
		// — by far the most common pattern in production.
		contains: []string{"the operation was aborted", "aborted"},
		category: session.ErrorCategoryAborted,
		source:   session.ErrorSourceClient,
		message:  "Operation aborted",
	},
	{
		// google-vertex emits invalid_rapt when the OAuth reauth-after-period
		// token expires — user must re-run `gcloud auth login`.
		contains: []string{"invalid_rapt", "reauth related error"},
		category: session.ErrorCategoryAuthError,
		source:   session.ErrorSourceProvider,
		message:  "Google OAuth reauth required (invalid_rapt)",
	},
	{
		contains: []string{"invalid_grant"},
		category: session.ErrorCategoryAuthError,
		source:   session.ErrorSourceProvider,
		message:  "OAuth grant invalid or expired",
	},
	{
		// Missing env vars / config (e.g. "AWS region setting is missing")
		// block authentication, so categorised as auth_error.
		contains: []string{"api key is missing", "region setting is missing", "credentials not found", "credential not found"},
		category: session.ErrorCategoryAuthError,
		source:   session.ErrorSourceClient,
		message:  "Provider credentials or required config missing",
	},
	{
		// Quota/billing maps to rate_limit (closest semantic — usage cap hit).
		contains: []string{"insufficient_quota", "out of credits", "out_of_credits", "quota exceeded", "billing", "exceeded your current quota"},
		category: session.ErrorCategoryRateLimit,
		source:   session.ErrorSourceProvider,
		message:  "Quota or billing limit reached",
	},
	{
		contains: []string{"internal server error"},
		category: session.ErrorCategoryProviderError,
		source:   session.ErrorSourceProvider,
		message:  "Provider internal server error",
	},
	{
		// Provider SDKs sometimes drop the HTTP code and only forward the
		// text body, so 5xx-equivalents land here.
		contains: []string{"overloaded", "currently unavailable", "service unavailable", "temporarily unavailable", "model is overloaded"},
		category: session.ErrorCategoryProviderError,
		source:   session.ErrorSourceProvider,
		message:  "Provider overloaded or unavailable",
	},
	{
		contains: []string{"rate limit", "too many requests"},
		category: session.ErrorCategoryRateLimit,
		source:   session.ErrorSourceProvider,
		message:  "Rate limit exceeded",
	},
	{
		// "too large to compact" / "exceeds model limit" are OpenCode-specific
		// strings emitted when even media-stripped context still overflows.
		contains: []string{"context length", "context window", "maximum context", "token limit", "too large to compact", "exceeds model limit", "prompt is too long"},
		category: session.ErrorCategoryContextOverflow,
		source:   session.ErrorSourceProvider,
		message:  "Context window exceeded",
	},
	{
		// Anthropic 400 when a tool_use block has no matching tool_result —
		// caused by killed sub-agents; poisons the rest of the session.
		contains: []string{"tool_use ids were found without tool_result", "tool_use_id", "without tool_result blocks"},
		category: session.ErrorCategoryValidation,
		source:   session.ErrorSourceProvider,
		message:  "Tool sequence corrupted (tool_use without tool_result)",
	},
	{
		contains: []string{"unauthorized", "invalid api key", "invalid x-api-key", "authentication_error", "invalid authentication credentials"},
		category: session.ErrorCategoryAuthError,
		source:   session.ErrorSourceProvider,
		message:  "Authentication failure",
	},
	{
		// Claude Code CLI emits this string when its stored OAuth token has
		// expired or been revoked; the only fix is re-running `claude` to
		// refresh credentials, so it is unambiguously an auth error.
		contains: []string{"credentials are unavailable", "credentials are expired", "credentials unavailable or expired", "token refresh failed"},
		category: session.ErrorCategoryAuthError,
		source:   session.ErrorSourceClient,
		message:  "Provider credentials expired or unavailable",
	},
	{
		contains: []string{"timeout", "timed out", "deadline exceeded"},
		category: session.ErrorCategoryNetworkError,
		source:   session.ErrorSourceProvider,
		message:  "Request timeout",
	},
	{
		contains: []string{"unable to connect", "was there a typo in the url", "name or service not known", "no route to host"},
		category: session.ErrorCategoryNetworkError,
		source:   session.ErrorSourceClient,
		message:  "Cannot reach provider endpoint",
	},
	{
		contains: []string{"connection refused", "connection reset", "dns resolution"},
		category: session.ErrorCategoryNetworkError,
		source:   session.ErrorSourceClient,
		message:  "Network connection failure",
	},
	{
		// TLS handshake failures and mid-stream connection drops surface as
		// these strings from Go's net/http and provider SDK shims; both are
		// transport-layer issues, not auth/quota.
		contains: []string{"certificate verification", "peer closed connection", "tls handshake", "x509:"},
		category: session.ErrorCategoryNetworkError,
		source:   session.ErrorSourceClient,
		message:  "TLS or transport-layer failure",
	},
	{
		// SQLite from OpenCode's local cache — host disk full or write lock.
		contains: []string{"database is locked", "database or disk is full", "disk i/o error", "no space left"},
		category: session.ErrorCategoryToolError,
		source:   session.ErrorSourceClient,
		message:  "Local storage error (SQLite/disk)",
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
