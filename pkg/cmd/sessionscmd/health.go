package sessionscmd

import (
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

type healthOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory
	Live    bool
	Days    int
	Limit   int
}

func newCmdHealth(f *cmdutil.Factory) *cobra.Command {
	opts := &healthOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "health",
		Short: "Show stall stats (stuck subagents, aborts, provider errors)",
		Long: `Show health stats for AI sessions — stuck subagents (tools running >15min
without completing), aborted messages, rate-limit hits, and provider errors.

Data comes from the stall_detector scheduled task running every 5 minutes
against your OpenCode database.

Examples:
  aisync sessions health                # last 7 days summary
  aisync sessions health --live         # only currently-stuck sessions
  aisync sessions health --days 30      # last 30 days
  aisync sessions health --limit 50     # show up to 50 recent stalls`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHealth(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Live, "live", false, "show only currently-stuck (unsealed) stalls")
	cmd.Flags().IntVar(&opts.Days, "days", 7, "lookback window in days for sealed stalls")
	cmd.Flags().IntVar(&opts.Limit, "limit", 20, "max stalls to display in the recent list")

	return cmd
}

func runHealth(opts *healthOptions) error {
	out := opts.IO.Out

	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	if opts.Live {
		live, err := store.ListLiveStalls()
		if err != nil {
			return fmt.Errorf("listing live stalls: %w", err)
		}
		now := time.Now()
		fmt.Fprintln(out)
		if len(live) == 0 {
			fmt.Fprintln(out, "  No live stalls. All running tools are within the 15-minute threshold.")
			fmt.Fprintln(out)
			return nil
		}
		fmt.Fprintf(out, "  %d live stalls\n\n", len(live))
		printStallTable(opts, live, now, true)
		return nil
	}

	since := time.Now().Add(-time.Duration(opts.Days) * 24 * time.Hour)
	filter := session.StallFilter{Since: since, Limit: 1000}

	stats, err := store.StallStats(filter)
	if err != nil {
		return fmt.Errorf("computing stats: %w", err)
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "  Sessions Health — last %dd\n\n", opts.Days)
	fmt.Fprintf(out, "    Total stalls : %d\n", stats.TotalCount)
	fmt.Fprintf(out, "    Live now     : %d\n", stats.LiveCount)
	fmt.Fprintf(out, "    Tokens lost  : %d\n", stats.TokensLost)
	fmt.Fprintf(out, "    Cost lost    : %s\n", formatCost(stats.CostLostUSD))
	fmt.Fprintf(out, "    Time stuck   : %.1fh\n", float64(stats.TotalDurationMs)/3600000)
	fmt.Fprintln(out)

	if len(stats.ByRootCause) > 0 {
		fmt.Fprintln(out, "  By root cause:")
		causes := make([]string, 0, len(stats.ByRootCause))
		for c := range stats.ByRootCause {
			causes = append(causes, string(c))
		}
		sort.Slice(causes, func(i, j int) bool {
			return stats.ByRootCause[session.StallRootCause(causes[i])].Count >
				stats.ByRootCause[session.StallRootCause(causes[j])].Count
		})
		for _, c := range causes {
			r := stats.ByRootCause[session.StallRootCause(c)]
			fmt.Fprintf(out, "    %-18s %5d  tokens=%d  cost=%s\n",
				c, r.Count, r.TokensLost, formatCost(r.CostLostUSD))
		}
		fmt.Fprintln(out)
	}

	if len(stats.ByProvider) > 0 {
		fmt.Fprintln(out, "  By provider:")
		provs := make([]string, 0, len(stats.ByProvider))
		for p := range stats.ByProvider {
			provs = append(provs, p)
		}
		sort.Slice(provs, func(i, j int) bool {
			return stats.ByProvider[provs[i]].Count > stats.ByProvider[provs[j]].Count
		})
		for _, p := range provs {
			r := stats.ByProvider[p]
			label := p
			if label == "" {
				label = "(unknown)"
			}
			fmt.Fprintf(out, "    %-18s %5d  tokens=%d  cost=%s\n",
				label, r.Count, r.TokensLost, formatCost(r.CostLostUSD))
		}
		fmt.Fprintln(out)
	}

	recent, err := store.ListStalls(filter)
	if err != nil {
		return fmt.Errorf("listing stalls: %w", err)
	}
	if len(recent) == 0 {
		return nil
	}
	if opts.Limit > 0 && len(recent) > opts.Limit {
		recent = recent[:opts.Limit]
	}
	fmt.Fprintf(out, "  Recent stalls (%d):\n\n", len(recent))
	printStallTable(opts, recent, time.Now(), false)
	return nil
}

func printStallTable(opts *healthOptions, stalls []session.SessionStall, now time.Time, liveOnly bool) {
	out := opts.IO.Out
	if liveOnly {
		fmt.Fprintf(out, "  %-20s %-18s %-22s %-10s %s\n",
			"SESSION", "TOOL", "AGENT", "STUCK FOR", "STARTED")
	} else {
		fmt.Fprintf(out, "  %-20s %-16s %-12s %-10s %10s %10s %s\n",
			"SESSION", "CAUSE", "TOOL", "DURATION", "TOKENS", "COST", "STARTED")
	}
	for _, st := range stalls {
		sid := st.ProviderSessionID
		if len(sid) > 18 {
			sid = sid[:18] + "…"
		}
		var dur time.Duration
		if st.EndedAt != nil {
			dur = st.EndedAt.Sub(st.StartedAt)
		} else {
			dur = now.Sub(st.StartedAt)
		}
		started := st.StartedAt.Format("2006-01-02 15:04")
		if liveOnly {
			fmt.Fprintf(out, "  %-20s %-18s %-22s %-10s %s\n",
				sid, truncate(st.ToolName, 18), truncate(st.Agent, 22),
				humanDur(dur), started)
		} else {
			fmt.Fprintf(out, "  %-20s %-16s %-12s %-10s %10d %10s %s\n",
				sid, string(st.RootCause), truncate(st.ToolName, 12),
				humanDur(dur), st.TokensLost, formatCost(st.CostLostUSD), started)
		}
	}
	fmt.Fprintln(out)
}

func formatCost(c float64) string {
	if c >= 1 {
		return fmt.Sprintf("$%.2f", c)
	}
	if c <= 0 {
		return "$0"
	}
	return fmt.Sprintf("$%.4f", c)
}

func humanDur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%.1fh", d.Hours())
	}
	return fmt.Sprintf("%.1fd", d.Hours()/24)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
