// Package errorscmd implements the `aisync errors` CLI command.
// It shows classified errors for a session or recent errors across all sessions.
package errorscmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// Options holds all inputs for the errors command.
type Options struct {
	IO      *iostreams.IOStreams
	Factory *cmdutil.Factory

	SessionID string
	Recent    bool
	Category  string
	Limit     int
	JSON      bool
}

// NewCmdErrors creates the `aisync errors` command.
func NewCmdErrors(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{
		IO:      f.IOStreams,
		Factory: f,
	}

	cmd := &cobra.Command{
		Use:   "errors [session-id]",
		Short: "Show classified errors for a session",
		Long: `Display errors extracted and classified from captured AI sessions.

By default shows errors for a specific session. Use --recent to see
errors across all sessions. Errors are classified by category:
provider_error, rate_limit, context_overflow, auth_error, validation,
tool_error, network_error, aborted, unknown.

Examples:
  aisync errors abc123                    # show errors for a session
  aisync errors abc123 --json             # JSON output
  aisync errors --recent                  # recent errors across all sessions
  aisync errors --recent --category tool_error --limit 20`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.SessionID = args[0]
			}
			if opts.SessionID == "" && !opts.Recent {
				return fmt.Errorf("provide a session ID or use --recent")
			}
			return runErrors(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Recent, "recent", false, "Show recent errors across all sessions")
	cmd.Flags().StringVar(&opts.Category, "category", "", "Filter by error category (e.g. tool_error, provider_error)")
	cmd.Flags().IntVar(&opts.Limit, "limit", 50, "Maximum number of errors to show")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Structured JSON output")

	return cmd
}

func runErrors(opts *Options) error {
	out := opts.IO.Out

	errSvc, err := opts.Factory.ErrorService()
	if err != nil {
		return fmt.Errorf("initializing error service: %w", err)
	}

	if opts.Recent {
		return runRecentErrors(opts, errSvc, out)
	}

	return runSessionErrors(opts, errSvc, out)
}

func runSessionErrors(opts *Options, errSvc service.ErrorServicer, out io.Writer) error {
	sessionID := session.ID(opts.SessionID)

	errors, err := errSvc.GetErrors(sessionID)
	if err != nil {
		return err
	}

	// Filter by category if specified.
	if opts.Category != "" {
		cat := session.ErrorCategory(opts.Category)
		var filtered []session.SessionError
		for _, e := range errors {
			if e.Category == cat {
				filtered = append(filtered, e)
			}
		}
		errors = filtered
	}

	if opts.JSON {
		summary, _ := errSvc.GetSummary(sessionID)
		result := struct {
			SessionID string                       `json:"session_id"`
			Errors    []session.SessionError       `json:"errors"`
			Summary   *session.SessionErrorSummary `json:"summary,omitempty"`
		}{
			SessionID: opts.SessionID,
			Errors:    errors,
			Summary:   summary,
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	if len(errors) == 0 {
		fmt.Fprintln(out, "No errors found for this session.")
		return nil
	}

	// Summary header.
	summary, _ := errSvc.GetSummary(sessionID)
	if summary != nil {
		provCount := summary.BySource[session.ErrorSourceProvider]
		toolCount := summary.BySource[session.ErrorSourceTool]
		clientCount := summary.BySource[session.ErrorSourceClient]
		fmt.Fprintf(out, "Errors — %d total (%d provider, %d tool, %d client)\n\n",
			summary.TotalErrors, provCount, toolCount, clientCount)
	} else {
		fmt.Fprintf(out, "Errors — %d total\n\n", len(errors))
	}

	// Table.
	fmt.Fprintf(out, "  %-18s  %-10s  %-8s  %s\n",
		"CATEGORY", "SOURCE", "CODE", "MESSAGE")
	fmt.Fprintf(out, "  %-18s  %-10s  %-8s  %s\n",
		"──────────────────", "──────────", "────────", strings.Repeat("─", 40))

	for _, e := range errors {
		code := "—"
		if e.HTTPStatus > 0 {
			code = fmt.Sprintf("%d", e.HTTPStatus)
		}
		msg := truncate(e.Message, 60)
		fmt.Fprintf(out, "  %-18s  %-10s  %-8s  %s\n",
			string(e.Category), string(e.Source), code, msg)
	}

	return nil
}

func runRecentErrors(opts *Options, errSvc service.ErrorServicer, out io.Writer) error {
	cat := session.ErrorCategory(opts.Category)
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}

	errors, err := errSvc.ListRecent(limit, cat)
	if err != nil {
		return err
	}

	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(errors)
	}

	if len(errors) == 0 {
		fmt.Fprintln(out, "No recent errors found.")
		return nil
	}

	fmt.Fprintf(out, "Recent Errors — %d results\n\n", len(errors))
	fmt.Fprintf(out, "  %-12s  %-18s  %-10s  %-8s  %s\n",
		"SESSION", "CATEGORY", "SOURCE", "CODE", "MESSAGE")
	fmt.Fprintf(out, "  %-12s  %-18s  %-10s  %-8s  %s\n",
		"────────────", "──────────────────", "──────────", "────────", strings.Repeat("─", 40))

	for _, e := range errors {
		code := "—"
		if e.HTTPStatus > 0 {
			code = fmt.Sprintf("%d", e.HTTPStatus)
		}
		msg := truncate(e.Message, 50)
		sid := truncate(string(e.SessionID), 12)
		fmt.Fprintf(out, "  %-12s  %-18s  %-10s  %-8s  %s\n",
			sid, string(e.Category), string(e.Source), code, msg)
	}

	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}
