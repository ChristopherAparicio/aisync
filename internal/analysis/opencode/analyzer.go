// Package opencode implements the analysis.Analyzer port by spawning the OpenCode CLI
// in non-interactive ("run") mode. The prompt with session data is passed as argument
// and the response is parsed from the streaming JSON events that OpenCode emits with
// --format json.
//
// This adapter requires the `opencode` binary to be installed and available in PATH.
package opencode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	llmadapter "github.com/ChristopherAparicio/aisync/internal/analysis/llm"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// Analyzer implements analysis.Analyzer by spawning an OpenCode agent process.
type Analyzer struct {
	binaryPath string
	model      string
}

// AnalyzerConfig configures the OpenCode analyzer.
type AnalyzerConfig struct {
	// BinaryPath is the path to the opencode binary. Default: "opencode".
	BinaryPath string

	// Model is an optional model override for the agent.
	Model string
}

// NewAnalyzer creates a new OpenCode-based analyzer.
func NewAnalyzer(cfg AnalyzerConfig) *Analyzer {
	binary := cfg.BinaryPath
	if binary == "" {
		binary = "opencode"
	}
	return &Analyzer{
		binaryPath: binary,
		model:      cfg.Model,
	}
}

// Name returns the adapter identifier.
func (a *Analyzer) Name() analysis.AdapterName {
	return analysis.AdapterOpenCode
}

// Analyze spawns `opencode run <prompt> --format json` and parses the streamed
// JSON events to extract the assistant's text response containing an AnalysisReport.
func (a *Analyzer) Analyze(ctx context.Context, req analysis.AnalyzeRequest) (*analysis.AnalysisReport, error) {
	if len(req.Session.Messages) == 0 {
		return nil, fmt.Errorf("session has no messages to analyze")
	}

	prompt := buildOpenCodePrompt(req)

	// Build the command: opencode run "<prompt>" --format json [--model <model>]
	args := []string{"run", prompt, "--format", "json"}
	if a.model != "" {
		args = append(args, "--model", a.model)
	}

	cmd := exec.CommandContext(ctx, a.binaryPath, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			return nil, fmt.Errorf("opencode run: %s: %w", stderrStr, err)
		}
		return nil, fmt.Errorf("opencode run: %w", err)
	}

	// Parse the streaming JSON events from stdout.
	// OpenCode emits one JSON event per line with --format json.
	// We collect all "text" events and concatenate their part.text fields.
	textContent := extractTextFromEvents(stdout.String())
	if textContent == "" {
		return nil, fmt.Errorf("opencode returned no text content")
	}

	// Extract the JSON analysis report from the text response.
	jsonStr := extractJSON(textContent)

	var report analysis.AnalysisReport
	if err := json.Unmarshal([]byte(jsonStr), &report); err != nil {
		return nil, fmt.Errorf("parsing opencode response: %w (raw: %.200s)", err, textContent)
	}

	// Clamp score.
	if report.Score < 0 {
		report.Score = 0
	}
	if report.Score > 100 {
		report.Score = 100
	}

	if err := report.Validate(); err != nil {
		return nil, fmt.Errorf("opencode produced invalid report: %w", err)
	}

	return &report, nil
}

// openCodeEvent represents a single streaming event from `opencode run --format json`.
// Only the fields we need are included; unknown fields are ignored by the decoder.
type openCodeEvent struct {
	Type string       `json:"type"`
	Part openCodePart `json:"part"`
}

type openCodePart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// extractTextFromEvents parses the newline-delimited JSON events emitted by
// `opencode run --format json` and returns the concatenated text from all
// "text" type events.
func extractTextFromEvents(output string) string {
	var b strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev openCodeEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue // skip non-JSON lines
		}
		if ev.Type == "text" && ev.Part.Text != "" {
			b.WriteString(ev.Part.Text)
		}
	}
	return b.String()
}

// buildOpenCodePrompt constructs the prompt for the OpenCode agent.
// It includes a system-level instruction followed by the session data,
// reusing the LLM prompt builder for the data portion.
func buildOpenCodePrompt(req analysis.AnalyzeRequest) string {
	var b strings.Builder

	b.WriteString("You are analyzing an AI coding session for efficiency and quality issues.\n\n")
	b.WriteString("Analyze the following session data and respond with ONLY a valid JSON object.\n")
	b.WriteString("Do NOT include markdown fences, explanations, or any text outside the JSON.\n\n")
	b.WriteString("Required JSON format:\n")
	b.WriteString(`{
  "score": <integer 0-100>,
  "summary": "<one-paragraph assessment>",
  "problems": [{"severity": "<low|medium|high>", "description": "<text>", "tool_name": "<optional>"}],
  "recommendations": [{"category": "<skill|config|workflow|tool>", "title": "<text>", "description": "<text>", "priority": <1-5>}],
  "skill_suggestions": [{"name": "<id>", "description": "<text>", "trigger": "<text>"}]
}`)
	b.WriteString("\n\n--- SESSION DATA ---\n\n")

	// Reuse the LLM prompt builder for the data portion.
	b.WriteString(llmadapter.BuildAnalysisPrompt(req))

	return b.String()
}

// extractJSON attempts to find a JSON object in the text response.
// The LLM may return clean JSON or wrap it in markdown fences.
func extractJSON(output string) string {
	// Try direct parse first
	output = strings.TrimSpace(output)
	if strings.HasPrefix(output, "{") {
		return output
	}

	// Try to extract from markdown code fence
	if idx := strings.Index(output, "```json"); idx >= 0 {
		start := idx + len("```json")
		if end := strings.Index(output[start:], "```"); end >= 0 {
			return strings.TrimSpace(output[start : start+end])
		}
	}

	// Try generic code fence
	if idx := strings.Index(output, "```"); idx >= 0 {
		start := idx + len("```")
		// Skip optional language identifier on same line
		if nlIdx := strings.Index(output[start:], "\n"); nlIdx >= 0 {
			start += nlIdx + 1
		}
		if end := strings.Index(output[start:], "```"); end >= 0 {
			return strings.TrimSpace(output[start : start+end])
		}
	}

	// Try to find the first { and last }
	firstBrace := strings.Index(output, "{")
	lastBrace := strings.LastIndex(output, "}")
	if firstBrace >= 0 && lastBrace > firstBrace {
		return output[firstBrace : lastBrace+1]
	}

	return output
}

// sessionErrorRate computes the tool error rate for the session.
func sessionErrorRate(sess *session.Session) float64 {
	var total, errors int
	for i := range sess.Messages {
		for j := range sess.Messages[i].ToolCalls {
			total++
			if sess.Messages[i].ToolCalls[j].State == session.ToolStateError {
				errors++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(errors) / float64(total) * 100
}
