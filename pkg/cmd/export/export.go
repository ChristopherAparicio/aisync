// Package export implements the `aisync export` CLI command.
package export

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the export command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	FormatFlag  string
	OutputFlag  string
	SessionFlag string
}

// NewCmdExport creates the `aisync export` command.
func NewCmdExport(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export a session to a file",
		Long:  "Exports a captured session in unified JSON, Claude Code JSONL, or OpenCode JSON format.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExport(opts)
		},
	}

	cmd.Flags().StringVar(&opts.FormatFlag, "format", "aisync", "Output format: aisync, claude, opencode, context")
	cmd.Flags().StringVarP(&opts.OutputFlag, "output", "o", "", "Output file path (default: stdout)")
	cmd.Flags().StringVar(&opts.SessionFlag, "session", "", "Export a specific session by ID")

	return cmd
}

func runExport(opts *Options) error {
	// Git info (for branch-based resolution)
	var projectPath, branch string
	gitClient, gitErr := opts.Factory.Git()
	if gitErr == nil {
		branch, _ = gitClient.CurrentBranch()
		projectPath, _ = gitClient.TopLevel()
	}

	// Parse session ID
	var sessionID session.ID
	if opts.SessionFlag != "" {
		parsed, parseErr := session.ParseID(opts.SessionFlag)
		if parseErr != nil {
			return parseErr
		}
		sessionID = parsed
	}

	// Get service
	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	// Export
	result, err := svc.Export(service.ExportRequest{
		SessionID:   sessionID,
		ProjectPath: projectPath,
		Branch:      branch,
		Format:      opts.FormatFlag,
	})
	if err != nil {
		return err
	}

	// Write output
	if opts.OutputFlag != "" {
		if writeErr := os.WriteFile(opts.OutputFlag, result.Data, 0o644); writeErr != nil {
			return fmt.Errorf("writing file: %w", writeErr)
		}
		fmt.Fprintf(opts.IO.ErrOut, "Exported session %s (%s format, %s) -> %s\n",
			result.SessionID, result.Format, formatSize(len(result.Data)), opts.OutputFlag)
	} else {
		_, _ = opts.IO.Out.Write(result.Data)
	}

	return nil
}

func formatSize(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
}
