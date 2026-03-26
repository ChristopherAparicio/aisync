// Package restore implements the `aisync restore` CLI command.
package restore

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/restore/filter"
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

	// Filter flags
	CleanErrors   bool
	StripEmpty    bool
	FixOrphans    bool
	RedactSecrets bool
	ExcludeFlag   string // comma-separated: indices (e.g. "0,3,5"), roles (e.g. "system"), or pattern (e.g. "/regex/")
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
		Long: `Restores a previously captured session into an AI tool.

Smart Restore filters can clean up the session before restoring:
  --fix-orphans      Fix orphan tool_use blocks by injecting synthetic error results
  --clean-errors     Replace tool error outputs with compact summaries
  --strip-empty      Remove messages with no content
  --redact-secrets   Replace detected secrets with $VARIABLE_NAME references
  --exclude          Remove messages by index, role, or content pattern

The --exclude flag accepts a comma-separated list of:
  - Indices:  "0,3,5" (0-based message positions)
  - Roles:    "system" (remove all system messages)
  - Patterns: "/regex/" (remove messages matching the regex)

Example: aisync restore --session ses-abc --clean-errors --redact-secrets`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestore(opts)
		},
	}

	cmd.Flags().StringVar(&opts.SessionFlag, "session", "", "Restore a specific session by ID")
	cmd.Flags().StringVar(&opts.ProviderFlag, "provider", "", "Force restore into a specific provider")
	cmd.Flags().StringVar(&opts.AgentFlag, "agent", "", "Target agent name (e.g., for OpenCode multi-agent)")
	cmd.Flags().BoolVar(&opts.AsContext, "as-context", false, "Generate CONTEXT.md instead of native import")
	_ = cmd.Flags().MarkHidden("as-context") // hidden: use native import instead; kept for backward compat
	cmd.Flags().IntVar(&opts.PRFlag, "pr", 0, "Restore session linked to this PR number")
	cmd.Flags().BoolVar(&opts.Pick, "pick", false, "Choose from available sessions on the current branch")

	// Smart Restore filter flags
	cmd.Flags().BoolVar(&opts.CleanErrors, "clean-errors", false, "Replace tool error outputs with compact summaries")
	cmd.Flags().BoolVar(&opts.StripEmpty, "strip-empty", false, "Remove empty messages (no content, no tool calls)")
	cmd.Flags().BoolVar(&opts.FixOrphans, "fix-orphans", false, "Fix orphan tool_use blocks by injecting synthetic error results")
	cmd.Flags().BoolVar(&opts.RedactSecrets, "redact-secrets", false, "Replace detected secrets with $VARIABLE_NAME references")
	cmd.Flags().StringVar(&opts.ExcludeFlag, "exclude", "", "Exclude messages by index, role, or /pattern/ (comma-separated)")

	return cmd
}

// buildFilters constructs the filter chain from CLI flags.
// Filter order: exclude → empty → errors → secrets
// (exclude first so subsequent filters work on a smaller set).
func buildFilters(opts *Options) ([]session.SessionFilter, error) {
	var filters []session.SessionFilter

	// 1. Message excluder (if --exclude is set)
	if opts.ExcludeFlag != "" {
		f, err := parseExcludeFlag(opts.ExcludeFlag)
		if err != nil {
			return nil, fmt.Errorf("--exclude: %w", err)
		}
		filters = append(filters, f)
	}

	// 2. Orphan tool fixer (before empty/error cleaners so they can process the results)
	if opts.FixOrphans {
		filters = append(filters, filter.NewOrphanToolFixer())
	}

	// 3. Empty message filter
	if opts.StripEmpty {
		filters = append(filters, filter.NewEmptyMessage())
	}

	// 4. Error cleaner
	if opts.CleanErrors {
		filters = append(filters, filter.NewErrorCleaner())
	}

	// 5. Secret redactor (last, so it scans everything that remains)
	if opts.RedactSecrets {
		filters = append(filters, filter.NewSecretRedactor())
	}

	return filters, nil
}

// parseExcludeFlag parses the --exclude flag value into a MessageExcluder.
// Format: comma-separated values where:
//   - numbers are message indices (0-based)
//   - /pattern/ is a content regex
//   - anything else is treated as a role name
func parseExcludeFlag(value string) (*filter.MessageExcluder, error) {
	cfg := filter.MessageExcluderConfig{}

	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Check if it's a regex pattern: /pattern/
		if strings.HasPrefix(part, "/") && strings.HasSuffix(part, "/") && len(part) > 2 {
			pattern := part[1 : len(part)-1]
			if cfg.ContentPattern != "" {
				return nil, fmt.Errorf("only one content pattern is supported, got multiple")
			}
			cfg.ContentPattern = pattern
			continue
		}

		// Check if it's an integer (message index)
		if idx, err := strconv.Atoi(part); err == nil {
			if idx < 0 {
				return nil, fmt.Errorf("invalid negative index: %d", idx)
			}
			cfg.Indices = append(cfg.Indices, idx)
			continue
		}

		// Otherwise treat as role name
		cfg.Roles = append(cfg.Roles, part)
	}

	return filter.NewMessageExcluder(cfg)
}

// pickSession lists sessions on the current branch and prompts the user to pick one.
func pickSession(opts *Options, svc service.SessionServicer, projectPath, branch string) (session.ID, error) {
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

	// Build filter chain from CLI flags
	filters, err := buildFilters(opts)
	if err != nil {
		return err
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
		Filters:      filters,
	})
	if err != nil {
		return err
	}

	// Print filter results (if any filters were applied)
	if len(result.FilterResults) > 0 {
		fmt.Fprintln(out, "Smart Restore filters applied:")
		for _, fr := range result.FilterResults {
			if fr.Applied {
				fmt.Fprintf(out, "  [%s] %s\n", fr.FilterName, fr.Summary)
			}
		}
		fmt.Fprintln(out)
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
