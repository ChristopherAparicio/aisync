package pricing

import (
	"sort"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

const defaultCurrency = "USD"

// ── Calculator (Domain Service) ─────────────────────────────────────────────

// Calculator is the domain service for computing token costs.
// It delegates model lookups to a Catalog and encapsulates all pricing logic
// including tiered pricing, cache-aware costing, and session-level aggregation.
//
// No pricing logic should leak outside this service. Consumers call
// Calculator methods with (model, tokens) and receive Cost values.
type Calculator struct {
	catalog Catalog
}

// NewCalculator creates a Calculator backed by the built-in embedded catalog.
func NewCalculator() *Calculator {
	return &Calculator{catalog: DefaultCatalog()}
}

// NewCalculatorWithCatalog creates a Calculator backed by a custom Catalog.
// This is the primary constructor for dependency injection.
func NewCalculatorWithCatalog(cat Catalog) *Calculator {
	return &Calculator{catalog: cat}
}

// WithOverrides returns a new Calculator that layers user-configured price
// overrides on top of the current catalog. Overrides take precedence.
func (c *Calculator) WithOverrides(overrides []ModelPrice) *Calculator {
	cat := NewOverrideCatalog(c.catalog, overrides)
	return &Calculator{catalog: cat}
}

// Lookup finds the price for a model identifier.
// Delegates to the underlying Catalog.
func (c *Calculator) Lookup(model string) (ModelPrice, bool) {
	return c.catalog.Lookup(model)
}

// CheaperAlternative returns a cheaper model suggestion for the given model, if one exists.
// Returns the alternative model name, estimated savings fraction (0.0–1.0), and true if found.
func (c *Calculator) CheaperAlternative(model string) (string, float64, bool) {
	current, ok := c.Lookup(model)
	if !ok {
		return "", 0, false
	}

	// Map of expensive → cheaper alternatives within the same family.
	alternatives := map[string]string{
		"claude-opus-4":   "claude-sonnet-4",
		"claude-sonnet-4": "claude-haiku-3.5",
		"gpt-4o":          "gpt-4o-mini",
		"gpt-4.1":         "gpt-4.1-mini",
		"gpt-4.1-mini":    "gpt-4.1-nano",
		"o3":              "o3-mini",
		"gemini-2.5-pro":  "gemini-2.5-flash",
	}

	lower := strings.ToLower(model)
	for expensive, cheap := range alternatives {
		if strings.HasPrefix(lower, expensive) {
			alt, found := c.Lookup(cheap)
			if !found {
				continue
			}
			// Weighted average cost (assume 50/50 input/output mix for simplicity).
			currentAvg := (current.InputPerMToken + current.OutputPerMToken) / 2
			altAvg := (alt.InputPerMToken + alt.OutputPerMToken) / 2
			if currentAvg <= 0 {
				continue
			}
			savings := 1.0 - (altAvg / currentAvg)
			if savings > 0.05 { // only recommend if >5% savings
				return cheap, savings, true
			}
		}
	}
	return "", 0, false
}

// ── Cost Computation ────────────────────────────────────────────────────────

// MessageCost computes the cost for a single message given its model and token counts.
// This uses the base (tier-0) rates. For tiered pricing within a session, use SessionCost.
func (c *Calculator) MessageCost(model string, inputTokens, outputTokens int) session.Cost {
	return c.MessageCostWithCache(model, inputTokens, outputTokens, 0, 0)
}

// MessageCostWithCache computes cost with separate cache token pricing.
// Cache read tokens are typically 10x cheaper than regular input tokens.
// Cache write tokens are typically 1.25x more expensive than input tokens.
//
// Note: this method applies the base rate only (no tiered pricing).
// For session-level tiered cost computation, use SessionCost.
func (c *Calculator) MessageCostWithCache(model string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int) session.Cost {
	price, found := c.Lookup(model)
	if !found {
		return session.Cost{Currency: defaultCurrency}
	}

	// Raw input = total input minus cache tokens.
	rawInput := inputTokens - cacheReadTokens - cacheWriteTokens
	if rawInput < 0 {
		rawInput = 0
	}

	// Price each component separately using base rates.
	rawInputCost := float64(rawInput) * price.InputPerMToken / 1_000_000

	cacheReadRate := price.CacheReadPerMToken
	if cacheReadRate == 0 {
		cacheReadRate = price.InputPerMToken // fallback to input price if no cache pricing
	}
	cacheReadCost := float64(cacheReadTokens) * cacheReadRate / 1_000_000

	cacheWriteRate := price.CacheWritePerMToken
	if cacheWriteRate == 0 {
		cacheWriteRate = price.InputPerMToken * 1.25 // default: 1.25x input price
	}
	cacheWriteCost := float64(cacheWriteTokens) * cacheWriteRate / 1_000_000

	totalInputCost := rawInputCost + cacheReadCost + cacheWriteCost
	outputCost := float64(outputTokens) * price.OutputPerMToken / 1_000_000

	return session.Cost{
		InputCost:  totalInputCost,
		OutputCost: outputCost,
		TotalCost:  totalInputCost + outputCost,
		Currency:   defaultCurrency,
	}
}

// computeTieredCost computes cost for a token count using tiered pricing.
// It splits the tokens across tier boundaries and applies the appropriate
// multiplier to each segment.
//
// Example for claude-opus-4 with 300k input tokens:
//   - First 200k at base rate ($15/M) = $3.00
//   - Next 100k at 2x rate ($30/M) = $3.00
//   - Total = $6.00 (vs $4.50 at flat rate)
func computeTieredCost(tokens int, baseRatePerMToken float64, tiers []PricingTier) float64 {
	if len(tiers) == 0 || tokens <= 0 {
		return float64(tokens) * baseRatePerMToken / 1_000_000
	}

	remaining := tokens
	cost := 0.0
	prevThreshold := 0

	for _, tier := range tiers {
		// Tokens in the segment before this tier's threshold use the current rate.
		segmentTokens := tier.ThresholdTokens - prevThreshold
		if segmentTokens > remaining {
			segmentTokens = remaining
		}
		if segmentTokens > 0 {
			cost += float64(segmentTokens) * baseRatePerMToken / 1_000_000
			remaining -= segmentTokens
		}
		if remaining <= 0 {
			return cost
		}
		// After this threshold, apply the tier's multiplier.
		baseRatePerMToken = baseRatePerMToken * tier.InputMultiplier
		prevThreshold = tier.ThresholdTokens
	}

	// Any remaining tokens use the last tier's rate.
	if remaining > 0 {
		cost += float64(remaining) * baseRatePerMToken / 1_000_000
	}

	return cost
}

// computeTieredOutputCost is like computeTieredCost but uses OutputMultiplier.
func computeTieredOutputCost(tokens int, baseRatePerMToken float64, tiers []PricingTier) float64 {
	if len(tiers) == 0 || tokens <= 0 {
		return float64(tokens) * baseRatePerMToken / 1_000_000
	}

	remaining := tokens
	cost := 0.0
	prevThreshold := 0

	for _, tier := range tiers {
		segmentTokens := tier.ThresholdTokens - prevThreshold
		if segmentTokens > remaining {
			segmentTokens = remaining
		}
		if segmentTokens > 0 {
			cost += float64(segmentTokens) * baseRatePerMToken / 1_000_000
			remaining -= segmentTokens
		}
		if remaining <= 0 {
			return cost
		}
		baseRatePerMToken = baseRatePerMToken * tier.OutputMultiplier
		prevThreshold = tier.ThresholdTokens
	}

	if remaining > 0 {
		cost += float64(remaining) * baseRatePerMToken / 1_000_000
	}

	return cost
}

// messageCostTiered computes cost for a single model's aggregated tokens,
// taking tiers into account. This is used by SessionCost for per-model totals.
func (c *Calculator) messageCostTiered(model string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int) session.Cost {
	price, found := c.Lookup(model)
	if !found {
		return session.Cost{Currency: defaultCurrency}
	}

	// If no tiers, fall back to flat-rate computation.
	if !price.HasTiers() {
		return c.MessageCostWithCache(model, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens)
	}

	// With tiers: compute raw input, cache read, cache write costs separately,
	// then apply tiered pricing to the total input token count.
	rawInput := inputTokens - cacheReadTokens - cacheWriteTokens
	if rawInput < 0 {
		rawInput = 0
	}

	// For tiered models, the tier threshold applies to the total context window,
	// which includes ALL input tokens (raw + cache read + cache write).
	// We compute the cost as if all inputTokens go through the tier system,
	// but then adjust for cache pricing.

	// Step 1: Compute the effective input rate at each token position.
	// We need to split the total input tokens across tiers.
	// Cache read tokens get a discount relative to the tier rate.
	// Cache write tokens get a premium relative to the tier rate.

	// Approach: compute the tiered cost for all input tokens, then
	// adjust the portion that was cache reads/writes.

	// Cost if all input tokens were at the raw input rate (tiered).
	totalInputTiered := computeTieredCost(inputTokens, price.InputPerMToken, price.Tiers)

	// The effective per-token rate (blended across tiers).
	effectiveInputRate := 0.0
	if inputTokens > 0 {
		effectiveInputRate = totalInputTiered / float64(inputTokens) * 1_000_000
	}

	// Now apply cache discounts relative to the effective rate.
	cacheReadRate := price.CacheReadPerMToken
	if cacheReadRate == 0 {
		cacheReadRate = price.InputPerMToken
	}
	// Scale cache read rate by the same tier multiplier as the effective rate.
	tierMultiplier := 1.0
	if price.InputPerMToken > 0 {
		tierMultiplier = effectiveInputRate / price.InputPerMToken
	}

	cacheWriteRate := price.CacheWritePerMToken
	if cacheWriteRate == 0 {
		cacheWriteRate = price.InputPerMToken * 1.25
	}

	rawInputCost := float64(rawInput) * effectiveInputRate / 1_000_000
	cacheReadCost := float64(cacheReadTokens) * cacheReadRate * tierMultiplier / 1_000_000
	cacheWriteCost := float64(cacheWriteTokens) * cacheWriteRate * tierMultiplier / 1_000_000

	totalInputCost := rawInputCost + cacheReadCost + cacheWriteCost

	// Output tokens also have tiered pricing.
	outputCost := computeTieredOutputCost(outputTokens, price.OutputPerMToken, price.Tiers)

	return session.Cost{
		InputCost:  totalInputCost,
		OutputCost: outputCost,
		TotalCost:  totalInputCost + outputCost,
		Currency:   defaultCurrency,
	}
}

// ── Session Cost ────────────────────────────────────────────────────────────

// SessionCost computes the full cost breakdown for a session.
//
// It produces two cost views:
//   - APICost:    what it would cost at public API per-token rates
//   - ActualCost: what was actually charged (from provider-reported cost fields)
//
// For models with tiered pricing, the tier threshold is evaluated against the
// total cumulative input tokens for that model across the entire session.
//
// Strategy for input token distribution:
//   - Session-level InputTokens are distributed proportionally across assistant
//     messages based on each message's output token share.
//   - If session-level InputTokens is zero, input cost is not computed.
func (c *Calculator) SessionCost(sess *session.Session) *session.CostEstimate {
	if sess == nil {
		return &session.CostEstimate{}
	}

	// Collect per-model aggregates.
	type modelAgg struct {
		inputTokens      int
		outputTokens     int
		cacheReadTokens  int
		cacheWriteTokens int
		msgCount         int
	}

	perModel := make(map[string]*modelAgg)
	unknownSet := make(map[string]struct{})

	// Track actual costs and billing type from provider-reported data.
	var actualTotal float64
	hasAPIMsgs := false          // at least one message with provider_cost > 0
	hasSubscriptionMsgs := false // at least one message with provider_cost == 0

	// First pass: sum per-model input and output tokens from messages.
	for i := range sess.Messages {
		msg := &sess.Messages[i]
		if msg.Role != session.RoleAssistant || msg.Model == "" {
			continue
		}

		// Accumulate actual provider-reported costs.
		if msg.ProviderCost > 0 {
			actualTotal += msg.ProviderCost
			hasAPIMsgs = true
		} else {
			hasSubscriptionMsgs = true
		}

		if _, found := c.Lookup(msg.Model); !found {
			unknownSet[msg.Model] = struct{}{}
			continue
		}

		agg, ok := perModel[msg.Model]
		if !ok {
			agg = &modelAgg{}
			perModel[msg.Model] = agg
		}
		agg.inputTokens += msg.InputTokens
		agg.outputTokens += msg.OutputTokens
		agg.msgCount++
	}

	// Fallback: if messages have no per-message input tokens, distribute
	// session-level InputTokens proportionally based on output token share.
	totalMsgInput := 0
	totalMsgOutput := 0
	for _, agg := range perModel {
		totalMsgInput += agg.inputTokens
		totalMsgOutput += agg.outputTokens
	}
	sessionInput := sess.TokenUsage.InputTokens
	if totalMsgInput == 0 && totalMsgOutput > 0 && sessionInput > 0 {
		for _, agg := range perModel {
			ratio := float64(agg.outputTokens) / float64(totalMsgOutput)
			agg.inputTokens = int(float64(sessionInput) * ratio)
		}
	}

	// Distribute session-level cache tokens proportionally across models.
	if sess.TokenUsage.CacheRead > 0 || sess.TokenUsage.CacheWrite > 0 {
		// Explicit cache data available — use it.
		totalAggInput := 0
		for _, agg := range perModel {
			totalAggInput += agg.inputTokens
		}
		if totalAggInput > 0 {
			for _, agg := range perModel {
				ratio := float64(agg.inputTokens) / float64(totalAggInput)
				agg.cacheReadTokens = int(float64(sess.TokenUsage.CacheRead) * ratio)
				agg.cacheWriteTokens = int(float64(sess.TokenUsage.CacheWrite) * ratio)
			}
		}
	} else if len(sess.Messages) > 20 {
		// No explicit cache data, but session has many messages.
		const estimatedCacheReadRatio = 0.90
		const estimatedCacheWriteRatio = 0.03
		for _, agg := range perModel {
			agg.cacheReadTokens = int(float64(agg.inputTokens) * estimatedCacheReadRatio)
			agg.cacheWriteTokens = int(float64(agg.inputTokens) * estimatedCacheWriteRatio)
		}
	}

	// Build result — use tiered cost computation.
	estimate := &session.CostEstimate{}
	var totalInput, totalOutput float64

	models := make([]string, 0, len(perModel))
	for m := range perModel {
		models = append(models, m)
	}
	sort.Strings(models)

	for _, model := range models {
		agg := perModel[model]
		// Use tiered computation (falls back to flat if model has no tiers).
		cost := c.messageCostTiered(model, agg.inputTokens, agg.outputTokens, agg.cacheReadTokens, agg.cacheWriteTokens)

		estimate.PerModel = append(estimate.PerModel, session.ModelCost{
			Model:        model,
			InputTokens:  agg.inputTokens,
			OutputTokens: agg.outputTokens,
			Cost:         cost,
			MessageCount: agg.msgCount,
		})

		totalInput += cost.InputCost
		totalOutput += cost.OutputCost
	}

	apiCost := session.Cost{
		InputCost:  totalInput,
		OutputCost: totalOutput,
		TotalCost:  totalInput + totalOutput,
		Currency:   defaultCurrency,
	}

	// Keep TotalCost as the API estimate for backward compatibility.
	estimate.TotalCost = apiCost

	// Determine billing type.
	billingType := session.BillingSubscription
	switch {
	case hasAPIMsgs && hasSubscriptionMsgs:
		billingType = session.BillingMixed
	case hasAPIMsgs:
		billingType = session.BillingAPI
	}

	actualCost := session.Cost{
		TotalCost: actualTotal,
		Currency:  defaultCurrency,
	}

	estimate.Breakdown = session.CostBreakdown{
		APICost:     apiCost,
		ActualCost:  actualCost,
		BillingType: billingType,
		Savings: session.Cost{
			TotalCost: apiCost.TotalCost - actualTotal,
			Currency:  defaultCurrency,
		},
	}

	unknowns := make([]string, 0, len(unknownSet))
	for m := range unknownSet {
		unknowns = append(unknowns, m)
	}
	sort.Strings(unknowns)
	estimate.UnknownModels = unknowns

	return estimate
}

// ── session.AnalyticsPricingLookup adapter ──────────────────────────────────
//
// These three methods make *Calculator satisfy session.AnalyticsPricingLookup
// without the session package having to import pricing (which would create a
// cycle — pricing already depends on session for its domain types).
//
// ComputeAnalytics reads only three things from pricing: the context window
// size, the raw input rate (for compaction rebuild cost), and the session's
// total/actual cost. Everything else the Calculator can do (tiered pricing,
// cache-read arbitrage, etc.) stays confined to pricing.SessionCost, which
// ComputeAnalytics calls as an opaque float via TotalCost/ActualCost.

// LookupPrice implements session.AnalyticsPricingLookup. It projects a
// pricing.ModelPrice onto the narrow session.AnalyticsModelPrice view that
// ComputeAnalytics uses.
func (c *Calculator) LookupPrice(model string) (session.AnalyticsModelPrice, bool) {
	mp, ok := c.Lookup(model)
	if !ok {
		return session.AnalyticsModelPrice{}, false
	}
	return session.AnalyticsModelPrice{
		MaxInputTokens: mp.MaxInputTokens,
		InputPerMToken: mp.InputPerMToken,
	}, true
}

// TotalCost implements session.AnalyticsPricingLookup. It returns the
// API-equivalent total cost for the session (estimated from token rates).
func (c *Calculator) TotalCost(sess *session.Session) float64 {
	if sess == nil {
		return 0
	}
	est := c.SessionCost(sess)
	if est == nil {
		return 0
	}
	return est.TotalCost.TotalCost
}

// ActualCost implements session.AnalyticsPricingLookup. It returns the
// provider-reported cost from per-message billing data.
func (c *Calculator) ActualCost(sess *session.Session) float64 {
	if sess == nil {
		return 0
	}
	est := c.SessionCost(sess)
	if est == nil {
		return 0
	}
	return est.Breakdown.ActualCost.TotalCost
}

// ── Backward Compatibility ──────────────────────────────────────────────────

// DefaultPrices returns the built-in pricing catalog as a slice.
// Deprecated: prefer using the Catalog interface directly.
var DefaultPrices = defaultCatalog.List()
