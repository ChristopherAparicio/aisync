// Package userscmd implements the `aisync users` CLI command group.
// It provides commands to list and manage user identities (kind, Slack, role).
package userscmd

import (
	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

// NewCmdUsers creates the `aisync users` command group.
func NewCmdUsers(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "users",
		Short:   "Manage user identities and classifications",
		Long:    "List and manage user identities — set kind (human/machine), Slack identity, and notification role.",
		Aliases: []string{"user"},
	}

	cmd.AddCommand(newCmdList(f))
	cmd.AddCommand(newCmdSetKind(f))
	cmd.AddCommand(newCmdSetSlack(f))
	cmd.AddCommand(newCmdSetRole(f))
	cmd.AddCommand(newCmdBackfillKind(f))
	cmd.AddCommand(newCmdSyncSlack(f))
	cmd.AddCommand(newCmdLink(f))

	return cmd
}
