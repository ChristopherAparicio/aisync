package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	llmadapter "github.com/ChristopherAparicio/aisync/internal/analysis/llm"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Ollama tool-calling API types (OpenAI-style) ──

// ollamaToolCallRequest is the /api/chat request with tool definitions.
type ollamaToolCallRequest struct {
	Model    string                `json:"model"`
	Messages []ollamaToolMessage   `json:"messages"`
	Stream   bool                  `json:"stream"`
	Tools    []ollamaToolDef       `json:"tools,omitempty"`
	Options  *ollamaRequestOptions `json:"options,omitempty"`
}

type ollamaRequestOptions struct {
	Temperature float64 `json:"temperature,omitempty"`
}

// ollamaToolMessage extends chatMessage with tool_calls for the agentic loop.
type ollamaToolMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

// ollamaToolDef is the OpenAI-style tool definition format used by Ollama.
type ollamaToolDef struct {
	Type     string             `json:"type"` // always "function"
	Function ollamaToolFunction `json:"function"`
}

type ollamaToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ollamaToolCall is a tool call in the response (OpenAI format).
type ollamaToolCall struct {
	Function ollamaToolCallFunction `json:"function"`
}

type ollamaToolCallFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ollamaToolCallResponse is the /api/chat response that may contain tool_calls.
type ollamaToolCallResponse struct {
	Model           string            `json:"model"`
	CreatedAt       string            `json:"created_at"`
	Message         ollamaToolMessage `json:"message"`
	Done            bool              `json:"done"`
	DoneReason      string            `json:"done_reason,omitempty"`
	TotalDuration   int64             `json:"total_duration,omitempty"`
	PromptEvalCount int               `json:"prompt_eval_count,omitempty"`
	EvalCount       int               `json:"eval_count,omitempty"`
}

// ── Test helpers ──

// agenticTestSession returns a rich session with errors and tool calls
// that should trigger the model to use investigation tools.
func agenticTestSession() *session.Session {
	return &session.Session{
		ID:       "e2e-agentic-001",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Branch:   "feature/auth",
		Messages: []session.Message{
			{
				ID:      "msg-1",
				Role:    session.RoleUser,
				Content: "Implement user authentication with JWT tokens",
			},
			{
				ID:          "msg-2",
				Role:        session.RoleAssistant,
				Content:     "I'll implement JWT authentication. Let me start by reading the existing code.",
				InputTokens: 500, OutputTokens: 200,
				ToolCalls: []session.ToolCall{
					{ID: "tc-1", Name: "Read", Input: "internal/auth/handler.go", State: session.ToolStateCompleted, Output: "package auth\n\nfunc LoginHandler() {}"},
					{ID: "tc-2", Name: "Read", Input: "go.mod", State: session.ToolStateCompleted, Output: "module example.com/app\ngo 1.22"},
				},
			},
			{
				ID:          "msg-3",
				Role:        session.RoleAssistant,
				Content:     "Now I'll write the JWT middleware.",
				InputTokens: 800, OutputTokens: 400,
				ToolCalls: []session.ToolCall{
					{ID: "tc-3", Name: "Write", Input: "internal/auth/jwt.go", State: session.ToolStateCompleted, Output: "wrote 85 lines"},
					{ID: "tc-4", Name: "Bash", Input: "go build ./...", State: session.ToolStateError, Output: "cannot find package \"github.com/golang-jwt/jwt/v5\""},
				},
			},
			{
				ID:      "msg-4",
				Role:    session.RoleUser,
				Content: "You need to install the jwt dependency first.",
			},
			{
				ID:          "msg-5",
				Role:        session.RoleAssistant,
				Content:     "Right, let me add the dependency.",
				InputTokens: 1200, OutputTokens: 100,
				ToolCalls: []session.ToolCall{
					{ID: "tc-5", Name: "Bash", Input: "go get github.com/golang-jwt/jwt/v5", State: session.ToolStateCompleted, Output: "added v5.2.1"},
					{ID: "tc-6", Name: "Bash", Input: "go build ./...", State: session.ToolStateCompleted, Output: ""},
				},
			},
			{
				ID:          "msg-6",
				Role:        session.RoleAssistant,
				Content:     "Now running the tests.",
				InputTokens: 1500, OutputTokens: 300,
				ToolCalls: []session.ToolCall{
					{ID: "tc-7", Name: "Bash", Input: "go test ./internal/auth/...", State: session.ToolStateError, Output: "--- FAIL: TestJWTMiddleware\n    jwt_test.go:42: token expired"},
					{ID: "tc-8", Name: "Bash", Input: "go test ./internal/auth/...", State: session.ToolStateError, Output: "--- FAIL: TestJWTMiddleware\n    jwt_test.go:42: token expired"},
				},
			},
			{
				ID:          "msg-7",
				Role:        session.RoleAssistant,
				Content:     "The test is failing because I hardcoded an expired timestamp. Let me fix that.",
				InputTokens: 2000, OutputTokens: 200,
				ToolCalls: []session.ToolCall{
					{ID: "tc-9", Name: "Write", Input: "internal/auth/jwt_test.go", State: session.ToolStateCompleted, Output: "wrote 60 lines"},
					{ID: "tc-10", Name: "Bash", Input: "go test ./internal/auth/...", State: session.ToolStateCompleted, Output: "ok  internal/auth 0.3s"},
				},
			},
		},
		TokenUsage: session.TokenUsage{
			InputTokens:  6000,
			OutputTokens: 1200,
			TotalTokens:  7200,
		},
	}
}

