package alertscmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

type listOptions struct {
	IO         *iostreams.IOStreams
	Factory    *cmdutil.Factory
	All        bool
	EventTypes []string
	Severities []string
	Project    string
	Limit      int
}

func newCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &listOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List in-app alerts",
		Long: `List notifications recorded by the aisync alerting pipeline.

By default only unacknowledged alerts are shown. Use --all to include
acknowledged history. Filter by event type, severity, or project.

Examples:
  aisync alerts list
  aisync alerts list --all
  aisync alerts list --severity critical --severity warning
  aisync alerts list --event-type stall_alert
  aisync alerts list --project /Users/me/dev/myrepo`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.All, "all", false, "include acknowledged alerts in the output")
	cmd.Flags().StringSliceVar(&opts.EventTypes, "event-type", nil, "filter by event type (repeatable)")
	cmd.Flags().StringSliceVar(&opts.Severities, "severity", nil, "filter by severity: info|warning|critical (repeatable)")
	cmd.Flags().StringVar(&opts.Project, "project", "", "filter by project path")
	cmd.Flags().IntVar(&opts.Limit, "limit", 50, "maximum rows to display (1-1000)")

	return cmd
}

func runList(opts *listOptions) error {
	out := opts.IO.Out

	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}

	filter := session.NotificationLogFilter{
		EventTypes: opts.EventTypes,
		Severities: opts.Severities,
		OnlyUnack:  !opts.All,
		Limit:      limit,
	}
	if opts.Project != "" {
		filter.Projects = []string{opts.Project}
	}

	entries, err := store.ListNotificationLogs(filter)
	if err != nil {
		return fmt.Errorf("listing alerts: %w", err)
	}

	unackCount, err := store.UnacknowledgedNotificationCount()
	if err != nil {
		return fmt.Errorf("counting unacknowledged alerts: %w", err)
	}

	fmt.Fprintln(out)
	if opts.All {
		fmt.Fprintf(out, "  Alerts (%d shown, %d unacknowledged total)\n\n", len(entries), unackCount)
	} else {
		fmt.Fprintf(out, "  Unacknowledged alerts (%d)\n\n", unackCount)
	}

	if len(entries) == 0 {
		if opts.All {
			fmt.Fprintln(out, "  No alerts match the current filter.")
		} else {
			fmt.Fprintln(out, "  You're all caught up — no unacknowledged alerts.")
		}
		fmt.Fprintln(out)
		return nil
	}

	printAlerts(out, entries)
	return nil
}

func printAlerts(out interface{ Write(p []byte) (int, error) }, entries []session.NotificationLogEntry) {
	fmt.Fprintf(out, "  %-6s %-10s %-18s %-7s %-26s %s\n",
		"ID", "SEVERITY", "EVENT", "STATUS", "DISPATCHED", "TITLE")
	for _, e := range entries {
		status := "active"
		if e.IsAcknowledged() {
			status = "acked"
		}
		fmt.Fprintf(out, "  %-6d %-10s %-18s %-7s %-26s %s\n",
			e.ID,
			truncate(e.Severity, 10),
			truncate(e.EventType, 18),
			status,
			e.DispatchedAt.Format(time.RFC3339),
			truncate(e.Title, 80),
		)
		if e.Summary != "" {
			fmt.Fprintf(out, "         %s\n", truncate(e.Summary, 100))
		}
		if e.Project != "" {
			fmt.Fprintf(out, "         project=%s\n", e.Project)
		}
		if e.IsAcknowledged() {
			actor := e.AcknowledgedBy
			if actor == "" {
				actor = "(unknown)"
			}
			fmt.Fprintf(out, "         acknowledged by %s at %s\n", actor, e.AcknowledgedAt.Format(time.RFC3339))
		}
	}
	fmt.Fprintln(out)
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}
