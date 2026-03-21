// Package llmanalyzer implements the skillresolver.SkillAnalyzer port
// using the existing analysis.Analyzer infrastructure.
//
// It builds a purpose-specific prompt for skill improvement analysis
// and parses the structured JSON response into SkillImprovement proposals.
// This is the infrastructure adapter — the domain knows nothing about LLMs.
package llmanalyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/skillresolver"
)

// systemPrompt is the instruction for the skill improvement LLM call.
const systemPrompt = `You are an expert at improving AI coding assistant skill configurations.
A "skill" is a SKILL.md file that defines when an AI assistant should load specialized instructions.
Skills have a description and keywords that determine when they are triggered.

Your task: Given a skill that SHOULD have been triggered for a session but WASN'T, analyze why
the trigger failed and propose specific improvements to the SKILL.md file.

Your response must be a valid JSON object:

{
  "improvements": [
    {
      "skill_name": "<skill identifier>",
      "skill_path": "<path to SKILL.md>",
      "kind": "<description|keywords|trigger_pattern|content>",
      "current_description": "<existing description>",
      "proposed_description": "<improved description, only if kind=description>",
      "add_keywords": ["<new keywords to add, only if kind=keywords>"],
      "add_trigger_patterns": ["<new patterns, only if kind=trigger_pattern>"],
      "proposed_content": "<updated body, only if kind=content>",
      "reasoning": "<why this change would help>",
      "confidence": <0.0-1.0>,
      "source_session_id": "<session ID>"
    }
  ]
}

Guidelines:
- Analyze the gap between user messages and the skill's current description/keywords
- Propose the MINIMAL changes needed — prefer adding keywords over rewriting descriptions
- Keywords should be lowercase, 3+ chars, meaningful terms the user would actually type
- Trigger patterns are regex-like phrases that should match user intent
- Confidence: 0.9+ = very sure, 0.7-0.9 = likely helps, <0.7 = speculative
- You may propose multiple improvements of different kinds for the same skill
- Keep proposed_description concise (1-2 sentences)
- Only propose content changes if the skill body is clearly wrong or missing key instructions

Respond ONLY with valid JSON, no markdown fences, no explanation.`

// Analyzer implements skillresolver.SkillAnalyzer using the existing analysis.Analyzer.
type Analyzer struct {
	analyzer analysis.Analyzer
}

// Config configures the LLM skill analyzer.
type Config struct {
	// Analyzer is the analysis.Analyzer adapter to use (required).
	// Any adapter (Ollama, Anthropic, LLM/Claude CLI) works.
	Analyzer analysis.Analyzer
}

// New creates a new LLM-based skill analyzer adapter.
func New(cfg Config) *Analyzer {
	return &Analyzer{
		analyzer: cfg.Analyzer,
	}
}

// Analyze implements skillresolver.SkillAnalyzer.
func (a *Analyzer) Analyze(ctx context.Context, input skillresolver.AnalyzeInput) (*skillresolver.AnalyzeOutput, error) {
	if a.analyzer == nil {
		return nil, fmt.Errorf("analyzer is nil")
	}

	prompt := buildPrompt(input)

	// Use the analysis.Analyzer with a synthetic session containing our prompt.
	report, err := a.analyzer.Analyze(ctx, analysis.AnalyzeRequest{
		Session: session.Session{
			ID:         session.ID(input.SessionID),
			Messages:   []session.Message{{Role: session.RoleUser, Content: prompt}},
			TokenUsage: session.TokenUsage{TotalTokens: 100},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("skill analysis LLM call: %w", err)
	}

	// The analyzer returns an AnalysisReport — the summary field contains our JSON.
	return parseResponse(report.Summary, input)
}

// buildPrompt constructs the data-rich prompt for skill improvement analysis.
func buildPrompt(input skillresolver.AnalyzeInput) string {
	var b strings.Builder

	b.WriteString(systemPrompt)
	b.WriteString("\n\n---\n\n")

	b.WriteString(fmt.Sprintf("Skill name: %s\n", input.SkillName))
	b.WriteString(fmt.Sprintf("Skill path: %s\n", input.SkillPath))
	b.WriteString(fmt.Sprintf("Session ID: %s\n\n", input.SessionID))

	if input.CurrentDescription != "" {
		b.WriteString(fmt.Sprintf("Current description: %s\n\n", input.CurrentDescription))
	}

	if len(input.CurrentKeywords) > 0 {
		b.WriteString(fmt.Sprintf("Current keywords: %s\n\n", strings.Join(input.CurrentKeywords, ", ")))
	}

	if input.CurrentContent != "" {
		content := input.CurrentContent
		if len(content) > 2000 {
			content = content[:2000] + "\n... (truncated)"
		}
		b.WriteString("Current SKILL.md content:\n```\n")
		b.WriteString(content)
		b.WriteString("\n```\n\n")
	}

	if input.SessionSummary != "" {
		b.WriteString(fmt.Sprintf("Session summary: %s\n\n", input.SessionSummary))
	}

	b.WriteString("User messages that should have triggered this skill:\n")
	for i, msg := range input.UserMessages {
		content := msg
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		b.WriteString(fmt.Sprintf("[%d] %s\n", i+1, content))
	}

	return b.String()
}

// parseResponse extracts SkillImprovement from the LLM's summary output.
// Falls back gracefully if the response can't be parsed.
func parseResponse(raw string, input skillresolver.AnalyzeInput) (*skillresolver.AnalyzeOutput, error) {
	raw = strings.TrimSpace(raw)

	var output skillresolver.AnalyzeOutput

	// Try direct JSON parse.
	if err := json.Unmarshal([]byte(raw), &output); err == nil {
		enrichImprovements(output.Improvements, input)
		return &output, nil
	}

	// Try extracting JSON from the raw text (LLM may wrap in text).
	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			if err := json.Unmarshal([]byte(raw[start:end+1]), &output); err == nil {
				enrichImprovements(output.Improvements, input)
				return &output, nil
			}
		}
	}

	return nil, fmt.Errorf("could not parse skill analysis response: %.200s", raw)
}

// enrichImprovements fills in any missing fields from the input context.
func enrichImprovements(improvements []skillresolver.SkillImprovement, input skillresolver.AnalyzeInput) {
	for i := range improvements {
		if improvements[i].SkillName == "" {
			improvements[i].SkillName = input.SkillName
		}
		if improvements[i].SkillPath == "" {
			improvements[i].SkillPath = input.SkillPath
		}
		if improvements[i].SourceSessionID == "" {
			improvements[i].SourceSessionID = input.SessionID
		}
		// Validate and default the kind.
		if !improvements[i].Kind.Valid() {
			improvements[i].Kind = skillresolver.KindKeywords
		}
		// Clamp confidence.
		if improvements[i].Confidence < 0 {
			improvements[i].Confidence = 0
		}
		if improvements[i].Confidence > 1 {
			improvements[i].Confidence = 1
		}
	}
}
