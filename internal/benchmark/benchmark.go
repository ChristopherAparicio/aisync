// Package benchmark provides coding benchmark data for AI model evaluation.
//
// It exposes a Catalog port interface for looking up benchmark scores by model,
// and a Recommender that joins benchmark data with pricing to produce model
// swap recommendations.
//
// # Architecture
//
// Domain types (this file):
//   - BenchmarkEntry — a model's score on a specific benchmark
//   - Catalog — port interface for benchmark data lookup
//   - ModelAlternative — a recommended model swap with cost/quality analysis
//
// Application logic:
//   - Recommender — joins Catalog + pricing.Catalog to compute alternatives
//
// Infrastructure:
//   - EmbeddedCatalog — go:embed YAML adapter with Aider polyglot data
package benchmark

// BenchmarkSource identifies which benchmark suite produced a score.
type BenchmarkSource string

const (
	// SourceAiderPolyglot is the Aider polyglot benchmark — 225 coding exercises
	// across 6 languages (Python, JavaScript, TypeScript, C#, Java, C++).
	// Measures the model's ability to correctly edit code.
	SourceAiderPolyglot BenchmarkSource = "aider_polyglot"
)

// BenchmarkEntry records a single model's score on a benchmark.
// This is a value object — immutable after creation.
type BenchmarkEntry struct {
	Model  string          `json:"model" yaml:"model"`   // model name (normalized for matching)
	Source BenchmarkSource `json:"source" yaml:"source"` // which benchmark
	Score  float64         `json:"score" yaml:"score"`   // 0-100 percentage (e.g. 72.0 = 72%)
	Date   string          `json:"date" yaml:"date"`     // when the benchmark was captured (YYYY-MM-DD)

	// Aliases are alternative model names that map to this entry.
	// Used for fuzzy matching — e.g. "claude-opus-4" matches "claude-opus-4-20250514".
	Aliases []string `json:"aliases,omitempty" yaml:"aliases,omitempty"`
}

// Catalog is the port interface for benchmark data lookup.
// Implementations can be embedded YAML, remote API, or database-backed.
type Catalog interface {
	// Lookup returns the benchmark entry for a model, if found.
	// Model matching is fuzzy — "claude-opus-4-20250514" matches "claude-opus-4".
	Lookup(model string) (BenchmarkEntry, bool)

	// List returns all benchmark entries, sorted by score descending.
	List() []BenchmarkEntry
}

// ModelAlternative recommends switching from one model to another.
// It captures both the quality delta (benchmark) and the cost delta (pricing).
type ModelAlternative struct {
	// Current model being used.
	CurrentModel string  `json:"current_model"`
	CurrentScore float64 `json:"current_score"` // 0-100 benchmark score
	CurrentCost  float64 `json:"current_cost"`  // $ per 1M input tokens

	// Recommended alternative.
	AltModel string  `json:"alt_model"`
	AltScore float64 `json:"alt_score"` // 0-100 benchmark score
	AltCost  float64 `json:"alt_cost"`  // $ per 1M input tokens

	// Deltas.
	ScoreDelta   float64 `json:"score_delta"`   // AltScore - CurrentScore (negative = quality drop)
	CostSavings  float64 `json:"cost_savings"`  // (CurrentCost - AltCost) / CurrentCost * 100 (percentage)
	QualityDrop  float64 `json:"quality_drop"`  // abs(ScoreDelta) when negative, 0 when positive
	MonthlySaved float64 `json:"monthly_saved"` // estimated $ saved per month (from real usage data)

	// Quality-Adjusted Cost (QAC).
	// QAC normalizes cost by quality: QAC = cost / (score/100).
	// A model scoring 50% effectively costs twice as much per successful task.
	CurrentQAC float64 `json:"current_qac"` // QAC for the current model
	AltQAC     float64 `json:"alt_qac"`     // QAC for the alternative
	QACSavings float64 `json:"qac_savings"` // (CurrentQAC - AltQAC) / CurrentQAC * 100

	// Classification.
	Verdict string `json:"verdict"` // "no-brainer", "tradeoff", "risky", "upgrade"
}

// QACLeaderEntry ranks a model by quality-adjusted cost.
// Used for the "best value" leaderboard across all known models.
type QACLeaderEntry struct {
	Model          string  `json:"model"`
	BenchmarkScore float64 `json:"benchmark_score"` // 0-100
	InputCost      float64 `json:"input_cost"`      // $ per 1M input tokens
	QAC            float64 `json:"qac"`             // quality-adjusted cost
	Rank           int     `json:"rank"`            // 1-based rank (lower QAC = better)
}

// ComputeQAC returns the quality-adjusted cost for a given price and score.
// Returns 0 if score is zero (undefined).
func ComputeQAC(costPerMToken, benchmarkScore float64) float64 {
	if benchmarkScore <= 0 {
		return 0
	}
	return costPerMToken / (benchmarkScore / 100)
}
