// Package alertscmd implements `aisync alerts` — list and acknowledge
// notifications dispatched by the aisync internal alerting pipeline.
package alertscmd

import (
	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

func NewCmdAlerts(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alerts",
		Short: "List and acknowledge in-app alerts",
		Long: `Inspect and acknowledge in-app alerts (stall alerts, error spikes,
budget warnings, daily/weekly digests) recorded by the aisync notification
pipeline.

Examples:
  aisync alerts list                  # show unacknowledged alerts
  aisync alerts list --all            # include acknowledged history
  aisync alerts list --severity critical
  aisync alerts ack 42                # acknowledge alert id 42
  aisync alerts ack --all             # acknowledge every unacked alert`,
	}

	cmd.AddCommand(newCmdList(f))
	cmd.AddCommand(newCmdAck(f))

	return cmd
}
