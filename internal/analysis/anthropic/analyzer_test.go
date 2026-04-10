package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// validReport is a well-formed JSON AnalysisReport for testing.
var validReport = analysis.AnalysisReport{
	Score:   82,
	Summary: "Well-structured session with efficient tool usage.",
	Problems: []analysis.Problem{
		{
			Severity:    analysis.SeverityLow,
			Description: "Minor re-read of config file.",
			ToolName:    "Read",
		},
	},
	Recommendations: []analysis.Recommendation{
		{
			Category:    analysis.CategoryWorkflow,
			Title:       "Batch file reads",
			Description: "Read related files in a single pass.",
			Priority:    3,
		},
	},
}

// testSession returns a minimal session for testing.
func testSession() session.Session {
	return session.Session{
		ID:       "test-sess-001",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Messages: []session.Message{
			{ID: "msg-1", Role: session.RoleUser, Content: "implement auth"},
			{ID: "msg-2", Role: session.RoleAssistant, Content: "Done."},
		},
		TokenUsage: session.TokenUsage{TotalTokens: 5000},
	}
}

// newMockAnthropicServer creates a test HTTP server that simulates the Anthropic Messages API.
func newMockAnthropicServer(t *testing.T, responseText string, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// Verify headers.
		if r.Header.Get("x-api-key") == "" {
			http.Error(w, "missing api key", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("anthropic-version") != apiVersion {
			http.Error(w, "wrong api version", http.StatusBadRequest)
			return
		}

		// Verify body.
		var req messagesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.Model == "" {
			http.Error(w, "model required", http.StatusBadRequest)
			return
		}

		resp := messagesResponse{
			ID:   "msg_test",
			Type: "message",
			Role: "assistant",
			Content: []contentBlock{
				{Type: "text", Text: responseText},
			},
			Model:      req.Model,
			StopReason: "end_turn",
			Usage:      usage{InputTokens: 1000, OutputTokens: 500},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestNewAnalyzer_NoAPIKey(t *testing.T) {
	// Ensure env var is not set.
	orig := os.Getenv("ANTHROPIC_API_KEY")
	os.Unsetenv("ANTHROPIC_API_KEY")
	defer func() {
		if orig != "" {
			os.Setenv("ANTHROPIC_API_KEY", orig)
		}
	}()

	_, err := NewAnalyzer(Config{})
	if err == nil {
		t.Fatal("expected error when no API key is provided")
	}
}

func TestNewAnalyzer_APIKeyFromEnv(t *testing.T) {
	orig := os.Getenv("ANTHROPIC_API_KEY")
	os.Setenv("ANTHROPIC_API_KEY", "test-key-from-env")
	defer func() {
		if orig != "" {
			os.Setenv("ANTHROPIC_API_KEY", orig)
		} else {
			os.Unsetenv("ANTHROPIC_API_KEY")
		}
	}()

	a, err := NewAnalyzer(Config{})
	if err != nil {
		t.Fatalf("NewAnalyzer() error = %v", err)
	}
	if a.apiKey != "test-key-from-env" {
		t.Errorf("apiKey = %q, want %q", a.apiKey, "test-key-from-env")
	}
}

func TestNewAnalyzer_ConfigOverridesEnv(t *testing.T) {
	orig := os.Getenv("ANTHROPIC_API_KEY")
	os.Setenv("ANTHROPIC_API_KEY", "env-key")
	defer func() {
		if orig != "" {
			os.Setenv("ANTHROPIC_API_KEY", orig)
		} else {
			os.Unsetenv("ANTHROPIC_API_KEY")
		}
	}()

	a, err := NewAnalyzer(Config{APIKey: "config-key"})
	if err != nil {
		t.Fatalf("NewAnalyzer() error = %v", err)
	}
	if a.apiKey != "config-key" {
		t.Errorf("apiKey = %q, want %q (config should override env)", a.apiKey, "config-key")
	}
}

func TestAnalyzer_Name(t *testing.T) {
	a, _ := NewAnalyzer(Config{APIKey: "test"})
	if a.Name() != analysis.AdapterAnthropic {
		t.Errorf("Name() = %q, want %q", a.Name(), analysis.AdapterAnthropic)
	}
}

func TestAnalyzer_Defaults(t *testing.T) {
	a, _ := NewAnalyzer(Config{APIKey: "test"})
	if a.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", a.baseURL, DefaultBaseURL)
	}
	if a.model != DefaultModel {
		t.Errorf("model = %q, want %q", a.model, DefaultModel)
	}
}

func TestAnalyze_Success(t *testing.T) {
	reportJSON, _ := json.Marshal(validReport)
	srv := newMockAnthropicServer(t, string(reportJSON), http.StatusOK)
	defer srv.Close()

	a, _ := NewAnalyzer(Config{
		APIKey:     "test-key",
		BaseURL:    srv.URL,
		Model:      "claude-haiku-4-20250514",
		HTTPClient: srv.Client(),
	})

	report, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session: testSession(),
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.Score != 82 {
		t.Errorf("Score = %d, want 82", report.Score)
	}
	if len(report.Problems) != 1 {
		t.Errorf("Problems count = %d, want 1", len(report.Problems))
	}
	if len(report.Recommendations) != 1 {
		t.Errorf("Recommendations count = %d, want 1", len(report.Recommendations))
	}
}

func TestAnalyze_MarkdownFenced(t *testing.T) {
	reportJSON, _ := json.Marshal(validReport)
	fenced := fmt.Sprintf("```json\n%s\n```", string(reportJSON))

	srv := newMockAnthropicServer(t, fenced, http.StatusOK)
	defer srv.Close()

	a, _ := NewAnalyzer(Config{APIKey: "test", BaseURL: srv.URL, HTTPClient: srv.Client()})
	report, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{Session: testSession()})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.Score != 82 {
		t.Errorf("Score = %d, want 82", report.Score)
	}
}

func TestAnalyze_ScoreClamping(t *testing.T) {
	bad := validReport
	bad.Score = -5
	reportJSON, _ := json.Marshal(bad)

	srv := newMockAnthropicServer(t, string(reportJSON), http.StatusOK)
	defer srv.Close()

	a, _ := NewAnalyzer(Config{APIKey: "test", BaseURL: srv.URL, HTTPClient: srv.Client()})
	report, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{Session: testSession()})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.Score != 0 {
		t.Errorf("Score = %d, want 0 (clamped)", report.Score)
	}
}

func TestAnalyze_EmptyMessages(t *testing.T) {
	a, _ := NewAnalyzer(Config{APIKey: "test"})
	_, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{Session: session.Session{}})
	if err == nil {
		t.Fatal("expected error for empty messages")
	}
}

