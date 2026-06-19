// Package blamecmd implements the `aisync blame` CLI command.
// It performs a reverse lookup from file_changes to find which AI sessions
// touched a given file — like `git blame` but for AI sessions.
package blamecmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

	FilePaths   []string
	FilesFrom   string
	ProjectPath string
	Branch      string
	Provider    string
	All         bool
	Restore     bool
	JSON        bool
	Quiet       bool
}

// NewCmdBlame creates the `aisync blame` command.
func NewCmdBlame(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "blame [file...]",
		Short: "Find which AI sessions touched a file",
		Long: `Reverse lookup from file changes to AI sessions.
Shows which AI sessions modified a given file, ordered by most recent first.

By default, only the most recent session is shown. Use --all to see all sessions.
Use --project to list all files touched in a project.

Examples:
  aisync blame src/main.go                    # last session that touched this file
  aisync blame --all src/main.go              # all sessions that touched this file
  aisync blame src/a.go src/b.go              # sessions that touched multiple files
  aisync blame --files-from changed.txt       # read file list from a manifest (JSON array or one path per line)
  aisync blame --branch feat/auth handler.go  # filter by branch
  aisync blame --restore handler.go           # restore the last session that touched this file
  aisync blame --json src/main.go             # machine-readable output
  aisync blame --project /path/to/project     # list all files touched in project`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.FilePaths = args
			return runBlame(opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "Show all sessions (default: most recent only)")
	cmd.Flags().BoolVar(&opts.Restore, "restore", false, "Restore the most recent session that touched this file")
	cmd.Flags().StringVar(&opts.Branch, "branch", "", "Filter by git branch")
	cmd.Flags().StringVar(&opts.Provider, "provider", "", "Filter by provider (claude-code, opencode, cursor)")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output results as JSON")
	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Only print session IDs")
	cmd.Flags().StringVar(&opts.ProjectPath, "project", "", "Show all files touched in this project path")
	cmd.Flags().StringVar(&opts.FilesFrom, "files-from", "", "Read file paths from a manifest (JSON array or one path per line); merged with positional args")

	return cmd
}

func runBlame(opts *Options) error {
	out := opts.IO.Out

	if opts.FilesFrom != "" {
		content, readErr := os.ReadFile(opts.FilesFrom)
		if readErr != nil {
			return fmt.Errorf("reading files manifest: %w", readErr)
		}
		manifestFiles, parseErr := service.ParseFileManifest(content)
		if parseErr != nil {
			return parseErr
		}
		opts.FilePaths = append(opts.FilePaths, manifestFiles...)
	}

	isProjectMode := opts.ProjectPath != ""

	// Require at least one file arg OR --project flag.
	if !isProjectMode && len(opts.FilePaths) == 0 {
		return fmt.Errorf("requires at least one file argument or --project flag")
	}

	// Resolve effective project path: explicit --project overrides git top-level.
	// git top-level is kept as fallback for the Restore shortcut in single-file mode.
	var effectiveProjectPath string
	gitClient, err := opts.Factory.Git()
	if err == nil {
		effectiveProjectPath, _ = gitClient.TopLevel()
	}
	if opts.ProjectPath != "" {
		effectiveProjectPath = opts.ProjectPath
	}

	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	var providerName session.ProviderName
	if opts.Provider != "" {
		parsed, parseErr := session.ParseProviderName(opts.Provider)
		if parseErr != nil {
			return parseErr
		}
		providerName = parsed
	}

	// Build the service request.
	// Single file uses FilePath so that --restore continues to work (service Restore
	// shortcut only fires in single-file mode). Multiple files use FilePaths.
	req := service.BlameRequest{
		ProjectPath: effectiveProjectPath,
		Branch:      opts.Branch,
		Provider:    providerName,
		All:         opts.All,
		Restore:     opts.Restore,
	}
	switch len(opts.FilePaths) {
	case 1:
		req.FilePath = opts.FilePaths[0]
	default:
		req.FilePaths = opts.FilePaths
	}

	result, err := svc.Blame(context.Background(), req)
	if err != nil {
		return err
	}

	if result.Restored != nil {
		fmt.Fprintf(out, "Restored session %s (%s)\n", result.Restored.Session.ID, result.Restored.Method)
		return nil
	}

	if isProjectMode {
		return renderProjectView(opts, result)
	}
	if result.FilesGrouped != nil {
		return renderGroupedFileMode(opts, result)
	}
	return renderFileMode(opts, result)
}

