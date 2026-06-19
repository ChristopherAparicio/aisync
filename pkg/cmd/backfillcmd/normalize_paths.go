package backfillcmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

func newCmdBackfillNormalizePaths(f *cmdutil.Factory) *cobra.Command {
	var jsonOutput bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "normalize-paths",
		Short: "Rewrite absolute in-project file paths to project-relative form",
		Long: `Normalize stored file_changes paths so blame matches across machines.

Sessions captured before path normalization stored absolute file paths
(e.g. /home/alice/proj/src/main.go). Those don't match the relative paths
produced by 'git status', nor sessions pulled from a teammate whose absolute
prefix differs. This command rewrites in-project absolute paths to their
project-relative form (src/main.go), anchored at each session's project_path.

Out-of-project paths stay absolute. Already-relative paths are left untouched,
so re-running is safe and idempotent.

Run with --dry-run first (ideally on a copy of the database) to preview the
counts before applying.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := f.SessionService()
			if err != nil {
				return err
			}

			stats, err := svc.NormalizePaths(context.Background(), dryRun)
			if err != nil {
				return err
			}

			if jsonOutput {
				data, _ := json.MarshalIndent(stats, "", "  ")
				fmt.Fprintln(f.IOStreams.Out, string(data))
				return nil
			}

			if dryRun {
				fmt.Fprintf(f.IOStreams.Out, "Path normalization (dry-run, no changes written):\n")
			} else {
				fmt.Fprintf(f.IOStreams.Out, "Path normalization complete:\n")
			}
			fmt.Fprintf(f.IOStreams.Out, "  Scanned:       %d absolute paths\n", stats.Scanned)
			fmt.Fprintf(f.IOStreams.Out, "  Normalized:    %d (rewritten to project-relative)\n", stats.Normalized)
			fmt.Fprintf(f.IOStreams.Out, "  Kept absolute: %d (out-of-project)\n", stats.KeptAbsolute)
			fmt.Fprintf(f.IOStreams.Out, "  Skipped:       %d (no project_path anchor)\n", stats.Skipped)
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview counts without writing changes")
	return cmd
}
