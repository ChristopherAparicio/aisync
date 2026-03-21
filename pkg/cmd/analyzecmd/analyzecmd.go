// Package analyzecmd implements the `aisync analyze` CLI command.
// It runs a session analysis using the configured analyzer adapter (LLM or OpenCode)
// and persists the results for later retrieval.
package analyzecmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the analyze command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	SessionID string
	JSON      bool
}

// NewCmdAnalyze creates the `aisync analyze` command.
func NewCmdAnalyze(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "analyze <session-id>",
		Short: "Analyze session quality and suggest improvements",
		Long: `Run an AI-powered analysis of a captured session.
Detects problems (retry loops, error cascades, wasted tokens), produces
an efficiency score (0-100), and suggests improvements including new skills.

The analysis is persisted and can be viewed later with "aisync show".

Configure the analyzer adapter via "aisync config set analysis.adapter llm|opencode".

Examples:
  aisync analyze abc123              # text report
  aisync analyze --json abc123       # structured JSON output`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.SessionID = args[0]
			return runAnalyze(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Structured JSON output")

	return cmd
}

func runAnalyze(opts *Options) error {
	out := opts.IO.Out

	analysisSvc, err := opts.Factory.AnalysisService()
	if err != nil {
		return fmt.Errorf("initializing analysis service: %w", err)
	}

	sid, err := session.ParseID(opts.SessionID)
	if err != nil {
		return err
	}

	// Get config for thresholds
	cfg, cfgErr := opts.Factory.Config()
	var errorThreshold float64 = 20
	var minToolCalls int = 5
	if cfgErr == nil {
		errorThreshold = cfg.GetAnalysisErrorThreshold()
		minToolCalls = cfg.GetAnalysisMinToolCalls()
	}

	// Optionally load capabilities for context
	var caps []struct{} // placeholder — would use RegistryService
	_ = caps

	fmt.Fprintf(out, "Analyzing session %s...\n", sid)

	result, err := analysisSvc.Analyze(context.Background(), service.AnalysisRequest{
		SessionID:      sid,
		Trigger:        analysis.TriggerManual,
		ErrorThreshold: errorThreshold,
		MinToolCalls:   minToolCalls,
	})
	if err != nil {
		return fmt.Errorf("analysis failed: %w", err)
	}

	sa := result.Analysis

	if !sa.OK() {
		fmt.Fprintf(out, "\nAnalysis completed with error: %s\n", sa.Error)
		return nil
	}

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(sa)
	}

	r := sa.Report

	fmt.Fprintf(out, "\n=== Session Analysis ===\n")
	fmt.Fprintf(out, "  Score: %d/100\n\n", r.Score)
	fmt.Fprintf(out, "%s\n\n", r.Summary)

	if len(r.Problems) > 0 {
		fmt.Fprintln(out, "Problems:")
		for _, p := range r.Problems {
			icon := "!"
			switch p.Severity {
			case analysis.SeverityHigh:
				icon = "!!!"
			case analysis.SeverityMedium:
				icon = "!!"
			}
			fmt.Fprintf(out, "  [%s] %s", icon, p.Description)
			if p.ToolName != "" {
				fmt.Fprintf(out, " (tool: %s)", p.ToolName)
			}
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out)
	}

	if len(r.Recommendations) > 0 {
		fmt.Fprintln(out, "Recommendations:")
		for _, rec := range r.Recommendations {
			fmt.Fprintf(out, "  [%s] %s\n", rec.Category, rec.Title)
			fmt.Fprintf(out, "        %s\n", rec.Description)
		}
		fmt.Fprintln(out)
	}

	if len(r.SkillSuggestions) > 0 {
		fmt.Fprintln(out, "Suggested skills:")
		for _, sk := range r.SkillSuggestions {
			fmt.Fprintf(out, "  %s: %s\n", sk.Name, sk.Description)
			if sk.Trigger != "" {
				fmt.Fprintf(out, "    trigger: %s\n", sk.Trigger)
			}
		}
		fmt.Fprintln(out)
	}

	fmt.Fprintf(out, "Adapter: %s | Duration: %dms\n", sa.Adapter, sa.DurationMs)
	if sa.Model != "" {
		fmt.Fprintf(out, "Model: %s\n", sa.Model)
	}

	return nil
}
