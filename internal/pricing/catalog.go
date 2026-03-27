// Package pricing computes monetary costs from token usage and model identifiers.
// It ships with a built-in YAML catalog of model prices (go:embed) and supports
// user-configured overrides. The Calculator uses prefix matching to handle dated
// model variants (e.g. "claude-sonnet-4-20250514" matches "claude-sonnet-4").
//
// Architecture:
//   - Catalog (port)        — interface for looking up model prices
//   - EmbeddedCatalog       — go:embed adapter that loads catalog.yaml at startup
//   - OverrideCatalog       — decorator that layers user overrides on top of a base catalog
//   - Calculator            — domain service that computes costs using a Catalog
package pricing

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ── Domain Types ────────────────────────────────────────────────────────────

// PricingTier defines a pricing tier that activates when cumulative input
// tokens exceed ThresholdTokens. The multipliers are applied to the base
// input/output rates (e.g. 2.0 means "double the base rate").
type PricingTier struct {
	ThresholdTokens  int     `json:"threshold_tokens" yaml:"threshold_tokens"`   // e.g. 200_000
	InputMultiplier  float64 `json:"input_multiplier" yaml:"input_multiplier"`   // e.g. 2.0
	OutputMultiplier float64 `json:"output_multiplier" yaml:"output_multiplier"` // e.g. 2.0
}

// ModelPrice defines the cost per million tokens for a model.
type ModelPrice struct {
	Model               string        `json:"model" yaml:"model"`
	InputPerMToken      float64       `json:"input_per_mtoken" yaml:"input_per_mtoken"`                       // $ per 1M input tokens (base rate)
	OutputPerMToken     float64       `json:"output_per_mtoken" yaml:"output_per_mtoken"`                     // $ per 1M output tokens (base rate)
	CacheReadPerMToken  float64       `json:"cache_read_per_mtoken" yaml:"cache_read_per_mtoken"`             // $ per 1M cache read tokens (0 = use InputPerMToken)
	CacheWritePerMToken float64       `json:"cache_write_per_mtoken" yaml:"cache_write_per_mtoken"`           // $ per 1M cache write tokens (0 = use InputPerMToken * 1.25)
	MaxInputTokens      int           `json:"max_input_tokens,omitempty" yaml:"max_input_tokens,omitempty"`   // context window size (0 = unknown)
	MaxOutputTokens     int           `json:"max_output_tokens,omitempty" yaml:"max_output_tokens,omitempty"` // max output size (0 = unknown)
	Tiers               []PricingTier `json:"tiers,omitempty" yaml:"tiers,omitempty"`                         // optional tiered pricing (sorted by threshold ascending)
}

// HasTiers returns true if the model has tiered pricing.
func (m ModelPrice) HasTiers() bool {
	return len(m.Tiers) > 0
}

// ── Catalog Port (Interface) ────────────────────────────────────────────────

// Catalog is the port for looking up model prices. Implementations include
// the embedded YAML catalog and user-override decorators.
type Catalog interface {
	// Lookup finds the price for a model identifier using prefix matching.
	// Returns the ModelPrice and true if found, or zero value and false.
	Lookup(model string) (ModelPrice, bool)

	// List returns all known model prices in the catalog.
	List() []ModelPrice
}

// ── YAML schema for go:embed ────────────────────────────────────────────────

//go:embed catalog.yaml
var catalogYAML []byte

type catalogFile struct {
	Models []ModelPrice `yaml:"models"`
}

// ── EmbeddedCatalog ─────────────────────────────────────────────────────────

// EmbeddedCatalog loads model prices from the embedded catalog.yaml file.
// It implements the Catalog interface.
type EmbeddedCatalog struct {
	entries []catalogEntry
}

type catalogEntry struct {
	prefix string
	price  ModelPrice
}

// NewEmbeddedCatalog parses the embedded catalog.yaml and returns a Catalog.
func NewEmbeddedCatalog() (*EmbeddedCatalog, error) {
	var file catalogFile
	if err := yaml.Unmarshal(catalogYAML, &file); err != nil {
		return nil, fmt.Errorf("parsing embedded catalog: %w", err)
	}
	return newEmbeddedCatalog(file.Models), nil
}

