// Package recommendcmd implements the `aisync recommend` CLI command.
//
// It exposes the fully offline, deterministic GenerateRecommendations()
// engine (from internal/service) plus the ListRecommendations() store
// reader. The default mode reads pre-computed recommendations from the
// store (fast — populated by the scheduler cron); --fresh re-runs the
// full analysis on demand (expensive: AgentROI + SkillROI + CacheEff +
// ContextSaturation).
//
// No LLM is ever called. Everything is rule-based on stored metrics,
// so this command is safe for air-gapped / offline usage and can be
// invoked by an external LLM agent via shell to surface actionable
// insights without spending a single input token of its own.
package recommendcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the recommend command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	ProjectPath string
	Priority    string // empty = all, "high", "medium", "low"
	Status      string // "active" (default), "dismissed", "snoozed", "all"
	Limit       int
	Fresh       bool // if true, regenerate via GenerateRecommendations (expensive)
	JSON        bool
	Quiet       bool // one-liner per recommendation
}

// NewCmdRecommend creates the `aisync recommend` command.
func NewCmdRecommend(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "recommend",
		Short: "List actionable recommendations for a project (100% offline, no LLM)",
		Long: `Produces deterministic, rule-based recommendations derived from stored
session metrics. No LLM is called — the engine mines data you already
captured (agent ROI, skill ROI, cache efficiency, context saturation,
token waste, model fitness, freshness, system prompt impact, budgets).

By default, recommendations are read from the store (populated by the
scheduler cron — cheap, instant). Use --fresh to regenerate on demand
(expensive: triggers the full analytics pipeline).

Every recommendation is an OBSERVATION — it tells you what was detected,
not what a model thinks you should do. Use 'aisync inspect --generate-fix'
if you want to materialize provider-specific fixes for a given session.

Examples:
  aisync recommend                               # stored recs, all projects
  aisync recommend --project /path/to/repo       # filtered by project
  aisync recommend --priority high               # only high-priority items
  aisync recommend --fresh --project /repo       # regenerate on the fly
  aisync recommend --json --project /repo        # machine-readable
  aisync recommend --quiet                       # compact one-liner view`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRecommend(opts)
		},
	}

	cmd.Flags().StringVar(&opts.ProjectPath, "project", "", "Filter by project path (absolute)")
	cmd.Flags().StringVar(&opts.Priority, "priority", "", "Filter by priority: high, medium, low")
	cmd.Flags().StringVar(&opts.Status, "status", "active", "Filter by status: active, dismissed, snoozed, all (ignored with --fresh)")
	cmd.Flags().IntVar(&opts.Limit, "limit", 50, "Maximum recommendations to return")
	cmd.Flags().BoolVar(&opts.Fresh, "fresh", false, "Regenerate recommendations via GenerateRecommendations (expensive)")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Structured JSON output")
	cmd.Flags().BoolVar(&opts.Quiet, "quiet", false, "Compact one-liner per recommendation")

	return cmd
}

func runRecommend(opts *Options) error {
	out := opts.IO.Out

	if opts.Fresh {
		return runFresh(opts, out)
	}
	return runStored(opts, out)
}

// runFresh calls GenerateRecommendations() on the service. This is the
// expensive path — triggers AgentROI + SkillROI + CacheEfficiency +
// ContextSaturation + BudgetStatus over the last 90 days.
func runFresh(opts *Options, out io.Writer) error {
	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	recs, err := svc.GenerateRecommendations(context.Background(), opts.ProjectPath)
	if err != nil {
		return fmt.Errorf("generating recommendations: %w", err)
	}

	// Filter by priority if requested.
	if opts.Priority != "" {
		recs = filterByPriority(recs, opts.Priority)
	}

	// Apply limit.
	if opts.Limit > 0 && len(recs) > opts.Limit {
		recs = recs[:opts.Limit]
	}

	if opts.JSON {
		return writeJSON(out, map[string]any{
			"source":          "fresh",
			"project_path":    opts.ProjectPath,
			"total":           len(recs),
			"recommendations": recs,
		})
	}

	return renderFresh(out, recs, opts)
}

