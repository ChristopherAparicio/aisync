package userscmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

type backfillKindOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory
	DryRun  bool
}

func newCmdBackfillKind(f *cmdutil.Factory) *cobra.Command {
	opts := &backfillKindOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "backfill-kind",
		Short: "Classify existing users as human/machine/unknown based on email patterns",
		Long:  "Re-evaluates all users with kind 'unknown' and applies the configured machine_patterns to classify them. Users with explicitly set kind (human/machine) are skipped.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackfillKind(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Show what would change without updating")

	return cmd
}

func runBackfillKind(opts *backfillKindOptions) error {
	out := opts.IO.Out

	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	cfg, err := opts.Factory.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	patterns := cfg.GetMachinePatterns()
	fmt.Fprintf(out, "Machine patterns: %v\n", patterns)

	// Get all users — we reclassify everyone, not just unknowns
	users, err := store.ListUsers()
	if err != nil {
		return fmt.Errorf("listing users: %w", err)
	}

	if len(users) == 0 {
		fmt.Fprintln(out, "No users found.")
		return nil
	}

	var changed int
	for _, u := range users {
		newKind := service.ClassifyUserKind(u.Email, patterns)
		if newKind == u.Kind {
			continue
		}

		if opts.DryRun {
			fmt.Fprintf(out, "  [DRY RUN] %s (%s) %s → %s\n", u.Name, u.Email, u.Kind, newKind)
		} else {
			if err := store.UpdateUserKind(u.ID, string(newKind)); err != nil {
				fmt.Fprintf(opts.IO.ErrOut, "  WARNING: failed to update %s: %v\n", u.ID, err)
				continue
			}
			fmt.Fprintf(out, "  Updated %s (%s) %s → %s\n", u.Name, u.Email, u.Kind, newKind)
		}
		changed++
	}

	action := "Updated"
	if opts.DryRun {
		action = "Would update"
	}
	fmt.Fprintf(out, "\n%s %d of %d user(s)\n", action, changed, len(users))

	// Show breakdown
	counts := map[session.UserKind]int{}
	for _, u := range users {
		kind := service.ClassifyUserKind(u.Email, patterns)
		counts[kind]++
	}
	fmt.Fprintf(out, "Classification: %d human, %d machine, %d unknown\n",
		counts[session.UserKindHuman], counts[session.UserKindMachine], counts[session.UserKindUnknown])

	return nil
}
