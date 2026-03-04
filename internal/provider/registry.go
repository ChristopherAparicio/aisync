// Package provider manages provider registration and auto-detection.
package provider

import (
	"fmt"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// Registry manages available providers and handles auto-detection.
type Registry struct {
	providers map[session.ProviderName]Provider
}

// NewRegistry creates a Registry with the given providers.
func NewRegistry(providers ...Provider) *Registry {
	r := &Registry{
		providers: make(map[session.ProviderName]Provider, len(providers)),
	}
	for _, p := range providers {
		r.providers[p.Name()] = p
	}
	return r
}

// Get returns a specific provider by name.
func (r *Registry) Get(name session.ProviderName) (Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider %q not registered: %w", name, session.ErrProviderNotDetected)
	}
	return p, nil
}

// DetectAll runs detection on all registered providers for a given project and branch.
// Returns summaries from all providers that found sessions, most recent first.
func (r *Registry) DetectAll(projectPath string, branch string) ([]session.Summary, error) {
	var all []session.Summary

	for _, p := range r.providers {
		summaries, err := p.Detect(projectPath, branch)
		if err != nil {
			// Provider detection failure is not fatal — skip it
			continue
		}
		all = append(all, summaries...)
	}

	return all, nil
}

// DetectBest runs detection on all providers and returns the best match.
// "Best" is the most recent session across all providers.
// Returns ErrProviderNotDetected if no sessions are found.
func (r *Registry) DetectBest(projectPath string, branch string) (*session.Summary, Provider, error) {
	var (
		bestSummary  *session.Summary
		bestProvider Provider
	)

	for _, p := range r.providers {
		summaries, err := p.Detect(projectPath, branch)
		if err != nil || len(summaries) == 0 {
			continue
		}
		// Summaries are already sorted most-recent-first by each provider
		newest := summaries[0]
		if bestSummary == nil || newest.CreatedAt.After(bestSummary.CreatedAt) {
			bestSummary = &newest
			bestProvider = p
		}
	}

	if bestSummary == nil {
		return nil, nil, session.ErrProviderNotDetected
	}

	return bestSummary, bestProvider, nil
}

// Names returns the names of all registered providers.
func (r *Registry) Names() []session.ProviderName {
	names := make([]session.ProviderName, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}
