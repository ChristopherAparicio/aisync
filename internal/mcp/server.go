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
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// Config holds the configuration for creating an MCP server.
type Config struct {
	SessionService      service.SessionServicer
	SyncService         *service.SyncService     // optional — nil when git sync is unavailable
	ErrorService        service.ErrorServicer    // optional — nil when error service is unavailable
	SessionEventService *sessionevent.Service    // optional — nil when event analytics is unavailable
	RegistryService     *service.RegistryService // optional — nil when registry unavailable (needed for skill observation)
	Store               storage.Store            // optional — nil when store is unavailable (needed for inspect + stored recommendations)
	Version             string                   // aisync version string
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
		sessionSvc:      cfg.SessionService,
		syncSvc:         cfg.SyncService,
		errorSvc:        cfg.ErrorService,
		sessionEventSvc: cfg.SessionEventService,
		registrySvc:     cfg.RegistryService,
		store:           cfg.Store,
	}

	registerSessionTools(s, h)
	registerSyncTools(s, h)
	registerErrorTools(s, h)
	registerEventTools(s, h)
	registerInspectTools(s, h)
	registerObservabilityTools(s, h)

	return s
}

// handlers holds service references for tool handler closures.
type handlers struct {
	sessionSvc      service.SessionServicer
	syncSvc         *service.SyncService
	errorSvc        service.ErrorServicer
	sessionEventSvc *sessionevent.Service
	registrySvc     *service.RegistryService // optional — needed for skill observation
	store           storage.Store            // optional — needed for inspect diagnostics + stored recommendations
}

