// Package resumecmd implements the `aisync resume` CLI command.
// It combines `git checkout` + `aisync restore` in one step.
package resumecmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the resume command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	Branch    string
	SessionID string
	Provider  string
	AsContext bool
}

// NewCmdResume creates the `aisync resume` command.
func NewCmdResume(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "resume <branch>",
		Short: "Switch branch and restore the AI session",
		Long: `Convenience command that combines git checkout + aisync restore.
Switches to the given branch and restores the most recent AI session
for that branch.

Examples:
  aisync resume feat/auth               # checkout + restore
  aisync resume --session abc123 feat/x  # restore a specific session
  aisync resume --as-context feat/auth   # restore as context.md`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Branch = args[0]
			return runResume(opts)
		},
	}

	cmd.Flags().StringVar(&opts.SessionID, "session", "", "Specific session ID to restore")
	cmd.Flags().StringVar(&opts.Provider, "provider", "", "Target provider (claude-code, opencode, cursor)")
	cmd.Flags().BoolVar(&opts.AsContext, "as-context", false, "Restore as context.md instead of native format")

	return cmd
}

func runResume(opts *Options) error {
	out := opts.IO.Out

	// Git checkout
	gitClient, err := opts.Factory.Git()
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	if err := gitClient.Checkout(opts.Branch); err != nil {
		return fmt.Errorf("git checkout %s: %w", opts.Branch, err)
	}
	fmt.Fprintf(out, "Switched to branch %s\n", opts.Branch)

	// Get project path
	topLevel, err := gitClient.TopLevel()
	if err != nil {
		return fmt.Errorf("could not determine repository root: %w", err)
	}

	// Restore session
	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	var providerName session.ProviderName
	if opts.Provider != "" {
		parsed, parseErr := session.ParseProviderName(opts.Provider)
		if parseErr != nil {
			return parseErr
		}
		providerName = parsed
	}

	var sid session.ID
	if opts.SessionID != "" {
		parsed, parseErr := session.ParseID(opts.SessionID)
		if parseErr != nil {
			return parseErr
		}
		sid = parsed
	}

	result, err := svc.Restore(service.RestoreRequest{
		SessionID:    sid,
		ProjectPath:  topLevel,
		Branch:       opts.Branch,
		ProviderName: providerName,
		AsContext:    opts.AsContext,
	})
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}

	fmt.Fprintf(out, "Restored session %s (%s)\n", result.Session.ID, result.Method)
	return nil
}
