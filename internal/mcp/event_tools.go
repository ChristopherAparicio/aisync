package mcp

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
)

// registerEventTools registers session event analytics MCP tools.
func registerEventTools(s *server.MCPServer, h *handlers) {
	// ── Session Events (micro view) ──
	s.AddTool(mcp.NewTool("aisync_session_events",
		mcp.WithDescription("Get structured events for a session: tool calls, skills loaded, commands executed, errors, and images. This is the micro view — everything that happened in one session."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID to get events for")),
		mcp.WithString("type", mcp.Description("Filter by event type: tool_call, skill_load, agent_detection, error, command, image_usage")),
	), h.handleSessionEvents)

	// ── Session Event Summary ──
	s.AddTool(mcp.NewTool("aisync_session_event_summary",
		mcp.WithDescription("Get a computed summary of events for a session: total counts, tool breakdown, skills loaded, command breakdown, error breakdown by category. Quick overview without listing every event."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID to summarize")),
	), h.handleSessionEventSummary)

	// ── Event Buckets (macro view) ──
	s.AddTool(mcp.NewTool("aisync_event_buckets",
		mcp.WithDescription("Query aggregated event buckets for project-wide analytics. Returns hourly or daily aggregations of tool calls, skills, commands, errors, agents, and images across all sessions. This is the macro view — trends over time for a project."),
		mcp.WithString("project_path", mcp.Description("Filter by project path")),
		mcp.WithString("remote_url", mcp.Description("Filter by git remote URL")),
		mcp.WithString("provider", mcp.Description("Filter by provider: claude-code, opencode, cursor")),
		mcp.WithString("granularity", mcp.Description("Bucket size: '1h' (hourly) or '1d' (daily). Default: 1d")),
		mcp.WithNumber("days", mcp.Description("Look-back window in days (default: 30)")),
	), h.handleEventBuckets)
}

// ── Handlers ──

func (h *handlers) handleSessionEvents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if h.sessionEventSvc == nil {
		return toolError(errNotConfigured("session event service")), nil
	}

	var args struct {
		SessionID string `json:"session_id"`
		Type      string `json:"type"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	sid := session.ID(args.SessionID)

	if args.Type != "" {
		eventType := sessionevent.EventType(args.Type)
		if !eventType.Valid() {
			return toolError(errInvalidParam("type", args.Type, "tool_call, skill_load, agent_detection, error, command, image_usage")), nil
		}
		events, err := h.sessionEventSvc.GetSessionEventsByType(sid, eventType)
		if err != nil {
			return toolError(err), nil
		}
		return toolJSON(map[string]any{
			"session_id": args.SessionID,
			"type":       args.Type,
			"count":      len(events),
			"events":     events,
		})
	}

	events, err := h.sessionEventSvc.GetSessionEvents(sid)
	if err != nil {
		return toolError(err), nil
	}
	return toolJSON(map[string]any{
		"session_id": args.SessionID,
		"count":      len(events),
		"events":     events,
	})
}

func (h *handlers) handleSessionEventSummary(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if h.sessionEventSvc == nil {
		return toolError(errNotConfigured("session event service")), nil
	}

	var args struct {
		SessionID string `json:"session_id"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	summary, err := h.sessionEventSvc.GetSessionSummary(session.ID(args.SessionID))
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(summary)
}

func (h *handlers) handleEventBuckets(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if h.sessionEventSvc == nil {
		return toolError(errNotConfigured("session event service")), nil
	}

	var args struct {
		ProjectPath string  `json:"project_path"`
		RemoteURL   string  `json:"remote_url"`
		Provider    string  `json:"provider"`
		Granularity string  `json:"granularity"`
		Days        float64 `json:"days"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	gran := "1d"
	if args.Granularity == "1h" {
		gran = "1h"
	}

	days := 30
	if args.Days > 0 {
		days = int(args.Days)
	}

	now := time.Now().UTC()
	query := sessionevent.BucketQuery{
		ProjectPath: args.ProjectPath,
		RemoteURL:   args.RemoteURL,
		Provider:    session.ProviderName(args.Provider),
		Granularity: gran,
		Since:       now.AddDate(0, 0, -days),
		Until:       now,
	}

	buckets, err := h.sessionEventSvc.QueryBuckets(query)
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(map[string]any{
		"granularity": gran,
		"days":        days,
		"count":       len(buckets),
		"buckets":     buckets,
	})
}

// ── Error helpers ──

type paramError struct{ field, value, allowed string }

func (e paramError) Error() string {
	return "invalid " + e.field + " " + e.value + ": allowed values are " + e.allowed
}

func errInvalidParam(field, value, allowed string) error {
	return paramError{field, value, allowed}
}

type notConfiguredError struct{ name string }

func (e notConfiguredError) Error() string {
	return e.name + " is not configured"
}

func errNotConfigured(name string) error {
	return notConfiguredError{name}
}
