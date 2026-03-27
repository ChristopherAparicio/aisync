package pricing

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Test Data ───────────────────────────────────────────────────────────────

// minimalLiteLLMJSON is a small test fixture with representative models.
// Must have at least 10 entries for UpdateCache validation (which checks total JSON keys >= 10).
var minimalLiteLLMJSON = `{
  "claude-opus-4-1": {
    "cache_creation_input_token_cost": 1.875e-05,
    "cache_read_input_token_cost": 1.5e-06,
    "input_cost_per_token": 1.5e-05,
    "litellm_provider": "anthropic",
    "max_input_tokens": 200000,
    "max_output_tokens": 32000,
    "mode": "chat",
    "output_cost_per_token": 7.5e-05
  },
  "claude-sonnet-4-20250514": {
    "cache_creation_input_token_cost": 3.75e-06,
    "cache_read_input_token_cost": 3e-07,
    "input_cost_per_token": 3e-06,
    "litellm_provider": "anthropic",
    "max_input_tokens": 200000,
    "max_output_tokens": 64000,
    "mode": "chat",
    "output_cost_per_token": 1.5e-05
  },
  "gpt-4o": {
    "cache_read_input_token_cost": 1.25e-06,
    "input_cost_per_token": 2.5e-06,
    "litellm_provider": "openai",
    "max_input_tokens": 128000,
    "max_output_tokens": 16384,
    "mode": "chat",
    "output_cost_per_token": 1e-05
  },
  "gemini-2.5-pro": {
    "cache_read_input_token_cost": 1.25e-07,
    "input_cost_per_token": 1.25e-06,
    "input_cost_per_token_above_200k_tokens": 2.5e-06,
    "litellm_provider": "vertex_ai-language-models",
    "max_input_tokens": 1048576,
    "max_output_tokens": 65535,
    "mode": "chat",
    "output_cost_per_token": 1e-05,
    "output_cost_per_token_above_200k_tokens": 1.5e-05
  },
  "anthropic.claude-opus-4-6-v1": {
    "cache_creation_input_token_cost": 6.25e-06,
    "cache_creation_input_token_cost_above_200k_tokens": 1.25e-05,
    "cache_read_input_token_cost": 5e-07,
    "cache_read_input_token_cost_above_200k_tokens": 1e-06,
    "input_cost_per_token": 5e-06,
    "input_cost_per_token_above_200k_tokens": 1e-05,
    "litellm_provider": "bedrock_converse",
    "max_input_tokens": 1000000,
    "max_output_tokens": 128000,
    "mode": "chat",
    "output_cost_per_token": 2.5e-05,
    "output_cost_per_token_above_200k_tokens": 3.75e-05
  },
  "dall-e-3": {
    "input_cost_per_image": 0.04,
    "litellm_provider": "openai",
    "mode": "image_generation",
    "output_cost_per_image": 0.08
  },
  "whisper-1": {
    "input_cost_per_second": 0.0001,
    "litellm_provider": "openai",
    "mode": "audio_transcription"
  },
  "text-embedding-3-small": {
    "input_cost_per_token": 2e-08,
    "litellm_provider": "openai",
    "mode": "embedding",
    "output_cost_per_token": 0
  },
  "gpt-4o-mini": {
    "input_cost_per_token": 1.5e-07,
    "output_cost_per_token": 6e-07,
    "litellm_provider": "openai",
    "mode": "chat"
  },
  "claude-haiku-3.5": {
    "input_cost_per_token": 8e-07,
    "output_cost_per_token": 4e-06,
    "cache_read_input_token_cost": 8e-08,
    "cache_creation_input_token_cost": 1e-06,
    "litellm_provider": "anthropic",
    "mode": "chat"
  },
  "o3-mini": {
    "input_cost_per_token": 1.1e-06,
    "output_cost_per_token": 4.4e-06,
    "litellm_provider": "openai",
    "mode": "chat"
  }
}`

// tieredLiteLLMJSON tests multiple threshold levels.
var tieredLiteLLMJSON = `{
  "test-model-128k-tier": {
    "input_cost_per_token": 1e-06,
    "input_cost_per_token_above_128k_tokens": 2e-06,
    "output_cost_per_token": 5e-06,
    "output_cost_per_token_above_128k_tokens": 7.5e-06,
    "litellm_provider": "test",
    "mode": "chat"
  },
  "test-model-multi-tier": {
    "input_cost_per_token": 1e-06,
    "input_cost_per_token_above_128k_tokens": 1.5e-06,
    "input_cost_per_token_above_256k_tokens": 3e-06,
    "output_cost_per_token": 4e-06,
    "output_cost_per_token_above_128k_tokens": 6e-06,
    "output_cost_per_token_above_256k_tokens": 8e-06,
    "litellm_provider": "test",
    "mode": "chat"
  }
}`

