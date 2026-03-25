package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
)

// ── View DTOs (separate from domain) ──

// ProjectEventOverview is the high-level project event summary returned by the overview endpoint.
type ProjectEventOverview struct {
	ProjectPath string    `json:"project_path"`
	RemoteURL   string    `json:"remote_url,omitempty"`
	Period      string    `json:"period"`
	Since       time.Time `json:"since"`
	Until       time.Time `json:"until"`

	// Totals
	TotalToolCalls  int `json:"total_tool_calls"`
	TotalSkillLoads int `json:"total_skill_loads"`
	TotalSessions   int `json:"total_sessions"`
	TotalCommands   int `json:"total_commands"`
	TotalErrors     int `json:"total_errors"`
	TotalImages     int `json:"total_images"`

	// Breakdowns
	UniqueTools     int            `json:"unique_tools"`
	TopTools        map[string]int `json:"top_tools"`
	UniqueSkills    int            `json:"unique_skills"`
	TopSkills       map[string]int `json:"top_skills"`
	AgentBreakdown  map[string]int `json:"agent_breakdown"`
	TopCommands     map[string]int `json:"top_commands"`
	ErrorByCategory map[string]int `json:"error_by_category"`
}

// eventsListResponse wraps a list of events with a count.
type eventsListResponse struct {
	Events []sessionevent.Event `json:"events"`
	Count  int                  `json:"count"`
}

// bucketsListResponse wraps a list of event buckets with a count.
type bucketsListResponse struct {
	Buckets []sessionevent.EventBucket `json:"buckets"`
	Count   int                        `json:"count"`
}

// ── Handlers ──

// handleGetSessionEvents returns all events for a session (micro view).
// GET /api/v1/sessions/{id}/events?type=tool_call
func (s *Server) handleGetSessionEvents(w http.ResponseWriter, r *http.Request) {
	if s.sessionEventSvc == nil {
		writeError(w, http.StatusNotFound, "session event service not configured")
		return
	}

	id := session.ID(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	// Optional type filter.
	var events []sessionevent.Event
	var err error

	if typeFilter := r.URL.Query().Get("type"); typeFilter != "" {
		eventType := sessionevent.EventType(typeFilter)
		if !eventType.Valid() {
			writeError(w, http.StatusBadRequest, "invalid event type: "+typeFilter)
			return
		}
		events, err = s.sessionEventSvc.GetSessionEventsByType(id, eventType)
	} else {
		events, err = s.sessionEventSvc.GetSessionEvents(id)
	}

	if err != nil {
		mapServiceError(w, err)
		return
	}

	if events == nil {
		events = []sessionevent.Event{}
	}

	writeJSON(w, http.StatusOK, eventsListResponse{
		Events: events,
		Count:  len(events),
	})
}

// handleGetSessionEventSummary returns a computed event summary for a session.
// GET /api/v1/sessions/{id}/events/summary
func (s *Server) handleGetSessionEventSummary(w http.ResponseWriter, r *http.Request) {
	if s.sessionEventSvc == nil {
		writeError(w, http.StatusNotFound, "session event service not configured")
		return
	}

	id := session.ID(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	summary, err := s.sessionEventSvc.GetSessionSummary(id)
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

// handleQueryEventBuckets returns aggregated event buckets (macro view).
// GET /api/v1/events/buckets?project_path=/path&granularity=1h&since=2025-03-01T00:00:00Z&until=2025-03-25T00:00:00Z
func (s *Server) handleQueryEventBuckets(w http.ResponseWriter, r *http.Request) {
	if s.sessionEventSvc == nil {
		writeError(w, http.StatusNotFound, "session event service not configured")
		return
	}

	q := r.URL.Query()

	query := sessionevent.BucketQuery{
		ProjectPath: q.Get("project_path"),
		RemoteURL:   q.Get("remote_url"),
		Granularity: q.Get("granularity"),
	}

	if query.Granularity == "" {
		query.Granularity = "1h"
	}
	if query.Granularity != "1h" && query.Granularity != "1d" {
		writeError(w, http.StatusBadRequest, "granularity must be '1h' or '1d'")
		return
	}

	if prov := q.Get("provider"); prov != "" {
		query.Provider = session.ProviderName(prov)
	}

	if since := q.Get("since"); since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid since time: "+err.Error())
			return
		}
		query.Since = t
	}

	if until := q.Get("until"); until != "" {
		t, err := time.Parse(time.RFC3339, until)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid until time: "+err.Error())
			return
		}
		query.Until = t
	}

	// Default to last 7 days if no time range specified.
	if query.Since.IsZero() && query.Until.IsZero() {
		query.Until = time.Now().UTC()
		query.Since = query.Until.Add(-7 * 24 * time.Hour)
	}

	buckets, err := s.sessionEventSvc.QueryBuckets(query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if buckets == nil {
		buckets = []sessionevent.EventBucket{}
	}

	writeJSON(w, http.StatusOK, bucketsListResponse{
		Buckets: buckets,
		Count:   len(buckets),
	})
}

// handleGetProjectEventOverview returns a high-level project event overview.
// GET /api/v1/events/overview?project_path=/path&remote_url=github.com/org/repo&days=30
func (s *Server) handleGetProjectEventOverview(w http.ResponseWriter, r *http.Request) {
	if s.sessionEventSvc == nil {
		writeError(w, http.StatusNotFound, "session event service not configured")
		return
	}

	q := r.URL.Query()
	projectPath := q.Get("project_path")
	remoteURL := q.Get("remote_url")

	if projectPath == "" && remoteURL == "" {
		writeError(w, http.StatusBadRequest, "project_path or remote_url is required")
		return
	}

	days := 30
	if v := q.Get("days"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, "days must be a positive integer")
			return
		}
		if parsed > 365 {
			parsed = 365
		}
		days = parsed
	}

	until := time.Now().UTC()
	since := until.Add(-time.Duration(days) * 24 * time.Hour)

	buckets, err := s.sessionEventSvc.QueryBuckets(sessionevent.BucketQuery{
		ProjectPath: projectPath,
		RemoteURL:   remoteURL,
		Granularity: "1d",
		Since:       since,
		Until:       until,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Aggregate all daily buckets into a single overview.
	overview := ProjectEventOverview{
		ProjectPath:     projectPath,
		RemoteURL:       remoteURL,
		Period:          fmt.Sprintf("%dd", days),
		Since:           since,
		Until:           until,
		TopTools:        make(map[string]int),
		TopSkills:       make(map[string]int),
		AgentBreakdown:  make(map[string]int),
		TopCommands:     make(map[string]int),
		ErrorByCategory: make(map[string]int),
	}

	for _, b := range buckets {
		overview.TotalToolCalls += b.ToolCallCount
		overview.TotalSkillLoads += b.SkillLoadCount
		overview.TotalSessions += b.SessionCount
		overview.TotalCommands += b.CommandCount
		overview.TotalErrors += b.ErrorCount
		overview.TotalImages += b.ImageCount

		for name, count := range b.TopTools {
			overview.TopTools[name] += count
		}
		for name, count := range b.TopSkills {
			overview.TopSkills[name] += count
		}
		for name, count := range b.AgentBreakdown {
			overview.AgentBreakdown[name] += count
		}
		for name, count := range b.TopCommands {
			overview.TopCommands[name] += count
		}
		for cat, count := range b.ErrorByCategory {
			overview.ErrorByCategory[string(cat)] += count
		}
	}

	overview.UniqueTools = len(overview.TopTools)
	overview.UniqueSkills = len(overview.TopSkills)

	writeJSON(w, http.StatusOK, overview)
}
