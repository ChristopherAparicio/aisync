package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/registry"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
)

// Cache TTLs for dashboard statistics.
const (
	statsCacheTTL    = 30 * time.Second
	forecastCacheTTL = 5 * time.Minute
)

// cachedStats returns Stats from cache if fresh, otherwise computes and caches.
func (s *Server) cachedStats(req service.StatsRequest) (*service.StatsResult, error) {
	cacheKey := "stats:" + req.ProjectPath + ":" + req.Branch

	// 1. Try cache
	if s.store != nil {
		if data, _ := s.store.GetCache(cacheKey, statsCacheTTL); data != nil {
			var result service.StatsResult
			if err := json.Unmarshal(data, &result); err == nil {
				return &result, nil
			}
		}
	}

	// 2. Compute
	result, err := s.sessionSvc.Stats(req)
	if err != nil {
		return nil, err
	}

	// 3. Cache
	if s.store != nil {
		if data, marshalErr := json.Marshal(result); marshalErr == nil {
			_ = s.store.SetCache(cacheKey, data)
		}
	}

	return result, nil
}

// cachedForecast returns Forecast from cache if fresh, otherwise computes and caches.
func (s *Server) cachedForecast(ctx context.Context, req service.ForecastRequest) (*session.ForecastResult, error) {
	cacheKey := "forecast:" + req.ProjectPath + ":" + req.Period

	// 1. Try cache
	if s.store != nil {
		if data, _ := s.store.GetCache(cacheKey, forecastCacheTTL); data != nil {
			var result session.ForecastResult
			if err := json.Unmarshal(data, &result); err == nil {
				return &result, nil
			}
		}
	}

	// 2. Compute
	result, err := s.sessionSvc.Forecast(ctx, req)
	if err != nil {
		return nil, err
	}

	// 3. Cache
	if s.store != nil {
		if data, marshalErr := json.Marshal(result); marshalErr == nil {
			_ = s.store.SetCache(cacheKey, data)
		}
	}

	return result, nil
}

// ── Shared ──

type branchStat struct {
	Branch       string
	SessionCount int
	TotalTokens  int
	TotalCost    float64
	ActualCost   float64
}

// projectItem is a template-friendly project entry for the project selector.
type projectItem struct {
	Name     string
	Path     string
	Selected bool
}

// sidebarProject is a project entry for the sidebar navigation.
type sidebarProject struct {
	Name         string
	Path         string
	SessionCount int
	Active       bool // true if this project is currently selected
}

// buildSidebarProjects returns project data for the sidebar, sorted by recent activity.
func (s *Server) buildSidebarProjects(ctx context.Context, activePath string) []sidebarProject {
	groups, err := s.sessionSvc.ListProjects(ctx)
	if err != nil || len(groups) == 0 {
		return nil
	}

	items := make([]sidebarProject, 0, len(groups))
	for _, g := range groups {
		items = append(items, sidebarProject{
			Name:         g.DisplayName,
			Path:         g.ProjectPath,
			SessionCount: g.SessionCount,
			Active:       g.ProjectPath == activePath,
		})
	}
	return items
}

// ── API: Projects ──

// handleAPIProjects returns JSON list of projects for the project selector.
func (s *Server) handleAPIProjects(w http.ResponseWriter, r *http.Request) {
	type projectJSON struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}

	var projects []projectJSON
	if s.registrySvc != nil {
		list, err := s.registrySvc.ListProjects()
		if err == nil {
			for _, p := range list {
				projects = append(projects, projectJSON{
					Name: p.Name,
					Path: p.RootPath,
				})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(projects)
}

// buildProjectList returns projects for the template selector dropdown.
func (s *Server) buildProjectList(selectedPath string) []projectItem {
	if s.registrySvc == nil {
		return nil
	}

	list, err := s.registrySvc.ListProjects()
	if err != nil {
		return nil
	}

	items := make([]projectItem, 0, len(list))
	for _, p := range list {
		items = append(items, projectItem{
			Name:     p.Name,
			Path:     p.RootPath,
			Selected: p.RootPath == selectedPath,
		})
	}
	return items
}

// ── Projects Page ──

type projectsPage struct {
	Nav             string
	SidebarProjects []sidebarProject
	Projects        []projectCard

	// Cross-project capabilities summary
	HasCapSummary       bool
	CapProjectCount     int
	CapTotalCaps        int
	CapTotalMCP         int
	CapKindMatrix       []capKindMatrixRow
	CapSharedMCP        []sharedMCPView
	CapProjectOverviews []capProjectOverview
}

type projectCard struct {
	DisplayName  string
	RemoteURL    string
	ProjectPath  string
	Provider     string
	Category     string
	SessionCount int
	TotalTokens  int
}

type capKindMatrixRow struct {
	Kind         string
	KindLabel    string
	TotalCount   int
	ProjectCount int
}

type sharedMCPView struct {
	Name         string
	ProjectCount int
	Projects     []string
	IsShared     bool // true if used by >1 project
}

type capProjectOverview struct {
	Name            string
	Path            string
	CapabilityCount int
	MCPServerCount  int
	SkillCount      int
	AgentCount      int
	CommandCount    int
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := projectsPage{Nav: "projects", SidebarProjects: s.buildSidebarProjects(ctx, "")}

	groups, err := s.sessionSvc.ListProjects(ctx)
	if err != nil {
		s.logger.Printf("projects list error: %v", err)
		s.render(w, "projects.html", data)
		return
	}

	for _, g := range groups {
		data.Projects = append(data.Projects, projectCard{
			DisplayName:  g.DisplayName,
			RemoteURL:    g.RemoteURL,
			ProjectPath:  g.ProjectPath,
			Provider:     string(g.Provider),
			Category:     g.Category,
			SessionCount: g.SessionCount,
			TotalTokens:  g.TotalTokens,
		})
	}

	// Cross-project capabilities summary.
	if s.registrySvc != nil {
		summary, capErr := s.registrySvc.CrossProjectCapabilities()
		if capErr == nil && summary != nil && summary.ProjectCount > 0 {
			data.HasCapSummary = true
			data.CapProjectCount = summary.ProjectCount
			data.CapTotalCaps = summary.TotalCapabilities
			data.CapTotalMCP = summary.TotalMCPServers

			for _, row := range summary.CapabilityMatrix {
				data.CapKindMatrix = append(data.CapKindMatrix, capKindMatrixRow{
					Kind:         string(row.Kind),
					KindLabel:    capabilityKindLabel(string(row.Kind)),
					TotalCount:   row.TotalCount,
					ProjectCount: row.ProjectCount,
				})
			}
			for _, shared := range summary.SharedMCPServers {
				data.CapSharedMCP = append(data.CapSharedMCP, sharedMCPView{
					Name:         shared.Name,
					ProjectCount: shared.ProjectCount,
					Projects:     shared.Projects,
					IsShared:     shared.ProjectCount > 1,
				})
			}
			for _, po := range summary.ProjectOverviews {
				data.CapProjectOverviews = append(data.CapProjectOverviews, capProjectOverview{
					Name:            po.Name,
					Path:            po.Path,
					CapabilityCount: po.CapabilityCount,
					MCPServerCount:  po.MCPServerCount,
					SkillCount:      po.SkillCount,
					AgentCount:      po.AgentCount,
					CommandCount:    po.CommandCount,
				})
			}
		}
	}

	s.render(w, "projects.html", data)
}

// ── Dashboard ──

// capabilityStat is a template-friendly capability count.
type capabilityStat struct {
	Kind  string
	Count int
}

type dashboardPage struct {
	Nav              string
	TotalSessions    int
	TotalTokens      int
	TotalCost        float64 // API-equivalent cost (estimated from token rates)
	ActualCost       float64 // actual cost reported by providers
	Savings          float64 // TotalCost - ActualCost
	DeduplicatedCost float64 // cost after fork deduplication
	ForkSavings      float64 // cost removed by deduplication
	HasForkDedup     bool    // true if there are detected forks
	BillingType      string  // "subscription", "api", "mixed", or ""
	TrendDir         string
	RecentSessions   []session.Summary
	TopBranches      []branchStat
	HasForecast      bool
	Projected30d     float64
	Projected90d     float64
	TrendPerDay      float64

	// Real cost forecast (API-only spend + subscriptions)
	HasRealForecast     bool
	TotalReal30d        float64
	SubscriptionMonthly float64
	APIProjected30d     float64

	// Cache efficiency (quick stats for dashboard)
	HasCacheStats         bool
	DashCacheHitRate      float64 // 0-100
	DashCacheMissSessions int     // sessions with at least 1 miss
	DashCacheTotalMisses  int     // total miss messages
	DashCacheWaste        float64 // $ wasted

	// Project filtering
	Projects        []projectItem
	SelectedProject string

	// Sidebar
	SidebarProjects []sidebarProject

	// Error KPIs (aggregated from session summaries)
	TotalErrors        int
	SessionsWithErrors int
	TotalToolCalls     int

	// Weekly trends (current vs previous period)
	HasTrends     bool
	TrendVerdict  string // "improving", "stable", "degrading"
	TrendSessions trendMetric
	TrendTokens   trendMetric
	TrendErrors   trendMetric

	// Capability metrics (when a project is selected)
	HasCapabilities bool
	ProjectName     string
	CapabilityStats []capabilityStat
	MCPServerCount  int
}

// trendMetric holds current vs previous values for a single metric.
type trendMetric struct {
	Current    int
	Previous   int
	Delta      int     // absolute change
	DeltaPct   float64 // percentage change
	Direction  string  // "up", "down", "flat"
	IsPositive bool    // true if this direction is good (e.g. errors going down)
}

// buildTrendMetric creates a trendMetric from raw values.
// invertPositive means "down is good" (e.g. for errors).
func buildTrendMetric(current, previous, delta int, deltaPct float64, invertPositive bool) trendMetric {
	m := trendMetric{
		Current:  current,
		Previous: previous,
		Delta:    delta,
		DeltaPct: deltaPct,
	}
	switch {
	case delta > 0:
		m.Direction = "up"
		m.IsPositive = invertPositive // errors going up is bad
	case delta < 0:
		m.Direction = "down"
		m.IsPositive = !invertPositive // errors going down is good
	default:
		m.Direction = "flat"
		m.IsPositive = true
	}
	// Compute percentage if not provided.
	if deltaPct == 0 && previous > 0 {
		m.DeltaPct = float64(delta) / float64(previous) * 100
	}
	return m
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data := s.buildDashboardData(r)
	s.render(w, "dashboard.html", data)
}

func (s *Server) buildDashboardData(r *http.Request) dashboardPage {
	project := r.URL.Query().Get("project")
	data := dashboardPage{
		Nav:             "dashboard",
		Projects:        s.buildProjectList(project),
		SelectedProject: project,
		SidebarProjects: s.buildSidebarProjects(r.Context(), project),
	}

	statsReq := service.StatsRequest{All: project == "", ProjectPath: project}
	stats, err := s.cachedStats(statsReq)
	if err != nil {
		s.logger.Printf("dashboard stats error: %v", err)
		return data
	}

	data.TotalSessions = stats.TotalSessions
	data.TotalTokens = stats.TotalTokens
	data.TotalCost = stats.TotalCost
	data.ActualCost = stats.ActualCost
	data.Savings = stats.Savings
	data.DeduplicatedCost = stats.DeduplicatedCost
	data.ForkSavings = stats.ForkSavings
	data.HasForkDedup = stats.ForkSavings > 0
	data.BillingType = stats.BillingType

	limit := 8
	if len(stats.PerBranch) < limit {
		limit = len(stats.PerBranch)
	}
	for i := range limit {
		bs := stats.PerBranch[i]
		data.TopBranches = append(data.TopBranches, branchStat{
			Branch:       bs.Branch,
			SessionCount: bs.SessionCount,
			TotalTokens:  bs.TotalTokens,
			TotalCost:    bs.TotalCost,
			ActualCost:   bs.ActualCost,
		})
	}

	listReq := service.ListRequest{All: project == "", ProjectPath: project}
	summaries, err := s.sessionSvc.List(listReq)
	if err != nil {
		s.logger.Printf("dashboard list error: %v", err)
	} else {
		// Aggregate error KPIs from all sessions.
		for _, sm := range summaries {
			data.TotalErrors += sm.ErrorCount
			data.TotalToolCalls += sm.ToolCallCount
			if sm.ErrorCount > 0 {
				data.SessionsWithErrors++
			}
		}

		// Sort by last update time (most recently active first).
		sort.Slice(summaries, func(i, j int) bool {
			ti := summaries[i].UpdatedAt
			if ti.IsZero() {
				ti = summaries[i].CreatedAt
			}
			tj := summaries[j].UpdatedAt
			if tj.IsZero() {
				tj = summaries[j].CreatedAt
			}
			return ti.After(tj)
		})

		limit := 10
		if len(summaries) < limit {
			limit = len(summaries)
		}
		data.RecentSessions = summaries[:limit]
	}

	forecast, err := s.cachedForecast(r.Context(), service.ForecastRequest{
		Period: "weekly",
		Days:   90,
	})
	if err == nil && forecast.SessionCount > 0 {
		data.HasForecast = true
		data.Projected30d = forecast.Projected30d
		data.Projected90d = forecast.Projected90d
		data.TrendPerDay = forecast.TrendPerDay
		data.TrendDir = forecast.TrendDir

		// Real cost (subscriptions + API) if available.
		if forecast.TotalReal30d > 0 || forecast.SubscriptionMonthly > 0 {
			data.HasRealForecast = true
			data.TotalReal30d = forecast.TotalReal30d
			data.SubscriptionMonthly = forecast.SubscriptionMonthly
			data.APIProjected30d = forecast.APIProjected30d
		}
	}

	if data.TrendDir == "" {
		data.TrendDir = "stable"
	}

	// Cache efficiency (quick stats).
	since7d := time.Now().AddDate(0, 0, -7)
	cacheEff, cacheErr := s.sessionSvc.CacheEfficiency(r.Context(), project, since7d)
	if cacheErr == nil && cacheEff != nil && cacheEff.TotalInputTokens > 0 {
		data.HasCacheStats = true
		data.DashCacheHitRate = cacheEff.CacheHitRate
		data.DashCacheMissSessions = cacheEff.SessionsWithMiss
		data.DashCacheTotalMisses = cacheEff.TotalCacheMisses
		data.DashCacheWaste = cacheEff.EstimatedWaste
	}

	// Weekly trends (current vs previous 7 days)
	trends, trendsErr := s.sessionSvc.Trends(r.Context(), service.TrendRequest{
		Period: 7 * 24 * time.Hour,
	})
	if trendsErr == nil && (trends.Current.SessionCount > 0 || trends.Previous.SessionCount > 0) {
		data.HasTrends = true
		data.TrendVerdict = trends.Delta.Verdict

		data.TrendSessions = buildTrendMetric(
			trends.Current.SessionCount, trends.Previous.SessionCount,
			trends.Delta.SessionCountChange, 0, false,
		)
		data.TrendTokens = buildTrendMetric(
			trends.Current.TotalTokens, trends.Previous.TotalTokens,
			trends.Delta.TokensChange, trends.Delta.TokensChangePercent, false,
		)
		data.TrendErrors = buildTrendMetric(
			trends.Current.TotalErrors, trends.Previous.TotalErrors,
			trends.Delta.ErrorsChange, trends.Delta.ErrorsChangePercent, true,
		)
	}

	// Capability metrics for selected project
	if project != "" && s.registrySvc != nil {
		proj, scanErr := s.registrySvc.ScanProject(project)
		if scanErr == nil {
			data.HasCapabilities = true
			data.ProjectName = proj.Name
			data.MCPServerCount = len(proj.MCPServers)
			for _, cs := range proj.CapabilityStats() {
				data.CapabilityStats = append(data.CapabilityStats, capabilityStat{
					Kind:  string(cs.Kind),
					Count: cs.Count,
				})
			}
		}
	}

	return data
}

// ── Sessions List ──

const defaultPageSize = 25

// columnDef describes a single column in the sessions table.
type columnDef struct {
	ID    string // config identifier ("id", "provider", etc.)
	Label string // display header
	Class string // CSS class for <th>/<td> ("", "text-right", etc.)
}

// sessionRow is a template-friendly row with pre-formatted cell values.
type sessionRow struct {
	ID    string // always present for link generation
	Cells []sessionCell
}

// sessionCell holds a single cell's display value and optional CSS class/badge.
type sessionCell struct {
	Value    string
	Class    string // extra CSS class for the <td>
	IsLink   bool   // render as <a href="/sessions/..."> (when LinkHref is empty)
	IsBadge  bool   // wrap in <span class="badge badge-provider">
	LinkID   string // session ID for the link target (used when LinkHref is empty)
	LinkHref string // explicit link URL (takes precedence over LinkID)
}

type sessionsPage struct {
	Nav string

	// Sidebar
	SidebarProjects []sidebarProject

	// Filter state (echoed back for form pre-fill).
	FilterKeyword         string
	FilterBranch          string
	FilterProvider        string
	FilterProject         string
	FilterOwner           string
	FilterSessionType     string
	FilterProjectCategory string
	FilterSince           string
	FilterUntil           string
	FilterStatus          string
	FilterHasErrors       string

	// Sort state
	SortBy    string
	SortOrder string

	// Project selector
	Projects []projectItem

	// Dynamic columns
	Columns []columnDef

	// Results (dynamic rows).
	Rows       []sessionRow
	TotalCount int
	Page       int // 1-based
	PageSize   int
	TotalPages int
	HasPrev    bool
	HasNext    bool
}

// handleSessions renders the full sessions list page.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	data := s.buildSessionsData(r)
	s.render(w, "sessions.html", data)
}

// handleSessionsTable renders just the table partial (for HTMX requests).
func (s *Server) handleSessionsTable(w http.ResponseWriter, r *http.Request) {
	data := s.buildSessionsData(r)
	s.renderPartial(w, "sessions_table.html", data)
}

func (s *Server) buildSessionsData(r *http.Request) sessionsPage {
	q := r.URL.Query()
	keyword := q.Get("keyword")
	branch := q.Get("branch")
	provider := q.Get("provider")
	project := q.Get("project")
	owner := q.Get("owner")
	sessionType := q.Get("session_type")
	projectCategory := q.Get("project_category")
	status := q.Get("status")
	hasErrors := q.Get("has_errors")
	since := q.Get("since")
	until := q.Get("until")
	sortBy := q.Get("sort_by")
	sortOrder := q.Get("sort_order")

	// Apply config defaults for filters + sort when no query params are set.
	if provider == "" && !q.Has("provider") && s.cfg != nil {
		provider = s.cfg.GetDashboardDefaultProvider()
	}
	if branch == "" && !q.Has("branch") && s.cfg != nil {
		branch = s.cfg.GetDashboardDefaultBranch()
	}
	if sortBy == "" {
		sortBy = s.getDashboardSortBy()
	}
	if sortOrder == "" {
		sortOrder = s.getDashboardSortOrder()
	}

	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		page = p
	}

	pageSize := s.getDashboardPageSize()
	offset := (page - 1) * pageSize

	var providerName session.ProviderName
	if provider != "" {
		parsed, err := session.ParseProviderName(provider)
		if err == nil {
			providerName = parsed
		}
	}

	result, err := s.sessionSvc.Search(service.SearchRequest{
		Keyword:         keyword,
		Branch:          branch,
		Provider:        providerName,
		ProjectPath:     project,
		OwnerID:         session.ID(owner),
		SessionType:     sessionType,
		ProjectCategory: projectCategory,
		Status:          status,
		HasErrors:       hasErrors,
		Since:           since,
		Until:           until,
		Limit:           pageSize,
		Offset:          offset,
	})
	if err != nil {
		s.logger.Printf("sessions search error: %v", err)
		cols := s.buildColumnDefs()
		return sessionsPage{Nav: "sessions", SidebarProjects: s.buildSidebarProjects(r.Context(), project), Projects: s.buildProjectList(project), Columns: cols, SortBy: sortBy, SortOrder: sortOrder}
	}

	totalPages := (result.TotalCount + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}

	cols := s.buildColumnDefs()
	rows := s.buildSessionRows(result.Sessions, cols)

	return sessionsPage{
		Nav:                   "sessions",
		SidebarProjects:       s.buildSidebarProjects(r.Context(), project),
		FilterKeyword:         keyword,
		FilterBranch:          branch,
		FilterProvider:        provider,
		FilterProject:         project,
		FilterOwner:           owner,
		FilterSessionType:     sessionType,
		FilterProjectCategory: projectCategory,
		FilterStatus:          status,
		FilterHasErrors:       hasErrors,
		FilterSince:           since,
		FilterUntil:           until,
		SortBy:                sortBy,
		SortOrder:             sortOrder,
		Projects:              s.buildProjectList(project),
		Columns:               cols,
		Rows:                  rows,
		TotalCount:            result.TotalCount,
		Page:                  page,
		PageSize:              pageSize,
		TotalPages:            totalPages,
		HasPrev:               page > 1,
		HasNext:               page < totalPages,
	}
}

