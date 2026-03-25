package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Capture ──

func (h *handlers) handleCapture(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		ProjectPath string `json:"project_path"`
		Branch      string `json:"branch"`
		Mode        string `json:"mode"`
		Provider    string `json:"provider"`
		Message     string `json:"message"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	mode := session.StorageModeFull
	if args.Mode != "" {
		parsed, err := session.ParseStorageMode(args.Mode)
		if err != nil {
			return toolError(err), nil
		}
		mode = parsed
	}

	var providerName session.ProviderName
	if args.Provider != "" {
		parsed, err := session.ParseProviderName(args.Provider)
		if err != nil {
			return toolError(err), nil
		}
		providerName = parsed
	}

	result, err := h.sessionSvc.Capture(service.CaptureRequest{
		ProjectPath:  args.ProjectPath,
		Branch:       args.Branch,
		Mode:         mode,
		ProviderName: providerName,
		Message:      args.Message,
	})
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

// ── Restore ──

func (h *handlers) handleRestore(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		SessionID   string `json:"session_id"`
		ProjectPath string `json:"project_path"`
		Branch      string `json:"branch"`
		Provider    string `json:"provider"`
		Agent       string `json:"agent"`
		AsContext   bool   `json:"as_context"`
		PRNumber    int    `json:"pr_number"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	var providerName session.ProviderName
	if args.Provider != "" {
		parsed, err := session.ParseProviderName(args.Provider)
		if err != nil {
			return toolError(err), nil
		}
		providerName = parsed
	}

	result, err := h.sessionSvc.Restore(service.RestoreRequest{
		SessionID:    session.ID(args.SessionID),
		ProjectPath:  args.ProjectPath,
		Branch:       args.Branch,
		ProviderName: providerName,
		Agent:        args.Agent,
		AsContext:    args.AsContext,
		PRNumber:     args.PRNumber,
	})
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

// ── Get ──

func (h *handlers) handleGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return toolError(err), nil
	}

	sess, err := h.sessionSvc.Get(id)
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(sess)
}

// ── List ──

func (h *handlers) handleList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		ProjectPath string `json:"project_path"`
		Branch      string `json:"branch"`
		Provider    string `json:"provider"`
		PRNumber    int    `json:"pr_number"`
		All         bool   `json:"all"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	var providerName session.ProviderName
	if args.Provider != "" {
		parsed, err := session.ParseProviderName(args.Provider)
		if err != nil {
			return toolError(err), nil
		}
		providerName = parsed
	}

	summaries, err := h.sessionSvc.List(service.ListRequest{
		ProjectPath: args.ProjectPath,
		Branch:      args.Branch,
		Provider:    providerName,
		PRNumber:    args.PRNumber,
		All:         args.All,
	})
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(summaries)
}

// ── Delete ──

func (h *handlers) handleDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return toolError(err), nil
	}

	sid, err := session.ParseID(id)
	if err != nil {
		return toolError(err), nil
	}

	if err := h.sessionSvc.Delete(sid); err != nil {
		return toolError(err), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Session %s deleted", id)), nil
}

// ── Export ──

func (h *handlers) handleExport(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		SessionID   string `json:"session_id"`
		ProjectPath string `json:"project_path"`
		Branch      string `json:"branch"`
		Format      string `json:"format"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	result, err := h.sessionSvc.Export(service.ExportRequest{
		SessionID:   session.ID(args.SessionID),
		ProjectPath: args.ProjectPath,
		Branch:      args.Branch,
		Format:      args.Format,
	})
	if err != nil {
		return toolError(err), nil
	}

	// Return the exported data as text (it's already formatted JSON/markdown)
	return mcp.NewToolResultText(string(result.Data)), nil
}

// ── Import ──

