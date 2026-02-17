// Package linkcmd implements the `aisync link` CLI command.
// It associates a captured session with a PR, commit, or other git object.
package linkcmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/domain"
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

	// Get dependencies
	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	// Find the session to link
	var sessionID domain.SessionID
	if opts.SessionFlag != "" {
		parsed, parseErr := domain.ParseSessionID(opts.SessionFlag)
		if parseErr != nil {
			return parseErr
		}
		sessionID = parsed
	} else {
		// Find session for current branch
		session, lookupErr := store.GetByBranch(topLevel, branch)
		if lookupErr != nil {
			return fmt.Errorf("no session found for branch %q: %w", branch, lookupErr)
		}
		sessionID = session.ID
	}

	// Auto-detect PR from current branch if requested
	if opts.AutoDetect && opts.PRFlag == 0 {
		plat, platErr := opts.Factory.Platform()
		if platErr != nil {
			return fmt.Errorf("platform not available: %w", platErr)
		}
		pr, prErr := plat.GetPRForBranch(branch)
		if prErr != nil {
			return fmt.Errorf("no open PR found for branch %q: %w", branch, prErr)
		}
		opts.PRFlag = pr.Number
		fmt.Fprintf(out, "Auto-detected PR #%d: %s\n", pr.Number, pr.Title)
	}

	// Add links
	var linked int

	if opts.PRFlag > 0 {
		link := domain.Link{
			LinkType: domain.LinkPR,
			Ref:      strconv.Itoa(opts.PRFlag),
		}
		if linkErr := store.AddLink(sessionID, link); linkErr != nil {
			return fmt.Errorf("linking to PR #%d: %w", opts.PRFlag, linkErr)
		}
		fmt.Fprintf(out, "Linked session %s to PR #%d\n", sessionID, opts.PRFlag)
		linked++
	}

	if opts.CommitFlag != "" {
		link := domain.Link{
			LinkType: domain.LinkCommit,
			Ref:      opts.CommitFlag,
		}
		if linkErr := store.AddLink(sessionID, link); linkErr != nil {
			return fmt.Errorf("linking to commit %s: %w", opts.CommitFlag, linkErr)
		}
		fmt.Fprintf(out, "Linked session %s to commit %s\n", sessionID, opts.CommitFlag)
		linked++
	}

	if linked == 0 {
		return fmt.Errorf("no links were added")
	}

	return nil
}
