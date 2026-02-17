// Package export implements the `aisync export` CLI command.
package export

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/domain"
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
	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	// Resolve session
	var session *domain.Session
	if opts.SessionFlag != "" {
		sessionID, parseErr := domain.ParseSessionID(opts.SessionFlag)
		if parseErr != nil {
			return parseErr
		}
		session, err = store.Get(sessionID)
		if err != nil {
			return fmt.Errorf("session %q not found: %w", opts.SessionFlag, err)
		}
	} else {
		// Use current branch
		gitClient, gitErr := opts.Factory.Git()
		if gitErr != nil {
			return fmt.Errorf("not a git repository: %w", gitErr)
		}
		branch, branchErr := gitClient.CurrentBranch()
		if branchErr != nil {
			return fmt.Errorf("could not determine current branch: %w", branchErr)
		}
		topLevel, topErr := gitClient.TopLevel()
		if topErr != nil {
			return fmt.Errorf("could not determine repository root: %w", topErr)
		}
		session, err = store.GetByBranch(topLevel, branch)
		if err != nil {
			return fmt.Errorf("no session found for current branch: %w", err)
		}
	}

	// Convert to the requested format
	var output []byte
	formatLabel := opts.FormatFlag

	switch opts.FormatFlag {
	case "aisync", "":
		output, err = json.MarshalIndent(session, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling session: %w", err)
		}
		output = append(output, '\n')
		formatLabel = "aisync"

	case "claude", "claude-code":
		conv := converter.New()
		output, err = conv.ToNative(session, domain.ProviderClaudeCode)
		if err != nil {
			return fmt.Errorf("converting to Claude format: %w", err)
		}
		formatLabel = "claude"

	case "opencode":
		conv := converter.New()
		output, err = conv.ToNative(session, domain.ProviderOpenCode)
		if err != nil {
			return fmt.Errorf("converting to OpenCode format: %w", err)
		}

	case "context", "context.md":
		output = converter.ToContextMD(session)
		formatLabel = "context.md"

	default:
		return fmt.Errorf("unknown format %q: valid values are [aisync, claude, opencode, context]", opts.FormatFlag)
	}

	// Write output
	if opts.OutputFlag != "" {
		if writeErr := os.WriteFile(opts.OutputFlag, output, 0o644); writeErr != nil {
			return fmt.Errorf("writing file: %w", writeErr)
		}
		fmt.Fprintf(opts.IO.ErrOut, "Exported session %s (%s format, %s) -> %s\n",
			session.ID, formatLabel, formatSize(len(output)), opts.OutputFlag)
	} else {
		// Write to stdout
		_, _ = opts.IO.Out.Write(output)
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
