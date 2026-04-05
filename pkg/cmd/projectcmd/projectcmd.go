// Package projectcmd implements `aisync project` — commands for managing
// project configuration (init, list, show, sync-prs).
package projectcmd

import (
	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

// NewCmdProject creates the `aisync project` command group.
func NewCmdProject(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage project configuration",
		Long: `Configure projects for aisync — set default branch, enable PR tracking,
manage budgets, and more.

Examples:
  aisync project init           # interactive wizard for current directory
  aisync project list           # list all known projects
  aisync project show           # show config for current project
  aisync project sync-prs       # manually sync PRs for current project`,
	}

	cmd.AddCommand(newCmdInit(f))
	cmd.AddCommand(newCmdList(f))
	cmd.AddCommand(newCmdShow(f))
	cmd.AddCommand(newCmdSyncPRs(f))

	return cmd
}
