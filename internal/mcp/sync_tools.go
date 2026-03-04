package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// ── Push ──

func (h *handlers) handlePush(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if h.syncSvc == nil {
		return toolError(fmt.Errorf("sync service is not available (git not configured)")), nil
	}

	var args struct {
		Remote bool `json:"remote"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	result, err := h.syncSvc.Push(args.Remote)
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

// ── Pull ──

func (h *handlers) handlePull(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if h.syncSvc == nil {
		return toolError(fmt.Errorf("sync service is not available (git not configured)")), nil
	}

	var args struct {
		Remote bool `json:"remote"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	result, err := h.syncSvc.Pull(args.Remote)
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

// ── Sync ──

func (h *handlers) handleSync(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if h.syncSvc == nil {
		return toolError(fmt.Errorf("sync service is not available (git not configured)")), nil
	}

	var args struct {
		Remote bool `json:"remote"`
	}
	if err := req.BindArguments(&args); err != nil {
		return toolError(err), nil
	}

	result, err := h.syncSvc.Sync(args.Remote)
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(result)
}

// ── Index ──

func (h *handlers) handleIndex(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if h.syncSvc == nil {
		return toolError(fmt.Errorf("sync service is not available (git not configured)")), nil
	}

	index, err := h.syncSvc.ReadIndex()
	if err != nil {
		return toolError(err), nil
	}

	return toolJSON(index)
}
