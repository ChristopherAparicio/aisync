package importcmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/provider/claude"
	"github.com/ChristopherAparicio/aisync/internal/provider/opencode"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// DiscoverOptions holds inputs for `aisync import --discover`.
type DiscoverOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	Yes  bool   // non-interactive: import everything
	Mode string // storage mode: full, compact, summary
}

// runDiscover implements the interactive import discovery wizard.
func runDiscover(opts *DiscoverOptions) error {
	out := opts.IO.Out
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Fprintln(out)
	fmt.Fprintln(out, "  aisync import — Discovery")
	fmt.Fprintln(out, "  ~~~~~~~~~~~~~~~~~~~~~~~~~")
	fmt.Fprintln(out)

	// ── Step 1: Scan providers ──
	fmt.Fprintln(out, "  Scanning providers...")

	type providerResult struct {
		name     string
		slug     session.ProviderName
		prov     provider.Provider
		projects []provider.ProjectInfo
		err      error
	}

	ocProv := opencode.New("")
	ccProv := claude.New("")

	var results []providerResult

	// OpenCode
	if disc, ok := provider.Provider(ocProv).(provider.ProjectDiscoverer); ok {
		projects, err := disc.ListAllProjects()
		results = append(results, providerResult{
			name: "OpenCode", slug: session.ProviderOpenCode,
			prov: ocProv, projects: projects, err: err,
		})
	}

	// Claude Code
	if disc, ok := provider.Provider(ccProv).(provider.ProjectDiscoverer); ok {
		projects, err := disc.ListAllProjects()
		results = append(results, providerResult{
			name: "Claude Code", slug: session.ProviderClaudeCode,
			prov: ccProv, projects: projects, err: err,
		})
	}

	totalSessions := 0
	for _, r := range results {
		if r.err != nil || len(r.projects) == 0 {
			fmt.Fprintf(out, "    %s  (not found or empty)\n", r.name)
			continue
		}
		count := 0
		for _, p := range r.projects {
			count += p.SessionCount
		}
		totalSessions += count
		fmt.Fprintf(out, "    %s  %d projects, %d sessions\n", r.name, len(r.projects), count)
	}
	fmt.Fprintln(out)

	if totalSessions == 0 {
		fmt.Fprintln(out, "  No sessions found. Nothing to import.")
		return nil
	}

	// ── Step 2: List projects per provider, let user select ──
	type selectedProject struct {
		providerSlug session.ProviderName
		providerObj  provider.Provider
		project      provider.ProjectInfo
	}
	var selected []selectedProject

	for _, r := range results {
		if r.err != nil || len(r.projects) == 0 {
			continue
		}

		fmt.Fprintf(out, "  ── %s: %d projects ──\n", r.name, len(r.projects))

		for i, proj := range r.projects {
			shortPath := proj.Path
			if home, err := os.UserHomeDir(); err == nil {
				shortPath = strings.Replace(proj.Path, home, "~", 1)
			}

			if opts.Yes {
				// Non-interactive: select all
				selected = append(selected, selectedProject{
					providerSlug: r.slug, providerObj: r.prov, project: proj,
				})
				fmt.Fprintf(out, "    [x] %d) %-45s  %d sessions\n", i+1, shortPath, proj.SessionCount)
			} else {
				fmt.Fprintf(out, "    %d) %-45s  %d sessions\n", i+1, shortPath, proj.SessionCount)
			}
		}

		if !opts.Yes {
			fmt.Fprintln(out)
			fmt.Fprint(out, "  Select projects (comma-separated numbers, or 'all'): ")
			scanner.Scan()
			answer := strings.TrimSpace(scanner.Text())

			if answer == "" || strings.EqualFold(answer, "all") || strings.EqualFold(answer, "a") {
				// Select all
				for _, proj := range r.projects {
					selected = append(selected, selectedProject{
						providerSlug: r.slug, providerObj: r.prov, project: proj,
					})
				}
			} else {
				// Parse comma-separated numbers
				for _, part := range strings.Split(answer, ",") {
					part = strings.TrimSpace(part)
					var idx int
					if _, err := fmt.Sscanf(part, "%d", &idx); err == nil && idx >= 1 && idx <= len(r.projects) {
						proj := r.projects[idx-1]
						selected = append(selected, selectedProject{
							providerSlug: r.slug, providerObj: r.prov, project: proj,
						})
					}
				}
			}
		}
		fmt.Fprintln(out)
	}

	if len(selected) == 0 {
		fmt.Fprintln(out, "  No projects selected. Nothing to import.")
		return nil
	}

	// Count total
	totalToImport := 0
	for _, s := range selected {
		totalToImport += s.project.SessionCount
	}

	if !opts.Yes {
		fmt.Fprintf(out, "  Ready to import %d sessions from %d projects.\n", totalToImport, len(selected))
		fmt.Fprint(out, "  Continue? [Y/n]: ")
		scanner.Scan()
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer != "" && answer != "y" && answer != "yes" {
			fmt.Fprintln(out, "  Cancelled.")
			return nil
		}
	}
	fmt.Fprintln(out)

	// ── Step 3: Import ──
	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	mode := session.StorageMode(opts.Mode)
	if mode == "" {
		mode = session.StorageModeCompact
	}

	totalImported := 0
	totalSkipped := 0
	totalErrors := 0

	for _, sel := range selected {
		shortPath := sel.project.Path
		if home, hErr := os.UserHomeDir(); hErr == nil {
			shortPath = strings.Replace(sel.project.Path, home, "~", 1)
		}

		fmt.Fprintf(out, "  %s (%s)...\n", shortPath, sel.providerSlug)

		// Detect all sessions for this project
		summaries, err := sel.providerObj.Detect(sel.project.Path, "" /* all branches */)
		if err != nil {
			fmt.Fprintf(out, "    Error detecting sessions: %v\n", err)
			totalErrors++
			continue
		}

		imported := 0
		skipped := 0
		errors := 0

		for _, summary := range summaries {
			result, captureErr := svc.Capture(service.CaptureRequest{
				ProjectPath:  sel.project.Path,
				Branch:       summary.Branch,
				Mode:         mode,
				ProviderName: sel.providerSlug,
			})
			if captureErr != nil {
				errors++
				continue
			}
			if result.Skipped {
				skipped++
			} else {
				imported++
			}
		}

		fmt.Fprintf(out, "    %d imported, %d skipped, %d errors\n", imported, skipped, errors)
		totalImported += imported
		totalSkipped += skipped
		totalErrors += errors
	}

	// ── Summary ──
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  Done! %d sessions imported, %d skipped (already in aisync)", totalImported, totalSkipped)
	if totalErrors > 0 {
		fmt.Fprintf(out, ", %d errors", totalErrors)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  Next steps:")
	fmt.Fprintln(out, "    aisync list --all         # see all imported sessions")
	fmt.Fprintln(out, "    aisync web                # open the dashboard")
	fmt.Fprintln(out, "    aisync stats              # view statistics")
	fmt.Fprintln(out)

	return nil
}
