// Package usagecmd implements the `aisync usage` CLI command.
// It provides on-demand token usage bucket computation.
package usagecmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

// NewCmdUsage creates the `aisync usage` command group.
func NewCmdUsage(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Token usage management",
		Long:  `Manage token usage data: compute pre-aggregated buckets for dashboards and reports.`,
	}

	cmd.AddCommand(newCmdUsageCompute(f))
	return cmd
}

func newCmdUsageCompute(f *cmdutil.Factory) *cobra.Command {
	var (
		granularity string
		incremental bool
		jsonOutput  bool
	)

	cmd := &cobra.Command{
		Use:   "compute",
		Short: "Pre-compute token usage buckets for dashboards",
		Long: `Scan sessions and pre-compute token usage per time bucket.

This generates aggregated data used by the Usage Heatmap page and
other analytics features. Normally runs as a nightly scheduled task,
but this command lets you trigger it on-demand.

Granularity options:
  1h   Hourly buckets (default) — used by the usage heatmap
  1d   Daily buckets — used by trend analysis

Examples:
  aisync usage compute                  # full recompute, hourly
  aisync usage compute --incremental    # only new data since last run
  aisync usage compute --granularity 1d # daily buckets`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := f.SessionService()
			if err != nil {
				return err
			}

			result, err := svc.ComputeTokenBuckets(context.Background(), service.ComputeTokenBucketsRequest{
				Granularity: granularity,
				Incremental: incremental,
			})
			if err != nil {
				return err
			}

			if jsonOutput {
				data, _ := json.MarshalIndent(result, "", "  ")
				fmt.Fprintln(f.IOStreams.Out, string(data))
				return nil
			}

			fmt.Fprintf(f.IOStreams.Out, "Token usage computation complete:\n")
			fmt.Fprintf(f.IOStreams.Out, "  Buckets written:  %d\n", result.BucketsWritten)
			fmt.Fprintf(f.IOStreams.Out, "  Sessions scanned: %d\n", result.SessionsScanned)
			fmt.Fprintf(f.IOStreams.Out, "  Messages scanned: %d\n", result.MessagesScanned)
			fmt.Fprintf(f.IOStreams.Out, "  Duration:         %s\n", result.Duration.Round(1e6))
			return nil
		},
	}

	cmd.Flags().StringVar(&granularity, "granularity", "1h", "Bucket granularity: 1h (hourly) or 1d (daily)")
	cmd.Flags().BoolVar(&incremental, "incremental", false, "Only compute since last run")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}
