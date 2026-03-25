// Package session — error.go defines structured error types for session diagnostics.
//
// SessionError is a first-class domain entity that captures errors occurring
// during AI coding sessions — both tool-level errors (bash exit code 1, file not found)
// and provider-level errors (Anthropic HTTP 500, rate limits, context overflow).
//
// ErrorClassifier is a port interface (Strategy pattern) for classifying raw
// errors into structured categories. Implementations live in infrastructure:
//   - DeterministicClassifier: pattern matching on HTTP status codes and known messages
//   - LLMClassifier (future): LLM-powered fallback for ambiguous tool errors
//
// ErrorSource distinguishes internal (our session/config) vs external (provider API)
// vs tool (tool execution failure) errors for quick triage.
package session

import "time"

// ── ErrorCategory ──

// ErrorCategory classifies an error by its nature.
type ErrorCategory string

// Known error categories.
const (
	ErrorCategoryProviderError   ErrorCategory = "provider_error"   // provider API returned a server error (5xx)
	ErrorCategoryRateLimit       ErrorCategory = "rate_limit"       // provider rate limit exceeded (429 or related headers)
	ErrorCategoryContextOverflow ErrorCategory = "context_overflow" // context window exceeded
	ErrorCategoryAuthError       ErrorCategory = "auth_error"       // authentication/authorization failure (401, 403)
	ErrorCategoryValidation      ErrorCategory = "validation"       // bad request / validation error (400)
	ErrorCategoryToolError       ErrorCategory = "tool_error"       // tool execution failure (bash exit code, file not found)
	ErrorCategoryNetworkError    ErrorCategory = "network_error"    // connection timeout, DNS failure, etc.
	ErrorCategoryAborted         ErrorCategory = "aborted"          // operation was aborted/cancelled by the user or system
	ErrorCategoryUnknown         ErrorCategory = "unknown"          // could not classify
)

var allErrorCategories = []ErrorCategory{
	ErrorCategoryProviderError,
	ErrorCategoryRateLimit,
	ErrorCategoryContextOverflow,
	ErrorCategoryAuthError,
	ErrorCategoryValidation,
	ErrorCategoryToolError,
	ErrorCategoryNetworkError,
	ErrorCategoryAborted,
	ErrorCategoryUnknown,
}

// Valid reports whether c is a known error category.
func (c ErrorCategory) Valid() bool {
	for _, v := range allErrorCategories {
		if c == v {
			return true
		}
	}
	return false
}

// String returns the string representation.
func (c ErrorCategory) String() string {
	return string(c)
}

// IsExternal reports whether this category represents an external error
// (not caused by the session content itself).
func (c ErrorCategory) IsExternal() bool {
	switch c {
	case ErrorCategoryProviderError, ErrorCategoryRateLimit, ErrorCategoryNetworkError:
		return true
	default:
		return false
	}
}

// ── ErrorSource ──

// ErrorSource indicates where the error originated.
type ErrorSource string

// Known error sources.
const (
	ErrorSourceProvider ErrorSource = "provider" // API-level error from the LLM provider (Anthropic, OpenAI, etc.)
	ErrorSourceTool     ErrorSource = "tool"     // tool execution failure (bash, file_edit, etc.)
	ErrorSourceClient   ErrorSource = "client"   // client-side error (OpenCode, Claude Code, etc.)
)

var allErrorSources = []ErrorSource{
	ErrorSourceProvider,
	ErrorSourceTool,
	ErrorSourceClient,
}

// Valid reports whether s is a known error source.
func (s ErrorSource) Valid() bool {
	for _, v := range allErrorSources {
		if s == v {
			return true
		}
	}
	return false
}

// String returns the string representation.
func (s ErrorSource) String() string {
	return string(s)
}

// ── SessionError ──

