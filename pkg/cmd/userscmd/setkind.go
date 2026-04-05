package userscmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

type setKindOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory
}

func newCmdSetKind(f *cmdutil.Factory) *cobra.Command {
	opts := &setKindOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	return &cobra.Command{
		Use:   "set-kind <user-id> <human|machine|unknown>",
		Short: "Set the kind classification for a user",
		Long:  "Override the automatic classification of a user as human, machine (bot/CI), or unknown.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetKind(opts, args[0], args[1])
		},
	}
}

func runSetKind(opts *setKindOptions, userID, kind string) error {
	// Validate kind
	switch session.UserKind(kind) {
	case session.UserKindHuman, session.UserKindMachine, session.UserKindUnknown:
		// valid
	default:
		return fmt.Errorf("invalid kind %q: must be human, machine, or unknown", kind)
	}

	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	if err := store.UpdateUserKind(session.ID(userID), kind); err != nil {
		return fmt.Errorf("updating kind: %w", err)
	}

	fmt.Fprintf(opts.IO.Out, "Updated user %s kind to %q\n", userID, kind)
	return nil
}
