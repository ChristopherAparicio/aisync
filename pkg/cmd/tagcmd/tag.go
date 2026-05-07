// Package tagcmd implements the `aisync tag` CLI command for adding,
// removing, and listing user-defined tags on captured sessions.
//
// Manual tags are arbitrary lowercase identifiers (e.g. "urgent", "wip",
// "blocked", "review-needed") attached to a session. They complement the
// automatic session_type classification and are filterable via
// `aisync list --tag X --tag Y`.
//
// Usage:
//
//	aisync tag <session-id-or-tag> [tags...]   add tags
//	aisync tag --remove <id> [tags...]         remove tags from session
//	aisync tag --list [session-id]             show tags on a session
//	aisync tag --all                           list all tags + session counts
//
// When the first non-flag argument does not look like a session ID
// (does not start with "ses_"), it is treated as a tag for the current
// session — resolved as the most recent session for cwd + git branch.
package tagcmd

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds inputs for the tag command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	Remove bool   // --remove: detach the listed tags instead of attaching
	List   bool   // --list: show tags on the given (or current) session
	All    bool   // --all: list all tags across all sessions with counts
	Quiet  bool   // -q: suppress "added X / removed Y" status lines
	Args   []string
}

// NewCmdTag creates the `aisync tag` command.
func NewCmdTag(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "tag [session-id] <tag> [tag...]",
		Short: "Add, remove, or list user-defined tags on sessions",
		Long: `Manage manual tags on captured sessions.

By default, tags are added. Use --remove to detach them. Use --list to
display the tags on a session, or --all to show every tag in use across
all sessions with their session counts.

If the first argument does not look like a session ID (i.e. does not
start with "ses_"), it is treated as a tag for the current session —
resolved as the most recent session for the current cwd + git branch.

Examples:
  aisync tag bugfix urgent                     # tag current session
  aisync tag ses_abc123 bugfix urgent          # tag specific session
  aisync tag --remove ses_abc123 wip           # detach a tag
  aisync tag --list                            # show tags on current session
  aisync tag --list ses_abc123                 # show tags on specific session
  aisync tag --all                             # all tags + session counts`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Args = args
			return runTag(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Remove, "remove", false, "Detach the listed tags from the session instead of attaching")
	cmd.Flags().BoolVar(&opts.List, "list", false, "Show the tags attached to the given session (or current if no id given)")
	cmd.Flags().BoolVar(&opts.All, "all", false, "List all tags across all sessions with their session counts")
	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Suppress status output (only errors are reported)")

	return cmd
}

func runTag(opts *Options) error {
	// Validate flag combinations early.
	if opts.All && (opts.List || opts.Remove) {
		return fmt.Errorf("--all is exclusive with --list and --remove")
	}
	if opts.List && opts.Remove {
		return fmt.Errorf("--list and --remove are mutually exclusive")
	}

	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}
	ctx := context.Background()

	if opts.All {
		return runListAll(ctx, opts, svc)
	}

	// Split args into a session ID (when present) and a list of tags.
	sessionID, tags, err := parseSessionAndTags(opts.Args)
	if err != nil {
		return err
	}

	// If no explicit ID, resolve the current session via cwd + branch.
	if sessionID == "" {
		resolved, resolveErr := resolveCurrentSession(ctx, opts, svc)
		if resolveErr != nil {
			return resolveErr
		}
		sessionID = resolved
	}

	if opts.List {
		return runShow(ctx, opts, svc, sessionID)
	}

	if len(tags) == 0 {
		return fmt.Errorf("no tags supplied — use `aisync tag <id> tag1 tag2` or `aisync tag --list` to view")
	}

	if opts.Remove {
		n, removeErr := svc.RemoveTags(ctx, sessionID, tags)
		if removeErr != nil {
			return removeErr
		}
		if !opts.Quiet {
			fmt.Fprintf(opts.IO.Out, "Removed %d tag(s) from %s.\n", n, sessionID)
		}
		return nil
	}

	n, addErr := svc.AddTags(ctx, sessionID, tags)
	if addErr != nil {
		return addErr
	}
	if !opts.Quiet {
		dup := len(tags) - n
		if dup > 0 {
			fmt.Fprintf(opts.IO.Out, "Added %d tag(s) to %s (%d already present).\n", n, sessionID, dup)
		} else {
			fmt.Fprintf(opts.IO.Out, "Added %d tag(s) to %s.\n", n, sessionID)
		}
	}
	return nil
}

