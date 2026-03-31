package web

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
)

// ── Analytics Page (Level 1: project overview) ──

type analyticsEventPage struct {
	Nav             string
	SidebarProjects []sidebarProject
	SelectedProject string

	// Overview KPIs (aggregated from daily buckets)
	TotalToolCalls  int
	TotalSkillLoads int
	TotalSessions   int
	TotalCommands   int
	TotalErrors     int
	TotalImages     int

	// Top breakdowns
	UniqueTools  int
	TopTools     []nameCount
	UniqueSkills int
	TopSkills    []nameCount
	TopAgents    []nameCount
	TopCommands  []nameCount

	// Skill context cost
	HasSkillTokens    bool
	TotalSkillTokens  int
	SkillContextCosts []skillContextCost

	// Period
	PeriodLabel string
	Since       time.Time
	Until       time.Time

	// Daily activity (for the sparkline/bar chart)
	DailyBuckets []dailyActivity

	HasData bool
}

type nameCount struct {
	Name  string
	Count int
	Pct   float64 // percentage of total
}

type skillContextCost struct {
	Name           string
	Loads          int
	AvgTokens      int
	TotalTokens    int
	TotalTokensK   string  // formatted "12K"
	ContextPercent float64 // percentage of total skill tokens
}

type dailyActivity struct {
	Date      string
	ToolCalls int
	Skills    int
	Commands  int
	Errors    int
	Sessions  int
}

func (s *Server) handleAnalytics(w http.ResponseWriter, r *http.Request) {
	if s.sessionEventSvc == nil {
		s.render(w, "analytics_events.html", analyticsEventPage{Nav: "analytics"})
		return
	}

	ctx := r.Context()
	project := r.URL.Query().Get("project")
	daysStr := r.URL.Query().Get("days")
	days := 30
	if daysStr == "7" {
		days = 7
	} else if daysStr == "90" {
		days = 90
	}

	now := time.Now().UTC()
	since := now.AddDate(0, 0, -days)

	data := analyticsEventPage{
		Nav:             "analytics",
		SelectedProject: project,
		SidebarProjects: s.buildSidebarProjects(ctx, project),
		Since:           since,
		Until:           now,
		PeriodLabel:     daysStr + " days",
	}
	if daysStr == "" {
		data.PeriodLabel = "30 days"
	}

	// Query daily buckets for the project.
	buckets, err := s.sessionEventSvc.QueryBuckets(sessionevent.BucketQuery{
		ProjectPath: project,
		Granularity: "1d",
		Since:       since,
		Until:       now,
	})
	if err != nil {
		s.logger.Printf("analytics event buckets error: %v", err)
		s.render(w, "analytics_events.html", data)
		return
	}

	if len(buckets) == 0 {
		s.render(w, "analytics_events.html", data)
		return
	}
	data.HasData = true

	// Aggregate all daily buckets into totals.
	allTools := make(map[string]int)
	allSkills := make(map[string]int)
	allSkillTokens := make(map[string]int)
	allAgents := make(map[string]int)
	allCommands := make(map[string]int)

	for _, b := range buckets {
		data.TotalToolCalls += b.ToolCallCount
		data.TotalSkillLoads += b.SkillLoadCount
		data.TotalSessions += b.SessionCount
		data.TotalCommands += b.CommandCount
		data.TotalErrors += b.ErrorCount
		data.TotalImages += b.ImageCount

		for k, v := range b.TopTools {
			allTools[k] += v
		}
		for k, v := range b.TopSkills {
			allSkills[k] += v
		}
		for k, v := range b.SkillTokens {
			allSkillTokens[k] += v
		}
		for k, v := range b.AgentBreakdown {
			allAgents[k] += v
		}
		for k, v := range b.TopCommands {
			allCommands[k] += v
		}

		data.DailyBuckets = append(data.DailyBuckets, dailyActivity{
			Date:      b.BucketStart.Format("Jan 2"),
			ToolCalls: b.ToolCallCount,
			Skills:    b.SkillLoadCount,
			Commands:  b.CommandCount,
			Errors:    b.ErrorCount,
			Sessions:  b.SessionCount,
		})
	}

	data.UniqueTools = len(allTools)
	data.UniqueSkills = len(allSkills)
	data.TopTools = topN(allTools, data.TotalToolCalls, 10)
	data.TopSkills = topN(allSkills, data.TotalSkillLoads, 10)
	data.TopAgents = topN(allAgents, data.TotalSessions, 10)
	data.TopCommands = topN(allCommands, data.TotalCommands, 10)

	// Build skill context cost breakdown.
	if len(allSkillTokens) > 0 {
		var totalSkillTok int
		for _, v := range allSkillTokens {
			totalSkillTok += v
		}
		data.HasSkillTokens = true
		data.TotalSkillTokens = totalSkillTok

		for name, tokens := range allSkillTokens {
			loads := allSkills[name]
			avgTok := tokens
			if loads > 0 {
				avgTok = tokens / loads
			}
			tokK := fmt.Sprintf("%d", tokens)
			if tokens >= 1000 {
				tokK = fmt.Sprintf("%dK", tokens/1000)
			}
			pct := float64(0)
			if totalSkillTok > 0 {
				pct = float64(tokens) / float64(totalSkillTok) * 100
			}
			data.SkillContextCosts = append(data.SkillContextCosts, skillContextCost{
				Name:           name,
				Loads:          loads,
				AvgTokens:      avgTok,
				TotalTokens:    tokens,
				TotalTokensK:   tokK,
				ContextPercent: pct,
			})
		}
		sort.Slice(data.SkillContextCosts, func(i, j int) bool {
			return data.SkillContextCosts[i].TotalTokens > data.SkillContextCosts[j].TotalTokens
		})
	}

	s.render(w, "analytics_events.html", data)
}

