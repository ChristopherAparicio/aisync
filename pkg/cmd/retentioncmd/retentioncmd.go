// Package retentioncmd implements the `aisync retention` CLI command group.
package retentioncmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

type retentionCompactor interface {
	CompactIdleSessions(ctx context.Context, req service.RetentionRequest) (*service.RetentionResult, error)
}

type retentionStatsReader interface {
	RetentionStats(ctx context.Context) (*service.RetentionStats, error)
}

type retentionOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory
	DryRun  bool
	JSON    bool
	Limit   int
}

// NewCmdRetention creates the `aisync retention` command group.
func NewCmdRetention(f *cmdutil.Factory) *cobra.Command {
	opts := &retentionOptions{IO: f.IOStreams, Factory: f}
	cmd := &cobra.Command{
		Use:   "retention",
		Short: "Manage retention compaction and storage statistics",
	}
	cmd.AddCommand(newCmdCompact(opts))
	cmd.AddCommand(newCmdStats(opts))
	return cmd
}

func newCmdCompact(opts *retentionOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compact",
		Short: "Run retention compaction once",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCompact(cmd.Context(), opts)
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Preview compaction without writing changes")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output as JSON")
	cmd.Flags().IntVar(&opts.Limit, "limit", 0, "Maximum sessions to scan per tier")
	return cmd
}

func newCmdStats(opts *retentionOptions) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show retention storage by tier",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.JSON = jsonOutput
			return runStats(cmd.Context(), opts)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func runCompact(ctx context.Context, opts *retentionOptions) error {
	svc, policy, err := retentionServiceAndPolicy(opts.Factory)
	if err != nil {
		return err
	}
	compactor, ok := svc.(retentionCompactor)
	if !ok {
		return fmt.Errorf("configured session service does not support local retention compaction")
	}
	result, err := compactor.CompactIdleSessions(ctx, service.RetentionRequest{Policy: policy, DryRun: opts.DryRun, Limit: opts.Limit})
	if err != nil {
		return err
	}
	if opts.JSON {
		enc := json.NewEncoder(opts.IO.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	if result.DryRun {
		fmt.Fprintln(opts.IO.Out, "Retention compaction dry-run:")
	} else {
		fmt.Fprintln(opts.IO.Out, "Retention compaction complete:")
	}
	fmt.Fprintf(opts.IO.Out, "  Scanned: %d\n", result.Scanned)
	fmt.Fprintf(opts.IO.Out, "  Candidates: %d\n", result.Candidates)
	fmt.Fprintf(opts.IO.Out, "  Compacted: %d\n", result.Compacted)
	fmt.Fprintf(opts.IO.Out, "  Cold candidates: %d\n", result.ColdCandidates)
	fmt.Fprintf(opts.IO.Out, "  Archived: %d\n", result.Archived)
	fmt.Fprintf(opts.IO.Out, "  Skipped: %d\n", result.Skipped)
	fmt.Fprintf(opts.IO.Out, "  Errors: %d\n", result.Errors)
	fmt.Fprintf(opts.IO.Out, "  Bytes: %d -> %d\n", result.BytesBefore, result.BytesAfter)
	return nil
}

func runStats(ctx context.Context, opts *retentionOptions) error {
	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}
	reader, ok := svc.(retentionStatsReader)
	if !ok {
		return fmt.Errorf("configured session service does not support local retention stats")
	}
	stats, err := reader.RetentionStats(ctx)
	if err != nil {
		return err
	}
	if opts.JSON {
		enc := json.NewEncoder(opts.IO.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(stats)
	}
	fmt.Fprintln(opts.IO.Out, "Retention storage:")
	fmt.Fprintf(opts.IO.Out, "  Total sessions: %d\n", stats.TotalSessions)
	fmt.Fprintf(opts.IO.Out, "  Total bytes:    %d\n", stats.TotalBytes)
	for _, tier := range sortedRetentionTiers(stats.ByTier) {
		entry := stats.ByTier[tier]
		fmt.Fprintf(opts.IO.Out, "  %-5s %8d sessions  %10d bytes\n", tier, entry.Sessions, entry.Bytes)
	}
	return nil
}

func retentionServiceAndPolicy(f *cmdutil.Factory) (service.SessionServicer, config.RetentionPolicy, error) {
	svc, err := f.SessionService()
	if err != nil {
		return nil, config.RetentionPolicy{}, fmt.Errorf("initializing service: %w", err)
	}
	cfg, err := f.Config()
	if err != nil {
		return svc, config.RetentionPolicy{}, nil
	}
	return svc, cfg.GetRetentionPolicy(), nil
}

func sortedRetentionTiers(stats map[session.RetentionTier]session.RetentionTierStorageStats) []session.RetentionTier {
	tiers := make([]session.RetentionTier, 0, len(stats))
	for tier := range stats {
		tiers = append(tiers, tier)
	}
	sort.Slice(tiers, func(i, j int) bool { return tiers[i] < tiers[j] })
	return tiers
}