// getDashboardPrefs loads preferences from DB (user_preferences table), falling
// back to config file, then system defaults. Pass empty userID for global defaults.
func (s *Server) getDashboardPrefs() *session.DashboardPreferences {
	// 1. Try DB preferences (global defaults = empty userID)
	if s.store != nil {
		if prefs, err := s.store.GetPreferences(""); err == nil && prefs != nil {
			return &prefs.Dashboard
		}
	}
	// 2. Not found → return nil (callers use config or system defaults)
	return nil
}

// getDashboardPageSize returns the configured page size (or default 25).
func (s *Server) getDashboardPageSize() int {
	if p := s.getDashboardPrefs(); p != nil && p.PageSize > 0 {
		return p.PageSize
	}
	if s.cfg != nil {
		return s.cfg.GetDashboardPageSize()
	}
	return defaultPageSize
}

// getDashboardSortBy returns the configured sort field (or default "created_at").
func (s *Server) getDashboardSortBy() string {
	if p := s.getDashboardPrefs(); p != nil && p.SortBy != "" {
		return p.SortBy
	}
	if s.cfg != nil {
		return s.cfg.GetDashboardSortBy()
	}
	return "created_at"
}

// getDashboardSortOrder returns the configured sort order (or default "desc").
func (s *Server) getDashboardSortOrder() string {
	if p := s.getDashboardPrefs(); p != nil && p.SortOrder != "" {
		return p.SortOrder
	}
	if s.cfg != nil {
		return s.cfg.GetDashboardSortOrder()
	}
	return "desc"
}

// allColumnDefs maps column IDs to their display definitions.
var allColumnDefs = map[string]columnDef{
	"id":         {ID: "id", Label: "ID", Class: ""},
	"project":    {ID: "project", Label: "Project", Class: ""},
	"provider":   {ID: "provider", Label: "Provider", Class: ""},
	"agent":      {ID: "agent", Label: "Agent", Class: ""},
	"branch":     {ID: "branch", Label: "Branch", Class: ""},
	"summary":    {ID: "summary", Label: "Summary", Class: ""},
	"health":     {ID: "health", Label: "Health", Class: ""},
	"messages":   {ID: "messages", Label: "Msgs", Class: "text-right"},
	"tokens":     {ID: "tokens", Label: "Tokens", Class: "text-right"},
	"cost":       {ID: "cost", Label: "Cost", Class: "text-right"},
	"tools":      {ID: "tools", Label: "Tools", Class: "text-right"},
	"errors":     {ID: "errors", Label: "Errs", Class: "text-right"},
	"error_rate": {ID: "error_rate", Label: "Errs", Class: "text-right"}, // alias for "errors"
	"when":       {ID: "when", Label: "When", Class: ""},
}

// providerShortName returns a compact display label for provider names.
// "claude-code" → "CC", "opencode" → "OC", "cursor" → "CU".
func providerShortName(p session.ProviderName) string {
	switch p {
	case session.ProviderClaudeCode:
		return "CC"
	case session.ProviderOpenCode:
		return "OC"
	case session.ProviderCursor:
		return "CU"
	case session.ProviderParlay:
		return "PA"
	case session.ProviderOllama:
		return "OL"
	default:
		s := string(p)
		if len(s) > 4 {
			return strings.ToUpper(s[:2])
		}
		return strings.ToUpper(s)
	}
}

// buildColumnDefs returns the ordered column definitions based on config.
func (s *Server) buildColumnDefs() []columnDef {
	var colIDs []string

	// 1. Try DB preferences
	if p := s.getDashboardPrefs(); p != nil && len(p.Columns) > 0 {
		colIDs = p.Columns
	}
	// 2. Fall back to config file
	if len(colIDs) == 0 && s.cfg != nil {
		colIDs = s.cfg.GetDashboardColumns()
	}
	// 3. System defaults
	if len(colIDs) == 0 {
		colIDs = config.DefaultDashboardColumns
	}

	defs := make([]columnDef, 0, len(colIDs))
	for _, id := range colIDs {
		if d, ok := allColumnDefs[id]; ok {
			defs = append(defs, d)
		}
	}
	return defs
}

// estimateTokenCost gives a rough $/token estimate for the summary list.
// It uses an average blended rate ($3/Mtoken) since Summary doesn't carry model info.
func estimateTokenCost(totalTokens int) float64 {
	const blendedRatePerMToken = 3.0 // rough average across models
	return float64(totalTokens) * blendedRatePerMToken / 1_000_000
}