// ── Agent Detail Partial (Level 2: HTMX drill-down) ──

type agentDetailData struct {
	AgentName    string
	SessionCount int
	ToolCalls    int
	SkillLoads   int
	Commands     int
	Errors       int
	TopTools     []nameCount
	TopSkills    []nameCount
	TopCommands  []nameCount
	Sessions     []session.Summary
	PeriodLabel  string
}

func (s *Server) handleAgentDetailPartial(w http.ResponseWriter, r *http.Request) {
	if s.sessionEventSvc == nil {
		http.Error(w, "not configured", http.StatusNotFound)
		return
	}

	agentName := r.URL.Query().Get("agent")
	project := r.URL.Query().Get("project")
	if agentName == "" {
		http.Error(w, "agent param required", http.StatusBadRequest)
		return
	}

	now := time.Now().UTC()
	since := now.AddDate(0, 0, -30)

	// Get daily buckets and filter for this agent.
	buckets, _ := s.sessionEventSvc.QueryBuckets(sessionevent.BucketQuery{
		ProjectPath: project,
		Granularity: "1d",
		Since:       since,
		Until:       now,
	})

	data := agentDetailData{
		AgentName:   agentName,
		PeriodLabel: "30 days",
	}

	allTools := make(map[string]int)
	allSkills := make(map[string]int)
	allCmds := make(map[string]int)

	for _, b := range buckets {
		count, ok := b.AgentBreakdown[agentName]
		if !ok {
			continue
		}
		data.SessionCount += count
		data.ToolCalls += b.ToolCallCount
		data.SkillLoads += b.SkillLoadCount
		data.Commands += b.CommandCount
		data.Errors += b.ErrorCount
		for k, v := range b.TopTools {
			allTools[k] += v
		}
		for k, v := range b.TopSkills {
			allSkills[k] += v
		}
		for k, v := range b.TopCommands {
			allCmds[k] += v
		}
	}

	data.TopTools = topN(allTools, data.ToolCalls, 8)
	data.TopSkills = topN(allSkills, data.SkillLoads, 8)
	data.TopCommands = topN(allCmds, data.Commands, 8)

	// Get recent sessions for this agent.
	summaries, _ := s.sessionSvc.List(service.ListRequest{
		ProjectPath: project,
	})
	for _, sm := range summaries {
		if sm.Agent == agentName && len(data.Sessions) < 20 {
			data.Sessions = append(data.Sessions, sm)
		}
	}

	s.renderPartial(w, "agent_detail_partial", data)
}

// ── Session Events Partial (Level 3: HTMX drill-down) ──

type sessionEventsData struct {
	SessionID   string
	Summary     *sessionevent.SessionEventSummary
	Events      []sessionevent.Event
	ToolEvents  []sessionevent.Event
	SkillEvents []sessionevent.Event
	HasData     bool
}

func (s *Server) handleSessionEventsPartial(w http.ResponseWriter, r *http.Request) {
	if s.sessionEventSvc == nil {
		http.Error(w, "not configured", http.StatusNotFound)
		return
	}

	sessionID := r.PathValue("id")
	if sessionID == "" {
		http.Error(w, "session id required", http.StatusBadRequest)
		return
	}

	sid := session.ID(sessionID)
	summary, err := s.sessionEventSvc.GetSessionSummary(sid)
	if err != nil {
		s.logger.Printf("session events error: %v", err)
		http.Error(w, "error loading events", http.StatusInternalServerError)
		return
	}

	events, _ := s.sessionEventSvc.GetSessionEvents(sid)

	data := sessionEventsData{
		SessionID: sessionID,
		Summary:   summary,
		Events:    events,
		HasData:   summary != nil && summary.TotalEvents > 0,
	}

	// Separate tool and skill events for the detail view.
	for i := range events {
		switch events[i].Type {
		case sessionevent.EventToolCall:
			data.ToolEvents = append(data.ToolEvents, events[i])
		case sessionevent.EventSkillLoad:
			data.SkillEvents = append(data.SkillEvents, events[i])
		}
	}

	s.renderPartial(w, "session_events_partial", data)
}

// ── Helpers ──

// topN converts a map to a sorted slice of the top N entries.
func topN(m map[string]int, total int, n int) []nameCount {
	items := make([]nameCount, 0, len(m))
	for k, v := range m {
		pct := 0.0
		if total > 0 {
			pct = float64(v) / float64(total) * 100
		}
		items = append(items, nameCount{Name: k, Count: v, Pct: pct})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Count > items[j].Count
	})
	if len(items) > n {
		items = items[:n]
	}
	return items
}
