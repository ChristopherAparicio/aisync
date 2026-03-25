package errorclass_test

import (
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/errorclass"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestDeterministicClassifier_Name(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
	if c.Name() != "deterministic" {
		t.Errorf("Name() = %q, want %q", c.Name(), "deterministic")
	}
}

func TestClassify_HTTPStatus500(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
	err := c.Classify(session.SessionError{
		HTTPStatus: 500,
		RawError:   `{"type":"api_error","message":"Internal server error"}`,
		Headers: map[string]string{
			"anthropic-ratelimit-unified-7d-utilization":          "0.74",
			"anthropic-ratelimit-unified-overage-disabled-reason": "out_of_credits",
			"request-id":                    "req_011CZPV3zLJ8eBjUETk76q9s",
			"x-envoy-upstream-service-time": "18597",
		},
	})

	if err.Category != session.ErrorCategoryProviderError {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryProviderError)
	}
	if err.Source != session.ErrorSourceProvider {
		t.Errorf("Source = %q, want %q", err.Source, session.ErrorSourceProvider)
	}
	if err.Confidence != "high" {
		t.Errorf("Confidence = %q, want %q", err.Confidence, "high")
	}
	if !err.IsRetryable {
		t.Error("expected IsRetryable=true for 500")
	}
	if err.RequestID != "req_011CZPV3zLJ8eBjUETk76q9s" {
		t.Errorf("RequestID = %q, want req_011CZPV3zLJ8eBjUETk76q9s", err.RequestID)
	}
	if err.DurationMs != 18597 {
		t.Errorf("DurationMs = %d, want 18597", err.DurationMs)
	}
	if err.Message == "" {
		t.Error("Message should not be empty")
	}
	// Should mention out_of_credits.
	if !containsStr(err.Message, "out of overage credits") {
		t.Errorf("Message should mention overage credits, got: %q", err.Message)
	}
}

func TestClassify_HTTPStatus429(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
	err := c.Classify(session.SessionError{
		HTTPStatus: 429,
		Headers: map[string]string{
			"anthropic-ratelimit-unified-7d-utilization": "0.95",
			"anthropic-ratelimit-unified-overage-status": "rejected",
		},
	})

	if err.Category != session.ErrorCategoryRateLimit {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryRateLimit)
	}
	if !err.IsRetryable {
		t.Error("expected IsRetryable=true for 429")
	}
	if !containsStr(err.Message, "7d utilization: 0.95") {
		t.Errorf("Message should include utilization, got: %q", err.Message)
	}
}

func TestClassify_HTTPStatus401(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
	err := c.Classify(session.SessionError{HTTPStatus: 401})

	if err.Category != session.ErrorCategoryAuthError {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryAuthError)
	}
	if err.IsRetryable {
		t.Error("expected IsRetryable=false for 401")
	}
}

func TestClassify_HTTPStatus400_ContextOverflow(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
	err := c.Classify(session.SessionError{
		HTTPStatus: 400,
		RawError:   "prompt is too long: 204800 tokens > 200000 maximum context length",
	})

	if err.Category != session.ErrorCategoryContextOverflow {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryContextOverflow)
	}
}

func TestClassify_HTTPStatus400_EmptyContent(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
	err := c.Classify(session.SessionError{
		HTTPStatus: 400,
		RawError:   "The content field in the Message object at messages.11 is empty. Add a ContentBlock object to the content field and try again.",
	})

	if err.Category != session.ErrorCategoryValidation {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryValidation)
	}
	if !containsStr(err.Message, "Empty content") {
		t.Errorf("Message = %q, expected mention of empty content", err.Message)
	}
}

func TestClassify_HTTPStatus529_Overloaded(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
	err := c.Classify(session.SessionError{HTTPStatus: 529})

	if err.Category != session.ErrorCategoryProviderError {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryProviderError)
	}
	if !err.IsRetryable {
		t.Error("expected IsRetryable=true for 529")
	}
}

func TestClassify_HTTPStatus413(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
	err := c.Classify(session.SessionError{HTTPStatus: 413})

	if err.Category != session.ErrorCategoryContextOverflow {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryContextOverflow)
	}
}

