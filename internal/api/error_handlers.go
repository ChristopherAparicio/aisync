package api

import (
	"net/http"
	"strconv"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── View DTOs (separate from domain) ──
// These are the JSON shapes exposed to API consumers for error endpoints.
// They deliberately differ from domain types to maintain boundary separation.

type sessionErrorResponse struct {
	ID           string            `json:"id"`
	SessionID    string            `json:"session_id"`
	Category     string            `json:"category"`
	Source       string            `json:"source"`
	Message      string            `json:"message"`
	RawError     string            `json:"raw_error,omitempty"`
	ToolName     string            `json:"tool_name,omitempty"`
	ToolCallID   string            `json:"tool_call_id,omitempty"`
	MessageID    string            `json:"message_id,omitempty"`
	MessageIndex int               `json:"message_index,omitempty"`
	HTTPStatus   int               `json:"http_status,omitempty"`
	ProviderName string            `json:"provider_name,omitempty"`
	RequestID    string            `json:"request_id,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	OccurredAt   string            `json:"occurred_at"`
	DurationMs   int               `json:"duration_ms,omitempty"`
	IsRetryable  bool              `json:"is_retryable,omitempty"`
	Confidence   string            `json:"confidence,omitempty"`
	IsExternal   bool              `json:"is_external"`
}

type errorSummaryResponse struct {
	SessionID      string         `json:"session_id"`
	TotalErrors    int            `json:"total_errors"`
	ByCategory     map[string]int `json:"by_category"`
	BySource       map[string]int `json:"by_source"`
	ExternalErrors int            `json:"external_errors"`
	InternalErrors int            `json:"internal_errors"`
	FirstErrorAt   string         `json:"first_error_at,omitempty"`
	LastErrorAt    string         `json:"last_error_at,omitempty"`
}

type errorsListResponse struct {
	Errors []sessionErrorResponse `json:"errors"`
	Count  int                    `json:"count"`
}

// ── Mappers (domain → view) ──

func toSessionErrorResponse(e session.SessionError) sessionErrorResponse {
	return sessionErrorResponse{
		ID:           e.ID,
		SessionID:    string(e.SessionID),
		Category:     e.Category.String(),
		Source:       e.Source.String(),
		Message:      e.Message,
		RawError:     e.RawError,
		ToolName:     e.ToolName,
		ToolCallID:   e.ToolCallID,
		MessageID:    e.MessageID,
		MessageIndex: e.MessageIndex,
		HTTPStatus:   e.HTTPStatus,
		ProviderName: e.ProviderName,
		RequestID:    e.RequestID,
		Headers:      e.Headers,
		OccurredAt:   e.OccurredAt.Format("2006-01-02T15:04:05Z"),
		DurationMs:   e.DurationMs,
		IsRetryable:  e.IsRetryable,
		Confidence:   e.Confidence,
		IsExternal:   e.IsExternal(),
	}
}

func toSessionErrorResponses(errors []session.SessionError) []sessionErrorResponse {
	resp := make([]sessionErrorResponse, 0, len(errors))
	for _, e := range errors {
		resp = append(resp, toSessionErrorResponse(e))
	}
	return resp
}

func toErrorSummaryResponse(s *session.SessionErrorSummary) errorSummaryResponse {
	resp := errorSummaryResponse{
		SessionID:      string(s.SessionID),
		TotalErrors:    s.TotalErrors,
		ByCategory:     make(map[string]int, len(s.ByCategory)),
		BySource:       make(map[string]int, len(s.BySource)),
		ExternalErrors: s.ExternalErrors,
		InternalErrors: s.InternalErrors,
	}
	for cat, count := range s.ByCategory {
		resp.ByCategory[cat.String()] = count
	}
	for src, count := range s.BySource {
		resp.BySource[src.String()] = count
	}
	if !s.FirstErrorAt.IsZero() {
		resp.FirstErrorAt = s.FirstErrorAt.Format("2006-01-02T15:04:05Z")
	}
	if !s.LastErrorAt.IsZero() {
		resp.LastErrorAt = s.LastErrorAt.Format("2006-01-02T15:04:05Z")
	}
	return resp
}

// ── Handlers ──

// handleGetSessionErrors returns all classified errors for a session.
// GET /api/v1/sessions/{id}/errors
func (s *Server) handleGetSessionErrors(w http.ResponseWriter, r *http.Request) {
	if s.errorSvc == nil {
		writeError(w, http.StatusNotFound, "error analysis not configured")
		return
	}

	id := session.ID(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	errors, err := s.errorSvc.GetErrors(id)
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, errorsListResponse{
		Errors: toSessionErrorResponses(errors),
		Count:  len(errors),
	})
}

// handleGetSessionErrorSummary returns aggregated error statistics for a session.
// GET /api/v1/sessions/{id}/errors/summary
func (s *Server) handleGetSessionErrorSummary(w http.ResponseWriter, r *http.Request) {
	if s.errorSvc == nil {
		writeError(w, http.StatusNotFound, "error analysis not configured")
		return
	}

	id := session.ID(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	summary, err := s.errorSvc.GetSummary(id)
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toErrorSummaryResponse(summary))
}

// handleListRecentErrors returns recent errors across all sessions.
// GET /api/v1/errors/recent?limit=50&category=provider_error
func (s *Server) handleListRecentErrors(w http.ResponseWriter, r *http.Request) {
	if s.errorSvc == nil {
		writeError(w, http.StatusNotFound, "error analysis not configured")
		return
	}

	// Parse query parameters.
	limit := 50 // default
	if v := r.URL.Query().Get("limit"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		if parsed > 200 {
			parsed = 200
		}
		limit = parsed
	}

	var category session.ErrorCategory
	if v := r.URL.Query().Get("category"); v != "" {
		category = session.ErrorCategory(v)
		if !category.Valid() {
			writeError(w, http.StatusBadRequest, "invalid error category: "+v)
			return
		}
	}

	errors, err := s.errorSvc.ListRecent(limit, category)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, errorsListResponse{
		Errors: toSessionErrorResponses(errors),
		Count:  len(errors),
	})
}
