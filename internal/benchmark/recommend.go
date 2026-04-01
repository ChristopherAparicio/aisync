package benchmark

import (
	"math"
	"sort"

	"github.com/ChristopherAparicio/aisync/internal/pricing"
)

// Recommender produces model swap recommendations by joining benchmark scores
// with pricing data. It is a stateless application service.
type Recommender struct {
	benchmarks Catalog
	prices     pricing.Catalog
}

// NewRecommender creates a Recommender with the given data sources.
func NewRecommender(benchmarks Catalog, prices pricing.Catalog) *Recommender {
	return &Recommender{benchmarks: benchmarks, prices: prices}
}

// Benchmarks returns the underlying benchmark catalog.
// The caller can type-assert to MultiCatalog for multi-source features.
func (r *Recommender) Benchmarks() Catalog {
	return r.benchmarks
}

// Recommend returns model alternatives for the given model, sorted by
// verdict quality (no-brainers first, then tradeoffs, then risky).
//
// monthlyInputTokens is the user's actual monthly input token usage for this
// model (from token_usage_buckets). If 0, MonthlySaved is not computed.
func (r *Recommender) Recommend(model string, monthlyInputTokens int) []ModelAlternative {
	currentBench, hasBench := r.benchmarks.Lookup(model)
	currentPrice, hasPrice := r.prices.Lookup(model)

	if !hasBench && !hasPrice {
		return nil // can't recommend without any data
	}

	var currentScore float64
	if hasBench {
		currentScore = currentBench.Score
	}
	var currentCost float64
	if hasPrice {
		currentCost = currentPrice.InputPerMToken
	}

	// Compare against all benchmark entries.
	allEntries := r.benchmarks.List()
	var alts []ModelAlternative

	for _, entry := range allEntries {
		if normalizeModel(entry.Model) == normalizeModel(model) {
			continue // skip self
		}
		// Check if alias matches current model too.
		isSelf := false
		for _, alias := range entry.Aliases {
			if normalizeModel(alias) == normalizeModel(model) {
				isSelf = true
				break
			}
		}
		if isSelf {
			continue
		}

		altPrice, altHasPrice := r.prices.Lookup(entry.Model)
		if !altHasPrice {
			continue // can't recommend without pricing
		}

		alt := ModelAlternative{
			CurrentModel: model,
			CurrentScore: currentScore,
			CurrentCost:  currentCost,
			AltModel:     entry.Model,
			AltScore:     entry.Score,
			AltCost:      altPrice.InputPerMToken,
		}

		alt.ScoreDelta = entry.Score - currentScore
		if alt.ScoreDelta < 0 {
			alt.QualityDrop = math.Abs(alt.ScoreDelta)
		}

		if currentCost > 0 {
			alt.CostSavings = (currentCost - altPrice.InputPerMToken) / currentCost * 100
		}

		// Estimate monthly savings from real usage.
		if monthlyInputTokens > 0 && currentCost > 0 {
			currentMonthly := float64(monthlyInputTokens) / 1_000_000 * currentCost
			altMonthly := float64(monthlyInputTokens) / 1_000_000 * altPrice.InputPerMToken
			alt.MonthlySaved = currentMonthly - altMonthly
		}

		// Quality-Adjusted Cost.
		alt.CurrentQAC = ComputeQAC(currentCost, currentScore)
		alt.AltQAC = ComputeQAC(altPrice.InputPerMToken, entry.Score)
		if alt.CurrentQAC > 0 {
			alt.QACSavings = (alt.CurrentQAC - alt.AltQAC) / alt.CurrentQAC * 100
		}

		alt.Verdict = classifyAlternative(alt)

		// Populate multi-benchmark score breakdowns if available.
		if mc, ok := r.benchmarks.(MultiCatalog); ok {
			alt.CurrentScores = mc.LookupScores(model)
			alt.AltScores = mc.LookupScores(entry.Model)
		}

		// Only include alternatives that save money.
		if alt.CostSavings > 0 {
			alts = append(alts, alt)
		}
	}

	// Sort: no-brainers first, then by cost savings descending.
	verdictOrder := map[string]int{"no-brainer": 0, "upgrade": 1, "tradeoff": 2, "risky": 3}
	sort.Slice(alts, func(i, j int) bool {
		oi, oj := verdictOrder[alts[i].Verdict], verdictOrder[alts[j].Verdict]
		if oi != oj {
			return oi < oj
		}
		return alts[i].CostSavings > alts[j].CostSavings
	})

	return alts
}

// classifyAlternative determines the verdict for a model swap.
//
// Verdicts:
//   - "no-brainer": better or equal quality AND cheaper
//   - "upgrade": better quality but more expensive (not a savings recommendation)
//   - "tradeoff": cheaper with acceptable quality drop (≤15%)
//   - "risky": cheaper but significant quality drop (>15%)
func classifyAlternative(alt ModelAlternative) string {
	switch {
	case alt.ScoreDelta >= 0 && alt.CostSavings > 0:
		return "no-brainer" // better quality AND cheaper
	case alt.ScoreDelta >= 0 && alt.CostSavings <= 0:
		return "upgrade" // better quality but more expensive
	case alt.QualityDrop <= 15 && alt.CostSavings > 0:
		return "tradeoff" // acceptable quality loss for savings
	default:
		return "risky" // significant quality drop
	}
}

// QACLeaderboard returns all models with both benchmark scores and pricing,
// ranked by quality-adjusted cost (lowest QAC = best value).
func (r *Recommender) QACLeaderboard() []QACLeaderEntry {
	allEntries := r.benchmarks.List()
	var entries []QACLeaderEntry

	for _, be := range allEntries {
		price, ok := r.prices.Lookup(be.Model)
		if !ok || price.InputPerMToken <= 0 {
			continue
		}

		qac := ComputeQAC(price.InputPerMToken, be.Score)
		if qac <= 0 {
			continue
		}

		entry := QACLeaderEntry{
			Model:          be.Model,
			BenchmarkScore: be.Score,
			InputCost:      price.InputPerMToken,
			QAC:            qac,
		}

		// Populate multi-benchmark score breakdowns if available.
		if mc, ok := r.benchmarks.(MultiCatalog); ok {
			entry.Scores = mc.LookupScores(be.Model)
			entry.SourceCount = len(entry.Scores)
		}

		entries = append(entries, entry)
	}

	// Sort by QAC ascending (best value first).
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].QAC < entries[j].QAC
	})

	// Assign ranks.
	for i := range entries {
		entries[i].Rank = i + 1
	}

	return entries
}
