// Package statscmd implements the `aisync stats` CLI command.
// It shows token totals, session counts per branch, and most-touched files.
package statscmd

import (
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

	// Stats
	result, err := svc.Stats(service.StatsRequest{
		ProjectPath: topLevel,
		Branch:      opts.BranchFlag,
		Provider:    providerName,
		All:         opts.All,
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
	fmt.Fprintln(out)

	// Print per-provider stats
	fmt.Fprintln(out, "=== By Provider ===")
	for prov, count := range result.PerProvider {
		fmt.Fprintf(out, "  %-14s %d sessions\n", prov, count)
	}
	fmt.Fprintln(out)

	// Print per-branch stats
	fmt.Fprintln(out, "=== By Branch ===")
	fmt.Fprintf(out, "  %-30s  %8s  %8s\n", "BRANCH", "SESSIONS", "TOKENS")
	for _, bs := range result.PerBranch {
		fmt.Fprintf(out, "  %-30s  %8d  %8s\n", truncate(bs.Branch, 30), bs.SessionCount, formatTokens(bs.TotalTokens))
	}
	fmt.Fprintln(out)

	// Print top files
	if len(result.TopFiles) > 0 {
		fmt.Fprintln(out, "=== Most Touched Files ===")
		for _, f := range result.TopFiles {
			fmt.Fprintf(out, "  %4d  %s\n", f.Count, f.Path)
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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}