// buildSessionRows converts session summaries into template-friendly rows
// with pre-formatted cell values for the configured columns.
func (s *Server) buildSessionRows(sessions []session.Summary, cols []columnDef) []sessionRow {
	rows := make([]sessionRow, 0, len(sessions))

	for _, sess := range sessions {
		row := sessionRow{ID: string(sess.ID)}
		for _, col := range cols {
			cell := sessionCell{}
			switch col.ID {
			case "id":
				cell.Value = truncate(string(sess.ID), 12)
				cell.IsLink = true
				cell.LinkID = string(sess.ID)
				cell.Class = "font-mono text-muted"
			case "project":
				name := projectBaseName(sess.ProjectPath)
				if name == "" || name == "." {
					name = "—"
					cell.Class = "text-muted"
				} else {
					cell.IsLink = true
					cell.LinkHref = "/projects" + sess.ProjectPath
					cell.Class = "cell-project"
				}
				cell.Value = name
			case "provider":
				cell.Value = providerShortName(sess.Provider)
				cell.IsBadge = true
			case "agent":
				if sess.Agent != "" {
					cell.Value = sess.Agent
				} else {
					cell.Value = "—"
					cell.Class = "text-muted"
				}
			case "branch":
				if sess.Branch != "" {
					cell.Value = truncate(sess.Branch, 30)
					cell.Class = "cell-branch"
				} else {
					cell.Value = "—"
					cell.Class = "text-muted"
				}
			case "summary":
				if sess.Summary != "" {
					cell.Value = truncate(sess.Summary, 90)
					cell.IsLink = true
					cell.LinkID = string(sess.ID)
					cell.Class = "cell-summary"
				} else {
					cell.Value = truncate(string(sess.ID), 16)
					cell.IsLink = true
					cell.LinkID = string(sess.ID)
					cell.Class = "text-muted font-mono cell-summary"
				}
			case "health":
				hs := session.ComputeHealthScore(sess)
				cell.Value = fmt.Sprintf("%s %d", hs.Grade, hs.Total)
				cell.IsBadge = true
				switch {
				case hs.Total >= 85:
					cell.Class = "health-a"
				case hs.Total >= 70:
					cell.Class = "health-b"
				case hs.Total >= 55:
					cell.Class = "health-c"
				default:
					cell.Class = "health-d"
				}
			case "messages":
				cell.Value = strconv.Itoa(sess.MessageCount)
				cell.Class = "text-right"
			case "tokens":
				cell.Value = formatTokens(sess.TotalTokens)
				cell.Class = "text-right font-mono"
			case "cost":
				cost := estimateTokenCost(sess.TotalTokens)
				cell.Value = "~" + formatCost(cost)
				cell.Class = "text-right font-mono"
			case "tools":
				cell.Value = strconv.Itoa(sess.ToolCallCount)
				cell.Class = "text-right"
			case "errors", "error_rate":
				cell.Value = strconv.Itoa(sess.ErrorCount)
				cell.Class = "text-right"
				if sess.ErrorCount > 0 {
					cell.Class = "text-right text-error"
				}
			case "when":
				when := sess.CreatedAt
				if !sess.UpdatedAt.IsZero() && sess.UpdatedAt.After(when) {
					when = sess.UpdatedAt
				}
				cell.Value = timeAgo(when)
				cell.Class = "text-muted"
			}
			row.Cells = append(row.Cells, cell)
		}
		rows = append(rows, row)
	}
	return rows
}

// ── Session Detail ──

// toolUsageEntry is a template-friendly view of a tool usage entry.
type toolUsageEntry struct {
	Name         string
	Calls        int
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	AvgDuration  int
	ErrorCount   int
	Percentage   float64
}

type sessionDetailPage struct {
	Nav           string
	Session       *session.Session
	TotalCost     float64
	CostBreakdown []session.ModelCost
	ToolCallCount int
	ErrorCount    int
	ErrorRate     float64 // 0-100 percentage
	RestoreCmd    string

	// Token clarity: billed vs actual context
	PeakContext     int     // peak input_tokens (real conversation size)
	PeakContextStr  string  // formatted "220K"
	TotalOutput     int     // sum of all output_tokens
	TotalOutputStr  string  // formatted "5.2M"
	BilledTokens    int     // total_tokens (cumulative input charged by API)
	BilledTokensStr string  // formatted "1.28B"
	ContextRatio    float64 // BilledTokens / PeakContext — how many times context was re-sent

	// Session objective (work description)
	HasObjective bool
	Objective    *objectiveView

	// Tool usage breakdown (Step 6)
	HasToolUsage bool
	ToolUsage    []toolUsageEntry

	// Activity bar (visual message flow)
	ActivitySegments []activitySegment

	// Fork tree
	HasForks bool
	ForkRels []forkRelView

	// Session-to-session links (delegation, continuation, etc.)
	HasSessionLinks bool
	SessionLinks    []sessionLinkView

	// File changes (from file_changes table, not provider)
	HasFileChanges bool
	FileChanges    []fileChangeView

	// Analysis (Step 9)
	HasAnalysis  bool
	CanAnalyze   bool // true when analysisSvc is wired
	Analysis     *analysisView
	AnalysisData analysisPartialData // pre-built data for the analysis_partial template
}

// fileChangeView is a template-friendly view of a file operation.
type fileChangeView struct {
	FilePath   string
	ChangeType string // created, modified, read, deleted
	ToolName   string // e.g. "Write", "Edit", "Bash"
}

// objectiveView is a template-friendly view of a session's work objective.
type objectiveView struct {
	Intent       string
	Outcome      string
	Decisions    []string
	Friction     []string
	OpenItems    []string
	ExplainShort string
}

// forkRelView is a template-friendly view of a fork relation.
type forkRelView struct {
	OriginalID     string
	ForkID         string
	ForkPoint      int
	SharedMessages int
	OverlapPct     int
	Reason         string
	Direction      string // "parent" (this session is the original) or "fork" (this session is the fork)
	LinkedID       string // the OTHER session's ID (for link)
}

// activitySegment represents one message segment in the activity bar.
type activitySegment struct {
	Role     string  // "user", "assistant", "tool"
	WidthPct float64 // percentage width (0-100)
	Tokens   int
	Index    int // 0-based message index
}

// sessionLinkView is a template-friendly view of a session-to-session link.
type sessionLinkView struct {
	ID              string
	TargetSessionID string
	LinkType        string
	LinkTypeClass   string // CSS class: "delegated", "continuation", "related", "follow-up", "replay"
	Description     string
	Direction       string // "outgoing" or "incoming"
}

// analysisView is a template-friendly view of a SessionAnalysis.
type analysisView struct {
	ID                 string
	Score              int
	ScoreClass         string // "good", "warning", "poor"
	Summary            string
	Trigger            string
	Adapter            string
	DurationMs         int
	CreatedAt          string // formatted time ago
	Error              string
	HasProblems        bool
	Problems           []problemView
	HasRecommendations bool
	Recommendations    []recommendationView
	HasSkills          bool
	SkillSuggestions   []skillView

	// Skill observation (available vs loaded vs missed)
	HasSkillObservation  bool
	SkillsAvailable      []string
	SkillsRecommended    []string
	SkillsLoaded         []string
	SkillsMissed         []string
	SkillsDiscovered     []string
	SkillMissedCount     int
	SkillDiscoveredCount int
}

type problemView struct {
	Severity      string
	SeverityClass string // "high", "medium", "low"
	Description   string
	ToolName      string
}

type recommendationView struct {
	Category      string
	CategoryClass string // CSS class for category badge
	Title         string
	Description   string
	Priority      int
}

type skillView struct {
	Name        string
	Description string
	Trigger     string
}

