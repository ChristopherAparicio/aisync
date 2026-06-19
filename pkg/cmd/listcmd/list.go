// Package listcmd implements the `aisync list` CLI command.
package listcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/auth"
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
	Global   bool   // cross-project: ignore current repo, list/search all projects
	Branch   string // override the auto-detected current branch
	Quiet    bool
	Tree     bool
	OffTopic bool
	PRFlag   int
	User     string // filter by owner ID
	Me       bool   // filter by current authenticated user
	Similar  string // session ID to find similar sessions by file overlap

	// Filters (combinable with any scope).
	Search        string   // FTS5 keyword (matches summary/content/tools)
	SessionType   string   // session_type filter (feature, bug, refactor, exploration, review, devops, other)
	Tags          []string // manual tags filter (AND); pass --tag multiple times
	Provider      string   // provider filter (claude-code, opencode, cursor)
	RemoteURL     string   // case-insensitive substring match on remote_url
	ProjectFilter string   // case-insensitive substring match on project_path
	Since         string   // RFC3339, YYYY-MM-DD, or relative duration (7d, 24h, 1w, 2mo)
	Until         string   // same formats as --since
	Limit         int      // max results (0 = no limit)
	JSON          bool     // machine-readable JSON output
}

// NewCmdList creates the `aisync list` command.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List and search captured sessions",
		Long: `Lists captured sessions for the current branch (default), all sessions in
this project (--all), or across every project (--global). Combine with
--search to filter by keyword (FTS5), --type to filter by classification,
--since/--until to filter by date, etc.

Examples:
  aisync list                              # current branch
  aisync list --all                        # everything in this project
  aisync list --global                     # all sessions across all projects
  aisync list --branch feat/auth           # specific branch
  aisync list --search "OAuth"             # keyword search (FTS5)
  aisync list --search "CV OR resume"      # FTS5 OR
  aisync list --type bug                   # classification filter
  aisync list --since 7d                   # last 7 days (relative)
  aisync list --since 2026-01-01           # absolute date
  aisync list --global --search "deploy" --type devops --since 30d
  aisync list --pr 42                      # sessions linked to PR #42`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(opts)
		},
	}

	// Scope flags
	cmd.Flags().BoolVar(&opts.All, "all", false, "Show all sessions in this project (ignores branch)")
	cmd.Flags().BoolVar(&opts.Global, "global", false, "Show sessions across all projects (ignores current repo)")
	cmd.Flags().StringVar(&opts.Branch, "branch", "", "Override the current branch (e.g. --branch main)")

	// Existing flags
	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Only print session IDs (one per line)")
	cmd.Flags().IntVar(&opts.PRFlag, "pr", 0, "List sessions linked to this PR number")
	cmd.Flags().BoolVar(&opts.Tree, "tree", false, "Show sessions as a tree (grouped by parent/child forks)")
	cmd.Flags().BoolVar(&opts.OffTopic, "off-topic", false, "Detect sessions with low file overlap on the current branch")
	cmd.Flags().StringVar(&opts.User, "user", "", "Filter sessions by owner ID")
	cmd.Flags().BoolVar(&opts.Me, "me", false, "Filter sessions by current authenticated user")
	cmd.Flags().StringVar(&opts.Similar, "similar", "", "Find sessions with similar file changes to this session ID")

	// New filter flags
	cmd.Flags().StringVar(&opts.Search, "search", "", "Full-text search keyword (FTS5, supports OR/AND/phrases)")
	cmd.Flags().StringVar(&opts.SessionType, "type", "", "Filter by session type (feature, bug, refactor, exploration, review, devops, other)")
	cmd.Flags().StringSliceVar(&opts.Tags, "tag", nil, "Filter by manual tag (AND across multiple --tag flags)")
	cmd.Flags().StringVar(&opts.Provider, "provider", "", "Filter by provider (claude-code, opencode, cursor)")
	cmd.Flags().StringVar(&opts.RemoteURL, "remote", "", "Filter by remote URL (case-insensitive substring, e.g. --remote Omogen-ai/monorepo)")
	cmd.Flags().StringVar(&opts.ProjectFilter, "project", "", "Filter by project path (case-insensitive substring, e.g. --project opencode/worktree)")
	cmd.Flags().StringVar(&opts.Since, "since", "", "Only sessions after this date or duration (e.g. 2026-01-01, 7d, 24h, 1w)")
	cmd.Flags().StringVar(&opts.Until, "until", "", "Only sessions before this date or duration")
	cmd.Flags().IntVar(&opts.Limit, "limit", 0, "Max results (0 = no limit, defaults to 50 when --search is used)")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output results as JSON")

	return cmd
}