// runStored reads recommendations from the store — cheap, instant.
// This is the default path, matching what the web UI reads.
func runStored(opts *Options, out io.Writer) error {
	store, err := opts.Factory.Store()
	if err != nil || store == nil {
		return fmt.Errorf("store unavailable: %w", err)
	}

	filter := session.RecommendationFilter{
		ProjectPath: opts.ProjectPath,
		Priority:    opts.Priority,
		Limit:       opts.Limit,
	}
	if opts.Status != "all" {
		filter.Status = session.RecommendationStatus(opts.Status)
	}

	recs, err := store.ListRecommendations(filter)
	if err != nil {
		return fmt.Errorf("listing recommendations: %w", err)
	}

	// Stats alongside the list (only for the requested project).
	stats, _ := store.RecommendationStats(opts.ProjectPath)

	if opts.JSON {
		return writeJSON(out, map[string]any{
			"source":          "stored",
			"project_path":    opts.ProjectPath,
			"filter_status":   opts.Status,
			"filter_priority": opts.Priority,
			"total":           len(recs),
			"stats":           stats,
			"recommendations": recs,
		})
	}

	return renderStored(out, recs, stats, opts)
}

// --- Rendering ---

func renderFresh(out io.Writer, recs []session.Recommendation, opts *Options) error {
	if len(recs) == 0 {
		fmt.Fprintln(out, "No recommendations detected.")
		return nil
	}

	if opts.Quiet {
		for _, r := range recs {
			fmt.Fprintf(out, "[%s] %s %s\n", strings.ToUpper(r.Priority), r.Icon, r.Title)
		}
		return nil
	}

	fmt.Fprintf(out, "=== RECOMMENDATIONS (fresh) ===\n")
	if opts.ProjectPath != "" {
		fmt.Fprintf(out, "Project: %s\n", opts.ProjectPath)
	}
	fmt.Fprintf(out, "Count:   %d\n\n", len(recs))

	for i, r := range recs {
		fmt.Fprintf(out, "%d. %s [%s] %s\n", i+1, r.Icon, strings.ToUpper(r.Priority), r.Title)
		if r.Message != "" {
			fmt.Fprintf(out, "   %s\n", r.Message)
		}
		meta := buildMetaLine(r.Type, r.Agent, r.Skill, r.Impact)
		if meta != "" {
			fmt.Fprintf(out, "   %s\n", meta)
		}
		fmt.Fprintln(out)
	}
	return nil
}

func renderStored(out io.Writer, recs []session.RecommendationRecord, stats session.RecommendationStats, opts *Options) error {
	if len(recs) == 0 {
		fmt.Fprintln(out, "No recommendations found. Run with --fresh to regenerate.")
		return nil
	}

	if opts.Quiet {
		for _, r := range recs {
			fmt.Fprintf(out, "[%s] %s %s\n", strings.ToUpper(r.Priority), r.Icon, r.Title)
		}
		return nil
	}

	fmt.Fprintf(out, "=== RECOMMENDATIONS (stored) ===\n")
	if opts.ProjectPath != "" {
		fmt.Fprintf(out, "Project: %s\n", opts.ProjectPath)
	}
	fmt.Fprintf(out, "Stats:   active=%d  dismissed=%d  snoozed=%d  total=%d\n",
		stats.Active, stats.Dismissed, stats.Snoozed, stats.Total)
	fmt.Fprintf(out, "Showing: %d\n\n", len(recs))

	for i, r := range recs {
		fmt.Fprintf(out, "%d. %s [%s] %s\n", i+1, r.Icon, strings.ToUpper(r.Priority), r.Title)
		if r.Message != "" {
			fmt.Fprintf(out, "   %s\n", r.Message)
		}
		meta := buildMetaLine(r.Type, r.Agent, r.Skill, r.Impact)
		if meta != "" {
			fmt.Fprintf(out, "   %s\n", meta)
		}
		fmt.Fprintf(out, "   id=%s  source=%s  created=%s\n",
			r.ID, r.Source, r.CreatedAt.Format("2006-01-02"))
		fmt.Fprintln(out)
	}
	return nil
}

func buildMetaLine(recType, agent, skill, impact string) string {
	var parts []string
	if recType != "" {
		parts = append(parts, fmt.Sprintf("type=%s", recType))
	}
	if agent != "" {
		parts = append(parts, fmt.Sprintf("agent=%s", agent))
	}
	if skill != "" {
		parts = append(parts, fmt.Sprintf("skill=%s", skill))
	}
	if impact != "" {
		parts = append(parts, fmt.Sprintf("impact=%q", impact))
	}
	return strings.Join(parts, "  ")
}

// --- Helpers ---

func filterByPriority(recs []session.Recommendation, priority string) []session.Recommendation {
	var filtered []session.Recommendation
	for _, r := range recs {
		if r.Priority == priority {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func writeJSON(out io.Writer, payload any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}