// ── parseLiteLLMJSON Tests ──────────────────────────────────────────────────

func TestParseLiteLLMJSON_basicModels(t *testing.T) {
	prices, err := parseLiteLLMJSON([]byte(minimalLiteLLMJSON), nil, 0)
	if err != nil {
		t.Fatalf("parseLiteLLMJSON error: %v", err)
	}

	// Should only include chat-mode models with input/output pricing.
	// Expected: claude-opus-4-1, claude-sonnet-4-20250514, gpt-4o, gemini-2.5-pro,
	//           anthropic.claude-opus-4-6-v1, gpt-4o-mini, claude-haiku-3.5, o3-mini
	// Excluded: dall-e-3 (image_generation), whisper-1 (audio_transcription),
	//           text-embedding-3-small (embedding mode)
	if len(prices) != 8 {
		t.Errorf("expected 8 chat models, got %d", len(prices))
		for _, p := range prices {
			t.Logf("  model: %s", p.Model)
		}
	}
}

func TestParseLiteLLMJSON_perMTokenConversion(t *testing.T) {
	prices, err := parseLiteLLMJSON([]byte(minimalLiteLLMJSON), nil, 0)
	if err != nil {
		t.Fatalf("parseLiteLLMJSON error: %v", err)
	}

	// Find claude-opus-4-1: $15/M input, $75/M output.
	var opus ModelPrice
	for _, p := range prices {
		if p.Model == "claude-opus-4-1" {
			opus = p
			break
		}
	}

	if opus.Model == "" {
		t.Fatal("claude-opus-4-1 not found")
	}

	assertFloat(t, "opus input", opus.InputPerMToken, 15.0)
	assertFloat(t, "opus output", opus.OutputPerMToken, 75.0)
	assertFloat(t, "opus cache read", opus.CacheReadPerMToken, 1.5)
	assertFloat(t, "opus cache write", opus.CacheWritePerMToken, 18.75)
}

func TestParseLiteLLMJSON_contextWindowSizes(t *testing.T) {
	prices, err := parseLiteLLMJSON([]byte(minimalLiteLLMJSON), nil, 0)
	if err != nil {
		t.Fatalf("parseLiteLLMJSON error: %v", err)
	}

	tests := []struct {
		model         string
		wantMaxInput  int
		wantMaxOutput int
	}{
		{"claude-opus-4-1", 200000, 32000},
		{"claude-sonnet-4-20250514", 200000, 64000},
		{"gpt-4o", 128000, 16384},
		{"gemini-2.5-pro", 1048576, 65535},
		{"anthropic.claude-opus-4-6-v1", 1000000, 128000},
	}

	priceMap := make(map[string]ModelPrice)
	for _, p := range prices {
		priceMap[p.Model] = p
	}

	for _, tt := range tests {
		p, ok := priceMap[tt.model]
		if !ok {
			t.Errorf("model %q not found", tt.model)
			continue
		}
		if p.MaxInputTokens != tt.wantMaxInput {
			t.Errorf("%s: MaxInputTokens = %d, want %d", tt.model, p.MaxInputTokens, tt.wantMaxInput)
		}
		if p.MaxOutputTokens != tt.wantMaxOutput {
			t.Errorf("%s: MaxOutputTokens = %d, want %d", tt.model, p.MaxOutputTokens, tt.wantMaxOutput)
		}
	}
}

func TestParseLiteLLMJSON_contextWindowZeroWhenMissing(t *testing.T) {
	prices, err := parseLiteLLMJSON([]byte(minimalLiteLLMJSON), nil, 0)
	if err != nil {
		t.Fatalf("parseLiteLLMJSON error: %v", err)
	}

	// gpt-4o-mini and o3-mini don't have max_input_tokens in the fixture.
	for _, p := range prices {
		if p.Model == "gpt-4o-mini" || p.Model == "o3-mini" {
			if p.MaxInputTokens != 0 {
				t.Errorf("%s: expected MaxInputTokens=0 (not in JSON), got %d", p.Model, p.MaxInputTokens)
			}
		}
	}
}

