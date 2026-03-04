// Package initcmd implements the `aisync init` command.
// The package is named initcmd because "init" is a Go builtin.
package initcmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// InitOptions holds all inputs for the init command.
type InitOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	NoHooks bool
}

// NewCmdInit creates the `aisync init` command.
func NewCmdInit(f *cmdutil.Factory) *cobra.Command {
	opts := &InitOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize aisync in the current Git repository",
		Long:  "Creates .aisync/config.json, detects available providers, and optionally installs git hooks.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.NoHooks, "no-hooks", false, "Skip git hook installation")

	return cmd
}

func runInit(opts *InitOptions) error {
	out := opts.IO.Out

	// Verify we're in a git repo
	gitClient, err := opts.Factory.Git()
	if err != nil {
		return fmt.Errorf("not a git repository — run 'git init' first")
	}

	topLevel, err := gitClient.TopLevel()
	if err != nil {
		return fmt.Errorf("could not determine repository root: %w", err)
	}

	configDir := filepath.Join(topLevel, ".aisync")
	configFile := filepath.Join(configDir, "config.json")

	// Check if already initialized
	if _, statErr := os.Stat(configFile); statErr == nil {
		fmt.Fprintln(out, "aisync is already initialized in this repository.")
		fmt.Fprintf(out, "  Config: %s\n", configFile)
		return nil
	}

	// Create config
	cfg, err := opts.Factory.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if saveErr := cfg.Save(); saveErr != nil {
		return fmt.Errorf("saving config: %w", saveErr)
	}

	fmt.Fprintln(out, "Initialized aisync!")
	fmt.Fprintf(out, "  Config: %s\n", configFile)

	// Show detected providers
	providers := cfg.GetProviders()
	if len(providers) > 0 {
		fmt.Fprintf(out, "  Providers: ")
		for i, p := range providers {
			if i > 0 {
				fmt.Fprint(out, ", ")
			}
			fmt.Fprint(out, p)
		}
		fmt.Fprintln(out)
	}

	// Hooks installation
	if opts.NoHooks {
		fmt.Fprintln(out, "  Hooks: skipped (--no-hooks)")
	} else {
		mgr, hooksErr := opts.Factory.HooksManager()
		if hooksErr != nil {
			fmt.Fprintf(out, "  Hooks: could not determine hooks directory: %v\n", hooksErr)
			return nil
		}
		if installErr := mgr.Install(); installErr != nil {
			fmt.Fprintf(out, "  Hooks: installation failed: %v\n", installErr)
			return nil
		}
		fmt.Fprintln(out, "  Hooks: installed (pre-commit, commit-msg, post-checkout)")
	}

	return nil
}
