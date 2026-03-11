// Package gccmd implements the `aisync gc` CLI command.
// It removes old sessions based on age and count policies.
package gccmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the gc command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	OlderThan  string
	KeepLatest int
	DryRun     bool
	JSON       bool
}

// NewCmdGC creates the `aisync gc` command.
func NewCmdGC(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Remove old sessions",
		Long: `Garbage-collect sessions that are no longer needed.

Policies:
  --older-than <duration>   Delete sessions older than the given duration (e.g. 30d, 24h, 7d12h)
  --keep-latest <n>         Keep only the N most recent sessions per branch, delete the rest
  --dry-run                 Show what would be deleted without actually deleting

Both --older-than and --keep-latest can be combined. At least one must be specified.

Examples:
  aisync gc --older-than 30d                   # delete sessions older than 30 days
  aisync gc --keep-latest 5                    # keep 5 most recent per branch
  aisync gc --older-than 7d --keep-latest 10   # combine both policies
  aisync gc --older-than 30d --dry-run         # preview what would be deleted
  aisync gc --older-than 14d --json            # JSON output`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGC(opts)
		},
	}

	cmd.Flags().StringVar(&opts.OlderThan, "older-than", "", "Delete sessions older than this duration (e.g. 30d, 24h, 7d12h)")
	cmd.Flags().IntVar(&opts.KeepLatest, "keep-latest", 0, "Keep only the N most recent sessions per branch")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Show what would be deleted without deleting")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output results as JSON")

	return cmd
}

func runGC(opts *Options) error {
	out := opts.IO.Out

	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	result, err := svc.GarbageCollect(context.Background(), service.GCRequest{
		OlderThan:  opts.OlderThan,
		KeepLatest: opts.KeepLatest,
		DryRun:     opts.DryRun,
	})
	if err != nil {
		return err
	}

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	if result.DryRun {
		fmt.Fprintf(out, "Dry run: %d session(s) would be deleted\n", result.Would)
	} else {
		fmt.Fprintf(out, "Deleted %d session(s)\n", result.Deleted)
	}

	return nil
}
