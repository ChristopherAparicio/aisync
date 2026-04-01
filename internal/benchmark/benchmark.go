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

	// SourceSWEBench is the SWE-bench benchmark — real GitHub issue resolution.
	// Measures end-to-end problem solving on real-world software engineering tasks.
	SourceSWEBench BenchmarkSource = "swe_bench"

	// SourceHumanEval is the HumanEval/MBPP benchmark — code generation from docstrings.
	// Measures the model's ability to generate correct code from specifications.
	SourceHumanEval BenchmarkSource = "humaneval"

	// SourceToolBench is the ToolBench/API-Bank benchmark — tool/API usage accuracy.
	// Measures the model's ability to correctly use tools and APIs (closest to MCP use case).
	SourceToolBench BenchmarkSource = "toolbench"

	// SourceArenaELO is the Chatbot Arena ELO benchmark — crowdsourced human preference.
	// Measures overall quality as perceived by human raters.
	// Scores are ELO ratings (typically 900-1400), normalized to 0-100 for composite scoring.
	SourceArenaELO BenchmarkSource = "arena_elo"
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

// BenchmarkScore records a single score from a specific benchmark source.
// Unlike BenchmarkEntry, it is source-specific and does not carry aliases.
// Used by MultiCatalog for multi-source aggregation.
type BenchmarkScore struct {
	Source    BenchmarkSource `json:"source"`     // which benchmark
	ModelName string          `json:"model_name"` // model name as listed in that benchmark
	Score     float64         `json:"score"`      // 0-100 percentage (normalized for Arena ELO)
	Date      string          `json:"date"`       // capture date YYYY-MM-DD
	Category  string          `json:"category"`   // e.g. "code_editing", "problem_solving", "tool_use", "preference"
}

// CompositeWeights defines the weight of each benchmark source for composite scoring.
// Weights should sum to 1.0.
type CompositeWeights map[BenchmarkSource]float64

// DefaultCompositeWeights returns the default weights per NEXT.md spec:
// Aider 40%, SWE-bench 30%, ToolBench 20%, Arena ELO 10%.
func DefaultCompositeWeights() CompositeWeights {
	return CompositeWeights{
		SourceAiderPolyglot: 0.40,
		SourceSWEBench:      0.30,
		SourceToolBench:     0.20,
		SourceArenaELO:      0.10,
	}
}

// MultiCatalog extends Catalog with multi-source benchmark support.
// Implementations aggregate scores from multiple benchmark suites and
// compute composite scores with configurable weights.
type MultiCatalog interface {
	Catalog // embeds single-score Catalog (returns composite as the single score)

	// LookupScores returns all available benchmark scores for a model.
	// Returns nil if the model is not found in any benchmark.
	LookupScores(model string) []BenchmarkScore

	// CompositeScore returns the weighted composite score for a model.
	// Only sources with data contribute; weights are renormalized.
	// Returns 0, false if the model has no benchmark data.
	CompositeScore(model string) (float64, bool)

	// Sources returns all benchmark sources available in this catalog.
	Sources() []BenchmarkSource
}

// CompositeEntry extends QACLeaderEntry with per-source score breakdown.
type CompositeEntry struct {
	Model          string           `json:"model"`
	CompositeScore float64          `json:"composite_score"` // weighted 0-100
	Scores         []BenchmarkScore `json:"scores"`          // per-source breakdown
	SourceCount    int              `json:"source_count"`    // how many sources have data for this model
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

	// Multi-benchmark breakdown (populated when MultiCatalog is used).
	CurrentScores []BenchmarkScore `json:"current_scores,omitempty"` // per-source scores for current model
	AltScores     []BenchmarkScore `json:"alt_scores,omitempty"`     // per-source scores for alternative
}

// QACLeaderEntry ranks a model by quality-adjusted cost.
// Used for the "best value" leaderboard across all known models.
type QACLeaderEntry struct {
	Model          string  `json:"model"`
	BenchmarkScore float64 `json:"benchmark_score"` // 0-100 (composite when MultiCatalog is used)
	InputCost      float64 `json:"input_cost"`      // $ per 1M input tokens
	QAC            float64 `json:"qac"`             // quality-adjusted cost
	Rank           int     `json:"rank"`            // 1-based rank (lower QAC = better)

	// Multi-benchmark breakdown (populated when MultiCatalog is used).
	Scores      []BenchmarkScore `json:"scores,omitempty"` // per-source breakdown
	SourceCount int              `json:"source_count"`     // how many sources have data
}

// ComputeQAC returns the quality-adjusted cost for a given price and score.
// Returns 0 if score is zero (undefined).
func ComputeQAC(costPerMToken, benchmarkScore float64) float64 {
	if benchmarkScore <= 0 {
		return 0
	}
	return costPerMToken / (benchmarkScore / 100)
}
