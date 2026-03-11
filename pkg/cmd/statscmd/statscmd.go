// Package statscmd implements the `aisync stats` CLI command.
// It shows token totals, session counts per branch, and most-touched files.
package statscmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the stats command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	BranchFlag   string
	ProviderFlag string
	All          bool
	ShowCost     bool
	ShowTools    bool
	Forecast     bool
	ForecastDays int
	Period       string
}

// NewCmdStats creates the `aisync stats` command.
func NewCmdStats(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show session statistics",
		Long:  "Displays token totals, session counts per branch, and most-touched files across captured sessions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStats(opts)
		},
	}

	cmd.Flags().StringVar(&opts.BranchFlag, "branch", "", "Filter by branch name")
	cmd.Flags().StringVar(&opts.ProviderFlag, "provider", "", "Filter by provider name")
	cmd.Flags().BoolVar(&opts.All, "all", false, "Show stats across all projects")
	cmd.Flags().BoolVar(&opts.ShowCost, "cost", false, "Show estimated cost per branch")
	cmd.Flags().BoolVar(&opts.ShowTools, "tools", false, "Show aggregated tool usage across sessions")
	cmd.Flags().BoolVar(&opts.Forecast, "forecast", false, "Show cost forecast and model recommendations")
	cmd.Flags().IntVar(&opts.ForecastDays, "forecast-days", 90, "Look-back window in days for forecast")
	cmd.Flags().StringVar(&opts.Period, "period", "weekly", "Bucketing period for forecast: daily or weekly")

	return cmd
}

func runStats(opts *Options) error {
	out := opts.IO.Out

	// Git info
	gitClient, err := opts.Factory.Git()
	if err != nil {
		return fmt.Errorf("not a git repository")
	}

	topLevel, err := gitClient.TopLevel()
	if err != nil {
		return fmt.Errorf("could not determine repository root: %w", err)
	}

	// Parse provider
	var providerName session.ProviderName
	if opts.ProviderFlag != "" {
		parsed, parseErr := session.ParseProviderName(opts.ProviderFlag)
		if parseErr != nil {
			return parseErr
		}
		providerName = parsed
	}

	// Get service
	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	// Forecast mode
	if opts.Forecast {
		return runForecast(opts, svc, topLevel)
	}

	// Stats
	result, err := svc.Stats(service.StatsRequest{
		ProjectPath:  topLevel,
		Branch:       opts.BranchFlag,
		Provider:     providerName,
		All:          opts.All,
		IncludeTools: opts.ShowTools,
	})
	if err != nil {
		return err
	}

	if result.TotalSessions == 0 {
		fmt.Fprintln(out, "No sessions found.")
		return nil
	}

	// Print overall stats
	fmt.Fprintln(out, "=== Overall Statistics ===")
	fmt.Fprintf(out, "  Sessions:  %d\n", result.TotalSessions)
	fmt.Fprintf(out, "  Messages:  %d\n", result.TotalMessages)
	fmt.Fprintf(out, "  Tokens:    %s\n", formatTokens(result.TotalTokens))
	if opts.ShowCost {
		fmt.Fprintf(out, "  Cost:      %s\n", formatCost(result.TotalCost))
	}
	fmt.Fprintln(out)

	// Print per-provider stats
	fmt.Fprintln(out, "=== By Provider ===")
	for prov, count := range result.PerProvider {
		fmt.Fprintf(out, "  %-14s %d sessions\n", prov, count)
	}
	fmt.Fprintln(out)

	// Print per-branch stats
	fmt.Fprintln(out, "=== By Branch ===")
	if opts.ShowCost {
		fmt.Fprintf(out, "  %-30s  %8s  %8s  %10s\n", "BRANCH", "SESSIONS", "TOKENS", "COST")
		for _, bs := range result.PerBranch {
			fmt.Fprintf(out, "  %-30s  %8d  %8s  %10s\n",
				truncate(bs.Branch, 30), bs.SessionCount,
				formatTokens(bs.TotalTokens), formatCost(bs.TotalCost))
		}
	} else {
		fmt.Fprintf(out, "  %-30s  %8s  %8s\n", "BRANCH", "SESSIONS", "TOKENS")
		for _, bs := range result.PerBranch {
			fmt.Fprintf(out, "  %-30s  %8d  %8s\n",
				truncate(bs.Branch, 30), bs.SessionCount, formatTokens(bs.TotalTokens))
		}
	}
	fmt.Fprintln(out)

	// Print top files
	if len(result.TopFiles) > 0 {
		fmt.Fprintln(out, "=== Most Touched Files ===")
		for _, f := range result.TopFiles {
			fmt.Fprintf(out, "  %4d  %s\n", f.Count, f.Path)
		}
		fmt.Fprintln(out)
	}

	// Print tool stats
	if result.ToolStats != nil {
		fmt.Fprintln(out, "=== Tool Usage (Aggregated) ===")
		if result.ToolStats.Warning != "" {
			fmt.Fprintf(out, "  Warning: %s\n\n", result.ToolStats.Warning)
		}
		if result.ToolStats.TotalCalls == 0 {
			fmt.Fprintln(out, "  No tool calls found across sessions.")
		} else {
			fmt.Fprintf(out, "  Total calls: %d\n\n", result.ToolStats.TotalCalls)
			fmt.Fprintf(out, "  %-20s  %6s  %8s  %8s  %8s  %6s  %7s\n",
				"TOOL", "CALLS", "IN TOK", "OUT TOK", "TOTAL", "ERR", "%")
			fmt.Fprintf(out, "  %-20s  %6s  %8s  %8s  %8s  %6s  %7s\n",
				"────────────────────", "──────", "────────", "────────", "────────", "──────", "───────")
			for _, t := range result.ToolStats.Tools {
				fmt.Fprintf(out, "  %-20s  %6d  %8s  %8s  %8s  %6d  %6.1f%%\n",
					truncate(t.Name, 20),
					t.Calls,
					formatTokens(t.InputTokens),
					formatTokens(t.OutputTokens),
					formatTokens(t.TotalTokens),
					t.ErrorCount,
					t.Percentage,
				)
			}
		}
	}

	return nil
}

