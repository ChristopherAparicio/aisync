// Package statscmd implements the `aisync stats` CLI command.
// It shows token totals, session counts per branch, and most-touched files.
package statscmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/domain"
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

// branchStats holds aggregated stats per branch.
type branchStats struct {
	branch       string
	totalTokens  int
	sessionCount int
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

	// Get store
	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	// List all sessions for this project
	listOpts := domain.ListOptions{
		ProjectPath: topLevel,
		All:         true,
	}

	// Filter by branch if specified
	if opts.BranchFlag != "" {
		listOpts.Branch = opts.BranchFlag
		listOpts.All = false
	}

	// Filter by provider if specified
	if opts.ProviderFlag != "" {
		parsed, parseErr := domain.ParseProviderName(opts.ProviderFlag)
		if parseErr != nil {
			return parseErr
		}
		listOpts.Provider = parsed
	}

	summaries, err := store.List(listOpts)
	if err != nil {
		return fmt.Errorf("listing sessions: %w", err)
	}

	if len(summaries) == 0 {
		fmt.Fprintln(out, "No sessions found.")
		return nil
	}

	// Aggregate stats
	var totalTokens, totalMessages, totalSessions int
	perBranch := make(map[string]*branchStats)
	perProvider := make(map[domain.ProviderName]int)
	fileCounts := make(map[string]int)

	for _, s := range summaries {
		totalSessions++
		totalTokens += s.TotalTokens
		totalMessages += s.MessageCount

		// Per-branch stats
		bs, ok := perBranch[s.Branch]
		if !ok {
			bs = &branchStats{branch: s.Branch}
			perBranch[s.Branch] = bs
		}
		bs.sessionCount++
		bs.totalTokens += s.TotalTokens

		// Per-provider counts
		perProvider[s.Provider]++

		// Load full session for file changes
		full, getErr := store.Get(s.ID)
		if getErr == nil {
			for _, fc := range full.FileChanges {
				fileCounts[fc.FilePath]++
			}
		}
	}

	// Print overall stats
	fmt.Fprintln(out, "=== Overall Statistics ===")
	fmt.Fprintf(out, "  Sessions:  %d\n", totalSessions)
	fmt.Fprintf(out, "  Messages:  %d\n", totalMessages)
	fmt.Fprintf(out, "  Tokens:    %s\n", formatTokens(totalTokens))
	fmt.Fprintln(out)

	// Print per-provider stats
	fmt.Fprintln(out, "=== By Provider ===")
	for prov, count := range perProvider {
		fmt.Fprintf(out, "  %-14s %d sessions\n", prov, count)
	}
	fmt.Fprintln(out)

	// Print per-branch stats (sorted by token count descending)
	branchList := make([]*branchStats, 0, len(perBranch))
	for _, bs := range perBranch {
		branchList = append(branchList, bs)
	}
	sort.Slice(branchList, func(i, j int) bool {
		return branchList[i].totalTokens > branchList[j].totalTokens
	})

	fmt.Fprintln(out, "=== By Branch ===")
	fmt.Fprintf(out, "  %-30s  %8s  %8s\n", "BRANCH", "SESSIONS", "TOKENS")
	for _, bs := range branchList {
		fmt.Fprintf(out, "  %-30s  %8d  %8s\n", truncate(bs.branch, 30), bs.sessionCount, formatTokens(bs.totalTokens))
	}
	fmt.Fprintln(out)

	// Print top files (up to 10)
	if len(fileCounts) > 0 {
		type fileEntry struct {
			path  string
			count int
		}
		files := make([]fileEntry, 0, len(fileCounts))
		for path, count := range fileCounts {
			files = append(files, fileEntry{path: path, count: count})
		}
		sort.Slice(files, func(i, j int) bool {
			return files[i].count > files[j].count
		})

		fmt.Fprintln(out, "=== Most Touched Files ===")
		limit := len(files)
		if limit > 10 {
			limit = 10
		}
		for _, f := range files[:limit] {
			fmt.Fprintf(out, "  %4d  %s\n", f.count, f.path)
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
