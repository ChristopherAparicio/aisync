package benchmark

import (
	"embed"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed catalog.yaml
var catalogYAML embed.FS

// catalogFile is the parsed YAML structure.
type catalogFile struct {
	Source  string           `yaml:"source"`
	Date    string           `yaml:"date"`
	Entries []BenchmarkEntry `yaml:"entries"`
}

// EmbeddedCatalog implements Catalog using the go:embed YAML file.
type EmbeddedCatalog struct {
	entries []BenchmarkEntry           // sorted by score descending
	byModel map[string]*BenchmarkEntry // exact model name → entry
	byAlias map[string]*BenchmarkEntry // alias → entry
}

// NewEmbeddedCatalog creates a Catalog from the embedded benchmark data.
func NewEmbeddedCatalog() (*EmbeddedCatalog, error) {
	data, err := catalogYAML.ReadFile("catalog.yaml")
	if err != nil {
		return nil, fmt.Errorf("benchmark: read embedded catalog: %w", err)
	}
	return parseCatalog(data)
}

// NewCatalogFromData creates a Catalog from raw YAML data (for testing).
func NewCatalogFromData(data []byte) (*EmbeddedCatalog, error) {
	return parseCatalog(data)
}

func parseCatalog(data []byte) (*EmbeddedCatalog, error) {
	var cf catalogFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("benchmark: parse catalog YAML: %w", err)
	}

	// Populate source and date on each entry if not set.
	for i := range cf.Entries {
		if cf.Entries[i].Source == "" {
			cf.Entries[i].Source = BenchmarkSource(cf.Source)
		}
		if cf.Entries[i].Date == "" {
			cf.Entries[i].Date = cf.Date
		}
	}

	// Sort by score descending.
	sort.Slice(cf.Entries, func(i, j int) bool {
		return cf.Entries[i].Score > cf.Entries[j].Score
	})

	c := &EmbeddedCatalog{
		entries: cf.Entries,
		byModel: make(map[string]*BenchmarkEntry, len(cf.Entries)),
		byAlias: make(map[string]*BenchmarkEntry, len(cf.Entries)*3),
	}

	for i := range c.entries {
		e := &c.entries[i]
		c.byModel[normalizeModel(e.Model)] = e
		for _, alias := range e.Aliases {
			c.byAlias[normalizeModel(alias)] = e
		}
	}

	return c, nil
}

// Lookup returns the benchmark entry for a model.
// Matching is fuzzy: tries exact match, then alias match, then prefix match.
func (c *EmbeddedCatalog) Lookup(model string) (BenchmarkEntry, bool) {
	norm := normalizeModel(model)

	// 1. Exact match on model name.
	if e, ok := c.byModel[norm]; ok {
		return *e, true
	}

	// 2. Exact match on alias.
	if e, ok := c.byAlias[norm]; ok {
		return *e, true
	}

	// 3. Prefix match: "claude-opus-4-20250514" starts with "claude-opus-4".
	for _, e := range c.entries {
		if strings.HasPrefix(norm, normalizeModel(e.Model)) {
			return e, true
		}
		for _, alias := range e.Aliases {
			if strings.HasPrefix(norm, normalizeModel(alias)) {
				return e, true
			}
		}
	}

	return BenchmarkEntry{}, false
}

// List returns all benchmark entries sorted by score descending.
func (c *EmbeddedCatalog) List() []BenchmarkEntry {
	result := make([]BenchmarkEntry, len(c.entries))
	copy(result, c.entries)
	return result
}

// normalizeModel lowercases and strips common provider prefixes for matching.
func normalizeModel(model string) string {
	model = strings.ToLower(model)

	// Strip common provider prefixes.
	prefixes := []string{
		"anthropic.", // bedrock: anthropic.claude-opus-4-6-v1
		"bedrock/",   // bedrock/anthropic.claude-opus-4-6-v1
		"openai/",    // openai/gpt-4o
		"google/",    // google/gemini-2.5-pro
		"vertex_ai/", // vertex_ai/gemini-2.5-pro
		"deepseek/",  // deepseek/deepseek-chat
		"moonshot/",  // moonshot/kimi-k2
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(model, prefix) {
			model = model[len(prefix):]
			break
		}
	}

	return model
}

// Compile-time interface check.
var _ Catalog = (*EmbeddedCatalog)(nil)
