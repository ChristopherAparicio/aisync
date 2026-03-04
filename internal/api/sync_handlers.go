package api

import "net/http"

// ── Sync Push ──

// syncPushRequest is the JSON body for POST /api/v1/sync/push.
type syncPushRequest struct {
	Remote bool `json:"remote,omitempty"`
}

func (s *Server) handleSyncPush(w http.ResponseWriter, r *http.Request) {
	if s.syncSvc == nil {
		writeErrorWithCode(w, http.StatusServiceUnavailable, "sync_unavailable", "sync service is not available (git not configured)")
		return
	}

	var req syncPushRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	result, err := s.syncSvc.Push(req.Remote)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Sync Pull ──

// syncPullRequest is the JSON body for POST /api/v1/sync/pull.
type syncPullRequest struct {
	Remote bool `json:"remote,omitempty"`
}

func (s *Server) handleSyncPull(w http.ResponseWriter, r *http.Request) {
	if s.syncSvc == nil {
		writeErrorWithCode(w, http.StatusServiceUnavailable, "sync_unavailable", "sync service is not available (git not configured)")
		return
	}

	var req syncPullRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	result, err := s.syncSvc.Pull(req.Remote)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Sync Sync ──

// syncSyncRequest is the JSON body for POST /api/v1/sync/sync.
type syncSyncRequest struct {
	Remote bool `json:"remote,omitempty"`
}

func (s *Server) handleSyncSync(w http.ResponseWriter, r *http.Request) {
	if s.syncSvc == nil {
		writeErrorWithCode(w, http.StatusServiceUnavailable, "sync_unavailable", "sync service is not available (git not configured)")
		return
	}

	var req syncSyncRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	result, err := s.syncSvc.Sync(req.Remote)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Sync Index ──

func (s *Server) handleSyncIndex(w http.ResponseWriter, r *http.Request) {
	if s.syncSvc == nil {
		writeErrorWithCode(w, http.StatusServiceUnavailable, "sync_unavailable", "sync service is not available (git not configured)")
		return
	}

	index, err := s.syncSvc.ReadIndex()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, index)
}
