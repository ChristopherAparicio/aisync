package backfillcmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

func newCmdBackfillFiles(f *cmdutil.Factory) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "files",
		Short: "Extract file operations from session tool calls",
		Long: `Parse tool calls in all sessions to extract file-level blame data.

For each session, this command:
  1. Loads the full session payload (messages + tool calls)
  2. Parses Read/Write/Edit/Bash tool inputs to find file paths
  3. Stores (session_id, file_path, change_type, tool_name) in file_changes

This enables:
  - File blame: "which sessions modified this file?"
  - Session file list: "what files did this session touch?"

Note: This is opt-in (features.file_blame in config.json).
Sessions that already have file data are skipped.
This can be slow for large session databases — each payload must be
decompressed and parsed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := f.SessionService()
			if err != nil {
				return err
			}

			processed, filesExtracted, err := svc.BackfillFileBlame(context.Background())
			if err != nil {
				return err
			}

			if jsonOutput {
				data, _ := json.MarshalIndent(map[string]int{
					"sessions_processed": processed,
					"files_extracted":    filesExtracted,
				}, "", "  ")
				fmt.Fprintln(f.IOStreams.Out, string(data))
				return nil
			}

			fmt.Fprintf(f.IOStreams.Out, "File blame backfill complete:\n")
			fmt.Fprintf(f.IOStreams.Out, "  Sessions processed: %d\n", processed)
			fmt.Fprintf(f.IOStreams.Out, "  Files extracted:    %d\n", filesExtracted)
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}
