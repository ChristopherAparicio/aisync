// Package inspectcmd implements the `aisync inspect` CLI command.
// It produces a comprehensive session analysis report covering token usage,
// image/screenshot costs, compaction patterns, command output, tool errors,
// behavioral patterns, and detected problems.
//
// All diagnostic output is observational — facts, counts, ratios. Detected
// problems describe what was observed, not what to do about it.
//
// With --generate-fix, the command also produces provider-specific artefacts
// (scripts, agent instructions patches, skills/commands) that address detected
// problems. These are always opt-in — printed to stdout for review, or written
// to disk with --apply.
package inspectcmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/diagnostic"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
	"github.com/ChristopherAparicio/aisync/internal/storage"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the inspect command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	SessionID   string
	JSON        bool
	Section     string // optional: "tokens", "images", "compactions", "commands", "errors", "patterns", "problems"
	GenerateFix bool   // when true, generate provider-specific fix artefacts
	Apply       bool   // when true, write fix artefacts to disk
}

// NewCmdInspect creates the `aisync inspect` command.
func NewCmdInspect(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "inspect <session-id>",
		Short: "Deep-inspect a session: tokens, images, compactions, commands, errors, patterns",
		Long: `Produces a comprehensive analysis report for a single session.

Covers six sections (all printed by default):
  - tokens:       input/output/cache/image breakdown per model
  - images:       screenshot analysis — count, tokens/image, context duration, cost
  - compactions:  compaction timeline, cascades, detection coverage
  - commands:     tool call output sizes, top commands by output footprint
  - errors:       tool error loops, error rates, consecutive failures
  - patterns:     behavioral anti-patterns (yolo editing, glob storms, drift)

Plus a "problems" section listing all detected issues with severity and impact.

Use --section to limit output to a specific section.
Use --json for machine-readable structured output.
Use --generate-fix to produce provider-specific fix artefacts.
Use --generate-fix --apply to write fixes to disk.

Examples:
  aisync inspect ses_abc123                       # full report
  aisync inspect --json ses_abc123                # structured JSON
  aisync inspect --section images abc123          # images only
  aisync inspect --section problems abc           # just detected problems
  aisync inspect --generate-fix ses_abc123        # print fixes to stdout
  aisync inspect --generate-fix --apply ses_abc   # write fixes to disk
  aisync inspect --generate-fix --json ses_abc    # fixes as JSON`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.SessionID = args[0]
			if opts.Apply && !opts.GenerateFix {
				return fmt.Errorf("--apply requires --generate-fix")
			}
			return runInspect(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Structured JSON output")
	cmd.Flags().StringVar(&opts.Section, "section", "", "Limit to section: tokens, images, compactions, commands, errors, patterns, problems, trend")
	cmd.Flags().BoolVar(&opts.GenerateFix, "generate-fix", false, "Generate provider-specific fix artefacts for detected problems")
	cmd.Flags().BoolVar(&opts.Apply, "apply", false, "Write generated fixes to disk (requires --generate-fix)")

	return cmd
}

func runInspect(opts *Options) error {
	out := opts.IO.Out

	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	sess, err := svc.Get(opts.SessionID)
	if err != nil {
		return fmt.Errorf("session %q not found: %w", opts.SessionID, err)
	}

	// Try to load store for richer data (events + trend)
	var events []sessionevent.Event
	store, _ := opts.Factory.Store()
	if store != nil {
		events, _ = store.GetSessionEvents(sess.ID)
	}

	// Discover and load script modules from .aisync/modules/ and ~/.aisync/modules/
	scriptDirs := diagnostic.DefaultScriptDirs()
	scriptMods := diagnostic.DiscoverScriptModules(scriptDirs)
	extraModules := diagnostic.AsAnalysisModules(scriptMods)

	// Build the report (built-in + script modules)
	report := diagnostic.BuildReport(sess, events, extraModules...)

	// Historical trend comparison — attach if store has enough baseline data
	attachTrend(report, sess, store)

	// If --generate-fix, produce fix artefacts
	if opts.GenerateFix {
		provider, _ := session.ParseProviderName(string(sess.Provider))
		if !provider.Valid() {
			provider = session.ProviderOpenCode // default fallback
		}
		fixSet := diagnostic.GenerateFixes(report, provider)
		fixSet.Applied = opts.Apply

		if opts.JSON {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(fixSet)
		}

		printFixes(out, fixSet)

		if opts.Apply {
			return applyFixes(out, fixSet)
		}
		return nil
	}

	// JSON output
	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	// Text output
	sec := opts.Section
	all := sec == ""

	fmt.Fprintf(out, "=== SESSION INSPECTION ===\n")
	fmt.Fprintf(out, "ID:       %s\n", report.SessionID)
	fmt.Fprintf(out, "Provider: %s\n", report.Provider)
	fmt.Fprintf(out, "Agent:    %s\n", report.Agent)
	fmt.Fprintf(out, "Messages: %d (%d user, %d assistant)\n\n", report.Messages, report.UserMsgs, report.AsstMsgs)

	if all || sec == "tokens" {
		printTokens(out, report.Tokens)
	}
	if all || sec == "images" {
		printImages(out, report.Images)
	}
	if all || sec == "compactions" {
		printCompactions(out, report.Compaction)
	}
	if all || sec == "commands" {
		printCommands(out, report.Commands)
	}
	if all || sec == "errors" {
		printToolErrors(out, report.ToolErrors)
	}
	if all || sec == "patterns" {
		printPatterns(out, report.Patterns)
	}
	if all || sec == "problems" {
		printProblems(out, report.Problems)
	}
	if all || sec == "trend" {
		printTrend(out, report.Trend)
	}

	return nil
}

// --- Print functions ---

func printTokens(w io.Writer, t *diagnostic.TokenSection) {
	if t == nil {
		return
	}
	fmt.Fprintf(w, "─── TOKENS ────────────────────────────────────────────\n")
	fmt.Fprintf(w, "  Input:       %s\n", fmtNum(t.Input))
	fmt.Fprintf(w, "  Output:      %s\n", fmtNum(t.Output))
	fmt.Fprintf(w, "  Total:       %s\n", fmtNum(t.Total))
	if t.Image > 0 {
		fmt.Fprintf(w, "  Image:       %s\n", fmtNum(t.Image))
	}
	if t.CacheRead > 0 {
		fmt.Fprintf(w, "  Cache read:  %s (%.1f%% of input)\n", fmtNum(t.CacheRead), t.CachePct)
	}
	if t.CacheWrite > 0 {
		fmt.Fprintf(w, "  Cache write: %s\n", fmtNum(t.CacheWrite))
	}
	if t.EstCost > 0 {
		fmt.Fprintf(w, "  Est. cost:   $%.2f\n", t.EstCost)
	}
	if t.InputOutputRatio > 0 {
		fmt.Fprintf(w, "  I/O ratio:   %.0f:1\n", t.InputOutputRatio)
	}
	if len(t.Models) > 0 {
		fmt.Fprintf(w, "\n  Per model:\n")
		fmt.Fprintf(w, "  %-35s  %10s  %10s  %6s\n", "MODEL", "INPUT", "OUTPUT", "MSGS")
		for _, m := range t.Models {
			fmt.Fprintf(w, "  %-35s  %10s  %10s  %6d\n", m.Model, fmtNum(m.Input), fmtNum(m.Output), m.Msgs)
		}
	}
	fmt.Fprintln(w)
}

func printImages(w io.Writer, r *diagnostic.ImageSection) {
	if r == nil {
		return
	}
	fmt.Fprintf(w, "─── IMAGES / SCREENSHOTS ──────────────────────────────\n")
	if r.InlineImages > 0 {
		fmt.Fprintf(w, "  Inline images:     %d (%s tokens)\n", r.InlineImages, fmtNum(r.InlineTokens))
	}
	fmt.Fprintf(w, "  Tool-read images:  %d\n", r.ToolReadImages)
	fmt.Fprintf(w, "  simctl captures:   %d\n", r.SimctlCaptures)
	if r.SipsResizes > 0 {
		fmt.Fprintf(w, "  sips resizes:      %d\n", r.SipsResizes)
	}
	if r.ToolReadImages == 0 && r.InlineImages == 0 {
		fmt.Fprintf(w, "  No images detected.\n\n")
		return
	}
	if r.ToolReadImages > 0 {
		fmt.Fprintf(w, "\n  Avg turns in context: %.1f\n", r.AvgTurnsInCtx)
		fmt.Fprintf(w, "  Total billed tokens:  %s\n", fmtTok(r.TotalBilledTok))
		fmt.Fprintf(w, "  Est. image cost:      $%.2f\n", r.EstImageCost)
	}
	fmt.Fprintln(w)
}

func printCompactions(w io.Writer, c *diagnostic.CompactionSection) {
	if c == nil {
		return
	}
	fmt.Fprintf(w, "─── COMPACTIONS ───────────────────────────────────────\n")
	if c.Count == 0 {
		if c.DetectionCoverage == "none" || c.DetectionCoverage == "partial" {
			fmt.Fprintf(w, "  No compactions detected (coverage: %s).\n\n", c.DetectionCoverage)
		} else {
			fmt.Fprintf(w, "  No compactions detected.\n\n")
		}
		return
	}
	fmt.Fprintf(w, "  Detected:     %d\n", c.Count)
	if c.CascadeCount > 0 {
		fmt.Fprintf(w, "  Cascades:     %d\n", c.CascadeCount)
	}
	fmt.Fprintf(w, "  Per user msg: %.3f\n", c.PerUserMsg)
	fmt.Fprintf(w, "  Last Q rate:  %.3f\n", c.LastQuartileRate)
	fmt.Fprintf(w, "  Coverage:     %s\n", c.DetectionCoverage)
	fmt.Fprintf(w, "  Tokens lost:  %s\n", fmtNum(c.TotalTokensLost))
	if c.IntervalMin > 0 {
		fmt.Fprintf(w, "  Intervals:    min=%d, max=%d, avg=%.0f, med=%d msgs\n",
			c.IntervalMin, c.IntervalMax, c.IntervalAvg, c.IntervalMedian)
	}
	fmt.Fprintln(w)
}

func printCommands(w io.Writer, cmd *diagnostic.CommandSection) {
	if cmd == nil {
		return
	}
	fmt.Fprintf(w, "─── COMMANDS ──────────────────────────────────────────\n")
	fmt.Fprintf(w, "  Total:    %d (%d unique, %.0f%% repeated)\n",
		cmd.TotalCommands, cmd.UniqueCommands, cmd.RepeatedRatio*100)
	fmt.Fprintf(w, "  Output:   %s (%s tokens)\n", fmtBytes(cmd.TotalOutputBytes), fmtTok(cmd.TotalOutputTok))
	if len(cmd.TopByOutput) > 0 && cmd.TopByOutput[0].TotalBytes > 0 {
		fmt.Fprintf(w, "\n  Top by output:\n")
		fmt.Fprintf(w, "  %-20s  %6s  %10s  %10s\n", "COMMAND", "CALLS", "OUTPUT", "EST TOK")
		for _, e := range cmd.TopByOutput {
			if e.TotalBytes == 0 {
				break
			}
			fmt.Fprintf(w, "  %-20s  %6d  %10s  %10s\n",
				truncStr(e.Command, 20), e.Invocations, fmtBytes(e.TotalBytes), fmtTok(e.EstTokens))
		}
	}
	fmt.Fprintln(w)
}

func printToolErrors(w io.Writer, te *diagnostic.ToolErrorSection) {
	if te == nil {
		return
	}
	fmt.Fprintf(w, "─── TOOL ERRORS ───────────────────────────────────────\n")
	fmt.Fprintf(w, "  Total calls:  %d\n", te.TotalToolCalls)
	fmt.Fprintf(w, "  Errors:       %d (%.1f%%)\n", te.ErrorCount, te.ErrorRate*100)
	if te.ConsecutiveMax > 0 {
		fmt.Fprintf(w, "  Max consec.:  %d\n", te.ConsecutiveMax)
	}
	if len(te.ErrorLoops) > 0 {
		fmt.Fprintf(w, "  Error loops:  %d\n", len(te.ErrorLoops))
		for _, l := range te.ErrorLoops {
			fmt.Fprintf(w, "    %s: %d errors at msgs %d-%d\n", l.ToolName, l.ErrorCount, l.StartMsgIdx, l.EndMsgIdx)
		}
	}
	if len(te.TopErrorTools) > 0 {
		fmt.Fprintf(w, "\n  %-20s  %6s  %6s  %7s\n", "TOOL", "CALLS", "ERRS", "RATE")
		for _, t := range te.TopErrorTools {
			fmt.Fprintf(w, "  %-20s  %6d  %6d  %6.1f%%\n", truncStr(t.Name, 20), t.TotalCalls, t.Errors, t.ErrorRate*100)
		}
	}
	fmt.Fprintln(w)
}

func printPatterns(w io.Writer, p *diagnostic.PatternSection) {
	if p == nil {
		return
	}
	fmt.Fprintf(w, "─── BEHAVIORAL PATTERNS ───────────────────────────────\n")
	fmt.Fprintf(w, "  Edit without read:  %d files\n", p.WriteWithoutReadCount)
	fmt.Fprintf(w, "  User corrections:   %d\n", p.UserCorrectionCount)
	fmt.Fprintf(w, "  Glob storms:        %d\n", p.GlobStormCount)
	fmt.Fprintf(w, "  Long runs:          %d (longest: %d msgs)\n", p.LongRunCount, p.LongestRunLength)
	fmt.Fprintln(w)
}

func printProblems(w io.Writer, problems []diagnostic.Problem) {
	fmt.Fprintf(w, "─── DETECTED PROBLEMS ─────────────────────────────────\n")
	if len(problems) == 0 {
		fmt.Fprintf(w, "  No significant problems detected.\n\n")
		return
	}
	fmt.Fprintf(w, "  %d problem(s) detected:\n\n", len(problems))

	sevIcon := map[diagnostic.Severity]string{
		diagnostic.SeverityHigh:   "\U0001F534", // 🔴
		diagnostic.SeverityMedium: "\U0001F7E0", // 🟠
		diagnostic.SeverityLow:    "\U0001F7E1", // 🟡
	}

	for i, p := range problems {
		icon := sevIcon[p.Severity]
		if icon == "" {
			icon = "⚪"
		}
		fmt.Fprintf(w, "  %s %d. [%s] %s\n", icon, i+1, strings.ToUpper(string(p.Severity)), p.Title)
		fmt.Fprintf(w, "     %s\n", p.Observation)
		fmt.Fprintf(w, "     Impact: %s\n\n", p.Impact)
	}
}

func printFixes(w io.Writer, fs *diagnostic.FixSet) {
	fmt.Fprintf(w, "=== GENERATED FIXES ===\n")
	fmt.Fprintf(w, "Session:  %s\n", fs.SessionID)
	fmt.Fprintf(w, "Provider: %s\n\n", fs.Provider)

	if len(fs.Fixes) == 0 {
		fmt.Fprintf(w, "No fixes generated (no problems with available fix generators).\n\n")
		return
	}

	fmt.Fprintf(w, "%d fix(es) generated:\n\n", len(fs.Fixes))

	sevIcon := map[diagnostic.Severity]string{
		diagnostic.SeverityHigh:   "\U0001F534", // 🔴
		diagnostic.SeverityMedium: "\U0001F7E0", // 🟠
		diagnostic.SeverityLow:    "\U0001F7E1", // 🟡
	}

	for i, fix := range fs.Fixes {
		icon := sevIcon[fix.Severity]
		if icon == "" {
			icon = "⚪"
		}
		fmt.Fprintf(w, "%s %d. [%s] %s\n", icon, i+1, strings.ToUpper(string(fix.Severity)), fix.ProblemTitle)
		fmt.Fprintf(w, "   Problem: %s\n", fix.ProblemID)
		fmt.Fprintf(w, "   Artefacts:\n")
		for _, a := range fix.Artefacts {
			action := "CREATE"
			if a.AppendTo {
				action = "APPEND"
			}
			fmt.Fprintf(w, "     [%s] %s \u2192 %s\n", action, a.Kind, a.RelPath)
			fmt.Fprintf(w, "     %s\n", a.Description)
		}
		if len(fix.DocLinks) > 0 {
			fmt.Fprintf(w, "   Learn more:\n")
			for _, dl := range fix.DocLinks {
				fmt.Fprintf(w, "     \u2022 %s: %s\n", dl.Label, dl.URL)
			}
		}
		fmt.Fprintln(w)
	}

	// Show file contents in review mode (not --apply)
	if !fs.Applied {
		fmt.Fprintf(w, "─── ARTEFACT CONTENTS ─────────────────────────────────\n\n")
		for _, fix := range fs.Fixes {
			for _, a := range fix.Artefacts {
				fmt.Fprintf(w, "── %s (%s) ──\n", a.RelPath, a.Kind)
				fmt.Fprintln(w, a.Content)
			}
		}
		fmt.Fprintf(w, "To apply these fixes, re-run with --apply\n")
	}
}

func applyFixes(w io.Writer, fs *diagnostic.FixSet) error {
	fmt.Fprintf(w, "\n─── APPLYING FIXES ────────────────────────────────────\n\n")

	for _, fix := range fs.Fixes {
		for _, a := range fix.Artefacts {
			if a.AppendTo {
				if err := appendToFile(a.RelPath, a.Content); err != nil {
					fmt.Fprintf(w, "  FAIL  %s: %v\n", a.RelPath, err)
					continue
				}
				fmt.Fprintf(w, "  APPEND  %s\n", a.RelPath)
			} else {
				if err := writeFile(a.RelPath, a.Content); err != nil {
					fmt.Fprintf(w, "  FAIL  %s: %v\n", a.RelPath, err)
					continue
				}
				fmt.Fprintf(w, "  CREATE  %s\n", a.RelPath)
			}
		}
	}

	fmt.Fprintf(w, "\nDone. Review the generated files before committing.\n")
	return nil
}

func appendToFile(relPath, content string) error {
	dir := filepath.Dir(relPath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(relPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(content)
	return err
}

func writeFile(relPath, content string) error {
	dir := filepath.Dir(relPath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(relPath, []byte(content), 0o644)
}

// --- Trend ---

// attachTrend computes historical trend comparison and attaches it to the report.
// Requires a non-nil store and at least 3 historical sessions for meaningful comparison.
func attachTrend(report *diagnostic.InspectReport, sess *session.Session, store storage.Store) {
	if store == nil {
		return
	}

	rows, err := store.QueryAnalytics(session.AnalyticsFilter{
		ProjectPath: sess.ProjectPath,
	})
	if err != nil || len(rows) < 3 {
		return
	}

	// Exclude the current session from the baseline.
	var baseline []session.AnalyticsRow
	for _, r := range rows {
		if r.SessionID != sess.ID {
			baseline = append(baseline, r)
		}
	}

	report.Trend = diagnostic.CompareTrend(report, baseline)
}

func printTrend(w io.Writer, tc *diagnostic.TrendComparison) {
	if tc == nil {
		return
	}
	fmt.Fprintf(w, "─── HISTORICAL TREND ──────────────────────────────────\n")
	fmt.Fprintf(w, "  Baseline: %d sessions over %d days\n\n", tc.BaselineSessions, tc.BaselineDays)

	for _, m := range tc.Metrics {
		marker := "  "
		if m.IsAnomaly {
			marker = "\U0001F53A" // 🔺
		}
		fmt.Fprintf(w, "  %s %s\n", marker, m.Narrative)
	}

	if len(tc.Anomalies) > 0 {
		fmt.Fprintf(w, "\n  %d anomaly(ies) detected (significantly above baseline).\n", len(tc.Anomalies))
	}
	fmt.Fprintln(w)
}

// --- Helpers ---

func fmtNum(n int) string {
	if n == 0 {
		return "0"
	}
	s := fmt.Sprintf("%d", n)
	parts := make([]string, 0)
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return strings.Join(parts, ",")
}

func fmtTok(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func fmtBytes(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1f MB", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1f KB", float64(n)/1_000)
	}
	return fmt.Sprintf("%d B", n)
}

func truncStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
