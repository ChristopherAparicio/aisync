// Package status implements the `aisync status` command.
package status

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the status command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory
}

// NewCmdStatus creates the `aisync status` command.
func NewCmdStatus(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show current aisync state",
		Long:  "Shows the current branch, detected sessions, provider status, and hook installation state.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(opts)
		},
	}

	return cmd
}

func runStatus(opts *Options) error {
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
	fmt.Fprintf(out, "Branch:    %s\n", branch)

	// Provider info
	cfg, cfgErr := opts.Factory.Config()
	if cfgErr == nil {
		providers := cfg.GetProviders()
		fmt.Fprint(out, "Providers: ")
		for i, p := range providers {
			if i > 0 {
				fmt.Fprint(out, ", ")
			}
			fmt.Fprint(out, p)
		}
		fmt.Fprintln(out)
	} else {
		fmt.Fprintln(out, "Providers: (not configured — run 'aisync init')")
	}

	// Session info
	store, storeErr := opts.Factory.Store()
	if storeErr == nil {
		topLevel, topErr := gitClient.TopLevel()
		if topErr == nil {
			sess, sessionErr := store.GetLatestByBranch(topLevel, branch)
			if sessionErr == nil {
				// Best-effort count — failure falls back to single-session display.
				count, countErr := store.CountByBranch(topLevel, branch)
				if countErr != nil {
					fmt.Fprintf(opts.IO.ErrOut, "Warning: could not count branch sessions: %v\n", countErr)
				}
				if count > 1 {
					fmt.Fprintf(out, "Session:   %s (%s, %d messages) [%d sessions on branch]\n",
						sess.ID, sess.Provider, len(sess.Messages), count)
				} else {
					fmt.Fprintf(out, "Session:   %s (%s, %d messages)\n",
						sess.ID, sess.Provider, len(sess.Messages))
				}
			} else {
				fmt.Fprintln(out, "Session:   none on this branch")
			}
		}
	} else {
		fmt.Fprintln(out, "Session:   (store unavailable)")
	}

	// Hooks info
	mgr, hooksErr := opts.Factory.HooksManager()
	if hooksErr == nil {
		fmt.Fprint(out, "Hooks:     ")
		for i, s := range mgr.Statuses() {
			if i > 0 {
				fmt.Fprint(out, "  ")
			}
			if s.Installed {
				fmt.Fprintf(out, "%s ✓", s.Name)
			} else {
				fmt.Fprintf(out, "%s ✗", s.Name)
			}
		}
		fmt.Fprintln(out)
	} else {
		fmt.Fprintln(out, "Hooks:     (could not determine hooks directory)")
	}

	return nil
}
