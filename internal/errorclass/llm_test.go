package errorclass_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/errorclass"
	"github.com/ChristopherAparicio/aisync/internal/llm"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// mockLLMClient implements llm.Client for testing.
type mockLLMClient struct {
	response string
	err      error
}

func (m *mockLLMClient) Complete(_ context.Context, _ llm.CompletionRequest) (*llm.CompletionResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.CompletionResponse{
		Content: m.response,
		Model:   "test-model",
	}, nil
}

func TestLLMClassifier_Name(t *testing.T) {
	c := errorclass.NewLLMClassifier(errorclass.LLMClassifierConfig{
		Client: &mockLLMClient{},
	})
	if c.Name() != "llm" {
		t.Errorf("Name() = %q, want %q", c.Name(), "llm")
	}
}

func TestLLMClassifier_AlreadyClassified(t *testing.T) {
	c := errorclass.NewLLMClassifier(errorclass.LLMClassifierConfig{
		Client: &mockLLMClient{response: `{"category":"rate_limit","source":"provider","message":"shouldn't matter","is_retryable":true}`},
	})

	err := c.Classify(session.SessionError{
		Category: session.ErrorCategoryToolError,
		Message:  "Already classified",
	})

	// Should not override.
	if err.Category != session.ErrorCategoryToolError {
		t.Errorf("Category = %q, want %q (should not override)", err.Category, session.ErrorCategoryToolError)
	}
	if err.Message != "Already classified" {
		t.Errorf("Message = %q, want %q (should not override)", err.Message, "Already classified")
	}
}

func TestLLMClassifier_ClassifiesToolError(t *testing.T) {
	c := errorclass.NewLLMClassifier(errorclass.LLMClassifierConfig{
		Client: &mockLLMClient{
			response: `{"category":"tool_error","source":"tool","message":"Compilation failed","is_retryable":false}`,
		},
	})

	err := c.Classify(session.SessionError{
		ID:       "err-1",
		RawError: "go build: cannot find module providing package github.com/foo/bar",
		Source:   session.ErrorSourceTool,
		ToolName: "bash",
	})

	if err.Category != session.ErrorCategoryToolError {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryToolError)
	}
	if err.Source != session.ErrorSourceTool {
		t.Errorf("Source = %q, want %q", err.Source, session.ErrorSourceTool)
	}
	if err.Message != "Compilation failed" {
		t.Errorf("Message = %q, want %q", err.Message, "Compilation failed")
	}
	if err.IsRetryable {
		t.Error("expected IsRetryable=false")
	}
	if err.Confidence != "medium" {
		t.Errorf("Confidence = %q, want %q", err.Confidence, "medium")
	}
}

func TestLLMClassifier_ClassifiesNetworkError(t *testing.T) {
	c := errorclass.NewLLMClassifier(errorclass.LLMClassifierConfig{
		Client: &mockLLMClient{
			response: `{"category":"network_error","source":"provider","message":"DNS resolution failed","is_retryable":true}`,
		},
	})

	err := c.Classify(session.SessionError{
		ID:       "err-2",
		RawError: "getaddrinfo ENOTFOUND api.anthropic.com",
	})

	if err.Category != session.ErrorCategoryNetworkError {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryNetworkError)
	}
	if !err.IsRetryable {
		t.Error("expected IsRetryable=true for network error")
	}
}

func TestLLMClassifier_HandlesMarkdownCodeFences(t *testing.T) {
	c := errorclass.NewLLMClassifier(errorclass.LLMClassifierConfig{
		Client: &mockLLMClient{
			response: "```json\n{\"category\":\"auth_error\",\"source\":\"provider\",\"message\":\"Invalid API key\",\"is_retryable\":false}\n```",
		},
	})

	err := c.Classify(session.SessionError{
		ID:       "err-3",
		RawError: "invalid x-api-key header",
	})

	if err.Category != session.ErrorCategoryAuthError {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryAuthError)
	}
	if err.Message != "Invalid API key" {
		t.Errorf("Message = %q, want %q", err.Message, "Invalid API key")
	}
}

