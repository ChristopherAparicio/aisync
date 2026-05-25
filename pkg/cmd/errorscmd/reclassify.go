// Package errorscmd: reclassify subcommand.
// Drives internal/scheduler.ReclassifyTask from the CLI so users can
// backfill the error_category column after deterministic patterns or LLM
// classifiers are updated.
package errorscmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/scheduler"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
)

// reclassifyOpts holds flags for the `aisync errors reclassify` command.
type reclassifyOpts struct {
	Factory *cmdutil.Factory

	Limit    int  // batch size per iteration (forwarded to ReclassifyTask)
	All      bool // loop until ListRecentErrors(unknown) returns empty
	MaxLoops int  // safety cap when --all is set
}

// newCmdReclassify builds the `aisync errors reclassify` subcommand.
func newCmdReclassify(f *cmdutil.Factory) *cobra.Command {
	opts := &reclassifyOpts{Factory: f}

	cmd := &cobra.Command{
		Use:   "reclassify",
		Short: "Re-run error classifier on errors stored as 'unknown'",
		Long: `Reclassify previously-captured errors whose category is 'unknown'.

Runs the configured composite classifier (deterministic patterns + fingerprint
+ optional LLM) over batches of unknown errors and updates the error_category
column when a more specific category is matched.

Use this after deploying new deterministic patterns or enabling an LLM
classifier to backfill historical data.

Examples:
  aisync errors reclassify                 # single batch of 100
  aisync errors reclassify --limit 500     # single batch of 500
  aisync errors reclassify --all           # loop until no unknowns remain`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReclassify(cmd.Context(), opts)
		},
	}

	cmd.Flags().IntVar(&opts.Limit, "limit", 100, "Max errors to reclassify per batch")
	cmd.Flags().BoolVar(&opts.All, "all", false, "Loop batches until no unknown errors remain")
	cmd.Flags().IntVar(&opts.MaxLoops, "max-loops", 1000, "Safety cap on iterations when --all is set")

	return cmd
}

// runReclassify executes one or more ReclassifyTask iterations and prints
// before/after counts so the user can see the impact.
func runReclassify(ctx context.Context, opts *reclassifyOpts) error {
	out := opts.Factory.IOStreams.Out

	errSvc, err := opts.Factory.ErrorService()
	if err != nil {
		return fmt.Errorf("initializing error service: %w", err)
	}
	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("initializing store: %w", err)
	}

	// Count unknowns up front (best-effort: pass a high cap so the result
	// approximates the total). ListRecentErrors caps at the limit, so
	// users with millions of errors will only see "at least X".
	const probeCap = 100000
	beforeList, err := store.ListRecentErrors(probeCap, session.ErrorCategoryUnknown)
	if err != nil {
		return fmt.Errorf("counting unknown errors: %w", err)
	}
	fmt.Fprintf(out, "Unknown errors before: %d\n\n", len(beforeList))
	if len(beforeList) == 0 {
		fmt.Fprintln(out, "Nothing to reclassify.")
		return nil
	}

	task := scheduler.NewReclassifyTask(scheduler.ReclassifyConfig{
		ErrorService: errSvc,
		Store:        store,
		Limit:        opts.Limit,
	})

	iterations := 1
	if opts.All {
		iterations = opts.MaxLoops
	}

	var batches int
	for i := 0; i < iterations; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Early-exit probe: only meaningful in --all mode where the loop
		// would otherwise run MaxLoops times. Single-batch callers want
		// exactly one Run() regardless of remaining unknowns.
		if opts.All {
			remaining, listErr := store.ListRecentErrors(1, session.ErrorCategoryUnknown)
			if listErr != nil {
				return fmt.Errorf("listing unknown errors at iter %d: %w", i, listErr)
			}
			if len(remaining) == 0 {
				break
			}
		}

		fmt.Fprintf(out, "Iteration %d: processing up to %d unknown errors...\n", i+1, opts.Limit)
		if err := task.Run(ctx); err != nil {
			return fmt.Errorf("reclassify iteration %d: %w", i+1, err)
		}
		batches++
	}

	afterList, err := store.ListRecentErrors(probeCap, session.ErrorCategoryUnknown)
	if err != nil {
		return fmt.Errorf("recounting unknown errors: %w", err)
	}

	fmt.Fprintf(out, "\nUnknown errors after:  %d\n", len(afterList))
	fmt.Fprintf(out, "Reclassified:          %d\n", len(beforeList)-len(afterList))
	fmt.Fprintf(out, "Batches processed:     %d (each up to %d errors)\n", batches, opts.Limit)
	return nil
}
