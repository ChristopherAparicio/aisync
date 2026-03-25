package pricing

import (
	"math"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── EmbeddedCatalog Tests ───────────────────────────────────────────────────

func TestEmbeddedCatalog_loadsFromYAML(t *testing.T) {
	cat, err := NewEmbeddedCatalog()
	if err != nil {
		t.Fatalf("NewEmbeddedCatalog() error: %v", err)
	}
	models := cat.List()
	if len(models) < 10 {
		t.Errorf("expected at least 10 models in catalog, got %d", len(models))
	}
}

func TestEmbeddedCatalog_opusHasTiers(t *testing.T) {
	cat, err := NewEmbeddedCatalog()
	if err != nil {
		t.Fatalf("NewEmbeddedCatalog() error: %v", err)
	}
	price, found := cat.Lookup("claude-opus-4")
	if !found {
		t.Fatal("expected to find claude-opus-4 in catalog")
	}
	if !price.HasTiers() {
		t.Fatal("claude-opus-4 should have tiered pricing")
	}
	if len(price.Tiers) != 1 {
		t.Fatalf("expected 1 tier, got %d", len(price.Tiers))
	}
	tier := price.Tiers[0]
	if tier.ThresholdTokens != 200_000 {
		t.Errorf("ThresholdTokens = %d, want 200000", tier.ThresholdTokens)
	}
	if tier.InputMultiplier != 2.0 {
		t.Errorf("InputMultiplier = %f, want 2.0", tier.InputMultiplier)
	}
	if tier.OutputMultiplier != 2.0 {
		t.Errorf("OutputMultiplier = %f, want 2.0", tier.OutputMultiplier)
	}
}

func TestEmbeddedCatalog_sonnetHasNoTiers(t *testing.T) {
	cat, err := NewEmbeddedCatalog()
	if err != nil {
		t.Fatalf("NewEmbeddedCatalog() error: %v", err)
	}
	price, found := cat.Lookup("claude-sonnet-4")
	if !found {
		t.Fatal("expected to find claude-sonnet-4")
	}
	if price.HasTiers() {
		t.Error("claude-sonnet-4 should not have tiered pricing")
	}
}

func TestEmbeddedCatalog_cacheRates(t *testing.T) {
	cat, err := NewEmbeddedCatalog()
	if err != nil {
		t.Fatalf("NewEmbeddedCatalog() error: %v", err)
	}
	price, found := cat.Lookup("claude-opus-4")
	if !found {
		t.Fatal("expected to find claude-opus-4")
	}
	if price.CacheReadPerMToken != 1.50 {
		t.Errorf("CacheReadPerMToken = %f, want 1.50", price.CacheReadPerMToken)
	}
	if price.CacheWritePerMToken != 18.75 {
		t.Errorf("CacheWritePerMToken = %f, want 18.75", price.CacheWritePerMToken)
	}
}

func TestEmbeddedCatalog_prefixMatching(t *testing.T) {
	cat, err := NewEmbeddedCatalog()
	if err != nil {
		t.Fatalf("NewEmbeddedCatalog() error: %v", err)
	}

	// Dated variant should match the prefix
	price, found := cat.Lookup("claude-opus-4-6-20250514")
	if !found {
		t.Fatal("expected prefix match for dated variant")
	}
	if price.Model != "claude-opus-4" {
		t.Errorf("Model = %q, want claude-opus-4", price.Model)
	}
}

func TestEmbeddedCatalog_cloudProviderNormalization(t *testing.T) {
	cat, err := NewEmbeddedCatalog()
	if err != nil {
		t.Fatalf("NewEmbeddedCatalog() error: %v", err)
	}

	tests := []struct {
		input     string
		wantModel string
	}{
		{"global.anthropic.claude-opus-4-6-v1", "claude-opus-4"},
		{"us.anthropic.claude-sonnet-4-6-v1", "claude-sonnet-4"},
		{"anthropic.claude-haiku-4", "claude-haiku-4"},
	}

	for _, tt := range tests {
		price, found := cat.Lookup(tt.input)
		if !found {
			t.Errorf("Lookup(%q): expected to find model", tt.input)
			continue
		}
		if price.Model != tt.wantModel {
			t.Errorf("Lookup(%q).Model = %q, want %q", tt.input, price.Model, tt.wantModel)
		}
	}
}

// ── OverrideCatalog Tests ───────────────────────────────────────────────────

func TestOverrideCatalog_overridesWin(t *testing.T) {
	base := mustEmbeddedCatalog()
	overrides := []ModelPrice{
		{Model: "claude-sonnet-4", InputPerMToken: 99.0, OutputPerMToken: 99.0},
	}
	cat := NewOverrideCatalog(base, overrides)

	price, found := cat.Lookup("claude-sonnet-4")
	if !found {
		t.Fatal("expected to find overridden model")
	}
	if price.InputPerMToken != 99.0 {
		t.Errorf("InputPerMToken = %f, want 99.0 (override)", price.InputPerMToken)
	}
}

func TestOverrideCatalog_baseFallback(t *testing.T) {
	base := mustEmbeddedCatalog()
	overrides := []ModelPrice{
		{Model: "my-custom-model", InputPerMToken: 1.0, OutputPerMToken: 2.0},
	}
	cat := NewOverrideCatalog(base, overrides)

	// Custom model from overrides
	_, found := cat.Lookup("my-custom-model")
	if !found {
		t.Fatal("expected to find custom model from overrides")
	}

	// Base model still accessible
	price, found := cat.Lookup("claude-opus-4")
	if !found {
		t.Fatal("expected to find base model")
	}
	if price.InputPerMToken != 15.0 {
		t.Errorf("InputPerMToken = %f, want 15.0 (base)", price.InputPerMToken)
	}
}

func TestOverrideCatalog_listMerged(t *testing.T) {
	base := mustEmbeddedCatalog()
	baseCount := len(base.List())

	overrides := []ModelPrice{
		{Model: "brand-new-model", InputPerMToken: 1.0, OutputPerMToken: 2.0},
	}
	cat := NewOverrideCatalog(base, overrides)

	// Should have base models + 1 new
	if len(cat.List()) != baseCount+1 {
		t.Errorf("List() length = %d, want %d", len(cat.List()), baseCount+1)
	}
}

func TestOverrideCatalog_overrideWithTiers(t *testing.T) {
	base := mustEmbeddedCatalog()
	overrides := []ModelPrice{
		{
			Model:           "claude-sonnet-4",
			InputPerMToken:  5.0,
			OutputPerMToken: 25.0,
			Tiers: []PricingTier{
				{ThresholdTokens: 100_000, InputMultiplier: 3.0, OutputMultiplier: 3.0},
			},
		},
	}
	cat := NewOverrideCatalog(base, overrides)

	price, found := cat.Lookup("claude-sonnet-4")
	if !found {
		t.Fatal("expected to find overridden model")
	}
	if !price.HasTiers() {
		t.Fatal("overridden model should have tiers")
	}
	if price.Tiers[0].InputMultiplier != 3.0 {
		t.Errorf("InputMultiplier = %f, want 3.0", price.Tiers[0].InputMultiplier)
	}
}

// ── Tiered Pricing Computation Tests ────────────────────────────────────────

func TestComputeTieredCost_noTiers(t *testing.T) {
	// Without tiers, should behave like flat rate.
	cost := computeTieredCost(100_000, 15.0, nil)
	// 100k tokens at $15/M = $1.50
	assertTieredFloat(t, "flat rate", cost, 1.50)
}

func TestComputeTieredCost_belowThreshold(t *testing.T) {
	tiers := []PricingTier{
		{ThresholdTokens: 200_000, InputMultiplier: 2.0},
	}
	// 100k tokens < 200k threshold → all at base rate
	cost := computeTieredCost(100_000, 15.0, tiers)
	assertTieredFloat(t, "below threshold", cost, 1.50)
}

func TestComputeTieredCost_exactlyAtThreshold(t *testing.T) {
	tiers := []PricingTier{
		{ThresholdTokens: 200_000, InputMultiplier: 2.0},
	}
	// 200k tokens = exactly at threshold → all at base rate
	cost := computeTieredCost(200_000, 15.0, tiers)
	assertTieredFloat(t, "at threshold", cost, 3.00)
}

func TestComputeTieredCost_aboveThreshold(t *testing.T) {
	tiers := []PricingTier{
		{ThresholdTokens: 200_000, InputMultiplier: 2.0},
	}
	// 300k tokens:
	//   First 200k at $15/M = $3.00
	//   Next 100k at $30/M (2x) = $3.00
	//   Total = $6.00
	cost := computeTieredCost(300_000, 15.0, tiers)
	assertTieredFloat(t, "above threshold", cost, 6.00)
}

func TestComputeTieredCost_wellAboveThreshold(t *testing.T) {
	tiers := []PricingTier{
		{ThresholdTokens: 200_000, InputMultiplier: 2.0},
	}
	// 1M tokens:
	//   First 200k at $15/M = $3.00
	//   Next 800k at $30/M = $24.00
	//   Total = $27.00
	cost := computeTieredCost(1_000_000, 15.0, tiers)
	assertTieredFloat(t, "well above threshold", cost, 27.00)
}

func TestComputeTieredCost_zeroTokens(t *testing.T) {
	tiers := []PricingTier{
		{ThresholdTokens: 200_000, InputMultiplier: 2.0},
	}
	cost := computeTieredCost(0, 15.0, tiers)
	assertTieredFloat(t, "zero tokens", cost, 0.0)
}

func TestComputeTieredOutputCost(t *testing.T) {
	tiers := []PricingTier{
		{ThresholdTokens: 200_000, OutputMultiplier: 2.0},
	}
	// 300k output tokens:
	//   First 200k at $75/M = $15.00
	//   Next 100k at $150/M (2x) = $15.00
	//   Total = $30.00
	cost := computeTieredOutputCost(300_000, 75.0, tiers)
	assertTieredFloat(t, "tiered output", cost, 30.00)
}

// ── Integration: SessionCost with Tiered Pricing ────────────────────────────

func TestSessionCost_opusTiered_belowThreshold(t *testing.T) {
	c := NewCalculator()

	// 100k input tokens → below 200k threshold → flat rate
	sess := &session.Session{
		TokenUsage: session.TokenUsage{
			InputTokens:  100_000,
			OutputTokens: 10_000,
			TotalTokens:  110_000,
		},
		Messages: []session.Message{
			{Role: session.RoleAssistant, Model: "claude-opus-4", InputTokens: 100_000, OutputTokens: 10_000},
		},
	}

	est := c.SessionCost(sess)
	mc := est.PerModel[0]

	// Below threshold: flat rate applies.
	// Input: 100k * $15/M = $1.50
	// Output: 10k * $75/M = $0.75
	// Total = $2.25
	assertTieredFloat(t, "TotalCost (below threshold)", mc.Cost.TotalCost, 2.25)
}

func TestSessionCost_opusTiered_aboveThreshold(t *testing.T) {
	c := NewCalculator()

	// 300k input tokens → above 200k threshold → tiered pricing kicks in
	sess := &session.Session{
		TokenUsage: session.TokenUsage{
			InputTokens:  300_000,
			OutputTokens: 10_000,
			TotalTokens:  310_000,
		},
		Messages: []session.Message{
			{Role: session.RoleAssistant, Model: "claude-opus-4", InputTokens: 300_000, OutputTokens: 10_000},
		},
	}

	est := c.SessionCost(sess)
	mc := est.PerModel[0]

	// Input (tiered):
	//   First 200k at $15/M = $3.00
	//   Next 100k at $30/M = $3.00
	//   Total input = $6.00
	// Output (tiered):
	//   All 10k at $75/M (below 200k output threshold) = $0.75
	// Total = $6.75
	assertTieredFloat(t, "InputCost (tiered)", mc.Cost.InputCost, 6.00)
	assertTieredFloat(t, "OutputCost (tiered)", mc.Cost.OutputCost, 0.75)
	assertTieredFloat(t, "TotalCost (tiered)", mc.Cost.TotalCost, 6.75)
}

func TestSessionCost_opusTiered_costsMoreThanFlat(t *testing.T) {
	c := NewCalculator()

	// With tiered pricing, 300k tokens should cost MORE than flat rate.
	sess := &session.Session{
		TokenUsage: session.TokenUsage{
			InputTokens:  300_000,
			OutputTokens: 10_000,
		},
		Messages: []session.Message{
			{Role: session.RoleAssistant, Model: "claude-opus-4", InputTokens: 300_000, OutputTokens: 10_000},
		},
	}
	tieredEst := c.SessionCost(sess)

	// Compare with a hypothetical flat-rate calc.
	flatRate := float64(300_000)*15.0/1_000_000 + float64(10_000)*75.0/1_000_000
	tieredTotal := tieredEst.TotalCost.TotalCost

	if tieredTotal <= flatRate {
		t.Errorf("tiered cost ($%f) should be > flat rate ($%f) for 300k tokens", tieredTotal, flatRate)
	}
}

func TestSessionCost_sonnetFlat_noTierEffect(t *testing.T) {
	c := NewCalculator()

	// Sonnet has no tiers → flat rate regardless of token count.
	sess := &session.Session{
		TokenUsage: session.TokenUsage{
			InputTokens:  300_000,
			OutputTokens: 10_000,
		},
		Messages: []session.Message{
			{Role: session.RoleAssistant, Model: "claude-sonnet-4", InputTokens: 300_000, OutputTokens: 10_000},
		},
	}

	est := c.SessionCost(sess)
	mc := est.PerModel[0]

	// Flat rate: 300k * $3/M = $0.90 input, 10k * $15/M = $0.15 output
	assertTieredFloat(t, "InputCost (flat)", mc.Cost.InputCost, 0.90)
	assertTieredFloat(t, "OutputCost (flat)", mc.Cost.OutputCost, 0.15)
}

// ── Catalog Interface Compliance ────────────────────────────────────────────

func TestCatalogInterface_embedded(t *testing.T) {
	var _ Catalog = (*EmbeddedCatalog)(nil)
}

func TestCatalogInterface_override(t *testing.T) {
	var _ Catalog = (*OverrideCatalog)(nil)
}

// ── DefaultCatalog Singleton ────────────────────────────────────────────────

func TestDefaultCatalog_notNil(t *testing.T) {
	cat := DefaultCatalog()
	if cat == nil {
		t.Fatal("DefaultCatalog() returned nil")
	}
}

func TestDefaultCatalog_hasPrices(t *testing.T) {
	cat := DefaultCatalog()
	if len(cat.List()) == 0 {
		t.Fatal("DefaultCatalog() has no prices")
	}
}

// ── ModelPrice.HasTiers ─────────────────────────────────────────────────────

func TestModelPrice_HasTiers(t *testing.T) {
	mp := ModelPrice{Model: "test"}
	if mp.HasTiers() {
		t.Error("empty tiers should return false")
	}

	mp.Tiers = []PricingTier{{ThresholdTokens: 100_000, InputMultiplier: 2.0}}
	if !mp.HasTiers() {
		t.Error("non-empty tiers should return true")
	}
}

// ── Calculator with custom Catalog ──────────────────────────────────────────

func TestNewCalculatorWithCatalog(t *testing.T) {
	custom := newEmbeddedCatalog([]ModelPrice{
		{Model: "test-model", InputPerMToken: 1.0, OutputPerMToken: 2.0},
	})
	c := NewCalculatorWithCatalog(custom)

	price, found := c.Lookup("test-model")
	if !found {
		t.Fatal("expected to find test-model in custom catalog")
	}
	if price.InputPerMToken != 1.0 {
		t.Errorf("InputPerMToken = %f, want 1.0", price.InputPerMToken)
	}

	// Default models should NOT be available
	_, found = c.Lookup("claude-opus-4")
	if found {
		t.Error("custom catalog should not have default models")
	}
}

// ── Gemini Tiered Pricing ───────────────────────────────────────────────────

func TestEmbeddedCatalog_geminiHasTiers(t *testing.T) {
	cat := mustEmbeddedCatalog()
	price, found := cat.Lookup("gemini-2.5-pro")
	if !found {
		t.Fatal("expected to find gemini-2.5-pro")
	}
	if !price.HasTiers() {
		t.Error("gemini-2.5-pro should have tiered pricing")
	}
}

func assertTieredFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.001 {
		t.Errorf("%s = %f, want %f (diff = %f)", name, got, want, got-want)
	}
}