func TestLLMClassifier_LLMError_FallsBackToUnknown(t *testing.T) {
	c := errorclass.NewLLMClassifier(errorclass.LLMClassifierConfig{
		Client: &mockLLMClient{
			err: fmt.Errorf("connection refused"),
		},
	})

	err := c.Classify(session.SessionError{
		ID:       "err-4",
		RawError: "some ambiguous error",
	})

	if err.Category != session.ErrorCategoryUnknown {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryUnknown)
	}
	if err.Confidence != "low" {
		t.Errorf("Confidence = %q, want %q", err.Confidence, "low")
	}
}

func TestLLMClassifier_InvalidJSON_FallsBackToUnknown(t *testing.T) {
	c := errorclass.NewLLMClassifier(errorclass.LLMClassifierConfig{
		Client: &mockLLMClient{
			response: "I'm sorry, I can't classify that error.",
		},
	})

	err := c.Classify(session.SessionError{
		ID:       "err-5",
		RawError: "mysterious error",
	})

	if err.Category != session.ErrorCategoryUnknown {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryUnknown)
	}
}

func TestLLMClassifier_InvalidCategory_FallsBackToUnknown(t *testing.T) {
	c := errorclass.NewLLMClassifier(errorclass.LLMClassifierConfig{
		Client: &mockLLMClient{
			response: `{"category":"banana","source":"provider","message":"Something","is_retryable":false}`,
		},
	})

	err := c.Classify(session.SessionError{
		ID:       "err-6",
		RawError: "some error",
	})

	if err.Category != session.ErrorCategoryUnknown {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryUnknown)
	}
}

func TestLLMClassifier_InvalidSource_UsesOriginal(t *testing.T) {
	c := errorclass.NewLLMClassifier(errorclass.LLMClassifierConfig{
		Client: &mockLLMClient{
			response: `{"category":"tool_error","source":"banana","message":"Something broke","is_retryable":false}`,
		},
	})

	err := c.Classify(session.SessionError{
		ID:     "err-7",
		Source: session.ErrorSourceTool,
	})

	if err.Category != session.ErrorCategoryToolError {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryToolError)
	}
	// Should use original source since LLM returned invalid.
	if err.Source != session.ErrorSourceTool {
		t.Errorf("Source = %q, want %q (should keep original)", err.Source, session.ErrorSourceTool)
	}
}

func TestLLMClassifier_PreservesIdentityFields(t *testing.T) {
	c := errorclass.NewLLMClassifier(errorclass.LLMClassifierConfig{
		Client: &mockLLMClient{
			response: `{"category":"validation","source":"provider","message":"Bad request format","is_retryable":false}`,
		},
	})

	err := c.Classify(session.SessionError{
		ID:        "err-8",
		SessionID: "sess-abc",
		ToolName:  "bash",
		RawError:  "original raw error text",
	})

	// Identity fields must be preserved.
	if err.ID != "err-8" {
		t.Errorf("ID = %q, want %q", err.ID, "err-8")
	}
	if err.SessionID != "sess-abc" {
		t.Errorf("SessionID = %q, want %q", err.SessionID, "sess-abc")
	}
	if err.ToolName != "bash" {
		t.Errorf("ToolName = %q, want %q", err.ToolName, "bash")
	}
	if err.RawError != "original raw error text" {
		t.Errorf("RawError = %q, want original", err.RawError)
	}
}

func TestLLMClassifier_UnknownCategory_Passthrough(t *testing.T) {
	// LLM can explicitly return "unknown" which is valid.
	c := errorclass.NewLLMClassifier(errorclass.LLMClassifierConfig{
		Client: &mockLLMClient{
			response: `{"category":"unknown","source":"client","message":"Truly unclassifiable","is_retryable":false}`,
		},
	})

	err := c.Classify(session.SessionError{
		ID:       "err-9",
		RawError: "????? unknown error",
	})

	if err.Category != session.ErrorCategoryUnknown {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryUnknown)
	}
}
