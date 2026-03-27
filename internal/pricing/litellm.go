package pricing

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ── LiteLLM Constants ───────────────────────────────────────────────────────

const (
	// LiteLLMURL is the canonical URL for the LiteLLM pricing database.
	LiteLLMURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

	// liteLLMCacheFileName is the local cache file name.
	liteLLMCacheFileName = "litellm_prices.json"

	// liteLLMMaxCacheAge is the maximum age of the local cache before a refresh
	// is recommended.
	liteLLMMaxCacheAge = 7 * 24 * time.Hour // 7 days

	// liteLLMFetchTimeout is the HTTP timeout for fetching the remote file.
	liteLLMFetchTimeout = 30 * time.Second
)

// ── LiteLLM JSON Schema ─────────────────────────────────────────────────────

// liteLLMModel represents a single model entry in the LiteLLM JSON database.
// The JSON is a map[string]liteLLMModel keyed by model identifier.
type liteLLMModel struct {
	// Core pricing (per-token, NOT per-million).
	InputCostPerToken  *float64 `json:"input_cost_per_token"`
	OutputCostPerToken *float64 `json:"output_cost_per_token"`

	// Cache pricing (per-token).
	CacheReadInputTokenCost     *float64 `json:"cache_read_input_token_cost"`
	CacheCreationInputTokenCost *float64 `json:"cache_creation_input_token_cost"`

	// Tiered pricing — above 200k tokens (per-token).
	InputCostPerTokenAbove200k  *float64 `json:"input_cost_per_token_above_200k_tokens"`
	OutputCostPerTokenAbove200k *float64 `json:"output_cost_per_token_above_200k_tokens"`
	CacheReadAbove200k          *float64 `json:"cache_read_input_token_cost_above_200k_tokens"`
	CacheCreationAbove200k      *float64 `json:"cache_creation_input_token_cost_above_200k_tokens"`

	// Tiered pricing — above 128k tokens (per-token). Used by some Gemini models.
	InputCostPerTokenAbove128k  *float64 `json:"input_cost_per_token_above_128k_tokens"`
	OutputCostPerTokenAbove128k *float64 `json:"output_cost_per_token_above_128k_tokens"`

	// Metadata.
	LiteLLMProvider string `json:"litellm_provider"`
	Mode            string `json:"mode"`
	MaxInputTokens  int    `json:"max_input_tokens"`
	MaxOutputTokens int    `json:"max_output_tokens"`

	// We capture all fields via a raw map for future-proofing.
	// (Not used in parsing but useful for debugging.)
}

// ── LiteLLMCatalog ──────────────────────────────────────────────────────────

// LiteLLMCatalog is a Catalog adapter backed by the LiteLLM open-source
// pricing database. It fetches 2500+ models from GitHub and caches locally.
//
// The adapter converts LiteLLM's per-token pricing to our per-million-token
// format and extracts tiered pricing from "_above_Nk_tokens" fields.
//
// Usage:
//
//	cat, err := NewLiteLLMCatalog(LiteLLMCatalogConfig{CacheDir: "~/.aisync"})
//	calc := NewCalculatorWithCatalog(cat)
type LiteLLMCatalog struct {
	inner *EmbeddedCatalog // reuses prefix-match logic
}

// LiteLLMCatalogConfig configures the LiteLLM catalog adapter.
type LiteLLMCatalogConfig struct {
	// CacheDir is the directory for storing the local price cache.
	// Default: ~/.aisync
	CacheDir string

	// SourceURL overrides the LiteLLM GitHub URL (for testing).
	SourceURL string

	// HTTPClient overrides the default HTTP client (for testing).
	HTTPClient HTTPClient

	// ProviderFilter limits models to specific LiteLLM providers.
	// Empty means "include all". Common values: "anthropic", "openai", "gemini".
	ProviderFilter []string

	// MaxModels limits the total number of models to include.
	// 0 means no limit.
	MaxModels int
}

