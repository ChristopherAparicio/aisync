// Package pricing computes monetary costs from token usage and model identifiers.
// It ships with built-in prices for popular models (Claude, GPT, Gemini) and
// supports user-configured overrides. The Calculator uses prefix matching to
// handle dated model variants (e.g., "claude-sonnet-4-20250514" matches "claude-sonnet-4").
package pricing

import (
	"sort"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

const defaultCurrency = "USD"

// ModelPrice defines the cost per million tokens for a model.
type ModelPrice struct {
	Model               string  `json:"model"`
	InputPerMToken      float64 `json:"input_per_mtoken"`       // $ per 1M input tokens
	OutputPerMToken     float64 `json:"output_per_mtoken"`      // $ per 1M output tokens
	CacheReadPerMToken  float64 `json:"cache_read_per_mtoken"`  // $ per 1M cache read tokens (0 = use InputPerMToken)
	CacheWritePerMToken float64 `json:"cache_write_per_mtoken"` // $ per 1M cache write tokens (0 = use InputPerMToken * 1.25)
}

// Calculator computes costs from token counts and model identifiers.
type Calculator struct {
	// prices maps model prefix → price. Sorted by key length descending
	// so longer (more specific) prefixes match first.
	prices []modelEntry
}

type modelEntry struct {
	prefix string
	price  ModelPrice
}

// DefaultPrices contains built-in pricing for popular AI models.
// Prices are in USD per 1M tokens, as of early 2026.
var DefaultPrices = []ModelPrice{
	// Claude models (Anthropic) — with prompt caching pricing
	{Model: "claude-opus-4", InputPerMToken: 15.0, OutputPerMToken: 75.0, CacheReadPerMToken: 1.50, CacheWritePerMToken: 18.75},
	{Model: "claude-sonnet-4", InputPerMToken: 3.0, OutputPerMToken: 15.0, CacheReadPerMToken: 0.30, CacheWritePerMToken: 3.75},
	{Model: "claude-haiku-4", InputPerMToken: 0.80, OutputPerMToken: 4.0, CacheReadPerMToken: 0.08, CacheWritePerMToken: 1.0},
	{Model: "claude-haiku-3.5", InputPerMToken: 0.80, OutputPerMToken: 4.0, CacheReadPerMToken: 0.08, CacheWritePerMToken: 1.0},

	// GPT models (OpenAI) — with cached input pricing
	{Model: "gpt-4o", InputPerMToken: 2.50, OutputPerMToken: 10.0, CacheReadPerMToken: 1.25},
	{Model: "gpt-4o-mini", InputPerMToken: 0.15, OutputPerMToken: 0.60, CacheReadPerMToken: 0.075},
	{Model: "gpt-4.1", InputPerMToken: 2.0, OutputPerMToken: 8.0, CacheReadPerMToken: 0.50},
	{Model: "gpt-4.1-mini", InputPerMToken: 0.40, OutputPerMToken: 1.60, CacheReadPerMToken: 0.10},
	{Model: "gpt-4.1-nano", InputPerMToken: 0.10, OutputPerMToken: 0.40, CacheReadPerMToken: 0.025},
	{Model: "o3", InputPerMToken: 2.0, OutputPerMToken: 8.0, CacheReadPerMToken: 0.50},
	{Model: "o3-mini", InputPerMToken: 1.10, OutputPerMToken: 4.40, CacheReadPerMToken: 0.275},
	{Model: "o4-mini", InputPerMToken: 1.10, OutputPerMToken: 4.40, CacheReadPerMToken: 0.275},

	// Gemini models (Google)
	{Model: "gemini-2.5-pro", InputPerMToken: 1.25, OutputPerMToken: 10.0},
	{Model: "gemini-2.5-flash", InputPerMToken: 0.15, OutputPerMToken: 0.60},
	{Model: "gemini-2.0-flash", InputPerMToken: 0.10, OutputPerMToken: 0.40},
}

// NewCalculator creates a Calculator with the built-in default prices.
func NewCalculator() *Calculator {
	return newCalculator(DefaultPrices)
}

// WithOverrides returns a new Calculator that adds (or replaces) prices from overrides.
// Overrides take precedence over defaults when model prefixes match.
func (c *Calculator) WithOverrides(overrides []ModelPrice) *Calculator {
	// Start with the current prices, then apply overrides.
	merged := make(map[string]ModelPrice, len(c.prices)+len(overrides))
	for _, e := range c.prices {
		merged[e.prefix] = e.price
	}
	for _, o := range overrides {
		merged[o.Model] = o
	}

	all := make([]ModelPrice, 0, len(merged))
	for _, p := range merged {
		all = append(all, p)
	}
	return newCalculator(all)
}

func newCalculator(prices []ModelPrice) *Calculator {
	entries := make([]modelEntry, len(prices))
	for i, p := range prices {
		entries[i] = modelEntry{prefix: strings.ToLower(p.Model), price: p}
	}

	// Sort by prefix length descending so longer (more specific) prefixes match first.
	sort.Slice(entries, func(i, j int) bool {
		return len(entries[i].prefix) > len(entries[j].prefix)
	})

	return &Calculator{prices: entries}
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

// Lookup finds the price for a model identifier using prefix matching.
// Handles provider-specific model name formats (Bedrock, Vertex, Ollama).
func (c *Calculator) Lookup(model string) (ModelPrice, bool) {
	lower := strings.ToLower(model)

	// Direct prefix match first.
	for _, e := range c.prices {
		if strings.HasPrefix(lower, e.prefix) {
			return e.price, true
		}
	}

	// Normalize: strip cloud provider prefixes (Bedrock, Vertex).
	// e.g. "global.anthropic.claude-opus-4-6-v1" → "claude-opus-4-6-v1"
	// e.g. "us.anthropic.claude-opus-4-6-v1" → "claude-opus-4-6-v1"
	// e.g. "anthropic.claude-sonnet-4-6" → "claude-sonnet-4-6"
	normalized := lower
	for _, prefix := range []string{
		"global.anthropic.", "us.anthropic.", "eu.anthropic.",
		"anthropic.", "google.", "meta.",
	} {
		if strings.HasPrefix(normalized, prefix) {
			normalized = strings.TrimPrefix(normalized, prefix)
			break
		}
	}

	// Strip version suffixes: "-v1", "-v1:0", "-v2:0"
	if idx := strings.LastIndex(normalized, "-v"); idx > 0 {
		normalized = normalized[:idx]
	}

	// Retry with normalized name.
	if normalized != lower {
		for _, e := range c.prices {
			if strings.HasPrefix(normalized, e.prefix) {
				return e.price, true
			}
		}
	}

	return ModelPrice{}, false
}

// MessageCost computes the cost for a single message given its model and token counts.
func (c *Calculator) MessageCost(model string, inputTokens, outputTokens int) session.Cost {
	return c.MessageCostWithCache(model, inputTokens, outputTokens, 0, 0)
}

// MessageCostWithCache computes cost with separate cache token pricing.
// Cache read tokens are typically 10x cheaper than regular input tokens.
// Cache write tokens are typically 1.25x more expensive than input tokens.
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

	// Price each component separately.
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

// SessionCost computes the full cost breakdown for a session.
//
// It produces two cost views:
//   - APICost:    what it would cost at public API per-token rates
//   - ActualCost: what was actually charged (from provider-reported cost fields)
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
		// For interactive sessions with prompt caching (Claude, GPT), most input tokens
		// are cache reads (~90%). Apply estimated cache ratio to avoid massive overcount.
		// This heuristic is conservative: 90% cache read, 3% cache write, 7% raw input.
		const estimatedCacheReadRatio = 0.90
		const estimatedCacheWriteRatio = 0.03
		for _, agg := range perModel {
			agg.cacheReadTokens = int(float64(agg.inputTokens) * estimatedCacheReadRatio)
			agg.cacheWriteTokens = int(float64(agg.inputTokens) * estimatedCacheWriteRatio)
		}
	}

	// Build result.
	estimate := &session.CostEstimate{}
	var totalInput, totalOutput float64

	models := make([]string, 0, len(perModel))
	for m := range perModel {
		models = append(models, m)
	}
	sort.Strings(models)

	for _, model := range models {
		agg := perModel[model]
		cost := c.MessageCostWithCache(model, agg.inputTokens, agg.outputTokens, agg.cacheReadTokens, agg.cacheWriteTokens)

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
