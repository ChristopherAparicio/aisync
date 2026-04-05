package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/llm"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// RecommendationEnricher enriches deterministic recommendations using an LLM.
// It takes a batch of recommendations and returns enriched versions with
// improved messages, actionable advice, and impact descriptions.
type RecommendationEnricher interface {
	// Enrich takes a batch of recommendations and returns enriched content
	// for each one. The returned slice matches the input slice by index.
	// If a recommendation cannot be enriched, the corresponding entry
	// has an empty Message (caller should keep the original).
	Enrich(ctx context.Context, recs []session.RecommendationRecord) ([]session.EnrichedRecommendation, error)
}

// ── LLM-based enricher adapter ──

// LLMEnricher uses an llm.Client to enrich recommendations via structured prompts.
// It batches recommendations into a single LLM call for efficiency.
type LLMEnricher struct {
	client   llm.Client
	maxBatch int // max recs per LLM call (0 = default 10)
}

// LLMEnricherConfig holds the configuration for creating an LLMEnricher.
type LLMEnricherConfig struct {
	Client   llm.Client
	MaxBatch int // max recs per LLM call; default 10
}

// NewLLMEnricher creates a new LLM-based recommendation enricher.
func NewLLMEnricher(cfg LLMEnricherConfig) *LLMEnricher {
	maxBatch := cfg.MaxBatch
	if maxBatch <= 0 {
		maxBatch = 10
	}
	return &LLMEnricher{
		client:   cfg.Client,
		maxBatch: maxBatch,
	}
}

const enrichSystemPrompt = `You are an AI DevOps advisor for a coding session analytics platform.
Your job is to take raw, auto-generated recommendations about AI coding sessions
and rewrite them with:
1. Clearer, more actionable titles (keep under 80 chars)
2. Detailed, contextual messages explaining WHY this matters and WHAT to do (2-4 sentences)
3. Concrete impact descriptions (what improves if the advice is followed)

Respond with a JSON array where each element has these fields:
- "title": improved title (string, max 80 chars)
- "message": enriched explanation with actionable advice (string, 2-4 sentences)
- "impact": concrete expected improvement (string, 1 sentence)

The array must have exactly the same number of elements as the input, in the same order.
Return ONLY the JSON array, no markdown fences, no explanation.`

// Enrich sends recommendations to the LLM for enrichment.
func (e *LLMEnricher) Enrich(ctx context.Context, recs []session.RecommendationRecord) ([]session.EnrichedRecommendation, error) {
	if len(recs) == 0 {
		return nil, nil
	}

	// Batch if needed.
	if len(recs) > e.maxBatch {
		recs = recs[:e.maxBatch]
	}

	// Build user prompt with recommendation data.
	userPrompt := buildEnrichPrompt(recs)

	resp, err := e.client.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: enrichSystemPrompt,
		UserPrompt:   userPrompt,
		MaxTokens:    2048,
	})
	if err != nil {
		return nil, fmt.Errorf("enrich LLM call: %w", err)
	}

	// Parse JSON response.
	enriched, parseErr := parseEnrichResponse(resp.Content, len(recs))
	if parseErr != nil {
		return nil, fmt.Errorf("enrich parse: %w", parseErr)
	}

	return enriched, nil
}

// buildEnrichPrompt creates the user prompt for the LLM with recommendation data.
func buildEnrichPrompt(recs []session.RecommendationRecord) string {
	var sb strings.Builder
	sb.WriteString("Here are the recommendations to enrich:\n\n")

	for i, r := range recs {
		fmt.Fprintf(&sb, "Recommendation %d:\n", i+1)
		fmt.Fprintf(&sb, "  Type: %s\n", r.Type)
		fmt.Fprintf(&sb, "  Priority: %s\n", r.Priority)
		fmt.Fprintf(&sb, "  Title: %s\n", r.Title)
		fmt.Fprintf(&sb, "  Message: %s\n", r.Message)
		if r.Impact != "" {
			fmt.Fprintf(&sb, "  Impact: %s\n", r.Impact)
		}
		if r.Agent != "" {
			fmt.Fprintf(&sb, "  Agent: %s\n", r.Agent)
		}
		if r.Skill != "" {
			fmt.Fprintf(&sb, "  Skill: %s\n", r.Skill)
		}
		fmt.Fprintf(&sb, "  Project: %s\n\n", r.ProjectPath)
	}

	return sb.String()
}

// parseEnrichResponse extracts enriched recommendations from the LLM JSON response.
func parseEnrichResponse(content string, expected int) ([]session.EnrichedRecommendation, error) {
	// Try direct JSON parse first.
	var result []session.EnrichedRecommendation
	if err := json.Unmarshal([]byte(content), &result); err == nil {
		if len(result) == expected {
			return result, nil
		}
	}

	// Try extracting JSON from markdown fences or surrounding text.
	cleaned := extractJSONArray(content)
	if cleaned != "" {
		if err := json.Unmarshal([]byte(cleaned), &result); err == nil {
			if len(result) == expected {
				return result, nil
			}
		}
	}

	return nil, fmt.Errorf("expected %d enriched recs, got %d (or invalid JSON)", expected, len(result))
}

// extractJSONArray finds the first JSON array in the content string.
func extractJSONArray(s string) string {
	start := strings.Index(s, "[")
	if start < 0 {
		return ""
	}

	// Find the matching closing bracket.
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