func TestEmbeddedCatalog_contextWindowSizes(t *testing.T) {
	cat, err := NewEmbeddedCatalog()
	if err != nil {
		t.Fatalf("NewEmbeddedCatalog error: %v", err)
	}

	tests := []struct {
		model         string
		wantMaxInput  int
		wantMaxOutput int
	}{
		{"claude-opus-4-20250514", 200000, 32000},
		{"claude-sonnet-4-20250514", 200000, 64000},
		{"gpt-4o-2025-01-01", 128000, 16384},
		{"gemini-2.5-pro-latest", 1048576, 65536},
		{"o3-2025-01-01", 200000, 100000},
	}

	for _, tt := range tests {
		p, ok := cat.Lookup(tt.model)
		if !ok {
			t.Errorf("model %q not found in embedded catalog", tt.model)
			continue
		}
		if p.MaxInputTokens != tt.wantMaxInput {
			t.Errorf("%s: MaxInputTokens = %d, want %d", tt.model, p.MaxInputTokens, tt.wantMaxInput)
		}
		if p.MaxOutputTokens != tt.wantMaxOutput {
			t.Errorf("%s: MaxOutputTokens = %d, want %d", tt.model, p.MaxOutputTokens, tt.wantMaxOutput)
		}
	}
}

func TestParseLiteLLMJSON_gpt4oPricing(t *testing.T) {
	prices, err := parseLiteLLMJSON([]byte(minimalLiteLLMJSON), nil, 0)
	if err != nil {
		t.Fatalf("parseLiteLLMJSON error: %v", err)
	}

	var gpt4o ModelPrice
	for _, p := range prices {
		if p.Model == "gpt-4o" {
			gpt4o = p
			break
		}
	}

	if gpt4o.Model == "" {
		t.Fatal("gpt-4o not found")
	}

	assertFloat(t, "gpt-4o input", gpt4o.InputPerMToken, 2.5)
	assertFloat(t, "gpt-4o output", gpt4o.OutputPerMToken, 10.0)
	assertFloat(t, "gpt-4o cache read", gpt4o.CacheReadPerMToken, 1.25)
	assertFloat(t, "gpt-4o cache write", gpt4o.CacheWritePerMToken, 0.0) // not in data

	if gpt4o.HasTiers() {
		t.Error("gpt-4o should not have tiers")
	}
}

func TestParseLiteLLMJSON_excludesNonChat(t *testing.T) {
	prices, err := parseLiteLLMJSON([]byte(minimalLiteLLMJSON), nil, 0)
	if err != nil {
		t.Fatalf("parseLiteLLMJSON error: %v", err)
	}

	for _, p := range prices {
		if p.Model == "dall-e-3" || p.Model == "whisper-1" || p.Model == "text-embedding-3-small" {
			t.Errorf("non-chat model %q should have been filtered out", p.Model)
		}
	}
}

func TestParseLiteLLMJSON_sortedByModelName(t *testing.T) {
	prices, err := parseLiteLLMJSON([]byte(minimalLiteLLMJSON), nil, 0)
	if err != nil {
		t.Fatalf("parseLiteLLMJSON error: %v", err)
	}

	for i := 1; i < len(prices); i++ {
		if prices[i-1].Model > prices[i].Model {
			t.Errorf("models not sorted: %q > %q", prices[i-1].Model, prices[i].Model)
		}
	}
}

// ── Tiered Pricing Tests ────────────────────────────────────────────────────

func TestParseLiteLLMJSON_tieredPricing_gemini(t *testing.T) {
	prices, err := parseLiteLLMJSON([]byte(minimalLiteLLMJSON), nil, 0)
	if err != nil {
		t.Fatalf("parseLiteLLMJSON error: %v", err)
	}

	var gemini ModelPrice
	for _, p := range prices {
		if p.Model == "gemini-2.5-pro" {
			gemini = p
			break
		}
	}

	if gemini.Model == "" {
		t.Fatal("gemini-2.5-pro not found")
	}

	if !gemini.HasTiers() {
		t.Fatal("gemini-2.5-pro should have tiered pricing")
	}

	if len(gemini.Tiers) != 1 {
		t.Fatalf("expected 1 tier, got %d", len(gemini.Tiers))
	}

	tier := gemini.Tiers[0]
	if tier.ThresholdTokens != 200_000 {
		t.Errorf("threshold = %d, want 200000", tier.ThresholdTokens)
	}
	assertFloat(t, "gemini input multiplier", tier.InputMultiplier, 2.0)
	assertFloat(t, "gemini output multiplier", tier.OutputMultiplier, 1.5)
}

