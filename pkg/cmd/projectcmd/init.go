// Package projectcmd — init.go implements `aisync project init`,
// an interactive wizard that auto-detects git settings and configures
// a project in aisync with smart defaults.
package projectcmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/platform"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// initOptions holds all inputs for the project init wizard.
type initOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	// Non-interactive flags
	NoPrompt  bool
	Name      string
	Branch    string
	PREnabled bool
	Budget    float64
	Tags      string
}

func newCmdInit(f *cmdutil.Factory) *cobra.Command {
	opts := &initOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Configure the current project for aisync",
		Long: `Interactive wizard to configure the current directory as an aisync project.

Auto-detects git remote, default branch, and platform. Prompts for
confirmation and optional settings (PR tracking, budget, tags).

Examples:
  aisync project init                                # interactive wizard
  aisync project init --no-prompt                    # accept all defaults
  aisync project init --name myproj --branch main    # override detected values
  aisync project init --pr-enabled --budget 200      # enable PR sync + budget`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.NoPrompt, "no-prompt", false, "Accept all defaults without prompting")
	cmd.Flags().StringVar(&opts.Name, "name", "", "Project name (default: auto-detected from remote)")
	cmd.Flags().StringVar(&opts.Branch, "branch", "", "Default branch (default: auto-detected)")
	cmd.Flags().BoolVar(&opts.PREnabled, "pr-enabled", false, "Enable pull request tracking")
	cmd.Flags().Float64Var(&opts.Budget, "budget", 0, "Monthly budget in USD (0 = none)")
	cmd.Flags().StringVar(&opts.Tags, "tags", "", "Comma-separated tags (e.g. backend,api)")

	return cmd
}

// projectDetection holds auto-detected git information.
type projectDetection struct {
	dir           string
	remoteURL     string // raw remote URL from git
	normalizedURL string // github.com/org/repo
	displayName   string // org/repo
	platformName  string // "GitHub", "GitLab", etc.
	defaultBranch string
	branches      []string
	isGitRepo     bool
}

// detect scans the current directory for git project information.
func detect(f *cmdutil.Factory) projectDetection {
	d := projectDetection{}

	// Current directory
	cwd, err := os.Getwd()
	if err != nil {
		return d
	}
	d.dir = cwd

	// Git client
	gitClient, err := f.Git()
	if err != nil {
		return d
	}

	d.isGitRepo = gitClient.IsRepo()
	if !d.isGitRepo {
		return d
	}

	// Remote URL
	d.remoteURL = gitClient.RemoteURL("origin")
	if d.remoteURL != "" {
		d.normalizedURL = service.NormalizeRemoteURL(d.remoteURL)
		d.displayName = config.RemoteDisplayName(d.remoteURL)

		// Detect platform
		if platName, err := platform.DetectFromRemoteURL(d.remoteURL); err == nil {
			d.platformName = platformDisplayName(string(platName))
		}
	}

	// Default branch
	d.defaultBranch = gitClient.DefaultBranch()

	// Branches
	d.branches = gitClient.ListBranches()

	return d
}

// platformDisplayName converts a platform slug to a display name.
func platformDisplayName(slug string) string {
	switch slug {
	case "github":
		return "GitHub"
	case "gitlab":
		return "GitLab"
	case "bitbucket":
		return "Bitbucket"
	default:
		return slug
	}
}