// HTTPClient is a minimal interface for HTTP fetching (testable).
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// NewLiteLLMCatalog creates a LiteLLM-backed catalog.
// It loads from local cache if available, or falls back to the embedded catalog.
// Call UpdateCache() to fetch fresh data from GitHub.
func NewLiteLLMCatalog(cfg LiteLLMCatalogConfig) (*LiteLLMCatalog, error) {
	cachePath := liteLLMCachePath(cfg.CacheDir)

	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, fmt.Errorf("litellm cache not found at %s: %w (run 'aisync update-prices' to fetch)", cachePath, err)
	}

	prices, err := parseLiteLLMJSON(data, cfg.ProviderFilter, cfg.MaxModels)
	if err != nil {
		return nil, fmt.Errorf("parsing litellm cache: %w", err)
	}

	return &LiteLLMCatalog{
		inner: newEmbeddedCatalog(prices),
	}, nil
}

// NewLiteLLMCatalogFromData creates a LiteLLMCatalog directly from raw JSON data.
// Useful for testing without filesystem access.
func NewLiteLLMCatalogFromData(data []byte, providerFilter []string, maxModels int) (*LiteLLMCatalog, error) {
	prices, err := parseLiteLLMJSON(data, providerFilter, maxModels)
	if err != nil {
		return nil, err
	}
	return &LiteLLMCatalog{
		inner: newEmbeddedCatalog(prices),
	}, nil
}

// Lookup finds the price for a model identifier using prefix matching.
func (c *LiteLLMCatalog) Lookup(model string) (ModelPrice, bool) {
	return c.inner.Lookup(model)
}

// List returns all known model prices.
func (c *LiteLLMCatalog) List() []ModelPrice {
	return c.inner.List()
}

// ── Cache Management ────────────────────────────────────────────────────────

// LiteLLMCacheInfo holds metadata about the local cache.
type LiteLLMCacheInfo struct {
	Path       string
	Exists     bool
	ModTime    time.Time
	Age        time.Duration
	Size       int64
	ModelCount int
	Stale      bool // true if age > liteLLMMaxCacheAge
}

// CacheInfo returns information about the local LiteLLM cache.
func CacheInfo(cacheDir string) LiteLLMCacheInfo {
	path := liteLLMCachePath(cacheDir)
	info := LiteLLMCacheInfo{Path: path}

	stat, err := os.Stat(path)
	if err != nil {
		return info
	}

	info.Exists = true
	info.ModTime = stat.ModTime()
	info.Size = stat.Size()
	info.Age = time.Since(stat.ModTime())
	info.Stale = info.Age > liteLLMMaxCacheAge

	// Try to count models.
	if data, readErr := os.ReadFile(path); readErr == nil {
		var raw map[string]json.RawMessage
		if json.Unmarshal(data, &raw) == nil {
			info.ModelCount = len(raw)
		}
	}

	return info
}

// UpdateCache fetches the LiteLLM pricing database from GitHub and saves
// it to the local cache directory. Returns the number of chat models found.
func UpdateCache(cfg LiteLLMCatalogConfig) (int, error) {
	sourceURL := cfg.SourceURL
	if sourceURL == "" {
		sourceURL = LiteLLMURL
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: liteLLMFetchTimeout}
	}

	req, err := http.NewRequest("GET", sourceURL, nil)
	if err != nil {
		return 0, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "aisync/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetching litellm prices: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("litellm returned HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("reading response: %w", err)
	}

	// Validate: must be valid JSON with at least some models.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return 0, fmt.Errorf("invalid JSON from litellm: %w", err)
	}
	if len(raw) < 10 {
		return 0, fmt.Errorf("suspiciously few models (%d) in litellm data", len(raw))
	}

	// Count chat models for reporting.
	chatCount := 0
	for _, v := range raw {
		var m struct {
			Mode string `json:"mode"`
		}
		if json.Unmarshal(v, &m) == nil && m.Mode == "chat" {
			chatCount++
		}
	}

	// Write to cache.
	cacheDir := cfg.CacheDir
	if cacheDir == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return 0, fmt.Errorf("determining home directory: %w", homeErr)
		}
		cacheDir = filepath.Join(home, ".aisync")
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return 0, fmt.Errorf("creating cache directory: %w", err)
	}

	cachePath := filepath.Join(cacheDir, liteLLMCacheFileName)
	if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		return 0, fmt.Errorf("writing cache: %w", err)
	}

	return chatCount, nil
}

// ── Parsing ─────────────────────────────────────────────────────────────────

