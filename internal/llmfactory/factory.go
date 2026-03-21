// Package llmfactory creates analysis.Analyzer instances from resolved LLM profiles.
// This is the single place that knows how to create the right adapter based on
// provider name, model, URL, and API key — used by the analysis service, the
// tagging classifier, and any future LLM-consuming feature.
package llmfactory

import (
	"fmt"
	"os/exec"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	anthropicanalyzer "github.com/ChristopherAparicio/aisync/internal/analysis/anthropic"
	llmanalyzer "github.com/ChristopherAparicio/aisync/internal/analysis/llm"
	ollamaanalyzer "github.com/ChristopherAparicio/aisync/internal/analysis/ollama"
	ocanalyzer "github.com/ChristopherAparicio/aisync/internal/analysis/opencode"
	"github.com/ChristopherAparicio/aisync/internal/config"
	internalllm "github.com/ChristopherAparicio/aisync/internal/llm"
	claudellm "github.com/ChristopherAparicio/aisync/internal/llm/claude"
)

// NewAnalyzer creates an analysis.Analyzer from a resolved LLM profile.
// This replaces the switch/case that was previously in pkg/cmd/factory/default.go.
func NewAnalyzer(profile config.ResolvedProfile) (analysis.Analyzer, error) {
	switch profile.Provider {
	case "ollama":
		url := profile.URL
		if url == "" {
			url = ollamaanalyzer.DefaultBaseURL
		}
		return ollamaanalyzer.NewAnalyzer(ollamaanalyzer.Config{
			BaseURL: url,
			Model:   profile.Model,
		}), nil

	case "anthropic":
		return anthropicanalyzer.NewAnalyzer(anthropicanalyzer.Config{
			APIKey: profile.APIKey,
			Model:  profile.Model,
		})

	case "opencode":
		return ocanalyzer.NewAnalyzer(ocanalyzer.AnalyzerConfig{
			Model: profile.Model,
		}), nil

	case "llm", "":
		// Legacy: uses the claude CLI binary.
		var llmClient internalllm.Client
		if _, lookupErr := exec.LookPath("claude"); lookupErr == nil {
			llmClient = claudellm.New()
		}
		if llmClient == nil {
			return nil, fmt.Errorf("LLM adapter requires claude CLI (not found in PATH)")
		}
		return llmanalyzer.NewAnalyzer(llmanalyzer.AnalyzerConfig{
			Client: llmClient,
			Model:  profile.Model,
		}), nil

	default:
		return nil, fmt.Errorf("unknown LLM provider %q", profile.Provider)
	}
}

// NewAnalyzerFromConfig is a convenience function that resolves a profile
// by name from the config and creates an analyzer in one step.
func NewAnalyzerFromConfig(cfg *config.Config, profileName string) (analysis.Analyzer, error) {
	profile := cfg.ResolveProfile(profileName)
	return NewAnalyzer(profile)
}
