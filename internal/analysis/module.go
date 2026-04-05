// Package analysis — modular analysis framework.
//
// An AnalysisModule is an independent analysis unit that can be selected
// by the user when analyzing a session. Modules run in parallel and each
// produces its own ModuleResult. The framework aggregates results into a
// single AnalysisReport.
//
// Built-in modules:
//   - "tool_efficiency" — LLM-based evaluation of each tool call's usefulness
//   - "session_quality" — existing full analysis (Score, Problems, Recommendations)
package analysis

import (
	"context"
	"encoding/json"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ModuleName identifies an analysis module.
type ModuleName string

const (
	// ModuleSessionQuality is the existing full analysis (score, problems, recommendations).
	ModuleSessionQuality ModuleName = "session_quality"

	// ModuleToolEfficiency evaluates each tool call's usefulness via LLM.
	ModuleToolEfficiency ModuleName = "tool_efficiency"
)

// AllModules returns all available module names in display order.
func AllModules() []ModuleName {
	return []ModuleName{ModuleSessionQuality, ModuleToolEfficiency}
}

// ModuleInfo describes a module for the UI (checkboxes).
type ModuleInfo struct {
	Name        ModuleName `json:"name"`
	Label       string     `json:"label"`
	Description string     `json:"description"`
	RequiresLLM bool       `json:"requires_llm"`
}

// ModuleRegistry returns metadata for all available modules.
func ModuleRegistry() []ModuleInfo {
	return []ModuleInfo{
		{
			Name:        ModuleSessionQuality,
			Label:       "Session Quality",
			Description: "Overall efficiency score, problems, and recommendations",
			RequiresLLM: true,
		},
		{
			Name:        ModuleToolEfficiency,
			Label:       "Tool Efficiency",
			Description: "Per-tool-call evaluation: was the result useful? Were calls redundant?",
			RequiresLLM: true,
		},
	}
}

// AnalysisModule is the port interface for pluggable analysis modules.
// Each module receives the full session and produces a typed result.
type AnalysisModule interface {
	// Name returns the module identifier.
	Name() ModuleName

	// Analyze runs the module's analysis on the given session.
	Analyze(ctx context.Context, req ModuleRequest) (*ModuleResult, error)
}

// ModuleRequest contains the inputs for a module analysis.
type ModuleRequest struct {
	Session session.Session
}

// ModuleResult contains the output of a single module's analysis.
type ModuleResult struct {
	// Module identifies which module produced this result.
	Module ModuleName `json:"module"`

	// Payload is the module-specific structured result (JSON-serializable).
	// The concrete type depends on the module (e.g. ToolEfficiencyReport).
	Payload json.RawMessage `json:"payload"`

	// TokensUsed is the total LLM tokens consumed by this module.
	TokensUsed int `json:"tokens_used,omitempty"`

	// DurationMs is how long this module took.
	DurationMs int `json:"duration_ms,omitempty"`

	// Error is non-empty if the module failed.
	Error string `json:"error,omitempty"`
}

// ── Tool Efficiency Module Domain Types ──

// ToolEfficiencyReport is the structured result of the tool_efficiency module.
type ToolEfficiencyReport struct {
	// ToolEvaluations is a per-tool-call assessment.
	ToolEvaluations []ToolEvaluation `json:"tool_evaluations"`

	// Summary is a brief overall assessment of tool usage efficiency.
	Summary string `json:"summary"`

	// OverallScore is 0-100 measuring overall tool usage efficiency.
	OverallScore int `json:"overall_score"`

	// RedundantCalls is the number of calls judged redundant.
	RedundantCalls int `json:"redundant_calls"`

	// UsefulCalls is the number of calls that provided useful results.
	UsefulCalls int `json:"useful_calls"`

	// Patterns lists detected anti-patterns in tool usage.
	Patterns []string `json:"patterns,omitempty"`
}

// ToolEvaluation is the LLM's assessment of a single tool call.
type ToolEvaluation struct {
	// Index is the position of the tool call in the session (0-based message index).
	Index int `json:"index"`

	// ToolName is the name of the tool.
	ToolName string `json:"tool_name"`

	// Usefulness is the LLM's judgment: "useful", "partial", "redundant", "wasteful".
	Usefulness string `json:"usefulness"`

	// Reason is a brief explanation of the judgment.
	Reason string `json:"reason"`

	// InputTokens is the estimated tokens consumed by the tool's input.
	InputTokens int `json:"input_tokens"`

	// OutputTokens is the estimated tokens consumed by the tool's output.
	OutputTokens int `json:"output_tokens"`
}
