package llmanalyzer

import (
	"context"
	"fmt"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/skillresolver"
)

// mockAnalyzer is a test double for analysis.Analyzer.
type mockAnalyzer struct {
	summary string
	err     error
}

func (m *mockAnalyzer) Name() analysis.AdapterName { return analysis.AdapterOllama }

func (m *mockAnalyzer) Analyze(_ context.Context, _ analysis.AnalyzeRequest) (*analysis.AnalysisReport, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &analysis.AnalysisReport{
		Score:   50,
		Summary: m.summary,
	}, nil
}

func TestAnalyze_ParsesJSON(t *testing.T) {
	mock := &mockAnalyzer{
		summary: `{
			"improvements": [
				{
					"skill_name": "replay-tester",
					"kind": "keywords",
					"add_keywords": ["replay", "session", "compare"],
					"reasoning": "user said 'replay the session'",
					"confidence": 0.9
				}
			]
		}`,
	}

	a := New(Config{Analyzer: mock})
	out, err := a.Analyze(context.Background(), skillresolver.AnalyzeInput{
		SkillName:          "replay-tester",
		SkillPath:          "/path/to/SKILL.md",
		CurrentDescription: "Test by replaying sessions",
		UserMessages:       []string{"Can you replay the session?"},
		SessionID:          "sess_123",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Improvements) != 1 {
		t.Fatalf("expected 1 improvement, got %d", len(out.Improvements))
	}
	imp := out.Improvements[0]
	if imp.SkillName != "replay-tester" {
		t.Errorf("SkillName = %q, want %q", imp.SkillName, "replay-tester")
	}
	if imp.Kind != skillresolver.KindKeywords {
		t.Errorf("Kind = %q, want %q", imp.Kind, skillresolver.KindKeywords)
	}
	if len(imp.AddKeywords) != 3 {
		t.Errorf("AddKeywords len = %d, want 3", len(imp.AddKeywords))
	}
	if imp.Confidence != 0.9 {
		t.Errorf("Confidence = %f, want 0.9", imp.Confidence)
	}
	if imp.SourceSessionID != "sess_123" {
		t.Errorf("SourceSessionID = %q, want %q", imp.SourceSessionID, "sess_123")
	}
	if imp.SkillPath != "/path/to/SKILL.md" {
		t.Errorf("SkillPath = %q, want %q", imp.SkillPath, "/path/to/SKILL.md")
	}
}

func TestAnalyze_ExtractsJSONFromText(t *testing.T) {
	mock := &mockAnalyzer{
		summary: `Here is the analysis:
{"improvements": [{"skill_name": "test-skill", "kind": "description", "proposed_description": "Better desc", "reasoning": "too vague", "confidence": 0.8}]}
That's my recommendation.`,
	}

	a := New(Config{Analyzer: mock})
	out, err := a.Analyze(context.Background(), skillresolver.AnalyzeInput{
		SkillName:    "test-skill",
		SkillPath:    "/skills/test-skill/SKILL.md",
		UserMessages: []string{"run tests"},
		SessionID:    "sess_abc",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Improvements) != 1 {
		t.Fatalf("expected 1 improvement, got %d", len(out.Improvements))
	}
	if out.Improvements[0].Kind != skillresolver.KindDescription {
		t.Errorf("Kind = %q, want %q", out.Improvements[0].Kind, skillresolver.KindDescription)
	}
	if out.Improvements[0].ProposedDescription != "Better desc" {
		t.Errorf("ProposedDescription = %q", out.Improvements[0].ProposedDescription)
	}
}

func TestAnalyze_InvalidJSON(t *testing.T) {
	mock := &mockAnalyzer{
		summary: "This is not JSON at all.",
	}

	a := New(Config{Analyzer: mock})
	_, err := a.Analyze(context.Background(), skillresolver.AnalyzeInput{
		SkillName:    "test-skill",
		UserMessages: []string{"test"},
		SessionID:    "sess_err",
	})

	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if got := err.Error(); !contains(got, "could not parse") {
		t.Errorf("error = %q, should contain 'could not parse'", got)
	}
}

func TestAnalyze_AnalyzerError(t *testing.T) {
	mock := &mockAnalyzer{
		err: fmt.Errorf("LLM unavailable"),
	}

	a := New(Config{Analyzer: mock})
	_, err := a.Analyze(context.Background(), skillresolver.AnalyzeInput{
		SkillName:    "test-skill",
		UserMessages: []string{"test"},
		SessionID:    "sess_err",
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !contains(got, "LLM unavailable") {
		t.Errorf("error = %q, should contain 'LLM unavailable'", got)
	}
}

func TestAnalyze_NilAnalyzer(t *testing.T) {
	a := New(Config{Analyzer: nil})
	_, err := a.Analyze(context.Background(), skillresolver.AnalyzeInput{
		SkillName:    "test-skill",
		UserMessages: []string{"test"},
		SessionID:    "sess_nil",
	})

	if err == nil {
		t.Fatal("expected error for nil analyzer")
	}
}

func TestAnalyze_EnrichesEmptyFields(t *testing.T) {
	mock := &mockAnalyzer{
		summary: `{"improvements": [{"kind": "keywords", "add_keywords": ["test"], "reasoning": "r", "confidence": 0.7}]}`,
	}

	a := New(Config{Analyzer: mock})
	out, err := a.Analyze(context.Background(), skillresolver.AnalyzeInput{
		SkillName:    "my-skill",
		SkillPath:    "/skills/my-skill/SKILL.md",
		SessionID:    "sess_enrich",
		UserMessages: []string{"test"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Improvements) != 1 {
		t.Fatalf("expected 1 improvement, got %d", len(out.Improvements))
	}
	imp := out.Improvements[0]
	if imp.SkillName != "my-skill" {
		t.Errorf("SkillName not enriched: %q", imp.SkillName)
	}
	if imp.SkillPath != "/skills/my-skill/SKILL.md" {
		t.Errorf("SkillPath not enriched: %q", imp.SkillPath)
	}
	if imp.SourceSessionID != "sess_enrich" {
		t.Errorf("SourceSessionID not enriched: %q", imp.SourceSessionID)
	}
}

func TestAnalyze_ClampsConfidence(t *testing.T) {
	mock := &mockAnalyzer{
		summary: `{"improvements": [{"kind": "keywords", "add_keywords": ["x"], "reasoning": "r", "confidence": 1.5}]}`,
	}

	a := New(Config{Analyzer: mock})
	out, err := a.Analyze(context.Background(), skillresolver.AnalyzeInput{
		SkillName:    "s",
		SessionID:    "sess",
		UserMessages: []string{"test"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Improvements[0].Confidence != 1.0 {
		t.Errorf("Confidence = %f, want 1.0 (clamped)", out.Improvements[0].Confidence)
	}
}

func TestAnalyze_DefaultsInvalidKind(t *testing.T) {
	mock := &mockAnalyzer{
		summary: `{"improvements": [{"kind": "bogus", "add_keywords": ["x"], "reasoning": "r", "confidence": 0.5}]}`,
	}

	a := New(Config{Analyzer: mock})
	out, err := a.Analyze(context.Background(), skillresolver.AnalyzeInput{
		SkillName:    "s",
		SessionID:    "sess",
		UserMessages: []string{"test"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Improvements[0].Kind != skillresolver.KindKeywords {
		t.Errorf("Kind = %q, want %q (defaulted)", out.Improvements[0].Kind, skillresolver.KindKeywords)
	}
}

func TestBuildPrompt(t *testing.T) {
	input := skillresolver.AnalyzeInput{
		SkillName:          "replay-tester",
		SkillPath:          "/path/to/SKILL.md",
		CurrentDescription: "Test evolutions by replaying sessions",
		CurrentKeywords:    []string{"replay", "test"},
		CurrentContent:     "# Replay Tester\nDetailed instructions...",
		UserMessages:       []string{"Can you replay the session?", "Compare the results"},
		SessionSummary:     "User wanted to replay a session",
		SessionID:          "sess_build",
	}

	prompt := buildPrompt(input)

	// Verify key parts are in the prompt.
	for _, want := range []string{
		"replay-tester",
		"/path/to/SKILL.md",
		"sess_build",
		"Test evolutions by replaying sessions",
		"replay, test",
		"Replay Tester",
		"Can you replay the session?",
		"Compare the results",
		"User wanted to replay a session",
	} {
		if !contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildPrompt_TruncatesLongContent(t *testing.T) {
	longContent := make([]byte, 3000)
	for i := range longContent {
		longContent[i] = 'x'
	}

	input := skillresolver.AnalyzeInput{
		SkillName:      "s",
		CurrentContent: string(longContent),
		UserMessages:   []string{"test"},
		SessionID:      "sess",
	}

	prompt := buildPrompt(input)
	if !contains(prompt, "truncated") {
		t.Error("prompt should indicate truncation for long content")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
