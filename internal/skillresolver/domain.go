// Package skillresolver resolves missed-skill observations by analyzing why
// a skill wasn't triggered and proposing improvements to SKILL.md files
// (descriptions, keywords, trigger patterns).
//
// Architecture:
//   - Analyzer (port): LLM-based analysis of why a skill was missed
//   - SkillFile: reads/writes SKILL.md files on disk
//   - Service: orchestrates observer → analyze → propose → (optionally) apply
//
// The resolver is a separate bounded context that takes SkillObservation
// (from skillobs) as input and produces SkillImprovement proposals.
package skillresolver

import "time"

// ── Enums ──

// Verdict describes the outcome of a resolution attempt.
type Verdict string

const (
	// VerdictFixed means all proposed improvements were validated.
	VerdictFixed Verdict = "fixed"

	// VerdictPartial means some improvements helped but not all.
	VerdictPartial Verdict = "partial"

	// VerdictNoChange means no improvements could be identified.
	VerdictNoChange Verdict = "no_change"

	// VerdictPending means improvements were proposed but not yet validated.
	VerdictPending Verdict = "pending"
)

// Valid reports whether v is a known verdict value.
func (v Verdict) Valid() bool {
	switch v {
	case VerdictFixed, VerdictPartial, VerdictNoChange, VerdictPending:
		return true
	}
	return false
}

// String returns the string representation.
func (v Verdict) String() string {
	return string(v)
}

// ImprovementKind categorizes the type of SKILL.md change proposed.
type ImprovementKind string

const (
	// KindDescription means the skill's description should be updated.
	KindDescription ImprovementKind = "description"

	// KindKeywords means new keywords should be added.
	KindKeywords ImprovementKind = "keywords"

	// KindTriggerPattern means new trigger patterns should be added.
	KindTriggerPattern ImprovementKind = "trigger_pattern"

	// KindContent means the skill's body content should be updated.
	KindContent ImprovementKind = "content"
)

// Valid reports whether k is a known improvement kind.
func (k ImprovementKind) Valid() bool {
	switch k {
	case KindDescription, KindKeywords, KindTriggerPattern, KindContent:
		return true
	}
	return false
}

// String returns the string representation.
func (k ImprovementKind) String() string {
	return string(k)
}

// ── Value Objects ──

// SkillImprovement proposes a specific change to a SKILL.md file.
// Produced by the LLM Analyzer, consumed by the Applier.
type SkillImprovement struct {
	// SkillName is the skill identifier (e.g. "replay-tester").
	SkillName string `json:"skill_name"`

	// SkillPath is the absolute path to the SKILL.md file.
	SkillPath string `json:"skill_path"`

	// Kind categorizes the type of change.
	Kind ImprovementKind `json:"kind"`

	// CurrentDescription is the existing description from SKILL.md.
	CurrentDescription string `json:"current_description,omitempty"`

	// ProposedDescription is the improved description (for KindDescription).
	ProposedDescription string `json:"proposed_description,omitempty"`

	// AddKeywords lists new keywords to add (for KindKeywords).
	AddKeywords []string `json:"add_keywords,omitempty"`

	// AddTriggerPatterns lists new trigger patterns to add (for KindTriggerPattern).
	AddTriggerPatterns []string `json:"add_trigger_patterns,omitempty"`

	// ProposedContent is the updated body content (for KindContent).
	ProposedContent string `json:"proposed_content,omitempty"`

	// Reasoning explains why this improvement was suggested.
	Reasoning string `json:"reasoning"`

	// Confidence is how certain the analyzer is (0.0-1.0).
	Confidence float64 `json:"confidence"`

	// SourceSessionID links back to the session that surfaced the miss.
	SourceSessionID string `json:"source_session_id"`
}

// ── Request / Response ──

// ResolveRequest specifies what to analyze and whether to auto-apply.
type ResolveRequest struct {
	// SessionID is the session whose SkillObservation to analyze.
	SessionID string `json:"session_id"`

	// SkillNames optionally restricts analysis to specific missed skills.
	// If empty, all missed skills from the observation are analyzed.
	SkillNames []string `json:"skill_names,omitempty"`

	// DryRun if true means only produce proposals, don't apply to disk.
	DryRun bool `json:"dry_run"`
}

// ResolveResult contains the outcome of a skill resolution.
type ResolveResult struct {
	// SessionID is the session that was analyzed.
	SessionID string `json:"session_id"`

	// Improvements lists all proposed (or applied) improvements.
	Improvements []SkillImprovement `json:"improvements"`

	// Applied is the count of improvements written to disk (0 if DryRun).
	Applied int `json:"applied"`

	// Validated is true if the improvements were verified via replay.
	Validated bool `json:"validated"`

	// Verdict summarizes the resolution outcome.
	Verdict Verdict `json:"verdict"`

	// Duration is how long the resolution took.
	Duration time.Duration `json:"duration"`

	// Error is non-empty if the resolution failed.
	Error string `json:"error,omitempty"`
}

// ── Analyzer Port (LLM is infrastructure) ──

// AnalyzeInput contains all data needed for the LLM to propose improvements.
type AnalyzeInput struct {
	// SkillName is the skill that was missed.
	SkillName string

	// SkillPath is the path to the SKILL.md file.
	SkillPath string

	// CurrentContent is the full content of the SKILL.md file.
	CurrentContent string

	// CurrentDescription is the description extracted from SKILL.md.
	CurrentDescription string

	// CurrentKeywords are the existing keywords.
	CurrentKeywords []string

	// UserMessages are the session messages that should have triggered the skill.
	// Typically the first N user messages from the session.
	UserMessages []string

	// SessionSummary is the session summary (if available).
	SessionSummary string

	// SessionID links back to the source session.
	SessionID string
}

// AnalyzeOutput is the structured response from the LLM analyzer.
type AnalyzeOutput struct {
	// Improvements lists all proposed changes.
	Improvements []SkillImprovement `json:"improvements"`
}