// handleSessionDetail renders a single session.
func (s *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	sess, err := s.sessionSvc.Get(id)
	if err != nil {
		s.logger.Printf("session detail: %v", err)
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	data := sessionDetailPage{
		Nav:     "sessions",
		Session: sess,
	}

	// Compute token clarity: peak context vs billed total.
	var peakInput, totalOutput int
	for _, msg := range sess.Messages {
		if msg.InputTokens > peakInput {
			peakInput = msg.InputTokens
		}
		totalOutput += msg.OutputTokens
	}
	data.PeakContext = peakInput
	data.PeakContextStr = formatTokens(peakInput)
	data.TotalOutput = totalOutput
	data.TotalOutputStr = formatTokens(totalOutput)
	data.BilledTokens = sess.TokenUsage.TotalTokens
	data.BilledTokensStr = formatTokens(sess.TokenUsage.TotalTokens)
	if peakInput > 0 {
		data.ContextRatio = float64(sess.TokenUsage.TotalTokens) / float64(peakInput)
	}

	// Count tool calls and errors across all messages.
	for i := range sess.Messages {
		for _, tc := range sess.Messages[i].ToolCalls {
			data.ToolCallCount++
			if tc.State == session.ToolStateError {
				data.ErrorCount++
			}
		}
	}
	if data.ToolCallCount > 0 {
		data.ErrorRate = float64(data.ErrorCount) / float64(data.ToolCallCount) * 100
	}

	// Compute cost breakdown via the session service (respects user pricing overrides).
	estimate, _ := s.sessionSvc.EstimateCost(r.Context(), string(sess.ID))
	if estimate != nil {
		data.TotalCost = estimate.TotalCost.TotalCost
		data.CostBreakdown = estimate.PerModel
	}

	data.RestoreCmd = buildRestoreCmd(string(sess.ID), "", false)

	// Session objective (work description).
	if s.store != nil {
		obj, objErr := s.store.GetObjective(sess.ID)
		if objErr == nil && obj != nil {
			data.HasObjective = true
			data.Objective = &objectiveView{
				Intent:       obj.Summary.Intent,
				Outcome:      obj.Summary.Outcome,
				Decisions:    obj.Summary.Decisions,
				Friction:     obj.Summary.Friction,
				OpenItems:    obj.Summary.OpenItems,
				ExplainShort: obj.ExplainShort,
			}
		}
	}

	// Activity bar segments
	if totalTokens := sess.TokenUsage.TotalTokens; totalTokens > 0 && len(sess.Messages) > 0 {
		for i := range sess.Messages {
			msg := &sess.Messages[i]
			msgTokens := msg.InputTokens + msg.OutputTokens
			if msgTokens == 0 {
				msgTokens = 1 // minimum 1 for visibility
			}
			data.ActivitySegments = append(data.ActivitySegments, activitySegment{
				Role:     string(msg.Role),
				WidthPct: float64(msgTokens) / float64(totalTokens) * 100,
				Tokens:   msgTokens,
				Index:    i,
			})
		}
	}

	// Fork relations
	var forkRels []session.ForkRelation
	var forkErr error
	if s.store != nil {
		forkRels, forkErr = s.store.GetForkRelations(sess.ID)
	}
	if forkErr == nil && len(forkRels) > 0 {
		data.HasForks = true
		for _, rel := range forkRels {
			view := forkRelView{
				OriginalID:     string(rel.OriginalID),
				ForkID:         string(rel.ForkID),
				ForkPoint:      rel.ForkPoint,
				SharedMessages: rel.SharedMessages,
				OverlapPct:     int(rel.OverlapRatio * 100),
				Reason:         rel.Reason,
			}
			if rel.OriginalID == sess.ID {
				view.Direction = "parent"
				view.LinkedID = string(rel.ForkID)
			} else {
				view.Direction = "fork"
				view.LinkedID = string(rel.OriginalID)
			}
			if view.Reason == "" && rel.ForkContext != "" {
				if len(rel.ForkContext) > 100 {
					view.Reason = rel.ForkContext[:97] + "..."
				} else {
					view.Reason = rel.ForkContext
				}
			}
			data.ForkRels = append(data.ForkRels, view)
		}
	}

	// Tool usage breakdown
	toolStats, tuErr := s.sessionSvc.ToolUsage(r.Context(), id)
	if tuErr == nil && len(toolStats.Tools) > 0 {
		data.HasToolUsage = true
		for _, t := range toolStats.Tools {
			data.ToolUsage = append(data.ToolUsage, toolUsageEntry{
				Name:         t.Name,
				Calls:        t.Calls,
				InputTokens:  t.InputTokens,
				OutputTokens: t.OutputTokens,
				TotalTokens:  t.TotalTokens,
				AvgDuration:  t.AvgDuration,
				ErrorCount:   t.ErrorCount,
				Percentage:   t.Percentage,
			})
		}
	}

	// Session-to-session links (delegation, continuation, etc.)
	links, linksErr := s.sessionSvc.GetLinkedSessions(r.Context(), sess.ID)
	if linksErr == nil && len(links) > 0 {
		data.HasSessionLinks = true
		for _, link := range links {
			view := sessionLinkView{
				ID:          string(link.ID),
				LinkType:    string(link.LinkType),
				Description: link.Description,
			}
			// Determine direction and target.
			if link.SourceSessionID == sess.ID {
				view.Direction = "outgoing"
				view.TargetSessionID = string(link.TargetSessionID)
			} else {
				view.Direction = "incoming"
				view.TargetSessionID = string(link.SourceSessionID)
			}
			// CSS class for link type badge.
			switch link.LinkType {
			case session.SessionLinkDelegatedTo, session.SessionLinkDelegatedFrom:
				view.LinkTypeClass = "delegated"
			case session.SessionLinkContinuation:
				view.LinkTypeClass = "continuation"
			case session.SessionLinkFollowUp:
				view.LinkTypeClass = "follow-up"
			case session.SessionLinkReplayOf:
				view.LinkTypeClass = "replay"
			default:
				view.LinkTypeClass = "related"
			}
			data.SessionLinks = append(data.SessionLinks, view)
		}
	}

	// File changes — populate from file_changes table (extracted file blame data).
	// The provider-supplied FileChanges is typically empty; the extracted data is richer.
	// Use the store directly (sessionSvc.GetSessionFiles may not work in remote mode).
	var fileRecords []session.SessionFileRecord
	var fcErr error
	if s.store != nil {
		fileRecords, fcErr = s.store.GetSessionFileChanges(sess.ID)
	}
	if fcErr == nil && len(fileRecords) > 0 {
		data.HasFileChanges = true
		for _, fr := range fileRecords {
			data.FileChanges = append(data.FileChanges, fileChangeView{
				FilePath:   fr.FilePath,
				ChangeType: string(fr.ChangeType),
				ToolName:   fr.ToolName,
			})
		}
	} else if len(sess.FileChanges) > 0 {
		// Fallback to provider-supplied file changes if no extracted data.
		data.HasFileChanges = true
		for _, fc := range sess.FileChanges {
			data.FileChanges = append(data.FileChanges, fileChangeView{
				FilePath:   fc.FilePath,
				ChangeType: string(fc.ChangeType),
			})
		}
	}

	// Analysis
	if s.analysisSvc != nil {
		data.CanAnalyze = true
		data.AnalysisData = analysisPartialData{
			SessionID:  id,
			CanAnalyze: true,
		}
		sa, aErr := s.analysisSvc.GetLatestAnalysis(id)
		if aErr == nil && sa != nil {
			data.HasAnalysis = true
			data.Analysis = buildAnalysisView(sa)
			data.AnalysisData.HasAnalysis = true
			data.AnalysisData.Analysis = data.Analysis
		}
	}

	s.render(w, "session_detail.html", data)
}

// handleRestoreCommand renders the restore command partial (HTMX).
func (s *Server) handleRestoreCommand(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	provider := r.URL.Query().Get("provider")
	asContext := r.URL.Query().Get("context") == "true"

	cmd := buildRestoreCmd(id, provider, asContext)
	s.renderPartial(w, "restore_command", cmd)
}

// buildRestoreCmd generates the aisync CLI restore command string.
func buildRestoreCmd(sessionID, provider string, asContext bool) string {
	cmd := "aisync restore --session " + sessionID
	if asContext {
		cmd += " --as-context"
	} else if provider != "" && provider != "context" {
		cmd += " --provider " + provider
	}
	if provider == "context" && !asContext {
		cmd += " --as-context"
	}
	return cmd
}

// ── Branch Explorer ──

// branchData holds a branch's stats plus its session tree.
type branchData struct {
	Name         string
	Slug         string // URL-safe anchor
	SessionCount int
	TotalTokens  int
	TotalCost    float64
	Sessions     []session.SessionTreeNode
}

type branchExplorerPage struct {
	Nav             string
	SidebarProjects []sidebarProject
	Branches        []branchData
	Projects        []projectItem
	Objectives      map[string]*objectiveView // session ID → objective (for inline display)
}

// handleBranches renders the branch explorer page.
func (s *Server) handleBranches(w http.ResponseWriter, r *http.Request) {
	data := s.buildBranchesData(r)
	s.render(w, "branch_explorer.html", data)
}

func (s *Server) buildBranchesData(r *http.Request) branchExplorerPage {
	project := r.URL.Query().Get("project")
	data := branchExplorerPage{Nav: "branches", SidebarProjects: s.buildSidebarProjects(r.Context(), project), Projects: s.buildProjectList(project)}

	// Get per-branch stats (cached).
	statsReq := service.StatsRequest{All: project == "", ProjectPath: project}
	stats, err := s.cachedStats(statsReq)
	if err != nil {
		s.logger.Printf("branches stats error: %v", err)
		return data
	}

	if len(stats.PerBranch) == 0 {
		return data
	}

	// Build tree per branch. We sort branches by session count descending.
	sort.Slice(stats.PerBranch, func(i, j int) bool {
		return stats.PerBranch[i].SessionCount > stats.PerBranch[j].SessionCount
	})

	// Collect all session IDs for objective lookup.
	var allSessionIDs []session.ID

	for _, bs := range stats.PerBranch {
		tree, treeErr := s.sessionSvc.ListTree(r.Context(), service.ListRequest{
			Branch:      bs.Branch,
			All:         project == "",
			ProjectPath: project,
		})
		if treeErr != nil {
			s.logger.Printf("branches tree %q: %v", bs.Branch, treeErr)
			continue
		}

		// Collect session IDs from tree nodes.
		for _, node := range tree {
			allSessionIDs = append(allSessionIDs, node.Summary.ID)
		}

		data.Branches = append(data.Branches, branchData{
			Name:         bs.Branch,
			Slug:         slugify(bs.Branch),
			SessionCount: bs.SessionCount,
			TotalTokens:  bs.TotalTokens,
			TotalCost:    bs.TotalCost,
			Sessions:     tree,
		})
	}

	// Bulk-load objectives for all sessions in the branch explorer.
	if s.store != nil && len(allSessionIDs) > 0 {
		objs, objErr := s.store.ListObjectives(allSessionIDs)
		if objErr == nil && len(objs) > 0 {
			data.Objectives = make(map[string]*objectiveView, len(objs))
			for id, obj := range objs {
				data.Objectives[string(id)] = &objectiveView{
					Intent:       obj.Summary.Intent,
					Outcome:      obj.Summary.Outcome,
					ExplainShort: obj.ExplainShort,
				}
			}
		}
	}

	return data
}

// ── Branch Timeline ──

type branchTimelinePage struct {
	Nav        string
	BranchName string
	Entries    []timelineEntryView
	HasEntries bool
}

type timelineEntryView struct {
	Type            string // "session" or "commit"
	TimeAgo         string
	SessionID       string
	SessionSummary  string
	SessionType     string
	SessionMessages int
	SessionTokens   int
	SessionProvider string
	Intent          string
	Outcome         string
	ExplainShort    string
	CommitSHA       string
	CommitMessage   string
	CommitAuthor    string
	LinkedSessionID string
}

func (s *Server) handleBranchTimeline(w http.ResponseWriter, r *http.Request) {
	branchName := r.PathValue("name")
	if branchName == "" {
		http.Redirect(w, r, "/branches", http.StatusFound)
		return
	}

	data := branchTimelinePage{
		Nav:        "branches",
		BranchName: branchName,
	}

	entries, err := s.sessionSvc.BranchTimeline(r.Context(), service.TimelineRequest{
		Branch: branchName,
		Limit:  50,
	})
	if err != nil {
		s.logger.Printf("branch timeline error: %v", err)
		s.render(w, "branch_timeline.html", data)
		return
	}

	for _, e := range entries {
		view := timelineEntryView{
			Type:    e.Type,
			TimeAgo: timeAgoString(e.Timestamp),
		}

		if e.Session != nil {
			view.SessionID = string(e.Session.ID)
			view.SessionSummary = e.Session.Summary
			view.SessionType = e.Session.SessionType
			view.SessionMessages = e.Session.MessageCount
			view.SessionTokens = e.Session.TotalTokens
			view.SessionProvider = string(e.Session.Provider)
		}

		if e.Objective != nil {
			view.Intent = e.Objective.Summary.Intent
			view.Outcome = e.Objective.Summary.Outcome
			view.ExplainShort = e.Objective.ExplainShort
		}

		if e.Commit != nil {
			view.CommitSHA = e.Commit.ShortSHA
			view.CommitMessage = e.Commit.Message
			view.CommitAuthor = e.Commit.Author
		}

		view.LinkedSessionID = string(e.LinkedSessionID)
		data.Entries = append(data.Entries, view)
	}
	data.HasEntries = len(data.Entries) > 0

	s.render(w, "branch_timeline.html", data)
}

// timeAgoString converts a timestamp to a human-readable "X ago" string.
func timeAgoString(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// ── Token Usage ──

type usagePage struct {
	Nav             string
	SidebarProjects []sidebarProject
	HasData         bool
	Projects        []projectItem
	Hours           []string
	HeatmapRows     []heatmapRow
	BarChartData    []barChartBar
	PeakHour        int
	NightTokens     int
	DayTokens       int
	EveningTokens   int
	NightPct        int
	DayPct          int
	EveningPct      int

	// Breakdown by time of day
	NightToolCalls    int
	DayToolCalls      int
	EveningToolCalls  int
	NightImages       int
	DayImages         int
	EveningImages     int
	NightUserMsgs     int
	DayUserMsgs       int
	EveningUserMsgs   int
	NightAssistMsgs   int
	DayAssistMsgs     int
	EveningAssistMsgs int
	TotalToolCalls    int
	TotalImages       int
}

type heatmapRow struct {
	DayLabel string
	Cells    []heatmapCell
}

type heatmapCell struct {
	Color string
	Label string
}

type barChartBar struct {
	Hour      string
	HeightPct int
	Label     string
}

// ── Analytics (combined view) ──

type analyticsPage struct {
	Nav            string
	TotalSessions  int
	TotalTokens    int
	TotalCost      float64
	TotalToolCalls int
	TotalErrors    int
	BillingType    string
}

// handleAnalytics is now in event_handlers.go — this placeholder remains for
// the analytics.html page (basic stats tab).

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	data := usagePage{Nav: "usage", SidebarProjects: s.buildSidebarProjects(r.Context(), project)}
	data.Hours = make([]string, 24)
	for i := 0; i < 24; i++ {
		data.Hours[i] = fmt.Sprintf("%d", i)
	}

	now := time.Now()
	since := now.AddDate(0, 0, -7)
	buckets, err := s.sessionSvc.QueryTokenUsage(r.Context(), service.QueryTokenUsageRequest{
		Granularity: "1h",
		Since:       since,
		Until:       now,
		ProjectPath: project,
	})
	if err != nil || len(buckets) == 0 {
		s.render(w, "usage.html", data)
		return
	}

	data.HasData = true

	type dayHour struct{ day, hour int }
	cells := make(map[dayHour]int)
	var maxTokens int

	for _, b := range buckets {
		daysAgo := int(now.Sub(b.BucketStart).Hours() / 24)
		if daysAgo < 0 || daysAgo > 6 {
			continue
		}
		hour := b.BucketStart.Hour()
		key := dayHour{day: daysAgo, hour: hour}
		total := b.InputTokens + b.OutputTokens
		cells[key] += total
		if cells[key] > maxTokens {
			maxTokens = cells[key]
		}
	}

	dayNames := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	for d := 6; d >= 0; d-- {
		day := now.AddDate(0, 0, -d)
		row := heatmapRow{DayLabel: dayNames[day.Weekday()], Cells: make([]heatmapCell, 24)}
		for h := 0; h < 24; h++ {
			tokens := cells[dayHour{day: d, hour: h}]
			intensity := 0.0
			if maxTokens > 0 {
				intensity = float64(tokens) / float64(maxTokens)
			}
			row.Cells[h] = heatmapCell{
				Color: heatmapColor(intensity),
				Label: fmt.Sprintf("%s %02d:00 — %s tokens", dayNames[day.Weekday()], h, formatTokensInt(tokens)),
			}
		}
		data.HeatmapRows = append(data.HeatmapRows, row)
	}

	hourlyTotals := make([]int, 24)
	for _, b := range buckets {
		hourlyTotals[b.BucketStart.Hour()] += b.InputTokens + b.OutputTokens
	}
	maxHourly := 1
	for _, t := range hourlyTotals {
		if avg := t / 7; avg > maxHourly {
			maxHourly = avg
		}
	}

	var peakHour, totalAll, nightTotal, dayTotal, eveningTotal int
	for h := 0; h < 24; h++ {
		avg := hourlyTotals[h] / 7
		pct := 0
		if maxHourly > 0 {
			pct = avg * 100 / maxHourly
		}
		data.BarChartData = append(data.BarChartData, barChartBar{
			Hour:      fmt.Sprintf("%d", h),
			HeightPct: pct,
			Label:     fmt.Sprintf("%02d:00 avg: %s tokens", h, formatTokensInt(avg)),
		})
		totalAll += hourlyTotals[h]
		if h < 6 {
			nightTotal += hourlyTotals[h]
		} else if h < 18 {
			dayTotal += hourlyTotals[h]
		} else {
			eveningTotal += hourlyTotals[h]
		}
		if hourlyTotals[h] > hourlyTotals[peakHour] {
			peakHour = h
		}
	}

	data.PeakHour = peakHour
	data.NightTokens = nightTotal
	data.DayTokens = dayTotal
	data.EveningTokens = eveningTotal
	if totalAll > 0 {
		data.NightPct = nightTotal * 100 / totalAll
		data.DayPct = dayTotal * 100 / totalAll
		data.EveningPct = eveningTotal * 100 / totalAll
	}

	// Aggregate tool calls, images, messages by time of day.
	for _, b := range buckets {
		h := b.BucketStart.Hour()
		data.TotalToolCalls += b.ToolCallCount
		data.TotalImages += b.ImageCount
		if h < 6 {
			data.NightToolCalls += b.ToolCallCount
			data.NightImages += b.ImageCount
			data.NightUserMsgs += b.UserMsgCount
			data.NightAssistMsgs += b.AssistMsgCount
		} else if h < 18 {
			data.DayToolCalls += b.ToolCallCount
			data.DayImages += b.ImageCount
			data.DayUserMsgs += b.UserMsgCount
			data.DayAssistMsgs += b.AssistMsgCount
		} else {
			data.EveningToolCalls += b.ToolCallCount
			data.EveningImages += b.ImageCount
			data.EveningUserMsgs += b.UserMsgCount
			data.EveningAssistMsgs += b.AssistMsgCount
		}
	}

	s.render(w, "usage.html", data)
}

func heatmapColor(intensity float64) string {
	if intensity <= 0 {
		return "var(--bg-card)"
	}
	if intensity < 0.25 {
		return "rgba(108,126,225,0.2)"
	}
	if intensity < 0.5 {
		return "rgba(108,126,225,0.4)"
	}
	if intensity < 0.75 {
		return "rgba(108,126,225,0.6)"
	}
	return "rgba(108,126,225,0.9)"
}

func formatTokensInt(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// slugify converts a branch name like "feature/my-branch" to "feature-my-branch".
func slugify(s string) string {
	r := strings.NewReplacer("/", "-", " ", "-", ".", "-")
	return strings.ToLower(r.Replace(s))
}

// ── Cost Dashboard ──

// costBucket is a template-friendly view of a forecast bucket.
type costBucket struct {
	Label        string
	Cost         float64
	SessionCount int
	HeightPct    float64 // 0–100, for bar chart rendering
}

// branchCostEntry is a template-friendly view of per-branch cost.
type branchCostEntry struct {
	Name         string
	Slug         string
	SessionCount int
	TotalTokens  int
	TotalCost    float64
}

type costDashboardPage struct {
	Nav             string
	SidebarProjects []sidebarProject
	HasData         bool
	Projects        []projectItem
	ProjectQuery    string  // raw project query param for tab URLs
	TotalCost       float64 // API-equivalent cost
	ActualCost      float64 // actual cost from providers
	Savings         float64 // TotalCost - ActualCost
	BillingType     string  // "subscription", "api", "mixed"
	TotalSessions   int
	AvgPerSession   float64

	// Forecast (API-equivalent — all sessions)
	HasForecast  bool
	Projected30d float64
	Projected90d float64
	TrendPerDay  float64
	TrendDir     string

	// Real cost forecast (API-only spend + subscriptions)
	HasRealForecast     bool
	APIProjected30d     float64
	APIProjected90d     float64
	APITrendPerDay      float64
	APITrendDir         string
	SubscriptionMonthly float64
	TotalReal30d        float64 // subscription + API projected 30d

	// Per-backend cost summary
	HasBackends  bool
	BackendCosts []backendCostView

	// Time buckets
	Period  string
	Buckets []costBucket

	// Model breakdown
	Models []session.ModelForecast

	// Per-branch cost
	BranchCosts []branchCostEntry

	// Per-tool cost (populated when project is selected)
	HasToolCosts  bool
	ToolCosts     []toolCostView
	TopMCPServers []mcpServerView
	TotalMCPCalls int
	TotalMCPCost  float64

	// Per-agent cost (populated when project is selected)
	HasAgentCosts bool
	AgentCosts    []agentCostView

	// Cache efficiency
	HasCacheData         bool
	CacheHitRate         float64 // 0-100
	CacheSavings         float64 // $ saved
	CacheWaste           float64 // $ wasted
	CacheReadTokens      int
	CacheWriteTokens     int
	CacheTotalInput      int
	CacheTotalSessions   int
	CacheSessionsMiss    int     // sessions with at least 1 cache miss
	CacheSessionsMissPct float64 // % of sessions with miss
	CacheTotalMisses     int     // total cache miss messages
	CacheAvgGap          float64 // avg gap between messages (minutes)
	CacheAvgMissGap      float64 // avg gap for miss messages (minutes)
	WorstCacheSessions   []cacheMissView

	// Cross-project MCP matrix
	HasMCPMatrix bool
	MCPMatrix    *mcpMatrixView

	// Model alternatives (benchmark-based recommendations)
	HasAlternatives   bool
	ModelAlternatives []modelAlternativeView

	// QAC leaderboard (all models ranked by quality-adjusted cost)
	HasQACLeaderboard bool
	QACLeaderboard    []qacLeaderView

	// Configured vs Used (MCP server governance)
	HasConfigVsUsed bool
	ConfigVsUsed    []mcpStatusView
	CVUActiveCount  int
	CVUGhostCount   int
	CVUOrphanCount  int

	// Context saturation
	HasSaturation    bool
	SatAvgPeak       float64 // average peak saturation (0-100)
	SatAbove80       int     // sessions >80% saturation
	SatAbove90       int     // sessions >90% saturation
	SatCompacted     int     // sessions that hit compaction
	SatTotalSessions int
	SatModels        []modelSaturationView
	SatWorstSessions []sessionSaturationView

	// Budget overview (all projects with budgets)
	HasBudgets bool
	Budgets    []budgetOverviewView
}

// budgetOverviewView is a template-friendly view of a project budget.
type budgetOverviewView struct {
	ProjectName    string
	ProjectPath    string
	CostMode       string // "actual", "estimated", "all"
	MonthlyLimit   float64
	MonthlySpent   float64
	MonthlyPercent float64
	MonthlyAlert   string
	DailyLimit     float64
	DailySpent     float64
	DailyPercent   float64
	DailyAlert     string
	ProjectedMonth float64
	DaysRemaining  int
}

// cacheMissView is a template-friendly view of a cache miss session.
type cacheMissView struct {
	ID             string
	Summary        string
	CacheHitRate   float64
	CacheMissCount int
	WastedTokens   int
	WastedCost     float64
	LongestGapMins int
}

// backendCostView is a template-friendly view of a backend cost summary.
type backendCostView struct {
	Backend       string
	BillingType   string // "subscription", "api", "free"
	PlanName      string
	MonthlyCost   float64
	MessageCount  int
	TotalTokens   int
	EstimatedCost float64
	ActualCost    float64
	SessionCount  int
}

// toolCostView is a template-friendly view of per-tool cost data.
type toolCostView struct {
	Name           string
	Category       string // "builtin" or "mcp:server"
	IsMCP          bool
	CallCount      int
	TotalTokens    int
	ErrorCount     int
	AvgDuration    int // ms
	EstimatedCost  float64
	AvgCostPerCall float64
}

// mcpServerView is a template-friendly view of per-MCP-server aggregated costs.
type mcpServerView struct {
	Server         string
	ToolCount      int
	CallCount      int
	TotalTokens    int
	ErrorCount     int
	EstimatedCost  float64
	AvgCostPerCall float64
}

// agentCostView is a template-friendly view of per-agent cost data.
type agentCostView struct {
	Agent             string
	SessionCount      int
	MessageCount      int
	TotalTokens       int
	ToolCallCount     int
	EstimatedCost     float64
	AvgCostPerSession float64
}

// mcpMatrixView is a template-friendly cross-project MCP server matrix.
type mcpMatrixView struct {
	Servers    []string               // column headers (MCP server names)
	Projects   []mcpMatrixRowView     // rows (one per project)
	Totals     map[string]mcpCellView // server name → total cell
	TotalCost  float64
	TotalCalls int
}

// mcpMatrixRowView is one project's row in the MCP matrix.
type mcpMatrixRowView struct {
	ProjectPath string
	DisplayName string
	Cells       map[string]mcpCellView // server name → cell
	TotalCost   float64
	TotalCalls  int
}

// mcpCellView is a single (project × MCP server) intersection.
type mcpCellView struct {
	CallCount     int
	TotalTokens   int
	ErrorCount    int
	EstimatedCost float64
	HasData       bool // true if this cell has non-zero data
}

// modelAlternativeView is a template-friendly model swap recommendation.
type modelAlternativeView struct {
	CurrentModel string
	CurrentScore float64 // 0-100 benchmark %
	CurrentCost  float64 // $ per 1M input tokens

	AltModel string
	AltScore float64
	AltCost  float64

	ScoreDelta   float64 // positive = quality gain
	CostSavings  float64 // % saved
	QualityDrop  float64 // absolute quality loss (0 if gain)
	MonthlySaved float64 // $ estimated monthly savings

	Verdict      string  // "no-brainer", "tradeoff", "risky"
	VerdictClass string  // CSS class: "good", "warning", "poor"
	CurrentQAC   float64 // quality-adjusted cost for current model
	AltQAC       float64 // quality-adjusted cost for alternative
	QACSavings   float64 // % saved on QAC basis
}

// modelSaturationView is a template-friendly per-model context saturation summary.
type modelSaturationView struct {
	Model             string
	MaxInputTokens    int
	SessionCount      int
	AvgPeakPct        float64
	MaxPeakPct        float64
	CompactedCount    int
	Above80Count      int
	SatClass          string // CSS class: "good", "warning", "poor"
	EfficiencyVerdict string // "oversized", "well-sized", "tight", "saturated"
	AvgPeakTokens     int
	WastedCapacityPct float64
}

// sessionSaturationView is a template-friendly per-session saturation entry.
type sessionSaturationView struct {
	ID              string
	Summary         string
	Model           string
	PeakInputTokens int
	MaxInputTokens  int
	PeakSaturation  float64
	MessageCount    int
	WasCompacted    bool
	SatClass        string // CSS class
}

// qacLeaderView is a template-friendly QAC leaderboard entry.
type qacLeaderView struct {
	Rank           int
	Model          string
	BenchmarkScore float64
	InputCost      float64 // $ per 1M input tokens
	QAC            float64
	IsCurrentModel bool // true if the user currently uses this model
}

// mcpStatusView is a template-friendly MCP server governance entry.
type mcpStatusView struct {
	Name        string
	Status      string // "active", "ghost", "orphan"
	StatusClass string // CSS class
	Scope       string // "global", "project", "" for orphans
	Enabled     bool
	CallCount   int
	ToolCount   int
	TotalCost   float64
}

// handleCosts renders the cost dashboard.
func (s *Server) handleCosts(w http.ResponseWriter, r *http.Request) {
	data := s.buildCostsData(r)
	s.render(w, "cost_dashboard.html", data)
}

func (s *Server) buildCostsData(r *http.Request) costDashboardPage {
	project := r.URL.Query().Get("project")
	data := costDashboardPage{Nav: "costs", SidebarProjects: s.buildSidebarProjects(r.Context(), project), Projects: s.buildProjectList(project), ProjectQuery: project}

	// Stats for totals + per-branch (cached).
	statsReq := service.StatsRequest{All: project == "", ProjectPath: project}
	stats, err := s.cachedStats(statsReq)
	if err != nil {
		s.logger.Printf("costs stats error: %v", err)
		return data
	}

	if stats.TotalSessions == 0 {
		return data
	}

	data.HasData = true
	data.TotalCost = stats.TotalCost
	data.ActualCost = stats.ActualCost
	data.Savings = stats.Savings
	data.BillingType = stats.BillingType
	data.TotalSessions = stats.TotalSessions
	if stats.TotalSessions > 0 {
		data.AvgPerSession = stats.TotalCost / float64(stats.TotalSessions)
	}

	// Per-branch costs (sorted by cost descending).
	sort.Slice(stats.PerBranch, func(i, j int) bool {
		return stats.PerBranch[i].TotalCost > stats.PerBranch[j].TotalCost
	})
	for _, bs := range stats.PerBranch {
		data.BranchCosts = append(data.BranchCosts, branchCostEntry{
			Name:         bs.Branch,
			Slug:         slugify(bs.Branch),
			SessionCount: bs.SessionCount,
			TotalTokens:  bs.TotalTokens,
			TotalCost:    bs.TotalCost,
		})
	}

	// Forecast for trend + model breakdown + time buckets (cached).
	forecast, fErr := s.cachedForecast(r.Context(), service.ForecastRequest{
		Period: "weekly",
		Days:   90,
	})
	if fErr == nil && forecast.SessionCount > 0 {
		data.HasForecast = true
		data.Projected30d = forecast.Projected30d
		data.Projected90d = forecast.Projected90d
		data.TrendPerDay = forecast.TrendPerDay
		data.TrendDir = forecast.TrendDir
		data.Period = forecast.Period
		data.Models = forecast.ModelBreakdown

		// Real cost forecast (API-only + subscriptions).
		if forecast.TotalReal30d > 0 || forecast.APIProjected30d > 0 || forecast.SubscriptionMonthly > 0 {
			data.HasRealForecast = true
			data.APIProjected30d = forecast.APIProjected30d
			data.APIProjected90d = forecast.APIProjected90d
			data.APITrendPerDay = forecast.APITrendPerDay
			data.APITrendDir = forecast.APITrendDir
			data.SubscriptionMonthly = forecast.SubscriptionMonthly
			data.TotalReal30d = forecast.TotalReal30d
		}

		// Per-backend cost breakdown.
		if len(forecast.BackendCosts) > 0 {
			data.HasBackends = true
			for _, bc := range forecast.BackendCosts {
				data.BackendCosts = append(data.BackendCosts, backendCostView{
					Backend:       bc.Backend,
					BillingType:   bc.BillingType,
					PlanName:      bc.PlanName,
					MonthlyCost:   bc.MonthlyCost,
					MessageCount:  bc.MessageCount,
					TotalTokens:   bc.TotalTokens,
					EstimatedCost: bc.EstimatedCost,
					ActualCost:    bc.ActualCost,
					SessionCount:  bc.SessionCount,
				})
			}
		}

		// Convert buckets to template-friendly format with bar heights.
		if len(forecast.Buckets) > 0 {
			maxCost := 0.0
			for _, b := range forecast.Buckets {
				if b.Cost > maxCost {
					maxCost = b.Cost
				}
			}
			for _, b := range forecast.Buckets {
				heightPct := 0.0
				if maxCost > 0 {
					heightPct = (b.Cost / maxCost) * 100
				}
				if heightPct < 2 && b.Cost > 0 {
					heightPct = 2 // minimum visible bar
				}
				data.Buckets = append(data.Buckets, costBucket{
					Label:        b.Start.Format("Jan 2"),
					Cost:         b.Cost,
					SessionCount: b.SessionCount,
					HeightPct:    heightPct,
				})
			}
		}
	}

	if data.TrendDir == "" {
		data.TrendDir = "stable"
	}
	if data.APITrendDir == "" {
		data.APITrendDir = "stable"
	}

	// Per-tool and per-agent costs (always loaded — uses pre-computed buckets).
	since90d := time.Now().AddDate(0, 0, -90)
	toolSummary, toolErr := s.sessionSvc.ToolCostSummary(r.Context(), project, since90d, time.Time{})
	if toolErr == nil && toolSummary != nil && len(toolSummary.Tools) > 0 {
		data.HasToolCosts = true
		// Top 20 tools by cost.
		limit := 20
		if len(toolSummary.Tools) < limit {
			limit = len(toolSummary.Tools)
		}
		for _, t := range toolSummary.Tools[:limit] {
			data.ToolCosts = append(data.ToolCosts, toolCostView{
				Name:           t.Name,
				Category:       t.Category,
				IsMCP:          session.MCPServerName(t.Category) != "",
				CallCount:      t.CallCount,
				TotalTokens:    t.TotalTokens,
				ErrorCount:     t.ErrorCount,
				AvgDuration:    t.AvgDuration,
				EstimatedCost:  t.EstimatedCost,
				AvgCostPerCall: t.AvgCostPerCall,
			})
		}
		// MCP servers.
		for _, ms := range toolSummary.MCPServers {
			data.TopMCPServers = append(data.TopMCPServers, mcpServerView{
				Server:         ms.Server,
				ToolCount:      ms.ToolCount,
				CallCount:      ms.CallCount,
				TotalTokens:    ms.TotalTokens,
				ErrorCount:     ms.ErrorCount,
				EstimatedCost:  ms.EstimatedCost,
				AvgCostPerCall: ms.AvgCostPerCall,
			})
		}
		data.TotalMCPCalls = toolSummary.TotalMCPCalls
		data.TotalMCPCost = toolSummary.TotalMCPCost
	}

	// Configured vs Used analysis (when project is selected and registry is available).
	if project != "" && s.registrySvc != nil {
		cvuResult, cvuErr := s.registrySvc.ConfiguredVsUsed(project, since90d)
		if cvuErr == nil && cvuResult != nil && len(cvuResult.Servers) > 0 {
			data.HasConfigVsUsed = true
			data.CVUActiveCount = cvuResult.ActiveCount
			data.CVUGhostCount = cvuResult.GhostCount
			data.CVUOrphanCount = cvuResult.OrphanCount
			for _, srv := range cvuResult.Servers {
				statusClass := "good"
				switch srv.Status {
				case registry.MCPStatusGhost:
					statusClass = "warning"
				case registry.MCPStatusOrphan:
					statusClass = "poor"
				}
				data.ConfigVsUsed = append(data.ConfigVsUsed, mcpStatusView{
					Name:        srv.Name,
					Status:      string(srv.Status),
					StatusClass: statusClass,
					Scope:       string(srv.Scope),
					Enabled:     srv.Enabled,
					CallCount:   srv.CallCount,
					ToolCount:   srv.ToolCount,
					TotalCost:   srv.TotalCost,
				})
			}
		}
	}

	agentCosts, agentErr := s.sessionSvc.AgentCostSummary(r.Context(), project, since90d, time.Time{})
	if agentErr == nil && len(agentCosts) > 0 {
		data.HasAgentCosts = true
		for _, ac := range agentCosts {
			data.AgentCosts = append(data.AgentCosts, agentCostView{
				Agent:             ac.Agent,
				SessionCount:      ac.SessionCount,
				MessageCount:      ac.MessageCount,
				TotalTokens:       ac.TotalTokens,
				ToolCallCount:     ac.ToolCallCount,
				EstimatedCost:     ac.EstimatedCost,
				AvgCostPerSession: ac.AvgCostPerSession,
			})
		}
	}

	// Cache efficiency analysis.
	cacheEff, cacheErr := s.sessionSvc.CacheEfficiency(r.Context(), project, since90d)

	// Cross-project MCP matrix (only when no project filter — global view).
	if project == "" {
		matrix, mErr := s.sessionSvc.MCPCostMatrix(r.Context(), since90d, time.Time{})
		if mErr == nil && matrix != nil && len(matrix.Projects) > 0 {
			data.HasMCPMatrix = true
			mv := &mcpMatrixView{
				Servers:    matrix.Servers,
				TotalCost:  matrix.TotalCost,
				TotalCalls: matrix.TotalCalls,
				Totals:     make(map[string]mcpCellView),
			}
			for srv, st := range matrix.ServerTotals {
				mv.Totals[srv] = mcpCellView{
					CallCount:     st.CallCount,
					TotalTokens:   st.TotalTokens,
					ErrorCount:    st.ErrorCount,
					EstimatedCost: st.EstimatedCost,
					HasData:       st.CallCount > 0,
				}
			}
			for _, row := range matrix.Projects {
				rv := mcpMatrixRowView{
					ProjectPath: row.ProjectPath,
					DisplayName: row.DisplayName,
					Cells:       make(map[string]mcpCellView),
					TotalCost:   row.TotalCost,
					TotalCalls:  row.TotalCalls,
				}
				for srv, cell := range row.Cells {
					rv.Cells[srv] = mcpCellView{
						CallCount:     cell.CallCount,
						TotalTokens:   cell.TotalTokens,
						ErrorCount:    cell.ErrorCount,
						EstimatedCost: cell.EstimatedCost,
						HasData:       cell.CallCount > 0,
					}
				}
				mv.Projects = append(mv.Projects, rv)
			}
			data.MCPMatrix = mv
		}
	}

	// Context saturation analysis.
	saturation, satErr := s.sessionSvc.ContextSaturation(r.Context(), project, since90d)
	if satErr == nil && saturation != nil && saturation.TotalSessions > 0 {
		data.HasSaturation = true
		data.SatAvgPeak = saturation.AvgPeakSaturation
		data.SatAbove80 = saturation.SessionsAbove80
		data.SatAbove90 = saturation.SessionsAbove90
		data.SatCompacted = saturation.SessionsCompacted
		data.SatTotalSessions = saturation.TotalSessions

		for _, ms := range saturation.Models {
			satClass := "good"
			if ms.AvgPeakPct > 80 {
				satClass = "poor"
			} else if ms.AvgPeakPct > 60 {
				satClass = "warning"
			}
			data.SatModels = append(data.SatModels, modelSaturationView{
				Model:             ms.Model,
				MaxInputTokens:    ms.MaxInputTokens,
				SessionCount:      ms.SessionCount,
				AvgPeakPct:        ms.AvgPeakPct,
				MaxPeakPct:        ms.MaxPeakPct,
				CompactedCount:    ms.CompactedCount,
				Above80Count:      ms.Above80Count,
				SatClass:          satClass,
				EfficiencyVerdict: ms.EfficiencyVerdict,
				AvgPeakTokens:     ms.AvgPeakTokens,
				WastedCapacityPct: ms.WastedCapacityPct,
			})
		}

		for _, ws := range saturation.WorstSessions {
			satClass := "good"
			if ws.PeakSaturation > 90 {
				satClass = "poor"
			} else if ws.PeakSaturation > 80 {
				satClass = "warning"
			}
			data.SatWorstSessions = append(data.SatWorstSessions, sessionSaturationView{
				ID:              string(ws.ID),
				Summary:         truncate(ws.Summary, 50),
				Model:           ws.Model,
				PeakInputTokens: ws.PeakInputTokens,
				MaxInputTokens:  ws.MaxInputTokens,
				PeakSaturation:  ws.PeakSaturation,
				MessageCount:    ws.MessageCount,
				WasCompacted:    ws.WasCompacted,
				SatClass:        satClass,
			})
		}
	}

	if cacheErr == nil && cacheEff != nil && cacheEff.TotalInputTokens > 0 {
		data.HasCacheData = true
		data.CacheHitRate = cacheEff.CacheHitRate
		data.CacheSavings = cacheEff.EstimatedSavings
		data.CacheWaste = cacheEff.EstimatedWaste
		data.CacheReadTokens = cacheEff.TotalCacheRead
		data.CacheWriteTokens = cacheEff.TotalCacheWrite
		data.CacheTotalInput = cacheEff.TotalInputTokens
		data.CacheTotalSessions = cacheEff.TotalSessions
		data.CacheSessionsMiss = cacheEff.SessionsWithMiss
		if cacheEff.TotalSessions > 0 {
			data.CacheSessionsMissPct = float64(cacheEff.SessionsWithMiss) / float64(cacheEff.TotalSessions) * 100
		}
		data.CacheTotalMisses = cacheEff.TotalCacheMisses
		data.CacheAvgGap = cacheEff.AvgGapMinutes
		data.CacheAvgMissGap = cacheEff.AvgMissGapMinutes
		for _, ws := range cacheEff.WorstSessions {
			data.WorstCacheSessions = append(data.WorstCacheSessions, cacheMissView{
				ID:             string(ws.ID),
				Summary:        truncate(ws.Summary, 50),
				CacheHitRate:   ws.CacheHitRate,
				CacheMissCount: ws.CacheMissCount,
				WastedTokens:   ws.WastedTokens,
				WastedCost:     ws.WastedCost,
				LongestGapMins: ws.LongestGapMins,
			})
		}
	}

	// Model alternatives (benchmark-based recommendations).
	if s.benchmarkRec != nil && data.HasForecast && len(data.Models) > 0 {
		for _, m := range data.Models {
			alts := s.benchmarkRec.Recommend(m.Model, 0)
			// Take top 3 alternatives per model.
			limit := 3
			if len(alts) < limit {
				limit = len(alts)
			}
			for _, alt := range alts[:limit] {
				verdictClass := "warning"
				switch alt.Verdict {
				case "no-brainer":
					verdictClass = "good"
				case "risky":
					verdictClass = "poor"
				}
				data.ModelAlternatives = append(data.ModelAlternatives, modelAlternativeView{
					CurrentModel: alt.CurrentModel,
					CurrentScore: alt.CurrentScore,
					CurrentCost:  alt.CurrentCost,
					AltModel:     alt.AltModel,
					AltScore:     alt.AltScore,
					AltCost:      alt.AltCost,
					ScoreDelta:   alt.ScoreDelta,
					CostSavings:  alt.CostSavings,
					QualityDrop:  alt.QualityDrop,
					MonthlySaved: alt.MonthlySaved,
					Verdict:      alt.Verdict,
					VerdictClass: verdictClass,
					CurrentQAC:   alt.CurrentQAC,
					AltQAC:       alt.AltQAC,
					QACSavings:   alt.QACSavings,
				})
			}
		}
		if len(data.ModelAlternatives) > 0 {
			data.HasAlternatives = true
		}

		// QAC leaderboard — all models ranked by quality-adjusted cost.
		leaderboard := s.benchmarkRec.QACLeaderboard()
		if len(leaderboard) > 0 {
			// Build set of current models for highlighting.
			currentModels := make(map[string]bool)
			for _, m := range data.Models {
				currentModels[m.Model] = true
			}

			data.HasQACLeaderboard = true
			for _, entry := range leaderboard {
				data.QACLeaderboard = append(data.QACLeaderboard, qacLeaderView{
					Rank:           entry.Rank,
					Model:          entry.Model,
					BenchmarkScore: entry.BenchmarkScore,
					InputCost:      entry.InputCost,
					QAC:            entry.QAC,
					IsCurrentModel: currentModels[entry.Model],
				})
			}
		}
	}

	// Budget overview.
	budgets, budgetErr := s.sessionSvc.BudgetStatus(r.Context())
	if budgetErr == nil && len(budgets) > 0 {
		data.HasBudgets = true
		for _, bs := range budgets {
			data.Budgets = append(data.Budgets, budgetOverviewView{
				ProjectName:    bs.ProjectName,
				ProjectPath:    bs.ProjectPath,
				CostMode:       bs.CostMode,
				MonthlyLimit:   bs.MonthlyLimit,
				MonthlySpent:   bs.MonthlySpent,
				MonthlyPercent: bs.MonthlyPercent,
				MonthlyAlert:   bs.MonthlyAlert,
				DailyLimit:     bs.DailyLimit,
				DailySpent:     bs.DailySpent,
				DailyPercent:   bs.DailyPercent,
				DailyAlert:     bs.DailyAlert,
				ProjectedMonth: bs.ProjectedMonth,
				DaysRemaining:  bs.DaysRemaining,
			})
		}
	}

	return data
}

// handleCostOverviewPartial renders the Overview tab content.
func (s *Server) handleCostOverviewPartial(w http.ResponseWriter, r *http.Request) {
	data := s.buildCostsData(r)
	s.renderPartial(w, "cost_overview_partial", data)
}

// handleCostToolsPartial renders the Tools & Agents tab content.
func (s *Server) handleCostToolsPartial(w http.ResponseWriter, r *http.Request) {
	data := s.buildCostsData(r)
	s.renderPartial(w, "cost_tools_partial", data)
}

// handleCostOptimizationPartial renders the Optimization tab content.
func (s *Server) handleCostOptimizationPartial(w http.ResponseWriter, r *http.Request) {
	data := s.buildCostsData(r)
	s.renderPartial(w, "cost_optimization_partial", data)
}

// capabilityKindLabel returns a human-friendly label for a capability kind.
func capabilityKindLabel(kind string) string {
	switch registry.CapabilityKind(kind) {
	case registry.KindAgent:
		return "Agents"
	case registry.KindCommand:
		return "Commands"
	case registry.KindSkill:
		return "Skills"
	case registry.KindTool:
		return "Tools"
	case registry.KindPlugin:
		return "Plugins"
	default:
		if len(kind) == 0 {
			return "Unknown"
		}
		return strings.ToUpper(kind[:1]) + kind[1:] + "s"
	}
}

// capabilityKindIcon returns an emoji/symbol for a capability kind.
func capabilityKindIcon(kind string) string {
	switch registry.CapabilityKind(kind) {
	case registry.KindAgent:
		return "A"
	case registry.KindCommand:
		return "C"
	case registry.KindSkill:
		return "S"
	case registry.KindTool:
		return "T"
	case registry.KindPlugin:
		return "P"
	default:
		return "?"
	}
}

// projectBaseName returns just the basename from a project path.
func projectBaseName(path string) string {
	return filepath.Base(path)
}

// ── Analysis ──

// analysisPartialData is the data structure for the analysis HTMX partial.
type analysisPartialData struct {
	SessionID   string
	HasAnalysis bool
	CanAnalyze  bool
	Analysis    *analysisView
}

// handleAnalysisPartial renders the analysis section partial (HTMX GET).
func (s *Server) handleAnalysisPartial(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	data := analysisPartialData{SessionID: id}

	if s.analysisSvc != nil {
		data.CanAnalyze = true
		sa, err := s.analysisSvc.GetLatestAnalysis(id)
		if err == nil && sa != nil {
			data.HasAnalysis = true
			data.Analysis = buildAnalysisView(sa)
		}
	}

	s.renderPartial(w, "analysis_partial", data)
}

// handleRunAnalysis triggers a manual analysis and returns the updated partial (HTMX POST).
func (s *Server) handleRunAnalysis(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	if s.analysisSvc == nil {
		http.Error(w, "Analysis not configured", http.StatusServiceUnavailable)
		return
	}

	// Run analysis synchronously — the UI shows a spinner via htmx-indicator.
	_, err := s.analysisSvc.Analyze(r.Context(), service.AnalysisRequest{
		SessionID: session.ID(id),
		Trigger:   analysis.TriggerManual,
	})
	if err != nil {
		s.logger.Printf("analysis for %s: %v", id, err)
		// Even on error, the analysis entity is persisted (with error field set).
		// Fall through to render whatever is stored.
	}

	// Return the updated partial.
	data := analysisPartialData{
		SessionID:  id,
		CanAnalyze: true,
	}
	sa, aErr := s.analysisSvc.GetLatestAnalysis(id)
	if aErr == nil && sa != nil {
		data.HasAnalysis = true
		data.Analysis = buildAnalysisView(sa)
	}

	s.renderPartial(w, "analysis_partial", data)
}

// buildAnalysisView converts a domain SessionAnalysis to a template-friendly view.
func buildAnalysisView(sa *analysis.SessionAnalysis) *analysisView {
	v := &analysisView{
		ID:         sa.ID,
		Score:      sa.Report.Score,
		ScoreClass: scoreClass(sa.Report.Score),
		Summary:    sa.Report.Summary,
		Trigger:    string(sa.Trigger),
		Adapter:    string(sa.Adapter),
		DurationMs: sa.DurationMs,
		CreatedAt:  timeAgo(sa.CreatedAt),
		Error:      sa.Error,
	}

	if len(sa.Report.Problems) > 0 {
		v.HasProblems = true
		for _, p := range sa.Report.Problems {
			v.Problems = append(v.Problems, problemView{
				Severity:      string(p.Severity),
				SeverityClass: string(p.Severity), // "high", "medium", "low" map directly to CSS classes
				Description:   p.Description,
				ToolName:      p.ToolName,
			})
		}
	}

	if len(sa.Report.Recommendations) > 0 {
		v.HasRecommendations = true
		for _, rec := range sa.Report.Recommendations {
			v.Recommendations = append(v.Recommendations, recommendationView{
				Category:      string(rec.Category),
				CategoryClass: string(rec.Category),
				Title:         rec.Title,
				Description:   rec.Description,
				Priority:      rec.Priority,
			})
		}
	}

	if len(sa.Report.SkillSuggestions) > 0 {
		v.HasSkills = true
		for _, sk := range sa.Report.SkillSuggestions {
			v.SkillSuggestions = append(v.SkillSuggestions, skillView{
				Name:        sk.Name,
				Description: sk.Description,
				Trigger:     sk.Trigger,
			})
		}
	}

	if obs := sa.Report.SkillObservation; obs != nil {
		if len(obs.Available)+len(obs.Recommended)+len(obs.Loaded)+len(obs.Missed)+len(obs.Discovered) > 0 {
			v.HasSkillObservation = true
			v.SkillsAvailable = obs.Available
			v.SkillsRecommended = obs.Recommended
			v.SkillsLoaded = obs.Loaded
			v.SkillsMissed = obs.Missed
			v.SkillsDiscovered = obs.Discovered
			v.SkillMissedCount = len(obs.Missed)
			v.SkillDiscoveredCount = len(obs.Discovered)
		}
	}

	return v
}

// scoreClass returns a CSS class based on the analysis score.
func scoreClass(score int) string {
	switch {
	case score >= 70:
		return "good"
	case score >= 40:
		return "warning"
	default:
		return "poor"
	}
}

// ── Project Detail ──

type projectDetailPage struct {
	Nav             string
	SidebarProjects []sidebarProject

	// Project identity
	ProjectPath string
	DisplayName string
	RemoteURL   string
	Provider    string
	Category    string

	// KPIs
	TotalSessions      int
	TotalTokens        int
	TotalCost          float64
	TotalErrors        int
	SessionsWithErrors int
	TotalToolCalls     int

	// Branches for this project
	TopBranches []branchStat

	// Recent sessions
	RecentSessions []session.Summary

	// Weekly trends
	HasTrends     bool
	TrendVerdict  string
	TrendSessions trendMetric
	TrendTokens   trendMetric
	TrendErrors   trendMetric

	// Forecast
	HasForecast  bool
	Projected30d float64
	Projected90d float64
	TrendPerDay  float64
	TrendDir     string

	// Real cost forecast
	HasRealForecast     bool
	TotalReal30d        float64
	SubscriptionMonthly float64
	APIProjected30d     float64

	// Cache efficiency
	HasCacheStats         bool
	DashCacheHitRate      float64
	DashCacheMissSessions int
	DashCacheTotalMisses  int
	DashCacheWaste        float64

	// Analytics (from event buckets)
	HasAnalytics    bool
	AnalyticsTools  int
	AnalyticsSkills int
	TopTools        []nameCount
	DailyBuckets    []dailyActivity

	// Capabilities
	HasCapabilities bool
	ProjectName     string
	CapabilityStats []capabilityStat
	MCPServerCount  int

	// Agent distribution (for clickable filter chips)
	Agents []agentChip

	// Budget
	HasBudget       bool
	BudgetMonthly   budgetView
	BudgetDaily     budgetView
	BudgetProjected float64
	BudgetDaysLeft  int

	// Context saturation
	HasSaturation    bool
	SatAvgPeak       float64
	SatAbove80       int
	SatCompacted     int
	SatTotalSessions int

	// Agent ROI
	HasAgentROI bool
	AgentROI    []agentROIView

	// Skill ROI
	HasSkillROI bool
	SkillROI    []skillROIView

	// Security
	HasSecurity        bool
	SecurityAlertCount int
	SecurityCritical   int
	SecurityHigh       int
	SecurityAvgRisk    float64
	SecurityTopCats    []securityCatView

	// Recommendations
	HasRecommendations bool
	Recommendations    []insightView

	// Top files (from file_changes / blame data)
	HasTopFiles    bool
	TopFileEntries []topFileView
}

// securityCatView is a template-friendly security category count.
type securityCatView struct {
	Category string
	Count    int
}

// insightView is a template-friendly auto-generated recommendation.
type insightView struct {
	Icon      string
	Title     string
	Message   string
	Impact    string
	Priority  string
	PrioClass string
}

// agentROIView is a template-friendly agent ROI entry.
type agentROIView struct {
	Agent          string
	SessionCount   int
	AvgCost        float64
	AvgMessages    float64
	ErrorRate      float64
	CompletionRate float64
	AvgSaturation  float64
	ROIScore       int
	ROIGrade       string
	GradeClass     string // CSS class for grade badge
}

// skillROIView is a template-friendly skill ROI entry.
type skillROIView struct {
	Name          string
	LoadCount     int
	UsagePercent  float64
	ContextTokens int
	ErrorDelta    float64
	Verdict       string
	VerdictClass  string // CSS class
	IsGhost       bool
}

// topFileView is a template-friendly top file entry for the project detail aside.
type topFileView struct {
	FilePath     string
	SessionCount int
	WriteCount   int
}

// agentChip is a template-friendly agent count for filter chips.
type agentChip struct {
	Name  string
	Count int
}

// budgetView is a template-friendly view of a budget limit.
type budgetView struct {
	Limit   float64
	Spent   float64
	Percent float64
	Alert   string // "", "warning", "exceeded"
}

func (s *Server) handleProjectDetail(w http.ResponseWriter, r *http.Request) {
	projectPath := "/" + r.PathValue("path")
	if projectPath == "/" {
		http.Redirect(w, r, "/projects", http.StatusFound)
		return
	}

	data := s.buildProjectDetailData(r, projectPath)
	s.render(w, "project_detail.html", data)
}

// handleProjectSessionsPartial returns a filtered list of sessions for a project (HTMX).
func (s *Server) handleProjectSessionsPartial(w http.ResponseWriter, r *http.Request) {
	projectPath := "/" + r.PathValue("path")
	q := r.URL.Query()
	keyword := q.Get("keyword")
	agent := q.Get("agent")
	sessionType := q.Get("session_type")

	// Build search request with provider filter mapped from agent.
	req := service.SearchRequest{
		Keyword:     keyword,
		ProjectPath: projectPath,
		Limit:       30,
	}

	// Agent filter: search by agent name in the sessions.
	// The SearchRequest doesn't have Agent directly, so we filter post-query.
	result, err := s.sessionSvc.Search(req)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// Post-filter by agent and session type if specified.
	var filtered []session.Summary
	for _, sess := range result.Sessions {
		if agent != "" && sess.Agent != agent {
			continue
		}
		if sessionType != "" && sess.SessionType != sessionType {
			continue
		}
		filtered = append(filtered, sess)
	}

	type partialData struct {
		Sessions    []session.Summary
		ProjectPath string
		Agent       string
		SessionType string
	}
	s.renderPartial(w, "project_sessions_partial.html", partialData{
		Sessions:    filtered,
		ProjectPath: projectPath,
		Agent:       agent,
		SessionType: sessionType,
	})
}

func (s *Server) buildProjectDetailData(r *http.Request, projectPath string) projectDetailPage {
	ctx := r.Context()
	data := projectDetailPage{
		Nav:             "projects",
		ProjectPath:     projectPath,
		DisplayName:     filepath.Base(projectPath),
		SidebarProjects: s.buildSidebarProjects(ctx, projectPath),
	}

	// Find project metadata from ListProjects.
	groups, _ := s.sessionSvc.ListProjects(ctx)
	for _, g := range groups {
		if g.ProjectPath == projectPath {
			data.DisplayName = g.DisplayName
			data.RemoteURL = g.RemoteURL
			data.Provider = string(g.Provider)
			data.Category = g.Category
			break
		}
	}

	// KPIs (filtered by project).
	statsReq := service.StatsRequest{ProjectPath: projectPath}
	stats, err := s.cachedStats(statsReq)
	if err != nil {
		s.logger.Printf("project detail stats error: %v", err)
		return data
	}

	data.TotalSessions = stats.TotalSessions
	data.TotalTokens = stats.TotalTokens
	data.TotalCost = stats.TotalCost

	// Top branches for this project.
	limit := 10
	if len(stats.PerBranch) < limit {
		limit = len(stats.PerBranch)
	}
	for i := range limit {
		bs := stats.PerBranch[i]
		data.TopBranches = append(data.TopBranches, branchStat{
			Branch:       bs.Branch,
			SessionCount: bs.SessionCount,
			TotalTokens:  bs.TotalTokens,
			TotalCost:    bs.TotalCost,
			ActualCost:   bs.ActualCost,
		})
	}

	// Recent sessions for this project.
	listReq := service.ListRequest{ProjectPath: projectPath}
	summaries, listErr := s.sessionSvc.List(listReq)
	if listErr == nil {
		for _, sm := range summaries {
			data.TotalErrors += sm.ErrorCount
			data.TotalToolCalls += sm.ToolCallCount
			if sm.ErrorCount > 0 {
				data.SessionsWithErrors++
			}
		}

		sort.Slice(summaries, func(i, j int) bool {
			ti := summaries[i].UpdatedAt
			if ti.IsZero() {
				ti = summaries[i].CreatedAt
			}
			tj := summaries[j].UpdatedAt
			if tj.IsZero() {
				tj = summaries[j].CreatedAt
			}
			return ti.After(tj)
		})

		recentLimit := 15
		if len(summaries) < recentLimit {
			recentLimit = len(summaries)
		}
		data.RecentSessions = summaries[:recentLimit]

		// Build agent distribution from all sessions (not just recent).
		agentCounts := make(map[string]int)
		for _, sm := range summaries {
			if sm.Agent != "" {
				agentCounts[sm.Agent]++
			}
		}
		for name, count := range agentCounts {
			data.Agents = append(data.Agents, agentChip{Name: name, Count: count})
		}
		sort.Slice(data.Agents, func(i, j int) bool {
			return data.Agents[i].Count > data.Agents[j].Count
		})
	}

	// Weekly trends (project-scoped).
	trends, trendsErr := s.sessionSvc.Trends(ctx, service.TrendRequest{
		Period:      7 * 24 * time.Hour,
		ProjectPath: projectPath,
	})
	if trendsErr == nil && (trends.Current.SessionCount > 0 || trends.Previous.SessionCount > 0) {
		data.HasTrends = true
		data.TrendVerdict = trends.Delta.Verdict
		data.TrendSessions = buildTrendMetric(
			trends.Current.SessionCount, trends.Previous.SessionCount,
			trends.Delta.SessionCountChange, 0, false,
		)
		data.TrendTokens = buildTrendMetric(
			trends.Current.TotalTokens, trends.Previous.TotalTokens,
			trends.Delta.TokensChange, trends.Delta.TokensChangePercent, false,
		)
		data.TrendErrors = buildTrendMetric(
			trends.Current.TotalErrors, trends.Previous.TotalErrors,
			trends.Delta.ErrorsChange, trends.Delta.ErrorsChangePercent, true,
		)
	}

	// Forecast.
	forecast, fErr := s.cachedForecast(ctx, service.ForecastRequest{
		Period:      "weekly",
		Days:        90,
		ProjectPath: projectPath,
	})
	if fErr == nil && forecast.SessionCount > 0 {
		data.HasForecast = true
		data.Projected30d = forecast.Projected30d
		data.Projected90d = forecast.Projected90d
		data.TrendPerDay = forecast.TrendPerDay
		data.TrendDir = forecast.TrendDir

		// Real cost if available.
		if forecast.TotalReal30d > 0 || forecast.SubscriptionMonthly > 0 {
			data.HasRealForecast = true
			data.TotalReal30d = forecast.TotalReal30d
			data.SubscriptionMonthly = forecast.SubscriptionMonthly
			data.APIProjected30d = forecast.APIProjected30d
		}
	}
	if data.TrendDir == "" {
		data.TrendDir = "stable"
	}

	// Cache efficiency (7-day window, project-scoped).
	since7d := time.Now().AddDate(0, 0, -7)
	cacheEff, cacheErr := s.sessionSvc.CacheEfficiency(ctx, projectPath, since7d)
	if cacheErr == nil && cacheEff != nil && cacheEff.TotalInputTokens > 0 {
		data.HasCacheStats = true
		data.DashCacheHitRate = cacheEff.CacheHitRate
		data.DashCacheMissSessions = cacheEff.SessionsWithMiss
		data.DashCacheTotalMisses = cacheEff.TotalCacheMisses
		data.DashCacheWaste = cacheEff.EstimatedWaste
	}

	// Analytics (event buckets).
	if s.sessionEventSvc != nil {
		now := time.Now().UTC()
		since := now.AddDate(0, 0, -30)
		buckets, bErr := s.sessionEventSvc.QueryBuckets(sessionevent.BucketQuery{
			ProjectPath: projectPath,
			Granularity: "1d",
			Since:       since,
			Until:       now,
		})
		if bErr == nil && len(buckets) > 0 {
			data.HasAnalytics = true
			allTools := make(map[string]int)
			for _, b := range buckets {
				data.AnalyticsTools += b.ToolCallCount
				data.AnalyticsSkills += b.SkillLoadCount
				for k, v := range b.TopTools {
					allTools[k] += v
				}
				data.DailyBuckets = append(data.DailyBuckets, dailyActivity{
					Date:      b.BucketStart.Format("Jan 2"),
					ToolCalls: b.ToolCallCount,
					Errors:    b.ErrorCount,
					Sessions:  b.SessionCount,
				})
			}
			data.TopTools = topN(allTools, data.AnalyticsTools, 8)
		}
	}

	// Capabilities.
	if s.registrySvc != nil {
		proj, scanErr := s.registrySvc.ScanProject(projectPath)
		if scanErr == nil {
			data.HasCapabilities = true
			data.ProjectName = proj.Name
			data.MCPServerCount = len(proj.MCPServers)
			for _, cs := range proj.CapabilityStats() {
				data.CapabilityStats = append(data.CapabilityStats, capabilityStat{
					Kind:  string(cs.Kind),
					Count: cs.Count,
				})
			}
		}
	}

	// Budget status.
	budgets, budgetErr := s.sessionSvc.BudgetStatus(ctx)
	if budgetErr == nil {
		for _, bs := range budgets {
			if bs.ProjectPath == projectPath || bs.RemoteURL == data.RemoteURL {
				data.HasBudget = true
				data.BudgetMonthly = budgetView{
					Limit:   bs.MonthlyLimit,
					Spent:   bs.MonthlySpent,
					Percent: bs.MonthlyPercent,
					Alert:   bs.MonthlyAlert,
				}
				data.BudgetDaily = budgetView{
					Limit:   bs.DailyLimit,
					Spent:   bs.DailySpent,
					Percent: bs.DailyPercent,
					Alert:   bs.DailyAlert,
				}
				data.BudgetProjected = bs.ProjectedMonth
				data.BudgetDaysLeft = bs.DaysRemaining
				break
			}
		}
	}

	// Context saturation.
	since90d := time.Now().AddDate(0, 0, -90)
	satResult, satErr := s.sessionSvc.ContextSaturation(ctx, projectPath, since90d)
	if satErr == nil && satResult != nil && satResult.TotalSessions > 0 {
		data.HasSaturation = true
		data.SatAvgPeak = satResult.AvgPeakSaturation
		data.SatAbove80 = satResult.SessionsAbove80
		data.SatCompacted = satResult.SessionsCompacted
		data.SatTotalSessions = satResult.TotalSessions
	}

	// Agent ROI.
	agentROI, roiErr := s.sessionSvc.AgentROIAnalysis(ctx, projectPath, since90d)
	if roiErr == nil && agentROI != nil && len(agentROI.Agents) > 0 {
		data.HasAgentROI = true
		for _, a := range agentROI.Agents {
			gradeClass := "badge-good"
			switch a.ROIGrade {
			case "C":
				gradeClass = "badge-warning"
			case "D", "F":
				gradeClass = "badge-poor"
			}
			data.AgentROI = append(data.AgentROI, agentROIView{
				Agent:          a.Agent,
				SessionCount:   a.SessionCount,
				AvgCost:        a.AvgCostPerSession,
				AvgMessages:    a.AvgMessages,
				ErrorRate:      a.ErrorRate,
				CompletionRate: a.CompletionRate,
				AvgSaturation:  a.AvgPeakSaturation,
				ROIScore:       a.ROIScore,
				ROIGrade:       a.ROIGrade,
				GradeClass:     gradeClass,
			})
		}
	}

	// Skill ROI.
	skillROI, skillErr := s.sessionSvc.SkillROIAnalysis(ctx, projectPath, since90d)
	if skillErr == nil && skillROI != nil && len(skillROI.Skills) > 0 {
		data.HasSkillROI = true
		for _, sk := range skillROI.Skills {
			verdictClass := "badge-good"
			switch sk.Verdict {
			case "ghost":
				verdictClass = "badge-warning"
			case "harmful":
				verdictClass = "badge-poor"
			case "neutral":
				verdictClass = "badge-provider"
			}
			data.SkillROI = append(data.SkillROI, skillROIView{
				Name:          sk.Name,
				LoadCount:     sk.LoadCount,
				UsagePercent:  sk.UsagePercent,
				ContextTokens: sk.ContextTokens,
				ErrorDelta:    sk.ErrorDelta,
				Verdict:       sk.Verdict,
				VerdictClass:  verdictClass,
				IsGhost:       sk.IsGhost,
			})
		}
	}

	// Security scan.
	if s.securityDetector != nil {
		secSummary, secErr := s.securityDetector.ScanProject(projectPath)
		if secErr == nil && secSummary != nil && secSummary.TotalAlerts > 0 {
			data.HasSecurity = true
			data.SecurityAlertCount = secSummary.TotalAlerts
			data.SecurityCritical = secSummary.CriticalAlerts
			data.SecurityHigh = secSummary.HighAlerts
			data.SecurityAvgRisk = secSummary.AvgRiskScore
			for _, cat := range secSummary.TopCategories {
				data.SecurityTopCats = append(data.SecurityTopCats, securityCatView{
					Category: string(cat.Category),
					Count:    cat.Count,
				})
			}
		}
	}

	// Recommendations.
	recommendations, recErr := s.sessionSvc.GenerateRecommendations(ctx, projectPath)
	if recErr == nil && len(recommendations) > 0 {
		data.HasRecommendations = true
		for _, rec := range recommendations {
			prioClass := "rec-low"
			switch rec.Priority {
			case "high":
				prioClass = "rec-high"
			case "medium":
				prioClass = "rec-medium"
			}
			data.Recommendations = append(data.Recommendations, insightView{
				Icon:      rec.Icon,
				Title:     rec.Title,
				Message:   rec.Message,
				Impact:    rec.Impact,
				Priority:  rec.Priority,
				PrioClass: prioClass,
			})
		}
	}

	// Top files (from file_changes table).
	if s.store != nil {
		topFiles, tfErr := s.store.TopFilesForProject(projectPath, 10)
		if tfErr == nil && len(topFiles) > 0 {
			data.HasTopFiles = true
			for _, tf := range topFiles {
				data.TopFileEntries = append(data.TopFileEntries, topFileView{
					FilePath:     tf.FilePath,
					SessionCount: tf.SessionCount,
					WriteCount:   tf.WriteCount,
				})
			}
		}
	}

	return data
}

// ── Context Saturation Partial ──

type saturationCurveView struct {
	Model         string
	MaxTokens     int
	InitOverhead  int
	InitOverheadK string // formatted "15K"
	PeakTokens    int
	PeakPercent   float64
	WasCompacted  bool
	MsgAtDegraded int
	MsgAtCritical int
	Points        []saturationPointView
}

type saturationPointView struct {
	Index     int
	Role      string
	Tokens    int
	Percent   float64
	Zone      string // "optimal", "degraded", "critical"
	Delta     int
	Label     string
	BarWidth  int    // 0-100 for CSS width
	ZoneClass string // CSS class
}

func (s *Server) handleSaturationPartial(w http.ResponseWriter, r *http.Request) {
	sessID := session.ID(r.PathValue("id"))
	curve, err := s.sessionSvc.SessionSaturationCurve(r.Context(), sessID)
	if err != nil {
		w.Write([]byte(`<div class="text-muted" style="padding:1rem;">Context saturation data unavailable.</div>`))
		return
	}

	view := saturationCurveView{
		Model:         curve.Model,
		MaxTokens:     curve.MaxInputTokens,
		InitOverhead:  curve.InitOverhead,
		PeakTokens:    curve.PeakTokens,
		PeakPercent:   curve.PeakPercent,
		WasCompacted:  curve.WasCompacted,
		MsgAtDegraded: curve.MsgAtDegraded,
		MsgAtCritical: curve.MsgAtCritical,
	}
	if curve.InitOverhead >= 1000 {
		view.InitOverheadK = fmt.Sprintf("%dK", curve.InitOverhead/1000)
	} else {
		view.InitOverheadK = fmt.Sprintf("%d", curve.InitOverhead)
	}

	for _, pt := range curve.Points {
		barWidth := int(pt.Percent)
		if barWidth > 100 {
			barWidth = 100
		}
		zoneClass := "sat-optimal"
		if pt.Zone == "degraded" {
			zoneClass = "sat-degraded"
		} else if pt.Zone == "critical" {
			zoneClass = "sat-critical"
		}

		view.Points = append(view.Points, saturationPointView{
			Index:     pt.MessageIndex + 1,
			Role:      pt.Role,
			Tokens:    pt.InputTokens,
			Percent:   pt.Percent,
			Zone:      pt.Zone,
			Delta:     pt.Delta,
			Label:     pt.Label,
			BarWidth:  barWidth,
			ZoneClass: zoneClass,
		})
	}

	s.renderPartial(w, "saturation_partial.html", view)
}

// ── Global Search (Ctrl+K) ──

// searchResultView is a template-friendly search result with optional highlights.
type searchResultView struct {
	ID           string
	Summary      string
	Provider     string
	ProjectPath  string
	Branch       string
	Agent        string
	SessionType  string
	MessageCount int
	TotalTokens  int
	ErrorCount   int
	CreatedAt    time.Time
	UpdatedAt    time.Time

	// Highlights (HTML with <mark> tags, empty if engine doesn't support).
	HighlightSummary template.HTML // highlighted summary (safe HTML)
	HighlightContent template.HTML // highlighted content snippet (safe HTML)
	HasHighlights    bool
	Score            float64 // relevance score (0 = no ranking)
}

type searchResultsData struct {
	Query      string
	Results    []searchResultView
	HasMore    bool
	Engine     string // search engine name ("fts5", "like", "")
	HasEngine  bool   // true if a named engine was used
	TotalCount int    // total matching results (not just displayed)
}

func (s *Server) handleSearchResults(w http.ResponseWriter, r *http.Request) {
	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	if keyword == "" {
		s.renderPartial(w, "search_results", searchResultsData{})
		return
	}

	const searchLimit = 8

	searchReq := service.SearchRequest{
		Keyword: keyword,
		Limit:   searchLimit + 1,
	}

	result, err := s.sessionSvc.Search(searchReq)
	if err != nil {
		s.logger.Printf("search error: %v", err)
		s.renderPartial(w, "search_results", searchResultsData{Query: keyword})
		return
	}

	hasMore := len(result.Sessions) > searchLimit
	if hasMore {
		result.Sessions = result.Sessions[:searchLimit]
	}

	views := make([]searchResultView, len(result.Sessions))
	for i, sum := range result.Sessions {
		v := searchResultView{
			ID:           string(sum.ID),
			Summary:      sum.Summary,
			Provider:     string(sum.Provider),
			ProjectPath:  sum.ProjectPath,
			Branch:       sum.Branch,
			Agent:        sum.Agent,
			SessionType:  sum.SessionType,
			MessageCount: sum.MessageCount,
			TotalTokens:  sum.TotalTokens,
			ErrorCount:   sum.ErrorCount,
			CreatedAt:    sum.CreatedAt,
			UpdatedAt:    sum.UpdatedAt,
		}
		if hl, ok := result.Highlights[sum.ID]; ok {
			v.HasHighlights = true
			v.Score = hl.Score
			if hl.Summary != "" {
				v.HighlightSummary = template.HTML(hl.Summary)
			}
			if hl.Content != "" {
				v.HighlightContent = template.HTML(hl.Content)
			}
		}
		views[i] = v
	}

	data := searchResultsData{
		Query:      keyword,
		Results:    views,
		HasMore:    hasMore,
		Engine:     result.Engine,
		HasEngine:  result.Engine != "",
		TotalCount: result.TotalCount,
	}

	s.renderPartial(w, "search_results", data)
}
