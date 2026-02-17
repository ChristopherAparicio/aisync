// Package synccmd implements the `aisync push`, `aisync pull`, and `aisync sync` CLI commands.
// The package is named synccmd to avoid conflict with the stdlib sync package.
package synccmd

import (
	"fmt"

	"github.com/spf13/cobra"

	syncsvc "github.com/ChristopherAparicio/aisync/internal/gitsync"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// PushOptions holds inputs for the push command.
type PushOptions struct {
	IO       *iostreams.IOStreams
	Factory  *cmdutil.Factory
	NoRemote bool
}

// PullOptions holds inputs for the pull command.
type PullOptions struct {
	IO       *iostreams.IOStreams
	Factory  *cmdutil.Factory
	NoRemote bool
}

// SyncOptions holds inputs for the sync command.
type SyncOptions struct {
	IO       *iostreams.IOStreams
	Factory  *cmdutil.Factory
	NoRemote bool
}

// NewCmdPush creates the `aisync push` command.
func NewCmdPush(f *cmdutil.Factory) *cobra.Command {
	opts := &PushOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push sessions to the sync branch",
		Long:  "Exports sessions from the local store to the aisync/sessions git branch and optionally pushes to the remote.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPush(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.NoRemote, "no-remote", false, "Only write to local sync branch, don't push to remote")

	return cmd
}

// NewCmdPull creates the `aisync pull` command.
func NewCmdPull(f *cmdutil.Factory) *cobra.Command {
	opts := &PullOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Pull sessions from the sync branch",
		Long:  "Imports sessions from the aisync/sessions git branch into the local store.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPull(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.NoRemote, "no-remote", false, "Only read from local sync branch, don't fetch from remote")

	return cmd
}

// NewCmdSync creates the `aisync sync` command.
func NewCmdSync(f *cmdutil.Factory) *cobra.Command {
	opts := &SyncOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync sessions with the team (pull then push)",
		Long:  "Pulls sessions from the remote, then pushes local sessions. Equivalent to running pull + push.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.NoRemote, "no-remote", false, "Only sync locally, don't interact with remote")

	return cmd
}

func runPush(opts *PushOptions) error {
	out := opts.IO.Out

	gitClient, err := opts.Factory.Git()
	if err != nil {
		return fmt.Errorf("not a git repository")
	}

	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	svc := syncsvc.NewService(gitClient, store)

	result, err := svc.Push(!opts.NoRemote)
	if err != nil {
		return err
	}

	if result.Pushed == 0 {
		fmt.Fprintln(out, "Nothing to push. All sessions already synced.")
		return nil
	}

	fmt.Fprintf(out, "Pushed %d session(s) to aisync/sessions branch.\n", result.Pushed)
	if result.Remote {
		fmt.Fprintln(out, "Pushed to remote origin.")
	}

	return nil
}

func runPull(opts *PullOptions) error {
	out := opts.IO.Out

	gitClient, err := opts.Factory.Git()
	if err != nil {
		return fmt.Errorf("not a git repository")
	}

	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	svc := syncsvc.NewService(gitClient, store)

	result, err := svc.Pull(!opts.NoRemote)
	if err != nil {
		return err
	}

	if result.Pulled == 0 {
		fmt.Fprintln(out, "Nothing to pull. Local store is up to date.")
		return nil
	}

	fmt.Fprintf(out, "Pulled %d session(s) from aisync/sessions branch.\n", result.Pulled)

	return nil
}

func runSync(opts *SyncOptions) error {
	out := opts.IO.Out

	gitClient, err := opts.Factory.Git()
	if err != nil {
		return fmt.Errorf("not a git repository")
	}

	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	svc := syncsvc.NewService(gitClient, store)
	remote := !opts.NoRemote

	// Pull first
	pullResult, err := svc.Pull(remote)
	if err != nil {
		return fmt.Errorf("pull: %w", err)
	}

	// Then push
	pushResult, err := svc.Push(remote)
	if err != nil {
		return fmt.Errorf("push: %w", err)
	}

	if pullResult.Pulled == 0 && pushResult.Pushed == 0 {
		fmt.Fprintln(out, "Everything up to date.")
		return nil
	}

	if pullResult.Pulled > 0 {
		fmt.Fprintf(out, "Pulled %d session(s).\n", pullResult.Pulled)
	}
	if pushResult.Pushed > 0 {
		fmt.Fprintf(out, "Pushed %d session(s).\n", pushResult.Pushed)
	}

	return nil
}
