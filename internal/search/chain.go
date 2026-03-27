package search

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// Chain tries engines in order and falls back to the next on failure.
// The first engine is the "primary", subsequent engines are fallbacks.
type Chain struct {
	engines []Engine
	logger  *log.Logger
}

// NewChain creates a chain of search engines.
// Engines are tried in order; the first successful result is returned.
func NewChain(logger *log.Logger, engines ...Engine) *Chain {
	if logger == nil {
		logger = log.Default()
	}
	return &Chain{engines: engines, logger: logger}
}

func (c *Chain) Search(ctx context.Context, query Query) (*Result, error) {
	var lastErr error
	for _, eng := range c.engines {
		result, err := eng.Search(ctx, query)
		if err == nil {
			return result, nil
		}
		c.logger.Printf("[search] engine %s failed: %v, trying next", eng.Name(), err)
		lastErr = err
	}
	return nil, fmt.Errorf("all search engines failed: %w", lastErr)
}

func (c *Chain) Index(ctx context.Context, doc Document) error {
	var errs []string
	for _, eng := range c.engines {
		if err := eng.Index(ctx, doc); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", eng.Name(), err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("index errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (c *Chain) Delete(ctx context.Context, id string) error {
	for _, eng := range c.engines {
		_ = eng.Delete(ctx, id) // best-effort across all engines
	}
	return nil
}

// Capabilities returns the union of all engines' capabilities.
func (c *Chain) Capabilities() Capabilities {
	var caps Capabilities
	for _, eng := range c.engines {
		ec := eng.Capabilities()
		caps.FullText = caps.FullText || ec.FullText
		caps.Semantic = caps.Semantic || ec.Semantic
		caps.Facets = caps.Facets || ec.Facets
		caps.Highlights = caps.Highlights || ec.Highlights
		caps.FuzzyMatch = caps.FuzzyMatch || ec.FuzzyMatch
		caps.Ranking = caps.Ranking || ec.Ranking
	}
	return caps
}

func (c *Chain) Name() string {
	names := make([]string, len(c.engines))
	for i, eng := range c.engines {
		names[i] = eng.Name()
	}
	return "chain[" + strings.Join(names, "→") + "]"
}

func (c *Chain) Close() error {
	for _, eng := range c.engines {
		_ = eng.Close()
	}
	return nil
}
