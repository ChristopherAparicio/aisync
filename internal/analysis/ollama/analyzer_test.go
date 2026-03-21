package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// validReport is a well-formed JSON AnalysisReport for testing.
var validReport = analysis.AnalysisReport{
	Score:   72,
	Summary: "Generally efficient session with minor retry loops on file reads.",
	Problems: []analysis.Problem{
		{
			Severity:    analysis.SeverityMedium,
			Description: "Read tool called 3 times on the same file without caching.",
			ToolName:    "Read",
		},
	},
	Recommendations: []analysis.Recommendation{
		{
			Category:    analysis.CategoryTool,
			Title:       "Cache file reads",
			Description: "Implement read caching to avoid re-reading unchanged files.",
			Priority:    2,
		},
	},
	SkillSuggestions: []analysis.SkillSuggestion{
		{
			Name:        "file-cache",
			Description: "Cache file contents to reduce redundant reads.",
			Trigger:     "When the same file is read more than twice",
		},
	},
}

// testSession returns a minimal session for testing.
func testSession() session.Session {
	return session.Session{
		ID:       "test-sess-001",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Branch:   "main",
		Messages: []session.Message{
			{ID: "msg-1", Role: session.RoleUser, Content: "implement auth"},
			{ID: "msg-2", Role: session.RoleAssistant, Content: "I'll implement authentication.", ToolCalls: []session.ToolCall{
				{ID: "tc-1", Name: "Read", Input: "auth.go", State: session.ToolStateCompleted},
				{ID: "tc-2", Name: "Write", Input: "auth.go", State: session.ToolStateCompleted},
			}},
			{ID: "msg-3", Role: session.RoleUser, Content: "looks good, add tests"},
			{ID: "msg-4", Role: session.RoleAssistant, Content: "Adding tests now.", ToolCalls: []session.ToolCall{
				{ID: "tc-3", Name: "Write", Input: "auth_test.go", State: session.ToolStateCompleted},
				{ID: "tc-4", Name: "bash", Input: "go test ./...", State: session.ToolStateError},
			}},
		},
		TokenUsage: session.TokenUsage{
			InputTokens:  5000,
			OutputTokens: 3000,
			TotalTokens:  8000,
		},
	}
}

// newMockOllamaServer creates a test HTTP server that simulates Ollama /api/chat.
func newMockOllamaServer(t *testing.T, responseContent string, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/chat" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}

		// Verify request body structure.
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Model == "" {
			http.Error(w, "model required", http.StatusBadRequest)
			return
		}
		if req.Format != "json" {
			http.Error(w, "expected format=json", http.StatusBadRequest)
			return
		}
		if len(req.Messages) < 2 {
			http.Error(w, "expected at least 2 messages (system + user)", http.StatusBadRequest)
			return
		}

		resp := chatResponse{
			Model:     req.Model,
			CreatedAt: time.Now().Format(time.RFC3339),
			Message:   chatMessage{Role: "assistant", Content: responseContent},
			Done:      true,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestAnalyzer_Name(t *testing.T) {
	a := NewAnalyzer(Config{})
	if a.Name() != analysis.AdapterOllama {
		t.Errorf("Name() = %q, want %q", a.Name(), analysis.AdapterOllama)
	}
}

func TestAnalyzer_Defaults(t *testing.T) {
	a := NewAnalyzer(Config{})
	if a.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", a.baseURL, DefaultBaseURL)
	}
	if a.model != DefaultModel {
		t.Errorf("model = %q, want %q", a.model, DefaultModel)
	}
}

func TestAnalyzer_CustomConfig(t *testing.T) {
	a := NewAnalyzer(Config{
		BaseURL: "http://gpu-server:11434",
		Model:   "llama3.1:70b",
		Timeout: 300 * time.Second,
	})
	if a.baseURL != "http://gpu-server:11434" {
		t.Errorf("baseURL = %q, want %q", a.baseURL, "http://gpu-server:11434")
	}
	if a.model != "llama3.1:70b" {
		t.Errorf("model = %q, want %q", a.model, "llama3.1:70b")
	}
}

func TestAnalyze_Success(t *testing.T) {
	reportJSON, _ := json.Marshal(validReport)
	srv := newMockOllamaServer(t, string(reportJSON), http.StatusOK)
	defer srv.Close()

	a := NewAnalyzer(Config{
		BaseURL:    srv.URL,
		Model:      "test-model",
		HTTPClient: srv.Client(),
	})

	report, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session: testSession(),
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.Score != 72 {
		t.Errorf("Score = %d, want 72", report.Score)
	}
	if report.Summary == "" {
		t.Error("Summary is empty")
	}
	if len(report.Problems) != 1 {
		t.Errorf("Problems count = %d, want 1", len(report.Problems))
	}
	if len(report.Recommendations) != 1 {
		t.Errorf("Recommendations count = %d, want 1", len(report.Recommendations))
	}
	if len(report.SkillSuggestions) != 1 {
		t.Errorf("SkillSuggestions count = %d, want 1", len(report.SkillSuggestions))
	}
}

func TestAnalyze_MarkdownFencedJSON(t *testing.T) {
	// Some models wrap JSON in markdown code fences despite instructions.
	reportJSON, _ := json.Marshal(validReport)
	fenced := fmt.Sprintf("Here is the analysis:\n```json\n%s\n```", string(reportJSON))

	srv := newMockOllamaServer(t, fenced, http.StatusOK)
	defer srv.Close()

	a := NewAnalyzer(Config{BaseURL: srv.URL, HTTPClient: srv.Client()})

	report, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session: testSession(),
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.Score != 72 {
		t.Errorf("Score = %d, want 72", report.Score)
	}
}

func TestAnalyze_ScoreClamping(t *testing.T) {
	// Model returns score out of range.
	bad := validReport
	bad.Score = 150

	reportJSON, _ := json.Marshal(bad)
	srv := newMockOllamaServer(t, string(reportJSON), http.StatusOK)
	defer srv.Close()

	a := NewAnalyzer(Config{BaseURL: srv.URL, HTTPClient: srv.Client()})

	report, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session: testSession(),
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.Score != 100 {
		t.Errorf("Score = %d, want 100 (clamped)", report.Score)
	}
}

func TestAnalyze_EmptyMessages(t *testing.T) {
	a := NewAnalyzer(Config{})
	_, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session: session.Session{},
	})
	if err == nil {
		t.Fatal("expected error for empty messages")
	}
}

