// Package hooks implements the `aisync hooks` CLI command group.
package hooks

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/hooks"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// InstallOptions holds all inputs for the hooks install command.
type InstallOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory
}

// UninstallOptions holds all inputs for the hooks uninstall command.
type UninstallOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory
}

// StatusOptions holds all inputs for the hooks status command.
type StatusOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory
}

// NewCmdHooks creates the `aisync hooks` command group.
func NewCmdHooks(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Manage git hooks for aisync",
		Long:  "Install, uninstall, or check the status of aisync git hooks (pre-commit, commit-msg, post-checkout).",
	}

	cmd.AddCommand(newCmdInstall(f))
	cmd.AddCommand(newCmdUninstall(f))
	cmd.AddCommand(newCmdStatus(f))

	return cmd
}

func newCmdInstall(f *cmdutil.Factory) *cobra.Command {
	opts := &InstallOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	return &cobra.Command{
		Use:   "install",
		Short: "Install aisync git hooks",
		Long:  "Installs pre-commit, commit-msg, and post-checkout hooks. Chains with existing hooks if present.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(opts)
		},
	}
}

func newCmdUninstall(f *cmdutil.Factory) *cobra.Command {
	opts := &UninstallOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove aisync git hooks",
		Long:  "Removes aisync sections from git hooks. Preserves any non-aisync hook content.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUninstall(opts)
		},
	}
}

func newCmdStatus(f *cmdutil.Factory) *cobra.Command {
	opts := &StatusOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	return &cobra.Command{
		Use:   "status",
		Short: "Show git hooks installation status",
		Long:  "Shows which aisync git hooks are currently installed.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(opts)
		},
	}
}

func runInstall(opts *InstallOptions) error {
	out := opts.IO.Out

	mgr, err := createManager(opts.Factory)
	if err != nil {
		return err
	}

	if err := mgr.Install(); err != nil {
		return fmt.Errorf("installing hooks: %w", err)
	}

	fmt.Fprintln(out, "Git hooks installed!")
	for _, s := range mgr.Statuses() {
		fmt.Fprintf(out, "  %s: installed\n", s.Name)
	}

	return nil
}

func runUninstall(opts *UninstallOptions) error {
	out := opts.IO.Out

	mgr, err := createManager(opts.Factory)
	if err != nil {
		return err
	}

	if err := mgr.Uninstall(); err != nil {
		return fmt.Errorf("uninstalling hooks: %w", err)
	}

	fmt.Fprintln(out, "Git hooks removed.")
	return nil
}

func runStatus(opts *StatusOptions) error {
	out := opts.IO.Out

	mgr, err := createManager(opts.Factory)
	if err != nil {
		return err
	}

	statuses := mgr.Statuses()
	for _, s := range statuses {
		if s.Installed {
			fmt.Fprintf(out, "  %s: installed\n", s.Name)
		} else {
			fmt.Fprintf(out, "  %s: not installed\n", s.Name)
		}
	}

	return nil
}

// createManager resolves the git hooks directory and creates a hooks manager.
func createManager(f *cmdutil.Factory) (*hooks.Manager, error) {
	gitClient, err := f.Git()
	if err != nil {
		return nil, fmt.Errorf("not a git repository")
	}

	hooksDir, err := gitClient.HooksPath()
	if err != nil {
		return nil, fmt.Errorf("could not determine hooks directory: %w", err)
	}

	return hooks.NewManager(hooksDir), nil
}
