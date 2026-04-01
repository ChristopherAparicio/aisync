package benchmark

import (
	"embed"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed data/*.yaml
var multiCatalogFS embed.FS

// multiCatalogFile extends catalogFile with a category field.
type multiCatalogFile struct {
	Source   string           `yaml:"source"`
	Date     string           `yaml:"date"`
	Category string           `yaml:"category"`
	Entries  []BenchmarkEntry `yaml:"entries"`
}

// MultiEmbeddedCatalog implements MultiCatalog using go:embed YAML files
// from multiple benchmark sources. It computes composite scores with
// configurable weights.
type MultiEmbeddedCatalog struct {
	// Per-source catalogs for individual lookups.
	sources map[BenchmarkSource]*EmbeddedCatalog

	// sourceCategories maps source → category string.
	sourceCategories map[BenchmarkSource]string

	// weights for composite score computation.
	weights CompositeWeights

	// allModels is a deduplicated set of all model names across sources.
	allModels map[string]bool

	// compositeEntries caches the composite entries sorted by composite score.
	compositeEntries []CompositeEntry
}

// MultiCatalogConfig configures the MultiEmbeddedCatalog.
type MultiCatalogConfig struct {
	// Weights for composite scoring. If nil, DefaultCompositeWeights() is used.
	Weights CompositeWeights

	// ExtraData maps source names to raw YAML data to include alongside
	// the embedded files. Useful for testing.
	ExtraData map[BenchmarkSource][]byte
}

// NewMultiEmbeddedCatalog creates a MultiCatalog from all embedded benchmark
// YAML files plus the original Aider catalog.
func NewMultiEmbeddedCatalog(cfg MultiCatalogConfig) (*MultiEmbeddedCatalog, error) {
	weights := cfg.Weights
	if weights == nil {
		weights = DefaultCompositeWeights()
	}

	mc := &MultiEmbeddedCatalog{
		sources:          make(map[BenchmarkSource]*EmbeddedCatalog),
		sourceCategories: make(map[BenchmarkSource]string),
		weights:          weights,
		allModels:        make(map[string]bool),
	}

	// 1. Load the original Aider polyglot catalog (from catalog.yaml in parent dir).
	aiderCat, err := NewEmbeddedCatalog()
	if err != nil {
		return nil, fmt.Errorf("benchmark: load aider catalog: %w", err)
	}
	mc.sources[SourceAiderPolyglot] = aiderCat
	mc.sourceCategories[SourceAiderPolyglot] = "code_editing"
	for _, e := range aiderCat.List() {
		mc.allModels[normalizeModel(e.Model)] = true
	}

	// 2. Load additional benchmark sources from data/*.yaml.
	entries, err := multiCatalogFS.ReadDir("data")
	if err != nil {
		return nil, fmt.Errorf("benchmark: read data dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		data, err := multiCatalogFS.ReadFile("data/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("benchmark: read %s: %w", entry.Name(), err)
		}
		if err := mc.loadSourceData(data); err != nil {
			return nil, fmt.Errorf("benchmark: parse %s: %w", entry.Name(), err)
		}
	}

	// 3. Load any extra test data.
	for _, data := range cfg.ExtraData {
		if err := mc.loadSourceData(data); err != nil {
			return nil, fmt.Errorf("benchmark: parse extra data: %w", err)
		}
	}

	// 4. Build composite entries cache.
	mc.buildCompositeCache()

	return mc, nil
}

// loadSourceData parses a YAML file and adds its entries to the catalog.
func (mc *MultiEmbeddedCatalog) loadSourceData(data []byte) error {
	var cf multiCatalogFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return err
	}

	source := BenchmarkSource(cf.Source)
	if source == "" {
		return fmt.Errorf("missing 'source' field in YAML")
	}

	// Populate source/date on entries.
	for i := range cf.Entries {
		if cf.Entries[i].Source == "" {
			cf.Entries[i].Source = source
		}
		if cf.Entries[i].Date == "" {
			cf.Entries[i].Date = cf.Date
		}
	}

	cat, err := parseCatalog(data)
	if err != nil {
		return err
	}

	mc.sources[source] = cat
	mc.sourceCategories[source] = cf.Category
	for _, e := range cat.List() {
		mc.allModels[normalizeModel(e.Model)] = true
	}

	return nil
}

// buildCompositeCache pre-computes composite entries for all known models.
func (mc *MultiEmbeddedCatalog) buildCompositeCache() {
	mc.compositeEntries = nil

	for model := range mc.allModels {
		scores := mc.lookupScoresNorm(model)
		if len(scores) == 0 {
			continue
		}

		composite, _ := mc.computeComposite(scores)
		mc.compositeEntries = append(mc.compositeEntries, CompositeEntry{
			Model:          mc.canonicalName(model),
			CompositeScore: composite,
			Scores:         scores,
			SourceCount:    len(scores),
		})
	}

	// Sort by composite score descending.
	sort.Slice(mc.compositeEntries, func(i, j int) bool {
		return mc.compositeEntries[i].CompositeScore > mc.compositeEntries[j].CompositeScore
	})
}

// canonicalName returns the display name for a normalized model key.
// Uses the Aider catalog name if available, otherwise the first source's name.
func (mc *MultiEmbeddedCatalog) canonicalName(normModel string) string {
	// Prefer Aider catalog name (most recognizable).
	if cat, ok := mc.sources[SourceAiderPolyglot]; ok {
		if e, found := cat.Lookup(normModel); found {
			return e.Model
		}
	}
	// Fallback: use first source that has the model.
	for _, cat := range mc.sources {
		if e, found := cat.Lookup(normModel); found {
			return e.Model
		}
	}
	return normModel
}

// ── Catalog interface (single-score, composite) ───────────────────

// Lookup returns a single BenchmarkEntry with the composite score.
// Satisfies the Catalog interface.
func (mc *MultiEmbeddedCatalog) Lookup(model string) (BenchmarkEntry, bool) {
	scores := mc.LookupScores(model)
	if len(scores) == 0 {
		return BenchmarkEntry{}, false
	}

	composite, _ := mc.computeComposite(scores)
	return BenchmarkEntry{
		Model:  mc.canonicalName(normalizeModel(model)),
		Source: "composite",
		Score:  composite,
		Date:   scores[0].Date, // use most recent date
	}, true
}

// List returns all models with composite scores, sorted descending.
// Satisfies the Catalog interface.
func (mc *MultiEmbeddedCatalog) List() []BenchmarkEntry {
	entries := make([]BenchmarkEntry, 0, len(mc.compositeEntries))
	for _, ce := range mc.compositeEntries {
		entries = append(entries, BenchmarkEntry{
			Model:  ce.Model,
			Source: "composite",
			Score:  ce.CompositeScore,
		})
	}
	return entries
}

// ── MultiCatalog interface ────────────────────────────────────────

// LookupScores returns all available benchmark scores for a model.
func (mc *MultiEmbeddedCatalog) LookupScores(model string) []BenchmarkScore {
	return mc.lookupScoresNorm(normalizeModel(model))
}

// lookupScoresNorm looks up scores using an already-normalized model name.
func (mc *MultiEmbeddedCatalog) lookupScoresNorm(normModel string) []BenchmarkScore {
	var scores []BenchmarkScore

	for source, cat := range mc.sources {
		entry, ok := cat.Lookup(normModel)
		if !ok {
			continue
		}

		category := mc.sourceCategories[source]
		scores = append(scores, BenchmarkScore{
			Source:    source,
			ModelName: entry.Model,
			Score:     entry.Score,
			Date:      entry.Date,
			Category:  category,
		})
	}

	// Sort by source name for deterministic output.
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].Source < scores[j].Source
	})

	return scores
}

// CompositeScore returns the weighted composite score for a model.
// Sources without data are excluded and weights are renormalized.
func (mc *MultiEmbeddedCatalog) CompositeScore(model string) (float64, bool) {
	scores := mc.LookupScores(model)
	if len(scores) == 0 {
		return 0, false
	}
	return mc.computeComposite(scores)
}

// computeComposite calculates the weighted average score from available sources.
// Weights are renormalized to account for missing sources.
func (mc *MultiEmbeddedCatalog) computeComposite(scores []BenchmarkScore) (float64, bool) {
	if len(scores) == 0 {
		return 0, false
	}

	var totalWeight float64
	var weightedSum float64

	for _, s := range scores {
		w, ok := mc.weights[s.Source]
		if !ok {
			// Source has no configured weight — use equal share of remaining.
			w = 1.0 / float64(len(scores))
		}
		totalWeight += w
		weightedSum += s.Score * w
	}

	if totalWeight <= 0 {
		return 0, false
	}

	return weightedSum / totalWeight, true
}

// Sources returns all benchmark sources available in this catalog.
func (mc *MultiEmbeddedCatalog) Sources() []BenchmarkSource {
	sources := make([]BenchmarkSource, 0, len(mc.sources))
	for s := range mc.sources {
		sources = append(sources, s)
	}
	// Sort for deterministic output.
	sort.Slice(sources, func(i, j int) bool {
		return sources[i] < sources[j]
	})
	return sources
}

// CompositeEntries returns all models with their composite scores and breakdowns.
func (mc *MultiEmbeddedCatalog) CompositeEntries() []CompositeEntry {
	result := make([]CompositeEntry, len(mc.compositeEntries))
	copy(result, mc.compositeEntries)
	return result
}

// Compile-time interface checks.
var (
	_ Catalog      = (*MultiEmbeddedCatalog)(nil)
	_ MultiCatalog = (*MultiEmbeddedCatalog)(nil)
)
