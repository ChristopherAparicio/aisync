// Package toolusagecmd implements the `aisync tool-usage` CLI command.
// It shows per-tool token usage breakdown for a captured session.
package toolusagecmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the tool-usage command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	SessionID string
	JSON      bool
}

// NewCmdToolUsage creates the `aisync tool-usage` command.
func NewCmdToolUsage(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "tool-usage <session-id>",
		Short: "Show per-tool token usage for a session",
		Long: `Displays a breakdown of tool/MCP usage within a captured session.
Shows per-tool call count, token consumption, error rate, and average duration.
Useful for understanding where tokens are spent and identifying expensive tools.

Examples:
  aisync tool-usage abc123           # table output
  aisync tool-usage --json abc123    # structured JSON`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.SessionID = args[0]
			return runToolUsage(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Structured JSON output")

	return cmd
}

func runToolUsage(opts *Options) error {
	out := opts.IO.Out

	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	result, err := svc.ToolUsage(context.Background(), opts.SessionID)
	if err != nil {
		return err
	}

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	if result.TotalCalls == 0 {
		fmt.Fprintln(out, "No tool calls found in this session.")
		return nil
	}

	fmt.Fprintf(out, "Tool Usage — %d total calls\n\n", result.TotalCalls)
	fmt.Fprintf(out, "  %-20s  %6s  %8s  %8s  %8s  %6s  %8s  %7s\n",
		"TOOL", "CALLS", "IN TOK", "OUT TOK", "TOTAL", "ERR", "AVG ms", "%")
	fmt.Fprintf(out, "  %-20s  %6s  %8s  %8s  %8s  %6s  %8s  %7s\n",
		"────────────────────", "──────", "────────", "────────", "────────", "──────", "────────", "───────")

	for _, t := range result.Tools {
		avgDur := "—"
		if t.AvgDuration > 0 {
			avgDur = fmt.Sprintf("%d", t.AvgDuration)
		}
		fmt.Fprintf(out, "  %-20s  %6d  %8s  %8s  %8s  %6d  %8s  %6.1f%%\n",
			truncate(t.Name, 20),
			t.Calls,
			formatTokens(t.InputTokens),
			formatTokens(t.OutputTokens),
			formatTokens(t.TotalTokens),
			t.ErrorCount,
			avgDur,
			t.Percentage,
		)
	}

	if result.TotalCost.TotalCost > 0 {
		fmt.Fprintf(out, "\n  Estimated tool cost: %s\n", formatCost(result.TotalCost.TotalCost))
	}

	return nil
}

func formatTokens(n int) string {
	if n == 0 {
		return "0"
	}
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func formatCost(cost float64) string {
	if cost == 0 {
		return "$0.00"
	}
	if cost < 0.01 {
		return fmt.Sprintf("$%.4f", cost)
	}
	return fmt.Sprintf("$%.2f", cost)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}
