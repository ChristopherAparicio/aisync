package errorclass_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/errorclass"
	"github.com/ChristopherAparicio/aisync/internal/llm"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// mockErrorStore implements storage.ErrorStore for integration testing.
type mockErrorStore struct {
	saved []session.SessionError
}

func (m *mockErrorStore) SaveErrors(errors []session.SessionError) error {
	m.saved = append(m.saved, errors...)
	return nil
}

func (m *mockErrorStore) GetErrors(id session.ID) ([]session.SessionError, error) {
	var result []session.SessionError
	for _, e := range m.saved {
		if e.SessionID == id {
			result = append(result, e)
		}
	}
	return result, nil
}

func (m *mockErrorStore) GetErrorSummary(id session.ID) (*session.SessionErrorSummary, error) {
	errors, _ := m.GetErrors(id)
	s := session.NewSessionErrorSummary(id, errors)
	return &s, nil
}

func (m *mockErrorStore) ListRecentErrors(limit int, category session.ErrorCategory) ([]session.SessionError, error) {
	return m.saved, nil
}

// smartLLMMock returns different classifications based on the error content.
type smartLLMMock struct{}

func (m *smartLLMMock) Complete(_ context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	// Simulate the LLM classifying based on keywords in the prompt.
	prompt := req.UserPrompt

	switch {
	case containsSubstr(prompt, "TypeScript"):
		return &llm.CompletionResponse{
			Content: `{"category":"tool_error","source":"tool","message":"TypeScript compilation error","is_retryable":false}`,
		}, nil
	case containsSubstr(prompt, "segmentation fault"):
		return &llm.CompletionResponse{
			Content: `{"category":"tool_error","source":"tool","message":"Process crashed with segfault","is_retryable":false}`,
		}, nil
	case containsSubstr(prompt, "gateway"):
		return &llm.CompletionResponse{
			Content: `{"category":"network_error","source":"provider","message":"Gateway routing failure","is_retryable":true}`,
		}, nil
	default:
		return &llm.CompletionResponse{
			Content: `{"category":"unknown","source":"client","message":"Could not classify","is_retryable":false}`,
		}, nil
	}
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestIntegration_ClassifierCascade_FullPipeline tests the complete error classification
// pipeline: raw errors → CompositeClassifier (deterministic + LLM) → ErrorService → store.
func TestIntegration_ClassifierCascade_FullPipeline(t *testing.T) {
	// Build the composite classifier: deterministic first, LLM fallback.
	composite := errorclass.NewCompositeClassifier(errorclass.CompositeClassifierConfig{
		Classifiers: []session.ErrorClassifier{
			errorclass.NewDeterministicClassifier(),
			errorclass.NewLLMClassifier(errorclass.LLMClassifierConfig{
				Client: &smartLLMMock{},
			}),
		},
	})

	store := &mockErrorStore{}
	errSvc := service.NewErrorService(service.ErrorServiceConfig{
		Store:      store,
		Classifier: composite,
	})

	// Build a session with a mix of errors that exercise different classifier paths.
	sess := &session.Session{
		ID: "ses_cascade_test",
		Errors: []session.SessionError{
			// Error 1: HTTP 500 — deterministic handles this (high confidence).
			{
				ID:         "err-500",
				SessionID:  "ses_cascade_test",
				HTTPStatus: 500,
				RawError:   `{"type":"api_error","message":"Internal server error"}`,
				OccurredAt: time.Now().Add(-5 * time.Minute),
			},
			// Error 2: HTTP 429 — deterministic handles rate limits.
			{
				ID:         "err-429",
				SessionID:  "ses_cascade_test",
				HTTPStatus: 429,
				RawError:   "rate limit exceeded",
				OccurredAt: time.Now().Add(-4 * time.Minute),
			},
			// Error 3: Tool error with known pattern — deterministic handles.
			{
				ID:         "err-notfound",
				SessionID:  "ses_cascade_test",
				Source:     session.ErrorSourceTool,
				ToolName:   "Read",
				RawError:   "open /tmp/missing.txt: no such file or directory",
				OccurredAt: time.Now().Add(-3 * time.Minute),
			},
			// Error 4: Ambiguous error — deterministic returns unknown, LLM classifies.
			{
				ID:         "err-ts",
				SessionID:  "ses_cascade_test",
				RawError:   "TypeScript TS2345: Argument of type 'string' is not assignable to parameter of type 'number'",
				OccurredAt: time.Now().Add(-2 * time.Minute),
			},
			// Error 5: Another ambiguous error — LLM classifies as tool_error.
			{
				ID:         "err-segfault",
				SessionID:  "ses_cascade_test",
				Source:     session.ErrorSourceTool,
				ToolName:   "bash",
				RawError:   "child process exited due to segmentation fault",
				OccurredAt: time.Now().Add(-1 * time.Minute),
			},
			// Error 6: Already classified — should be skipped entirely.
			{
				ID:         "err-already",
				SessionID:  "ses_cascade_test",
				Category:   session.ErrorCategoryAborted,
				Message:    "User cancelled",
				Confidence: "high",
				OccurredAt: time.Now(),
			},
		},
	}

	result, err := errSvc.ProcessSession(sess)
	if err != nil {
		t.Fatalf("ProcessSession: %v", err)
	}

	// Verify result counts.
	if result.Processed != 5 {
		t.Errorf("Processed = %d, want 5", result.Processed)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 (already classified)", result.Skipped)
	}

	// Verify all 6 errors were saved.
	if len(store.saved) != 6 {
		t.Fatalf("saved %d errors, want 6", len(store.saved))
	}

	// Build a map for easy lookup.
	errMap := make(map[string]session.SessionError)
	for _, e := range store.saved {
		errMap[e.ID] = e
	}

	// Error 1: HTTP 500 → deterministic → provider_error.
	e1 := errMap["err-500"]
	if e1.Category != session.ErrorCategoryProviderError {
		t.Errorf("err-500: category = %s, want provider_error", e1.Category)
	}
	if e1.Source != session.ErrorSourceProvider {
		t.Errorf("err-500: source = %s, want provider", e1.Source)
	}
	if e1.Confidence != "high" {
		t.Errorf("err-500: confidence = %s, want high", e1.Confidence)
	}
	if !e1.IsRetryable {
		t.Error("err-500: expected retryable")
	}

	// Error 2: HTTP 429 → deterministic → rate_limit.
	e2 := errMap["err-429"]
	if e2.Category != session.ErrorCategoryRateLimit {
		t.Errorf("err-429: category = %s, want rate_limit", e2.Category)
	}
	if !e2.IsRetryable {
		t.Error("err-429: expected retryable")
	}

	// Error 3: Tool "no such file or directory" → deterministic → tool_error.
	e3 := errMap["err-notfound"]
	if e3.Category != session.ErrorCategoryToolError {
		t.Errorf("err-notfound: category = %s, want tool_error", e3.Category)
	}
	if e3.Message != "File not found" {
		t.Errorf("err-notfound: message = %q, want 'File not found'", e3.Message)
	}

	// Error 4: TypeScript error → deterministic unknown → LLM → tool_error.
	e4 := errMap["err-ts"]
	if e4.Category != session.ErrorCategoryToolError {
		t.Errorf("err-ts: category = %s, want tool_error (LLM classified)", e4.Category)
	}
	if e4.Message != "TypeScript compilation error" {
		t.Errorf("err-ts: message = %q, want 'TypeScript compilation error'", e4.Message)
	}
	if e4.Confidence != "medium" {
		t.Errorf("err-ts: confidence = %s, want medium (LLM)", e4.Confidence)
	}

	// Error 5: Segfault — deterministic may catch "exit code/status" pattern,
	// but "segmentation fault" is not in deterministic patterns → LLM fallback.
	e5 := errMap["err-segfault"]
	if e5.Category != session.ErrorCategoryToolError {
		t.Errorf("err-segfault: category = %s, want tool_error", e5.Category)
	}

	// Error 6: Already classified → preserved as-is.
	e6 := errMap["err-already"]
	if e6.Category != session.ErrorCategoryAborted {
		t.Errorf("err-already: category = %s, want aborted (preserved)", e6.Category)
	}
	if e6.Message != "User cancelled" {
		t.Errorf("err-already: message = %q, want 'User cancelled'", e6.Message)
	}
}

// TestIntegration_ClassifierCascade_AllDeterministic tests that when all errors
// are classifiable by deterministic rules, the LLM is never called.
func TestIntegration_ClassifierCascade_AllDeterministic(t *testing.T) {
	llmCallCount := 0
	llmMock := &countingLLMMock{count: &llmCallCount}

	composite := errorclass.NewCompositeClassifier(errorclass.CompositeClassifierConfig{
		Classifiers: []session.ErrorClassifier{
			errorclass.NewDeterministicClassifier(),
			errorclass.NewLLMClassifier(errorclass.LLMClassifierConfig{
				Client: llmMock,
			}),
		},
	})

	store := &mockErrorStore{}
	errSvc := service.NewErrorService(service.ErrorServiceConfig{
		Store:      store,
		Classifier: composite,
	})

	sess := &session.Session{
		ID: "ses_all_det",
		Errors: []session.SessionError{
			{ID: "e1", SessionID: "ses_all_det", HTTPStatus: 500, OccurredAt: time.Now()},
			{ID: "e2", SessionID: "ses_all_det", HTTPStatus: 429, OccurredAt: time.Now()},
			{ID: "e3", SessionID: "ses_all_det", HTTPStatus: 401, OccurredAt: time.Now()},
			{ID: "e4", SessionID: "ses_all_det", Source: session.ErrorSourceTool, ToolName: "bash", RawError: "command not found: foobar", OccurredAt: time.Now()},
		},
	}

	result, err := errSvc.ProcessSession(sess)
	if err != nil {
		t.Fatal(err)
	}

	if result.Processed != 4 {
		t.Errorf("Processed = %d, want 4", result.Processed)
	}
	if llmCallCount != 0 {
		t.Errorf("LLM was called %d times, want 0 (all deterministic)", llmCallCount)
	}
}

// TestIntegration_ClassifierCascade_LLMFailure tests graceful fallback
// when the LLM returns errors — errors stay as "unknown".
func TestIntegration_ClassifierCascade_LLMFailure(t *testing.T) {
	composite := errorclass.NewCompositeClassifier(errorclass.CompositeClassifierConfig{
		Classifiers: []session.ErrorClassifier{
			errorclass.NewDeterministicClassifier(),
			errorclass.NewLLMClassifier(errorclass.LLMClassifierConfig{
				Client: &failingLLMMock{},
			}),
		},
	})

	store := &mockErrorStore{}
	errSvc := service.NewErrorService(service.ErrorServiceConfig{
		Store:      store,
		Classifier: composite,
	})

	sess := &session.Session{
		ID: "ses_llm_fail",
		Errors: []session.SessionError{
			// This error has no HTTP status and no matching pattern → deterministic returns unknown.
			// LLM also fails → stays unknown.
			{
				ID:         "e-fail",
				SessionID:  "ses_llm_fail",
				RawError:   "some completely novel error that nobody has seen before",
				OccurredAt: time.Now(),
			},
		},
	}

	result, err := errSvc.ProcessSession(sess)
	if err != nil {
		t.Fatal(err)
	}

	if result.Processed != 1 {
		t.Errorf("Processed = %d, want 1", result.Processed)
	}

	// Error should be saved as unknown.
	if len(store.saved) != 1 {
		t.Fatalf("saved = %d, want 1", len(store.saved))
	}
	if store.saved[0].Category != session.ErrorCategoryUnknown {
		t.Errorf("category = %s, want unknown", store.saved[0].Category)
	}
}

// TestIntegration_ClassifierCascade_DeterministicOnly tests the non-composite
// (deterministic-only) path to ensure it still works end-to-end.
func TestIntegration_ClassifierCascade_DeterministicOnly(t *testing.T) {
	store := &mockErrorStore{}
	errSvc := service.NewErrorService(service.ErrorServiceConfig{
		Store:      store,
		Classifier: errorclass.NewDeterministicClassifier(),
	})

	sess := &session.Session{
		ID: "ses_det_only",
		Errors: []session.SessionError{
			{ID: "e1", SessionID: "ses_det_only", HTTPStatus: 500, OccurredAt: time.Now()},
			{ID: "e2", SessionID: "ses_det_only", RawError: "totally unknown stuff", OccurredAt: time.Now()},
		},
	}

	result, err := errSvc.ProcessSession(sess)
	if err != nil {
		t.Fatal(err)
	}

	if result.Processed != 2 {
		t.Errorf("Processed = %d, want 2", result.Processed)
	}

	errMap := make(map[string]session.SessionError)
	for _, e := range store.saved {
		errMap[e.ID] = e
	}

	if errMap["e1"].Category != session.ErrorCategoryProviderError {
		t.Errorf("e1 category = %s, want provider_error", errMap["e1"].Category)
	}
	if errMap["e2"].Category != session.ErrorCategoryUnknown {
		t.Errorf("e2 category = %s, want unknown", errMap["e2"].Category)
	}
}

// ── Mock helpers ──

// countingLLMMock counts LLM calls.
type countingLLMMock struct {
	count *int
}

func (m *countingLLMMock) Complete(_ context.Context, _ llm.CompletionRequest) (*llm.CompletionResponse, error) {
	*m.count++
	return &llm.CompletionResponse{
		Content: `{"category":"tool_error","source":"tool","message":"LLM classified","is_retryable":false}`,
	}, nil
}

// failingLLMMock always returns an error.
type failingLLMMock struct{}

func (m *failingLLMMock) Complete(_ context.Context, _ llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return nil, fmt.Errorf("LLM service unavailable")
}
