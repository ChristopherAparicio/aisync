package api

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	ollamaingest "github.com/ChristopherAparicio/aisync/internal/ingest/ollama"
	"github.com/ChristopherAparicio/aisync/internal/replay"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/skillresolver"
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

// ── Cost ──

func (s *Server) handleCost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "session ID is required")
		return
	}

	est, err := s.sessionSvc.EstimateCost(r.Context(), id)
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, est)
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
		OwnerID:     q.Get("owner_id"),
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
	voice := q.Get("voice") == "true"

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
		Voice:       voice,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Blame ──

func (s *Server) handleBlame(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	file := q.Get("file")
	if file == "" {
		writeError(w, http.StatusBadRequest, "file parameter is required")
		return
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

	result, err := s.sessionSvc.Blame(r.Context(), service.BlameRequest{
		FilePath: file,
		Branch:   q.Get("branch"),
		Provider: provider,
		All:      all,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Explain ──

type explainRequest struct {
	SessionID string `json:"session_id"`
	Model     string `json:"model,omitempty"`
	Short     bool   `json:"short,omitempty"`
}

func (s *Server) handleExplain(w http.ResponseWriter, r *http.Request) {
	var req explainRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.SessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id is required")
		return
	}

	sid, err := session.ParseID(req.SessionID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := s.sessionSvc.Explain(r.Context(), service.ExplainRequest{
		SessionID: sid,
		Model:     req.Model,
		Short:     req.Short,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Rewind ──

type rewindRequest struct {
	SessionID string `json:"session_id"`
	AtMessage int    `json:"at_message"`
}

func (s *Server) handleRewind(w http.ResponseWriter, r *http.Request) {
	var req rewindRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.SessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id is required")
		return
	}

	sid, err := session.ParseID(req.SessionID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.AtMessage < 1 {
		writeError(w, http.StatusBadRequest, "at_message must be >= 1")
		return
	}

	result, err := s.sessionSvc.Rewind(r.Context(), service.RewindRequest{
		SessionID: sid,
		AtMessage: req.AtMessage,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Validate ──

func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
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

	result := session.Validate(sess)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleValidateWithFix(w http.ResponseWriter, r *http.Request) {
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

	result := session.Validate(sess)

	// If no errors or no rewind suggestion, just return validation result
	if result.Valid || result.SuggestedRewindTo == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"validation":  result,
			"fix_applied": false,
		})
		return
	}

	// Auto-fix: rewind
	sid, err := session.ParseID(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	rewindResult, err := s.sessionSvc.Rewind(r.Context(), service.RewindRequest{
		SessionID: sid,
		AtMessage: result.SuggestedRewindTo,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"validation":  result,
		"fix_applied": true,
		"fix": map[string]any{
			"new_session_id":   rewindResult.NewSession.ID,
			"truncated_at":     rewindResult.TruncatedAt,
			"messages_removed": rewindResult.MessagesRemoved,
		},
	})
}

// ── Tool Usage ──

func (s *Server) handleToolUsage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "session ID is required")
		return
	}

	result, err := s.sessionSvc.ToolUsage(r.Context(), id)
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Efficiency ──

type efficiencyRequest struct {
	SessionID string `json:"session_id"`
	Model     string `json:"model,omitempty"`
}

func (s *Server) handleEfficiency(w http.ResponseWriter, r *http.Request) {
	var req efficiencyRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.SessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id is required")
		return
	}

	sid, err := session.ParseID(req.SessionID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := s.sessionSvc.AnalyzeEfficiency(r.Context(), service.EfficiencyRequest{
		SessionID: sid,
		Model:     req.Model,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Garbage Collection ──

type gcRequest struct {
	OlderThan  string `json:"older_than,omitempty"`
	KeepLatest int    `json:"keep_latest,omitempty"`
	DryRun     bool   `json:"dry_run,omitempty"`
}

func (s *Server) handleGC(w http.ResponseWriter, r *http.Request) {
	var req gcRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	result, err := s.sessionSvc.GarbageCollect(r.Context(), service.GCRequest{
		OlderThan:  req.OlderThan,
		KeepLatest: req.KeepLatest,
		DryRun:     req.DryRun,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Diff ──

func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	left := q.Get("left")
	right := q.Get("right")
	if left == "" || right == "" {
		writeError(w, http.StatusBadRequest, "both left and right query parameters are required")
		return
	}

	result, err := s.sessionSvc.Diff(r.Context(), service.DiffRequest{
		LeftID:  left,
		RightID: right,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Off-Topic Detection ──

func (s *Server) handleOffTopic(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	branch := q.Get("branch")
	if branch == "" {
		writeError(w, http.StatusBadRequest, "branch query parameter is required")
		return
	}

	var threshold float64
	if t := q.Get("threshold"); t != "" {
		parsed, err := strconv.ParseFloat(t, 64)
		if err != nil || parsed < 0 || parsed > 1 {
			writeError(w, http.StatusBadRequest, "threshold must be a number between 0 and 1")
			return
		}
		threshold = parsed
	}

	result, err := s.sessionSvc.DetectOffTopic(r.Context(), service.OffTopicRequest{
		ProjectPath: q.Get("project_path"),
		Branch:      branch,
		Threshold:   threshold,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Forecast ──

func (s *Server) handleForecast(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var days int
	if d := q.Get("days"); d != "" {
		parsed, err := strconv.Atoi(d)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "days must be a positive integer")
			return
		}
		days = parsed
	}

	period := q.Get("period")
	if period != "" && period != "daily" && period != "weekly" {
		writeError(w, http.StatusBadRequest, "period must be 'daily' or 'weekly'")
		return
	}

	result, err := s.sessionSvc.Forecast(r.Context(), service.ForecastRequest{
		ProjectPath: q.Get("project_path"),
		Branch:      q.Get("branch"),
		Period:      period,
		Days:        days,
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
	includeTools := q.Get("tools") == "true"

	result, err := s.sessionSvc.Stats(service.StatsRequest{
		ProjectPath:  q.Get("project_path"),
		Branch:       q.Get("branch"),
		Provider:     provider,
		OwnerID:      q.Get("owner_id"),
		All:          all,
		IncludeTools: includeTools,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Ingest ──

// handleIngest accepts a session pushed by an external client.
// POST /api/v1/ingest
func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	var req service.IngestRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	result, err := s.sessionSvc.Ingest(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Ingest Ollama ──

// handleIngestOllama accepts an Ollama-native /api/chat conversation format
// and converts it into an aisync session via the universal ingest path.
// POST /api/v1/ingest/ollama
func (s *Server) handleIngestOllama(w http.ResponseWriter, r *http.Request) {
	var req ollamaingest.Request
	if !decodeJSON(w, r, &req) {
		return
	}

	// Convert Ollama format → IngestRequest.
	ingestReq, err := ollamaingest.Convert(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Delegate to the universal ingest path.
	result, err := s.sessionSvc.Ingest(r.Context(), ingestReq)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Projects ──

// handleListProjects returns all distinct projects with aggregated stats.
// GET /api/v1/projects
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	groups, err := s.sessionSvc.ListProjects(r.Context())
	if err != nil {
		mapServiceError(w, err)
		return
	}
	if groups == nil {
		groups = []session.ProjectGroup{}
	}
	writeJSON(w, http.StatusOK, groups)
}

// ── Trends ──

// handleTrends returns trend comparison between current and previous period.
// GET /api/v1/stats/trends?type=bug&period=7d
func (s *Server) handleTrends(w http.ResponseWriter, r *http.Request) {
	req := service.TrendRequest{}
	req.SessionType = r.URL.Query().Get("type")
	if prov := r.URL.Query().Get("provider"); prov != "" {
		req.Provider = session.ProviderName(prov)
	}

	// Parse period: "7d", "30d", "24h", etc.
	if periodStr := r.URL.Query().Get("period"); periodStr != "" {
		// Simple parser: last char is unit (d/h), rest is number.
		if len(periodStr) > 1 {
			numStr := periodStr[:len(periodStr)-1]
			unit := periodStr[len(periodStr)-1]
			num, err := strconv.Atoi(numStr)
			if err == nil && num > 0 {
				switch unit {
				case 'd':
					req.Period = time.Duration(num) * 24 * time.Hour
				case 'h':
					req.Period = time.Duration(num) * time.Hour
				}
			}
		}
	}

	result, err := s.sessionSvc.Trends(r.Context(), req)
	if err != nil {
		mapServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// ── Session Updates ──

// patchSessionRequest is the body for PATCH /api/v1/sessions/{id}.
type patchSessionRequest struct {
	SessionType string `json:"session_type,omitempty"`
}

// handlePatchSession updates mutable fields on a session (currently only session_type).
// PATCH /api/v1/sessions/{id}
func (s *Server) handlePatchSession(w http.ResponseWriter, r *http.Request) {
	id, err := session.ParseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}

	var req patchSessionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.SessionType != "" {
		if patchErr := s.sessionSvc.TagSession(r.Context(), id, req.SessionType); patchErr != nil {
			mapServiceError(w, patchErr)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// ── Session Links ──

// handleLinkSessions creates a session-to-session link (delegation, continuation, etc.).
// POST /api/v1/sessions/session-links
func (s *Server) handleLinkSessions(w http.ResponseWriter, r *http.Request) {
	var req service.SessionLinkRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	link, err := s.sessionSvc.LinkSessions(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, link)
}

// handleGetSessionLinks retrieves all session-to-session links for a given session.
// GET /api/v1/sessions/{id}/session-links
func (s *Server) handleGetSessionLinks(w http.ResponseWriter, r *http.Request) {
	id, err := session.ParseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}

	links, err := s.sessionSvc.GetLinkedSessions(r.Context(), id)
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, links)
}

// handleDeleteSessionLink removes a session-to-session link by its ID.
// DELETE /api/v1/sessions/session-links/{linkID}
func (s *Server) handleDeleteSessionLink(w http.ResponseWriter, r *http.Request) {
	id, err := session.ParseID(r.PathValue("linkID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid link id")
		return
	}

	if err := s.sessionSvc.DeleteSessionLink(r.Context(), id); err != nil {
		mapServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ── Analysis ──

// analyzeRequest is the optional JSON body for POST /api/v1/sessions/{id}/analyze.
type analyzeRequest struct {
	Trigger string `json:"trigger,omitempty"` // "manual" (default) or "auto"
}

// handleAnalyze triggers an analysis for a session.
// POST /api/v1/sessions/{id}/analyze
func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if s.analysisSvc == nil {
		writeError(w, http.StatusNotFound, "analysis service not configured")
		return
	}

	sessionID, err := session.ParseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}

	// Parse optional body (trigger defaults to "manual").
	var req analyzeRequest
	if r.ContentLength > 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	trigger := analysis.TriggerManual
	if req.Trigger == string(analysis.TriggerAuto) {
		trigger = analysis.TriggerAuto
	}

	result, err := s.analysisSvc.Analyze(r.Context(), service.AnalysisRequest{
		SessionID: sessionID,
		Trigger:   trigger,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, result.Analysis)
}

// handleGetAnalysis returns the latest analysis for a session.
// GET /api/v1/sessions/{id}/analysis
func (s *Server) handleGetAnalysis(w http.ResponseWriter, r *http.Request) {
	if s.analysisSvc == nil {
		writeError(w, http.StatusNotFound, "analysis service not configured")
		return
	}

	sessionID, err := session.ParseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}

	a, err := s.analysisSvc.GetLatestAnalysis(string(sessionID))
	if err != nil {
		if err == analysis.ErrAnalysisNotFound {
			writeError(w, http.StatusNotFound, "no analysis found for this session")
			return
		}
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, a)
}

// handleListAnalyses returns all analyses for a session, newest first.
// GET /api/v1/sessions/{id}/analyses
func (s *Server) handleListAnalyses(w http.ResponseWriter, r *http.Request) {
	if s.analysisSvc == nil {
		writeError(w, http.StatusNotFound, "analysis service not configured")
		return
	}

	sessionID, err := session.ParseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}

	analyses, err := s.analysisSvc.ListAnalyses(string(sessionID))
	if err != nil {
		mapServiceError(w, err)
		return
	}

	if analyses == nil {
		analyses = []*analysis.SessionAnalysis{}
	}
	writeJSON(w, http.StatusOK, analyses)
}

// ── Replay ──

// replayRequest is the JSON body for POST /api/v1/sessions/{id}/replay.
type replayRequest struct {
	Provider    string `json:"provider,omitempty"`     // override provider
	Agent       string `json:"agent,omitempty"`        // override agent
	Model       string `json:"model,omitempty"`        // override model
	CommitSHA   string `json:"commit_sha,omitempty"`   // override commit
	MaxMessages int    `json:"max_messages,omitempty"` // limit user messages (0 = all)
}

// handleReplay triggers a session replay.
// POST /api/v1/sessions/{id}/replay
func (s *Server) handleReplay(w http.ResponseWriter, r *http.Request) {
	if s.replayEngine == nil {
		writeError(w, http.StatusNotFound, "replay engine not configured")
		return
	}

	sessionID, err := session.ParseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}

	// Parse optional body.
	var req replayRequest
	if r.ContentLength > 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}

	// Parse optional provider.
	var provider session.ProviderName
	if req.Provider != "" {
		parsed, parseErr := session.ParseProviderName(req.Provider)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, parseErr.Error())
			return
		}
		provider = parsed
	}

	result, err := s.replayEngine.Replay(r.Context(), replay.Request{
		SourceSessionID: sessionID,
		Provider:        provider,
		Agent:           req.Agent,
		Model:           req.Model,
		CommitSHA:       req.CommitSHA,
		MaxMessages:     req.MaxMessages,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, result)
}

// ── Skill Resolver ──

type skillResolveRequest struct {
	SkillNames []string `json:"skill_names,omitempty"` // optional filter
	DryRun     bool     `json:"dry_run"`               // true = suggest only, false = apply
}

// handleSkillResolve analyzes missed skills and proposes/applies improvements.
// POST /api/v1/sessions/{id}/skills/resolve
func (s *Server) handleSkillResolve(w http.ResponseWriter, r *http.Request) {
	if s.skillResolver == nil {
		writeError(w, http.StatusNotFound, "skill resolver not configured")
		return
	}

	sessionID := r.PathValue("id")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	// Parse optional body.
	var req skillResolveRequest
	req.DryRun = true // default to dry-run for safety
	if r.ContentLength > 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}

	result, err := s.skillResolver.Resolve(r.Context(), skillresolver.ResolveRequest{
		SessionID:  sessionID,
		SkillNames: req.SkillNames,
		DryRun:     req.DryRun,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}
