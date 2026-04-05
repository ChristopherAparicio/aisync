// Package analysis contains the domain types for the Session Analysis bounded context.
// It analyzes captured AI sessions and produces actionable recommendations
// (skill suggestions, workflow improvements, configuration tweaks).
//
// This is a separate BC linked to the Session BC via session_id.
// No interfaces live here — they are defined by the packages that own
// the abstraction (analysis.Analyzer port, service.AnalysisService).
package analysis

import (
	"fmt"
	"time"
)

// ── Enums ──

// Trigger indicates what initiated the analysis.
type Trigger string

const (
	TriggerAuto   Trigger = "auto"   // triggered automatically after capture
	TriggerManual Trigger = "manual" // triggered by user via CLI or UI
)

// Valid reports whether t is a known trigger value.
func (t Trigger) Valid() bool {
	return t == TriggerAuto || t == TriggerManual
}

// String returns the string representation.
func (t Trigger) String() string {
	return string(t)
}

// AdapterName identifies which analysis adapter produced the report.
type AdapterName string

const (
	AdapterLLM       AdapterName = "llm"       // internal LLM via llm.Client (Claude CLI)
	AdapterOpenCode  AdapterName = "opencode"  // external OpenCode agent process
	AdapterOllama    AdapterName = "ollama"    // Ollama local API (/api/chat)
	AdapterAnthropic AdapterName = "anthropic" // Anthropic Messages API (direct HTTP)
)

// Valid reports whether a is a known adapter name.
func (a AdapterName) Valid() bool {
	switch a {
	case AdapterLLM, AdapterOpenCode, AdapterOllama, AdapterAnthropic:
		return true
	}
	return false
}

// String returns the string representation.
func (a AdapterName) String() string {
	return string(a)
}

// Severity indicates the impact level of a detected problem.
type Severity string

const (
	SeverityLow    Severity = "low"
	SeverityMedium Severity = "medium"
	SeverityHigh   Severity = "high"
)

// Valid reports whether s is a known severity value.
func (s Severity) Valid() bool {
	return s == SeverityLow || s == SeverityMedium || s == SeverityHigh
}

// String returns the string representation.
func (s Severity) String() string {
	return string(s)
}

// RecommendationCategory classifies what kind of improvement is suggested.
type RecommendationCategory string

const (
	CategorySkill    RecommendationCategory = "skill"    // create or improve a skill
	CategoryConfig   RecommendationCategory = "config"   // configuration change
	CategoryWorkflow RecommendationCategory = "workflow" // process improvement
	CategoryTool     RecommendationCategory = "tool"     // tool usage improvement
)

// Valid reports whether c is a known category.
func (c RecommendationCategory) Valid() bool {
	switch c {
	case CategorySkill, CategoryConfig, CategoryWorkflow, CategoryTool:
		return true
	}
	return false
}

// String returns the string representation.
func (c RecommendationCategory) String() string {
	return string(c)
}

// ── Entities & Value Objects ──

// SessionAnalysis is the aggregate root for a persisted session analysis.
// Each analysis is linked to a single Session by session_id.
type SessionAnalysis struct {
	// ID is the unique identifier for this analysis.
	ID string `json:"id"`

	// SessionID is the foreign key linking to the analyzed session.
	SessionID string `json:"session_id"`

	// CreatedAt is when the analysis was performed.
	CreatedAt time.Time `json:"created_at"`

	// Trigger indicates what initiated this analysis.
	Trigger Trigger `json:"trigger"`

	// Report contains the analysis results.
	Report AnalysisReport `json:"report"`

	// Adapter identifies which analyzer produced this report.
	Adapter AdapterName `json:"adapter"`

	// Model is the specific model used (e.g. "claude-sonnet-4-20250514").
	Model string `json:"model,omitempty"`

	// TokensUsed is the total tokens consumed by the analysis.
	TokensUsed int `json:"tokens_used,omitempty"`

	// DurationMs is how long the analysis took in milliseconds.
	DurationMs int `json:"duration_ms,omitempty"`

	// Error is non-empty if the analysis failed.
	Error string `json:"error,omitempty"`
}

// OK reports whether the analysis completed successfully (no error).
func (a *SessionAnalysis) OK() bool {
	return a.Error == ""
}