// SessionError is a structured error event captured from a session.
// It represents a single error occurrence with classification metadata
// that allows structured querying (e.g. "show all provider_error in last 24h").
type SessionError struct {
	// Identity
	ID        string `json:"id"`         // unique error ID (UUID)
	SessionID ID     `json:"session_id"` // owning session

	// Classification (set by ErrorClassifier)
	Category ErrorCategory `json:"category"` // what kind of error
	Source   ErrorSource   `json:"source"`   // where it originated

	// Error details
	Message      string `json:"message"`                 // human-readable error message
	RawError     string `json:"raw_error,omitempty"`     // original error text from provider/tool
	ToolName     string `json:"tool_name,omitempty"`     // tool that failed (for tool errors)
	ToolCallID   string `json:"tool_call_id,omitempty"`  // tool call ID (for tool errors)
	MessageID    string `json:"message_id,omitempty"`    // message where the error occurred
	MessageIndex int    `json:"message_index,omitempty"` // 0-based position in session messages

	// Provider error details (for API errors)
	HTTPStatus   int               `json:"http_status,omitempty"`   // HTTP status code (500, 429, etc.)
	ProviderName string            `json:"provider_name,omitempty"` // e.g. "anthropic", "openai"
	RequestID    string            `json:"request_id,omitempty"`    // provider request ID for support
	Headers      map[string]string `json:"headers,omitempty"`       // relevant response headers (rate limit info, etc.)

	// Timing
	OccurredAt time.Time `json:"occurred_at"`           // when the error happened
	DurationMs int       `json:"duration_ms,omitempty"` // how long the request took before failing

	// Classification metadata
	IsRetryable bool   `json:"is_retryable,omitempty"` // whether the error is retryable
	Confidence  string `json:"confidence,omitempty"`   // classification confidence: "high", "medium", "low"
}

// IsExternal reports whether this error originated from outside the session
// (provider API, network) rather than from the session content itself.
func (e SessionError) IsExternal() bool {
	return e.Category.IsExternal()
}

// SessionErrorSummary provides aggregated error statistics for a session.
type SessionErrorSummary struct {
	SessionID      ID                    `json:"session_id"`
	TotalErrors    int                   `json:"total_errors"`
	ByCategory     map[ErrorCategory]int `json:"by_category"`
	BySource       map[ErrorSource]int   `json:"by_source"`
	ExternalErrors int                   `json:"external_errors"` // errors from provider/network
	InternalErrors int                   `json:"internal_errors"` // errors from tools/client
	FirstErrorAt   time.Time             `json:"first_error_at,omitempty"`
	LastErrorAt    time.Time             `json:"last_error_at,omitempty"`
}

// NewSessionErrorSummary computes aggregated statistics from a list of errors.
func NewSessionErrorSummary(sessionID ID, errors []SessionError) SessionErrorSummary {
	s := SessionErrorSummary{
		SessionID:  sessionID,
		ByCategory: make(map[ErrorCategory]int),
		BySource:   make(map[ErrorSource]int),
	}

	for i, e := range errors {
		s.TotalErrors++
		s.ByCategory[e.Category]++
		s.BySource[e.Source]++

		if e.IsExternal() {
			s.ExternalErrors++
		} else {
			s.InternalErrors++
		}

		if i == 0 || e.OccurredAt.Before(s.FirstErrorAt) {
			s.FirstErrorAt = e.OccurredAt
		}
		if e.OccurredAt.After(s.LastErrorAt) {
			s.LastErrorAt = e.OccurredAt
		}
	}

	return s
}

// ── ErrorClassifier (port interface) ──

// ErrorClassifier classifies raw error data into structured SessionError entities.
// This is a port — concrete implementations (deterministic, LLM) live in infrastructure.
type ErrorClassifier interface {
	// Classify takes a raw, unclassified SessionError (with RawError, HTTPStatus, etc. populated)
	// and returns it with Category, Source, Message, and Confidence filled in.
	// The classifier MUST NOT modify fields that are already set (ID, SessionID, timing, etc.).
	Classify(err SessionError) SessionError

	// Name returns a short identifier for logging (e.g. "deterministic", "llm").
	Name() string
}
