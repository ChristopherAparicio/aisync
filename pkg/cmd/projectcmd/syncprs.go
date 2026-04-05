package projectcmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/platform/github"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

type syncPRsOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory
}

func newCmdSyncPRs(f *cmdutil.Factory) *cobra.Command {
	opts := &syncPRsOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	return &cobra.Command{
		Use:   "sync-prs",
		Short: "Sync pull requests for the current project",
		Long: `Manually trigger a PR sync for the current project.

This fetches recent pull requests from the configured platform (e.g. GitHub)
and links them to captured sessions by branch name.

Requires:
  - gh CLI installed and authenticated
  - Project configured with PR tracking enabled (aisync project init)

Example:
  aisync project sync-prs`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncPRs(opts)
		},
	}
}

func runSyncPRs(opts *syncPRsOptions) error {
	out := opts.IO.Out

	// Detect project
	d := detect(opts.Factory)
	if !d.isGitRepo {
		return fmt.Errorf("not a git repository")
	}

	cfg, err := opts.Factory.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Find project config
	pc := cfg.GetProjectClassifier(d.remoteURL, d.dir)
	if pc == nil {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Project not configured. Run `aisync project init` first.")
		fmt.Fprintln(out)
		return nil
	}

	if !pc.PREnabled {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  PR tracking is disabled for this project.")
		fmt.Fprintln(out, "  Enable it with: aisync project init --pr-enabled")
		fmt.Fprintln(out)
		return nil
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "  Syncing PRs for %s...\n", d.displayName)

	// Create GitHub platform client for this project's directory
	plat := github.New(d.dir)
	if !plat.Available() {
		return fmt.Errorf("gh CLI not available — install it from https://cli.github.com")
	}

	// Fetch PRs
	prs, err := plat.ListRecentPRs("all", 100)
	if err != nil {
		return fmt.Errorf("fetching PRs: %w", err)
	}

	if len(prs) == 0 {
		fmt.Fprintln(out, "  No pull requests found.")
		fmt.Fprintln(out)
		return nil
	}

	// Save to store
	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	saved, linked := 0, 0
	for i := range prs {
		if err := store.SavePullRequest(&prs[i]); err != nil {
			continue
		}
		saved++

		// Link sessions by branch
		if prs[i].Branch != "" {
			sessions, _ := store.List(session.ListOptions{Branch: prs[i].Branch})
			for _, s := range sessions {
				if linkErr := store.LinkSessionPR(s.ID, prs[i].RepoOwner, prs[i].RepoName, prs[i].Number); linkErr == nil {
					linked++
				}
			}
		}
	}

	fmt.Fprintf(out, "  Fetched %d PRs, saved %d, linked %d sessions\n", len(prs), saved, linked)
	fmt.Fprintln(out)

	return nil
}
