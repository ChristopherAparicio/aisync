package userscmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

type linkOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory
}

func newCmdLink(f *cmdutil.Factory) *cobra.Command {
	opts := &linkOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	return &cobra.Command{
		Use:   "link <user-id> <slack-id> [slack-name]",
		Short: "Link a Git user to a Slack member",
		Long: `Manually link a Git user to a Slack member by setting the user's
Slack ID and optional display name.

This is used to confirm identity match suggestions or to manually
set up a link that automatic matching couldn't find.

Examples:
  aisync users link abc123 U0123ABCDEF "John Doe"
  aisync users link abc123 U0123ABCDEF`,
		Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			slackName := ""
			if len(args) >= 3 {
				slackName = args[2]
			}
			return runLink(opts, args[0], args[1], slackName)
		},
	}
}

func runLink(opts *linkOptions, userID, slackID, slackName string) error {
	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	// Verify the user exists
	user, err := store.GetUser(session.ID(userID))
	if err != nil {
		return fmt.Errorf("looking up user: %w", err)
	}
	if user == nil {
		return fmt.Errorf("user %q not found", userID)
	}

	// Update the slack identity
	if err := store.UpdateUserSlack(session.ID(userID), slackID, slackName); err != nil {
		return fmt.Errorf("linking user: %w", err)
	}

	out := opts.IO.Out
	fmt.Fprintf(out, "Linked %s (%s) → Slack %s", user.Name, user.Email, slackID)
	if slackName != "" {
		fmt.Fprintf(out, " (%s)", slackName)
	}
	fmt.Fprintln(out)

	return nil
}
