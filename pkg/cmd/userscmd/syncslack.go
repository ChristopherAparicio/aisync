package userscmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/identity"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

type syncSlackOptions struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	AutoLink bool   // automatically link exact matches
	MinConf  string // minimum confidence level: "exact", "high", "medium", "low"
	DryRun   bool   // only show suggestions, don't link anything
}

func newCmdSyncSlack(f *cmdutil.Factory) *cobra.Command {
	opts := &syncSlackOptions{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "sync-slack",
		Short: "Import Slack workspace members and suggest identity matches",
		Long: `Fetches all members from your Slack workspace (via bot token) and
matches them against Git users using fuzzy name matching.

Requires a Slack bot token configured in:
  notification.slack.bot_token (in config.json)

The bot needs the "users:read" scope. For email matching, also add "users:read.email".

Examples:
  aisync users sync-slack                    # Show match suggestions
  aisync users sync-slack --auto-link        # Auto-link exact matches
  aisync users sync-slack --min-confidence high  # Only show high+ confidence matches
  aisync users sync-slack --dry-run          # Preview without linking`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncSlack(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.AutoLink, "auto-link", false, "Automatically link exact matches (email or name)")
	cmd.Flags().StringVar(&opts.MinConf, "min-confidence", "low", "Minimum confidence level: exact, high, medium, low")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Preview matches without linking anything")

	return cmd
}

func runSyncSlack(opts *syncSlackOptions) error {
	out := opts.IO.Out

	// Get config for bot token
	cfg, err := opts.Factory.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	botToken := cfg.GetNotificationSlackBotToken()
	if botToken == "" {
		fmt.Fprintln(out, "Error: No Slack bot token configured.")
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Set it with:")
		fmt.Fprintln(out, "  aisync config set notification.slack.bot_token xoxb-your-token")
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "The bot needs the 'users:read' scope (and 'users:read.email' for email matching).")
		return fmt.Errorf("slack bot token not configured")
	}

	// Get store
	store, err := opts.Factory.Store()
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	// Parse min confidence
	minConf := parseConfidence(opts.MinConf)

	// Create the identity service
	slackClient := identity.NewSlackClient(identity.SlackClientConfig{
		BotToken: botToken,
	})

	svc := identity.NewService(slackClient, store, identity.ServiceConfig{
		MinConfidence: minConf,
		AutoLinkExact: opts.AutoLink && !opts.DryRun,
	})

	if svc == nil {
		return fmt.Errorf("failed to create identity service")
	}

	fmt.Fprintln(out, "Fetching Slack workspace members...")
	result, err := svc.SyncSlackMembers()
	if err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}

	// Print summary
	fmt.Fprintln(out, "")
	fmt.Fprintf(out, "Slack members found: %d\n", result.SlackMembersFound)
	fmt.Fprintf(out, "Git users total:     %d\n", result.GitUsersTotal)
	fmt.Fprintf(out, "Already linked:      %d\n", result.AlreadyLinked)
	if result.AutoLinked > 0 {
		fmt.Fprintf(out, "Auto-linked:         %d\n", result.AutoLinked)
	}
	fmt.Fprintf(out, "Unmatched:           %d\n", result.Unmatched)
	fmt.Fprintln(out, "")

	// Print suggestions
	if len(result.NewSuggestions) == 0 {
		fmt.Fprintln(out, "No new match suggestions.")
		return nil
	}

	fmt.Fprintf(out, "%d match suggestion(s):\n\n", len(result.NewSuggestions))
	fmt.Fprintf(out, "%-25s %-25s %-10s %-10s %s\n",
		"GIT USER", "SLACK MEMBER", "SCORE", "CONF", "REASON")
	fmt.Fprintf(out, "%-25s %-25s %-10s %-10s %s\n",
		"─────────────────────────", "─────────────────────────",
		"──────────", "──────────", "──────────────────────")

	for _, s := range result.NewSuggestions {
		gitName := truncate(s.GitUserName, 24)
		slackName := truncate(s.SlackRealName, 24)
		status := ""
		if s.Status == identity.StatusApproved {
			status = " ✓ linked"
		}
		fmt.Fprintf(out, "%-25s %-25s %-10s %-10s %s%s\n",
			gitName, slackName,
			fmt.Sprintf("%.2f", s.Score),
			s.Confidence,
			truncate(s.MatchReason, 30),
			status,
		)
	}

	// Print link instructions for pending suggestions
	pending := 0
	for _, s := range result.NewSuggestions {
		if s.Status == identity.StatusPending {
			pending++
		}
	}

	if pending > 0 {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "To link a user manually:")
		fmt.Fprintln(out, "  aisync users link <user-id> --slack-id <slack-id>")
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "To auto-link all exact matches:")
		fmt.Fprintln(out, "  aisync users sync-slack --auto-link")
	}

	return nil
}

func parseConfidence(s string) identity.MatchConfidence {
	switch strings.ToLower(s) {
	case "exact":
		return identity.ConfidenceExact
	case "high":
		return identity.ConfidenceHigh
	case "medium":
		return identity.ConfidenceMedium
	case "low":
		return identity.ConfidenceLow
	default:
		return identity.ConfidenceLow
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}
