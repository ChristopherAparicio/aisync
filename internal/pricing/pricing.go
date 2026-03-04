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
	Model           string  `json:"model"`
	InputPerMToken  float64 `json:"input_per_mtoken"`  // $ per 1M input tokens
	OutputPerMToken float64 `json:"output_per_mtoken"` // $ per 1M output tokens
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
	// Claude models (Anthropic)
	{Model: "claude-opus-4", InputPerMToken: 15.0, OutputPerMToken: 75.0},
	{Model: "claude-sonnet-4", InputPerMToken: 3.0, OutputPerMToken: 15.0},
	{Model: "claude-haiku-3.5", InputPerMToken: 0.80, OutputPerMToken: 4.0},

	// GPT models (OpenAI)
	{Model: "gpt-4o", InputPerMToken: 2.50, OutputPerMToken: 10.0},
	{Model: "gpt-4o-mini", InputPerMToken: 0.15, OutputPerMToken: 0.60},
	{Model: "gpt-4.1", InputPerMToken: 2.0, OutputPerMToken: 8.0},
	{Model: "gpt-4.1-mini", InputPerMToken: 0.40, OutputPerMToken: 1.60},
	{Model: "gpt-4.1-nano", InputPerMToken: 0.10, OutputPerMToken: 0.40},
	{Model: "o3", InputPerMToken: 2.0, OutputPerMToken: 8.0},
	{Model: "o3-mini", InputPerMToken: 1.10, OutputPerMToken: 4.40},
	{Model: "o4-mini", InputPerMToken: 1.10, OutputPerMToken: 4.40},

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

// Lookup finds the price for a model identifier using prefix matching.
// Returns the price and true if found, or zero ModelPrice and false if not.
func (c *Calculator) Lookup(model string) (ModelPrice, bool) {
	lower := strings.ToLower(model)
	for _, e := range c.prices {
		if strings.HasPrefix(lower, e.prefix) {
			return e.price, true
		}
	}
	return ModelPrice{}, false
}

// MessageCost computes the cost for a single message given its model and token counts.
func (c *Calculator) MessageCost(model string, inputTokens, outputTokens int) session.Cost {
	price, found := c.Lookup(model)
	if !found {
		return session.Cost{Currency: defaultCurrency}
	}

	inputCost := float64(inputTokens) * price.InputPerMToken / 1_000_000
	outputCost := float64(outputTokens) * price.OutputPerMToken / 1_000_000

	return session.Cost{
		InputCost:  inputCost,
		OutputCost: outputCost,
		TotalCost:  inputCost + outputCost,
		Currency:   defaultCurrency,
	}
}

// SessionCost computes the full cost breakdown for a session.
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
		inputTokens  int
		outputTokens int
		msgCount     int
	}

	perModel := make(map[string]*modelAgg)
	unknownSet := make(map[string]struct{})

	// First pass: sum per-model input and output tokens from messages.
	for i := range sess.Messages {
		msg := &sess.Messages[i]
		if msg.Role != session.RoleAssistant || msg.Model == "" {
			continue
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
		cost := c.MessageCost(model, agg.inputTokens, agg.outputTokens)

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

	estimate.TotalCost = session.Cost{
		InputCost:  totalInput,
		OutputCost: totalOutput,
		TotalCost:  totalInput + totalOutput,
		Currency:   defaultCurrency,
	}

	unknowns := make([]string, 0, len(unknownSet))
	for m := range unknownSet {
		unknowns = append(unknowns, m)
	}
	sort.Strings(unknowns)
	estimate.UnknownModels = unknowns

	return estimate
}
