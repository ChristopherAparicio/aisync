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

// ── Stats ──

func (h *handlers) handleStats(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		ProjectPath string `json:"project_path"`
		Branch      string `json:"branch"`
		Provider    string `json:"provider"`
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

	result, err := h.sessionSvc.Stats(service.StatsRequest{
		ProjectPath: args.ProjectPath,
		Branch:      args.Branch,
		Provider:    providerName,
		All:         args.All,
	})
	if err != nil {
		return toolError(err), nil
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
