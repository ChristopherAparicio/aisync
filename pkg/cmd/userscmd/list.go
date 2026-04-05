package userscmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

type listOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	Kind string // filter by kind: "human", "machine", "unknown", "" = all
	JSON bool
}

func newCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &listOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all users with their kind and Slack identity",
		Long:  "Shows all users registered in aisync, including their kind (human/machine/unknown), Slack ID, and role.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(opts)
		},
	}

	cmd.Flags().StringVar(&opts.Kind, "kind", "", "Filter by kind: human, machine, unknown")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output as JSON")

	return cmd
}

func runList(opts *listOptions) error {
	out := opts.IO.Out

	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	var users []*session.User
	if opts.Kind != "" {
		users, err = store.ListUsersByKind(opts.Kind)
	} else {
		users, err = store.ListUsers()
	}
	if err != nil {
		return fmt.Errorf("listing users: %w", err)
	}

	if len(users) == 0 {
		fmt.Fprintln(out, "No users found.")
		return nil
	}

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(users)
	}

	// Table output
	fmt.Fprintf(out, "%-14s %-25s %-30s %-10s %-14s %-8s\n",
		"ID", "NAME", "EMAIL", "KIND", "SLACK ID", "ROLE")
	fmt.Fprintf(out, "%-14s %-25s %-30s %-10s %-14s %-8s\n",
		"──────────────", "─────────────────────────", "──────────────────────────────",
		"──────────", "──────────────", "────────")

	for _, u := range users {
		id := string(u.ID)
		if len(id) > 12 {
			id = id[:12] + ".."
		}
		name := u.Name
		if len(name) > 24 {
			name = name[:24] + "…"
		}
		email := u.Email
		if len(email) > 29 {
			email = email[:29] + "…"
		}
		slackID := u.SlackID
		if slackID == "" {
			slackID = "-"
		}
		kind := string(u.Kind)
		if kind == "" {
			kind = "unknown"
		}
		role := string(u.Role)
		if role == "" {
			role = "member"
		}
		fmt.Fprintf(out, "%-14s %-25s %-30s %-10s %-14s %-8s\n",
			id, name, email, kind, slackID, role)
	}

	fmt.Fprintf(out, "\n%d user(s)\n", len(users))
	return nil
}
