// Package pricescmd implements the `aisync update-prices` CLI command.
// It fetches the LiteLLM open-source pricing database from GitHub and caches
// it locally for accurate cost computation across 2500+ models.
package pricescmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/pricing"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the update-prices command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	ShowInfo bool // just show cache status, don't fetch
}

// NewCmdUpdatePrices creates the `aisync update-prices` command.
func NewCmdUpdatePrices(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "update-prices",
		Short: "Fetch latest model pricing from LiteLLM database",
		Long: `Fetch the LiteLLM open-source pricing database from GitHub and cache locally.

This enables accurate cost computation for 2500+ models across all major providers
(Anthropic, OpenAI, Google, Bedrock, Azure, Vertex, Mistral, and more).

The cache is stored at ~/.aisync/litellm_prices.json and is refreshed when you run
this command. A stale cache (>7 days) will log a warning but still work.

Pricing data source: https://github.com/BerriAI/litellm`,
		Aliases: []string{"prices"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdatePrices(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.ShowInfo, "info", false, "Show cache status without fetching")

	return cmd
}

func runUpdatePrices(opts *Options) error {
	out := opts.IO.Out
	cacheDir := defaultCacheDir()

	// --info: just show cache status.
	if opts.ShowInfo {
		return showCacheInfo(out, cacheDir)
	}

	// Fetch and update.
	fmt.Fprintln(out, "Fetching LiteLLM pricing database...")

	start := time.Now()
	chatCount, err := pricing.UpdateCache(pricing.LiteLLMCatalogConfig{
		CacheDir: cacheDir,
	})
	elapsed := time.Since(start)

	if err != nil {
		return fmt.Errorf("failed to update prices: %w", err)
	}

	fmt.Fprintf(out, "Updated pricing database in %s\n", elapsed.Round(time.Millisecond))
	fmt.Fprintf(out, "  %d chat models available for cost computation\n", chatCount)

	// Show cache info after update.
	info := pricing.CacheInfo(cacheDir)
	fmt.Fprintf(out, "  Cache: %s (%s)\n", info.Path, formatBytes(info.Size))

	// Quick test: load the catalog and show a few well-known models.
	cat, loadErr := pricing.NewLiteLLMCatalog(pricing.LiteLLMCatalogConfig{CacheDir: cacheDir})
	if loadErr != nil {
		fmt.Fprintf(opts.IO.ErrOut, "Warning: could not load cached data: %v\n", loadErr)
		return nil
	}

	spotCheck := []string{"claude-opus-4-1", "claude-sonnet-4", "gpt-4o", "gemini-2.5-pro"}
	fmt.Fprintln(out, "\nSpot check:")
	for _, model := range spotCheck {
		price, found := cat.Lookup(model)
		if !found {
			fmt.Fprintf(out, "  %-30s (not found)\n", model)
			continue
		}
		tierInfo := ""
		if price.HasTiers() {
			tierInfo = fmt.Sprintf(" (tiered: %d levels)", len(price.Tiers))
		}
		fmt.Fprintf(out, "  %-30s $%.2f / $%.2f per M tokens%s\n",
			model, price.InputPerMToken, price.OutputPerMToken, tierInfo)
	}

	return nil
}

func showCacheInfo(out io.Writer, cacheDir string) error {
	info := pricing.CacheInfo(cacheDir)

	if !info.Exists {
		fmt.Fprintln(out, "No LiteLLM price cache found.")
		fmt.Fprintln(out, "  Run 'aisync update-prices' to fetch pricing data.")
		return nil
	}

	fmt.Fprintln(out, "LiteLLM Price Cache")
	fmt.Fprintf(out, "  Path:    %s\n", info.Path)
	fmt.Fprintf(out, "  Size:    %s\n", formatBytes(info.Size))
	fmt.Fprintf(out, "  Models:  %d (total entries)\n", info.ModelCount)
	fmt.Fprintf(out, "  Updated: %s (%s ago)\n", info.ModTime.Format("2006-01-02 15:04"), formatDuration(info.Age))

	if info.Stale {
		fmt.Fprintln(out, "  Status:  stale (recommend: aisync update-prices)")
	} else {
		fmt.Fprintln(out, "  Status:  fresh")
	}

	return nil
}

func defaultCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".aisync")
}

func formatBytes(b int64) string {
	switch {
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/1024/1024)
	case b >= 1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func formatDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	case d >= time.Hour:
		return fmt.Sprintf("%.1f hours", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%.0f minutes", d.Minutes())
	default:
		return "just now"
	}
}
