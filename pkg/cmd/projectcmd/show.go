package projectcmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

type showOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	Name string // explicit project name (empty = detect from cwd)
}

func newCmdShow(f *cmdutil.Factory) *cobra.Command {
	opts := &showOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "show [name]",
		Short: "Show configuration for a project",
		Long: `Display the full configuration for a project.

If no name is given, detects the project from the current directory.

Examples:
  aisync project show                # current directory
  aisync project show cycloplan      # by name`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.Name = args[0]
			}
			return runShow(opts)
		},
	}

	return cmd
}

func runShow(opts *showOptions) error {
	out := opts.IO.Out

	cfg, err := opts.Factory.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// ── Find project ──
	var projectName string
	var remoteURL, projectPath string

	if opts.Name != "" {
		// Explicit name
		projectName = opts.Name
	} else {
		// Detect from current directory
		d := detect(opts.Factory)
		remoteURL = d.remoteURL
		projectPath = d.dir
		if d.displayName != "" {
			projectName = d.displayName
		}
	}

	// Look up in config
	classifiers := cfg.GetAllProjectClassifiers()
	var foundName string
	var found bool

	// Try exact name match
	if projectName != "" {
		if _, ok := classifiers[projectName]; ok {
			foundName = projectName
			found = true
		}
	}

	// Try by remote URL or path
	if !found && (remoteURL != "" || projectPath != "") {
		pc := cfg.GetProjectClassifier(remoteURL, projectPath)
		if pc != nil {
			// Find the key
			for name, c := range classifiers {
				if c.DefaultBranch == pc.DefaultBranch &&
					c.GitRemote == pc.GitRemote &&
					c.ProjectPath == pc.ProjectPath {
					foundName = name
					found = true
					break
				}
			}
		}
	}

	if !found {
		fmt.Fprintln(out)
		if projectName != "" {
			fmt.Fprintf(out, "  Project %q is not configured.\n", projectName)
		} else {
			cwd, _ := os.Getwd()
			fmt.Fprintf(out, "  No project configured for %s\n", cwd)
		}
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Run `aisync project init` to configure it.")
		fmt.Fprintln(out)
		return nil
	}

	pc := classifiers[foundName]

	// ── Display ──
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  Project: %s\n", foundName)

	if pc.ProjectPath != "" {
		fmt.Fprintf(out, "  Path:    %s\n", pc.ProjectPath)
	}
	if pc.GitRemote != "" {
		fmt.Fprintf(out, "  Remote:  %s\n", pc.GitRemote)
	}
	if pc.Platform != "" {
		fmt.Fprintf(out, "  Platform: %s\n", platformDisplayName(pc.Platform))
	}

	fmt.Fprintln(out)

	if pc.DefaultBranch != "" {
		fmt.Fprintf(out, "  Branch:     %s\n", pc.DefaultBranch)
	}
	if pc.PREnabled {
		fmt.Fprintln(out, "  PR sync:    enabled")
	} else {
		fmt.Fprintln(out, "  PR sync:    disabled")
	}

	// Session count (best-effort)
	if store, err := opts.Factory.Store(); err == nil {
		if projects, err := store.ListProjects(); err == nil {
			for _, p := range projects {
				if p.ProjectPath == pc.ProjectPath || p.RemoteURL == pc.GitRemote {
					fmt.Fprintf(out, "  Sessions:   %d\n", p.SessionCount)
					break
				}
			}
		}
	}

	// Budget
	if pc.Budget != nil && pc.Budget.MonthlyLimit > 0 {
		fmt.Fprintf(out, "  Budget:     $%.0f/mo\n", pc.Budget.MonthlyLimit)
		if pc.Budget.DailyLimit > 0 {
			fmt.Fprintf(out, "              $%.0f/day limit\n", pc.Budget.DailyLimit)
		}
		if pc.Budget.AlertAtPercent > 0 {
			fmt.Fprintf(out, "              Alert at %.0f%%\n", pc.Budget.AlertAtPercent)
		}
	}

	fmt.Fprintln(out)

	// Rules section
	hasRules := false

	if pc.TicketPattern != "" {
		if !hasRules {
			fmt.Fprintln(out, "  Rules:")
			hasRules = true
		}
		source := pc.TicketSource
		if source == "" {
			source = "unknown"
		}
		fmt.Fprintf(out, "    Tickets: %s (%s)\n", pc.TicketPattern, source)
		if pc.TicketURL != "" {
			fmt.Fprintf(out, "             URL: %s\n", pc.TicketURL)
		}
	}

	if len(pc.BranchRules) > 0 {
		if !hasRules {
			fmt.Fprintln(out, "  Rules:")
			hasRules = true
		}
		var rules []string
		for pattern, typ := range pc.BranchRules {
			rules = append(rules, fmt.Sprintf("%s → %s", pattern, typ))
		}
		fmt.Fprintf(out, "    Branch: %s\n", strings.Join(rules, ", "))
	}

	if len(pc.Tags) > 0 {
		if !hasRules {
			fmt.Fprintln(out, "  Rules:")
			hasRules = true
		}
		fmt.Fprintf(out, "    Tags: %s\n", strings.Join(pc.Tags, ", "))
	}

	if len(pc.AgentRules) > 0 {
		if !hasRules {
			fmt.Fprintln(out, "  Rules:")
			hasRules = true
		}
		var rules []string
		for agent, typ := range pc.AgentRules {
			rules = append(rules, fmt.Sprintf("%s → %s", agent, typ))
		}
		fmt.Fprintf(out, "    Agent: %s\n", strings.Join(rules, ", "))
	}

	if hasRules {
		fmt.Fprintln(out)
	}

	return nil
}
