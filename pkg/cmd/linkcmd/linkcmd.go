// Package linkcmd implements the `aisync link` CLI command.
// It associates a captured session with a PR, commit, or other git object.
package linkcmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the link command.
type Options struct {
	IO          *iostreams.IOStreams
	Factory     *cmdutil.Factory
	SessionFlag string
	CommitFlag  string
	PRFlag      int
	AutoDetect  bool
}

// NewCmdLink creates the `aisync link` command.
func NewCmdLink(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "link",
		Short: "Link a session to a PR or commit",
		Long:  "Associates a captured session with a pull request or commit for cross-referencing.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLink(opts)
		},
	}

	cmd.Flags().StringVar(&opts.SessionFlag, "session", "", "Session ID to link (default: current branch session)")
	cmd.Flags().IntVar(&opts.PRFlag, "pr", 0, "PR number to link to")
	cmd.Flags().StringVar(&opts.CommitFlag, "commit", "", "Commit SHA to link to")
	cmd.Flags().BoolVar(&opts.AutoDetect, "auto", false, "Auto-detect PR from current branch")

	return cmd
}

func runLink(opts *Options) error {
	out := opts.IO.Out

	// Require at least one link target
	if opts.PRFlag == 0 && opts.CommitFlag == "" && !opts.AutoDetect {
		return fmt.Errorf("specify --pr <number>, --commit <sha>, or --auto to detect PR from branch")
	}

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

	// Link
	result, err := svc.Link(service.LinkRequest{
		SessionID:   sessionID,
		ProjectPath: topLevel,
		Branch:      branch,
		PRNumber:    opts.PRFlag,
		CommitSHA:   opts.CommitFlag,
		AutoDetect:  opts.AutoDetect,
	})
	if err != nil {
		return err
	}

	if result.PRNumber > 0 {
		fmt.Fprintf(out, "Linked session %s to PR #%d\n", result.SessionID, result.PRNumber)
	}
	if result.CommitSHA != "" {
		fmt.Fprintf(out, "Linked session %s to commit %s\n", result.SessionID, result.CommitSHA)
	}

	return nil
}
