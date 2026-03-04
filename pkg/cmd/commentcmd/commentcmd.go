// Package commentcmd implements the `aisync comment` CLI command.
// It posts or updates a PR comment with an AI session summary.
package commentcmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the comment command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	SessionFlag string
	PRFlag      int
}

// NewCmdComment creates the `aisync comment` command.
func NewCmdComment(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "comment",
		Short: "Post session summary as a PR comment",
		Long:  "Posts or updates a PR comment with the AI session summary. Uses an HTML marker for idempotent updates.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComment(opts)
		},
	}

	cmd.Flags().StringVar(&opts.SessionFlag, "session", "", "Session ID to comment (default: current branch session)")
	cmd.Flags().IntVar(&opts.PRFlag, "pr", 0, "PR number to comment on (default: auto-detect from branch)")

	return cmd
}

// aisyncMarker is re-exported from the service layer for tests.
const aisyncMarker = service.AisyncMarker

// buildCommentBody wraps the service-layer function for backward compatibility.
func buildCommentBody(sess *session.Session) string {
	return service.BuildCommentBody(sess)
}

func runComment(opts *Options) error {
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

	// Comment
	result, err := svc.Comment(service.CommentRequest{
		SessionID:   sessionID,
		ProjectPath: topLevel,
		Branch:      branch,
		PRNumber:    opts.PRFlag,
	})
	if err != nil {
		return err
	}

	if result.Updated {
		fmt.Fprintf(out, "Updated aisync comment on PR #%d\n", result.PRNumber)
	} else {
		fmt.Fprintf(out, "Posted aisync comment on PR #%d\n", result.PRNumber)
	}

	return nil
}
