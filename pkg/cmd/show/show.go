// Package show implements the `aisync show` CLI command.
package show

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the show command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	ShowFiles  bool
	ShowTokens bool
	ShowCost   bool
}

// NewCmdShow creates the `aisync show` command.
func NewCmdShow(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "show [session-id | commit-sha]",
		Short: "Show details of a session",
		Long: `Shows detailed information about a captured session.

Accepts either a session ID or a git commit SHA. When a commit SHA is given,
aisync parses the AI-Session trailer from the commit message to find the
associated session.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShow(opts, args[0])
		},
	}

	cmd.Flags().BoolVar(&opts.ShowFiles, "files", false, "Show files changed in session")
	cmd.Flags().BoolVar(&opts.ShowTokens, "tokens", false, "Show token usage breakdown")
	cmd.Flags().BoolVar(&opts.ShowCost, "cost", false, "Show estimated cost breakdown")

	return cmd
}

func runShow(opts *Options, idArg string) error {
	out := opts.IO.Out

	// Get service
	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	// Resolve the argument: could be a session ID or a commit SHA
	sess, err := svc.Get(idArg)
	if err != nil {
		return fmt.Errorf("session %q not found: %w", idArg, err)
	}

	// Basic info
	fmt.Fprintf(out, "Session:  %s\n", sess.ID)
	fmt.Fprintf(out, "Provider: %s\n", sess.Provider)
	fmt.Fprintf(out, "Agent:    %s\n", sess.Agent)
	if sess.Branch != "" {
		fmt.Fprintf(out, "Branch:   %s\n", sess.Branch)
	}
	if !sess.CreatedAt.IsZero() {
		fmt.Fprintf(out, "Captured: %s\n", sess.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	fmt.Fprintf(out, "Mode:     %s\n", sess.StorageMode)

	// Message count by role
	var userCount, assistantCount int
	for _, msg := range sess.Messages {
		switch msg.Role {
		case session.RoleUser:
			userCount++
		case session.RoleAssistant:
			assistantCount++
		}
	}
	fmt.Fprintf(out, "Messages: %d (%d user, %d assistant)\n",
		len(sess.Messages), userCount, assistantCount)

	// Tokens
	if sess.TokenUsage.TotalTokens > 0 || opts.ShowTokens {
		fmt.Fprintf(out, "Tokens:   %s in / %s out / %s total\n",
			formatNumber(sess.TokenUsage.InputTokens),
			formatNumber(sess.TokenUsage.OutputTokens),
			formatNumber(sess.TokenUsage.TotalTokens))
	}

	// Summary
	if sess.Summary != "" {
		fmt.Fprintf(out, "Summary:  %s\n", sess.Summary)
	}

	// Cost estimate
	if opts.ShowCost {
		est, costErr := svc.EstimateCost(context.Background(), idArg)
		if costErr != nil {
			fmt.Fprintf(out, "\nCost: (error: %v)\n", costErr)
		} else if est.TotalCost.TotalCost > 0 || len(est.UnknownModels) > 0 {
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Cost Estimate:")
			fmt.Fprintf(out, "  %-30s  %10s  %10s  %10s\n", "MODEL", "INPUT $", "OUTPUT $", "TOTAL $")
			for _, mc := range est.PerModel {
				fmt.Fprintf(out, "  %-30s  %10s  %10s  %10s\n",
					mc.Model,
					formatCost(mc.Cost.InputCost),
					formatCost(mc.Cost.OutputCost),
					formatCost(mc.Cost.TotalCost))
			}
			fmt.Fprintf(out, "  %-30s  %10s  %10s  %10s\n",
				"────────────────────────────",
				"──────────", "──────────", "──────────")
			fmt.Fprintf(out, "  %-30s  %10s  %10s  %10s\n",
				"Total",
				formatCost(est.TotalCost.InputCost),
				formatCost(est.TotalCost.OutputCost),
				formatCost(est.TotalCost.TotalCost))
			if len(est.UnknownModels) > 0 {
				fmt.Fprintf(out, "  (unknown pricing: %s)\n", strings.Join(est.UnknownModels, ", "))
			}
		}
	}

	// Files
	if len(sess.FileChanges) > 0 && opts.ShowFiles {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Files changed:")
		for _, fc := range sess.FileChanges {
			prefix := "~"
			switch fc.ChangeType {
			case session.ChangeCreated:
				prefix = "+"
			case session.ChangeDeleted:
				prefix = "-"
			case session.ChangeRead:
				prefix = "?"
			}
			fmt.Fprintf(out, "  %s %s  (%s)\n", prefix, fc.FilePath, fc.ChangeType)
		}
	}

	// Links
	if len(sess.Links) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Linked to:")
		for _, link := range sess.Links {
			fmt.Fprintf(out, "  %s: %s\n", link.LinkType, link.Ref)
		}
	}

	// Children
	if len(sess.Children) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "Children: %d sub-agent sessions\n", len(sess.Children))
		for _, child := range sess.Children {
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

func formatCost(cost float64) string {
	if cost == 0 {
		return "$0.00"
	}
	if cost < 0.01 {
		return fmt.Sprintf("$%.4f", cost)
	}
	return fmt.Sprintf("$%.2f", cost)
}