func TestAnalyze_EmptyResponse(t *testing.T) {
	srv := newMockAnthropicServer(t, "", http.StatusOK)
	defer srv.Close()

	a, _ := NewAnalyzer(Config{APIKey: "test", BaseURL: srv.URL, HTTPClient: srv.Client()})
	_, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{Session: testSession()})
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestAnalyze_InvalidJSON(t *testing.T) {
	srv := newMockAnthropicServer(t, "not json at all", http.StatusOK)
	defer srv.Close()

	a, _ := NewAnalyzer(Config{APIKey: "test", BaseURL: srv.URL, HTTPClient: srv.Client()})
	_, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{Session: testSession()})
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}

func TestAnalyze_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"type":"rate_limit_error","message":"too many requests"}}`)
	}))
	defer srv.Close()

	a, _ := NewAnalyzer(Config{APIKey: "test", BaseURL: srv.URL, HTTPClient: srv.Client()})
	_, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{Session: testSession()})
	if err == nil {
		t.Fatal("expected error for API 429")
	}
}

func TestAnalyze_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	a, _ := NewAnalyzer(Config{APIKey: "test", BaseURL: srv.URL, HTTPClient: srv.Client()})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := a.Analyze(ctx, analysis.AnalyzeRequest{Session: testSession()})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// ── Agentic tool use tests ──

// newAgenticMockServer creates a server that simulates tool_use → tool_result → final response.
// On the first call, it responds with a tool_use block. On the second call (with tool_result),
// it responds with the final text analysis report.
func newAgenticMockServer(t *testing.T, finalReport string) *httptest.Server {
	t.Helper()
	callCount := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		callCount++
		w.Header().Set("Content-Type", "application/json")

		if callCount == 1 {
			// First call: respond with tool_use.
			resp := messagesResponse{
				ID:   "msg_1",
				Type: "message",
				Role: "assistant",
				Content: []contentBlock{
					{Type: "text", Text: "Let me investigate the error details."},
					{
						Type:  "tool_use",
						ID:    "toolu_01",
						Name:  "query_session",
						Input: json.RawMessage(`{"action": "get_error_details", "limit": 5}`),
					},
				},
				StopReason: "tool_use",
				Usage:      usage{InputTokens: 2000, OutputTokens: 100},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		// Second call: respond with final text.
		resp := messagesResponse{
			ID:   "msg_2",
			Type: "message",
			Role: "assistant",
			Content: []contentBlock{
				{Type: "text", Text: finalReport},
			},
			StopReason: "end_turn",
			Usage:      usage{InputTokens: 3000, OutputTokens: 500},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestAnalyze_AgenticToolUse(t *testing.T) {
	reportJSON, _ := json.Marshal(validReport)
	srv := newAgenticMockServer(t, string(reportJSON))
	defer srv.Close()

	a, _ := NewAnalyzer(Config{
		APIKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	sess := testSession()
	sess.Messages = append(sess.Messages, session.Message{
		ID:   "msg-3",
		Role: session.RoleAssistant,
		ToolCalls: []session.ToolCall{
			{ID: "tc-1", Name: "Bash", State: session.ToolStateError, Input: "go test", Output: "FAIL"},
		},
	})

	report, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session:      sess,
		ToolExecutor: analysis.NewSessionToolExecutor(&sess),
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.Score != 82 {
		t.Errorf("Score = %d, want 82", report.Score)
	}
}

// newMaxIterationsServer always responds with tool_use, never end_turn.
func newMaxIterationsServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := messagesResponse{
			ID:   "msg_loop",
			Type: "message",
			Role: "assistant",
			Content: []contentBlock{
				{
					Type:  "tool_use",
					ID:    "toolu_loop",
					Name:  "query_session",
					Input: json.RawMessage(`{"action": "get_messages", "from": 0, "to": 1}`),
				},
			},
			StopReason: "tool_use",
			Usage:      usage{InputTokens: 100, OutputTokens: 50},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestAnalyze_MaxIterationsSafety(t *testing.T) {
	srv := newMaxIterationsServer(t)
	defer srv.Close()

	a, _ := NewAnalyzer(Config{
		APIKey:        "test-key",
		BaseURL:       srv.URL,
		HTTPClient:    srv.Client(),
		MaxIterations: 3, // Low cap for testing.
	})

	sess := testSession()
	_, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session:      sess,
		ToolExecutor: analysis.NewSessionToolExecutor(&sess),
	})

	// Should error because the model never produces a final text response.
	if err == nil {
		t.Fatal("expected error when max iterations reached without final response")
	}
	t.Logf("got expected error: %v", err)
}

func TestAnalyze_NoToolExecutor_NoTools(t *testing.T) {
	// When ToolExecutor is nil, no tools should be sent to the API.
	reportJSON, _ := json.Marshal(validReport)
	var receivedTools bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req messagesRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Tools) > 0 {
			receivedTools = true
		}

		resp := messagesResponse{
			ID:         "msg_no_tools",
			Type:       "message",
			Role:       "assistant",
			Content:    []contentBlock{{Type: "text", Text: string(reportJSON)}},
			StopReason: "end_turn",
			Usage:      usage{InputTokens: 1000, OutputTokens: 500},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	a, _ := NewAnalyzer(Config{APIKey: "test", BaseURL: srv.URL, HTTPClient: srv.Client()})
	_, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session: testSession(),
		// No ToolExecutor.
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if receivedTools {
		t.Error("tools should NOT be sent when ToolExecutor is nil")
	}
}

func TestAnalyze_WithToolExecutor_SendsTools(t *testing.T) {
	reportJSON, _ := json.Marshal(validReport)
	var receivedToolCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req messagesRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		receivedToolCount = len(req.Tools)

		resp := messagesResponse{
			ID:         "msg_with_tools",
			Type:       "message",
			Role:       "assistant",
			Content:    []contentBlock{{Type: "text", Text: string(reportJSON)}},
			StopReason: "end_turn",
			Usage:      usage{InputTokens: 1000, OutputTokens: 500},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	sess := testSession()
	a, _ := NewAnalyzer(Config{APIKey: "test", BaseURL: srv.URL, HTTPClient: srv.Client()})
	_, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session:      sess,
		ToolExecutor: analysis.NewSessionToolExecutor(&sess),
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if receivedToolCount != 1 {
		t.Errorf("received %d tools, want 1 (single polymorphic query_session)", receivedToolCount)
	}
}

// ── Integration test ──

func TestIntegration_AnthropicAnalysis(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set — skipping integration test")
	}

	a, err := NewAnalyzer(Config{
		APIKey:  apiKey,
		Model:   "claude-haiku-4-20250514", // cheapest model for testing
		Timeout: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewAnalyzer() error = %v", err)
	}

	report, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session: testSession(),
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	t.Logf("Report: score=%d summary=%.100s", report.Score, report.Summary)
	t.Logf("Problems: %d, Recommendations: %d", len(report.Problems), len(report.Recommendations))

	if report.Score < 0 || report.Score > 100 {
		t.Errorf("Score out of range: %d", report.Score)
	}
	if report.Summary == "" {
		t.Error("Summary is empty")
	}
}