func TestParseLiteLLMJSON_tieredPricing_bedrockOpus(t *testing.T) {
	prices, err := parseLiteLLMJSON([]byte(minimalLiteLLMJSON), nil, 0)
	if err != nil {
		t.Fatalf("parseLiteLLMJSON error: %v", err)
	}

	var bedrockOpus ModelPrice
	for _, p := range prices {
		if p.Model == "anthropic.claude-opus-4-6-v1" {
			bedrockOpus = p
			break
		}
	}

	if bedrockOpus.Model == "" {
		t.Fatal("anthropic.claude-opus-4-6-v1 not found")
	}

	// Bedrock Opus 4.6 = $5/M input, $25/M output (cheaper than direct API).
	assertFloat(t, "bedrock opus input", bedrockOpus.InputPerMToken, 5.0)
	assertFloat(t, "bedrock opus output", bedrockOpus.OutputPerMToken, 25.0)

	if !bedrockOpus.HasTiers() {
		t.Fatal("bedrock opus should have tiered pricing")
	}

	tier := bedrockOpus.Tiers[0]
	if tier.ThresholdTokens != 200_000 {
		t.Errorf("threshold = %d, want 200000", tier.ThresholdTokens)
	}
	// $10/M above 200k / $5/M base = 2.0x
	assertFloat(t, "bedrock opus input multiplier", tier.InputMultiplier, 2.0)
	// $37.5/M above 200k / $25/M base = 1.5x
	assertFloat(t, "bedrock opus output multiplier", tier.OutputMultiplier, 1.5)
}

func TestParseLiteLLMJSON_tieredPricing_128kThreshold(t *testing.T) {
	prices, err := parseLiteLLMJSON([]byte(tieredLiteLLMJSON), nil, 0)
	if err != nil {
		t.Fatalf("parseLiteLLMJSON error: %v", err)
	}

	var model128k ModelPrice
	for _, p := range prices {
		if p.Model == "test-model-128k-tier" {
			model128k = p
			break
		}
	}

	if model128k.Model == "" {
		t.Fatal("test-model-128k-tier not found")
	}

	if !model128k.HasTiers() {
		t.Fatal("should have tiers")
	}

	if len(model128k.Tiers) != 1 {
		t.Fatalf("expected 1 tier, got %d", len(model128k.Tiers))
	}

	tier := model128k.Tiers[0]
	if tier.ThresholdTokens != 128_000 {
		t.Errorf("threshold = %d, want 128000", tier.ThresholdTokens)
	}
	assertFloat(t, "128k input multiplier", tier.InputMultiplier, 2.0)
	assertFloat(t, "128k output multiplier", tier.OutputMultiplier, 1.5)
}

func TestParseLiteLLMJSON_tieredPricing_multiTier(t *testing.T) {
	prices, err := parseLiteLLMJSON([]byte(tieredLiteLLMJSON), nil, 0)
	if err != nil {
		t.Fatalf("parseLiteLLMJSON error: %v", err)
	}

	var multiTier ModelPrice
	for _, p := range prices {
		if p.Model == "test-model-multi-tier" {
			multiTier = p
			break
		}
	}

	if multiTier.Model == "" {
		t.Fatal("test-model-multi-tier not found")
	}

	if len(multiTier.Tiers) != 2 {
		t.Fatalf("expected 2 tiers, got %d", len(multiTier.Tiers))
	}

	// First tier: 128k.
	if multiTier.Tiers[0].ThresholdTokens != 128_000 {
		t.Errorf("tier[0] threshold = %d, want 128000", multiTier.Tiers[0].ThresholdTokens)
	}
	assertFloat(t, "tier[0] input mult", multiTier.Tiers[0].InputMultiplier, 1.5)
	assertFloat(t, "tier[0] output mult", multiTier.Tiers[0].OutputMultiplier, 1.5)

	// Second tier: 256k.
	if multiTier.Tiers[1].ThresholdTokens != 256_000 {
		t.Errorf("tier[1] threshold = %d, want 256000", multiTier.Tiers[1].ThresholdTokens)
	}
	assertFloat(t, "tier[1] input mult", multiTier.Tiers[1].InputMultiplier, 3.0)
	assertFloat(t, "tier[1] output mult", multiTier.Tiers[1].OutputMultiplier, 2.0)
}

func TestParseLiteLLMJSON_noTiersForFlatModels(t *testing.T) {
	prices, err := parseLiteLLMJSON([]byte(minimalLiteLLMJSON), nil, 0)
	if err != nil {
		t.Fatalf("parseLiteLLMJSON error: %v", err)
	}

	var opus ModelPrice
	for _, p := range prices {
		if p.Model == "claude-opus-4-1" {
			opus = p
			break
		}
	}

	if opus.HasTiers() {
		t.Error("claude-opus-4-1 (direct API) should not have tiers — no _above_ fields")
	}
}

// ── Provider Filter Tests ───────────────────────────────────────────────────

