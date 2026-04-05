package userscmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

type setRoleOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory
}

func newCmdSetRole(f *cmdutil.Factory) *cobra.Command {
	opts := &setRoleOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	return &cobra.Command{
		Use:   "set-role <user-id> <admin|member>",
		Short: "Set the notification role for a user",
		Long:  "Set a user's role for notification routing. Admins receive machine account alerts.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetRole(opts, args[0], args[1])
		},
	}
}

func runSetRole(opts *setRoleOptions, userID, role string) error {
	// Validate role
	switch session.UserRole(role) {
	case session.UserRoleAdmin, session.UserRoleMember:
		// valid
	default:
		return fmt.Errorf("invalid role %q: must be admin or member", role)
	}

	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	if err := store.UpdateUserRole(session.ID(userID), role); err != nil {
		return fmt.Errorf("updating role: %w", err)
	}

	fmt.Fprintf(opts.IO.Out, "Updated user %s role to %q\n", userID, role)
	return nil
}
