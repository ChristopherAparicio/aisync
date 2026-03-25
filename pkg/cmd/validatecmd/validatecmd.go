// Package validatecmd implements the `aisync validate` CLI command.
// It checks a session's message structure for integrity issues such as
// orphan tool_use blocks (tool calls without corresponding tool_result),
// consecutive same-role messages, and other structural problems that
// can break the Anthropic Messages API.
package validatecmd

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

// Options holds all inputs for the validate command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	SessionID string
	Fix       bool // if true, auto-rewind to fix errors
	JSON      bool
	Quiet     bool // only output if invalid
}

// NewCmdValidate creates the `aisync validate` command.
func NewCmdValidate(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "validate <session-id>",
		Short: "Check session integrity for structural issues",
		Long: `Validate a session's message structure for integrity issues.

Detects problems that break the Anthropic Messages API, such as:
  - Orphan tool_use blocks (tool called but no tool_result returned)
  - Consecutive same-role messages (breaks alternation rule)
  - Pending tool calls that never completed
  - Empty messages

When issues are found, the command suggests the optimal rewind point
to fix the session. Use --fix to automatically rewind.

Examples:
  aisync validate abc123                # check session integrity
  aisync validate --fix abc123          # auto-fix by rewinding
  aisync validate --json abc123         # JSON output
  aisync validate --quiet abc123        # exit code only (0=valid, 1=invalid)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.SessionID = args[0]
			return runValidate(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Fix, "fix", false, "Auto-fix by rewinding to before the first error")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output results as JSON")
	cmd.Flags().BoolVar(&opts.Quiet, "quiet", false, "Quiet mode: exit 0 if valid, 1 if invalid (no output)")

	return cmd
}

func runValidate(opts *Options) error {
	out := opts.IO.Out

	svc, err := opts.Factory.SessionService()
	if err != nil {
		return fmt.Errorf("initializing service: %w", err)
	}

	sid, err := session.ParseID(opts.SessionID)
	if err != nil {
		return err
	}

	sess, err := svc.Get(string(sid))
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}

	result := session.Validate(sess)

	// Quiet mode: just exit code
	if opts.Quiet {
		if !result.Valid {
			return fmt.Errorf("session %s has %d error(s)", sid, result.ErrorCount)
		}
		return nil
	}

	// JSON mode
	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	// Human-readable output
	if result.Valid && result.WarningCount == 0 {
		fmt.Fprintf(out, "✅ Session %s is valid (%d messages)\n", sid, result.MessageCount)
		return nil
	}

	if result.Valid {
		fmt.Fprintf(out, "⚠️  Session %s is valid with %d warning(s) (%d messages)\n", sid, result.WarningCount, result.MessageCount)
	} else {
		fmt.Fprintf(out, "❌ Session %s has %d error(s), %d warning(s) (%d messages)\n", sid, result.ErrorCount, result.WarningCount, result.MessageCount)
	}

	fmt.Fprintln(out)

	// Print issues grouped by severity
	for _, issue := range result.Issues {
		icon := "ℹ️ "
		switch issue.Severity {
		case session.SeverityError:
			icon = "🔴"
		case session.SeverityWarning:
			icon = "🟡"
		}

		fmt.Fprintf(out, "  %s [msg %d] %s\n", icon, issue.MessageNumber, issue.Description)
		if issue.ToolCallID != "" {
			fmt.Fprintf(out, "       Tool: %s (ID: %s)\n", issue.ToolName, issue.ToolCallID)
		}
		if issue.RewindTo > 0 {
			fmt.Fprintf(out, "       Fix: aisync rewind --message %d %s\n", issue.RewindTo, sid)
		}
	}

	if result.SuggestedRewindTo > 0 {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "💡 Suggested fix: rewind to message %d\n", result.SuggestedRewindTo)
		fmt.Fprintf(out, "   aisync rewind --message %d %s\n", result.SuggestedRewindTo, sid)
	}

	// Auto-fix mode
	if opts.Fix && result.SuggestedRewindTo > 0 {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "🔧 Auto-fixing: rewinding to message %d...\n", result.SuggestedRewindTo)

		rewindResult, err := svc.Rewind(context.Background(), service.RewindRequest{
			SessionID: sid,
			AtMessage: result.SuggestedRewindTo,
		})
		if err != nil {
			return fmt.Errorf("auto-fix rewind failed: %w", err)
		}

		fmt.Fprintf(out, "   ✅ Created fixed session: %s\n", rewindResult.NewSession.ID)
		fmt.Fprintf(out, "   Messages: %d (removed %d)\n", len(rewindResult.NewSession.Messages), rewindResult.MessagesRemoved)
		fmt.Fprintf(out, "\n   Restore with: aisync restore --session %s\n", rewindResult.NewSession.ID)
	}

	if !result.Valid {
		return fmt.Errorf("validation failed: %d error(s) found", result.ErrorCount)
	}

	return nil
}
