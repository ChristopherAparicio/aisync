// Package listcmd implements the `aisync list` CLI command.
package listcmd

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the list command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	All    bool
	Quiet  bool
	PRFlag int
}

// NewCmdList creates the `aisync list` command.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List captured sessions",
		Long:  "Lists captured sessions for the current branch or all sessions in this project.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.All, "all", false, "Show all sessions in this project")
	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Only print session IDs (one per line)")
	cmd.Flags().IntVar(&opts.PRFlag, "pr", 0, "List sessions linked to this PR number")

	return cmd
}

func runList(opts *Options) error {
	out := opts.IO.Out

	// Git info
	gitClient, err := opts.Factory.Git()
	if err != nil {
		return fmt.Errorf("not a git repository")
	}

	branch, err := gitClient.CurrentBranch()
	if err != nil {
		return fmt.Errorf("could not determine current branch: %w", err)
	}

	topLevel, err := gitClient.TopLevel()
	if err != nil {
		return fmt.Errorf("could not determine repository root: %w", err)
	}

	// Get store
	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	// List sessions — either by PR link or by branch/project
	var summaries []domain.SessionSummary
	if opts.PRFlag > 0 {
		prSummaries, lookupErr := store.GetByLink(domain.LinkPR, strconv.Itoa(opts.PRFlag))
		if lookupErr != nil {
			return fmt.Errorf("no sessions linked to PR #%d: %w", opts.PRFlag, lookupErr)
		}
		summaries = prSummaries
	} else {
		listOpts := domain.ListOptions{
			ProjectPath: topLevel,
			All:         opts.All,
		}
		if !opts.All {
			listOpts.Branch = branch
		}

		listed, listErr := store.List(listOpts)
		if listErr != nil {
			return fmt.Errorf("listing sessions: %w", listErr)
		}
		summaries = listed
	}

	if len(summaries) == 0 {
		if opts.Quiet {
			return nil
		}
		if opts.All {
			fmt.Fprintln(out, "No sessions found in this project.")
		} else {
			fmt.Fprintf(out, "No sessions found for branch %q.\n", branch)
			fmt.Fprintln(out, "Use --all to see all sessions in this project.")
		}
		return nil
	}

	// Quiet mode: print only session IDs, one per line
	if opts.Quiet {
		for _, s := range summaries {
			fmt.Fprintln(out, s.ID)
		}
		return nil
	}

	// Print header
	fmt.Fprintf(out, "%-12s  %-12s  %-24s  %8s  %8s  %s\n",
		"ID", "PROVIDER", "BRANCH", "MESSAGES", "TOKENS", "CAPTURED")
	fmt.Fprintf(out, "%-12s  %-12s  %-24s  %8s  %8s  %s\n",
		"----", "--------", "------", "--------", "------", "--------")

	for _, s := range summaries {
		id := truncate(string(s.ID), 12)
		prov := truncate(string(s.Provider), 12)
		br := truncate(s.Branch, 24)
		captured := timeAgo(s.CreatedAt)

		fmt.Fprintf(out, "%-12s  %-12s  %-24s  %8d  %8s  %s\n",
			id, prov, br, s.MessageCount, formatTokens(s.TotalTokens), captured)
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