func runList(opts *Options) error {
	out := opts.IO.Out

	// Resolve scope: project path & branch.
	// --global skips git entirely; otherwise we need the repo to find them.
	var topLevel, branch string
	if !opts.Global {
		gitClient, err := opts.Factory.Git()
		if err != nil {
			return fmt.Errorf("not a git repository (use --global to search across all projects)")
		}

		topLevel, err = gitClient.TopLevel()
		if err != nil {
			return fmt.Errorf("could not determine repository root: %w", err)
		}

		// --branch overrides auto-detection.
		if opts.Branch != "" {
			branch = opts.Branch
		} else {
			branch, err = gitClient.CurrentBranch()
			if err != nil {
				return fmt.Errorf("could not determine current branch: %w", err)
			}
		}
	} else if opts.Branch != "" {
		// --global --branch X: still allow filtering by branch name across projects.
		branch = opts.Branch
	}

	// Get service
	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	// Resolve --me to owner ID via auth token.
	ownerID := opts.User
	if opts.Me {
		if resolved, meErr := resolveMe(opts.Factory); meErr == nil {
			ownerID = resolved
		} else {
			return fmt.Errorf("--me requires authentication: %w", meErr)
		}
	}

	// Validate --type if provided.
	if opts.SessionType != "" && !session.ValidSessionType(opts.SessionType) {
		return fmt.Errorf("invalid --type %q (valid: feature, bug, refactor, exploration, review, devops, other)", opts.SessionType)
	}

	// Off-topic / similar / tree modes preempt the regular list path.
	if opts.OffTopic {
		return runListOffTopic(opts, svc, topLevel, branch)
	}
	if opts.Similar != "" {
		return runListSimilar(opts, svc, topLevel)
	}
	if opts.Tree {
		return runListTree(opts, svc, topLevel, branch)
	}

	// Build the list request honoring all scope/filter flags.
	req := service.ListRequest{
		ProjectPath:   topLevel,
		Branch:        branch,
		OwnerID:       ownerID,
		PRNumber:      opts.PRFlag,
		All:           opts.All,
		Global:        opts.Global,
		Provider:      session.ProviderName(opts.Provider),
		Keyword:       opts.Search,
		SessionType:   opts.SessionType,
		Tags:          opts.Tags,
		RemoteURL:     opts.RemoteURL,
		ProjectFilter: opts.ProjectFilter,
		Since:         opts.Since,
		Until:         opts.Until,
		Limit:         opts.Limit,
	}

	summaries, err := svc.List(req)
	if err != nil {
		return err
	}

	// JSON output: dump and return.
	if opts.JSON {
		return writeJSON(out, summaries)
	}

	if len(summaries) == 0 {
		if opts.Quiet {
			return nil
		}
		writeEmptyMessage(out, opts, branch)
		return nil
	}

	// Quiet mode: print only session IDs, one per line.
	if opts.Quiet {
		for _, s := range summaries {
			fmt.Fprintln(out, s.ID)
		}
		return nil
	}

	// Header banner shows the active scope/filters so the user knows what they got.
	if banner := buildScopeBanner(opts, branch, len(summaries)); banner != "" {
		fmt.Fprintln(out, banner)
	}

	// Show project column when scope is global (it carries useful context),
	// otherwise show the branch column.
	idWidth := maxIDWidth(summaries)
	if opts.Global {
		fmt.Fprintf(out, "%-*s  %-12s  %-30s  %8s  %8s  %s\n",
			idWidth,
			"ID", "PROVIDER", "PROJECT", "MESSAGES", "TOKENS", "CAPTURED")
		fmt.Fprintf(out, "%-*s  %-12s  %-30s  %8s  %8s  %s\n",
			idWidth,
			"----", "--------", "-------", "--------", "------", "--------")
		for _, s := range summaries {
			id := string(s.ID)
			prov := truncate(string(s.Provider), 12)
			proj := truncate(projectDisplayName(opts.Factory, s.RemoteURL, s.ProjectPath), 30)
			fmt.Fprintf(out, "%-*s  %-12s  %-30s  %8d  %8s  %s\n",
				idWidth, id, prov, proj, s.MessageCount, formatTokens(s.TotalTokens), timeAgo(s.CreatedAt))
		}
		return nil
	}

	fmt.Fprintf(out, "%-*s  %-12s  %-24s  %8s  %8s  %s\n",
		idWidth,
		"ID", "PROVIDER", "BRANCH", "MESSAGES", "TOKENS", "CAPTURED")
	fmt.Fprintf(out, "%-*s  %-12s  %-24s  %8s  %8s  %s\n",
		idWidth,
		"----", "--------", "------", "--------", "------", "--------")

	for _, s := range summaries {
		id := string(s.ID)
		prov := truncate(string(s.Provider), 12)
		br := truncate(s.Branch, 24)
		captured := timeAgo(s.CreatedAt)

		fmt.Fprintf(out, "%-*s  %-12s  %-24s  %8d  %8s  %s\n",
			idWidth, id, prov, br, s.MessageCount, formatTokens(s.TotalTokens), captured)
	}

	return nil
}

