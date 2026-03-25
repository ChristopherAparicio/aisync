package session_test

import (
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestErrorCategory_Valid(t *testing.T) {
	tests := []struct {
		cat  session.ErrorCategory
		want bool
	}{
		{session.ErrorCategoryProviderError, true},
		{session.ErrorCategoryRateLimit, true},
		{session.ErrorCategoryContextOverflow, true},
		{session.ErrorCategoryAuthError, true},
		{session.ErrorCategoryValidation, true},
		{session.ErrorCategoryToolError, true},
		{session.ErrorCategoryNetworkError, true},
		{session.ErrorCategoryAborted, true},
		{session.ErrorCategoryUnknown, true},
		{"invalid_category", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(string(tt.cat), func(t *testing.T) {
			if got := tt.cat.Valid(); got != tt.want {
				t.Errorf("ErrorCategory(%q).Valid() = %v, want %v", tt.cat, got, tt.want)
			}
		})
	}
}

func TestErrorCategory_IsExternal(t *testing.T) {
	tests := []struct {
		cat  session.ErrorCategory
		want bool
	}{
		{session.ErrorCategoryProviderError, true},
		{session.ErrorCategoryRateLimit, true},
		{session.ErrorCategoryNetworkError, true},
		{session.ErrorCategoryToolError, false},
		{session.ErrorCategoryContextOverflow, false},
		{session.ErrorCategoryAuthError, false},
		{session.ErrorCategoryAborted, false},
		{session.ErrorCategoryUnknown, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.cat), func(t *testing.T) {
			if got := tt.cat.IsExternal(); got != tt.want {
				t.Errorf("ErrorCategory(%q).IsExternal() = %v, want %v", tt.cat, got, tt.want)
			}
		})
	}
}

func TestErrorSource_Valid(t *testing.T) {
	tests := []struct {
		src  session.ErrorSource
		want bool
	}{
		{session.ErrorSourceProvider, true},
		{session.ErrorSourceTool, true},
		{session.ErrorSourceClient, true},
		{"invalid", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(string(tt.src), func(t *testing.T) {
			if got := tt.src.Valid(); got != tt.want {
				t.Errorf("ErrorSource(%q).Valid() = %v, want %v", tt.src, got, tt.want)
			}
		})
	}
}

func TestSessionError_IsExternal(t *testing.T) {
	providerErr := session.SessionError{Category: session.ErrorCategoryProviderError}
	toolErr := session.SessionError{Category: session.ErrorCategoryToolError}

	if !providerErr.IsExternal() {
		t.Error("expected provider error to be external")
	}
	if toolErr.IsExternal() {
		t.Error("expected tool error to NOT be external")
	}
}

func TestNewSessionErrorSummary(t *testing.T) {
	now := time.Now()
	sessionID := session.ID("test-session")

	errors := []session.SessionError{
		{
			Category:   session.ErrorCategoryProviderError,
			Source:     session.ErrorSourceProvider,
			OccurredAt: now.Add(-10 * time.Minute),
		},
		{
			Category:   session.ErrorCategoryProviderError,
			Source:     session.ErrorSourceProvider,
			OccurredAt: now.Add(-5 * time.Minute),
		},
		{
			Category:   session.ErrorCategoryToolError,
			Source:     session.ErrorSourceTool,
			OccurredAt: now.Add(-2 * time.Minute),
		},
		{
			Category:   session.ErrorCategoryRateLimit,
			Source:     session.ErrorSourceProvider,
			OccurredAt: now,
		},
	}

	summary := session.NewSessionErrorSummary(sessionID, errors)

	if summary.SessionID != sessionID {
		t.Errorf("SessionID = %v, want %v", summary.SessionID, sessionID)
	}
	if summary.TotalErrors != 4 {
		t.Errorf("TotalErrors = %d, want 4", summary.TotalErrors)
	}
	if summary.ExternalErrors != 3 {
		t.Errorf("ExternalErrors = %d, want 3 (provider_error x2 + rate_limit x1)", summary.ExternalErrors)
	}
	if summary.InternalErrors != 1 {
		t.Errorf("InternalErrors = %d, want 1 (tool_error x1)", summary.InternalErrors)
	}
	if summary.ByCategory[session.ErrorCategoryProviderError] != 2 {
		t.Errorf("ByCategory[provider_error] = %d, want 2", summary.ByCategory[session.ErrorCategoryProviderError])
	}
	if summary.ByCategory[session.ErrorCategoryToolError] != 1 {
		t.Errorf("ByCategory[tool_error] = %d, want 1", summary.ByCategory[session.ErrorCategoryToolError])
	}
	if summary.ByCategory[session.ErrorCategoryRateLimit] != 1 {
		t.Errorf("ByCategory[rate_limit] = %d, want 1", summary.ByCategory[session.ErrorCategoryRateLimit])
	}
	if summary.BySource[session.ErrorSourceProvider] != 3 {
		t.Errorf("BySource[provider] = %d, want 3", summary.BySource[session.ErrorSourceProvider])
	}
	if summary.BySource[session.ErrorSourceTool] != 1 {
		t.Errorf("BySource[tool] = %d, want 1", summary.BySource[session.ErrorSourceTool])
	}
	if !summary.FirstErrorAt.Equal(now.Add(-10 * time.Minute)) {
		t.Errorf("FirstErrorAt = %v, want %v", summary.FirstErrorAt, now.Add(-10*time.Minute))
	}
	if !summary.LastErrorAt.Equal(now) {
		t.Errorf("LastErrorAt = %v, want %v", summary.LastErrorAt, now)
	}
}

func TestNewSessionErrorSummary_Empty(t *testing.T) {
	summary := session.NewSessionErrorSummary("empty-session", nil)

	if summary.TotalErrors != 0 {
		t.Errorf("TotalErrors = %d, want 0", summary.TotalErrors)
	}
	if summary.ExternalErrors != 0 {
		t.Errorf("ExternalErrors = %d, want 0", summary.ExternalErrors)
	}
}
