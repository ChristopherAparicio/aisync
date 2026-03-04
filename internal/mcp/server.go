// Package mcp implements an MCP (Model Context Protocol) server for aisync.
// It exposes SessionService and SyncService operations as MCP tools,
// allowing AI assistants (Claude, Cursor, OpenCode) to interact with
// aisync sessions directly through the MCP protocol.
//
// The server communicates over stdio (JSON-RPC 2.0) and is started
// via the `aisync mcp` CLI command.
package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/ChristopherAparicio/aisync/internal/service"
)

// Config holds the configuration for creating an MCP server.
type Config struct {
	SessionService *service.SessionService
	SyncService    *service.SyncService // optional — nil when git sync is unavailable
	Version        string               // aisync version string
}

// NewServer creates a new MCP server with all aisync tools registered.
func NewServer(cfg Config) *server.MCPServer {
	version := cfg.Version
	if version == "" {
		version = "dev"
	}

	s := server.NewMCPServer(
		"aisync",
		version,
		server.WithToolCapabilities(false),
	)

	h := &handlers{
		sessionSvc: cfg.SessionService,
		syncSvc:    cfg.SyncService,
	}

	registerSessionTools(s, h)
	registerSyncTools(s, h)

	return s
}

// handlers holds service references for tool handler closures.
type handlers struct {
	sessionSvc *service.SessionService
	syncSvc    *service.SyncService
}