func maxIDWidth(summaries []session.Summary) int {
	width := len("ID")
	for _, s := range summaries {
		if n := len(string(s.ID)); n > width {
			width = n
		}
	}
	return width
}

// writeEmptyMessage prints a context-aware "no sessions found" message.
func writeEmptyMessage(out io.Writer, opts *Options, branch string) {
	switch {
	case opts.Search != "":
		fmt.Fprintf(out, "No sessions matched %q.\n", opts.Search)
	case opts.Global:
		fmt.Fprintln(out, "No sessions found across any project.")
	case opts.All:
		fmt.Fprintln(out, "No sessions found in this project.")
	default:
		fmt.Fprintf(out, "No sessions found for branch %q.\n", branch)
		fmt.Fprintln(out, "Use --all to see all sessions in this project, or --global for everything.")
	}
}

// buildScopeBanner returns a one-line summary of the active scope and filters,
// shown above the result table when filters are non-trivial. Returns "" when
// the default branch-scoped view is used (to keep the simple case quiet).
func buildScopeBanner(opts *Options, branch string, count int) string {
	var scope string
	switch {
	case opts.Global:
		scope = "all projects"
	case opts.All:
		scope = "this project (all branches)"
	case branch != "":
		scope = fmt.Sprintf("branch %q", branch)
	}

	var filters []string
	if opts.Search != "" {
		filters = append(filters, fmt.Sprintf("search=%q", opts.Search))
	}
	if opts.SessionType != "" {
		filters = append(filters, "type="+opts.SessionType)
	}
	if opts.Provider != "" {
		filters = append(filters, "provider="+opts.Provider)
	}
	if opts.RemoteURL != "" {
		filters = append(filters, "remote="+opts.RemoteURL)
	}
	if opts.ProjectFilter != "" {
		filters = append(filters, "project="+opts.ProjectFilter)
	}
	if opts.Since != "" {
		filters = append(filters, "since="+opts.Since)
	}
	if opts.Until != "" {
		filters = append(filters, "until="+opts.Until)
	}

	// Keep silent when nothing interesting to report (default invocation).
	if len(filters) == 0 && !opts.Global && !opts.All {
		return ""
	}

	if len(filters) > 0 {
		return fmt.Sprintf("# %d session(s) — scope: %s — filters: %s\n",
			count, scope, strings.Join(filters, ", "))
	}
	return fmt.Sprintf("# %d session(s) — scope: %s\n", count, scope)
}

