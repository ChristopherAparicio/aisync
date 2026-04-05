package projectcmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

type listOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory
}

func newCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &listOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	return &cobra.Command{
		Use:   "list",
		Short: "List all known projects",
		Long: `List all projects known to aisync — both configured (via project init)
and unconfigured (detected from captured sessions).

Example:
  aisync project list`,
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(opts)
		},
	}
}

// mergedProject combines DB-discovered and config-based project info.
type mergedProject struct {
	Name         string
	SessionCount int
	Branch       string
	PREnabled    bool
	Budget       float64
	Configured   bool
}

func runList(opts *listOptions) error {
	out := opts.IO.Out

	cfg, err := opts.Factory.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Get configured projects from config
	classifiers := cfg.GetAllProjectClassifiers()

	// Get projects from DB (sessions)
	var dbProjects []session.ProjectGroup
	if store, err := opts.Factory.Store(); err == nil {
		dbProjects, _ = store.ListProjects()
	}

	// Merge: start with configured projects
	projects := make(map[string]*mergedProject)
	for name, pc := range classifiers {
		projects[name] = &mergedProject{
			Name:       name,
			Branch:     pc.DefaultBranch,
			PREnabled:  pc.PREnabled,
			Budget:     budgetAmount(pc.Budget),
			Configured: true,
		}
	}

	// Add/enrich from DB
	for _, pg := range dbProjects {
		key := findProjectKey(pg, classifiers)
		if mp, ok := projects[key]; ok {
			// Enrich existing
			mp.SessionCount = pg.SessionCount
		} else {
			// New from DB (unconfigured)
			name := pg.DisplayName
			if name == "" {
				name = pg.ProjectPath
			}
			projects[name] = &mergedProject{
				Name:         name,
				SessionCount: pg.SessionCount,
				Configured:   false,
			}
		}
	}

	if len(projects) == 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  No projects found.")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Get started:")
		fmt.Fprintln(out, "    cd /path/to/your/project")
		fmt.Fprintln(out, "    aisync project init")
		fmt.Fprintln(out)
		return nil
	}

	// Render table
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  %-35s %8s  %-8s %-4s %-10s %s\n",
		"PROJECT", "SESSIONS", "BRANCH", "PRS", "BUDGET", "STATUS")

	configuredCount := 0
	unconfiguredCount := 0
	for _, mp := range sortedProjects(projects) {
		sessions := fmt.Sprintf("%d", mp.SessionCount)
		branch := mp.Branch
		if branch == "" {
			branch = "—"
		}
		prs := "—"
		if mp.PREnabled {
			prs = "✓"
		}
		budgetStr := "—"
		if mp.Budget > 0 {
			budgetStr = fmt.Sprintf("$%.0f/mo", mp.Budget)
		}
		status := "unconfigured"
		if mp.Configured {
			status = "configured"
			configuredCount++
		} else {
			unconfiguredCount++
		}

		fmt.Fprintf(out, "  %-35s %8s  %-8s %-4s %-10s %s\n",
			truncate(mp.Name, 35), sessions, truncate(branch, 8), prs, budgetStr, status)
	}

	fmt.Fprintln(out)
	total := configuredCount + unconfiguredCount
	fmt.Fprintf(out, "  %d projects (%d configured, %d unconfigured)\n",
		total, configuredCount, unconfiguredCount)

	if unconfiguredCount > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Tip: cd into an unconfigured project and run `aisync project init`")
	}
	fmt.Fprintln(out)

	return nil
}

// findProjectKey finds the config key that matches a DB project group.
func findProjectKey(pg session.ProjectGroup, classifiers map[string]config.ProjectClassifierConf) string {
	// Try display name
	if _, ok := classifiers[pg.DisplayName]; ok {
		return pg.DisplayName
	}
	// Try remote URL
	if _, ok := classifiers[pg.RemoteURL]; ok {
		return pg.RemoteURL
	}
	// Try project path
	if _, ok := classifiers[pg.ProjectPath]; ok {
		return pg.ProjectPath
	}
	// Try matching by git_remote or project_path fields in classifiers
	for name, pc := range classifiers {
		if pc.GitRemote != "" && pc.GitRemote == pg.RemoteURL {
			return name
		}
		if pc.ProjectPath != "" && pc.ProjectPath == pg.ProjectPath {
			return name
		}
	}
	return pg.DisplayName
}

func budgetAmount(b *config.ProjectBudgetConf) float64 {
	if b == nil {
		return 0
	}
	return b.MonthlyLimit
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// sortedProjects returns projects sorted: configured first, then by name.
func sortedProjects(m map[string]*mergedProject) []*mergedProject {
	var configured, unconfigured []*mergedProject
	for _, mp := range m {
		if mp.Configured {
			configured = append(configured, mp)
		} else {
			unconfigured = append(unconfigured, mp)
		}
	}

	// Sort each group by name
	sortByName(configured)
	sortByName(unconfigured)

	return append(configured, unconfigured...)
}

func sortByName(ps []*mergedProject) {
	for i := 1; i < len(ps); i++ {
		for j := i; j > 0 && strings.ToLower(ps[j].Name) < strings.ToLower(ps[j-1].Name); j-- {
			ps[j], ps[j-1] = ps[j-1], ps[j]
		}
	}
}
