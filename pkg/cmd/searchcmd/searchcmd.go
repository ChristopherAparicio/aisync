// Package searchcmd implements the `aisync search` CLI command.
package searchcmd

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the search command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	Keyword  string
	Branch   string
	Provider string
	OwnerID  string
	Since    string
	Until    string
	Limit    int
	JSON     bool
	Quiet    bool
}

// NewCmdSearch creates the `aisync search` command.
func NewCmdSearch(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "search [keyword]",
		Short: "Search sessions by keyword, branch, user, or date",
		Long: `Search across all captured sessions using keyword matching, 
filters, or a combination of both.

Examples:
  aisync search "OAuth2"                     # keyword search in summaries
  aisync search --branch feat/auth           # all sessions on a branch
  aisync search --owner-id abc123            # sessions by a specific user
  aisync search "auth" --since 2026-01-01    # keyword + date filter
  aisync search --branch feat/auth --json    # machine-readable output`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.Keyword = args[0]
			}
			return runSearch(opts)
		},
	}

	cmd.Flags().StringVar(&opts.Branch, "branch", "", "Filter by git branch")
	cmd.Flags().StringVar(&opts.Provider, "provider", "", "Filter by provider (claude-code, opencode, cursor)")
	cmd.Flags().StringVar(&opts.OwnerID, "owner-id", "", "Filter by owner/user ID")
	cmd.Flags().StringVar(&opts.Since, "since", "", "Only sessions after this date (YYYY-MM-DD or RFC3339)")
	cmd.Flags().StringVar(&opts.Until, "until", "", "Only sessions before this date (YYYY-MM-DD or RFC3339)")
	cmd.Flags().IntVar(&opts.Limit, "limit", 0, "Max results (default: 50)")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output results as JSON")
	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Only print session IDs")

	return cmd
}

func runSearch(opts *Options) error {
	out := opts.IO.Out

	// Resolve project path (optional — search can be global)
	var projectPath string
	gitClient, err := opts.Factory.Git()
	if err == nil {
		projectPath, _ = gitClient.TopLevel()
	}

	// Get service
	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	// Build provider filter
	var providerName session.ProviderName
	if opts.Provider != "" {
		parsed, parseErr := session.ParseProviderName(opts.Provider)
		if parseErr != nil {
			return parseErr
		}
		providerName = parsed
	}

	result, err := svc.Search(service.SearchRequest{
		Keyword:     opts.Keyword,
		ProjectPath: projectPath,
		Branch:      opts.Branch,
		Provider:    providerName,
		OwnerID:     session.ID(opts.OwnerID),
		Since:       opts.Since,
		Until:       opts.Until,
		Limit:       opts.Limit,
	})
	if err != nil {
		return err
	}

	// JSON output
	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	// Quiet mode
	if opts.Quiet {
		for _, s := range result.Sessions {
			fmt.Fprintln(out, s.ID)
		}
		return nil
	}

	// No results
	if len(result.Sessions) == 0 {
		fmt.Fprintln(out, "No sessions found matching your search.")
		return nil
	}

	// Results header
	fmt.Fprintf(out, "Found %d session(s) (showing %d-%d):\n\n",
		result.TotalCount,
		result.Offset+1,
		min(result.Offset+len(result.Sessions), result.TotalCount))

	// Table
	fmt.Fprintf(out, "%-12s  %-12s  %-24s  %8s  %8s  %s\n",
		"ID", "PROVIDER", "BRANCH", "MESSAGES", "TOKENS", "CAPTURED")
	fmt.Fprintf(out, "%-12s  %-12s  %-24s  %8s  %8s  %s\n",
		"----", "--------", "------", "--------", "------", "--------")

	for _, s := range result.Sessions {
		id := truncate(string(s.ID), 12)
		prov := truncate(string(s.Provider), 12)
		br := truncate(s.Branch, 24)
		captured := timeAgo(s.CreatedAt)

		fmt.Fprintf(out, "%-12s  %-12s  %-24s  %8d  %8s  %s\n",
			id, prov, br, s.MessageCount, formatTokens(s.TotalTokens), captured)
	}

	// Show summary line if there are more results
	if result.TotalCount > result.Offset+len(result.Sessions) {
		remaining := result.TotalCount - result.Offset - len(result.Sessions)
		fmt.Fprintf(out, "\n... and %d more. Use --limit and --json for full results.\n", remaining)
	}

	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

func formatTokens(n int) string {
	if n == 0 {
		return "-"
	}
	if n >= 1000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%d", n)
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}
