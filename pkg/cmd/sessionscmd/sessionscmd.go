// Package sessionscmd implements `aisync sessions` — commands for inspecting
// session health (stalls, aborts, provider errors).
package sessionscmd

import (
	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

func NewCmdSessions(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "Inspect session health and stalls",
		Long: `Inspect AI session health — stuck subagents, aborted messages,
rate-limit hits, and provider errors detected by the stall_detector
scheduled task.

Examples:
  aisync sessions health           # show stall stats for last 7 days
  aisync sessions health --live    # show only currently-stuck sessions`,
	}

	cmd.AddCommand(newCmdHealth(f))

	return cmd
}