// registerSessionTools registers all session-related MCP tools.
func registerSessionTools(s *server.MCPServer, h *handlers) {
	// ── Capture ──
	s.AddTool(mcp.NewTool("aisync_capture",
		mcp.WithDescription("Capture the current AI session and store it in aisync. Detects the active AI provider, exports the session, and saves it locally. If session_id is provided, captures that specific session instead of auto-detecting."),
		mcp.WithString("project_path", mcp.Required(), mcp.Description("Absolute path to the project directory")),
		mcp.WithString("branch", mcp.Required(), mcp.Description("Git branch name")),
		mcp.WithString("mode", mcp.Description("Storage mode: full, compact, or summary (default: full)")),
		mcp.WithString("provider", mcp.Description("AI provider name: claude-code, opencode, or cursor (default: auto-detect)")),
		mcp.WithString("message", mcp.Description("Optional summary message for the session")),
		mcp.WithString("session_id", mcp.Description("Provider-native session ID to capture (e.g. OpenCode session UUID). If omitted, auto-detects the most recent session.")),
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

	// ── Explain ──
	s.AddTool(mcp.NewTool("aisync_explain",
		mcp.WithDescription("Generate an AI-powered explanation of a captured session. The explanation covers the goal, approach, files changed, decisions made, and outcome. Requires an LLM client (Claude CLI in PATH)."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID to explain")),
		mcp.WithString("model", mcp.Description("LLM model to use (default: adapter default)")),
		mcp.WithBoolean("short", mcp.Description("If true, produce a brief 2-3 sentence summary")),
	), h.handleExplain)

	// ── Rewind ──
	s.AddTool(mcp.NewTool("aisync_rewind",
		mcp.WithDescription("Fork a session at a specific message index. Creates a new session with only the first N messages. The original session is never modified."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID to rewind")),
		mcp.WithNumber("at_message", mcp.Required(), mcp.Description("Truncate at this message index (1-based, inclusive)")),
	), h.handleRewind)

	// ── Stats ──
	s.AddTool(mcp.NewTool("aisync_stats",
		mcp.WithDescription("Get aggregated statistics about captured sessions: total counts, token usage, per-branch breakdown, per-provider counts, and top files. Use include_tools to see aggregated tool usage."),
		mcp.WithString("project_path", mcp.Description("Filter by project path")),
		mcp.WithString("branch", mcp.Description("Filter by branch")),
		mcp.WithString("provider", mcp.Description("Filter by provider")),
		mcp.WithBoolean("all", mcp.Description("Include all sessions")),
		mcp.WithBoolean("include_tools", mcp.Description("Include aggregated tool usage across sessions")),
	), h.handleStats)

	// ── Cost ──
	s.AddTool(mcp.NewTool("aisync_cost",
		mcp.WithDescription("Estimate the monetary cost of a captured session based on model pricing. Returns per-model cost breakdown."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Session ID or commit SHA")),
	), h.handleCost)

	// ── Tool Usage ──
	s.AddTool(mcp.NewTool("aisync_tool_usage",
		mcp.WithDescription("Get per-tool token usage breakdown for a session. Shows call count, token consumption, error rate, and average duration for each tool."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Session ID or commit SHA")),
	), h.handleToolUsage)

	// ── Efficiency ──
	s.AddTool(mcp.NewTool("aisync_efficiency",
		mcp.WithDescription("Generate an LLM-powered efficiency analysis of a session. Evaluates token waste, tool usage patterns, and provides a score (0-100) with actionable suggestions. Requires an LLM client."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID to analyze")),
		mcp.WithString("model", mcp.Description("LLM model to use (default: adapter default)")),
	), h.handleEfficiency)

	// ── Diff ──
	s.AddTool(mcp.NewTool("aisync_diff",
		mcp.WithDescription("Compare two sessions side-by-side. Shows token delta, cost delta, file overlap, tool usage comparison, and message divergence point."),
		mcp.WithString("left_id", mcp.Required(), mcp.Description("Left session ID or commit SHA")),
		mcp.WithString("right_id", mcp.Required(), mcp.Description("Right session ID or commit SHA")),
	), h.handleDiff)

	// ── Garbage Collection ──
	s.AddTool(mcp.NewTool("aisync_gc",
		mcp.WithDescription("Remove old sessions based on age and/or count policies. Use --dry-run to preview what would be deleted."),
		mcp.WithString("older_than", mcp.Description("Delete sessions older than this duration (e.g. 30d, 24h, 7d12h)")),
		mcp.WithNumber("keep_latest", mcp.Description("Keep only the N most recent sessions per branch")),
		mcp.WithBoolean("dry_run", mcp.Description("If true, count but don't delete")),
	), h.handleGC)

	// ── Off-Topic Detection ──
	s.AddTool(mcp.NewTool("aisync_off_topic",
		mcp.WithDescription("Detect off-topic sessions on a branch by comparing file overlap. Sessions whose files don't overlap with other sessions are flagged. Useful for identifying unrelated work mixed into a feature branch."),
		mcp.WithString("branch", mcp.Required(), mcp.Description("Git branch to analyze")),
		mcp.WithString("project_path", mcp.Description("Filter by project path")),
		mcp.WithNumber("threshold", mcp.Description("Overlap threshold (0.0-1.0). Sessions below this are off-topic (default: 0.2)")),
	), h.handleOffTopic)

	// ── Cost Forecast ──
	s.AddTool(mcp.NewTool("aisync_forecast",
		mcp.WithDescription("Forecast future AI costs based on historical session data. Analyzes spending trends, projects 30/90 day costs, and recommends cheaper model alternatives."),
		mcp.WithString("project_path", mcp.Description("Filter by project path")),
		mcp.WithString("branch", mcp.Description("Filter by branch")),
		mcp.WithString("period", mcp.Description("Bucketing period: 'daily' or 'weekly' (default: weekly)")),
		mcp.WithNumber("days", mcp.Description("Look-back window in days (default: 90)")),
	), h.handleForecast)

	// ── Validate ──
	s.AddTool(mcp.NewTool("aisync_validate",
		mcp.WithDescription("Validate a session's message structure for integrity issues. Detects orphan tool_use blocks (tool called but no tool_result returned), consecutive same-role messages, pending tool calls, and other structural problems that break the Anthropic Messages API. Returns issues with severity, affected message index, and suggested rewind point."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID to validate")),
		mcp.WithBoolean("fix", mcp.Description("If true, auto-fix by rewinding to before the first error")),
	), h.handleValidate)

	// ── Ingest ──
	s.AddTool(mcp.NewTool("aisync_ingest",
		mcp.WithDescription("Push a session into aisync from an external client (e.g. voice assistant, custom agent, Ollama). This is the simplest path — no provider detection or file-system reads. Requires at least a provider name and one message."),
		mcp.WithString("provider", mcp.Required(), mcp.Description("Provider name: parlay, ollama, claude-code, opencode, or cursor")),
		mcp.WithString("messages_json", mcp.Required(), mcp.Description("JSON array of messages. Each message: {\"role\":\"user\"|\"assistant\"|\"system\", \"content\":\"...\", \"model\":\"...\", \"tool_calls\":[{\"name\":\"...\",\"input\":\"...\",\"output\":\"...\"}], \"input_tokens\":N, \"output_tokens\":N}")),
		mcp.WithString("agent", mcp.Description("Agent name (e.g. 'jarvis'). Defaults to provider name.")),
		mcp.WithString("project_path", mcp.Description("Absolute path to the project directory")),
		mcp.WithString("branch", mcp.Description("Git branch name")),
		mcp.WithString("summary", mcp.Description("One-line summary of the session")),
		mcp.WithString("session_id", mcp.Description("Custom session ID (UUID). Auto-generated if omitted.")),
		mcp.WithString("remote_url", mcp.Description("Git remote URL (e.g. 'github.com/org/repo'). Auto-detected from git if omitted.")),
		mcp.WithString("delegated_from_session_id", mcp.Description("If set, creates a delegated_from link to this parent session")),
	), h.handleIngest)
}

// registerErrorTools registers error-related MCP tools.
func registerErrorTools(s *server.MCPServer, h *handlers) {
	s.AddTool(mcp.NewTool("aisync_errors",
		mcp.WithDescription("Get classified errors for a session or recent errors across all sessions. Errors are categorized as: provider_error, rate_limit, context_overflow, auth_error, validation, tool_error, network_error, aborted, unknown."),
		mcp.WithString("session_id", mcp.Description("Session ID to get errors for")),
		mcp.WithBoolean("recent", mcp.Description("If true, show recent errors across all sessions")),
		mcp.WithString("category", mcp.Description("Filter by error category (e.g. tool_error, provider_error, rate_limit)")),
		mcp.WithNumber("limit", mcp.Description("Max errors to return (default: 50)")),
	), h.handleErrors)
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