func (h *handlers) handleImport(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		Data         string `json:"data"`
		SourceFormat string `json:"source_format"`
		IntoTarget   string `json:"into_target"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	result, err := h.sessionSvc.Import(service.ImportRequest{
		Data:         []byte(args.Data),
		SourceFormat: args.SourceFormat,
		IntoTarget:   args.IntoTarget,
	})
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

// ── Link ──

func (h *handlers) handleLink(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		SessionID   string `json:"session_id"`
		ProjectPath string `json:"project_path"`
		Branch      string `json:"branch"`
		PRNumber    int    `json:"pr_number"`
		CommitSHA   string `json:"commit_sha"`
		AutoDetect  bool   `json:"auto_detect"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	result, err := h.sessionSvc.Link(service.LinkRequest{
		SessionID:   session.ID(args.SessionID),
		ProjectPath: args.ProjectPath,
		Branch:      args.Branch,
		PRNumber:    args.PRNumber,
		CommitSHA:   args.CommitSHA,
		AutoDetect:  args.AutoDetect,
	})
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

// ── Comment ──

func (h *handlers) handleComment(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		SessionID   string `json:"session_id"`
		ProjectPath string `json:"project_path"`
		Branch      string `json:"branch"`
		PRNumber    int    `json:"pr_number"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	result, err := h.sessionSvc.Comment(service.CommentRequest{
		SessionID:   session.ID(args.SessionID),
		ProjectPath: args.ProjectPath,
		Branch:      args.Branch,
		PRNumber:    args.PRNumber,
	})
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

// ── Search ──

func (h *handlers) handleSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		Keyword     string  `json:"keyword"`
		ProjectPath string  `json:"project_path"`
		Branch      string  `json:"branch"`
		Provider    string  `json:"provider"`
		OwnerID     string  `json:"owner_id"`
		Since       string  `json:"since"`
		Until       string  `json:"until"`
		Limit       float64 `json:"limit"`
		Offset      float64 `json:"offset"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	var providerName session.ProviderName
	if args.Provider != "" {
		parsed, err := session.ParseProviderName(args.Provider)
		if err != nil {
			return toolError(err), nil
		}
		providerName = parsed
	}

	result, err := h.sessionSvc.Search(service.SearchRequest{
		Keyword:     args.Keyword,
		ProjectPath: args.ProjectPath,
		Branch:      args.Branch,
		Provider:    providerName,
		OwnerID:     session.ID(args.OwnerID),
		Since:       args.Since,
		Until:       args.Until,
		Limit:       int(args.Limit),
		Offset:      int(args.Offset),
	})
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

// ── Blame ──

func (h *handlers) handleBlame(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		File     string `json:"file"`
		Branch   string `json:"branch"`
		Provider string `json:"provider"`
		All      bool   `json:"all"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	if args.File == "" {
		return toolError(fmt.Errorf("file parameter is required")), nil
	}

	var providerName session.ProviderName
	if args.Provider != "" {
		parsed, err := session.ParseProviderName(args.Provider)
		if err != nil {
			return toolError(err), nil
		}
		providerName = parsed
	}

	result, err := h.sessionSvc.Blame(ctx, service.BlameRequest{
		FilePath: args.File,
		Branch:   args.Branch,
		Provider: providerName,
		All:      args.All,
	})
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

// ── Explain ──

func (h *handlers) handleExplain(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		SessionID string `json:"session_id"`
		Model     string `json:"model"`
		Short     bool   `json:"short"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	if args.SessionID == "" {
		return toolError(fmt.Errorf("session_id is required")), nil
	}

	sid, err := session.ParseID(args.SessionID)
	if err != nil {
		return toolError(err), nil
	}

	result, err := h.sessionSvc.Explain(ctx, service.ExplainRequest{
		SessionID: sid,
		Model:     args.Model,
		Short:     args.Short,
	})
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

// ── Rewind ──

func (h *handlers) handleRewind(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		SessionID string  `json:"session_id"`
		AtMessage float64 `json:"at_message"` // MCP numbers are float64
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	if args.SessionID == "" {
		return toolError(fmt.Errorf("session_id is required")), nil
	}

	sid, err := session.ParseID(args.SessionID)
	if err != nil {
		return toolError(err), nil
	}

	atMessage := int(args.AtMessage)
	if atMessage < 1 {
		return toolError(fmt.Errorf("at_message must be >= 1")), nil
	}

	result, err := h.sessionSvc.Rewind(ctx, service.RewindRequest{
		SessionID: sid,
		AtMessage: atMessage,
	})
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

// ── Stats ──

func (h *handlers) handleStats(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		ProjectPath  string `json:"project_path"`
		Branch       string `json:"branch"`
		Provider     string `json:"provider"`
		All          bool   `json:"all"`
		IncludeTools bool   `json:"include_tools"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	var providerName session.ProviderName
	if args.Provider != "" {
		parsed, err := session.ParseProviderName(args.Provider)
		if err != nil {
			return toolError(err), nil
		}
		providerName = parsed
	}

	result, err := h.sessionSvc.Stats(service.StatsRequest{
		ProjectPath:  args.ProjectPath,
		Branch:       args.Branch,
		Provider:     providerName,
		All:          args.All,
		IncludeTools: args.IncludeTools,
	})
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

// ── Cost ──

func (h *handlers) handleCost(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return toolError(err), nil
	}

	est, err := h.sessionSvc.EstimateCost(ctx, id)
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(est)
}

// ── Tool Usage ──

func (h *handlers) handleToolUsage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return toolError(err), nil
	}

	result, err := h.sessionSvc.ToolUsage(ctx, id)
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

// ── Efficiency ──

func (h *handlers) handleEfficiency(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		SessionID string `json:"session_id"`
		Model     string `json:"model"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	if args.SessionID == "" {
		return toolError(fmt.Errorf("session_id is required")), nil
	}

	sid, err := session.ParseID(args.SessionID)
	if err != nil {
		return toolError(err), nil
	}

	result, err := h.sessionSvc.AnalyzeEfficiency(ctx, service.EfficiencyRequest{
		SessionID: sid,
		Model:     args.Model,
	})
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

// ── Garbage Collection ──

func (h *handlers) handleGC(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		OlderThan  string  `json:"older_than"`
		KeepLatest float64 `json:"keep_latest"` // MCP numbers are float64
		DryRun     bool    `json:"dry_run"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	result, err := h.sessionSvc.GarbageCollect(ctx, service.GCRequest{
		OlderThan:  args.OlderThan,
		KeepLatest: int(args.KeepLatest),
		DryRun:     args.DryRun,
	})
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

// ── Diff ──

func (h *handlers) handleDiff(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		LeftID  string `json:"left_id"`
		RightID string `json:"right_id"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	result, err := h.sessionSvc.Diff(ctx, service.DiffRequest{
		LeftID:  args.LeftID,
		RightID: args.RightID,
	})
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

// ── Off-Topic Detection ──

func (h *handlers) handleOffTopic(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		Branch      string  `json:"branch"`
		ProjectPath string  `json:"project_path"`
		Threshold   float64 `json:"threshold"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	result, err := h.sessionSvc.DetectOffTopic(ctx, service.OffTopicRequest{
		ProjectPath: args.ProjectPath,
		Branch:      args.Branch,
		Threshold:   args.Threshold,
	})
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

func (h *handlers) handleForecast(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		ProjectPath string  `json:"project_path"`
		Branch      string  `json:"branch"`
		Period      string  `json:"period"`
		Days        float64 `json:"days"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	result, err := h.sessionSvc.Forecast(ctx, service.ForecastRequest{
		ProjectPath: args.ProjectPath,
		Branch:      args.Branch,
		Period:      args.Period,
		Days:        int(args.Days),
	})
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

// ── Ingest ──

func (h *handlers) handleIngest(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		Provider               string `json:"provider"`
		MessagesJSON           string `json:"messages_json"`
		Agent                  string `json:"agent"`
		ProjectPath            string `json:"project_path"`
		Branch                 string `json:"branch"`
		Summary                string `json:"summary"`
		SessionID              string `json:"session_id"`
		RemoteURL              string `json:"remote_url"`
		DelegatedFromSessionID string `json:"delegated_from_session_id"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	if args.Provider == "" {
		return toolError(fmt.Errorf("provider is required")), nil
	}
	if args.MessagesJSON == "" {
		return toolError(fmt.Errorf("messages_json is required")), nil
	}

	// Parse the messages JSON array.
	var messages []service.IngestMessage
	if err := json.Unmarshal([]byte(args.MessagesJSON), &messages); err != nil {
		return toolError(fmt.Errorf("invalid messages_json: %w", err)), nil
	}

	result, err := h.sessionSvc.Ingest(ctx, service.IngestRequest{
		Provider:               args.Provider,
		Messages:               messages,
		Agent:                  args.Agent,
		ProjectPath:            args.ProjectPath,
		Branch:                 args.Branch,
		Summary:                args.Summary,
		SessionID:              args.SessionID,
		RemoteURL:              args.RemoteURL,
		DelegatedFromSessionID: args.DelegatedFromSessionID,
	})
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

func (h *handlers) handleValidate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		SessionID string `json:"session_id"`
		Fix       bool   `json:"fix"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}
	if args.SessionID == "" {
		return toolError(fmt.Errorf("session_id is required")), nil
	}

	sess, err := h.sessionSvc.Get(args.SessionID)
	if err != nil {
		return toolError(err), nil
	}

	result := session.Validate(sess)

	// Auto-fix if requested
	if args.Fix && result.SuggestedRewindTo > 0 {
		parsedID, err := session.ParseID(args.SessionID)
		if err != nil {
			return toolError(err), nil
		}

		rewindResult, err := h.sessionSvc.Rewind(ctx, service.RewindRequest{
			SessionID: parsedID,
			AtMessage: result.SuggestedRewindTo,
		})
		if err != nil {
			return toolError(fmt.Errorf("auto-fix rewind failed: %w", err)), nil
		}

		// Return both validation result and fix info
		response := map[string]any{
			"validation": result,
			"fix_applied": map[string]any{
				"new_session_id":   rewindResult.NewSession.ID,
				"truncated_at":     rewindResult.TruncatedAt,
				"messages_removed": rewindResult.MessagesRemoved,
			},
		}
		return toolJSON(response)
	}

	return toolJSON(result)
}

// ── Errors ──

func (h *handlers) handleErrors(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if h.errorSvc == nil {
		return toolError(fmt.Errorf("error service not available")), nil
	}

	var args struct {
		SessionID string `json:"session_id"`
		Recent    bool   `json:"recent"`
		Category  string `json:"category"`
		Limit     int    `json:"limit"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	// Recent errors across all sessions.
	if args.Recent {
		limit := args.Limit
		if limit <= 0 {
			limit = 50
		}
		cat := session.ErrorCategory(args.Category)
		errors, err := h.errorSvc.ListRecent(limit, cat)
		if err != nil {
			return toolError(err), nil
		}
		return toolJSON(errors)
	}

	// Session-specific errors.
	if args.SessionID == "" {
		return toolError(fmt.Errorf("provide session_id or set recent=true")), nil
	}

	sessionID := session.ID(args.SessionID)
	errors, err := h.errorSvc.GetErrors(sessionID)
	if err != nil {
		return toolError(err), nil
	}

	// Filter by category if specified.
	if args.Category != "" {
		cat := session.ErrorCategory(args.Category)
		var filtered []session.SessionError
		for _, e := range errors {
			if e.Category == cat {
				filtered = append(filtered, e)
			}
		}
		errors = filtered
	}

	summary, _ := h.errorSvc.GetSummary(sessionID)

	result := struct {
		SessionID string                       `json:"session_id"`
		Errors    []session.SessionError       `json:"errors"`
		Summary   *session.SessionErrorSummary `json:"summary,omitempty"`
	}{
		SessionID: args.SessionID,
		Errors:    errors,
		Summary:   summary,
	}

	return toolJSON(result)
}

// ── Helpers ──

// toolError returns an MCP error result from any error.
func toolError(err error) *mcp.CallToolResult {
	return mcp.NewToolResultError(err.Error())
}

// toolJSON marshals v to JSON and returns it as a text result.
func toolJSON(v any) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return toolError(fmt.Errorf("marshal result: %w", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}
