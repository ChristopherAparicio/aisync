// Package resumecmd implements the `aisync resume` CLI command.
// It combines `git checkout` + `aisync restore` in one step.
package resumecmd

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/restore/filter"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the resume command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	Branch    string
	SessionID string
	Provider  string
	AsContext bool

	// DryRun previews the restore without writing anything to disk.
	DryRun bool

	// Smart Restore filter flags
	CleanErrors   bool
	StripEmpty    bool
	FixOrphans    bool
	RedactSecrets bool
	ExcludeFlag   string // comma-separated: indices, roles, or /pattern/
}

// NewCmdResume creates the `aisync resume` command.
func NewCmdResume(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "resume <branch>",
		Short: "Switch branch and restore the AI session",
		Long: `Convenience command that combines git checkout + aisync restore.
Switches to the given branch and restores the most recent AI session
for that branch.

Smart Restore filters can clean up the session before restoring:
  --fix-orphans      Fix orphan tool_use blocks by injecting synthetic error results
  --clean-errors     Replace tool error outputs with compact summaries
  --strip-empty      Remove messages with no content
  --redact-secrets   Replace detected secrets with $VARIABLE_NAME references
  --exclude          Remove messages by index, role, or content pattern

Examples:
  aisync resume feat/auth                          # checkout + restore
  aisync resume --session abc123 feat/x            # restore a specific session
  aisync resume --clean-errors feat/auth           # resume with cleaned errors
  aisync resume --fix-orphans --strip-empty feat/x # resume with multiple filters`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Branch = args[0]
			return runResume(opts)
		},
	}

	cmd.Flags().StringVar(&opts.SessionID, "session", "", "Specific session ID to restore")
	cmd.Flags().StringVar(&opts.Provider, "provider", "", "Target provider (claude-code, opencode, cursor)")
	cmd.Flags().BoolVar(&opts.AsContext, "as-context", false, "Restore as context.md instead of native format")
	_ = cmd.Flags().MarkHidden("as-context") // hidden: use native import instead; kept for backward compat

	// Dry-run preview
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Preview what the restore would do without writing anything")

	// Smart Restore filter flags (same as `aisync restore`)
	cmd.Flags().BoolVar(&opts.CleanErrors, "clean-errors", false, "Replace tool error outputs with compact summaries")
	cmd.Flags().BoolVar(&opts.StripEmpty, "strip-empty", false, "Remove empty messages (no content, no tool calls)")
	cmd.Flags().BoolVar(&opts.FixOrphans, "fix-orphans", false, "Fix orphan tool_use blocks by injecting synthetic error results")
	cmd.Flags().BoolVar(&opts.RedactSecrets, "redact-secrets", false, "Replace detected secrets with $VARIABLE_NAME references")
	cmd.Flags().StringVar(&opts.ExcludeFlag, "exclude", "", "Exclude messages by index, role, or /pattern/ (comma-separated)")

	return cmd
}

func runResume(opts *Options) error {
	out := opts.IO.Out

	// Git checkout
	gitClient, err := opts.Factory.Git()
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	if err := gitClient.Checkout(opts.Branch); err != nil {
		return fmt.Errorf("git checkout %s: %w", opts.Branch, err)
	}
	fmt.Fprintf(out, "Switched to branch %s\n", opts.Branch)

	// Get project path
	topLevel, err := gitClient.TopLevel()
	if err != nil {
		return fmt.Errorf("could not determine repository root: %w", err)
	}

	// Restore session
	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	var providerName session.ProviderName
	if opts.Provider != "" {
		parsed, parseErr := session.ParseProviderName(opts.Provider)
		if parseErr != nil {
			return parseErr
		}
		providerName = parsed
	}

	var sid session.ID
	if opts.SessionID != "" {
		parsed, parseErr := session.ParseID(opts.SessionID)
		if parseErr != nil {
			return parseErr
		}
		sid = parsed
	}

	// Build filter chain from CLI flags
	filters, err := buildFilters(opts)
	if err != nil {
		return err
	}

	result, err := svc.Restore(service.RestoreRequest{
		SessionID:    sid,
		ProjectPath:  topLevel,
		Branch:       opts.Branch,
		ProviderName: providerName,
		AsContext:    opts.AsContext,
		DryRun:       opts.DryRun,
		Filters:      filters,
	})
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}

	// ── Dry-run preview ──
	if result.DryRun != nil {
		return renderResumeDryRun(out, result.DryRun)
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

	fmt.Fprintf(out, "Restored session %s (%s)\n", result.Session.ID, result.Method)
	return nil
}

// renderResumeDryRun prints a compact dry-run preview for the resume command.
func renderResumeDryRun(out io.Writer, preview *service.DryRunPreview) error {
	fmt.Fprintln(out, "Dry-run preview (no changes written):")
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  Session:    %s\n", preview.SessionID)
	fmt.Fprintf(out, "  Provider:   %s\n", preview.Provider)
	if preview.Branch != "" {
		fmt.Fprintf(out, "  Branch:     %s\n", preview.Branch)
	}
	if preview.Summary != "" {
		fmt.Fprintf(out, "  Summary:    %s\n", preview.Summary)
	}
	fmt.Fprintf(out, "  Method:     %s\n", preview.Method)
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  Messages:   %d\n", preview.MessageCount)
	fmt.Fprintf(out, "  Tool calls: %d\n", preview.ToolCallCount)
	if preview.ErrorCount > 0 {
		fmt.Fprintf(out, "  Errors:     %d\n", preview.ErrorCount)
	}
	fmt.Fprintf(out, "  Tokens:     %d (in: %d, out: %d)\n", preview.TotalTokens, preview.InputTokens, preview.OutputTokens)
	if preview.FileChanges > 0 {
		fmt.Fprintf(out, "  Files:      %d\n", preview.FileChanges)
	}

	if len(preview.FilterResults) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Filters that would be applied:")
		for _, fr := range preview.FilterResults {
			if fr.Applied {
				fmt.Fprintf(out, "    [%s] %s\n", fr.FilterName, fr.Summary)
			} else {
				fmt.Fprintf(out, "    [%s] (no changes)\n", fr.FilterName)
			}
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Run without --dry-run to apply.")
	return nil
}

// buildFilters constructs the filter chain from CLI flags.
// Filter order: exclude → fix-orphans → strip-empty → clean-errors → redact-secrets
// (same order as `aisync restore`).
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

		// Try as index
		if idx, err := strconv.Atoi(part); err == nil {
			if idx < 0 {
				return nil, fmt.Errorf("invalid negative index: %d", idx)
			}
			cfg.Indices = append(cfg.Indices, idx)
			continue
		}

		// Try as regex pattern (/.../)
		if strings.HasPrefix(part, "/") && strings.HasSuffix(part, "/") && len(part) > 2 {
			pattern := part[1 : len(part)-1]
			if cfg.ContentPattern != "" {
				return nil, fmt.Errorf("only one content pattern is supported, got multiple")
			}
			cfg.ContentPattern = pattern
			continue
		}

		// Otherwise: role name
		cfg.Roles = append(cfg.Roles, part)
	}

	return filter.NewMessageExcluder(cfg)
}
