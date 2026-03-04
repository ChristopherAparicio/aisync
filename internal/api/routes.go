package api

import "net/http"

// registerRoutes registers all API routes on the given ServeMux.
// Uses Go 1.22+ method-based routing patterns: "METHOD /path".
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// Health
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)

	// Sessions
	mux.HandleFunc("POST /api/v1/sessions/capture", s.handleCapture)
	mux.HandleFunc("POST /api/v1/sessions/restore", s.handleRestore)
	mux.HandleFunc("GET /api/v1/sessions/{id}", s.handleGetSession)
	mux.HandleFunc("GET /api/v1/sessions", s.handleListSessions)
	mux.HandleFunc("DELETE /api/v1/sessions/{id}", s.handleDeleteSession)
	mux.HandleFunc("POST /api/v1/sessions/export", s.handleExport)
	mux.HandleFunc("POST /api/v1/sessions/import", s.handleImport)
	mux.HandleFunc("POST /api/v1/sessions/link", s.handleLink)
	mux.HandleFunc("POST /api/v1/sessions/comment", s.handleComment)

	// Search
	mux.HandleFunc("GET /api/v1/sessions/search", s.handleSearch)

	// Explain & Rewind
	mux.HandleFunc("POST /api/v1/sessions/explain", s.handleExplain)
	mux.HandleFunc("POST /api/v1/sessions/rewind", s.handleRewind)

	// Blame
	mux.HandleFunc("GET /api/v1/blame", s.handleBlame)

	// Stats & Cost
	mux.HandleFunc("GET /api/v1/stats", s.handleStats)
	mux.HandleFunc("GET /api/v1/sessions/{id}/cost", s.handleCost)

	// Sync
	mux.HandleFunc("POST /api/v1/sync/push", s.handleSyncPush)
	mux.HandleFunc("POST /api/v1/sync/pull", s.handleSyncPull)
	mux.HandleFunc("POST /api/v1/sync/sync", s.handleSyncSync)
	mux.HandleFunc("GET /api/v1/sync/index", s.handleSyncIndex)
}

// handleHealth returns a simple health check.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