// thresholdRegexp matches fields like "input_cost_per_token_above_200k_tokens"
// and extracts the threshold value (e.g. "200k" → 200000).
var thresholdRegexp = regexp.MustCompile(`_above_(\d+)([kKmM]?)_tokens$`)

// parseLiteLLMJSON converts the LiteLLM JSON database into our ModelPrice format.
// It filters to chat-mode models, converts per-token to per-million-token pricing,
// and extracts tiered pricing from "_above_Nk_tokens" fields.
func parseLiteLLMJSON(data []byte, providerFilter []string, maxModels int) ([]ModelPrice, error) {
	// Parse into raw map for flexible field access.
	var rawModels map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawModels); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}

	// Build provider filter set.
	filterSet := make(map[string]struct{}, len(providerFilter))
	for _, p := range providerFilter {
		filterSet[strings.ToLower(p)] = struct{}{}
	}

	var prices []ModelPrice

	for modelKey, rawValue := range rawModels {
		var m liteLLMModel
		if err := json.Unmarshal(rawValue, &m); err != nil {
			continue // skip malformed entries
		}

		// Filter: chat mode only.
		if m.Mode != "chat" {
			continue
		}

		// Filter: must have input and output pricing.
		if m.InputCostPerToken == nil || m.OutputCostPerToken == nil {
			continue
		}

		// Filter: provider if specified.
		if len(filterSet) > 0 {
			provider := strings.ToLower(m.LiteLLMProvider)
			if _, ok := filterSet[provider]; !ok {
				continue
			}
		}

		// Convert per-token → per-million-token.
		price := ModelPrice{
			Model:           modelKey,
			InputPerMToken:  *m.InputCostPerToken * 1_000_000,
			OutputPerMToken: *m.OutputCostPerToken * 1_000_000,
			MaxInputTokens:  m.MaxInputTokens,
			MaxOutputTokens: m.MaxOutputTokens,
		}

		// Cache pricing.
		if m.CacheReadInputTokenCost != nil {
			price.CacheReadPerMToken = *m.CacheReadInputTokenCost * 1_000_000
		}
		if m.CacheCreationInputTokenCost != nil {
			price.CacheWritePerMToken = *m.CacheCreationInputTokenCost * 1_000_000
		}

		// Extract tiered pricing from raw JSON fields.
		// We look for patterns like "*_above_Nk_tokens" to build tiers.
		price.Tiers = extractTiers(rawValue, price.InputPerMToken, price.OutputPerMToken)

		prices = append(prices, price)
	}

	// Sort by model name for deterministic output.
	sort.Slice(prices, func(i, j int) bool {
		return prices[i].Model < prices[j].Model
	})

	// Apply max models limit.
	if maxModels > 0 && len(prices) > maxModels {
		prices = prices[:maxModels]
	}

	return prices, nil
}

// extractTiers parses raw JSON to find tiered pricing fields and builds
// PricingTier entries. It handles multiple threshold values (128k, 200k, 256k, etc.).
func extractTiers(rawValue json.RawMessage, baseInputPerM, baseOutputPerM float64) []PricingTier {
	// Parse into generic map to find all cost fields.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rawValue, &fields); err != nil {
		return nil
	}

	// Collect thresholds and their associated cost fields.
	type tierData struct {
		thresholdTokens int
		inputPerToken   *float64
		outputPerToken  *float64
	}

	tierMap := make(map[int]*tierData)

	for fieldName, fieldValue := range fields {
		match := thresholdRegexp.FindStringSubmatch(fieldName)
		if match == nil {
			continue
		}

		threshold, err := parseThresholdValue(match[1], match[2])
		if err != nil {
			continue
		}

		td, ok := tierMap[threshold]
		if !ok {
			td = &tierData{thresholdTokens: threshold}
			tierMap[threshold] = td
		}

		var cost float64
		if err := json.Unmarshal(fieldValue, &cost); err != nil {
			continue
		}

		// Classify the field.
		if strings.HasPrefix(fieldName, "input_cost_per_token_above_") {
			td.inputPerToken = &cost
		} else if strings.HasPrefix(fieldName, "output_cost_per_token_above_") {
			td.outputPerToken = &cost
		}
		// Note: we could also extract cache tier rates here, but our PricingTier
		// model currently only supports input/output multipliers. The cache tier
		// rates are handled separately by the Calculator's tierMultiplier logic.
	}

	if len(tierMap) == 0 {
		return nil
	}

	// Convert to PricingTier entries with multipliers.
	var tiers []PricingTier
	for _, td := range tierMap {
		tier := PricingTier{
			ThresholdTokens:  td.thresholdTokens,
			InputMultiplier:  1.0,
			OutputMultiplier: 1.0,
		}

		// Compute multiplier: tier rate / base rate.
		if td.inputPerToken != nil && baseInputPerM > 0 {
			tierInputPerM := *td.inputPerToken * 1_000_000
			mult := tierInputPerM / baseInputPerM
			tier.InputMultiplier = roundMultiplier(mult)
		}
		if td.outputPerToken != nil && baseOutputPerM > 0 {
			tierOutputPerM := *td.outputPerToken * 1_000_000
			mult := tierOutputPerM / baseOutputPerM
			tier.OutputMultiplier = roundMultiplier(mult)
		}

		// Only include tiers where at least one multiplier differs from 1.0.
		if tier.InputMultiplier != 1.0 || tier.OutputMultiplier != 1.0 {
			tiers = append(tiers, tier)
		}
	}

	// Sort tiers by threshold ascending.
	sort.Slice(tiers, func(i, j int) bool {
		return tiers[i].ThresholdTokens < tiers[j].ThresholdTokens
	})

	return tiers
}

