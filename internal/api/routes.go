package api

import "net/http"

// RegisterRoutes registers all API routes on the given ServeMux.
// This allows external callers (e.g., a unified server) to mount API routes
// alongside other route sets on a shared mux.
// Uses Go 1.22+ method-based routing patterns: "METHOD /path".
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Health
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)

	// Sessions
	mux.HandleFunc("POST /api/v1/sessions/capture", s.handleCapture)
	mux.HandleFunc("POST /api/v1/sessions/restore", s.handleRestore)
	mux.HandleFunc("GET /api/v1/sessions/{id}/messages", s.handleGetMessageWindow)
	mux.HandleFunc("GET /api/v1/sessions/{id}", s.handleGetSession)
	mux.HandleFunc("GET /api/v1/sessions", s.handleListSessions)
	mux.HandleFunc("DELETE /api/v1/sessions/{id}", s.handleDeleteSession)
	mux.HandleFunc("POST /api/v1/sessions/export", s.handleExport)
	mux.HandleFunc("POST /api/v1/sessions/import", s.handleImport)
	mux.HandleFunc("POST /api/v1/sessions/link", s.handleLink)
	mux.HandleFunc("POST /api/v1/sessions/comment", s.handleComment)

	// Search, Diff & Off-Topic
	mux.HandleFunc("GET /api/v1/sessions/search", s.handleSearch)
	mux.HandleFunc("GET /api/v1/sessions/diff", s.handleDiff)
	mux.HandleFunc("GET /api/v1/sessions/off-topic", s.handleOffTopic)

	// Explain, Rewind & Validate
	mux.HandleFunc("POST /api/v1/sessions/explain", s.handleExplain)
	mux.HandleFunc("POST /api/v1/sessions/rewind", s.handleRewind)
	mux.HandleFunc("GET /api/v1/sessions/{id}/validate", s.handleValidate)
	mux.HandleFunc("POST /api/v1/sessions/{id}/validate", s.handleValidateWithFix)

	// Blame
	mux.HandleFunc("GET /api/v1/blame", s.handleBlame)

	// Garbage Collection
	mux.HandleFunc("POST /api/v1/gc", s.handleGC)

	// Stats, Cost, Tool Usage, Efficiency & Forecast
	mux.HandleFunc("GET /api/v1/stats", s.handleStats)
	mux.HandleFunc("GET /api/v1/stats/forecast", s.handleForecast)
	mux.HandleFunc("GET /api/v1/sessions/{id}/cost", s.handleCost)
	mux.HandleFunc("GET /api/v1/sessions/{id}/tool-usage", s.handleToolUsage)
	mux.HandleFunc("POST /api/v1/sessions/efficiency", s.handleEfficiency)

	// Sync
	mux.HandleFunc("POST /api/v1/sync/push", s.handleSyncPush)
	mux.HandleFunc("POST /api/v1/sync/pull", s.handleSyncPull)
	mux.HandleFunc("POST /api/v1/sync/sync", s.handleSyncSync)
	mux.HandleFunc("GET /api/v1/sync/index", s.handleSyncIndex)

	// Ingest
	mux.HandleFunc("POST /api/v1/ingest", s.handleIngest)
	mux.HandleFunc("POST /api/v1/ingest/ollama", s.handleIngestOllama)

	// Session updates
	mux.HandleFunc("PATCH /api/v1/sessions/{id}", s.handlePatchSession)

	// Trends
	mux.HandleFunc("GET /api/v1/stats/trends", s.handleTrends)

	// Projects
	mux.HandleFunc("GET /api/v1/projects", s.handleListProjects)

	// Analysis (optional — returns 404 if AnalysisService is nil)
	mux.HandleFunc("POST /api/v1/sessions/{id}/analyze", s.handleAnalyze)
	mux.HandleFunc("GET /api/v1/sessions/{id}/analysis", s.handleGetAnalysis)
	mux.HandleFunc("GET /api/v1/sessions/{id}/analyses", s.handleListAnalyses)

	// Session-to-session links (delegation, continuation, related, etc.)
	mux.HandleFunc("POST /api/v1/sessions/session-links", s.handleLinkSessions)
	mux.HandleFunc("GET /api/v1/sessions/{id}/session-links", s.handleGetSessionLinks)
	mux.HandleFunc("DELETE /api/v1/sessions/session-links/{linkID}", s.handleDeleteSessionLink)

	// Session replay (optional — returns 404 if replay engine is nil)
	mux.HandleFunc("POST /api/v1/sessions/{id}/replay", s.handleReplay)

	// Skill resolver (optional — returns 404 if skill resolver is nil)
	mux.HandleFunc("POST /api/v1/sessions/{id}/skills/resolve", s.handleSkillResolve)

	// Errors (optional — returns 404 if error service is nil)
	mux.HandleFunc("GET /api/v1/sessions/{id}/errors/summary", s.handleGetSessionErrorSummary)
	mux.HandleFunc("GET /api/v1/sessions/{id}/errors", s.handleGetSessionErrors)
	mux.HandleFunc("GET /api/v1/errors/recent", s.handleListRecentErrors)

	// Session events (optional — returns 404 if session event service is nil)
	mux.HandleFunc("GET /api/v1/sessions/{id}/events/summary", s.handleGetSessionEventSummary)
	mux.HandleFunc("GET /api/v1/sessions/{id}/events", s.handleGetSessionEvents)
	mux.HandleFunc("GET /api/v1/events/buckets", s.handleQueryEventBuckets)
	mux.HandleFunc("GET /api/v1/events/overview", s.handleGetProjectEventOverview)

	// Authentication (optional — returns 404 if auth service is nil)
	mux.HandleFunc("POST /api/v1/auth/register", s.handleAuthRegister)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleAuthLogin)
	mux.HandleFunc("GET /api/v1/auth/me", s.handleAuthMe)
	mux.HandleFunc("POST /api/v1/auth/keys", s.handleCreateAPIKey)
	mux.HandleFunc("GET /api/v1/auth/keys", s.handleListAPIKeys)
	mux.HandleFunc("DELETE /api/v1/auth/keys/{id}", s.handleDeleteAPIKey)
	mux.HandleFunc("POST /api/v1/auth/keys/{id}/revoke", s.handleRevokeAPIKey)
	mux.HandleFunc("GET /api/v1/auth/users", s.handleListUsers)
}

// handleHealth returns a simple health check.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