// AnalysisReport contains the structured results of a session analysis.
type AnalysisReport struct {
	// Score is the overall efficiency score (0-100).
	Score int `json:"score"`

	// Summary is a one-paragraph assessment of the session.
	Summary string `json:"summary"`

	// Problems lists detected issues and inefficiencies.
	Problems []Problem `json:"problems,omitempty"`

	// Recommendations lists actionable improvement suggestions.
	Recommendations []Recommendation `json:"recommendations,omitempty"`

	// SkillSuggestions lists new skills that could be created.
	SkillSuggestions []SkillSuggestion `json:"skill_suggestions,omitempty"`

	// SkillObservation is the result of comparing recommended vs loaded skills.
	// Populated by the Skill Observer as a post-analysis enrichment step.
	// Nil/empty if no skills are available for the project.
	SkillObservation *SkillObservation `json:"skill_observation,omitempty"`

	// ModuleResults contains per-module analysis results from the modular framework.
	// Each entry is keyed by module name. Empty if no modules were run.
	ModuleResults []ModuleResult `json:"module_results,omitempty"`
}

// Validate checks that the report has required fields and valid ranges.
func (r *AnalysisReport) Validate() error {
	if r.Score < 0 || r.Score > 100 {
		return fmt.Errorf("score must be 0-100, got %d", r.Score)
	}
	if r.Summary == "" {
		return fmt.Errorf("summary is required")
	}
	for i, p := range r.Problems {
		if !p.Severity.Valid() {
			return fmt.Errorf("problem[%d]: invalid severity %q", i, p.Severity)
		}
		if p.Description == "" {
			return fmt.Errorf("problem[%d]: description is required", i)
		}
	}
	for i, rec := range r.Recommendations {
		if !rec.Category.Valid() {
			return fmt.Errorf("recommendation[%d]: invalid category %q", i, rec.Category)
		}
		if rec.Title == "" {
			return fmt.Errorf("recommendation[%d]: title is required", i)
		}
	}
	return nil
}

// Problem describes a detected issue in the session.
type Problem struct {
	// Severity indicates the impact level.
	Severity Severity `json:"severity"`

	// Description explains what went wrong.
	Description string `json:"description"`

	// MessageRange is the optional span of message indices (1-based, inclusive)
	// where the problem occurred. Zero values mean "not localized".
	MessageStart int `json:"message_start,omitempty"`
	MessageEnd   int `json:"message_end,omitempty"`

	// ToolName is the optional tool name related to this problem.
	ToolName string `json:"tool_name,omitempty"`
}

// Recommendation suggests an actionable improvement.
type Recommendation struct {
	// Category classifies the type of improvement.
	Category RecommendationCategory `json:"category"`

	// Title is a short, descriptive heading.
	Title string `json:"title"`

	// Description provides detailed explanation and rationale.
	Description string `json:"description"`

	// Priority indicates importance (1 = highest, 5 = lowest).
	Priority int `json:"priority"`
}

// SkillObservation captures the result of comparing recommended skills
// (based on user messages) vs actually loaded skills (detected from tool calls).
// This is the output of the Skill Observer — it identifies missed opportunities.
type SkillObservation struct {
	// Available lists all skills known for the project (from registry).
	Available []string `json:"available,omitempty"`

	// Recommended lists skills the recommender suggests based on user messages.
	Recommended []string `json:"recommended,omitempty"`

	// Loaded lists skills actually loaded during the session (detected from tool calls).
	Loaded []string `json:"loaded,omitempty"`

	// Missed lists skills that were recommended but NOT loaded.
	// This is the key metric — high miss rate indicates agent/prompt improvement needed.
	Missed []string `json:"missed,omitempty"`

	// Discovered lists skills that were loaded but NOT recommended.
	// Indicates the recommender's keyword coverage is incomplete.
	Discovered []string `json:"discovered,omitempty"`
}

// SkillSuggestion proposes a new skill that could be created
// to avoid a recurring problem.
type SkillSuggestion struct {
	// Name is the proposed skill identifier (e.g. "db-migration-helper").
	Name string `json:"name"`

	// Description explains what the skill would do.
	Description string `json:"description"`

	// Trigger describes when the skill should activate.
	Trigger string `json:"trigger,omitempty"`

	// Content is an optional draft of the skill content/instructions.
	Content string `json:"content,omitempty"`
}
