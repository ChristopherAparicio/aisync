// Package backfillcmd implements the `aisync backfill` CLI command.
// It provides sub-commands for data quality maintenance tasks.
package backfillcmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

// NewCmdBackfill creates the `aisync backfill` command group.
func NewCmdBackfill(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backfill",
		Short: "Run data quality maintenance tasks",
		Long: `Run data quality maintenance tasks on existing sessions.

These commands fix historical data that may be incomplete:
  remote-url   Resolve git remote URLs for sessions missing them
  forks        Detect fork relationships across all sessions
  events       Extract session events and recompute analytics buckets`,
	}

	cmd.AddCommand(newCmdBackfillRemoteURL(f))
	cmd.AddCommand(newCmdBackfillForks(f))
	cmd.AddCommand(newCmdBackfillEvents(f))

	return cmd
}

func newCmdBackfillRemoteURL(f *cmdutil.Factory) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "remote-url",
		Short: "Resolve git remote URLs for sessions with empty remote_url",
		Long: `Resolve git remote URLs for sessions that don't have one.

This fixes project grouping in the dashboard by ensuring sessions
from the same git repository are grouped together, even when they
come from different local paths (e.g. OpenCode worktrees).

For each session with an empty remote_url, this command:
  1. Opens the session's project_path as a git repository
  2. Runs 'git remote get-url origin'
  3. Normalizes and stores the result`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := f.SessionService()
			if err != nil {
				return err
			}

			result, err := svc.BackfillRemoteURLs(context.Background())
			if err != nil {
				return err
			}

			if jsonOutput {
				data, _ := json.MarshalIndent(result, "", "  ")
				fmt.Fprintln(f.IOStreams.Out, string(data))
				return nil
			}

			fmt.Fprintf(f.IOStreams.Out, "Backfill complete:\n")
			fmt.Fprintf(f.IOStreams.Out, "  Candidates: %d sessions with empty remote_url\n", result.Candidates)
			fmt.Fprintf(f.IOStreams.Out, "  Updated:    %d\n", result.Updated)
			fmt.Fprintf(f.IOStreams.Out, "  Skipped:    %d (no git remote found)\n", result.Skipped)
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func newCmdBackfillForks(f *cmdutil.Factory) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "forks",
		Short: "Detect fork relationships across all sessions",
		Long: `Run fork detection on all sessions and persist the results.

Fork detection compares sessions on the same branch by analyzing
message overlap. When two sessions share a common prefix of messages,
one is identified as a fork of the other.

Results are stored in the session_forks table and displayed in the
branch explorer and session detail pages.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := f.SessionService()
			if err != nil {
				return err
			}

			result, err := svc.DetectForksBatch(context.Background())
			if err != nil {
				return err
			}

			if jsonOutput {
				data, _ := json.MarshalIndent(result, "", "  ")
				fmt.Fprintln(f.IOStreams.Out, string(data))
				return nil
			}

			fmt.Fprintf(f.IOStreams.Out, "Fork detection complete:\n")
			fmt.Fprintf(f.IOStreams.Out, "  Sessions scanned: %d\n", result.SessionsScanned)
			fmt.Fprintf(f.IOStreams.Out, "  Forks detected:   %d\n", result.ForksDetected)
			fmt.Fprintf(f.IOStreams.Out, "  Relations saved:  %d\n", result.RelationsSaved)
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}
