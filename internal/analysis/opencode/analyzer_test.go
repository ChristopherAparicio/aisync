package opencode

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestAnalyzerName(t *testing.T) {
	a := NewAnalyzer(AnalyzerConfig{})
	if got := a.Name(); got != analysis.AdapterOpenCode {
		t.Errorf("Name() = %q, want %q", got, analysis.AdapterOpenCode)
	}
}

func TestNewAnalyzer_DefaultBinary(t *testing.T) {
	a := NewAnalyzer(AnalyzerConfig{})
	if a.binaryPath != "opencode" {
		t.Errorf("binaryPath = %q, want %q", a.binaryPath, "opencode")
	}
}

func TestNewAnalyzer_CustomBinary(t *testing.T) {
	a := NewAnalyzer(AnalyzerConfig{BinaryPath: "/usr/local/bin/opencode"})
	if a.binaryPath != "/usr/local/bin/opencode" {
		t.Errorf("binaryPath = %q, want %q", a.binaryPath, "/usr/local/bin/opencode")
	}
}

func TestExtractTextFromEvents(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "single text event",
			input: `{"type":"text","part":{"type":"text","text":"Hello world"}}`,
			want:  "Hello world",
		},
		{
			name: "multiple text events",
			input: `{"type":"text","part":{"type":"text","text":"Hello "}}
{"type":"text","part":{"type":"text","text":"world"}}`,
			want: "Hello world",
		},
		{
			name: "mixed event types",
			input: `{"type":"step_start","part":{"type":"step_start"}}
{"type":"text","part":{"type":"text","text":"analysis result"}}
{"type":"step_finish","part":{"type":"step_finish"}}`,
			want: "analysis result",
		},
		{
			name:  "empty output",
			input: "",
			want:  "",
		},
		{
			name: "no text events",
			input: `{"type":"step_start","part":{"type":"step_start"}}
{"type":"step_finish","part":{"type":"step_finish"}}`,
			want: "",
		},
		{
			name: "text event with empty text",
			input: `{"type":"text","part":{"type":"text","text":""}}
{"type":"text","part":{"type":"text","text":"content"}}`,
			want: "content",
		},
		{
			name: "non-JSON lines are skipped",
			input: `some random text
{"type":"text","part":{"type":"text","text":"valid"}}
more garbage`,
			want: "valid",
		},
		{
			name: "blank lines are skipped",
			input: `
{"type":"text","part":{"type":"text","text":"a"}}

{"type":"text","part":{"type":"text","text":"b"}}
`,
			want: "ab",
		},
		{
			name: "full realistic streaming output",
			input: `{"type":"step_start","part":{"type":"step_start"}}
{"type":"text","part":{"type":"text","text":"{"}}
{"type":"text","part":{"type":"text","text":"\"score\": 72"}}
{"type":"text","part":{"type":"text","text":"}"}}
{"type":"step_finish","part":{"type":"step_finish"}}`,
			want: `{"score": 72}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTextFromEvents(tt.input)
			if got != tt.want {
				t.Errorf("extractTextFromEvents() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractTextFromEvents_FullReport(t *testing.T) {
	// Simulate OpenCode streaming a full JSON report across multiple text events.
	report := analysis.AnalysisReport{
		Score:   82,
		Summary: "Efficient session with minor tool errors.",
		Problems: []analysis.Problem{
			{Severity: analysis.SeverityLow, Description: "One file read retry"},
		},
		Recommendations: []analysis.Recommendation{
			{Category: analysis.CategoryTool, Title: "Use glob", Description: "Use glob for file search", Priority: 2},
		},
	}
	reportJSON, _ := json.Marshal(report)

	// Split the JSON across multiple text events to simulate streaming.
	reportStr := string(reportJSON)
	mid := len(reportStr) / 2
	chunk1 := reportStr[:mid]
	chunk2 := reportStr[mid:]

	eventLine := func(typ, text string) string {
		ev := openCodeEvent{Type: typ, Part: openCodePart{Type: typ, Text: text}}
		b, _ := json.Marshal(ev)
		return string(b)
	}

	var lines []string
	lines = append(lines, eventLine("step_start", ""))
	lines = append(lines, eventLine("text", chunk1))
	lines = append(lines, eventLine("text", chunk2))
	lines = append(lines, eventLine("step_finish", ""))

	output := strings.Join(lines, "\n")
	textContent := extractTextFromEvents(output)

	// The concatenated text should be valid JSON.
	var parsed analysis.AnalysisReport
	if err := json.Unmarshal([]byte(textContent), &parsed); err != nil {
		t.Fatalf("failed to parse concatenated text as JSON: %v\ntext: %s", err, textContent)
	}
	if parsed.Score != 82 {
		t.Errorf("Score = %d, want 82", parsed.Score)
	}
	if parsed.Summary != "Efficient session with minor tool errors." {
		t.Errorf("Summary = %q, want %q", parsed.Summary, "Efficient session with minor tool errors.")
	}
}

func TestExtractJSON(t *testing.T) {
	validJSON := `{"score": 75, "summary": "Good session"}`

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "direct JSON",
			input: validJSON,
			want:  validJSON,
		},
		{
			name:  "json code fence",
			input: "Here is the result:\n```json\n" + validJSON + "\n```\nDone.",
			want:  validJSON,
		},
		{
			name:  "generic code fence",
			input: "```\n" + validJSON + "\n```",
			want:  validJSON,
		},
		{
			name:  "embedded in text",
			input: "The analysis: " + validJSON + " was complete.",
			want:  validJSON,
		},
		{
			name:  "with leading whitespace",
			input: "  \n  " + validJSON,
			want:  validJSON,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSON(tt.input)
			if got != tt.want {
				t.Errorf("extractJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSessionErrorRate(t *testing.T) {
	sess := &session.Session{
		Messages: []session.Message{
			{
				ToolCalls: []session.ToolCall{
					{State: session.ToolStateCompleted},
					{State: session.ToolStateError},
					{State: session.ToolStateCompleted},
					{State: session.ToolStateError},
				},
			},
		},
	}

	rate := sessionErrorRate(sess)
	if rate != 50.0 {
		t.Errorf("sessionErrorRate() = %.1f, want 50.0", rate)
	}
}

func TestSessionErrorRate_NoToolCalls(t *testing.T) {
	sess := &session.Session{
		Messages: []session.Message{{Content: "hello"}},
	}
	rate := sessionErrorRate(sess)
	if rate != 0 {
		t.Errorf("sessionErrorRate() = %.1f, want 0", rate)
	}
}

func TestBuildOpenCodePrompt(t *testing.T) {
	req := analysis.AnalyzeRequest{
		Session: session.Session{
			ID:         "oc-test-001",
			Provider:   "opencode",
			CreatedAt:  time.Now(),
			Messages:   []session.Message{{Role: session.RoleUser, Content: "test"}},
			TokenUsage: session.TokenUsage{TotalTokens: 100},
		},
		ErrorThreshold: 15,
	}

	prompt := buildOpenCodePrompt(req)

	// Should contain both the instruction header and session data
	checks := []string{
		"analyzing an AI coding session",
		"valid JSON object",
		"SESSION DATA",
		"oc-test-001",
		"opencode",
	}
	for _, want := range checks {
		if !contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestExtractJSON_FullReport(t *testing.T) {
	report := analysis.AnalysisReport{
		Score:   65,
		Summary: "Fair session with some retry issues.",
		Problems: []analysis.Problem{
			{Severity: analysis.SeverityMedium, Description: "Retry loop detected"},
		},
		Recommendations: []analysis.Recommendation{
			{Category: analysis.CategoryWorkflow, Title: "Break tasks down", Description: "Smaller commits", Priority: 1},
		},
	}
	b, _ := json.Marshal(report)
	wrapped := "Here is my analysis:\n```json\n" + string(b) + "\n```\n"

	extracted := extractJSON(wrapped)
	var parsed analysis.AnalysisReport
	if err := json.Unmarshal([]byte(extracted), &parsed); err != nil {
		t.Fatalf("failed to parse extracted JSON: %v", err)
	}
	if parsed.Score != 65 {
		t.Errorf("Score = %d, want 65", parsed.Score)
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) > len(substr) && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
