// Package capture implements the `aisync capture` CLI command.
package capture

import (
	"fmt"

	"github.com/spf13/cobra"

	capturesvc "github.com/ChristopherAparicio/aisync/internal/capture"
	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the capture command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	ProviderFlag string
	ModeFlag     string
	Message      string
	Auto         bool
}

// NewCmdCapture creates the `aisync capture` command.
func NewCmdCapture(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Capture the active AI session",
		Long:  "Captures the currently active AI session and stores it linked to the current branch.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCapture(opts)
		},
	}

	cmd.Flags().StringVar(&opts.ProviderFlag, "provider", "", "Force a specific provider (claude-code, opencode)")
	cmd.Flags().StringVar(&opts.ModeFlag, "mode", "", "Storage mode: full, compact, summary")
	cmd.Flags().StringVar(&opts.Message, "message", "", "Manual summary message")
	cmd.Flags().BoolVar(&opts.Auto, "auto", false, "Auto mode (used by git hooks, silent)")

	return cmd
}

func runCapture(opts *Options) error {
	out := opts.IO.Out

	// Git info
	gitClient, err := opts.Factory.Git()
	if err != nil {
		return fmt.Errorf("not a git repository")
	}

	branch, err := gitClient.CurrentBranch()
	if err != nil {
		return fmt.Errorf("could not determine current branch: %w", err)
	}

	topLevel, err := gitClient.TopLevel()
	if err != nil {
		return fmt.Errorf("could not determine repository root: %w", err)
	}

	// Resolve storage mode
	cfg, cfgErr := opts.Factory.Config()
	mode := domain.StorageModeCompact
	if cfgErr == nil {
		mode = cfg.GetStorageMode()
	}
	if opts.ModeFlag != "" {
		parsed, parseErr := domain.ParseStorageMode(opts.ModeFlag)
		if parseErr != nil {
			return parseErr
		}
		mode = parsed
	}

	// Resolve provider
	var providerName domain.ProviderName
	if opts.ProviderFlag != "" {
		parsed, parseErr := domain.ParseProviderName(opts.ProviderFlag)
		if parseErr != nil {
			return parseErr
		}
		providerName = parsed
	}

	// Get dependencies
	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	registry := opts.Factory.Registry()

	// Create capture service with optional scanner
	scanner := opts.Factory.Scanner()
	var svc *capturesvc.Service
	if scanner != nil {
		svc = capturesvc.NewServiceWithScanner(registry, store, scanner)
	} else {
		svc = capturesvc.NewService(registry, store)
	}

	// Capture
	result, err := svc.Capture(capturesvc.Request{
		ProjectPath:  topLevel,
		Branch:       branch,
		Mode:         mode,
		ProviderName: providerName,
		Message:      opts.Message,
	})
	if err != nil {
		if opts.Auto {
			// Silent failure in auto mode
			return nil
		}
		return err
	}

	if !opts.Auto {
		fmt.Fprintf(out, "Captured session %s\n", result.Session.ID)
		fmt.Fprintf(out, "  Provider: %s\n", result.Provider)
		fmt.Fprintf(out, "  Branch:   %s\n", result.Session.Branch)
		fmt.Fprintf(out, "  Mode:     %s\n", result.Session.StorageMode)
		fmt.Fprintf(out, "  Messages: %d\n", len(result.Session.Messages))
		if result.Session.Summary != "" {
			fmt.Fprintf(out, "  Summary:  %s\n", result.Session.Summary)
		}
		if result.SecretsFound > 0 {
			fmt.Fprintf(out, "  Secrets:  %d detected", result.SecretsFound)
			if scanner != nil {
				switch scanner.Mode() {
				case domain.SecretModeMask:
					fmt.Fprint(out, " (masked)")
				case domain.SecretModeWarn:
					fmt.Fprint(out, " (warning: stored as-is)")
				}
			}
			fmt.Fprintln(out)
		}
	}

	return nil
}
