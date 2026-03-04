// Package rewindcmd implements the `aisync rewind` CLI command.
// It creates a fork of a session truncated at a given message index.
package rewindcmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the rewind command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	SessionID string
	AtMessage int
	JSON      bool
}

// NewCmdRewind creates the `aisync rewind` command.
func NewCmdRewind(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "rewind <session-id>",
		Short: "Fork a session at a specific message",
		Long: `Create a new session that is a fork of an existing session,
truncated at the given message index. The original session is never modified.

Use this to go back to a point where things were still good and discard bad turns.

The --message flag specifies the 1-based message index to keep up to (inclusive).

Examples:
  aisync rewind --message 5 abc123       # keep first 5 messages
  aisync rewind --json --message 3 xyz   # JSON output`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.SessionID = args[0]
			return runRewind(opts)
		},
	}

	cmd.Flags().IntVar(&opts.AtMessage, "message", 0, "Truncate at this message index (1-based, inclusive, required)")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output results as JSON")
	_ = cmd.MarkFlagRequired("message")

	return cmd
}

func runRewind(opts *Options) error {
	out := opts.IO.Out

	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	sid, err := session.ParseID(opts.SessionID)
	if err != nil {
		return err
	}

	result, err := svc.Rewind(context.Background(), service.RewindRequest{
		SessionID: sid,
		AtMessage: opts.AtMessage,
	})
	if err != nil {
		return err
	}

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	fmt.Fprintf(out, "Rewound session %s at message %d\n", result.OriginalID, result.TruncatedAt)
	fmt.Fprintf(out, "  New session: %s\n", result.NewSession.ID)
	fmt.Fprintf(out, "  Messages:    %d (removed %d)\n", len(result.NewSession.Messages), result.MessagesRemoved)
	fmt.Fprintf(out, "  Branch:      %s\n", result.NewSession.Branch)

	return nil
}
