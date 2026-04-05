package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/llm"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Mock LLM Client ──

type mockLLMClient struct {
	response string
	err      error
	calls    int
}

func (m *mockLLMClient) Complete(_ context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return &llm.CompletionResponse{
		Content:      m.response,
		Model:        "mock-model",
		InputTokens:  100,
		OutputTokens: 50,
	}, nil
}

// ── extractJSONArray tests ──

func TestExtractJSONArray(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "clean JSON",
			input: `[{"title":"a"}]`,
			want:  `[{"title":"a"}]`,
		},
		{
			name:  "with markdown fences",
			input: "```json\n[{\"title\":\"a\"}]\n```",
			want:  `[{"title":"a"}]`,
		},
		{
			name:  "with surrounding text",
			input: "Here is the result:\n[{\"title\":\"a\"}]\nDone.",
			want:  `[{"title":"a"}]`,
		},
		{
			name:  "nested arrays",
			input: `[{"items": [1, 2]}, {"items": [3]}]`,
			want:  `[{"items": [1, 2]}, {"items": [3]}]`,
		},
		{
			name:  "no array",
			input: `{"title":"a"}`,
			want:  "",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSONArray(tt.input)
			if got != tt.want {
				t.Errorf("extractJSONArray() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ── parseEnrichResponse tests ──

func TestParseEnrichResponse(t *testing.T) {
	t.Run("valid JSON array", func(t *testing.T) {
		input := `[{"title":"Better title","message":"Do this thing.","impact":"Reduces errors by 50%."}]`
		result, err := parseEnrichResponse(input, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result) != 1 {
			t.Fatalf("expected 1 result, got %d", len(result))
		}
		if result[0].Title != "Better title" {
			t.Errorf("title = %q, want %q", result[0].Title, "Better title")
		}
		if result[0].Message != "Do this thing." {
			t.Errorf("message = %q, want %q", result[0].Message, "Do this thing.")
		}
		if result[0].Impact != "Reduces errors by 50%." {
			t.Errorf("impact = %q, want %q", result[0].Impact, "Reduces errors by 50%.")
		}
	})

	t.Run("JSON in markdown fence", func(t *testing.T) {
		input := "Sure! Here:\n```json\n[{\"title\":\"T\",\"message\":\"M\",\"impact\":\"I\"}]\n```"
		result, err := parseEnrichResponse(input, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result) != 1 {
			t.Fatalf("expected 1, got %d", len(result))
		}
		if result[0].Title != "T" {
			t.Errorf("title = %q", result[0].Title)
		}
	})

	t.Run("wrong count returns error", func(t *testing.T) {
		input := `[{"title":"a","message":"b","impact":"c"},{"title":"d","message":"e","impact":"f"}]`
		_, err := parseEnrichResponse(input, 3)
		if err == nil {
			t.Fatal("expected error for mismatched count")
		}
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		_, err := parseEnrichResponse("not json at all", 1)
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
}

// ── buildEnrichPrompt tests ──

func TestBuildEnrichPrompt(t *testing.T) {
	recs := []session.RecommendationRecord{
		{
			Type:        "agent_error",
			Priority:    "high",
			Title:       "High error rate",
			Message:     "Agent has errors.",
			Impact:      "5 errors",
			Agent:       "claude",
			ProjectPath: "/proj/test",
		},
		{
			Type:        "skill_ghost",
			Priority:    "medium",
			Title:       "Unused skill",
			Message:     "Remove it.",
			Skill:       "test-skill",
			ProjectPath: "/proj/test",
		},
	}

	prompt := buildEnrichPrompt(recs)

	// Verify key content is present.
	for _, want := range []string{
		"Recommendation 1:",
		"Type: agent_error",
		"Priority: high",
		"Title: High error rate",
		"Agent: claude",
		"Recommendation 2:",
		"Type: skill_ghost",
		"Skill: test-skill",
		"Project: /proj/test",
	} {
		if !contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ── LLMEnricher tests ──

func TestLLMEnricher_Enrich(t *testing.T) {
	enrichedJSON, _ := json.Marshal([]session.EnrichedRecommendation{
		{Title: "Reduce error rate for claude agent", Message: "The claude agent encounters errors in 30% of sessions. Review the CLAUDE.md prompt for ambiguous instructions.", Impact: "Could reduce error rate by 50%."},
		{Title: "Remove unused skill 'test-skill'", Message: "The 'test-skill' skill has not been used in 14 days. Removing it will reduce context overhead.", Impact: "Saves ~2K tokens per session."},
	})

	client := &mockLLMClient{response: string(enrichedJSON)}

	enricher := NewLLMEnricher(LLMEnricherConfig{Client: client})

	recs := []session.RecommendationRecord{
		{Type: "agent_error", Priority: "high", Title: "High error rate", Message: "Errors.", ProjectPath: "/proj/a"},
		{Type: "skill_ghost", Priority: "medium", Title: "Unused skill", Message: "Remove.", ProjectPath: "/proj/a"},
	}

	result, err := enricher.Enrich(context.Background(), recs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	if result[0].Title != "Reduce error rate for claude agent" {
		t.Errorf("title[0] = %q", result[0].Title)
	}
	if result[1].Impact != "Saves ~2K tokens per session." {
		t.Errorf("impact[1] = %q", result[1].Impact)
	}
	if client.calls != 1 {
		t.Errorf("expected 1 LLM call, got %d", client.calls)
	}
}

func TestLLMEnricher_EmptyRecs(t *testing.T) {
	client := &mockLLMClient{response: "[]"}
	enricher := NewLLMEnricher(LLMEnricherConfig{Client: client})

	result, err := enricher.Enrich(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for empty recs, got %v", result)
	}
	if client.calls != 0 {
		t.Errorf("expected 0 LLM calls for empty recs, got %d", client.calls)
	}
}

func TestLLMEnricher_LLMError(t *testing.T) {
	client := &mockLLMClient{err: fmt.Errorf("LLM unavailable")}
	enricher := NewLLMEnricher(LLMEnricherConfig{Client: client})

	recs := []session.RecommendationRecord{
		{Type: "agent_error", Title: "Test", Message: "msg", ProjectPath: "/p"},
	}

	_, err := enricher.Enrich(context.Background(), recs)
	if err == nil {
		t.Fatal("expected error from LLM failure")
	}
}

func TestLLMEnricher_InvalidJSON(t *testing.T) {
	client := &mockLLMClient{response: "I cannot help with that."}
	enricher := NewLLMEnricher(LLMEnricherConfig{Client: client})

	recs := []session.RecommendationRecord{
		{Type: "agent_error", Title: "Test", Message: "msg", ProjectPath: "/p"},
	}

	_, err := enricher.Enrich(context.Background(), recs)
	if err == nil {
		t.Fatal("expected error from invalid JSON")
	}
}

func TestLLMEnricher_BatchLimit(t *testing.T) {
	// Create an enricher with maxBatch=2 and send 5 recs — only first 2 processed.
	enrichedJSON, _ := json.Marshal([]session.EnrichedRecommendation{
		{Title: "T1", Message: "M1", Impact: "I1"},
		{Title: "T2", Message: "M2", Impact: "I2"},
	})
	client := &mockLLMClient{response: string(enrichedJSON)}
	enricher := NewLLMEnricher(LLMEnricherConfig{Client: client, MaxBatch: 2})

	recs := make([]session.RecommendationRecord, 5)
	for i := range recs {
		recs[i] = session.RecommendationRecord{
			Type: "test", Title: "T", Message: "M", ProjectPath: "/p",
		}
	}

	result, err := enricher.Enrich(context.Background(), recs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 results (batch limited), got %d", len(result))
	}
}

func TestLLMEnricher_DefaultMaxBatch(t *testing.T) {
	enricher := NewLLMEnricher(LLMEnricherConfig{Client: &mockLLMClient{}})
	if enricher.maxBatch != 10 {
		t.Errorf("expected default maxBatch 10, got %d", enricher.maxBatch)
	}
}