// parseThresholdValue converts "200" + "k" → 200000, "128" + "k" → 128000, etc.
func parseThresholdValue(numStr, suffix string) (int, error) {
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, err
	}

	switch strings.ToLower(suffix) {
	case "k":
		return num * 1000, nil
	case "m":
		return num * 1_000_000, nil
	case "":
		return num, nil
	default:
		return 0, fmt.Errorf("unknown suffix %q", suffix)
	}
}

// roundMultiplier rounds a multiplier to a reasonable precision.
// This avoids floating point artifacts like 1.9999999999 → 2.0.
func roundMultiplier(m float64) float64 {
	// Round to 4 decimal places.
	return math.Round(m*10000) / 10000
}

// ── FallbackCatalog ─────────────────────────────────────────────────────────

// FallbackCatalog chains multiple catalogs together. It tries each catalog
// in order for Lookup and returns the first match. List returns the union
// of all catalogs (first catalog's entries take precedence).
type FallbackCatalog struct {
	primary   Catalog
	fallbacks []Catalog
	allPrices []ModelPrice
}

// NewFallbackCatalog creates a catalog that tries primary first, then falls
// back to each subsequent catalog. This is used to layer LiteLLM data on top
// of the embedded catalog: primary = LiteLLM, fallback = Embedded.
func NewFallbackCatalog(primary Catalog, fallbacks ...Catalog) *FallbackCatalog {
	// Build merged list: primary wins.
	seen := make(map[string]struct{})
	var all []ModelPrice

	for _, p := range primary.List() {
		key := strings.ToLower(p.Model)
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			all = append(all, p)
		}
	}

	for _, fb := range fallbacks {
		for _, p := range fb.List() {
			key := strings.ToLower(p.Model)
			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				all = append(all, p)
			}
		}
	}

	return &FallbackCatalog{
		primary:   primary,
		fallbacks: fallbacks,
		allPrices: all,
	}
}

// Lookup tries the primary catalog first, then each fallback in order.
func (c *FallbackCatalog) Lookup(model string) (ModelPrice, bool) {
	if price, found := c.primary.Lookup(model); found {
		return price, true
	}
	for _, fb := range c.fallbacks {
		if price, found := fb.Lookup(model); found {
			return price, true
		}
	}
	return ModelPrice{}, false
}

// List returns the merged list of all model prices (primary takes precedence).
func (c *FallbackCatalog) List() []ModelPrice {
	return c.allPrices
}

// ── Helpers ─────────────────────────────────────────────────────────────────

// liteLLMCachePath returns the full path to the local cache file.
func liteLLMCachePath(cacheDir string) string {
	if cacheDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return liteLLMCacheFileName
		}
		cacheDir = filepath.Join(home, ".aisync")
	}
	return filepath.Join(cacheDir, liteLLMCacheFileName)
}
