// Package diagnosecmd implements the `aisync diagnose` CLI command.
// It provides unified session debugging: health score, error timeline,
// phase analysis, tool report, verdict, and optional LLM-powered deep analysis.
package diagnosecmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the diagnose command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	SessionID string
	Deep      bool
	Model     string
	JSON      bool
	Quiet     bool
}

// NewCmdDiagnose creates the `aisync diagnose` command.
func NewCmdDiagnose(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "diagnose <session-id>",
		Short: "Unified session debugging and health analysis",
		Long: `Diagnose a session to understand what went wrong and how to fix it.

The default quick scan is instant (no LLM needed) and produces:
  - Health score (0-100, A-F grade)
  - Error timeline (positions errors in message flow)
  - Phase analysis (clean start / late crash / error from start)
  - Tool report (per-tool error rates)
  - Verdict (healthy / degraded / broken)
  - Restore advice (suggested rewind point + filter flags)

Use --deep for LLM-powered root cause analysis and suggestions.

Examples:
  aisync diagnose abc123                  # quick scan
  aisync diagnose abc123 --deep           # + LLM root cause analysis
  aisync diagnose abc123 --json           # structured JSON output
  aisync diagnose abc123 --quiet          # one-liner verdict only`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.SessionID = args[0]
			return runDiagnose(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Deep, "deep", false, "Include LLM-powered deep analysis (root cause, suggestions)")
	cmd.Flags().StringVar(&opts.Model, "model", "", "LLM model to use for deep analysis")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Structured JSON output")
	cmd.Flags().BoolVar(&opts.Quiet, "quiet", false, "One-liner verdict only")

	return cmd
}

func runDiagnose(opts *Options) error {
	out := opts.IO.Out

	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	sid, err := session.ParseID(opts.SessionID)
	if err != nil {
		return err
	}

	report, err := svc.Diagnose(context.Background(), service.DiagnoseRequest{
		SessionID: sid,
		Deep:      opts.Deep,
		Model:     opts.Model,
	})
	if err != nil {
		return err
	}

	// JSON output.
	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	// Quiet mode: verdict one-liner only.
	if opts.Quiet {
		fmt.Fprintf(out, "%s (%d/100): %s\n",
			report.Verdict.Status, report.Verdict.Score, report.Verdict.OneLiner)
		return nil
	}

	// Full text output.
	renderReport(out, report)
	return nil
}