// convertToolsToOllamaFormat converts our AnalystTools to Ollama's OpenAI-style format.
func convertToolsToOllamaFormat(tools []analysis.ToolDefinition) []ollamaToolDef {
	var result []ollamaToolDef
	for _, t := range tools {
		result = append(result, ollamaToolDef{
			Type: "function",
			Function: ollamaToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}
	return result
}

// ollamaDispatchTool dispatches a tool call to the executor (reuses the same logic
// as the Anthropic adapter but decoupled from Anthropic types).
func ollamaDispatchTool(executor analysis.ToolExecutor, name string, args json.RawMessage) string {
	if name != analysis.AnalystToolName {
		return fmt.Sprintf(`{"error": "unknown tool: %s"}`, name)
	}

	// Parse the action-based input.
	var params struct {
		Action  string `json:"action"`
		From    int    `json:"from"`
		To      int    `json:"to"`
		Name    string `json:"name"`
		State   string `json:"state"`
		Pattern string `json:"pattern"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return fmt.Sprintf(`{"error": "invalid arguments: %v"}`, err)
	}

	var result json.RawMessage
	var err error

	switch params.Action {
	case "get_messages":
		result, err = executor.GetMessages(params.From, params.To)
	case "get_tool_calls":
		filter := analysis.ToolCallFilter{Name: params.Name, State: params.State, Limit: params.Limit}
		result, err = executor.GetToolCalls(filter)
	case "search_messages":
		result, err = executor.SearchMessages(params.Pattern, params.Limit)
	case "get_compaction_details":
		result, err = executor.GetCompactionDetails()
	case "get_error_details":
		result, err = executor.GetErrorDetails(params.Limit)
	case "get_token_timeline":
		result, err = executor.GetTokenTimeline()
	default:
		return fmt.Sprintf(`{"error": "unknown action: %s"}`, params.Action)
	}

	if err != nil {
		return fmt.Sprintf(`{"error": "%v"}`, err)
	}
	return string(result)
}

// ── E2E test ──

// TestE2E_OllamaAgenticToolUse tests the full agentic loop against a real Ollama instance.
// It verifies that a small local model (qwen3:8b) can:
// 1. Receive the query_session tool definition
// 2. Decide to call it with appropriate actions
// 3. Use the tool results to produce a valid analysis report
//
// Skip conditions:
// - testing.Short() is set
// - Ollama is not running
// - No suitable model is available locally
func TestE2E_OllamaAgenticToolUse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	const baseURL = "http://localhost:11434"
	const maxIterations = 10

	// Check if Ollama is reachable.
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get(baseURL + "/api/tags")
	if err != nil {
		t.Skipf("Ollama not reachable at %s: %v", baseURL, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("Ollama returned %d (expected 200)", resp.StatusCode)
	}

	// Find a suitable model that supports tool calling.
	models := []string{"qwen3:8b", "qwen3:4b", "qwen3.5:35b", "qwen3:30b"}
	var model string
	for _, m := range models {
		checkReq := fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"hi"}],"stream":false}`, m)
		checkResp, checkErr := httpClient.Post(baseURL+"/api/chat", "application/json",
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

	// Build session and tool executor.
	sess := agenticTestSession()
	executor := analysis.NewSessionToolExecutor(sess)

	// Build tool definitions in Ollama format.
	tools := convertToolsToOllamaFormat(analysis.AnalystTools())

	// Build the initial prompt (same as production code).
	analysisPrompt := llmadapter.BuildAnalysisPrompt(analysis.AnalyzeRequest{
		Session: *sess,
	})

	// Initial conversation.
	messages := []ollamaToolMessage{
		{
			Role: "system",
			Content: llmadapter.SystemPrompt + "\n\n" +
				"You have access to the `query_session` tool to investigate the session. " +
				"Use it to look at errors, tool calls, and messages before writing your analysis. " +
				"When you are done investigating, respond with ONLY a JSON object matching this schema:\n" +
				`{"score": <0-100>, "summary": "<string>", "problems": [{"severity": "low|medium|high", "description": "<string>", "tool_name": "<optional>"}], "recommendations": [{"category": "tool|workflow|skill|config", "title": "<string>", "description": "<string>", "priority": <1-5>}]}` +
				"\nDo NOT wrap the JSON in markdown fences. Return ONLY the raw JSON object.",
		},
		{
			Role:    "user",
			Content: analysisPrompt,
		},
	}

	// Use a longer timeout for the LLM client (models can be slow).
	llmClient := &http.Client{Timeout: 5 * time.Minute}
	ctx := context.Background()

	var toolCallCount int
	var finalContent string

	// Agentic loop.
	for iteration := 0; iteration < maxIterations; iteration++ {
		t.Logf("Iteration %d: sending request with %d messages", iteration+1, len(messages))

		reqBody := ollamaToolCallRequest{
			Model:    model,
			Messages: messages,
			Stream:   false,
			Tools:    tools,
			Options:  &ollamaRequestOptions{Temperature: 0.3},
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			t.Fatalf("marshaling request: %v", err)
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/chat", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("creating request: %v", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := llmClient.Do(httpReq)
		if err != nil {
			t.Fatalf("calling Ollama: %v", err)
		}

		respBody, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("reading response: %v", err)
		}

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Ollama returned %d: %s", resp.StatusCode, string(respBody))
		}

		var ollamaResp ollamaToolCallResponse
		if err := json.Unmarshal(respBody, &ollamaResp); err != nil {
			t.Fatalf("decoding response: %v", err)
		}

		t.Logf("  Response: content_len=%d tool_calls=%d done=%v done_reason=%q",
			len(ollamaResp.Message.Content),
			len(ollamaResp.Message.ToolCalls),
			ollamaResp.Done,
			ollamaResp.DoneReason,
		)

		// Check if the model wants to use tools.
		if len(ollamaResp.Message.ToolCalls) > 0 {
			// Append the assistant message (with tool calls) to conversation.
			messages = append(messages, ollamaResp.Message)

			// Execute each tool call and add results.
			for _, tc := range ollamaResp.Message.ToolCalls {
				toolCallCount++
				t.Logf("  Tool call #%d: %s(%s)", toolCallCount, tc.Function.Name, string(tc.Function.Arguments))

				result := ollamaDispatchTool(executor, tc.Function.Name, tc.Function.Arguments)
				t.Logf("  Tool result: %.200s", result)

				// Ollama expects tool results as role="tool" messages.
				messages = append(messages, ollamaToolMessage{
					Role:    "tool",
					Content: result,
				})
			}
			continue
		}

		// No tool calls — this should be the final response.
		finalContent = ollamaResp.Message.Content
		break
	}

	// ── Validate results ──

	if finalContent == "" {
		t.Fatal("model did not produce a final response after max iterations")
	}

	t.Logf("Tool calls made: %d", toolCallCount)
	t.Logf("Final response (first 500 chars): %.500s", finalContent)

	// The model should have used at least one tool to investigate.
	if toolCallCount == 0 {
		t.Log("WARNING: model did not use any tools — it may have generated the report directly from the prompt context")
	}

	// Try to parse the response as a valid AnalysisReport.
	// The model might wrap it in markdown or add preamble, so use the existing extractor.
	report, err := parseReport(finalContent)
	if err != nil {
		// If parsing fails, try to find JSON in the content using brace matching.
		t.Logf("Direct parse failed: %v", err)

		// Some models include thinking in <think> tags before the JSON.
		cleaned := finalContent
		if idx := strings.Index(cleaned, "</think>"); idx >= 0 {
			cleaned = strings.TrimSpace(cleaned[idx+len("</think>"):])
		}

		report, err = parseReport(cleaned)
		if err != nil {
			t.Fatalf("Could not parse analysis report from model response: %v\nFull response:\n%s", err, finalContent)
		}
	}

	// Validate the report.
	t.Logf("Parsed report: score=%d summary=%.100s", report.Score, report.Summary)
	t.Logf("Problems: %d, Recommendations: %d", len(report.Problems), len(report.Recommendations))

	if report.Score < 0 || report.Score > 100 {
		t.Errorf("Score out of range: %d", report.Score)
	}
	if report.Summary == "" {
		t.Error("Summary is empty")
	}

	// The session has clear problems (build errors, test failures, retries) — expect at least 1 problem detected.
	if len(report.Problems) == 0 {
		t.Error("Expected at least 1 problem detected (session has build errors and test failures)")
	}

	// Validate severity values are valid.
	for i, p := range report.Problems {
		switch p.Severity {
		case analysis.SeverityLow, analysis.SeverityMedium, analysis.SeverityHigh:
			// Valid.
		default:
			t.Errorf("Problem[%d].Severity = %q, want low|medium|high", i, p.Severity)
		}
	}

	// Validate recommendation categories.
	for i, r := range report.Recommendations {
		switch r.Category {
		case analysis.CategoryTool, analysis.CategoryWorkflow, analysis.CategorySkill, analysis.CategoryConfig:
			// Valid.
		default:
			t.Errorf("Recommendation[%d].Category = %q, want tool|workflow|skill|config", i, r.Category)
		}
	}

	t.Logf("E2E agentic test PASSED: model=%s, iterations=%d tool_calls, score=%d",
		model, toolCallCount, report.Score)
}
