// Package efficiencycmd implements the `aisync efficiency` CLI command.
// It generates an LLM-powered efficiency analysis of a captured session.
package efficiencycmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the efficiency command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	SessionID string
	Model     string
	JSON      bool
}

// NewCmdEfficiency creates the `aisync efficiency` command.
func NewCmdEfficiency(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "efficiency <session-id>",
		Short: "AI-analyze session efficiency",
		Long: `Generate an LLM-powered efficiency analysis of a captured session.
Evaluates token waste, tool usage patterns, retry loops, and provides
an efficiency score (0-100) with actionable suggestions.

Requires an LLM client (Claude CLI in PATH).

Examples:
  aisync efficiency abc123                  # text report
  aisync efficiency --json abc123           # structured JSON
  aisync efficiency --model sonnet abc123   # use specific model`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.SessionID = args[0]
			return runEfficiency(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Structured JSON output")
	cmd.Flags().StringVar(&opts.Model, "model", "", "LLM model to use (default: adapter default)")

	return cmd
}

func runEfficiency(opts *Options) error {
	out := opts.IO.Out

	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	sid, err := session.ParseID(opts.SessionID)
	if err != nil {
		return err
	}

	result, err := svc.AnalyzeEfficiency(context.Background(), service.EfficiencyRequest{
		SessionID: sid,
		Model:     opts.Model,
	})
	if err != nil {
		return err
	}

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	r := result.Report

	fmt.Fprintf(out, "=== Efficiency Report ===\n")
	fmt.Fprintf(out, "  Score: %d/100\n\n", r.Score)
	fmt.Fprintf(out, "%s\n\n", r.Summary)

	if len(r.Strengths) > 0 {
		fmt.Fprintln(out, "Strengths:")
		for _, s := range r.Strengths {
			fmt.Fprintf(out, "  ✓ %s\n", s)
		}
		fmt.Fprintln(out)
	}

	if len(r.Issues) > 0 {
		fmt.Fprintln(out, "Issues:")
		for _, s := range r.Issues {
			fmt.Fprintf(out, "  ✗ %s\n", s)
		}
		fmt.Fprintln(out)
	}

	if len(r.Suggestions) > 0 {
		fmt.Fprintln(out, "Suggestions:")
		for _, s := range r.Suggestions {
			fmt.Fprintf(out, "  → %s\n", s)
		}
		fmt.Fprintln(out)
	}

	if len(r.Patterns) > 0 {
		fmt.Fprintln(out, "Detected patterns:")
		for _, s := range r.Patterns {
			fmt.Fprintf(out, "  • %s\n", s)
		}
		fmt.Fprintln(out)
	}

	fmt.Fprintf(out, "Model: %s | Tokens used: %d\n", result.Model, result.TokensUsed)

	return nil
}
