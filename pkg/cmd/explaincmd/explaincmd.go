// Package explaincmd implements the `aisync explain` CLI command.
// It generates an AI-powered natural language explanation of a session.
package explaincmd

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

// Options holds all inputs for the explain command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	SessionID string
	Model     string
	Short     bool
	JSON      bool
}

// NewCmdExplain creates the `aisync explain` command.
func NewCmdExplain(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "explain <session-id>",
		Short: "AI-explain a captured session",
		Long: `Generate an AI-powered explanation of a captured session.
The explanation covers the goal, approach, files changed, decisions made,
and outcome — written for a developer taking over the branch.

The result is printed to stdout (not stored). Requires an LLM client
(Claude CLI in PATH).

Examples:
  aisync explain abc123                  # detailed explanation
  aisync explain --short abc123          # brief 2-3 sentence summary
  aisync explain --json abc123           # structured JSON output
  aisync explain --model sonnet abc123   # use a specific model`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.SessionID = args[0]
			return runExplain(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Short, "short", false, "Brief 2-3 sentence summary")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Structured JSON output")
	cmd.Flags().StringVar(&opts.Model, "model", "", "LLM model to use (default: adapter default)")

	return cmd
}

func runExplain(opts *Options) error {
	out := opts.IO.Out

	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	sid, err := session.ParseID(opts.SessionID)
	if err != nil {
		return err
	}

	result, err := svc.Explain(context.Background(), service.ExplainRequest{
		SessionID: sid,
		Model:     opts.Model,
		Short:     opts.Short,
	})
	if err != nil {
		return err
	}

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	fmt.Fprintln(out, result.Explanation)
	return nil
}
