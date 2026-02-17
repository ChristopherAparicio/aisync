// Package commentcmd implements the `aisync comment` CLI command.
// It posts or updates a PR comment with an AI session summary.
package commentcmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// aisyncMarker is the HTML comment used to identify aisync PR comments for idempotent updates.
const aisyncMarker = "<!-- aisync -->"

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

	// Get platform
	plat, err := opts.Factory.Platform()
	if err != nil {
		return fmt.Errorf("platform not available: %w", err)
	}

	// Determine PR number
	prNumber := opts.PRFlag
	if prNumber == 0 {
		pr, prErr := plat.GetPRForBranch(branch)
		if prErr != nil {
			return fmt.Errorf("no open PR found for branch %q (use --pr to specify): %w", branch, prErr)
		}
		prNumber = pr.Number
		fmt.Fprintf(out, "Auto-detected PR #%d\n", prNumber)
	}

	// Get store
	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	// Find session
	var session *domain.Session
	if opts.SessionFlag != "" {
		sid, parseErr := domain.ParseSessionID(opts.SessionFlag)
		if parseErr != nil {
			return parseErr
		}
		session, err = store.Get(sid)
		if err != nil {
			return fmt.Errorf("session not found: %w", err)
		}
	} else {
		// Try to find session linked to this PR first
		summaries, lookupErr := store.GetByLink(domain.LinkPR, strconv.Itoa(prNumber))
		if lookupErr == nil && len(summaries) > 0 {
			session, err = store.Get(summaries[0].ID)
			if err != nil {
				return fmt.Errorf("loading session: %w", err)
			}
		} else {
			// Fall back to branch session
			session, err = store.GetByBranch(topLevel, branch)
			if err != nil {
				return fmt.Errorf("no session found for branch %q: %w", branch, err)
			}
		}
	}

	// Build comment body
	body := buildCommentBody(session)

	// Check for existing aisync comment (idempotent update)
	comments, err := plat.ListComments(prNumber)
	if err != nil {
		return fmt.Errorf("listing PR comments: %w", err)
	}

	var existingID int64
	for _, c := range comments {
		if strings.Contains(c.Body, aisyncMarker) {
			existingID = c.ID
			break
		}
	}

	if existingID > 0 {
		if updateErr := plat.UpdateComment(existingID, body); updateErr != nil {
			return fmt.Errorf("updating comment: %w", updateErr)
		}
		fmt.Fprintf(out, "Updated aisync comment on PR #%d\n", prNumber)
	} else {
		if addErr := plat.AddComment(prNumber, body); addErr != nil {
			return fmt.Errorf("adding comment: %w", addErr)
		}
		fmt.Fprintf(out, "Posted aisync comment on PR #%d\n", prNumber)
	}

	return nil
}

// buildCommentBody creates the Markdown comment body from a session.
func buildCommentBody(s *domain.Session) string {
	var b strings.Builder

	b.WriteString(aisyncMarker)
	b.WriteString("\n## 🤖 AI Session Summary\n\n")
	b.WriteString(fmt.Sprintf("**Session:** `%s`\n", s.ID))
	b.WriteString(fmt.Sprintf("**Provider:** %s\n", s.Provider))
	b.WriteString(fmt.Sprintf("**Branch:** %s\n", s.Branch))

	if s.Summary != "" {
		b.WriteString(fmt.Sprintf("\n### Summary\n\n%s\n", s.Summary))
	}

	// Token usage
	if s.TokenUsage.TotalTokens > 0 {
		b.WriteString("\n### Token Usage\n\n")
		b.WriteString("| Metric | Count |\n")
		b.WriteString("|--------|-------|\n")
		b.WriteString(fmt.Sprintf("| Input  | %d |\n", s.TokenUsage.InputTokens))
		b.WriteString(fmt.Sprintf("| Output | %d |\n", s.TokenUsage.OutputTokens))
		b.WriteString(fmt.Sprintf("| Total  | %d |\n", s.TokenUsage.TotalTokens))
	}

	// Messages stats
	b.WriteString(fmt.Sprintf("\n**Messages:** %d\n", len(s.Messages)))

	// File changes
	if len(s.FileChanges) > 0 {
		b.WriteString("\n### Files Changed\n\n")
		for _, fc := range s.FileChanges {
			b.WriteString(fmt.Sprintf("- `%s` (%s)\n", fc.FilePath, fc.ChangeType))
		}
	}

	b.WriteString("\n---\n*Posted by [aisync](https://github.com/ChristopherAparicio/aisync)*\n")

	return b.String()
}