// registerSessionTools registers all session-related MCP tools.
func registerSessionTools(s *server.MCPServer, h *handlers) {
	// ── Capture ──
	s.AddTool(mcp.NewTool("aisync_capture",
		mcp.WithDescription("Capture the current AI session and store it in aisync. Detects the active AI provider, exports the session, and saves it locally."),
		mcp.WithString("project_path", mcp.Required(), mcp.Description("Absolute path to the project directory")),
		mcp.WithString("branch", mcp.Required(), mcp.Description("Git branch name")),
		mcp.WithString("mode", mcp.Description("Storage mode: full, compact, or summary (default: full)")),
		mcp.WithString("provider", mcp.Description("AI provider name: claude-code, opencode, or cursor (default: auto-detect)")),
		mcp.WithString("message", mcp.Description("Optional summary message for the session")),
	), h.handleCapture)

	// ── Restore ──
	s.AddTool(mcp.NewTool("aisync_restore",
		mcp.WithDescription("Restore an AI session into the current provider. Looks up the session and imports it so the AI assistant can resume the conversation."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID to restore")),
		mcp.WithString("project_path", mcp.Required(), mcp.Description("Absolute path to the project directory")),
		mcp.WithString("branch", mcp.Required(), mcp.Description("Git branch name")),
		mcp.WithString("provider", mcp.Description("Target provider: claude-code, opencode, or cursor")),
		mcp.WithString("agent", mcp.Description("Agent name for the restored session")),
		mcp.WithBoolean("as_context", mcp.Description("If true, restore as context.md instead of native format")),
		mcp.WithNumber("pr_number", mcp.Description("PR number to look up session by (alternative to session_id)")),
	), h.handleRestore)

	// ── Get ──
	s.AddTool(mcp.NewTool("aisync_get",
		mcp.WithDescription("Get detailed information about a specific session by ID or commit SHA."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Session ID or git commit SHA")),
	), h.handleGet)

	// ── List ──
	s.AddTool(mcp.NewTool("aisync_list",
		mcp.WithDescription("List captured AI sessions. Returns session summaries with ID, provider, branch, message count, and token usage."),
		mcp.WithString("project_path", mcp.Description("Filter by project path")),
		mcp.WithString("branch", mcp.Description("Filter by git branch")),
		mcp.WithString("provider", mcp.Description("Filter by provider: claude-code, opencode, or cursor")),
		mcp.WithNumber("pr_number", mcp.Description("Filter by PR number")),
		mcp.WithBoolean("all", mcp.Description("If true, list all sessions across branches")),
	), h.handleList)

	// ── Delete ──
	s.AddTool(mcp.NewTool("aisync_delete",
		mcp.WithDescription("Delete a captured session by ID."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Session ID to delete")),
	), h.handleDelete)

	// ── Export ──
	s.AddTool(mcp.NewTool("aisync_export",
		mcp.WithDescription("Export a session to a specific format. Returns the exported data as a string."),
		mcp.WithString("session_id", mcp.Description("Session ID to export (default: current branch session)")),
		mcp.WithString("project_path", mcp.Description("Project path (used if session_id is empty)")),
		mcp.WithString("branch", mcp.Description("Branch (used if session_id is empty)")),
		mcp.WithString("format", mcp.Description("Output format: aisync, claude, opencode, or context (default: aisync)")),
	), h.handleExport)

	// ── Import ──
	s.AddTool(mcp.NewTool("aisync_import",
		mcp.WithDescription("Import a session from raw data. Parses the data, optionally scans for secrets, and stores it."),
		mcp.WithString("data", mcp.Required(), mcp.Description("Raw session data (JSON string)")),
		mcp.WithString("source_format", mcp.Description("Source format: aisync, claude, or opencode (default: auto-detect)")),
		mcp.WithString("into_target", mcp.Description("Import target: aisync, claude-code, or opencode (default: aisync)")),
	), h.handleImport)

	// ── Link ──
	s.AddTool(mcp.NewTool("aisync_link",
		mcp.WithDescription("Link a session to a PR or commit. Associates the session with a git object for easy lookup."),
		mcp.WithString("session_id", mcp.Description("Session ID (default: resolve from branch)")),
		mcp.WithString("project_path", mcp.Description("Project path")),
		mcp.WithString("branch", mcp.Description("Branch name")),
		mcp.WithNumber("pr_number", mcp.Description("PR number to link")),
		mcp.WithString("commit_sha", mcp.Description("Commit SHA to link")),
		mcp.WithBoolean("auto_detect", mcp.Description("Auto-detect PR from branch")),
	), h.handleLink)

	// ── Comment ──
	s.AddTool(mcp.NewTool("aisync_comment",
		mcp.WithDescription("Post or update a PR comment with an AI session summary. Creates an idempotent comment on the pull request."),
		mcp.WithString("session_id", mcp.Description("Session ID (default: resolve from branch or PR)")),
		mcp.WithString("project_path", mcp.Description("Project path")),
		mcp.WithString("branch", mcp.Description("Branch name")),
		mcp.WithNumber("pr_number", mcp.Description("PR number (default: auto-detect from branch)")),
	), h.handleComment)

	// ── Search ──
	s.AddTool(mcp.NewTool("aisync_search",
		mcp.WithDescription("Search for sessions by keyword, branch, provider, owner, or time range. Returns paginated results with total count."),
		mcp.WithString("keyword", mcp.Description("Text to search for in session summaries (case-insensitive)")),
		mcp.WithString("project_path", mcp.Description("Filter by project path")),
		mcp.WithString("branch", mcp.Description("Filter by git branch")),
		mcp.WithString("provider", mcp.Description("Filter by provider: claude-code, opencode, or cursor")),
		mcp.WithString("owner_id", mcp.Description("Filter by owner (user) ID")),
		mcp.WithString("since", mcp.Description("Only sessions created after this date (RFC3339 or YYYY-MM-DD)")),
		mcp.WithString("until", mcp.Description("Only sessions created before this date (RFC3339 or YYYY-MM-DD)")),
		mcp.WithNumber("limit", mcp.Description("Max results to return (default: 50, max: 200)")),
		mcp.WithNumber("offset", mcp.Description("Offset for pagination")),
	), h.handleSearch)

	// ── Blame ──
	s.AddTool(mcp.NewTool("aisync_blame",
		mcp.WithDescription("Find which AI sessions touched a given file. Reverse lookup from file changes to sessions, like git blame but for AI sessions."),
		mcp.WithString("file", mcp.Required(), mcp.Description("File path relative to project root")),
		mcp.WithString("branch", mcp.Description("Filter by git branch")),
		mcp.WithString("provider", mcp.Description("Filter by provider: claude-code, opencode, or cursor")),
		mcp.WithBoolean("all", mcp.Description("If true, return all sessions (default: most recent only)")),
	), h.handleBlame)

	// ── Stats ──
	s.AddTool(mcp.NewTool("aisync_stats",
		mcp.WithDescription("Get aggregated statistics about captured sessions: total counts, token usage, per-branch breakdown, per-provider counts, and top files."),
		mcp.WithString("project_path", mcp.Description("Filter by project path")),
		mcp.WithString("branch", mcp.Description("Filter by branch")),
		mcp.WithString("provider", mcp.Description("Filter by provider")),
		mcp.WithBoolean("all", mcp.Description("Include all sessions")),
	), h.handleStats)
}

// registerSyncTools registers all sync-related MCP tools.
func registerSyncTools(s *server.MCPServer, h *handlers) {
	// ── Push ──
	s.AddTool(mcp.NewTool("aisync_push",
		mcp.WithDescription("Push sessions to the git sync branch. Exports sessions and optionally pushes to the remote."),
		mcp.WithBoolean("remote", mcp.Description("If true, push to remote after writing to sync branch")),
	), h.handlePush)

	// ── Pull ──
	s.AddTool(mcp.NewTool("aisync_pull",
		mcp.WithDescription("Pull sessions from the git sync branch. Imports sessions from the sync branch into the local store."),
		mcp.WithBoolean("remote", mcp.Description("If true, fetch from remote first")),
	), h.handlePull)

	// ── Sync ──
	s.AddTool(mcp.NewTool("aisync_sync",
		mcp.WithDescription("Sync sessions: pull from sync branch then push local sessions. Combines pull and push in one operation."),
		mcp.WithBoolean("remote", mcp.Description("If true, interact with remote")),
	), h.handleSync)

	// ── Index ──
	s.AddTool(mcp.NewTool("aisync_index",
		mcp.WithDescription("Read the sync branch index. Returns a lightweight list of all sessions available on the sync branch."),
	), h.handleIndex)
}
