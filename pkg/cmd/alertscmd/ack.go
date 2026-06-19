package alertscmd

import (
	"fmt"
	"os/user"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

type ackOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory
	All     bool
	Actor   string
}

func newCmdAck(f *cmdutil.Factory) *cobra.Command {
	opts := &ackOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "ack [id]",
		Short: "Acknowledge an alert (or all alerts with --all)",
		Long: `Acknowledge an in-app alert. Once acknowledged, the entry is hidden from
the default list and from the /alerts page (unless --all is passed to list).

Examples:
  aisync alerts ack 42                # acknowledge alert id 42
  aisync alerts ack --all             # acknowledge every unacked alert
  aisync alerts ack 42 --actor alice  # override the recorded actor name`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAck(opts, args)
		},
	}

	cmd.Flags().BoolVar(&opts.All, "all", false, "acknowledge every currently-unacked alert")
	cmd.Flags().StringVar(&opts.Actor, "actor", "", "actor name recorded as acknowledged_by (defaults to OS user)")

	return cmd
}

func runAck(opts *ackOptions, args []string) error {
	out := opts.IO.Out

	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	actor := opts.Actor
	if actor == "" {
		actor = defaultActor()
	}
	now := time.Now()

	if opts.All {
		if len(args) > 0 {
			return fmt.Errorf("cannot mix --all with a positional id")
		}
		n, err := store.AcknowledgeAllNotifications(actor, now)
		if err != nil {
			return fmt.Errorf("acknowledging all alerts: %w", err)
		}
		fmt.Fprintf(out, "Acknowledged %d alert(s) as %s.\n", n, actor)
		return nil
	}

	if len(args) != 1 {
		return fmt.Errorf("alert id required (or pass --all)")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid alert id %q", args[0])
	}

	if err := store.AcknowledgeNotification(id, actor, now); err != nil {
		return fmt.Errorf("acknowledging alert %d: %w", id, err)
	}
	fmt.Fprintf(out, "Acknowledged alert %d as %s.\n", id, actor)
	return nil
}

func defaultActor() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "cli"
}