func runInit(opts *initOptions) error {
	out := opts.IO.Out

	fmt.Fprintln(out)
	fmt.Fprintln(out, "  Scanning project...")
	fmt.Fprintln(out)

	// ── Auto-detect ──
	d := detect(opts.Factory)

	if d.dir == "" {
		return fmt.Errorf("cannot determine current directory")
	}

	// Display detected info
	fmt.Fprintf(out, "  Directory:  %s\n", d.dir)
	if d.remoteURL != "" {
		fmt.Fprintf(out, "  Git remote: %s\n", d.normalizedURL)
	} else if d.isGitRepo {
		fmt.Fprintln(out, "  Git remote: (none)")
	} else {
		fmt.Fprintln(out, "  Git:        not a git repository")
	}

	if d.platformName != "" {
		fmt.Fprintf(out, "  Platform:   %s\n", d.platformName)
	}

	if len(d.branches) > 0 {
		shown := d.branches
		suffix := ""
		if len(shown) > 5 {
			shown = shown[:5]
			suffix = fmt.Sprintf(" ... (%d total)", len(d.branches))
		}
		fmt.Fprintf(out, "  Branches:   %s%s\n", strings.Join(shown, ", "), suffix)
	}

	fmt.Fprintln(out)

	// ── Resolve defaults ──

	// Project name: flag > display name from remote > directory basename
	projectName := opts.Name
	if projectName == "" {
		if d.displayName != "" {
			projectName = d.displayName
		} else {
			projectName = filepath.Base(d.dir)
		}
	}

	// Default branch: flag > detected > ""
	defaultBranch := opts.Branch
	if defaultBranch == "" {
		defaultBranch = d.defaultBranch
	}

	// PR enabled: flag (only meaningful with --no-prompt)
	prEnabled := opts.PREnabled

	// Budget: flag
	budget := opts.Budget

	// Tags: flag
	tags := opts.Tags

	// ── Interactive prompts (unless --no-prompt) ──
	if !opts.NoPrompt {
		scanner := bufio.NewScanner(opts.IO.In)

		projectName = prompt(out, scanner, "Project name", projectName)
		defaultBranch = prompt(out, scanner, "Default branch", defaultBranch)

		// PR tracking — only ask if we detected a known platform
		if d.platformName != "" {
			prEnabled = promptYesNo(out, scanner, "Track pull requests?", true)
		}

		// Budget
		budgetStr := prompt(out, scanner, "Monthly budget USD (0 = none)", "0")
		if v, err := strconv.ParseFloat(budgetStr, 64); err == nil {
			budget = v
		}

		// Tags
		tags = prompt(out, scanner, "Tags (comma-separated)", tags)
	}

	// ── Build config ──
	cfg, err := opts.Factory.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Check if project already exists
	existing := cfg.GetProjectClassifier(d.remoteURL, d.dir)
	var pc config.ProjectClassifierConf
	if existing != nil {
		pc = *existing
	}

	// Apply new values
	pc.DefaultBranch = defaultBranch
	pc.PREnabled = prEnabled
	pc.ProjectPath = d.dir
	if d.normalizedURL != "" {
		pc.GitRemote = d.normalizedURL
	}
	if d.platformName != "" {
		pc.Platform = strings.ToLower(d.platformName)
	}

	if budget > 0 {
		if pc.Budget == nil {
			pc.Budget = &config.ProjectBudgetConf{}
		}
		pc.Budget.MonthlyLimit = budget
	}

	if tags != "" {
		var tagList []string
		for _, t := range strings.Split(tags, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tagList = append(tagList, t)
			}
		}
		if len(tagList) > 0 {
			pc.Tags = tagList
		}
	}

	// ── Save ──
	if err := cfg.SetProjectClassifier(projectName, pc); err != nil {
		return fmt.Errorf("saving project config: %w", err)
	}
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("saving config file: %w", err)
	}

	// ── Summary ──
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  Project configured:")
	fmt.Fprintf(out, "    Name:           %s\n", projectName)
	if defaultBranch != "" {
		fmt.Fprintf(out, "    Default branch: %s\n", defaultBranch)
	}
	if prEnabled {
		fmt.Fprintln(out, "    PR tracking:    enabled")
	} else {
		fmt.Fprintln(out, "    PR tracking:    disabled")
	}
	if d.normalizedURL != "" {
		fmt.Fprintf(out, "    Git remote:     %s\n", d.normalizedURL)
	}
	if budget > 0 {
		fmt.Fprintf(out, "    Budget:         $%.0f/mo\n", budget)
	}
	if tags != "" {
		fmt.Fprintf(out, "    Tags:           %s\n", tags)
	}

	// Session count (best-effort)
	if store, err := opts.Factory.Store(); err == nil {
		if projects, err := store.ListProjects(); err == nil {
			for _, p := range projects {
				if p.ProjectPath == d.dir || p.RemoteURL == d.normalizedURL {
					fmt.Fprintf(out, "    Sessions:       %d already captured\n", p.SessionCount)
					break
				}
			}
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "  Saved to %s\n", cfg.GlobalDir())
	fmt.Fprintln(out)

	return nil
}

// ── Interactive Helpers ──
// These use io.Writer for output (testable) and bufio.Scanner for input.

func prompt(w io.Writer, scanner *bufio.Scanner, question, defaultVal string) string {
	if defaultVal != "" {
		fmt.Fprintf(w, "  ? %s [%s]: ", question, defaultVal)
	} else {
		fmt.Fprintf(w, "  ? %s: ", question)
	}
	scanner.Scan()
	answer := strings.TrimSpace(scanner.Text())
	if answer == "" {
		return defaultVal
	}
	return answer
}

func promptYesNo(w io.Writer, scanner *bufio.Scanner, question string, defaultYes bool) bool {
	suffix := "[Y/n]"
	if !defaultYes {
		suffix = "[y/N]"
	}
	fmt.Fprintf(w, "  ? %s %s: ", question, suffix)
	scanner.Scan()
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if answer == "" {
		return defaultYes
	}
	return answer == "y" || answer == "yes"
}