func TestParseLiteLLMJSON_providerFilter(t *testing.T) {
	prices, err := parseLiteLLMJSON([]byte(minimalLiteLLMJSON), []string{"anthropic"}, 0)
	if err != nil {
		t.Fatalf("parseLiteLLMJSON error: %v", err)
	}

	for _, p := range prices {
		if p.Model == "gpt-4o" || p.Model == "gemini-2.5-pro" || p.Model == "gpt-4o-mini" || p.Model == "o3-mini" {
			t.Errorf("model %q should be filtered out (not anthropic provider)", p.Model)
		}
	}

	// Should only have anthropic models: claude-opus-4-1, claude-sonnet-4-20250514, claude-haiku-3.5
	if len(prices) != 3 {
		t.Errorf("expected 3 anthropic models, got %d", len(prices))
		for _, p := range prices {
			t.Logf("  model: %s", p.Model)
		}
	}
}

func TestParseLiteLLMJSON_providerFilter_multiple(t *testing.T) {
	prices, err := parseLiteLLMJSON([]byte(minimalLiteLLMJSON), []string{"anthropic", "openai"}, 0)
	if err != nil {
		t.Fatalf("parseLiteLLMJSON error: %v", err)
	}

	// anthropic: 3 (opus-4-1, sonnet-4, haiku-3.5), openai: 3 (gpt-4o, gpt-4o-mini, o3-mini)
	if len(prices) != 6 {
		t.Errorf("expected 6 models, got %d", len(prices))
	}
}

func TestParseLiteLLMJSON_maxModels(t *testing.T) {
	prices, err := parseLiteLLMJSON([]byte(minimalLiteLLMJSON), nil, 2)
	if err != nil {
		t.Fatalf("parseLiteLLMJSON error: %v", err)
	}
	if len(prices) != 2 {
		t.Errorf("expected 2 models (maxModels=2), got %d", len(prices))
	}
}

// ── NewLiteLLMCatalogFromData Tests ─────────────────────────────────────────

func TestNewLiteLLMCatalogFromData_implementsCatalog(t *testing.T) {
	cat, err := NewLiteLLMCatalogFromData([]byte(minimalLiteLLMJSON), nil, 0)
	if err != nil {
		t.Fatalf("NewLiteLLMCatalogFromData error: %v", err)
	}

	// Verify it implements the Catalog interface.
	var _ Catalog = cat
}

func TestNewLiteLLMCatalogFromData_lookup(t *testing.T) {
	cat, err := NewLiteLLMCatalogFromData([]byte(minimalLiteLLMJSON), nil, 0)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	tests := []struct {
		model string
		found bool
		input float64 // expected InputPerMToken
	}{
		{"claude-opus-4-1", true, 15.0},
		{"claude-sonnet-4-20250514", true, 3.0},
		{"gpt-4o", true, 2.5},
		{"gemini-2.5-pro", true, 1.25},
		{"nonexistent-model", false, 0},
	}

	for _, tt := range tests {
		price, found := cat.Lookup(tt.model)
		if found != tt.found {
			t.Errorf("Lookup(%q): found=%v, want %v", tt.model, found, tt.found)
		}
		if found {
			assertFloat(t, fmt.Sprintf("Lookup(%q) input", tt.model), price.InputPerMToken, tt.input)
		}
	}
}

func TestNewLiteLLMCatalogFromData_list(t *testing.T) {
	cat, err := NewLiteLLMCatalogFromData([]byte(minimalLiteLLMJSON), nil, 0)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	models := cat.List()
	if len(models) != 8 {
		t.Errorf("List() returned %d models, want 8", len(models))
	}
}

// ── FallbackCatalog Tests ───────────────────────────────────────────────────

func TestFallbackCatalog_primaryWins(t *testing.T) {
	// Create primary with a custom price for claude-opus-4-1.
	primary := newEmbeddedCatalog([]ModelPrice{
		{Model: "claude-opus-4-1", InputPerMToken: 99.0, OutputPerMToken: 199.0},
	})

	fallback := newEmbeddedCatalog([]ModelPrice{
		{Model: "claude-opus-4-1", InputPerMToken: 15.0, OutputPerMToken: 75.0},
		{Model: "gpt-4o", InputPerMToken: 2.5, OutputPerMToken: 10.0},
	})

	cat := NewFallbackCatalog(primary, fallback)

	// Primary wins for claude-opus-4-1.
	price, found := cat.Lookup("claude-opus-4-1")
	if !found {
		t.Fatal("claude-opus-4-1 not found")
	}
	assertFloat(t, "primary wins input", price.InputPerMToken, 99.0)

	// Fallback provides gpt-4o.
	price, found = cat.Lookup("gpt-4o")
	if !found {
		t.Fatal("gpt-4o not found in fallback")
	}
	assertFloat(t, "fallback gpt-4o input", price.InputPerMToken, 2.5)
}

