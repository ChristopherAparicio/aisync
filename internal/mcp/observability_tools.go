package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/skillobs"
)

// registerObservabilityTools registers the three 100%-offline observation
// tools that expose aisync's deterministic analytics engines to any MCP
// client. No LLM is ever called from these handlers — every result is
// derived from stored session metrics and rule-based analysis.
//
// Exposed tools:
//
//   - aisync_recommendations  — GenerateRecommendations() + stored reader
//   - aisync_diagnose         — SessionService.Diagnose() quick scan only
//   - aisync_skill_observation — skillobs.Observe()
//
// These tools are the MCP counterpart to the CLI commands
// `aisync recommend`, `aisync diagnose`, and `aisync skill-observe`.
func registerObservabilityTools(s *server.MCPServer, h *handlers) {
	// ── Recommendations ──
	s.AddTool(mcp.NewTool("aisync_recommendations",
		mcp.WithDescription("List deterministic, rule-based recommendations for a project. 100% offline — no LLM call. By default reads pre-computed recommendations from the store (cheap, instant). Set fresh=true to regenerate on demand (expensive: triggers AgentROI + SkillROI + CacheEfficiency + ContextSaturation analytics)."),
		mcp.WithString("project_path", mcp.Description("Absolute path to the project directory (empty = all projects when reading stored recs)")),
		mcp.WithString("priority", mcp.Description("Filter by priority: high, medium, low")),
		mcp.WithString("status", mcp.Description("Filter by status: active (default), dismissed, snoozed, all. Ignored when fresh=true.")),
		mcp.WithNumber("limit", mcp.Description("Maximum recommendations to return (default: 50)")),
		mcp.WithBoolean("fresh", mcp.Description("If true, regenerate via GenerateRecommendations (expensive). Default: read from store.")),
	), h.handleRecommendations)

	// ── Diagnose ──
	s.AddTool(mcp.NewTool("aisync_diagnose",
		mcp.WithDescription("Produce a unified diagnosis report for a session: health score, overload analysis, error timeline, per-tool report, phase analysis, verdict, and restore advice. 100% offline quick scan — no LLM call. Returns the same data as `aisync diagnose --json` without --deep."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID to diagnose")),
	), h.handleDiagnose)

	// ── Skill Observation ──
	s.AddTool(mcp.NewTool("aisync_skill_observation",
		mcp.WithDescription("Observe skill usage in a session: compares recommended skills (keyword matching on user messages) vs actually loaded skills (from tool-call detection). Returns lists of Available, Recommended, Loaded, Missed (recommended but not loaded — agent/prompt improvement signal), and Discovered (loaded but not recommended — recommender coverage gap). 100% offline — no LLM."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID to observe")),
	), h.handleSkillObservation)
}

// --- aisync_recommendations ---

// recommendationsResult is the JSON envelope returned by aisync_recommendations.
// The two inner slices are mutually exclusive — `stored` is populated when
// reading from the store, `fresh` when regenerating via the service.
type recommendationsResult struct {
	Source      string                         `json:"source"` // "stored" or "fresh"
	ProjectPath string                         `json:"project_path,omitempty"`
	Total       int                            `json:"total"`
	Stats       *session.RecommendationStats   `json:"stats,omitempty"` // only when source == "stored"
	Stored      []session.RecommendationRecord `json:"stored,omitempty"`
	Fresh       []session.Recommendation       `json:"fresh,omitempty"`
}

func (h *handlers) handleRecommendations(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		ProjectPath string  `json:"project_path"`
		Priority    string  `json:"priority"`
		Status      string  `json:"status"`
		Limit       float64 `json:"limit"`
		Fresh       bool    `json:"fresh"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	limit := int(args.Limit)
	if limit <= 0 {
		limit = 50
	}

	if args.Fresh {
		return h.handleRecommendationsFresh(ctx, args.ProjectPath, args.Priority, limit)
	}
	return h.handleRecommendationsStored(args.ProjectPath, args.Priority, args.Status, limit)
}

func (h *handlers) handleRecommendationsFresh(ctx context.Context, projectPath, priority string, limit int) (*mcp.CallToolResult, error) {
	if h.sessionSvc == nil {
		return toolError(fmt.Errorf("session service unavailable")), nil
	}

	recs, err := h.sessionSvc.GenerateRecommendations(ctx, projectPath)
	if err != nil {
		return toolError(fmt.Errorf("generating recommendations: %w", err)), nil
	}

	if priority != "" {
		recs = filterRecommendationsByPriority(recs, priority)
	}
	if limit > 0 && len(recs) > limit {
		recs = recs[:limit]
	}

	return toolJSON(&recommendationsResult{
		Source:      "fresh",
		ProjectPath: projectPath,
		Total:       len(recs),
		Fresh:       recs,
	})
}

func (h *handlers) handleRecommendationsStored(projectPath, priority, status string, limit int) (*mcp.CallToolResult, error) {
	if h.store == nil {
		return toolError(fmt.Errorf("store unavailable — cannot read stored recommendations (use fresh=true to regenerate)")), nil
	}

	filter := session.RecommendationFilter{
		ProjectPath: projectPath,
		Priority:    priority,
		Limit:       limit,
	}
	if status != "" && status != "all" {
		filter.Status = session.RecommendationStatus(status)
	} else if status == "" {
		filter.Status = session.RecStatusActive
	}

	recs, err := h.store.ListRecommendations(filter)
	if err != nil {
		return toolError(fmt.Errorf("listing recommendations: %w", err)), nil
	}

	stats, _ := h.store.RecommendationStats(projectPath)

	return toolJSON(&recommendationsResult{
		Source:      "stored",
		ProjectPath: projectPath,
		Total:       len(recs),
		Stats:       &stats,
		Stored:      recs,
	})
}

func filterRecommendationsByPriority(recs []session.Recommendation, priority string) []session.Recommendation {
	var out []session.Recommendation
	for _, r := range recs {
		if r.Priority == priority {
			out = append(out, r)
		}
	}
	return out
}

// --- aisync_diagnose ---

func (h *handlers) handleDiagnose(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		SessionID string `json:"session_id"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}
	if args.SessionID == "" {
		return toolError(fmt.Errorf("session_id is required")), nil
	}
	if h.sessionSvc == nil {
		return toolError(fmt.Errorf("session service unavailable")), nil
	}

	sid, err := session.ParseID(args.SessionID)
	if err != nil {
		return toolError(fmt.Errorf("invalid session id %q: %w", args.SessionID, err)), nil
	}

	// We intentionally keep Deep=false — this MCP tool is the 100%-offline
	// quick scan. An LLM-powered deep analysis would defeat the purpose.
	report, err := h.sessionSvc.Diagnose(ctx, service.DiagnoseRequest{
		SessionID: sid,
		Deep:      false,
	})
	if err != nil {
		return toolError(fmt.Errorf("diagnosing session: %w", err)), nil
	}

	return toolJSON(report)
}

// --- aisync_skill_observation ---

// skillObservationResult is the JSON envelope returned by aisync_skill_observation.
type skillObservationResult struct {
	SessionID       string      `json:"session_id"`
	ProjectPath     string      `json:"project_path"`
	MessageCount    int         `json:"message_count"`
	CapabilityCount int         `json:"capability_count"`
	Observation     interface{} `json:"observation"` // *analysis.SkillObservation or nil
	Note            string      `json:"note,omitempty"`
}

func (h *handlers) handleSkillObservation(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		SessionID string `json:"session_id"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}
	if args.SessionID == "" {
		return toolError(fmt.Errorf("session_id is required")), nil
	}
	if h.sessionSvc == nil {
		return toolError(fmt.Errorf("session service unavailable")), nil
	}
	if h.registrySvc == nil {
		return toolError(fmt.Errorf("registry service unavailable — cannot load rich capabilities needed by the observer")), nil
	}

	sess, err := h.sessionSvc.Get(args.SessionID)
	if err != nil {
		return toolError(fmt.Errorf("loading session %q: %w", args.SessionID, err)), nil
	}
	if sess == nil {
		return toolError(fmt.Errorf("session %q not found", args.SessionID)), nil
	}

	project, err := h.registrySvc.ScanProject(sess.ProjectPath)
	if err != nil {
		return toolError(fmt.Errorf("scanning project registry at %s: %w", sess.ProjectPath, err)), nil
	}

	observation := skillobs.Observe(sess.Messages, project.Capabilities)

	result := &skillObservationResult{
		SessionID:       args.SessionID,
		ProjectPath:     sess.ProjectPath,
		MessageCount:    len(sess.Messages),
		CapabilityCount: len(project.Capabilities),
		Observation:     observation,
	}
	if observation == nil {
		result.Note = "no skills available for this project"
	}

	return toolJSON(result)
}
