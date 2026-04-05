package userscmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

type setSlackOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory
}

func newCmdSetSlack(f *cmdutil.Factory) *cobra.Command {
	opts := &setSlackOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	return &cobra.Command{
		Use:   "set-slack <user-id> <slack-id> [slack-name]",
		Short: "Set the Slack identity for a user",
		Long:  "Map a user to their Slack account for DM notifications. Slack ID is required (e.g. U0123ABCDEF). Slack display name is optional.",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			slackName := ""
			if len(args) >= 3 {
				slackName = args[2]
			}
			return runSetSlack(opts, args[0], args[1], slackName)
		},
	}
}

func runSetSlack(opts *setSlackOptions, userID, slackID, slackName string) error {
	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	if err := store.UpdateUserSlack(session.ID(userID), slackID, slackName); err != nil {
		return fmt.Errorf("updating slack: %w", err)
	}

	msg := fmt.Sprintf("Updated user %s Slack ID to %q", userID, slackID)
	if slackName != "" {
		msg += fmt.Sprintf(" (name: %q)", slackName)
	}
	fmt.Fprintln(opts.IO.Out, msg)
	return nil
}