func TestFallbackCatalog_listMergesNoDuplicates(t *testing.T) {
	primary := newEmbeddedCatalog([]ModelPrice{
		{Model: "model-a", InputPerMToken: 1.0, OutputPerMToken: 2.0},
		{Model: "model-b", InputPerMToken: 3.0, OutputPerMToken: 4.0},
	})

	fallback := newEmbeddedCatalog([]ModelPrice{
		{Model: "model-b", InputPerMToken: 30.0, OutputPerMToken: 40.0}, // duplicate
		{Model: "model-c", InputPerMToken: 5.0, OutputPerMToken: 6.0},
	})

	cat := NewFallbackCatalog(primary, fallback)

	models := cat.List()
	if len(models) != 3 {
		t.Errorf("List() returned %d models, want 3 (no duplicates)", len(models))
	}

	// model-b should have primary's price.
	for _, m := range models {
		if m.Model == "model-b" {
			assertFloat(t, "model-b from primary", m.InputPerMToken, 3.0)
		}
	}
}

func TestFallbackCatalog_notFound(t *testing.T) {
	primary := newEmbeddedCatalog([]ModelPrice{
		{Model: "model-a", InputPerMToken: 1.0, OutputPerMToken: 2.0},
	})
	fallback := newEmbeddedCatalog([]ModelPrice{
		{Model: "model-b", InputPerMToken: 3.0, OutputPerMToken: 4.0},
	})

	cat := NewFallbackCatalog(primary, fallback)

	_, found := cat.Lookup("nonexistent")
	if found {
		t.Error("should not find nonexistent model")
	}
}

// ── Calculator Integration with LiteLLM Catalog ────────────────────────────

func TestCalculator_withLiteLLMCatalog(t *testing.T) {
	cat, err := NewLiteLLMCatalogFromData([]byte(minimalLiteLLMJSON), nil, 0)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	calc := NewCalculatorWithCatalog(cat)

	// Test basic cost computation.
	cost := calc.MessageCost("claude-opus-4-1", 1_000_000, 100_000)

	// Input: 1M tokens * $15/M = $15.00
	assertFloat(t, "input cost", cost.InputCost, 15.0)
	// Output: 100K tokens * $75/M = $7.50
	assertFloat(t, "output cost", cost.OutputCost, 7.5)
	assertFloat(t, "total cost", cost.TotalCost, 22.5)
}

func TestCalculator_withLiteLLMCatalog_cacheAware(t *testing.T) {
	cat, err := NewLiteLLMCatalogFromData([]byte(minimalLiteLLMJSON), nil, 0)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	calc := NewCalculatorWithCatalog(cat)

	// 1M input, 100K output, 900K cache read, 50K cache write.
	cost := calc.MessageCostWithCache("claude-opus-4-1", 1_000_000, 100_000, 900_000, 50_000)

	// Raw input = 1M - 900K - 50K = 50K → 50K * $15/M = $0.75
	// Cache read = 900K * $1.50/M = $1.35
	// Cache write = 50K * $18.75/M = $0.9375
	// Total input = $0.75 + $1.35 + $0.9375 = $3.0375
	// Output = 100K * $75/M = $7.50
	// Total = $10.5375

	assertFloatApprox(t, "cache-aware total", 10.5375, cost.TotalCost, 0.001)
}

// ── UpdateCache Tests ───────────────────────────────────────────────────────

func TestUpdateCache_success(t *testing.T) {
	// Create a test HTTP server serving our fixture data.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, minimalLiteLLMJSON)
	}))
	defer srv.Close()

	tmpDir := t.TempDir()

	count, err := UpdateCache(LiteLLMCatalogConfig{
		CacheDir:  tmpDir,
		SourceURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("UpdateCache error: %v", err)
	}

	// 8 chat models in our fixture.
	if count != 8 {
		t.Errorf("UpdateCache returned %d chat models, want 8", count)
	}

	// Verify cache file exists.
	cachePath := filepath.Join(tmpDir, liteLLMCacheFileName)
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache file not created: %v", err)
	}

	// Load from cache.
	cat, err := NewLiteLLMCatalog(LiteLLMCatalogConfig{CacheDir: tmpDir})
	if err != nil {
		t.Fatalf("NewLiteLLMCatalog error: %v", err)
	}

	price, found := cat.Lookup("gpt-4o")
	if !found {
		t.Fatal("gpt-4o not found after cache round-trip")
	}
	assertFloat(t, "gpt-4o after cache", price.InputPerMToken, 2.5)
}