func TestClassify_ToolError_PermissionDenied(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
	err := c.Classify(session.SessionError{
		Source:   session.ErrorSourceTool,
		ToolName: "bash",
		RawError: "bash: /etc/shadow: Permission denied",
	})

	if err.Category != session.ErrorCategoryAuthError {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryAuthError)
	}
	if err.Source != session.ErrorSourceTool {
		t.Errorf("Source = %q, want %q", err.Source, session.ErrorSourceTool)
	}
}

func TestClassify_ToolError_FileNotFound(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
	err := c.Classify(session.SessionError{
		Source:   session.ErrorSourceTool,
		ToolName: "Read",
		RawError: "open /tmp/nonexistent.txt: no such file or directory",
	})

	if err.Category != session.ErrorCategoryToolError {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryToolError)
	}
	if err.Message != "File not found" {
		t.Errorf("Message = %q, want %q", err.Message, "File not found")
	}
}

func TestClassify_ToolError_CommandNotFound(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
	err := c.Classify(session.SessionError{
		Source:   session.ErrorSourceTool,
		RawError: "zsh: command not found: foobar",
	})

	if err.Category != session.ErrorCategoryToolError {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryToolError)
	}
	if err.Message != "Command not found" {
		t.Errorf("Message = %q, want %q", err.Message, "Command not found")
	}
}

func TestClassify_ToolError_NetworkError(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
	err := c.Classify(session.SessionError{
		Source:   session.ErrorSourceTool,
		RawError: "connect ECONNREFUSED 127.0.0.1:5432",
	})

	if err.Category != session.ErrorCategoryNetworkError {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryNetworkError)
	}
}

func TestClassify_MessagePattern_Aborted(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
	err := c.Classify(session.SessionError{
		RawError: "The operation was aborted.",
	})

	if err.Category != session.ErrorCategoryAborted {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryAborted)
	}
	if err.Source != session.ErrorSourceClient {
		t.Errorf("Source = %q, want %q", err.Source, session.ErrorSourceClient)
	}
}

func TestClassify_MessagePattern_InternalServerError(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
	err := c.Classify(session.SessionError{
		RawError: "Internal server error",
	})

	if err.Category != session.ErrorCategoryProviderError {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryProviderError)
	}
}

func TestClassify_AlreadyClassified(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
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

func TestClassify_Empty(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
	err := c.Classify(session.SessionError{})

	if err.Category != session.ErrorCategoryUnknown {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryUnknown)
	}
	if err.Confidence != "low" {
		t.Errorf("Confidence = %q, want %q", err.Confidence, "low")
	}
}

func TestClassify_ToolError_ExitCode(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
	err := c.Classify(session.SessionError{
		Source:   session.ErrorSourceTool,
		ToolName: "bash",
		RawError: "Process exited with exit code 1",
	})

	if err.Category != session.ErrorCategoryToolError {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryToolError)
	}
	if err.Confidence != "high" {
		t.Errorf("Confidence = %q, want %q", err.Confidence, "high")
	}
}

func TestClassify_ToolError_OOM(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
	err := c.Classify(session.SessionError{
		Source:   session.ErrorSourceTool,
		RawError: "fatal error: out of memory",
	})

	if err.Category != session.ErrorCategoryToolError {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryToolError)
	}
	if err.Message != "Out of memory" {
		t.Errorf("Message = %q, want %q", err.Message, "Out of memory")
	}
}

func TestClassify_ToolError_DiskFull(t *testing.T) {
	c := errorclass.NewDeterministicClassifier()
	err := c.Classify(session.SessionError{
		Source:   session.ErrorSourceTool,
		RawError: "write /tmp/output: no space left on device",
	})

	if err.Category != session.ErrorCategoryToolError {
		t.Errorf("Category = %q, want %q", err.Category, session.ErrorCategoryToolError)
	}
	if err.Message != "Disk full" {
		t.Errorf("Message = %q, want %q", err.Message, "Disk full")
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsLower(s, substr))
}

func containsLower(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) && findSubstr(s, substr)
}

func findSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
