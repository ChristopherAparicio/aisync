// Package itemcmd implements the `aisync items` and `aisync item` CLI commands.
// They report external tracker references (e.g. Notion tickets) linked to AI
// sessions, with the aggregated cost of implementing each one.
package itemcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds inputs shared by the items and item commands.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	Ref         string
	Kind        string
	ProjectPath string
	All         bool
	JSON        bool
}

// NewCmdItems creates the `aisync items [kind]` command.
func NewCmdItems(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{IO: f.IOStreams, Factory: f}

	cmd := &cobra.Command{
		Use:   "items [kind]",
		Short: "List tracker tickets with their aggregated AI cost",
		Long: `List external tracker references (e.g. Notion tickets) linked to AI sessions,
with the number of sessions and the estimated cost of implementing each one.

An optional kind argument filters by work-item kind (feature, bug, ...).
By default only the current project's tickets are shown; use --all for every project.

Examples:
  aisync items                 # all tickets in the current project, sorted by cost
  aisync items bug             # only bugs
  aisync items feature --json  # features, machine-readable
  aisync items --all           # tickets across every project`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.Kind = args[0]
			}
			return runItems(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output results as JSON")
	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "List tickets across all projects (default: current project only)")
	cmd.Flags().StringVar(&opts.ProjectPath, "project", "", "Filter by an explicit project path")

	return cmd
}

// NewCmdItem creates the `aisync item <ref>` command.
func NewCmdItem(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{IO: f.IOStreams, Factory: f}

	cmd := &cobra.Command{
		Use:   "item <ref>",
		Short: "Show one tracker ticket, its sessions and total cost",
		Long: `Show a single external tracker reference: its linked AI sessions, total token
usage and estimated implementation cost.

Examples:
  aisync item OMO-904          # detail for ticket OMO-904
  aisync item OMO-904 --json   # machine-readable`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			opts.Ref = args[0]
			return runItem(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output result as JSON")

	return cmd
}

func runItems(opts *Options) error {
	out := opts.IO.Out

	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	req := service.WorkItemRequest{Kind: opts.Kind}
	switch {
	case opts.ProjectPath != "":
		req.ProjectPath = opts.ProjectPath
	case !opts.All:
		if gitClient, gitErr := opts.Factory.Git(); gitErr == nil {
			req.ProjectPath, _ = gitClient.TopLevel()
		}
	}

	list, err := svc.WorkItems(context.Background(), req)
	if err != nil {
		return err
	}

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(list)
	}

	if len(list.Items) == 0 {
		fmt.Fprintln(out, "No tickets found. Configure a ticket_pattern for this project and run classification.")
		return nil
	}

	fmt.Fprintf(out, "%-10s  %-16s  %-8s  %-10s  %-10s  %s\n", "KIND", "REF", "SESSIONS", "TOKENS", "COST", "LAST ACTIVITY")
	for i := range list.Items {
		it := &list.Items[i]
		fmt.Fprintf(out, "%-10s  %-16s  %-8d  %-10d  $%-9.4f  %s\n",
			it.Kind, it.Ref, it.SessionCount, it.TotalTokens, it.EstimatedCost, formatDate(it.LastActivity))
	}
	fmt.Fprintf(out, "\nTotal: %d ticket(s), %d session(s), $%.4f\n", len(list.Items), list.TotalSessions, list.TotalCost)
	return nil
}

func runItem(opts *Options) error {
	out := opts.IO.Out

	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	item, err := svc.WorkItem(context.Background(), opts.Ref)
	if err != nil {
		return err
	}

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(item)
	}

	fmt.Fprintf(out, "Work item %s  (%s", item.Ref, item.Kind)
	if item.Source != "" {
		fmt.Fprintf(out, ", %s", item.Source)
	}
	fmt.Fprintf(out, ")\n")
	if item.URL != "" {
		fmt.Fprintf(out, "URL: %s\n", item.URL)
	}
	fmt.Fprintf(out, "Sessions: %d | Tokens: %d | Estimated cost: $%.4f\n",
		item.SessionCount, item.TotalTokens, item.EstimatedCost)
	fmt.Fprintf(out, "Activity: %s -> %s\n\n", formatDate(item.FirstActivity), formatDate(item.LastActivity))

	fmt.Fprintf(out, "%-22s  %-10s  %-20s  %-10s  %s\n", "SESSION ID", "PROVIDER", "BRANCH", "TOKENS", "SUMMARY")
	for i := range item.Sessions {
		s := &item.Sessions[i]
		fmt.Fprintf(out, "%-22s  %-10s  %-20s  %-10d  %s\n",
			s.ID, s.Provider, truncate(s.Branch, 20), s.TotalTokens, truncate(s.Summary, 50))
	}
	return nil
}

func formatDate(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