func renderReport(out io.Writer, r *session.DiagnosisReport) {
	// ── Verdict ──
	icon := verdictIcon(r.Verdict.Status)
	fmt.Fprintf(out, "%s %s  Score: %d/100  Grade: %s\n",
		icon, strings.ToUpper(r.Verdict.Status), r.Verdict.Score, r.HealthScore.Grade)
	fmt.Fprintf(out, "  %s\n\n", r.Verdict.OneLiner)

	// ── Health Score breakdown ──
	fmt.Fprintln(out, "Health Score Breakdown:")
	fmt.Fprintf(out, "  Error:      %d/30\n", r.HealthScore.ErrorScore)
	fmt.Fprintf(out, "  Saturation: %d/25\n", r.HealthScore.SaturationScore)
	fmt.Fprintf(out, "  Cache:      %d/20\n", r.HealthScore.CacheScore)
	fmt.Fprintf(out, "  Completion: %d/15\n", r.HealthScore.CompletionScore)
	fmt.Fprintf(out, "  Efficiency: %d/10\n\n", r.HealthScore.EfficiencyScore)

	// ── Phase Analysis ──
	if r.Phases.Pattern != "too-short" {
		fmt.Fprintf(out, "Phase Analysis: %s\n", r.Phases.Pattern)
		for _, p := range r.Phases.Phases {
			fmt.Fprintf(out, "  [%d-%d] %s  errors=%d/%d (%.0f%%)\n",
				p.StartMsg, p.EndMsg, p.Label, p.ToolErrors, p.ToolCalls, p.ErrorRate)
		}
		if r.Phases.TurningPoint > 0 {
			fmt.Fprintf(out, "  Turning point: message %d\n", r.Phases.TurningPoint)
		}
		fmt.Fprintln(out)
	}

	// ── Overload ──
	if r.Overload.Verdict != "healthy" {
		fmt.Fprintf(out, "Overload: %s\n", r.Overload.Verdict)
		if r.Overload.Reason != "" {
			fmt.Fprintf(out, "  %s\n", r.Overload.Reason)
		}
		fmt.Fprintln(out)
	}

	// ── Tool Report ──
	if r.ToolReport.TotalCalls > 0 {
		fmt.Fprintf(out, "Tool Report: %d calls, %d errors\n", r.ToolReport.TotalCalls, r.ToolReport.TotalErrors)
		for _, t := range r.ToolReport.Tools {
			errMark := ""
			if t.Errors > 0 {
				errMark = fmt.Sprintf(" (%.0f%% error rate)", t.ErrorRate)
			}
			fmt.Fprintf(out, "  %-20s calls=%-4d errors=%-3d%s\n", t.Name, t.Calls, t.Errors, errMark)
		}
		fmt.Fprintln(out)
	}

	// ── Error Timeline ──
	if len(r.ErrorTimeline) > 0 {
		fmt.Fprintf(out, "Error Timeline: %d errors\n", len(r.ErrorTimeline))
		limit := len(r.ErrorTimeline)
		if limit > 10 {
			limit = 10
		}
		for _, e := range r.ErrorTimeline[:limit] {
			esc := ""
			if e.IsEscalation {
				esc = " [escalation]"
			}
			fmt.Fprintf(out, "  [msg %d] %s %s%s\n",
				e.MessageIndex, e.Phase, e.Error.Category, esc)
		}
		if len(r.ErrorTimeline) > 10 {
			fmt.Fprintf(out, "  ... and %d more\n", len(r.ErrorTimeline)-10)
		}
		fmt.Fprintln(out)
	}

	// ── Error Summary ──
	if r.ErrorSummary.TotalErrors > 0 {
		fmt.Fprintf(out, "Error Summary: %d total errors\n", r.ErrorSummary.TotalErrors)
		for cat, count := range r.ErrorSummary.ByCategory {
			fmt.Fprintf(out, "  %-20s %d\n", cat, count)
		}
		fmt.Fprintln(out)
	}

	// ── Restore Advice ──
	if r.RestoreAdvice != nil {
		fmt.Fprintln(out, "Restore Advice:")
		if r.RestoreAdvice.RecommendedRewindTo > 0 {
			fmt.Fprintf(out, "  Rewind to message %d\n", r.RestoreAdvice.RecommendedRewindTo)
		}
		if len(r.RestoreAdvice.SuggestedFilters) > 0 {
			fmt.Fprintf(out, "  Suggested flags: %s\n", strings.Join(r.RestoreAdvice.SuggestedFilters, " "))
		}
		if r.RestoreAdvice.Reason != "" {
			fmt.Fprintf(out, "  Reason: %s\n", r.RestoreAdvice.Reason)
		}
		fmt.Fprintln(out)
	}

	// ── Deep Analysis ──
	if r.RootCause != "" {
		fmt.Fprintln(out, "Root Cause Analysis:")
		fmt.Fprintf(out, "  %s\n\n", r.RootCause)
	}

	if len(r.Suggestions) > 0 {
		fmt.Fprintln(out, "Suggestions:")
		for _, s := range r.Suggestions {
			fmt.Fprintf(out, "  -> %s\n", s)
		}
		fmt.Fprintln(out)
	}
}

func verdictIcon(status string) string {
	switch status {
	case "healthy":
		return "[OK]"
	case "degraded":
		return "[!!]"
	case "broken":
		return "[XX]"
	default:
		return "[??]"
	}
}