func TestAnalyze_EmptyResponse(t *testing.T) {
	srv := newMockOllamaServer(t, "", http.StatusOK)
	defer srv.Close()

	a := NewAnalyzer(Config{BaseURL: srv.URL, HTTPClient: srv.Client()})
	_, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session: testSession(),
	})
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestAnalyze_InvalidJSON(t *testing.T) {
	srv := newMockOllamaServer(t, "this is not json at all", http.StatusOK)
	defer srv.Close()

	a := NewAnalyzer(Config{BaseURL: srv.URL, HTTPClient: srv.Client()})
	_, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session: testSession(),
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestAnalyze_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer srv.Close()

	a := NewAnalyzer(Config{BaseURL: srv.URL, HTTPClient: srv.Client()})
	_, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session: testSession(),
	})
	if err == nil {
		t.Fatal("expected error for server error")
	}
}

func TestAnalyze_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow model — block until context cancelled.
		<-r.Context().Done()
	}))
	defer srv.Close()

	a := NewAnalyzer(Config{BaseURL: srv.URL, HTTPClient: srv.Client()})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := a.Analyze(ctx, analysis.AnalyzeRequest{
		Session: testSession(),
	})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestExtractJSON(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"direct json", `{"score":70}`, `{"score":70}`},
		{"markdown fenced", "```json\n{\"score\":70}\n```", `{"score":70}`},
		{"generic fenced", "```\n{\"score\":70}\n```", `{"score":70}`},
		{"preamble + json", "Here is my analysis:\n{\"score\":70}", `{"score":70}`},
		{"no json", "no json here", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractJSON(tc.raw)
			if got != tc.want {
				t.Errorf("extractJSON() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseReport_Valid(t *testing.T) {
	reportJSON, _ := json.Marshal(validReport)
	report, err := parseReport(string(reportJSON))
	if err != nil {
		t.Fatalf("parseReport() error = %v", err)
	}
	if report.Score != 72 {
		t.Errorf("Score = %d, want 72", report.Score)
	}
}

func TestParseReport_InvalidSeverity(t *testing.T) {
	bad := `{"score":50,"summary":"test","problems":[{"severity":"critical","description":"bad"}]}`
	_, err := parseReport(bad)
	if err == nil {
		t.Fatal("expected error for invalid severity")
	}
}

// ── Integration test ──
// Runs against a real Ollama instance if available.
// Skip in CI with: go test -short

func TestIntegration_OllamaAnalysis(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Check if Ollama is reachable.
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(DefaultBaseURL + "/api/tags")
	if err != nil {
		t.Skipf("Ollama not reachable at %s: %v", DefaultBaseURL, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("Ollama returned %d (expected 200)", resp.StatusCode)
	}

	// Try models in order of preference — use whatever's available locally.
	models := []string{"qwen3:8b", "qwen3:4b", "qwen3:1.7b", "llama3.2:3b", "gemma3:4b", "phi4-mini"}
	var model string
	for _, m := range models {
		checkReq := fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"hi"}],"stream":false}`, m)
		checkResp, checkErr := client.Post(DefaultBaseURL+"/api/chat", "application/json",
			bytes.NewBufferString(checkReq))
		if checkErr == nil && checkResp.StatusCode == http.StatusOK {
			_ = checkResp.Body.Close()
			model = m
			break
		}
		if checkResp != nil {
			_ = checkResp.Body.Close()
		}
	}
	if model == "" {
		t.Skipf("no suitable model found locally — tried %v", models)
	}

	t.Logf("Using Ollama model: %s", model)

	a := NewAnalyzer(Config{
		Model:   model,
		Timeout: 180 * time.Second, // generous timeout for integration
	})

	report, err := a.Analyze(context.Background(), analysis.AnalyzeRequest{
		Session: testSession(),
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	t.Logf("Report: score=%d summary=%.100s", report.Score, report.Summary)
	t.Logf("Problems: %d, Recommendations: %d, SkillSuggestions: %d",
		len(report.Problems), len(report.Recommendations), len(report.SkillSuggestions))

	if report.Score < 0 || report.Score > 100 {
		t.Errorf("Score out of range: %d", report.Score)
	}
	if report.Summary == "" {
		t.Error("Summary is empty")
	}
}
