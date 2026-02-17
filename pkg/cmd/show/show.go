// Package show implements the `aisync show` CLI command.
package show

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the show command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	ShowFiles  bool
	ShowTokens bool
}

// NewCmdShow creates the `aisync show` command.
func NewCmdShow(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "show [session-id]",
		Short: "Show details of a session",
		Long:  "Shows detailed information about a captured session.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShow(opts, args[0])
		},
	}

	cmd.Flags().BoolVar(&opts.ShowFiles, "files", false, "Show files changed in session")
	cmd.Flags().BoolVar(&opts.ShowTokens, "tokens", false, "Show token usage breakdown")

	return cmd
}

func runShow(opts *Options, idArg string) error {
	out := opts.IO.Out

	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	// Try as session ID first
	sessionID, parseErr := domain.ParseSessionID(idArg)
	if parseErr != nil {
		return parseErr
	}

	session, err := store.Get(sessionID)
	if err != nil {
		return fmt.Errorf("session %q not found: %w", idArg, err)
	}

	// Basic info
	fmt.Fprintf(out, "Session:  %s\n", session.ID)
	fmt.Fprintf(out, "Provider: %s\n", session.Provider)
	fmt.Fprintf(out, "Agent:    %s\n", session.Agent)
	if session.Branch != "" {
		fmt.Fprintf(out, "Branch:   %s\n", session.Branch)
	}
	if !session.CreatedAt.IsZero() {
		fmt.Fprintf(out, "Captured: %s\n", session.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	fmt.Fprintf(out, "Mode:     %s\n", session.StorageMode)

	// Message count by role
	var userCount, assistantCount int
	for _, msg := range session.Messages {
		switch msg.Role {
		case domain.RoleUser:
			userCount++
		case domain.RoleAssistant:
			assistantCount++
		}
	}
	fmt.Fprintf(out, "Messages: %d (%d user, %d assistant)\n",
		len(session.Messages), userCount, assistantCount)

	// Tokens
	if session.TokenUsage.TotalTokens > 0 || opts.ShowTokens {
		fmt.Fprintf(out, "Tokens:   %s in / %s out / %s total\n",
			formatNumber(session.TokenUsage.InputTokens),
			formatNumber(session.TokenUsage.OutputTokens),
			formatNumber(session.TokenUsage.TotalTokens))
	}

	// Summary
	if session.Summary != "" {
		fmt.Fprintf(out, "Summary:  %s\n", session.Summary)
	}

	// Files
	if len(session.FileChanges) > 0 && (opts.ShowFiles || true) {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Files changed:")
		for _, fc := range session.FileChanges {
			prefix := "~"
			switch fc.ChangeType {
			case domain.ChangeCreated:
				prefix = "+"
			case domain.ChangeDeleted:
				prefix = "-"
			case domain.ChangeRead:
				prefix = "?"
			}
			fmt.Fprintf(out, "  %s %s  (%s)\n", prefix, fc.FilePath, fc.ChangeType)
		}
	}

	// Links
	if len(session.Links) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Linked to:")
		for _, link := range session.Links {
			fmt.Fprintf(out, "  %s: %s\n", link.LinkType, link.Ref)
		}
	}

	// Children
	if len(session.Children) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "Children: %d sub-agent sessions\n", len(session.Children))
		for _, child := range session.Children {
			fmt.Fprintf(out, "  - %s (agent: %s, %d messages)\n",
				child.ID, child.Agent, len(child.Messages))
		}
	}

	return nil
}

func formatNumber(n int) string {
	if n == 0 {
		return "0"
	}
	s := fmt.Sprintf("%d", n)
	// Insert commas
	parts := make([]string, 0)
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return strings.Join(parts, ",")
}
