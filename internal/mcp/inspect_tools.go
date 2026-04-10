package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/ChristopherAparicio/aisync/internal/diagnostic"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
)

// registerInspectTools registers session diagnostic MCP tools.
func registerInspectTools(s *server.MCPServer, h *handlers) {
	s.AddTool(mcp.NewTool("aisync_inspect_session",
		mcp.WithDescription("Deep-inspect a session: tokens, images, compactions, commands, tool errors, behavioral patterns, and detected problems. Returns the same comprehensive diagnostic as `aisync inspect --json`. Optionally generates provider-specific fix artefacts for detected problems."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID to inspect")),
		mcp.WithString("section", mcp.Description("Limit to a specific section: tokens, images, compactions, commands, errors, patterns, problems, trend. Omit for full report.")),
		mcp.WithBoolean("generate_fix", mcp.Description("If true, also generate provider-specific fix artefacts for detected problems")),
	), h.handleInspectSession)
}

// inspectResult wraps the full diagnostic output returned by aisync_inspect_session.
type inspectResult struct {
	Report *diagnostic.InspectReport `json:"report"`
	Fixes  *diagnostic.FixSet        `json:"fixes,omitempty"`
}

func (h *handlers) handleInspectSession(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		SessionID   string `json:"session_id"`
		Section     string `json:"section"`
		GenerateFix bool   `json:"generate_fix"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	if args.SessionID == "" {
		return toolError(fmt.Errorf("session_id is required")), nil
	}

	// Retrieve the session.
	sess, err := h.sessionSvc.Get(args.SessionID)
	if err != nil {
		return toolError(fmt.Errorf("session %q not found: %w", args.SessionID, err)), nil
	}

	// Load session events for richer command data.
	var events []sessionevent.Event
	if h.store != nil {
		events, _ = h.store.GetSessionEvents(sess.ID)
	}

	// Build the diagnostic report.
	report := diagnostic.BuildReport(sess, events)

	// Attach historical trend if store is available.
	if h.store != nil {
		attachTrend(report, sess, h.store)
	}

	// If a section filter is specified, zero out the other sections.
	if args.Section != "" {
		report = filterSection(report, args.Section)
	}

	result := &inspectResult{Report: report}

	// Optionally generate fix artefacts.
	if args.GenerateFix {
		provider, _ := session.ParseProviderName(string(sess.Provider))
		if !provider.Valid() {
			provider = session.ProviderOpenCode
		}
		fixSet := diagnostic.GenerateFixes(report, provider)
		result.Fixes = fixSet
	}

	return toolJSON(result)
}

// attachTrend computes historical trend comparison and attaches it to the report.
func attachTrend(report *diagnostic.InspectReport, sess *session.Session, store interface {
	QueryAnalytics(filter session.AnalyticsFilter) ([]session.AnalyticsRow, error)
}) {
	rows, err := store.QueryAnalytics(session.AnalyticsFilter{
		ProjectPath: sess.ProjectPath,
	})
	if err != nil || len(rows) < 3 {
		return
	}

	// Exclude the current session from the baseline.
	var baseline []session.AnalyticsRow
	for _, r := range rows {
		if r.SessionID != sess.ID {
			baseline = append(baseline, r)
		}
	}

	report.Trend = diagnostic.CompareTrend(report, baseline)
}

// filterSection returns a copy of the report with only the requested section populated.
func filterSection(report *diagnostic.InspectReport, section string) *diagnostic.InspectReport {
	filtered := &diagnostic.InspectReport{
		SessionID: report.SessionID,
		Provider:  report.Provider,
		Agent:     report.Agent,
		Messages:  report.Messages,
		UserMsgs:  report.UserMsgs,
		AsstMsgs:  report.AsstMsgs,
	}

	switch section {
	case "tokens":
		filtered.Tokens = report.Tokens
	case "images":
		filtered.Images = report.Images
	case "compactions":
		filtered.Compaction = report.Compaction
	case "commands":
		filtered.Commands = report.Commands
	case "errors":
		filtered.ToolErrors = report.ToolErrors
	case "patterns":
		filtered.Patterns = report.Patterns
	case "problems":
		filtered.Problems = report.Problems
	case "trend":
		filtered.Trend = report.Trend
	default:
		// Unknown section — return full report
		return report
	}

	return filtered
}