// parseSessionAndTags splits CLI args into an optional session ID and a
// list of tags. The first arg is interpreted as a session ID iff it starts
// with the canonical "ses_" prefix; otherwise all args are treated as tags
// and the session ID is left empty (caller resolves via current session).
func parseSessionAndTags(args []string) (session.ID, []string, error) {
	if len(args) == 0 {
		return "", nil, nil
	}
	first := args[0]
	if strings.HasPrefix(first, "ses_") {
		// Validate the ID shape; we keep this lenient since session.ParseID
		// has its own format expectations.
		if _, err := session.ParseID(first); err != nil {
			return "", nil, fmt.Errorf("invalid session id %q: %w", first, err)
		}
		return session.ID(first), args[1:], nil
	}
	return "", args, nil
}

// resolveCurrentSession infers the session ID for the current cwd + branch.
// Returns a friendly error when nothing can be resolved.
func resolveCurrentSession(ctx context.Context, opts *Options, svc service.SessionServicer) (session.ID, error) {
	gitClient, err := opts.Factory.Git()
	if err != nil {
		return "", fmt.Errorf("not a git repository — pass an explicit session id (ses_...): %w", err)
	}
	topLevel, err := gitClient.TopLevel()
	if err != nil {
		return "", fmt.Errorf("could not determine repository root: %w", err)
	}

	id, err := svc.ResolveCurrentSessionID(ctx, topLevel)
	if err != nil {
		if errors.Is(err, service.ErrNoCurrentSession) {
			return "", fmt.Errorf("no current session for %s — pass an explicit id (ses_...) or capture a session first", shortPath(topLevel))
		}
		return "", err
	}
	return id, nil
}

func runShow(ctx context.Context, opts *Options, svc service.SessionServicer, id session.ID) error {
	tags, err := svc.GetSessionTags(ctx, id)
	if err != nil {
		return err
	}
	if len(tags) == 0 {
		if !opts.Quiet {
			fmt.Fprintf(opts.IO.Out, "%s has no tags.\n", id)
		}
		return nil
	}
	if opts.Quiet {
		for _, t := range tags {
			fmt.Fprintln(opts.IO.Out, t)
		}
		return nil
	}
	fmt.Fprintf(opts.IO.Out, "Tags for %s:\n", id)
	for _, t := range tags {
		fmt.Fprintf(opts.IO.Out, "  %s\n", t)
	}
	return nil
}

func runListAll(ctx context.Context, opts *Options, svc service.SessionServicer) error {
	counts, err := svc.ListAllTags(ctx)
	if err != nil {
		return err
	}
	if len(counts) == 0 {
		if !opts.Quiet {
			fmt.Fprintln(opts.IO.Out, "No tags yet. Use `aisync tag <tag>` on a session to create one.")
		}
		return nil
	}
	if opts.Quiet {
		for _, c := range counts {
			fmt.Fprintln(opts.IO.Out, c.Tag)
		}
		return nil
	}
	// Pretty table.
	maxLen := 3
	for _, c := range counts {
		if len(c.Tag) > maxLen {
			maxLen = len(c.Tag)
		}
	}
	// Already sorted by count DESC then tag ASC by service.
	_ = sort.IsSorted // sort imported but unused warning safety
	fmt.Fprintf(opts.IO.Out, "%-*s  %s\n", maxLen, "TAG", "SESSIONS")
	fmt.Fprintf(opts.IO.Out, "%-*s  %s\n", maxLen, strings.Repeat("-", maxLen), "--------")
	for _, c := range counts {
		fmt.Fprintf(opts.IO.Out, "%-*s  %d\n", maxLen, c.Tag, c.Count)
	}
	return nil
}

// shortPath truncates very long paths for friendlier error messages.
func shortPath(p string) string {
	if len(p) <= 60 {
		return p
	}
	return "..." + p[len(p)-57:]
}