// shortProject returns a compact representation of a project path for table display:
// uses the last 2 path components when the path is long, otherwise the full path.
func shortProject(p string) string {
	if p == "" {
		return "-"
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) <= 2 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

func projectDisplayName(factory *cmdutil.Factory, remoteURL, projectPath string) string {
	if factory == nil {
		return shortProject(projectPath)
	}
	cfg, err := factory.Config()
	if err != nil {
		return shortProject(projectPath)
	}
	display := cfg.ResolveProjectDisplay(remoteURL, projectPath)
	if display.Project == "" {
		return shortProject(projectPath)
	}
	return display.Project
}

// writeJSON emits the summaries as a JSON array.
func writeJSON(out io.Writer, summaries []session.Summary) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(summaries)
}

func runListOffTopic(opts *Options, svc service.SessionServicer, projectPath, branch string) error {
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

// similarEntry holds a session with its computed similarity score.
type similarEntry struct {
	ID         session.ID
	Provider   session.ProviderName
	Similarity float64
	Summary    string
}

func runListSimilar(opts *Options, svc service.SessionServicer, projectPath string) error {
	out := opts.IO.Out

	// Get the target session's file set.
	target, err := svc.Get(opts.Similar)
	if err != nil {
		return fmt.Errorf("session %q not found: %w", opts.Similar, err)
	}

	targetFiles := make(map[string]bool, len(target.FileChanges))
	for _, fc := range target.FileChanges {
		targetFiles[fc.FilePath] = true
	}

	if len(targetFiles) == 0 {
		fmt.Fprintf(out, "Session %s has no file changes — cannot compute similarity.\n", truncate(opts.Similar, 12))
		return nil
	}

	// Get all sessions.
	summaries, err := svc.List(service.ListRequest{ProjectPath: projectPath, All: true})
	if err != nil {
		return err
	}

	// Compute Jaccard similarity for each session.
	var results []similarEntry
	for _, sm := range summaries {
		if sm.ID == target.ID {
			continue // skip self
		}
		sess, getErr := svc.Get(string(sm.ID))
		if getErr != nil {
			continue
		}

		otherFiles := make(map[string]bool, len(sess.FileChanges))
		for _, fc := range sess.FileChanges {
			otherFiles[fc.FilePath] = true
		}
		if len(otherFiles) == 0 {
			continue
		}

		// Jaccard = |intersection| / |union|
		var intersection int
		union := make(map[string]bool)
		for f := range targetFiles {
			union[f] = true
			if otherFiles[f] {
				intersection++
			}
		}
		for f := range otherFiles {
			union[f] = true
		}

		similarity := float64(intersection) / float64(len(union))
		if similarity > 0 {
			results = append(results, similarEntry{
				ID:         sm.ID,
				Provider:   sm.Provider,
				Similarity: similarity,
				Summary:    sm.Summary,
			})
		}
	}

	// Sort by similarity descending.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Similarity > results[j].Similarity
	})

	// Cap at 10.
	if len(results) > 10 {
		results = results[:10]
	}

	if len(results) == 0 {
		fmt.Fprintf(out, "No similar sessions found for %s.\n", truncate(opts.Similar, 12))
		return nil
	}

	fmt.Fprintf(out, "Sessions similar to %s (%d files):\n\n",
		truncate(opts.Similar, 12), len(targetFiles))
	fmt.Fprintf(out, "%-12s  %-12s  %10s  %s\n", "ID", "PROVIDER", "SIMILARITY", "SUMMARY")
	fmt.Fprintf(out, "%-12s  %-12s  %10s  %s\n", "----", "--------", "----------", "-------")

	for _, r := range results {
		summary := r.Summary
		if summary == "" {
			summary = "(no summary)"
		}
		if len(summary) > 40 {
			summary = summary[:37] + "..."
		}
		fmt.Fprintf(out, "%-12s  %-12s  %9.0f%%  %s\n",
			truncate(string(r.ID), 12),
			truncate(string(r.Provider), 12),
			r.Similarity*100,
			summary)
	}

	return nil
}

func runListTree(opts *Options, svc service.SessionServicer, projectPath, branch string) error {
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

// resolveMe extracts the current authenticated user's ID from the stored JWT token.
func resolveMe(f *cmdutil.Factory) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home directory: %w", err)
	}
	configDir := filepath.Join(home, ".aisync")
	token := auth.LoadToken(configDir)
	if token == "" {
		return "", fmt.Errorf("not logged in (no token found in %s)", configDir)
	}
	return auth.ParseTokenUserID(token)
}
