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

	FormatFlag   string
	OutputFlag   string
	SessionFlag  string
	ProviderFlag string
	AllFlag      bool
	GlobalFlag   bool
}

// NewCmdExport creates the `aisync export` command.
func NewCmdExport(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export a session (or all sessions) to a file",
		Long: `Exports sessions from aisync.

Single session (default):
  aisync export --session <id> -o sess.json    # unified, Claude, OpenCode, or context format

Bulk export (JSONL bundle, for moving sessions between servers):
  aisync export --all -o bundle.jsonl          # every session in the current project
  aisync export --global -o all.jsonl          # every session across all projects
  aisync export --all --provider opencode ...  # filter by provider

Bulk exports always use the unified aisync format as JSONL (one session per line)
and round-trip through 'aisync import'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExport(opts)
		},
	}

	cmd.Flags().StringVar(&opts.FormatFlag, "format", "aisync", "Output format: aisync, claude, opencode, context")
	cmd.Flags().StringVarP(&opts.OutputFlag, "output", "o", "", "Output file path (default: stdout)")
	cmd.Flags().StringVar(&opts.SessionFlag, "session", "", "Export a specific session by ID")
	cmd.Flags().BoolVar(&opts.AllFlag, "all", false, "Export all sessions in the current project as a JSONL bundle")
	cmd.Flags().BoolVar(&opts.GlobalFlag, "global", false, "Export all sessions across all projects as a JSONL bundle")
	cmd.Flags().StringVar(&opts.ProviderFlag, "provider", "", "Filter bulk export by provider (claude-code, opencode, …)")

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

	if opts.AllFlag || opts.GlobalFlag {
		return runExportAll(opts, svc, projectPath, branch)
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

func runExportAll(opts *Options, svc service.SessionServicer, projectPath, branch string) error {
	if opts.FormatFlag != "" && opts.FormatFlag != "aisync" {
		return fmt.Errorf("bulk export only supports the aisync format (got %q)", opts.FormatFlag)
	}

	var provider session.ProviderName
	if opts.ProviderFlag != "" {
		parsed, parseErr := session.ParseProviderName(opts.ProviderFlag)
		if parseErr != nil {
			return parseErr
		}
		provider = parsed
	}

	result, err := svc.ExportAll(service.ExportAllRequest{
		ProjectPath: projectPath,
		Branch:      branch,
		Provider:    provider,
		All:         opts.AllFlag,
		Global:      opts.GlobalFlag,
	})
	if err != nil {
		return err
	}

	if opts.OutputFlag != "" {
		if writeErr := os.WriteFile(opts.OutputFlag, result.Data, 0o644); writeErr != nil {
			return fmt.Errorf("writing file: %w", writeErr)
		}
		fmt.Fprintf(opts.IO.ErrOut, "Exported %d session(s) (aisync JSONL bundle, %s) -> %s\n",
			result.Count, formatSize(len(result.Data)), opts.OutputFlag)
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