func renderGroupedFileMode(opts *Options, result *service.BlameResult) error {
	out := opts.IO.Out

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result.FilesGrouped)
	}

	if opts.Quiet {
		for _, g := range result.FilesGrouped {
			for _, e := range g.Sessions {
				fmt.Fprintln(out, e.SessionID)
			}
		}
		return nil
	}

	header := "Last AI session per file:"
	if opts.All {
		header = "All AI sessions grouped by file:"
	}
	fmt.Fprintf(out, "%s\n\n", header)

	fmt.Fprintf(out, "%-30s  %-12s  %-12s  %-12s  %-20s  %-8s  %-12s  %s\n",
		"FILE", "SESSION_ID", "PROVIDER", "AGENT", "BRANCH", "CHANGE", "DATE", "SUMMARY")
	fmt.Fprintf(out, "%-30s  %-12s  %-12s  %-12s  %-20s  %-8s  %-12s  %s\n",
		"----", "----------", "--------", "-----", "------", "------", "----", "-------")

	for _, g := range result.FilesGrouped {
		file := truncate(g.File, 30)
		if len(g.Sessions) == 0 {
			fmt.Fprintf(out, "%-30s  %s\n", file, "(no sessions)")
			continue
		}
		for _, e := range g.Sessions {
			id := truncate(string(e.SessionID), 12)
			prov := truncate(string(e.Provider), 12)
			agent := e.Agent
			if agent == "" {
				agent = "-"
			}
			br := truncate(e.Branch, 20)
			change := truncate(string(e.ChangeType), 8)
			date := timeAgo(e.CreatedAt)
			summary := truncate(e.Summary, 30)

			fmt.Fprintf(out, "%-30s  %-12s  %-12s  %-12s  %-20s  %-8s  %-12s  %s\n",
				file, id, prov, agent, br, change, date, summary)
		}
	}
	return nil
}

func renderProjectView(opts *Options, result *service.BlameResult) error {
	out := opts.IO.Out

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result.ProjectFiles)
	}

	if opts.Quiet {
		for _, e := range result.ProjectFiles {
			fmt.Fprintln(out, e.LastSessionID)
		}
		return nil
	}

	if len(result.ProjectFiles) == 0 {
		fmt.Fprintf(out, "No files found for project %q\n", opts.ProjectPath)
		return nil
	}

	fmt.Fprintf(out, "%-40s  %-12s  %-12s  %s\n", "FILE", "SESSION_ID", "AGENT", "DATE")
	fmt.Fprintf(out, "%-40s  %-12s  %-12s  %s\n", "----", "----------", "-----", "----")
	for _, e := range result.ProjectFiles {
		file := truncate(e.FilePath, 40)
		id := truncate(string(e.LastSessionID), 12)
		agent := e.LastAgent
		if agent == "" {
			agent = "-"
		}
		date := timeAgo(e.LastSessionTime)
		fmt.Fprintf(out, "%-40s  %-12s  %-12s  %s\n", file, id, agent, date)
	}
	return nil
}

func renderFileMode(opts *Options, result *service.BlameResult) error {
	out := opts.IO.Out

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result.Entries)
	}

	if opts.Quiet {
		for _, e := range result.Entries {
			fmt.Fprintln(out, e.SessionID)
		}
		return nil
	}

	if len(result.Entries) == 0 {
		if len(opts.FilePaths) == 1 {
			fmt.Fprintf(out, "No AI sessions found for file %q\n", opts.FilePaths[0])
		} else {
			fmt.Fprintf(out, "No AI sessions found for the specified files\n")
		}
		return nil
	}

	if opts.All {
		if len(opts.FilePaths) == 1 {
			fmt.Fprintf(out, "AI sessions that touched %q (%d found):\n\n", opts.FilePaths[0], len(result.Entries))
		} else {
			fmt.Fprintf(out, "AI sessions that touched %d files (%d found):\n\n", len(opts.FilePaths), len(result.Entries))
		}
	} else {
		if len(opts.FilePaths) == 1 {
			fmt.Fprintf(out, "Last AI session that touched %q:\n\n", opts.FilePaths[0])
		} else {
			fmt.Fprintf(out, "Last AI sessions that touched %d files:\n\n", len(opts.FilePaths))
		}
	}

	fmt.Fprintf(out, "%-12s  %-12s  %-12s  %-24s  %-8s  %-12s  %s\n",
		"SESSION_ID", "PROVIDER", "AGENT", "BRANCH", "CHANGE", "DATE", "SUMMARY")
	fmt.Fprintf(out, "%-12s  %-12s  %-12s  %-24s  %-8s  %-12s  %s\n",
		"----------", "--------", "-----", "------", "------", "----", "-------")

	for _, e := range result.Entries {
		id := truncate(string(e.SessionID), 12)
		prov := truncate(string(e.Provider), 12)
		agent := e.Agent
		if agent == "" {
			agent = "-"
		}
		br := truncate(e.Branch, 24)
		change := truncate(string(e.ChangeType), 8)
		date := timeAgo(e.CreatedAt)
		summary := truncate(e.Summary, 40)

		fmt.Fprintf(out, "%-12s  %-12s  %-12s  %-24s  %-8s  %-12s  %s\n",
			id, prov, agent, br, change, date, summary)
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
