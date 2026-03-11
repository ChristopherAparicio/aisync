// Package diffcmd implements the `aisync diff` CLI command.
// It compares two sessions side-by-side: token usage, cost, files, tools, and message divergence.
package diffcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the diff command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	JSON bool
}

// NewCmdDiff creates the `aisync diff` command.
func NewCmdDiff(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "diff <session-id-1> <session-id-2>",
		Short: "Compare two sessions side-by-side",
		Long: `Compare two captured sessions and show differences in token usage,
cost, files touched, tool usage, and where their message sequences diverge.

Both arguments accept session IDs or git commit SHAs.

Examples:
  aisync diff abc123 def456
  aisync diff abc123 def456 --json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiff(opts, args[0], args[1])
		},
	}

	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output results as JSON")

	return cmd
}

func runDiff(opts *Options, leftID, rightID string) error {
	out := opts.IO.Out

	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	result, err := svc.Diff(context.Background(), service.DiffRequest{
		LeftID:  leftID,
		RightID: rightID,
	})
	if err != nil {
		return err
	}

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	printDiff(opts, result)
	return nil
}

func printDiff(opts *Options, r *session.DiffResult) {
	out := opts.IO.Out

	// Header: sides
	fmt.Fprintf(out, "%-20s %-30s %-30s\n", "", "Left", "Right")
	fmt.Fprintf(out, "%-20s %-30s %-30s\n", "ID", string(r.Left.ID), string(r.Right.ID))
	fmt.Fprintf(out, "%-20s %-30s %-30s\n", "Provider", string(r.Left.Provider), string(r.Right.Provider))
	if r.Left.Branch != "" || r.Right.Branch != "" {
		fmt.Fprintf(out, "%-20s %-30s %-30s\n", "Branch", r.Left.Branch, r.Right.Branch)
	}
	fmt.Fprintf(out, "%-20s %-30d %-30d\n", "Messages", r.Left.MessageCount, r.Right.MessageCount)
	fmt.Fprintf(out, "%-20s %-30d %-30d\n", "Total tokens", r.Left.TotalTokens, r.Right.TotalTokens)

	// Token delta
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Token delta:")
	fmt.Fprintf(out, "  Input:  %s\n", formatDelta(r.TokenDelta.InputDelta))
	fmt.Fprintf(out, "  Output: %s\n", formatDelta(r.TokenDelta.OutputDelta))
	fmt.Fprintf(out, "  Total:  %s\n", formatDelta(r.TokenDelta.TotalDelta))

	// Cost delta
	if r.CostDelta.LeftCost > 0 || r.CostDelta.RightCost > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Cost delta:")
		fmt.Fprintf(out, "  Left:  $%.4f\n", r.CostDelta.LeftCost)
		fmt.Fprintf(out, "  Right: $%.4f\n", r.CostDelta.RightCost)
		fmt.Fprintf(out, "  Delta: %s\n", formatCostDelta(r.CostDelta.Delta))
	}

	// File diff
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Files:")
	if len(r.FileDiff.Shared) > 0 {
		fmt.Fprintf(out, "  Shared (%d):     %s\n", len(r.FileDiff.Shared), strings.Join(r.FileDiff.Shared, ", "))
	}
	if len(r.FileDiff.LeftOnly) > 0 {
		fmt.Fprintf(out, "  Left only (%d):  %s\n", len(r.FileDiff.LeftOnly), strings.Join(r.FileDiff.LeftOnly, ", "))
	}
	if len(r.FileDiff.RightOnly) > 0 {
		fmt.Fprintf(out, "  Right only (%d): %s\n", len(r.FileDiff.RightOnly), strings.Join(r.FileDiff.RightOnly, ", "))
	}
	if len(r.FileDiff.Shared) == 0 && len(r.FileDiff.LeftOnly) == 0 && len(r.FileDiff.RightOnly) == 0 {
		fmt.Fprintln(out, "  (no file changes recorded)")
	}

	// Tool diff
	if len(r.ToolDiff.Entries) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Tools:")
		fmt.Fprintf(out, "  %-20s %10s %10s %10s\n", "Name", "Left", "Right", "Delta")
		for _, e := range r.ToolDiff.Entries {
			fmt.Fprintf(out, "  %-20s %10d %10d %10s\n", e.Name, e.LeftCalls, e.RightCalls, formatDelta(e.CallsDelta))
		}
	}

	// Message delta
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Messages:")
	fmt.Fprintf(out, "  Common prefix: %d message(s)\n", r.MessageDelta.CommonPrefix)
	if r.MessageDelta.LeftAfter > 0 || r.MessageDelta.RightAfter > 0 {
		fmt.Fprintf(out, "  After divergence: %d left, %d right\n", r.MessageDelta.LeftAfter, r.MessageDelta.RightAfter)
	}
}

// formatDelta formats an integer delta with a +/- prefix.
func formatDelta(d int) string {
	if d > 0 {
		return fmt.Sprintf("+%d", d)
	}
	return fmt.Sprintf("%d", d)
}

// formatCostDelta formats a cost delta with +/- prefix and $ sign.
func formatCostDelta(d float64) string {
	if d > 0 {
		return fmt.Sprintf("+$%.4f", d)
	}
	if d < 0 {
		return fmt.Sprintf("-$%.4f", -d)
	}
	return "$0.0000"
}
