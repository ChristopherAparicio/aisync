// Package importcmd implements the `aisync import` CLI command.
// The package is named importcmd because "import" is a Go reserved word.
package importcmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the import command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	FormatFlag   string
	IntoFlag     string
	DiscoverFlag bool
	YesFlag      bool
	ModeFlag     string
}

// NewCmdImport creates the `aisync import` command.
func NewCmdImport(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "import [file]",
		Short: "Import sessions from a file or discover from providers",
		Long: `Imports sessions into aisync.

Two modes:
  aisync import <file>            Import a single session from a file
  aisync import --discover        Scan all providers, select projects, bulk import

The --discover flag scans installed AI providers (OpenCode, Claude Code),
lists all projects and session counts, lets you select which to import,
and captures everything into aisync.

Examples:
  aisync import session.json                  # import from file
  aisync import --discover                    # interactive bulk import
  aisync import --discover --yes              # import everything (non-interactive)
  aisync import --discover --mode compact     # set storage mode`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.DiscoverFlag {
				return runDiscover(&DiscoverOptions{
					IO:      opts.IO,
					Factory: opts.Factory,
					Yes:     opts.YesFlag,
					Mode:    opts.ModeFlag,
				})
			}
			if len(args) == 0 {
				return fmt.Errorf("provide a file path, or use --discover for bulk import")
			}
			return runImport(opts, args[0])
		},
	}

	cmd.Flags().StringVar(&opts.FormatFlag, "format", "", "Source format: aisync, claude, opencode (default: auto-detect)")
	cmd.Flags().StringVar(&opts.IntoFlag, "into", "aisync", "Target: aisync (store only), claude-code, opencode")
	cmd.Flags().BoolVar(&opts.DiscoverFlag, "discover", false, "Scan providers and interactively import sessions")
	cmd.Flags().BoolVarP(&opts.YesFlag, "yes", "y", false, "Accept all defaults without prompting (with --discover)")
	cmd.Flags().StringVar(&opts.ModeFlag, "mode", "compact", "Storage mode: full, compact, summary (with --discover)")

	return cmd
}

func runImport(opts *Options, filePath string) error {
	out := opts.IO.Out

	// Read the file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	if len(data) == 0 {
		return fmt.Errorf("file %s is empty", filePath)
	}

	// Get service
	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	// Import
	result, err := svc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: opts.FormatFlag,
		IntoTarget:   opts.IntoFlag,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Detected format: %s\n", result.SourceFormat)

	switch result.Target {
	case "aisync":
		fmt.Fprintf(out, "Stored session %s locally.\n", result.SessionID)
		fmt.Fprintf(out, "Use 'aisync restore --session %s' to load into your agent.\n", result.SessionID)
	default:
		fmt.Fprintf(out, "Imported session %s into %s.\n", result.SessionID, result.Target)
	}

	return nil
}
