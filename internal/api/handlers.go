package api

import (
	"encoding/base64"
	"net/http"
	"strconv"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Capture ──

// captureRequest is the JSON body for POST /api/v1/sessions/capture.
type captureRequest struct {
	ProjectPath  string `json:"project_path"`
	Branch       string `json:"branch"`
	Mode         string `json:"mode,omitempty"`
	ProviderName string `json:"provider,omitempty"`
	Message      string `json:"message,omitempty"`
}

func (s *Server) handleCapture(w http.ResponseWriter, r *http.Request) {
	var req captureRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	mode := session.StorageModeFull
	if req.Mode != "" {
		parsed, err := session.ParseStorageMode(req.Mode)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		mode = parsed
	}

	var providerName session.ProviderName
	if req.ProviderName != "" {
		parsed, err := session.ParseProviderName(req.ProviderName)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		providerName = parsed
	}

	result, err := s.sessionSvc.Capture(service.CaptureRequest{
		ProjectPath:  req.ProjectPath,
		Branch:       req.Branch,
		Mode:         mode,
		ProviderName: providerName,
		Message:      req.Message,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Restore ──

// restoreRequest is the JSON body for POST /api/v1/sessions/restore.
type restoreRequest struct {
	ProjectPath  string `json:"project_path"`
	Branch       string `json:"branch"`
	Agent        string `json:"agent,omitempty"`
	SessionID    string `json:"session_id"`
	ProviderName string `json:"provider,omitempty"`
	AsContext    bool   `json:"as_context,omitempty"`
	PRNumber     int    `json:"pr_number,omitempty"`
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	var req restoreRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	var providerName session.ProviderName
	if req.ProviderName != "" {
		parsed, err := session.ParseProviderName(req.ProviderName)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		providerName = parsed
	}

	result, err := s.sessionSvc.Restore(service.RestoreRequest{
		ProjectPath:  req.ProjectPath,
		Branch:       req.Branch,
		Agent:        req.Agent,
		SessionID:    session.ID(req.SessionID),
		ProviderName: providerName,
		AsContext:    req.AsContext,
		PRNumber:     req.PRNumber,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Get ──

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "session ID is required")
		return
	}

	sess, err := s.sessionSvc.Get(id)
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, sess)
}

// ── List ──

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var prNumber int
	if prStr := q.Get("pr"); prStr != "" {
		n, err := strconv.Atoi(prStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid pr parameter: "+err.Error())
			return
		}
		prNumber = n
	}

	var provider session.ProviderName
	if p := q.Get("provider"); p != "" {
		parsed, err := session.ParseProviderName(p)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		provider = parsed
	}

	all := q.Get("all") == "true"

	result, err := s.sessionSvc.List(service.ListRequest{
		ProjectPath: q.Get("project_path"),
		Branch:      q.Get("branch"),
		Provider:    provider,
		PRNumber:    prNumber,
		All:         all,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Delete ──

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "session ID is required")
		return
	}

	sid, err := session.ParseID(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.sessionSvc.Delete(sid); err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ── Export ──

// exportRequest is the JSON body for POST /api/v1/sessions/export.
type exportRequest struct {
	SessionID   string `json:"session_id,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Format      string `json:"format,omitempty"`
}

// exportResponse is the JSON response for export. Data is base64-encoded.
type exportResponse struct {
	Data      string `json:"data"`
	Format    string `json:"format"`
	SessionID string `json:"session_id"`
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	var req exportRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	result, err := s.sessionSvc.Export(service.ExportRequest{
		SessionID:   session.ID(req.SessionID),
		ProjectPath: req.ProjectPath,
		Branch:      req.Branch,
		Format:      req.Format,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, exportResponse{
		Data:      base64.StdEncoding.EncodeToString(result.Data),
		Format:    result.Format,
		SessionID: string(result.SessionID),
	})
}

// ── Import ──

// importRequest is the JSON body for POST /api/v1/sessions/import.
// Data is base64-encoded.
type importRequest struct {
	Data         string `json:"data"`
	SourceFormat string `json:"source_format,omitempty"`
	IntoTarget   string `json:"into_target,omitempty"`
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	var req importRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid base64 data: "+err.Error())
		return
	}

	result, err := s.sessionSvc.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: req.SourceFormat,
		IntoTarget:   req.IntoTarget,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Link ──

// linkRequest is the JSON body for POST /api/v1/sessions/link.
type linkRequest struct {
	SessionID   string `json:"session_id,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	Branch      string `json:"branch,omitempty"`
	PRNumber    int    `json:"pr_number,omitempty"`
	CommitSHA   string `json:"commit_sha,omitempty"`
	AutoDetect  bool   `json:"auto_detect,omitempty"`
}

func (s *Server) handleLink(w http.ResponseWriter, r *http.Request) {
	var req linkRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	result, err := s.sessionSvc.Link(service.LinkRequest{
		SessionID:   session.ID(req.SessionID),
		ProjectPath: req.ProjectPath,
		Branch:      req.Branch,
		PRNumber:    req.PRNumber,
		CommitSHA:   req.CommitSHA,
		AutoDetect:  req.AutoDetect,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Comment ──

// commentRequest is the JSON body for POST /api/v1/sessions/comment.
type commentRequest struct {
	SessionID   string `json:"session_id,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	Branch      string `json:"branch,omitempty"`
	PRNumber    int    `json:"pr_number,omitempty"`
}

func (s *Server) handleComment(w http.ResponseWriter, r *http.Request) {
	var req commentRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	result, err := s.sessionSvc.Comment(service.CommentRequest{
		SessionID:   session.ID(req.SessionID),
		ProjectPath: req.ProjectPath,
		Branch:      req.Branch,
		PRNumber:    req.PRNumber,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Search ──

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var provider session.ProviderName
	if p := q.Get("provider"); p != "" {
		parsed, err := session.ParseProviderName(p)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		provider = parsed
	}

	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	result, err := s.sessionSvc.Search(service.SearchRequest{
		Keyword:     q.Get("keyword"),
		ProjectPath: q.Get("project_path"),
		Branch:      q.Get("branch"),
		Provider:    provider,
		OwnerID:     session.ID(q.Get("owner_id")),
		Since:       q.Get("since"),
		Until:       q.Get("until"),
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Stats ──

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var provider session.ProviderName
	if p := q.Get("provider"); p != "" {
		parsed, err := session.ParseProviderName(p)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		provider = parsed
	}

	all := q.Get("all") == "true"

	result, err := s.sessionSvc.Stats(service.StatsRequest{
		ProjectPath: q.Get("project_path"),
		Branch:      q.Get("branch"),
		Provider:    provider,
		All:         all,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}