func TestUpdateCache_httpError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := UpdateCache(LiteLLMCatalogConfig{
		CacheDir:  t.TempDir(),
		SourceURL: srv.URL,
	})
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("error should mention HTTP status: %v", err)
	}
}

func TestUpdateCache_invalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()

	_, err := UpdateCache(LiteLLMCatalogConfig{
		CacheDir:  t.TempDir(),
		SourceURL: srv.URL,
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestUpdateCache_tooFewModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"model-a": {"mode": "chat"}}`)
	}))
	defer srv.Close()

	_, err := UpdateCache(LiteLLMCatalogConfig{
		CacheDir:  t.TempDir(),
		SourceURL: srv.URL,
	})
	if err == nil {
		t.Fatal("expected error for too few models")
	}
	if !strings.Contains(err.Error(), "few models") {
		t.Errorf("error should mention few models: %v", err)
	}
}

// ── CacheInfo Tests ─────────────────────────────────────────────────────────

func TestCacheInfo_noCache(t *testing.T) {
	info := CacheInfo(t.TempDir())
	if info.Exists {
		t.Error("cache should not exist in empty dir")
	}
}

func TestCacheInfo_withCache(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, liteLLMCacheFileName)
	if err := os.WriteFile(cachePath, []byte(minimalLiteLLMJSON), 0o644); err != nil {
		t.Fatalf("write error: %v", err)
	}

	info := CacheInfo(tmpDir)
	if !info.Exists {
		t.Fatal("cache should exist")
	}
	if info.ModelCount != 11 { // total entries in JSON, not just chat
		t.Errorf("model count = %d, want 11", info.ModelCount)
	}
	if info.Stale {
		t.Error("freshly written cache should not be stale")
	}
}

// ── NewLiteLLMCatalog Tests (filesystem) ────────────────────────────────────

func TestNewLiteLLMCatalog_noCache(t *testing.T) {
	_, err := NewLiteLLMCatalog(LiteLLMCatalogConfig{CacheDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error when cache doesn't exist")
	}
	if !strings.Contains(err.Error(), "update-prices") {
		t.Errorf("error should suggest 'update-prices': %v", err)
	}
}

func TestNewLiteLLMCatalog_withCache(t *testing.T) {
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, liteLLMCacheFileName)
	if err := os.WriteFile(cachePath, []byte(minimalLiteLLMJSON), 0o644); err != nil {
		t.Fatalf("write error: %v", err)
	}

	cat, err := NewLiteLLMCatalog(LiteLLMCatalogConfig{CacheDir: tmpDir})
	if err != nil {
		t.Fatalf("NewLiteLLMCatalog error: %v", err)
	}

	models := cat.List()
	if len(models) != 8 {
		t.Errorf("expected 8 chat models, got %d", len(models))
	}
}

// ── parseThresholdValue Tests ───────────────────────────────────────────────

func TestParseThresholdValue(t *testing.T) {
	tests := []struct {
		num    string
		suffix string
		want   int
		err    bool
	}{
		{"200", "k", 200_000, false},
		{"128", "k", 128_000, false},
		{"256", "K", 256_000, false},
		{"1", "m", 1_000_000, false},
		{"100", "", 100, false},
		{"abc", "k", 0, true},
	}

	for _, tt := range tests {
		got, err := parseThresholdValue(tt.num, tt.suffix)
		if (err != nil) != tt.err {
			t.Errorf("parseThresholdValue(%q, %q) error=%v, wantErr=%v", tt.num, tt.suffix, err, tt.err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseThresholdValue(%q, %q) = %d, want %d", tt.num, tt.suffix, got, tt.want)
		}
	}
}

// ── roundMultiplier Tests ───────────────────────────────────────────────────

func TestRoundMultiplier(t *testing.T) {
	tests := []struct {
		input float64
		want  float64
	}{
		{2.0, 2.0},
		{1.9999999, 2.0},
		{1.5000001, 1.5},
		{1.333333333, 1.3333},
		{0.1, 0.1},
	}

	for _, tt := range tests {
		got := roundMultiplier(tt.input)
		if math.Abs(got-tt.want) > 0.00001 {
			t.Errorf("roundMultiplier(%f) = %f, want %f", tt.input, got, tt.want)
		}
	}
}

// ── Edge Cases ──────────────────────────────────────────────────────────────

func TestParseLiteLLMJSON_emptyJSON(t *testing.T) {
	prices, err := parseLiteLLMJSON([]byte(`{}`), nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prices) != 0 {
		t.Errorf("expected 0 models from empty JSON, got %d", len(prices))
	}
}

func TestParseLiteLLMJSON_invalidJSON(t *testing.T) {
	_, err := parseLiteLLMJSON([]byte(`not json`), nil, 0)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseLiteLLMJSON_malformedEntry(t *testing.T) {
	// One valid entry, one malformed.
	data := `{
		"valid-model": {
			"input_cost_per_token": 1e-06,
			"output_cost_per_token": 5e-06,
			"litellm_provider": "test",
			"mode": "chat"
		},
		"malformed-model": "not an object"
	}`

	prices, err := parseLiteLLMJSON([]byte(data), nil, 0)
	if err != nil {
		t.Fatalf("should skip malformed, not error: %v", err)
	}
	if len(prices) != 1 {
		t.Errorf("expected 1 valid model, got %d", len(prices))
	}
}

func TestParseLiteLLMJSON_missingCostFields(t *testing.T) {
	// Model with input but no output cost → should be excluded.
	data := `{
		"no-output": {
			"input_cost_per_token": 1e-06,
			"litellm_provider": "test",
			"mode": "chat"
		},
		"no-input": {
			"output_cost_per_token": 5e-06,
			"litellm_provider": "test",
			"mode": "chat"
		}
	}`

	prices, err := parseLiteLLMJSON([]byte(data), nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prices) != 0 {
		t.Errorf("expected 0 models (missing cost fields), got %d", len(prices))
	}
}

// ── Full Integration: LiteLLM + Embedded Fallback ───────────────────────────

func TestFallbackCatalog_liteLLMWithEmbeddedFallback(t *testing.T) {
	liteLLM, err := NewLiteLLMCatalogFromData([]byte(minimalLiteLLMJSON), nil, 0)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	embedded, err := NewEmbeddedCatalog()
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	cat := NewFallbackCatalog(liteLLM, embedded)

	// LiteLLM provides claude-opus-4-1 ($15/M) — matches "claude-opus-4-1".
	price, found := cat.Lookup("claude-opus-4-1")
	if !found {
		t.Fatal("claude-opus-4-1 not found")
	}
	assertFloat(t, "opus from litellm", price.InputPerMToken, 15.0)

	// Embedded provides claude-haiku-4 (not in our LiteLLM fixture).
	price, found = cat.Lookup("claude-haiku-4")
	if !found {
		t.Fatal("claude-haiku-4 should be found in embedded fallback")
	}
	assertFloat(t, "haiku from embedded", price.InputPerMToken, 0.80)

	// Total models should be the union.
	liteLLMCount := len(liteLLM.List())
	embeddedCount := len(embedded.List())
	totalCount := len(cat.List())
	if totalCount < liteLLMCount || totalCount < embeddedCount {
		t.Errorf("merged list (%d) should be >= max(%d, %d)", totalCount, liteLLMCount, embeddedCount)
	}
}

// ── Real LiteLLM Data Test (optional, uses downloaded file) ─────────────────

func TestParseLiteLLMJSON_realData(t *testing.T) {
	data, err := os.ReadFile("/tmp/litellm_prices.json")
	if err != nil {
		t.Skip("skipping real data test: /tmp/litellm_prices.json not found (run: curl -sL '" + LiteLLMURL + "' -o /tmp/litellm_prices.json)")
	}

	prices, err := parseLiteLLMJSON(data, nil, 0)
	if err != nil {
		t.Fatalf("error parsing real LiteLLM data: %v", err)
	}

	if len(prices) < 500 {
		t.Errorf("expected 500+ chat models from real data, got %d", len(prices))
	}

	// Verify a few well-known models.
	cat := newEmbeddedCatalog(prices)

	wellKnown := []struct {
		model    string
		minInput float64
	}{
		{"gpt-4o", 1.0},
		{"gemini-2.5-pro", 0.5},
	}

	for _, wk := range wellKnown {
		price, found := cat.Lookup(wk.model)
		if !found {
			t.Errorf("well-known model %q not found in real data", wk.model)
			continue
		}
		if price.InputPerMToken < wk.minInput {
			t.Errorf("%s input = $%.4f/M, expected at least $%.2f/M", wk.model, price.InputPerMToken, wk.minInput)
		}
	}

	// Count tiered models.
	tieredCount := 0
	for _, p := range prices {
		if p.HasTiers() {
			tieredCount++
		}
	}
	t.Logf("Real data: %d total chat models, %d with tiered pricing", len(prices), tieredCount)
}

// assertFloatApprox checks that got is within tolerance of want.
func assertFloatApprox(t *testing.T, name string, want, got, tolerance float64) {
	t.Helper()
	if math.Abs(want-got) > tolerance {
		t.Errorf("%s = %.6f, want %.6f (±%.4f)", name, got, want, tolerance)
	}
}
