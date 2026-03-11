// Package listcmd implements the `aisync list` CLI command.
package listcmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the list command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	All      bool
	Quiet    bool
	Tree     bool
	OffTopic bool
	PRFlag   int
}

// NewCmdList creates the `aisync list` command.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List captured sessions",
		Long:  "Lists captured sessions for the current branch or all sessions in this project.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.All, "all", false, "Show all sessions in this project")
	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Only print session IDs (one per line)")
	cmd.Flags().IntVar(&opts.PRFlag, "pr", 0, "List sessions linked to this PR number")
	cmd.Flags().BoolVar(&opts.Tree, "tree", false, "Show sessions as a tree (grouped by parent/child forks)")
	cmd.Flags().BoolVar(&opts.OffTopic, "off-topic", false, "Detect sessions with low file overlap on the current branch")

	return cmd
}

func runList(opts *Options) error {
	out := opts.IO.Out

	// Git info
	gitClient, err := opts.Factory.Git()
	if err != nil {
		return fmt.Errorf("not a git repository")
	}

	branch, err := gitClient.CurrentBranch()
	if err != nil {
		return fmt.Errorf("could not determine current branch: %w", err)
	}

	topLevel, err := gitClient.TopLevel()
	if err != nil {
		return fmt.Errorf("could not determine repository root: %w", err)
	}

	// Get service
	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	// List sessions
	summaries, err := svc.List(service.ListRequest{
		ProjectPath: topLevel,
		Branch:      branch,
		PRNumber:    opts.PRFlag,
		All:         opts.All,
	})
	if err != nil {
		return err
	}

	// Off-topic detection mode.
	if opts.OffTopic {
		return runListOffTopic(opts, svc, topLevel, branch)
	}

	// Tree mode: show sessions as parent/child tree.
	if opts.Tree {
		return runListTree(opts, svc, topLevel, branch)
	}

	if len(summaries) == 0 {
		if opts.Quiet {
			return nil
		}
		if opts.All {
			fmt.Fprintln(out, "No sessions found in this project.")
		} else {
			fmt.Fprintf(out, "No sessions found for branch %q.\n", branch)
			fmt.Fprintln(out, "Use --all to see all sessions in this project.")
		}
		return nil
	}

	// Quiet mode: print only session IDs, one per line
	if opts.Quiet {
		for _, s := range summaries {
			fmt.Fprintln(out, s.ID)
		}
		return nil
	}

	// Print header
	fmt.Fprintf(out, "%-12s  %-12s  %-24s  %8s  %8s  %s\n",
		"ID", "PROVIDER", "BRANCH", "MESSAGES", "TOKENS", "CAPTURED")
	fmt.Fprintf(out, "%-12s  %-12s  %-24s  %8s  %8s  %s\n",
		"----", "--------", "------", "--------", "------", "--------")

	for _, s := range summaries {
		id := truncate(string(s.ID), 12)
		prov := truncate(string(s.Provider), 12)
		br := truncate(s.Branch, 24)
		captured := timeAgo(s.CreatedAt)

		fmt.Fprintf(out, "%-12s  %-12s  %-24s  %8d  %8s  %s\n",
			id, prov, br, s.MessageCount, formatTokens(s.TotalTokens), captured)
	}

	return nil
}

func runListOffTopic(opts *Options, svc *service.SessionService, projectPath, branch string) error {
	out := opts.IO.Out

	result, err := svc.DetectOffTopic(context.Background(), service.OffTopicRequest{
		ProjectPath: projectPath,
		Branch:      branch,
	})
	if err != nil {
		return err
	}

	if result.Total == 0 {
		fmt.Fprintf(out, "No sessions found for branch %q.\n", branch)
		return nil
	}

	if result.Total < 2 {
		fmt.Fprintf(out, "Only 1 session on branch %q — off-topic detection needs at least 2.\n", branch)
		return nil
	}

	// Header
	fmt.Fprintf(out, "Off-topic analysis for branch %q (%d sessions, %d off-topic)\n\n", branch, result.Total, result.OffTopic)

	// Session table
	fmt.Fprintf(out, "%-12s  %-12s  %8s  %-8s  %s\n", "ID", "PROVIDER", "OVERLAP", "STATUS", "SUMMARY")
	fmt.Fprintf(out, "%-12s  %-12s  %8s  %-8s  %s\n", "----", "--------", "-------", "------", "-------")

	for _, entry := range result.Sessions {
		id := truncate(string(entry.ID), 12)
		prov := truncate(string(entry.Provider), 12)
		overlap := fmt.Sprintf("%.0f%%", entry.Overlap*100)

		status := "ok"
		if entry.IsOffTopic {
			status = "OFF"
		}

		summary := entry.Summary
		if summary == "" {
			summary = "(no summary)"
		}
		if len(summary) > 40 {
			summary = summary[:37] + "..."
		}

		fmt.Fprintf(out, "%-12s  %-12s  %8s  %-8s  %s\n", id, prov, overlap, status, summary)
	}

	// Top files
	if len(result.TopFiles) > 0 {
		fmt.Fprintf(out, "\nTop files on this branch:\n")
		for _, f := range result.TopFiles {
			fmt.Fprintf(out, "  %s\n", f)
		}
	}

	return nil
}

func runListTree(opts *Options, svc *service.SessionService, projectPath, branch string) error {
	out := opts.IO.Out

	tree, err := svc.ListTree(context.Background(), service.ListRequest{
		ProjectPath: projectPath,
		Branch:      branch,
		All:         opts.All,
	})
	if err != nil {
		return err
	}

	if len(tree) == 0 {
		fmt.Fprintln(out, "No sessions found.")
		return nil
	}

	for i, node := range tree {
		printTreeNode(opts, &node, "", i == len(tree)-1)
	}

	return nil
}

// printTreeNode recursively prints a tree node with indentation.
func printTreeNode(opts *Options, node *session.SessionTreeNode, prefix string, isLast bool) {
	out := opts.IO.Out
	sm := node.Summary

	// Choose the branch character.
	connector := "├── "
	if isLast {
		connector = "└── "
	}
	if prefix == "" {
		connector = "" // root nodes have no connector
	}

	id := truncate(string(sm.ID), 12)
	summary := sm.Summary
	if summary == "" {
		summary = "(no summary)"
	}
	if len(summary) > 40 {
		summary = summary[:37] + "..."
	}

	forkLabel := ""
	if node.IsFork {
		forkLabel = " [fork]"
	}

	tokens := formatTokens(sm.TotalTokens)
	captured := timeAgo(sm.CreatedAt)

	fmt.Fprintf(out, "%s%s%-12s  %-14s  %6s  %s  %s%s\n",
		prefix, connector, id, sm.Provider, tokens, captured, summary, forkLabel)

	// Recurse into children.
	childPrefix := prefix
	if prefix != "" {
		if isLast {
			childPrefix += "    "
		} else {
			childPrefix += "│   "
		}
	}

	for i, child := range node.Children {
		childCopy := child
		printTreeNode(opts, &childCopy, childPrefix, i == len(node.Children)-1)
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

func formatTokens(n int) string {
	if n == 0 {
		return "-"
	}
	if n >= 1000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%d", n)
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// ── Aliases for session types used in display ──

// Summary aliases session.Summary for test accessibility within this package.
type Summary = session.Summary
