// Package blamecmd implements the `aisync blame` CLI command.
// It performs a reverse lookup from file_changes to find which AI sessions
// touched a given file — like `git blame` but for AI sessions.
package blamecmd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the blame command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	FilePath string
	Branch   string
	Provider string
	All      bool
	Restore  bool
	JSON     bool
	Quiet    bool
}

// NewCmdBlame creates the `aisync blame` command.
func NewCmdBlame(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "blame <file>",
		Short: "Find which AI sessions touched a file",
		Long: `Reverse lookup from file changes to AI sessions.
Shows which AI sessions modified a given file, ordered by most recent first.

By default, only the most recent session is shown. Use --all to see all sessions.

Examples:
  aisync blame src/main.go                    # last session that touched this file
  aisync blame --all src/main.go              # all sessions that touched this file
  aisync blame --branch feat/auth handler.go  # filter by branch
  aisync blame --restore handler.go           # restore the last session that touched this file
  aisync blame --json src/main.go             # machine-readable output`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.FilePath = args[0]
			return runBlame(opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "Show all sessions (default: most recent only)")
	cmd.Flags().BoolVar(&opts.Restore, "restore", false, "Restore the most recent session that touched this file")
	cmd.Flags().StringVar(&opts.Branch, "branch", "", "Filter by git branch")
	cmd.Flags().StringVar(&opts.Provider, "provider", "", "Filter by provider (claude-code, opencode, cursor)")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output results as JSON")
	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Only print session IDs")

	return cmd
}

func runBlame(opts *Options) error {
	out := opts.IO.Out

	// Resolve project path for restore shortcut
	var projectPath string
	gitClient, err := opts.Factory.Git()
	if err == nil {
		projectPath, _ = gitClient.TopLevel()
	}

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

	result, err := svc.Blame(context.Background(), service.BlameRequest{
		FilePath:    opts.FilePath,
		ProjectPath: projectPath,
		Branch:      opts.Branch,
		Provider:    providerName,
		All:         opts.All,
		Restore:     opts.Restore,
	})
	if err != nil {
		return err
	}

	// Handle restore result
	if result.Restored != nil {
		fmt.Fprintf(out, "Restored session %s (%s)\n", result.Restored.Session.ID, result.Restored.Method)
		return nil
	}

	// JSON output
	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result.Entries)
	}

	// Quiet mode
	if opts.Quiet {
		for _, e := range result.Entries {
			fmt.Fprintln(out, e.SessionID)
		}
		return nil
	}

	// No results
	if len(result.Entries) == 0 {
		fmt.Fprintf(out, "No AI sessions found for file %q\n", opts.FilePath)
		return nil
	}

	// Header
	if opts.All {
		fmt.Fprintf(out, "AI sessions that touched %q (%d found):\n\n", opts.FilePath, len(result.Entries))
	} else {
		fmt.Fprintf(out, "Last AI session that touched %q:\n\n", opts.FilePath)
	}

	// Table
	fmt.Fprintf(out, "%-12s  %-12s  %-24s  %-8s  %-12s  %s\n",
		"SESSION_ID", "PROVIDER", "BRANCH", "CHANGE", "DATE", "SUMMARY")
	fmt.Fprintf(out, "%-12s  %-12s  %-24s  %-8s  %-12s  %s\n",
		"----------", "--------", "------", "------", "----", "-------")

	for _, e := range result.Entries {
		id := truncate(string(e.SessionID), 12)
		prov := truncate(string(e.Provider), 12)
		br := truncate(e.Branch, 24)
		change := truncate(string(e.ChangeType), 8)
		date := timeAgo(e.CreatedAt)
		summary := truncate(e.Summary, 40)

		fmt.Fprintf(out, "%-12s  %-12s  %-24s  %-8s  %-12s  %s\n",
			id, prov, br, change, date, summary)
	}

	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
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
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%dd ago", days)
	}
}