func formatTokens(n int) string {
	if n == 0 {
		return "0"
	}
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func formatCost(cost float64) string {
	if cost == 0 {
		return "$0.00"
	}
	if cost < 0.01 {
		return fmt.Sprintf("$%.4f", cost)
	}
	return fmt.Sprintf("$%.2f", cost)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

func runForecast(opts *Options, svc *service.SessionService, projectPath string) error {
	out := opts.IO.Out

	result, err := svc.Forecast(context.Background(), service.ForecastRequest{
		ProjectPath: projectPath,
		Branch:      opts.BranchFlag,
		Period:      opts.Period,
		Days:        opts.ForecastDays,
	})
	if err != nil {
		return err
	}

	if result.SessionCount == 0 {
		fmt.Fprintln(out, "No sessions found in the look-back window.")
		return nil
	}

	// Header
	fmt.Fprintln(out, "=== Cost Forecast ===")
	fmt.Fprintf(out, "  Analyzed:     %d sessions over %d %s buckets\n",
		result.SessionCount, len(result.Buckets), result.Period)
	fmt.Fprintf(out, "  Total cost:   %s\n", formatCost(result.TotalCost))
	fmt.Fprintf(out, "  Avg/%s:  %s\n", result.Period, formatCost(result.AvgPerBucket))
	fmt.Fprintln(out)

	// Trend
	fmt.Fprintln(out, "=== Trend ===")
	fmt.Fprintf(out, "  Direction:    %s\n", result.TrendDir)
	fmt.Fprintf(out, "  Trend/day:    %s\n", formatCost(result.TrendPerDay))
	fmt.Fprintln(out)

	// Projections
	fmt.Fprintln(out, "=== Projections ===")
	fmt.Fprintf(out, "  Next 30 days: %s\n", formatCost(result.Projected30d))
	fmt.Fprintf(out, "  Next 90 days: %s\n", formatCost(result.Projected90d))
	fmt.Fprintln(out)

	// Model breakdown
	if len(result.ModelBreakdown) > 0 {
		fmt.Fprintln(out, "=== Model Breakdown ===")
		fmt.Fprintf(out, "  %-30s  %8s  %8s  %6s  %s\n",
			"MODEL", "COST", "TOKENS", "SHARE", "RECOMMENDATION")
		fmt.Fprintf(out, "  %-30s  %8s  %8s  %6s  %s\n",
			"─────", "────", "──────", "─────", "──────────────")

		for _, m := range result.ModelBreakdown {
			rec := m.Recommendation
			if rec == "" {
				rec = "—"
			}
			fmt.Fprintf(out, "  %-30s  %8s  %8s  %5.1f%%  %s\n",
				truncate(m.Model, 30),
				formatCost(m.Cost),
				formatTokens(m.Tokens),
				m.Share,
				rec,
			)
		}
		fmt.Fprintln(out)
	}

	// Historical buckets (compact view — last 10)
	buckets := result.Buckets
	if len(buckets) > 10 {
		buckets = buckets[len(buckets)-10:]
		fmt.Fprintf(out, "=== Recent Buckets (last 10 of %d) ===\n", len(result.Buckets))
	} else {
		fmt.Fprintln(out, "=== Historical Buckets ===")
	}
	fmt.Fprintf(out, "  %-12s  %-12s  %8s  %8s  %5s\n",
		"START", "END", "COST", "TOKENS", "SESS")
	for _, b := range buckets {
		fmt.Fprintf(out, "  %-12s  %-12s  %8s  %8s  %5d\n",
			b.Start.Format("2006-01-02"),
			b.End.Format("2006-01-02"),
			formatCost(b.Cost),
			formatTokens(b.Tokens),
			b.SessionCount,
		)
	}

	return nil
}