// mustEmbeddedCatalog is like NewEmbeddedCatalog but panics on error.
// Used during package init for the default catalog.
func mustEmbeddedCatalog() *EmbeddedCatalog {
	cat, err := NewEmbeddedCatalog()
	if err != nil {
		panic("pricing: " + err.Error())
	}
	return cat
}

func newEmbeddedCatalog(prices []ModelPrice) *EmbeddedCatalog {
	entries := make([]catalogEntry, len(prices))
	for i, p := range prices {
		// Sort tiers by threshold ascending.
		if len(p.Tiers) > 1 {
			sort.Slice(p.Tiers, func(a, b int) bool {
				return p.Tiers[a].ThresholdTokens < p.Tiers[b].ThresholdTokens
			})
		}
		entries[i] = catalogEntry{prefix: strings.ToLower(p.Model), price: p}
	}
	// Sort by prefix length descending so longer (more specific) prefixes match first.
	sort.Slice(entries, func(i, j int) bool {
		return len(entries[i].prefix) > len(entries[j].prefix)
	})
	return &EmbeddedCatalog{entries: entries}
}

// Lookup finds the price for a model identifier using prefix matching.
// Handles cloud provider prefixes (Bedrock, Vertex) and version suffixes.
func (c *EmbeddedCatalog) Lookup(model string) (ModelPrice, bool) {
	lower := strings.ToLower(model)

	// Direct prefix match first.
	for _, e := range c.entries {
		if strings.HasPrefix(lower, e.prefix) {
			return e.price, true
		}
	}

	// Normalize: strip cloud provider prefixes (Bedrock, Vertex).
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
		for _, e := range c.entries {
			if strings.HasPrefix(normalized, e.prefix) {
				return e.price, true
			}
		}
	}

	return ModelPrice{}, false
}

// List returns all model prices in the catalog.
func (c *EmbeddedCatalog) List() []ModelPrice {
	result := make([]ModelPrice, len(c.entries))
	for i, e := range c.entries {
		result[i] = e.price
	}
	return result
}

// ── OverrideCatalog ─────────────────────────────────────────────────────────

// OverrideCatalog wraps a base Catalog and layers user-configured price
// overrides on top. Overrides take precedence when model prefixes match.
type OverrideCatalog struct {
	base      Catalog
	overrides *EmbeddedCatalog // reuses the same prefix-match logic
	allPrices []ModelPrice     // merged list
}

// NewOverrideCatalog creates a Catalog that merges overrides on top of base.
func NewOverrideCatalog(base Catalog, overrides []ModelPrice) *OverrideCatalog {
	// Build merged list: overrides win.
	merged := make(map[string]ModelPrice)
	for _, p := range base.List() {
		merged[strings.ToLower(p.Model)] = p
	}
	for _, o := range overrides {
		merged[strings.ToLower(o.Model)] = o
	}
	all := make([]ModelPrice, 0, len(merged))
	for _, p := range merged {
		all = append(all, p)
	}

	return &OverrideCatalog{
		base:      base,
		overrides: newEmbeddedCatalog(overrides),
		allPrices: all,
	}
}

// Lookup checks overrides first, then falls back to the base catalog.
func (c *OverrideCatalog) Lookup(model string) (ModelPrice, bool) {
	if price, found := c.overrides.Lookup(model); found {
		return price, true
	}
	return c.base.Lookup(model)
}

// List returns the merged list of all model prices.
func (c *OverrideCatalog) List() []ModelPrice {
	return c.allPrices
}

// ── Default Catalog (package-level singleton) ───────────────────────────────

// defaultCatalog is the package-level embedded catalog, initialized at startup.
var defaultCatalog = mustEmbeddedCatalog()

// DefaultCatalog returns the built-in embedded catalog.
func DefaultCatalog() Catalog {
	return defaultCatalog
}
