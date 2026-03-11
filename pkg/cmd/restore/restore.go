// Package restore implements the `aisync restore` CLI command.
package restore

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the restore command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	SessionFlag  string
	ProviderFlag string
	AgentFlag    string
	AsContext    bool
	PRFlag       int
	Pick         bool
}

// NewCmdRestore creates the `aisync restore` command.
func NewCmdRestore(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore a captured AI session",
		Long:  "Restores a previously captured session into an AI tool, or generates a CONTEXT.md file.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestore(opts)
		},
	}

	cmd.Flags().StringVar(&opts.SessionFlag, "session", "", "Restore a specific session by ID")
	cmd.Flags().StringVar(&opts.ProviderFlag, "provider", "", "Force restore into a specific provider")
	cmd.Flags().StringVar(&opts.AgentFlag, "agent", "", "Target agent name (e.g., for OpenCode multi-agent)")
	cmd.Flags().BoolVar(&opts.AsContext, "as-context", false, "Generate CONTEXT.md instead of native import")
	cmd.Flags().IntVar(&opts.PRFlag, "pr", 0, "Restore session linked to this PR number")
	cmd.Flags().BoolVar(&opts.Pick, "pick", false, "Choose from available sessions on the current branch")

	return cmd
}

// pickSession lists sessions on the current branch and prompts the user to pick one.
func pickSession(opts *Options, svc *service.SessionService, projectPath, branch string) (session.ID, error) {
	out := opts.IO.Out

	summaries, err := svc.List(service.ListRequest{
		ProjectPath: projectPath,
		Branch:      branch,
	})
	if err != nil {
		return "", fmt.Errorf("listing sessions: %w", err)
	}

	if len(summaries) == 0 {
		return "", fmt.Errorf("no sessions found on branch %q", branch)
	}

	if len(summaries) == 1 {
		fmt.Fprintf(out, "Only one session on branch %q, using %s\n", branch, summaries[0].ID)
		return summaries[0].ID, nil
	}

	fmt.Fprintf(out, "Sessions on branch %q:\n\n", branch)
	fmt.Fprintf(out, "  %-4s %-20s %-14s %6s  %s\n", "#", "ID", "PROVIDER", "TOKENS", "SUMMARY")
	for i, s := range summaries {
		summary := s.Summary
		if len(summary) > 50 {
			summary = summary[:47] + "..."
		}
		if summary == "" {
			summary = "(no summary)"
		}
		fmt.Fprintf(out, "  %-4d %-20s %-14s %6d  %s\n",
			i+1, truncateID(string(s.ID), 20), s.Provider, s.TotalTokens, summary)
	}

	fmt.Fprintf(out, "\nEnter number (1-%d): ", len(summaries))

	scanner := bufio.NewScanner(opts.IO.In)
	if !scanner.Scan() {
		return "", fmt.Errorf("no input received")
	}

	choice, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
	if err != nil || choice < 1 || choice > len(summaries) {
		return "", fmt.Errorf("invalid choice: enter a number between 1 and %d", len(summaries))
	}

	return summaries[choice-1].ID, nil
}

// truncateID truncates a string to maxLen, adding "..." if needed.
func truncateID(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func runRestore(opts *Options) error {
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

	topLevel, err := gitClient.TopLevel()
	if err != nil {
		return fmt.Errorf("could not determine repository root: %w", err)
	}

	// Parse session ID
	var sessionID session.ID
	if opts.SessionFlag != "" {
		parsed, parseErr := session.ParseID(opts.SessionFlag)
		if parseErr != nil {
			return parseErr
		}
		sessionID = parsed
	}

	// Parse provider
	var providerName session.ProviderName
	if opts.ProviderFlag != "" {
		parsed, parseErr := session.ParseProviderName(opts.ProviderFlag)
		if parseErr != nil {
			return parseErr
		}
		providerName = parsed
	}

	// Get service
	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	// Interactive pick: list sessions on the current branch and let the user choose.
	if opts.Pick && sessionID == "" {
		picked, pickErr := pickSession(opts, svc, topLevel, branch)
		if pickErr != nil {
			return pickErr
		}
		sessionID = picked
	}

	// Restore
	result, err := svc.Restore(service.RestoreRequest{
		ProjectPath:  topLevel,
		Branch:       branch,
		SessionID:    sessionID,
		ProviderName: providerName,
		Agent:        opts.AgentFlag,
		AsContext:    opts.AsContext,
		PRNumber:     opts.PRFlag,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Restored session %s\n", result.Session.ID)
	fmt.Fprintf(out, "  Provider: %s\n", result.Session.Provider)

	switch result.Method {
	case "native":
		fmt.Fprintln(out, "  Method:   native import")
		fmt.Fprintln(out, "  Launch your AI agent to continue with this context.")
	case "converted":
		fmt.Fprintln(out, "  Method:   cross-provider conversion")
		fmt.Fprintln(out, "  Session was converted to the target provider format.")
		fmt.Fprintln(out, "  Launch your AI agent to continue with this context.")
	case "context":
		fmt.Fprintf(out, "  Method:   CONTEXT.md\n")
		fmt.Fprintf(out, "  File:     %s\n", result.ContextPath)
		fmt.Fprintln(out, "  Open CONTEXT.md or paste it into your AI agent to resume.")
	}

	return nil
}
