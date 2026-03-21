package skillresolver

import "context"

// SkillAnalyzer is the port interface for analyzing why a skill was missed
// and proposing SKILL.md improvements. The LLM adapter implements this.
//
// This is deliberately separate from analysis.Analyzer (ISP — different
// bounded context, different input/output shapes).
type SkillAnalyzer interface {
	// Analyze examines a missed skill and proposes improvements.
	// Returns structured improvements, never nil AnalyzeOutput on success.
	Analyze(ctx context.Context, input AnalyzeInput) (*AnalyzeOutput, error)
}
