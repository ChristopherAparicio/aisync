package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"sync"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/benchmark"
	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/diagnostic"
	"github.com/ChristopherAparicio/aisync/internal/gittree"
	"github.com/ChristopherAparicio/aisync/internal/registry"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
)

// Cache TTLs for dashboard statistics.
// NOTE: forecastCacheTTL, saturationCacheTTL, cacheEfficiencyCTTL, agentROICacheTTL
// were removed — those handlers now read from pre-computed session_analytics rows.
const (
	statsCacheTTL    = 2 * time.Minute
	costPageCacheTTL = 60 * time.Second
	trendsCacheTTL   = 5 * time.Minute
	sidebarCacheTTL  = 2 * time.Minute
	skillROICacheTTL = 2 * time.Hour
	securityCacheTTL = 2 * time.Hour
	fileCacheTTL     = 60 * time.Second
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

// NOTE: cachedForecast, cachedSaturation, cachedCacheEfficiency, cachedAgentROI
// were removed as part of Phase 4 CQRS migration. These four hot-path handlers
// now read from pre-computed session_analytics rows (50-100ms) and no longer
// need a JSON cache layer. Callers invoke s.sessionSvc.Forecast(),
// s.sessionSvc.ContextSaturation(), s.sessionSvc.CacheEfficiency(),
// s.sessionSvc.AgentROIAnalysis() directly.

// cachedTrends returns Trends from cache if fresh, otherwise computes and caches.
// Trends compare current vs previous period and are moderately expensive (2 full scans).
func (s *Server) cachedTrends(ctx context.Context, req service.TrendRequest) (*service.TrendResult, error) {
	cacheKey := "trends:" + req.ProjectPath

	// 1. Try cache.
	if s.store != nil {
		if data, _ := s.store.GetCache(cacheKey, trendsCacheTTL); data != nil {
			var result service.TrendResult
			if err := json.Unmarshal(data, &result); err == nil {
				return &result, nil
			}
		}
	}

	// 2. Compute.
	result, err := s.sessionSvc.Trends(ctx, req)
	if err != nil {
		return nil, err
	}

	// 3. Cache.
	if s.store != nil {
		if data, marshalErr := json.Marshal(result); marshalErr == nil {
			_ = s.store.SetCache(cacheKey, data)
		}
	}

	return result, nil
}

// cachedSidebarGroups returns the project groups for the sidebar from cache if fresh.
// The raw groups are cached centrally; the caller applies the active path locally.
func (s *Server) cachedSidebarGroups(ctx context.Context) []session.ProjectGroup {
	const cacheKey = "sidebar_groups"

	// 1. Try cache.
	if s.store != nil {
		if data, _ := s.store.GetCache(cacheKey, sidebarCacheTTL); data != nil {
			var groups []session.ProjectGroup
			if err := json.Unmarshal(data, &groups); err == nil {
				return groups
			}
		}
	}

	// 2. Compute.
	groups, err := s.sessionSvc.ListProjects(ctx)
	if err != nil || len(groups) == 0 {
		return nil
	}

	// 3. Cache.
	if s.store != nil {
		if data, marshalErr := json.Marshal(groups); marshalErr == nil {
			_ = s.store.SetCache(cacheKey, data)
		}
	}

	return groups
}

// cachedCostsPage returns the full cost dashboard data from cache if fresh,
// otherwise computes via buildCostsData and caches for subsequent tab switches.
// This prevents each HTMX tab partial from recomputing the entire cost page.
func (s *Server) cachedCostsPage(r *http.Request) costDashboardPage {
	project := r.URL.Query().Get("project")
	cacheKey := "costs_page:" + project

	// 1. Try cache.
	if s.store != nil {
		if data, _ := s.store.GetCache(cacheKey, costPageCacheTTL); data != nil {
			var result costDashboardPage
			if err := json.Unmarshal(data, &result); err == nil {
				return result
			}
		}
	}

	// 2. Compute.
	data := s.buildCostsData(r)

	// 3. Cache for tab switches.
	if s.store != nil {
		if payload, marshalErr := json.Marshal(data); marshalErr == nil {
			_ = s.store.SetCache(cacheKey, payload)
		}
	}

	return data
}

// cachedFilesForProject returns FilesForProject from cache if fresh, otherwise queries and caches.
// The file explorer page calls this instead of store.FilesForProject directly.
func (s *Server) cachedFilesForProject(projectPath, dirPrefix string, limit int) ([]session.ProjectFileEntry, error) {
	cacheKey := "files:" + projectPath + ":" + dirPrefix
	if s.store != nil {
		if data, _ := s.store.GetCache(cacheKey, fileCacheTTL); data != nil {
			var result []session.ProjectFileEntry
			if err := json.Unmarshal(data, &result); err == nil {
				return result, nil
			}
		}
	}

	entries, err := s.store.FilesForProject(projectPath, dirPrefix, limit)
	if err != nil {
		return nil, err
	}

	if s.store != nil {
		if payload, marshalErr := json.Marshal(entries); marshalErr == nil {
			_ = s.store.SetCache(cacheKey, payload)
		}
	}
	return entries, nil
}

// cachedSkillROI returns SkillROI from cache if fresh, otherwise computes and caches.
// SkillROI queries session events for every session in the project — expensive for large projects.
func (s *Server) cachedSkillROI(ctx context.Context, projectPath string, since time.Time) (*session.SkillROI, error) {
	cacheKey := "skill_roi:" + projectPath

	if s.store != nil {
		if data, _ := s.store.GetCache(cacheKey, skillROICacheTTL); data != nil {
			var result session.SkillROI
			if err := json.Unmarshal(data, &result); err == nil {
				return &result, nil
			}
		}
	}

	result, err := s.sessionSvc.SkillROIAnalysis(ctx, projectPath, since)
	if err != nil {
		return nil, err
	}

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
// Uses cachedSidebarGroups() so the expensive ListProjects() call is not repeated
// on every page load (11+ call sites).
func (s *Server) buildSidebarProjects(ctx context.Context, activePath string) []sidebarProject {
	groups := s.cachedSidebarGroups(ctx)
	if len(groups) == 0 {
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

// sparklineBar is a single bar in a mini sparkline chart.
// HeightPct is 0-100 representing the bar height relative to the max value in the set.
type sparklineBar struct {
	Value     int
	HeightPct int    // 0-100
	Label     string // tooltip label (e.g. "Mar 25")
}

// buildSparklineBars converts a slice of int values into sparkline bars with percentage heights.
func buildSparklineBars(values []int, labels []string) []sparklineBar {
	if len(values) == 0 {
		return nil
	}
	maxVal := 0
	for _, v := range values {
		if v > maxVal {
			maxVal = v
		}
	}
	bars := make([]sparklineBar, len(values))
	for i, v := range values {
		pct := 0
		if maxVal > 0 {
			pct = v * 100 / maxVal
			if pct == 0 && v > 0 {
				pct = 2 // minimum visible bar for non-zero values
			}
		}
		lbl := ""
		if i < len(labels) {
			lbl = labels[i]
		}
		bars[i] = sparklineBar{Value: v, HeightPct: pct, Label: lbl}
	}
	return bars
}

// buildSparklineBarsFloat converts float values into sparkline bars.
func buildSparklineBarsFloat(values []float64, labels []string) []sparklineBar {
	if len(values) == 0 {
		return nil
	}
	maxVal := 0.0
	for _, v := range values {
		if v > maxVal {
			maxVal = v
		}
	}
	bars := make([]sparklineBar, len(values))
	for i, v := range values {
		pct := 0
		if maxVal > 0 {
			pct = int(v * 100 / maxVal)
			if pct == 0 && v > 0 {
				pct = 2
			}
		}
		lbl := ""
		if i < len(labels) {
			lbl = labels[i]
		}
		bars[i] = sparklineBar{Value: int(v * 100), HeightPct: pct, Label: lbl}
	}
	return bars
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

	// Activity sparklines (14-day mini bar charts under KPI strip)
	HasSparklines     bool
	SparklineSessions []sparklineBar
	SparklineTokens   []sparklineBar
	SparklineCost     []sparklineBar
	SparklineErrors   []sparklineBar
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
	ctx := r.Context()
	project := r.URL.Query().Get("project")
	data := dashboardPage{
		Nav:             "dashboard",
		Projects:        s.buildProjectList(project),
		SelectedProject: project,
		SidebarProjects: s.buildSidebarProjects(ctx, project),
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

	// Error / activity KPIs + recent sessions come straight from the same
	// stats call — no second List() needed (Fix #10). The Stats() loop
	// already iterates all matching summaries and populates these fields,
	// so reading them here costs zero extra queries.
	data.TotalErrors = stats.TotalErrors
	data.TotalToolCalls = stats.TotalToolCalls
	data.SessionsWithErrors = stats.SessionsWithErrors
	data.RecentSessions = stats.RecentSessions

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

	// ── Concurrent data fetches ──
	var mu sync.Mutex
	var wg sync.WaitGroup

	// 1. Forecast.
	wg.Add(1)
	go func() {
		defer wg.Done()
		forecast, fErr := s.sessionSvc.Forecast(ctx, service.ForecastRequest{
			Period: "weekly",
			Days:   90,
		})
		if fErr != nil || forecast.SessionCount == 0 {
			return
		}

		mu.Lock()
		data.HasForecast = true
		data.Projected30d = forecast.Projected30d
		data.Projected90d = forecast.Projected90d
		data.TrendPerDay = forecast.TrendPerDay
		data.TrendDir = forecast.TrendDir
		if forecast.TotalReal30d > 0 || forecast.SubscriptionMonthly > 0 {
			data.HasRealForecast = true
			data.TotalReal30d = forecast.TotalReal30d
			data.SubscriptionMonthly = forecast.SubscriptionMonthly
			data.APIProjected30d = forecast.APIProjected30d
		}
		mu.Unlock()
	}()

	// 2. Cache efficiency (7-day window).
	wg.Add(1)
	go func() {
		defer wg.Done()
		since7d := time.Now().AddDate(0, 0, -7)
		cacheEff, cacheErr := s.sessionSvc.CacheEfficiency(ctx, project, since7d)
		if cacheErr != nil || cacheEff == nil || cacheEff.TotalInputTokens == 0 {
			return
		}

		mu.Lock()
		data.HasCacheStats = true
		data.DashCacheHitRate = cacheEff.CacheHitRate
		data.DashCacheMissSessions = cacheEff.SessionsWithMiss
		data.DashCacheTotalMisses = cacheEff.TotalCacheMisses
		data.DashCacheWaste = cacheEff.EstimatedWaste
		mu.Unlock()
	}()

	// 3. Weekly trends.
	wg.Add(1)
	go func() {
		defer wg.Done()
		trends, trendsErr := s.cachedTrends(ctx, service.TrendRequest{
			Period: 7 * 24 * time.Hour,
		})
		if trendsErr != nil {
			return
		}
		if trends.Current.SessionCount == 0 && trends.Previous.SessionCount == 0 {
			return
		}

		mu.Lock()
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
		mu.Unlock()
	}()

	// 4. Capabilities (filesystem only, fast).
	wg.Add(1)
	go func() {
		defer wg.Done()
		if project == "" || s.registrySvc == nil {
			return
		}
		proj, scanErr := s.registrySvc.ScanProject(project)
		if scanErr != nil {
			return
		}

		var capStats []capabilityStat
		for _, cs := range proj.CapabilityStats() {
			capStats = append(capStats, capabilityStat{
				Kind:  string(cs.Kind),
				Count: cs.Count,
			})
		}

		mu.Lock()
		data.HasCapabilities = true
		data.ProjectName = proj.Name
		data.MCPServerCount = len(proj.MCPServers)
		data.CapabilityStats = capStats
		mu.Unlock()
	}()

	// 5. Activity sparklines (14-day mini bar charts).
	wg.Add(1)
	go func() {
		defer wg.Done()
		if s.store == nil {
			return
		}
		sparkSince := time.Now().AddDate(0, 0, -14)
		sparkUntil := time.Now()
		buckets, sparkErr := s.store.QueryTokenBuckets("1d", sparkSince, sparkUntil, project)
		if sparkErr != nil || len(buckets) == 0 {
			return
		}

		type dayAgg struct {
			sessions int
			tokens   int
			cost     float64
			errors   int
			label    string
		}
		byDay := make(map[string]*dayAgg)
		dayOrder := make([]string, 0, 14)
		for _, b := range buckets {
			key := b.BucketStart.Format("2006-01-02")
			agg, ok := byDay[key]
			if !ok {
				agg = &dayAgg{label: b.BucketStart.Format("Jan 2")}
				byDay[key] = agg
				dayOrder = append(dayOrder, key)
			}
			agg.sessions += b.SessionCount
			agg.tokens += b.InputTokens + b.OutputTokens
			agg.cost += b.EstimatedCost
			if b.ActualCost > 0 {
				agg.cost = b.ActualCost + agg.cost - b.EstimatedCost
			}
			agg.errors += b.ToolErrorCount
		}
		sort.Strings(dayOrder)

		sessions := make([]int, len(dayOrder))
		tokens := make([]int, len(dayOrder))
		costs := make([]float64, len(dayOrder))
		errors := make([]int, len(dayOrder))
		labels := make([]string, len(dayOrder))
		for i, key := range dayOrder {
			a := byDay[key]
			sessions[i] = a.sessions
			tokens[i] = a.tokens
			costs[i] = a.cost
			errors[i] = a.errors
			labels[i] = a.label
		}

		mu.Lock()
		data.HasSparklines = true
		data.SparklineSessions = buildSparklineBars(sessions, labels)
		data.SparklineTokens = buildSparklineBars(tokens, labels)
		data.SparklineCost = buildSparklineBarsFloat(costs, labels)
		data.SparklineErrors = buildSparklineBars(errors, labels)
		mu.Unlock()
	}()

	wg.Wait()

	if data.TrendDir == "" {
		data.TrendDir = "stable"
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

	// Facets for sidebar navigation.
	Facets *session.SearchFacets
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

	// Build facets for sidebar navigation.
	// Facets use the same filters EXCEPT keyword (so counts show all sessions in the filter context).
	var facets *session.SearchFacets
	if s.store != nil {
		var sinceT, untilT time.Time
		if since != "" {
			sinceT, _ = time.Parse("2006-01-02", since)
		}
		if until != "" {
			untilT, _ = time.Parse("2006-01-02", until)
			// Include the full day.
			if !untilT.IsZero() {
				untilT = untilT.Add(24*time.Hour - time.Second)
			}
		}
		facetQuery := session.SearchQuery{
			ProjectPath:     project,
			Branch:          branch,
			Provider:        providerName,
			OwnerID:         session.ID(owner),
			SessionType:     sessionType,
			ProjectCategory: projectCategory,
			Since:           sinceT,
			Until:           untilT,
		}
		if status != "" {
			facetQuery.Status = session.SessionStatus(status)
		}
		facets, _ = s.store.SearchFacets(facetQuery)
	}

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
		Facets:                facets,
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
	Nav               string
	Session           *session.Session
	BackURL           string // contextual back link (default /sessions)
	BackLabel         string // label for the back link (e.g. "Sessions", "File Tree", "PRs")
	OwnerName         string // resolved from users table (empty if not found)
	ForkParentSummary string // summary of the parent session (for fork context)
	TotalCost         float64
	CostBreakdown     []session.ModelCost
	ToolCallCount     int
	ErrorCount        int
	ErrorRate         float64 // 0-100 percentage
	RestoreCmd        string

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

	// Skill map: message index → list of skill names used in that message.
	// Built by scanning tool calls (name == "skill") and content tags (<skill_content>).
	SkillMap     map[int][]string // key = 0-based message index
	SkillCount   int              // total skill loads across all messages
	UniqueSkills []string         // sorted unique skill names (for filter chips)

	// Pull requests linked to this session
	HasPRs bool
	PRs    []sessionPRBadge

	// Compaction detection: context window summarization events.
	HasCompactions        bool
	CompactionCount       int
	CompactionTokensLost  string // formatted "45K"
	CompactionRebuildCost string // formatted "$0.68"
	CompactionAvgDrop     string // "92%"
	CompactionEvents      []compactionEventView
	// Section 8.1: enhanced compaction metrics.
	CompactionRate            string // compactions per user message, e.g. "0.15"
	CompactionLastQuartile    string // last-quartile rate, e.g. "0.30"
	CompactionCascadeCount    int    // number of multi-pass cascade events
	CompactionDetectionStatus string // "full", "partial", "none" — data coverage indicator

	// Cache miss timeline: breaks exceeding the 5-minute cache TTL.
	HasCacheMisses        bool
	CacheMissCount        int
	CacheMissWastedTokens string // formatted "20K"
	CacheMissLongestGap   int    // minutes
	CacheMissEvents       []cacheMissEventView
	CacheMissShowList     bool                  // true when ≤10 events (show list), false = show distribution
	CacheMissBuckets      []cacheMissBucketView // gap distribution buckets for chart

	// Section 8.2: Top commands by output size.
	HasCommandOutput     bool
	TopCommandsByOutput  []commandOutputEntry
	TotalCommandOutput   string // formatted total output bytes across all commands
	TotalCommandOutBytes int    // raw total for sorting

	// Section 8.3: Investigation hot-spots.
	HasHotspots         bool
	Hotspots            *hotspotView
	HotspotsPrecomputed bool // true if loaded from cache, false if computed on demand

	// Message pagination: only first PageSize messages are rendered initially.
	// The rest are loaded via HTMX "Load More".
	TotalMessages int  // total count of messages in the session
	HasMoreMsgs   bool // true when messages are truncated
}

// sessionPRBadge is a template-friendly view of a PR linked to a session.
type sessionPRBadge struct {
	Number     int
	Title      string
	State      string
	StateClass string // "open", "merged", "closed"
	URL        string
	Branch     string
	RepoOwner  string
	RepoName   string
}

// cacheMissEventView is a template-friendly view of a single cache miss event.
type cacheMissEventView struct {
	MessageIdx   int    // 1-based message index for display
	GapMinutes   int    // gap duration in minutes
	WastedTokens string // formatted "20K"
	TimeStr      string // time of the miss "14:30"
}

// cacheMissBucketView is a gap-duration distribution bucket for the chart view.
type cacheMissBucketView struct {
	Label   string // e.g. "5–15 min", "15–30 min", "30–60 min", "1–2 h", "2 h+"
	Count   int    // number of misses in this bucket
	Percent int    // percentage of total misses (for bar width)
}

// compactionEventView is a template-friendly view of a single compaction event.
type compactionEventView struct {
	BeforeMsg   int    // message index before compaction
	AfterMsg    int    // message index after compaction
	TokensLost  string // formatted "45K"
	DropPercent string // "92%"
	CacheReset  bool   // true if cache was invalidated
	IsCascade   bool   // true if this is a merged multi-pass cascade event
	MergedLegs  int    // number of legs merged (0 if not a cascade)
}

// hotspotView is a template-friendly view of pre-computed investigation hot-spots (Section 8.3).
type hotspotView struct {
	// Command hot-spots.
	TopByOutput []commandHotspotView
	TopByReuse  []commandHotspotView
	TotalCmds   int
	UniqueCmds  int
	ErrorCount  int
	TotalOutput string // formatted bytes

	// Skill footprints.
	HasSkills bool
	Skills    []skillFootprintView

	// Expensive messages.
	ExpensiveMessages []expensiveMessageView

	// Compaction summary (already shown elsewhere, included for completeness).
	CompactionCount int
	TotalTokensLost string
}

// commandHotspotView is a template-friendly view of a single command hot-spot.
type commandHotspotView struct {
	BaseCommand string
	Invocations int
	TotalOutput string // formatted bytes
	AvgOutput   string // formatted bytes
	ErrorCount  int
	TokenImpact string // formatted tokens
}

// skillFootprintView is a template-friendly view of a skill's context cost.
type skillFootprintView struct {
	Name            string
	LoadCount       int
	TotalBytes      string // formatted
	EstimatedTokens string // formatted
}

// expensiveMessageView is a template-friendly view of an expensive message.
type expensiveMessageView struct {
	Index        int    // 1-based for display
	Role         string // "user", "assistant"
	InputTokens  string // formatted
	OutputTokens string // formatted
	TotalTokens  string // formatted
	Model        string
}

// commandOutputEntry is a template-friendly view of a command's output footprint (Section 8.2).
type commandOutputEntry struct {
	BaseCommand string // e.g. "go", "npm", "git"
	Invocations int    // how many times this command was run
	TotalOutput string // formatted total output bytes (e.g. "1.2M")
	TotalBytes  int    // raw bytes for sorting
	AvgOutput   string // formatted average output per invocation
	TokenImpact string // formatted estimated tokens (bytes/4)
}

// fileChangeView is a template-friendly view of a file operation.
type fileChangeView struct {
	FilePath   string
	ChangeType string // created, modified, read, deleted
	ToolName   string // e.g. "Write", "Edit", "Bash"
}

// buildHotspotView converts a SessionHotspots into a template-friendly hotspotView.
func buildHotspotView(h *session.SessionHotspots) *hotspotView {
	v := &hotspotView{
		TotalCmds:       h.TotalCommands,
		UniqueCmds:      h.UniqueCommands,
		ErrorCount:      h.CommandErrorCount,
		TotalOutput:     formatBytes(h.TotalOutputBytes),
		CompactionCount: h.CompactionCount,
		TotalTokensLost: formatTokens(h.TotalTokensLost),
	}

	for _, c := range h.TopCommandsByOutput {
		v.TopByOutput = append(v.TopByOutput, commandHotspotView{
			BaseCommand: c.BaseCommand,
			Invocations: c.Invocations,
			TotalOutput: formatBytes(c.TotalOutput),
			AvgOutput:   formatBytes(c.AvgOutput),
			ErrorCount:  c.ErrorCount,
			TokenImpact: formatTokens(c.TokenImpact),
		})
	}

	for _, c := range h.TopCommandsByReuse {
		v.TopByReuse = append(v.TopByReuse, commandHotspotView{
			BaseCommand: c.BaseCommand,
			Invocations: c.Invocations,
			TotalOutput: formatBytes(c.TotalOutput),
			AvgOutput:   formatBytes(c.AvgOutput),
			ErrorCount:  c.ErrorCount,
			TokenImpact: formatTokens(c.TokenImpact),
		})
	}

	if len(h.SkillFootprints) > 0 {
		v.HasSkills = true
		for _, sf := range h.SkillFootprints {
			v.Skills = append(v.Skills, skillFootprintView{
				Name:            sf.Name,
				LoadCount:       sf.LoadCount,
				TotalBytes:      formatBytes(sf.TotalBytes),
				EstimatedTokens: formatTokens(sf.EstimatedTokens),
			})
		}
	}

	for _, em := range h.ExpensiveMessages {
		v.ExpensiveMessages = append(v.ExpensiveMessages, expensiveMessageView{
			Index:        em.Index + 1, // 1-based for display
			Role:         string(em.Role),
			InputTokens:  formatTokens(em.InputTokens),
			OutputTokens: formatTokens(em.OutputTokens),
			TotalTokens:  formatTokens(em.TotalTokens),
			Model:        em.Model,
		})
	}

	return v
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

	// Module results
	HasModuleResults bool
	ModuleResults    []moduleResultView
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

	// Contextual back link: use Referer to determine where the user came from.
	data.BackURL = "/sessions"
	data.BackLabel = "Sessions"
	if ref := r.Header.Get("Referer"); ref != "" {
		if u, err := url.Parse(ref); err == nil {
			p := u.Path
			switch {
			case strings.HasPrefix(p, "/tree/") || p == "/tree":
				data.BackURL = ref
				data.BackLabel = "File Tree"
			case strings.HasPrefix(p, "/pulls"):
				data.BackURL = ref
				data.BackLabel = "PRs"
			case strings.HasPrefix(p, "/branches/"):
				data.BackURL = ref
				data.BackLabel = "Branch"
			case strings.HasPrefix(p, "/projects/"):
				data.BackURL = ref
				data.BackLabel = "Project"
			case strings.HasPrefix(p, "/files/"):
				data.BackURL = ref
				data.BackLabel = "Git"
			case strings.HasPrefix(p, "/blame"):
				data.BackURL = ref
				data.BackLabel = "File Blame"
			}
		}
	}

	// Resolve owner name from users table.
	if sess.OwnerID != "" && s.store != nil {
		if user, err := s.store.GetUser(sess.OwnerID); err == nil && user != nil && user.Name != "" {
			data.OwnerName = user.Name
		}
	}

	// Resolve fork parent summary for context.
	if sess.ParentID != "" {
		if parent, err := s.sessionSvc.Get(string(sess.ParentID)); err == nil && parent != nil {
			data.ForkParentSummary = parent.Summary
		}
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

	// Build skill map: detect skill loads per message.
	data.SkillMap = detectSkillsPerMessage(sess.Messages)
	uniqueSet := make(map[string]bool)
	for _, names := range data.SkillMap {
		data.SkillCount += len(names)
		for _, n := range names {
			uniqueSet[n] = true
		}
	}
	if len(uniqueSet) > 0 {
		data.UniqueSkills = make([]string, 0, len(uniqueSet))
		for n := range uniqueSet {
			data.UniqueSkills = append(data.UniqueSkills, n)
		}
		sort.Strings(data.UniqueSkills)
	}

	// Compute cost breakdown via the session service (respects user pricing overrides).
	estimate, _ := s.sessionSvc.EstimateCost(r.Context(), string(sess.ID))
	if estimate != nil {
		data.TotalCost = estimate.TotalCost.TotalCost
		data.CostBreakdown = estimate.PerModel
	}

	data.RestoreCmd = buildRestoreCmd(string(sess.ID), "", false)

	// Compaction detection: find context window compaction events.
	// Derive inputRate from the session's total input cost / total input tokens.
	var inputRate float64
	if estimate != nil && sess.TokenUsage.InputTokens > 0 {
		inputRate = estimate.TotalCost.InputCost / float64(sess.TokenUsage.InputTokens)
	}
	compSummary := session.DetectCompactions(sess.Messages, inputRate)
	data.CompactionDetectionStatus = compSummary.DetectionCoverage
	if compSummary.TotalCompactions > 0 {
		data.HasCompactions = true
		data.CompactionCount = compSummary.TotalCompactions
		data.CompactionTokensLost = formatTokens(compSummary.TotalTokensLost)
		data.CompactionRebuildCost = fmt.Sprintf("$%.2f", compSummary.TotalRebuildCost)
		data.CompactionAvgDrop = fmt.Sprintf("%.0f%%", compSummary.AvgDropPercent)
		data.CompactionCascadeCount = compSummary.CascadeCount
		if compSummary.CompactionsPerUserMessage > 0 {
			data.CompactionRate = fmt.Sprintf("%.2f", compSummary.CompactionsPerUserMessage)
		}
		if compSummary.LastQuartileCompactionRate > 0 {
			data.CompactionLastQuartile = fmt.Sprintf("%.2f", compSummary.LastQuartileCompactionRate)
		}
		for _, e := range compSummary.Events {
			data.CompactionEvents = append(data.CompactionEvents, compactionEventView{
				BeforeMsg:   e.BeforeMessageIdx + 1, // 1-based for display
				AfterMsg:    e.AfterMessageIdx + 1,
				TokensLost:  formatTokens(e.TokensLost),
				DropPercent: fmt.Sprintf("%.0f%%", e.DropPercent),
				CacheReset:  e.CacheInvalidated,
				IsCascade:   e.IsCascade,
				MergedLegs:  e.MergedLegs,
			})
		}
	}

	// Cache miss timeline: detect breaks exceeding the 5-minute cache TTL.
	cacheMissTimeline := session.DetectCacheMisses(sess.Messages)
	if cacheMissTimeline.TotalMisses > 0 {
		data.HasCacheMisses = true
		data.CacheMissCount = cacheMissTimeline.TotalMisses
		data.CacheMissWastedTokens = formatTokens(cacheMissTimeline.TotalWastedTokens)
		data.CacheMissLongestGap = cacheMissTimeline.LongestGapMins
		for _, e := range cacheMissTimeline.Events {
			data.CacheMissEvents = append(data.CacheMissEvents, cacheMissEventView{
				MessageIdx:   e.MessageIndex + 1, // 1-based
				GapMinutes:   e.GapMinutes,
				WastedTokens: formatTokens(e.InputTokens),
				TimeStr:      e.Timestamp.Format("15:04"),
			})
		}

		// Display mode: list for ≤10, distribution chart for >10.
		const cacheMissListThreshold = 10
		data.CacheMissShowList = cacheMissTimeline.TotalMisses <= cacheMissListThreshold

		if !data.CacheMissShowList {
			// Build gap-duration distribution buckets.
			type bucket struct {
				label   string
				minMins int
				maxMins int
				count   int
			}
			buckets := []bucket{
				{"5–15 min", 5, 15, 0},
				{"15–30 min", 15, 30, 0},
				{"30–60 min", 30, 60, 0},
				{"1–2 h", 60, 120, 0},
				{"2–6 h", 120, 360, 0},
				{"6 h+", 360, 1<<31 - 1, 0},
			}
			for _, e := range cacheMissTimeline.Events {
				for i := range buckets {
					if e.GapMinutes >= buckets[i].minMins && e.GapMinutes < buckets[i].maxMins {
						buckets[i].count++
						break
					}
				}
			}
			total := cacheMissTimeline.TotalMisses
			for _, b := range buckets {
				if b.count > 0 {
					pct := b.count * 100 / total
					if pct == 0 {
						pct = 1 // ensure at least 1% for visibility
					}
					data.CacheMissBuckets = append(data.CacheMissBuckets, cacheMissBucketView{
						Label:   b.label,
						Count:   b.count,
						Percent: pct,
					})
				}
			}
		}
	}

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

	// Section 8.2: Top commands by output size.
	// Scan tool calls for bash/shell commands and aggregate output bytes per base command.
	{
		type cmdAgg struct {
			count      int
			totalBytes int
		}
		cmdMap := make(map[string]*cmdAgg)
		var totalBytes int
		for _, msg := range sess.Messages {
			for _, tc := range msg.ToolCalls {
				baseCmd := session.ExtractBaseCommand(tc.Name, tc.Input)
				if baseCmd == "" {
					continue
				}
				outBytes := len(tc.Output)
				totalBytes += outBytes
				if agg, ok := cmdMap[baseCmd]; ok {
					agg.count++
					agg.totalBytes += outBytes
				} else {
					cmdMap[baseCmd] = &cmdAgg{count: 1, totalBytes: outBytes}
				}
			}
		}
		if len(cmdMap) > 0 && totalBytes > 0 {
			data.HasCommandOutput = true
			data.TotalCommandOutput = formatBytes(totalBytes)
			data.TotalCommandOutBytes = totalBytes
			// Build sorted list (top 10 by output bytes).
			type kv struct {
				key string
				val *cmdAgg
			}
			var pairs []kv
			for k, v := range cmdMap {
				pairs = append(pairs, kv{k, v})
			}
			sort.Slice(pairs, func(i, j int) bool {
				return pairs[i].val.totalBytes > pairs[j].val.totalBytes
			})
			limit := 10
			if len(pairs) < limit {
				limit = len(pairs)
			}
			for _, p := range pairs[:limit] {
				avg := p.val.totalBytes / p.val.count
				data.TopCommandsByOutput = append(data.TopCommandsByOutput, commandOutputEntry{
					BaseCommand: p.key,
					Invocations: p.val.count,
					TotalOutput: formatBytes(p.val.totalBytes),
					TotalBytes:  p.val.totalBytes,
					AvgOutput:   formatBytes(avg),
					TokenImpact: formatTokens(p.val.totalBytes / 4),
				})
			}
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

	// Pull requests linked to this session
	if s.store != nil {
		linkedPRs, prErr := s.store.GetPRsForSession(sess.ID)
		if prErr == nil && len(linkedPRs) > 0 {
			data.HasPRs = true
			for _, pr := range linkedPRs {
				stateClass := "open"
				switch pr.State {
				case "merged":
					stateClass = "merged"
				case "closed":
					stateClass = "closed"
				}
				data.PRs = append(data.PRs, sessionPRBadge{
					Number:     pr.Number,
					Title:      pr.Title,
					State:      pr.State,
					StateClass: stateClass,
					URL:        pr.URL,
					Branch:     pr.Branch,
					RepoOwner:  pr.RepoOwner,
					RepoName:   pr.RepoName,
				})
			}
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

	// Section 8.3: Investigation hot-spots.
	// Try pre-computed cache first, fall back to on-demand computation.
	if s.store != nil {
		cached, hsErr := s.store.GetHotspots(sess.ID)
		if hsErr == nil && cached != nil {
			data.HasHotspots = true
			data.Hotspots = buildHotspotView(cached)
			data.HotspotsPrecomputed = true
		} else {
			// On-demand computation for sessions not yet processed by the nightly task.
			h := session.ComputeHotspots(sess, 0)
			data.HasHotspots = true
			data.Hotspots = buildHotspotView(&h)
			data.HotspotsPrecomputed = false
		}
	}

	// Message pagination: for sessions with many messages, only render the first
	// batch to keep the page small. The rest can be loaded via HTMX "Load More".
	const msgPageSize = 50
	data.TotalMessages = len(sess.Messages)
	if len(sess.Messages) > msgPageSize {
		data.HasMoreMsgs = true
		sess.Messages = sess.Messages[:msgPageSize]
		// Truncate activity segments too (they mirror messages 1:1).
		if len(data.ActivitySegments) > msgPageSize {
			data.ActivitySegments = data.ActivitySegments[:msgPageSize]
		}
	}

	s.render(w, "session_detail.html", data)
}

// handleSessionMessagesMore returns the next batch of messages for a session (HTMX partial).
func (s *Server) handleSessionMessagesMore(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	offset := 50 // default start offset
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
	}

	sess, err := s.sessionSvc.Get(id)
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	const batchSize = 50
	total := len(sess.Messages)
	if offset >= total {
		// Nothing more to render.
		w.WriteHeader(http.StatusOK)
		return
	}

	end := offset + batchSize
	if end > total {
		end = total
	}
	batch := sess.Messages[offset:end]
	hasMore := end < total

	// Build skill map for this batch.
	allSkills := detectSkillsPerMessage(sess.Messages)

	type msgBatchData struct {
		Messages []session.Message
		Offset   int
		HasMore  bool
		NextURL  string
		SkillMap map[int][]string
	}

	nextURL := ""
	if hasMore {
		nextURL = "/partials/session-messages/" + id + "?offset=" + strconv.Itoa(end)
	}

	data := msgBatchData{
		Messages: batch,
		Offset:   offset,
		HasMore:  hasMore,
		NextURL:  nextURL,
		SkillMap: allSkills,
	}

	s.renderPartial(w, "session-messages-batch", data)
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

// branchData holds a branch's stats (lightweight — no inline sessions).
type branchData struct {
	Name         string
	Slug         string // URL-safe anchor
	SessionCount int
	TotalTokens  int
	TotalCost    float64
	LastActivity string // human-readable time ago
}

type branchExplorerPage struct {
	Nav             string
	SidebarProjects []sidebarProject
	Branches        []branchData
	Projects        []projectItem
}

// handleBranches renders the branch explorer page.
func (s *Server) handleBranches(w http.ResponseWriter, r *http.Request) {
	data := s.buildBranchesData(r)
	s.render(w, "branch_explorer.html", data)
}

func (s *Server) buildBranchesData(r *http.Request) branchExplorerPage {
	project := r.URL.Query().Get("project")
	data := branchExplorerPage{Nav: "branches", SidebarProjects: s.buildSidebarProjects(r.Context(), project), Projects: s.buildProjectList(project)}

	// Get per-branch stats (cached). No ListTree calls — just aggregate stats.
	statsReq := service.StatsRequest{All: project == "", ProjectPath: project}
	stats, err := s.cachedStats(statsReq)
	if err != nil {
		s.logger.Printf("branches stats error: %v", err)
		return data
	}

	if len(stats.PerBranch) == 0 {
		return data
	}

	// Sort branches by session count descending.
	sort.Slice(stats.PerBranch, func(i, j int) bool {
		return stats.PerBranch[i].SessionCount > stats.PerBranch[j].SessionCount
	})

	data.Branches = make([]branchData, 0, len(stats.PerBranch))
	for _, bs := range stats.PerBranch {
		data.Branches = append(data.Branches, branchData{
			Name:         bs.Branch,
			Slug:         slugify(bs.Branch),
			SessionCount: bs.SessionCount,
			TotalTokens:  bs.TotalTokens,
			TotalCost:    bs.TotalCost,
			LastActivity: timeAgoString(bs.LastActivity),
		})
	}

	return data
}

// ── Branch Timeline ──

type branchTimelinePage struct {
	Nav             string
	SidebarProjects []sidebarProject
	BranchName      string
	Entries         []timelineEntryView
	HasEntries      bool

	// Session tree (parent-child hierarchy + forks).
	Tree          []branchTreeNodeView
	HasTree       bool
	TreeStats     branchTreeStats
	ForkRelations []branchForkView
}

// branchTreeNodeView is a view model for a session tree node.
type branchTreeNodeView struct {
	ID          string
	IDShort     string
	Summary     string
	Agent       string
	Provider    string
	SessionType string
	Status      string
	StatusClass string
	Messages    int
	Tokens      string
	Errors      int
	TimeAgo     string
	IsFork      bool
	Depth       int // nesting depth (0 = root)
	IndentPx    int // pre-computed left indent in pixels (Depth * 24)
	HasChildren bool
	Children    []branchTreeNodeView
}

// branchTreeStats holds aggregate stats for the session tree.
type branchTreeStats struct {
	TotalSessions int
	RootSessions  int
	ForkCount     int
	MaxDepth      int
}

// branchForkView holds a fork relationship for display.
type branchForkView struct {
	OriginalID    string
	OriginalShort string
	ForkID        string
	ForkShort     string
	ForkPoint     int
	SharedMsgs    int
	Reason        string
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
		Nav:             "branches",
		SidebarProjects: s.buildSidebarProjects(r.Context(), ""),
		BranchName:      branchName,
	}

	// ── Timeline entries (flat, with commits) ──
	entries, err := s.sessionSvc.BranchTimeline(r.Context(), service.TimelineRequest{
		Branch: branchName,
		Limit:  50,
	})
	if err != nil {
		s.logger.Printf("branch timeline error: %v", err)
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

	// ── Session tree (parent-child hierarchy) ──
	tree, treeErr := s.sessionSvc.ListTree(r.Context(), service.ListRequest{
		Branch: branchName,
		All:    true,
	})
	if treeErr != nil {
		s.logger.Printf("branch tree error: %v", treeErr)
	}

	if len(tree) > 0 {
		data.HasTree = true
		var maxDepth int
		var forkCount int
		for _, node := range tree {
			data.Tree = append(data.Tree, buildBranchTreeNodeView(node, 0, &maxDepth, &forkCount))
		}
		data.TreeStats = branchTreeStats{
			TotalSessions: countTreeNodes(tree),
			RootSessions:  len(tree),
			ForkCount:     forkCount,
			MaxDepth:      maxDepth,
		}

		// ── Fork relations (batch-loaded in single query) ──
		if s.store != nil {
			// Collect all session IDs in the tree.
			var sessionIDs []session.ID
			collectTreeIDs(tree, &sessionIDs)

			// Batch-load fork relations for all sessions (1 query instead of N).
			forkRelMap, _ := s.store.GetForkRelationsForSessions(sessionIDs)
			seen := make(map[string]bool)
			for _, rels := range forkRelMap {
				for _, rel := range rels {
					key := string(rel.OriginalID) + "→" + string(rel.ForkID)
					if seen[key] {
						continue
					}
					seen[key] = true
					data.ForkRelations = append(data.ForkRelations, branchForkView{
						OriginalID:    string(rel.OriginalID),
						OriginalShort: truncateID(string(rel.OriginalID), 10),
						ForkID:        string(rel.ForkID),
						ForkShort:     truncateID(string(rel.ForkID), 10),
						ForkPoint:     rel.ForkPoint,
						SharedMsgs:    rel.SharedMessages,
						Reason:        rel.Reason,
					})
				}
			}
		}
	}

	s.render(w, "branch_timeline.html", data)
}

// buildBranchTreeNodeView converts a SessionTreeNode to a view model.
func buildBranchTreeNodeView(node session.SessionTreeNode, depth int, maxDepth *int, forkCount *int) branchTreeNodeView {
	if depth > *maxDepth {
		*maxDepth = depth
	}
	if node.IsFork {
		*forkCount++
	}

	statusClass := ""
	switch node.Summary.Status {
	case "active":
		statusClass = "bt-status--active"
	case "completed":
		statusClass = "bt-status--completed"
	case "review":
		statusClass = "bt-status--review"
	case "idle":
		statusClass = "bt-status--idle"
	}

	v := branchTreeNodeView{
		ID:          string(node.Summary.ID),
		IDShort:     truncateID(string(node.Summary.ID), 12),
		Summary:     node.Summary.Summary,
		Agent:       node.Summary.Agent,
		Provider:    string(node.Summary.Provider),
		SessionType: node.Summary.SessionType,
		Status:      string(node.Summary.Status),
		StatusClass: statusClass,
		Messages:    node.Summary.MessageCount,
		Tokens:      formatTokens(node.Summary.TotalTokens),
		Errors:      node.Summary.ErrorCount,
		TimeAgo:     timeAgoString(node.Summary.CreatedAt),
		IsFork:      node.IsFork,
		Depth:       depth,
		IndentPx:    depth * 24,
		HasChildren: len(node.Children) > 0,
	}

	if len(v.Summary) > 60 {
		v.Summary = v.Summary[:57] + "..."
	}

	for _, child := range node.Children {
		v.Children = append(v.Children, buildBranchTreeNodeView(child, depth+1, maxDepth, forkCount))
	}

	return v
}

// countTreeNodes counts total nodes in a tree.
func countTreeNodes(nodes []session.SessionTreeNode) int {
	count := len(nodes)
	for _, n := range nodes {
		count += countTreeNodes(n.Children)
	}
	return count
}

// detectSkillsPerMessage scans messages for skill loads and returns a map
// of message index → skill names. Detects two patterns:
//  1. Tool calls named "skill" (skill name extracted from input JSON "name" field)
//  2. Content tags: <skill_content name="xxx"> in assistant messages
func detectSkillsPerMessage(msgs []session.Message) map[int][]string {
	result := make(map[int][]string)

	for i := range msgs {
		msg := &msgs[i]
		var skills []string

		// Check tool calls for skill loads.
		for _, tc := range msg.ToolCalls {
			if tc.Name == "skill" {
				name := extractSkillNameFromInput(tc.Input)
				if name != "" {
					skills = append(skills, name)
				}
			}
		}

		// Check assistant content for <skill_content name="xxx"> tags.
		if msg.Role == session.RoleAssistant && strings.Contains(msg.Content, "<skill_content") {
			names := extractSkillContentNames(msg.Content)
			skills = append(skills, names...)
		}

		if len(skills) > 0 {
			result[i] = skills
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// extractSkillNameFromInput extracts the skill name from a tool call input.
// Input is typically JSON like {"name": "replay-tester"}.
func extractSkillNameFromInput(input string) string {
	// Quick JSON parse for the "name" field.
	var obj struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(input), &obj); err == nil && obj.Name != "" {
		return obj.Name
	}
	return ""
}

// extractSkillContentNames extracts skill names from <skill_content name="xxx"> tags.
func extractSkillContentNames(text string) []string {
	const prefix = `<skill_content name="`
	var names []string
	for {
		idx := strings.Index(text, prefix)
		if idx == -1 {
			break
		}
		text = text[idx+len(prefix):]
		end := strings.Index(text, `"`)
		if end == -1 {
			break
		}
		name := text[:end]
		if name != "" {
			names = append(names, name)
		}
		text = text[end+1:]
	}
	return names
}

// collectTreeIDs collects all session IDs from a tree.
func collectTreeIDs(nodes []session.SessionTreeNode, ids *[]session.ID) {
	for _, n := range nodes {
		*ids = append(*ids, n.Summary.ID)
		collectTreeIDs(n.Children, ids)
	}
}

// truncateID truncates an ID to maxLen chars.
func truncateID(id string, maxLen int) string {
	if len(id) <= maxLen {
		return id
	}
	return id[:maxLen]
}

// timeAgoString converts a timestamp to a human-readable "X ago" string.
func timeAgoString(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	if d < 0 {
		// Clock skew: treat timestamps up to 1 hour in the future as "just now".
		if d > -time.Hour {
			return "just now"
		}
		return t.Format("2 Jan")
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		months := int(d.Hours()/24/30.44 + 0.5) // round instead of truncate
		if months < 1 {
			months = 1
		}
		if months <= 12 {
			return fmt.Sprintf("%dmo ago", months)
		}
		return t.Format("Jan 2006")
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

// treemapNodeView is the view-layer representation of a treemap node.
type treemapNodeView struct {
	Name         string
	Cost         string  // formatted "$X.XX"
	CostRaw      float64 // raw cost for width calculations
	Tokens       string  // formatted "123K"
	SessionCount int
	Share        float64 // percentage of parent (0-100)
	WidthPct     string  // pre-computed CSS width percentage
	ColorIdx     int     // index into --pie-color-N palette
	Children     []treemapNodeView
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
	Period              string
	Buckets             []costBucket
	BudgetDailyLine     float64 // daily budget limit for overlay (sum of all project daily limits, or single project)
	BudgetLineHeightPct float64 // budget line as % of chart height (capped at 100)
	HasBudgetLine       bool    // true when BudgetDailyLine > 0

	// Model breakdown
	Models []session.ModelForecast

	// Cost treemap: backend → model hierarchy
	HasTreemap   bool
	TreemapNodes []treemapNodeView

	// Per-branch cost
	BranchCosts []branchCostEntry

	// Per-tool cost (populated when project is selected)
	HasToolCosts   bool
	ToolCosts      []toolCostView
	TopMCPServers  []mcpServerView
	TotalMCPCalls  int
	TotalMCPCost   float64
	MCPPieGradient template.CSS // pre-computed conic-gradient stops for pie chart (safe CSS)
	HasMCPPie      bool         // true when there are ≥2 MCP servers with cost

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

	// Cross-project MCP governance matrix (5.2)
	HasGovMatrix bool
	GovMatrix    *mcpGovMatrixView

	// Skill reuse map (1.5)
	HasSkillReuse    bool
	SkillShared      []skillReuseView
	SkillMono        []skillReuseView
	SkillIdle        []skillReuseView
	SkillTotalLoads  int
	SkillSharedCount int
	SkillMonoCount   int
	SkillIdleCount   int

	// Model alternatives (benchmark-based recommendations)
	HasAlternatives   bool
	ModelAlternatives []modelAlternativeView

	// QAC leaderboard (all models ranked by quality-adjusted cost)
	HasQACLeaderboard bool
	QACLeaderboard    []qacLeaderView

	// Multi-benchmark sources (6.3)
	HasMultiBenchmark bool                  // true when MultiCatalog is used
	BenchmarkSources  []benchmarkSourceView // available benchmark sources

	// Configured vs Used (MCP server governance)
	HasConfigVsUsed bool
	ConfigVsUsed    []mcpStatusView
	CVUActiveCount  int
	CVUGhostCount   int
	CVUOrphanCount  int

	// Context saturation
	HasSaturation        bool
	SatAvgPeak           float64 // average peak saturation (0-100)
	SatAbove80           int     // sessions >80% saturation
	SatAbove90           int     // sessions >90% saturation
	SatCompacted         int     // sessions that hit compaction
	SatTotalSessions     int
	SatModels            []modelSaturationView
	SatWorstSessions     []sessionSaturationView
	SatCompactionEvents  int     // total compaction events across all sessions
	SatTotalTokensLost   int     // total tokens lost to compaction
	SatTotalTokensLostK  string  // formatted "1.2M" or "170K"
	SatTotalRebuildCost  float64 // estimated cost to rebuild context ($)
	SatAvgDropPercent    float64 // average compaction drop severity
	HasCompactionDetails bool    // true if compaction events > 0

	// Token waste classification (4.2)
	HasWasteData       bool
	WasteTotalTokens   int
	WasteProductive    wasteView
	WasteRetry         wasteView
	WasteCompaction    wasteView
	WasteCacheMiss     wasteView
	WasteIdleContext   wasteView
	WasteProductivePct float64
	WasteWastePct      float64
	WastePieGradient   template.CSS // conic-gradient for waste pie chart
	HasWastePie        bool

	// Session freshness & diminishing returns (2.3)
	HasFreshnessData       bool
	FreshTotalSessions     int
	FreshCompactedSessions int
	FreshCompactedPct      float64
	FreshAvgCompactions    float64 // avg compactions per compacted session
	FreshAvgErrorGrowth    float64 // % increase in error rate after compaction
	FreshAvgRetryGrowth    float64 // % increase in retry rate after compaction
	FreshAvgOutputDecay    float64 // % decrease in output ratio after compaction
	FreshOptimalMsgIdx     int     // avg optimal message index
	FreshRecommendation    string  // aggregate recommendation
	FreshDepthStats        []freshnessDepthView
	HasFreshDepthStats     bool

	// Overload aggregate (3.2)
	HasOverloadData bool
	OverloadedCount int
	DecliningCount  int
	AvgHealthScore  int
	HealthGrade     string // "good", "warning", "poor"

	// Saturation forecast
	HasForecastSat              bool
	ForecastAvgMsgsToCompaction int
	ForecastAvgGrowthPerMsg     int
	ForecastAvgGrowthK          string // formatted "3.5K"
	ForecastSessionsAnalyzed    int
	ForecastSessionsCompacted   int
	ForecastCompactedPct        float64 // compacted / analyzed * 100
	ForecastModels              []modelForecastView
	ForecastHistogram           []histogramBucketView
	HasHistogram                bool

	// System prompt impact (5.3)
	HasPromptImpact       bool
	PromptAvgEstimate     string // formatted "4.2K"
	PromptMedianEstimate  string // formatted "3.8K"
	PromptMinEstimate     string // formatted "1.2K"
	PromptMaxEstimate     string // formatted "12K"
	PromptTotalSessions   int
	PromptAvgCostPct      float64 // avg % of input tokens consumed by system prompt
	PromptTotalTokens     string  // formatted "1.2M"
	PromptSmallCount      int
	PromptMediumCount     int
	PromptLargeCount      int
	PromptSmallErrorRate  float64
	PromptMediumErrorRate float64
	PromptLargeErrorRate  float64
	PromptSmallRetryRate  float64
	PromptMediumRetryRate float64
	PromptLargeRetryRate  float64
	PromptTrend           string // "growing", "stable", "shrinking"
	PromptTrendIcon       string // "↗", "→", "↘"
	PromptRecommendation  string
	HasPromptBuckets      bool // true when at least 2 size buckets have data

	// Model fitness profiles (6.4)
	HasFitness           bool
	FitnessTotalSessions int
	FitnessTaskTypes     []fitnessTaskTypeView
	FitnessRecs          []string
	HasFitnessRecs       bool

	// Budget overview (all projects with budgets)
	HasBudgets bool
	Budgets    []budgetOverviewView
}

// fitnessTaskTypeView is a template-friendly view of a task type profile.
type fitnessTaskTypeView struct {
	TaskType     string
	SessionCount int
	BestModel    string
	BestScore    int
	BestGrade    string
	GradeClass   string // "good", "warning", "poor"
	Models       []fitnessModelView
	HasMultiple  bool // true when >1 model
}

// fitnessModelView is a template-friendly view of a model fitness profile.
type fitnessModelView struct {
	Model        string
	SessionCount int
	AvgCost      string // formatted "$0.12"
	AvgMessages  string // formatted "23.5"
	ErrorRate    float64
	RetryRate    float64
	FitnessScore int
	FitnessGrade string
	GradeClass   string // "good", "warning", "poor"
	IsBest       bool   // true if this is the top model for the task type
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

// freshnessDepthView is a template-friendly view of quality at a compaction depth.
type freshnessDepthView struct {
	Depth        int    // 0 = pre-compaction, 1 = after 1st, 2 = after 2nd, 3 = after 3+
	DepthLabel   string // "Pre-compaction", "After 1st", etc.
	SessionCount int
	AvgErrorRate float64
	AvgRetryRate float64
	AvgOutputRat float64
	ErrorClass   string // "good", "warning", "poor"
}

// wasteView is a template-friendly view of a token waste category.
type wasteView struct {
	Tokens  int
	Percent float64
	Label   string // formatted token count (e.g. "1.2M", "170K")
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
	CostPercent    float64 // percentage of total MCP cost (0-100)
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
	CurrentScore float64 // 0-100 benchmark % (composite when multi-benchmark)
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

	// Multi-benchmark breakdown (6.3).
	CurrentScores []benchmarkScoreView // per-source scores for current model
	AltScores     []benchmarkScoreView // per-source scores for alternative
	HasBreakdown  bool                 // true when multi-source data available
}

// benchmarkScoreView is a template-friendly per-source benchmark score.
type benchmarkScoreView struct {
	Source      string  // e.g. "Aider", "SWE-bench", "ToolBench", "Arena"
	SourceClass string  // CSS class for badge styling
	Score       float64 // 0-100
	Category    string  // e.g. "code_editing", "problem_solving", "tool_use", "preference"
}

// benchmarkSourceView is a template-friendly benchmark source descriptor.
type benchmarkSourceView struct {
	Name      string  // human-readable name
	Key       string  // source key (e.g. "aider_polyglot")
	Category  string  // e.g. "code_editing"
	Weight    float64 // 0-1 weight in composite
	WeightPct int     // weight as integer percent
}

// benchmarkSourceDisplayName returns a human-readable name for a benchmark source.
func benchmarkSourceDisplayName(source benchmark.BenchmarkSource) string {
	switch source {
	case benchmark.SourceAiderPolyglot:
		return "Aider"
	case benchmark.SourceSWEBench:
		return "SWE-bench"
	case benchmark.SourceToolBench:
		return "ToolBench"
	case benchmark.SourceArenaELO:
		return "Arena ELO"
	default:
		return string(source)
	}
}

// benchmarkSourceClass returns a CSS class for styling a benchmark source badge.
func benchmarkSourceClass(source benchmark.BenchmarkSource) string {
	switch source {
	case benchmark.SourceAiderPolyglot:
		return "bench-aider"
	case benchmark.SourceSWEBench:
		return "bench-swe"
	case benchmark.SourceToolBench:
		return "bench-tool"
	case benchmark.SourceArenaELO:
		return "bench-arena"
	default:
		return "bench-other"
	}
}

// toBenchmarkScoreViews converts domain benchmark scores to template-friendly views.
func toBenchmarkScoreViews(scores []benchmark.BenchmarkScore) []benchmarkScoreView {
	views := make([]benchmarkScoreView, 0, len(scores))
	for _, s := range scores {
		views = append(views, benchmarkScoreView{
			Source:      benchmarkSourceDisplayName(s.Source),
			SourceClass: benchmarkSourceClass(s.Source),
			Score:       s.Score,
			Category:    s.Category,
		})
	}
	return views
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

	// Efficiency score (3.1)
	CostPer1KOutput string // formatted "$0.024"
	OutputPerDollar string // formatted "40.7K"
	ErrorRate       float64
	AvgOutputRatio  float64
	EfficiencyScore int
	EfficiencyGrade string
	GradeClass      string // CSS class for grade badge
	TotalCost       float64
	HasCost         bool // true when TotalCost > 0
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

// modelForecastView is a template-friendly per-model saturation forecast.
type modelForecastView struct {
	Model                 string
	MaxInputTokens        int
	SessionCount          int
	CompactedCount        int
	AvgMsgsToCompacted    int
	MedianMsgsToCompacted int
	AvgTokenGrowthPerMsg  int
	AvgTokenGrowthK       string // formatted "3.5K"
	AvgPeakUtilization    float64
	PredictedMsgsTo80     int
	PredictedMsgsTo100    int
	Recommendation        string
	HasCompactions        bool
	HasPrediction         bool
}

// histogramBucketView is a template-friendly histogram bucket.
type histogramBucketView struct {
	Label     string
	Count     int
	HeightPct float64 // 0-100 for bar height
}

// qacLeaderView is a template-friendly QAC leaderboard entry.
type qacLeaderView struct {
	Rank           int
	Model          string
	BenchmarkScore float64 // composite score when multi-benchmark
	InputCost      float64 // $ per 1M input tokens
	QAC            float64
	IsCurrentModel bool // true if the user currently uses this model

	// Multi-benchmark breakdown (6.3).
	Scores       []benchmarkScoreView // per-source scores
	SourceCount  int                  // how many sources have data
	HasBreakdown bool                 // true when multi-source data available
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

// mcpGovMatrixView is the template-friendly cross-project MCP governance matrix.
type mcpGovMatrixView struct {
	Servers      []string
	Projects     []mcpGovMatrixRowView
	TotalActive  int
	TotalGhost   int
	TotalOrphan  int
	TotalServers int
}

type mcpGovMatrixRowView struct {
	ProjectPath string
	DisplayName string
	Cells       map[string]mcpGovCellView
	ActiveCount int
	GhostCount  int
	OrphanCount int
	TotalCost   float64
}

type mcpGovCellView struct {
	Status      string // "active", "ghost", "orphan", "" (absent)
	StatusClass string // CSS badge class
	CallCount   int
	TotalCost   float64
	HasData     bool // true if server is present for this project
}

// skillReuseView is a template-friendly skill reuse entry.
type skillReuseView struct {
	Name         string
	Scope        string
	ProjectCount int
	Projects     string // comma-separated
	TotalLoads   int
	TotalTokens  int
	TotalTokensK string // formatted "36K"
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
	forecast, fErr := s.sessionSvc.Forecast(r.Context(), service.ForecastRequest{
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

		// Cost treemap: backend → model hierarchy.
		if len(forecast.Treemap) > 0 {
			data.HasTreemap = true
			data.TreemapNodes = buildTreemapViews(forecast.Treemap)
		}

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
		// Compute cost percentages and conic-gradient for pie chart.
		if data.TotalMCPCost > 0 && len(data.TopMCPServers) >= 1 {
			cumPct := 0.0
			var gradientStops []string
			for i := range data.TopMCPServers {
				pctVal := (data.TopMCPServers[i].EstimatedCost / data.TotalMCPCost) * 100
				data.TopMCPServers[i].CostPercent = pctVal
				stop := fmt.Sprintf("var(--pie-color-%d) %.2f%% %.2f%%", i, cumPct, cumPct+pctVal)
				gradientStops = append(gradientStops, stop)
				cumPct += pctVal
			}
			data.MCPPieGradient = template.CSS(strings.Join(gradientStops, ", "))
			data.HasMCPPie = true
		} else if data.TotalMCPCost > 0 {
			for i := range data.TopMCPServers {
				data.TopMCPServers[i].CostPercent = (data.TopMCPServers[i].EstimatedCost / data.TotalMCPCost) * 100
			}
		}
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

		// Cross-project MCP governance matrix (5.2).
		if s.registrySvc != nil {
			govMatrix, govErr := s.registrySvc.CrossProjectMCPGovernance(since90d)
			if govErr == nil && govMatrix != nil && len(govMatrix.Projects) > 0 {
				data.HasGovMatrix = true
				gv := &mcpGovMatrixView{
					Servers:      govMatrix.Servers,
					TotalActive:  govMatrix.TotalActive,
					TotalGhost:   govMatrix.TotalGhost,
					TotalOrphan:  govMatrix.TotalOrphan,
					TotalServers: govMatrix.TotalServers,
				}
				for _, row := range govMatrix.Projects {
					rv := mcpGovMatrixRowView{
						ProjectPath: row.ProjectPath,
						DisplayName: row.DisplayName,
						Cells:       make(map[string]mcpGovCellView),
						ActiveCount: row.ActiveCount,
						GhostCount:  row.GhostCount,
						OrphanCount: row.OrphanCount,
						TotalCost:   row.TotalCost,
					}
					for srv, cell := range row.Cells {
						statusClass := "good"
						switch cell.Status {
						case registry.MCPStatusGhost:
							statusClass = "warning"
						case registry.MCPStatusOrphan:
							statusClass = "poor"
						}
						rv.Cells[srv] = mcpGovCellView{
							Status:      string(cell.Status),
							StatusClass: statusClass,
							CallCount:   cell.CallCount,
							TotalCost:   cell.TotalCost,
							HasData:     true,
						}
					}
					gv.Projects = append(gv.Projects, rv)
				}
				data.GovMatrix = gv
			}
		}

		// Skill reuse map (1.5).
		if s.registrySvc != nil {
			skillReuse, srErr := s.registrySvc.SkillReuseAnalysis(since90d)
			if srErr == nil && skillReuse != nil && skillReuse.TotalSkills > 0 {
				data.HasSkillReuse = true
				data.SkillTotalLoads = skillReuse.TotalLoads
				data.SkillSharedCount = skillReuse.SharedCount
				data.SkillMonoCount = skillReuse.MonoCount
				data.SkillIdleCount = skillReuse.IdleCount

				toView := func(entries []registry.SkillReuseEntry) []skillReuseView {
					var views []skillReuseView
					for _, e := range entries {
						v := skillReuseView{
							Name:         e.Name,
							Scope:        string(e.Scope),
							ProjectCount: e.ProjectCount,
							TotalLoads:   e.TotalLoads,
							TotalTokens:  e.TotalTokens,
						}
						if len(e.Projects) > 0 {
							v.Projects = joinStrings(e.Projects)
						}
						if e.TotalTokens >= 1000000 {
							v.TotalTokensK = fmt.Sprintf("%.1fM", float64(e.TotalTokens)/1000000)
						} else if e.TotalTokens >= 1000 {
							v.TotalTokensK = fmt.Sprintf("%.1fK", float64(e.TotalTokens)/1000)
						} else {
							v.TotalTokensK = strconv.Itoa(e.TotalTokens)
						}
						views = append(views, v)
					}
					return views
				}
				data.SkillShared = toView(skillReuse.SharedSkills)
				data.SkillMono = toView(skillReuse.MonoSkills)
				data.SkillIdle = toView(skillReuse.IdleSkills)
			}
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
		data.SatCompactionEvents = saturation.TotalCompactionEvents
		data.SatTotalTokensLost = saturation.TotalTokensLost
		data.SatTotalRebuildCost = saturation.TotalRebuildCost
		data.SatAvgDropPercent = saturation.AvgDropPercent
		data.HasCompactionDetails = saturation.TotalCompactionEvents > 0

		// Session freshness & diminishing returns (2.3).
		if f := saturation.Freshness; f != nil && f.TotalSessions > 0 {
			data.HasFreshnessData = true
			data.FreshTotalSessions = f.TotalSessions
			data.FreshCompactedSessions = f.SessionsWithCompaction
			if f.TotalSessions > 0 {
				data.FreshCompactedPct = float64(f.SessionsWithCompaction) / float64(f.TotalSessions) * 100
			}
			data.FreshAvgCompactions = f.AvgCompactionsPerSession
			data.FreshAvgErrorGrowth = f.AvgErrorRateGrowth
			data.FreshAvgRetryGrowth = f.AvgRetryRateGrowth
			data.FreshAvgOutputDecay = f.AvgOutputRatioDecay
			data.FreshOptimalMsgIdx = f.AvgOptimalMessageIdx
			data.FreshRecommendation = f.Recommendation
			if len(f.ByCompactionCount) > 0 {
				data.HasFreshDepthStats = true
				depthLabels := map[int]string{0: "Pre-compaction", 1: "After 1st", 2: "After 2nd", 3: "After 3+"}
				for _, ds := range f.ByCompactionCount {
					label := depthLabels[ds.Depth]
					if label == "" {
						label = fmt.Sprintf("Depth %d", ds.Depth)
					}
					errClass := "good"
					if ds.AvgErrorRate > 15 {
						errClass = "poor"
					} else if ds.AvgErrorRate > 8 {
						errClass = "warning"
					}
					data.FreshDepthStats = append(data.FreshDepthStats, freshnessDepthView{
						Depth:        ds.Depth,
						DepthLabel:   label,
						SessionCount: ds.SessionCount,
						AvgErrorRate: ds.AvgErrorRate,
						AvgRetryRate: ds.AvgRetryRate,
						AvgOutputRat: ds.AvgOutputRat,
						ErrorClass:   errClass,
					})
				}
			}
		}

		// Overload aggregate (3.2).
		if saturation.TotalSessions > 0 {
			data.HasOverloadData = true
			data.OverloadedCount = saturation.SessionsOverloaded
			data.DecliningCount = saturation.SessionsDeclining
			data.AvgHealthScore = int(saturation.AvgHealthScore)
			switch {
			case data.AvgHealthScore >= 70:
				data.HealthGrade = "good"
			case data.AvgHealthScore >= 40:
				data.HealthGrade = "warning"
			default:
				data.HealthGrade = "poor"
			}
		}

		// Token waste classification (4.2).
		if w := saturation.TokenWaste; w != nil && w.TotalTokens > 0 {
			data.HasWasteData = true
			data.WasteTotalTokens = w.TotalTokens
			data.WasteProductive = wasteView{Tokens: w.Productive.Tokens, Percent: w.Productive.Percent, Label: formatTokensInt(w.Productive.Tokens)}
			data.WasteRetry = wasteView{Tokens: w.Retry.Tokens, Percent: w.Retry.Percent, Label: formatTokensInt(w.Retry.Tokens)}
			data.WasteCompaction = wasteView{Tokens: w.Compaction.Tokens, Percent: w.Compaction.Percent, Label: formatTokensInt(w.Compaction.Tokens)}
			data.WasteCacheMiss = wasteView{Tokens: w.CacheMiss.Tokens, Percent: w.CacheMiss.Percent, Label: formatTokensInt(w.CacheMiss.Tokens)}
			data.WasteIdleContext = wasteView{Tokens: w.IdleContext.Tokens, Percent: w.IdleContext.Percent, Label: formatTokensInt(w.IdleContext.Tokens)}
			data.WasteProductivePct = w.ProductivePct
			data.WasteWastePct = w.WastePct
			// Build conic-gradient for waste pie chart.
			type wasteSlice struct {
				pct   float64
				color string
			}
			slices := []wasteSlice{
				{w.Productive.Percent, "var(--success, #22c55e)"},
				{w.Retry.Percent, "var(--error, #ef4444)"},
				{w.Compaction.Percent, "#a855f7"},
				{w.CacheMiss.Percent, "#f59e0b"},
				{w.IdleContext.Percent, "var(--text-muted, #6b7280)"},
			}
			cumPct := 0.0
			var stops []string
			for _, sl := range slices {
				if sl.pct < 0.1 {
					continue
				}
				stops = append(stops, fmt.Sprintf("%s %.2f%% %.2f%%", sl.color, cumPct, cumPct+sl.pct))
				cumPct += sl.pct
			}
			if len(stops) >= 2 {
				data.WastePieGradient = template.CSS(strings.Join(stops, ", "))
				data.HasWastePie = true
			}
		}

		if saturation.TotalTokensLost >= 1_000_000 {
			data.SatTotalTokensLostK = fmt.Sprintf("%.1fM", float64(saturation.TotalTokensLost)/1_000_000)
		} else if saturation.TotalTokensLost >= 1000 {
			data.SatTotalTokensLostK = fmt.Sprintf("%dK", saturation.TotalTokensLost/1000)
		} else {
			data.SatTotalTokensLostK = fmt.Sprintf("%d", saturation.TotalTokensLost)
		}

		for _, ms := range saturation.Models {
			satClass := "good"
			if ms.AvgPeakPct > 80 {
				satClass = "poor"
			} else if ms.AvgPeakPct > 60 {
				satClass = "warning"
			}
			// Format efficiency metrics.
			costPer1K := ""
			if ms.CostPer1KOutput > 0 {
				if ms.CostPer1KOutput < 0.01 {
					costPer1K = fmt.Sprintf("$%.4f", ms.CostPer1KOutput)
				} else {
					costPer1K = fmt.Sprintf("$%.3f", ms.CostPer1KOutput)
				}
			}
			outPerDollar := ""
			if ms.OutputPerDollar > 0 {
				if ms.OutputPerDollar >= 1_000_000 {
					outPerDollar = fmt.Sprintf("%.1fM", ms.OutputPerDollar/1_000_000)
				} else if ms.OutputPerDollar >= 1000 {
					outPerDollar = fmt.Sprintf("%.1fK", ms.OutputPerDollar/1000)
				} else {
					outPerDollar = fmt.Sprintf("%.0f", ms.OutputPerDollar)
				}
			}
			gradeClass := "good"
			switch ms.EfficiencyGrade {
			case "C":
				gradeClass = "warning"
			case "D", "F":
				gradeClass = "poor"
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
				CostPer1KOutput:   costPer1K,
				OutputPerDollar:   outPerDollar,
				ErrorRate:         ms.ErrorRate,
				AvgOutputRatio:    ms.AvgOutputRatio,
				EfficiencyScore:   ms.EfficiencyScore,
				EfficiencyGrade:   ms.EfficiencyGrade,
				GradeClass:        gradeClass,
				TotalCost:         ms.TotalCost,
				HasCost:           ms.TotalCost > 0,
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

		// Saturation forecast.
		if saturation.Forecast != nil && saturation.Forecast.SessionsWithForecast > 0 {
			fc := saturation.Forecast
			data.HasForecastSat = true
			data.ForecastAvgMsgsToCompaction = fc.AvgMsgsToCompaction
			data.ForecastAvgGrowthPerMsg = fc.AvgTokenGrowthPerMsg
			data.ForecastSessionsAnalyzed = fc.SessionsWithForecast
			data.ForecastSessionsCompacted = fc.SessionsWithCompacted
			if fc.SessionsWithForecast > 0 {
				data.ForecastCompactedPct = float64(fc.SessionsWithCompacted) / float64(fc.SessionsWithForecast) * 100
			}
			if fc.AvgTokenGrowthPerMsg >= 1000 {
				data.ForecastAvgGrowthK = fmt.Sprintf("%.1fK", float64(fc.AvgTokenGrowthPerMsg)/1000)
			} else {
				data.ForecastAvgGrowthK = fmt.Sprintf("%d", fc.AvgTokenGrowthPerMsg)
			}

			for _, mf := range fc.Models {
				fv := modelForecastView{
					Model:                 mf.Model,
					MaxInputTokens:        mf.MaxInputTokens,
					SessionCount:          mf.SessionCount,
					CompactedCount:        mf.CompactedCount,
					AvgMsgsToCompacted:    mf.AvgMsgsToCompacted,
					MedianMsgsToCompacted: mf.MedianMsgsToCompacted,
					AvgTokenGrowthPerMsg:  mf.AvgTokenGrowthPerMsg,
					AvgPeakUtilization:    mf.AvgPeakUtilization,
					PredictedMsgsTo80:     mf.PredictedMsgsTo80,
					PredictedMsgsTo100:    mf.PredictedMsgsTo100,
					Recommendation:        mf.Recommendation,
					HasCompactions:        mf.CompactedCount > 0,
					HasPrediction:         mf.PredictedMsgsTo80 > 0,
				}
				if mf.AvgTokenGrowthPerMsg >= 1000 {
					fv.AvgTokenGrowthK = fmt.Sprintf("%.1fK", float64(mf.AvgTokenGrowthPerMsg)/1000)
				} else {
					fv.AvgTokenGrowthK = fmt.Sprintf("%d", mf.AvgTokenGrowthPerMsg)
				}
				data.ForecastModels = append(data.ForecastModels, fv)
			}

			// Histogram with bar heights.
			if len(fc.CompactionHistogram) > 0 {
				data.HasHistogram = true
				maxCount := 0
				for _, b := range fc.CompactionHistogram {
					if b.Count > maxCount {
						maxCount = b.Count
					}
				}
				for _, b := range fc.CompactionHistogram {
					heightPct := 0.0
					if maxCount > 0 {
						heightPct = float64(b.Count) / float64(maxCount) * 100
					}
					if heightPct < 3 && b.Count > 0 {
						heightPct = 3
					}
					data.ForecastHistogram = append(data.ForecastHistogram, histogramBucketView{
						Label:     b.Label,
						Count:     b.Count,
						HeightPct: heightPct,
					})
				}
			}
		}

		// System prompt impact (5.3).
		if pi := saturation.PromptImpact; pi != nil && pi.TotalSessions > 0 {
			data.HasPromptImpact = true
			data.PromptTotalSessions = pi.TotalSessions
			data.PromptAvgEstimate = formatTokensInt(pi.AvgEstimate)
			data.PromptMedianEstimate = formatTokensInt(pi.MedianEst)
			data.PromptMinEstimate = formatTokensInt(pi.MinEstimate)
			data.PromptMaxEstimate = formatTokensInt(pi.MaxEstimate)
			data.PromptAvgCostPct = pi.AvgPromptCostPct
			data.PromptTotalTokens = formatTokensInt(pi.TotalPromptTokens)
			data.PromptSmallCount = pi.SmallCount
			data.PromptMediumCount = pi.MediumCount
			data.PromptLargeCount = pi.LargeCount
			data.PromptSmallErrorRate = pi.SmallErrorRate
			data.PromptMediumErrorRate = pi.MediumErrorRate
			data.PromptLargeErrorRate = pi.LargeErrorRate
			data.PromptSmallRetryRate = pi.SmallRetryRate
			data.PromptMediumRetryRate = pi.MediumRetryRate
			data.PromptLargeRetryRate = pi.LargeRetryRate
			data.PromptTrend = pi.Trend
			data.PromptRecommendation = pi.Recommendation
			switch pi.Trend {
			case "growing":
				data.PromptTrendIcon = "↗"
			case "shrinking":
				data.PromptTrendIcon = "↘"
			default:
				data.PromptTrendIcon = "→"
			}
			// At least 2 buckets with data.
			nonEmpty := 0
			if pi.SmallCount > 0 {
				nonEmpty++
			}
			if pi.MediumCount > 0 {
				nonEmpty++
			}
			if pi.LargeCount > 0 {
				nonEmpty++
			}
			data.HasPromptBuckets = nonEmpty >= 2
		}

		// Model fitness profiles (6.4).
		if fit := saturation.Fitness; fit != nil && fit.TotalSessions > 0 {
			data.HasFitness = true
			data.FitnessTotalSessions = fit.TotalSessions
			data.FitnessRecs = fit.Recommendations
			data.HasFitnessRecs = len(fit.Recommendations) > 0
			for _, tp := range fit.TaskTypes {
				gradeClass := "good"
				switch {
				case tp.BestScore < 40:
					gradeClass = "poor"
				case tp.BestScore < 70:
					gradeClass = "warning"
				}
				bestGrade := ""
				if len(tp.Models) > 0 {
					bestGrade = tp.Models[0].FitnessGrade
				}
				ttv := fitnessTaskTypeView{
					TaskType:     tp.TaskType,
					SessionCount: tp.SessionCount,
					BestModel:    tp.BestModel,
					BestScore:    tp.BestScore,
					BestGrade:    bestGrade,
					GradeClass:   gradeClass,
					HasMultiple:  len(tp.Models) > 1,
				}
				for i, m := range tp.Models {
					mGradeClass := "good"
					switch {
					case m.FitnessScore < 40:
						mGradeClass = "poor"
					case m.FitnessScore < 70:
						mGradeClass = "warning"
					}
					costStr := fmt.Sprintf("$%.3f", m.AvgCost)
					if m.AvgCost >= 1.0 {
						costStr = fmt.Sprintf("$%.2f", m.AvgCost)
					}
					ttv.Models = append(ttv.Models, fitnessModelView{
						Model:        m.Model,
						SessionCount: m.SessionCount,
						AvgCost:      costStr,
						AvgMessages:  fmt.Sprintf("%.0f", m.AvgMessages),
						ErrorRate:    m.ErrorRate,
						RetryRate:    m.RetryRate,
						FitnessScore: m.FitnessScore,
						FitnessGrade: m.FitnessGrade,
						GradeClass:   mGradeClass,
						IsBest:       i == 0,
					})
				}
				data.FitnessTaskTypes = append(data.FitnessTaskTypes, ttv)
			}
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
		// Detect if we're using multi-benchmark catalog.
		isMulti := false
		if mc, ok := s.benchmarkRec.Benchmarks().(benchmark.MultiCatalog); ok {
			isMulti = true
			// Populate benchmark source metadata.
			data.HasMultiBenchmark = true
			weights := benchmark.DefaultCompositeWeights()
			for _, src := range mc.Sources() {
				w := weights[src]
				data.BenchmarkSources = append(data.BenchmarkSources, benchmarkSourceView{
					Name:      benchmarkSourceDisplayName(src),
					Key:       string(src),
					Weight:    w,
					WeightPct: int(w * 100),
				})
			}
		}

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
				av := modelAlternativeView{
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
				}
				if isMulti && len(alt.CurrentScores) > 0 {
					av.CurrentScores = toBenchmarkScoreViews(alt.CurrentScores)
					av.AltScores = toBenchmarkScoreViews(alt.AltScores)
					av.HasBreakdown = true
				}
				data.ModelAlternatives = append(data.ModelAlternatives, av)
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
				lv := qacLeaderView{
					Rank:           entry.Rank,
					Model:          entry.Model,
					BenchmarkScore: entry.BenchmarkScore,
					InputCost:      entry.InputCost,
					QAC:            entry.QAC,
					IsCurrentModel: currentModels[entry.Model],
				}
				if isMulti && len(entry.Scores) > 0 {
					lv.Scores = toBenchmarkScoreViews(entry.Scores)
					lv.SourceCount = entry.SourceCount
					lv.HasBreakdown = true
				}
				data.QACLeaderboard = append(data.QACLeaderboard, lv)
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
		// Compute daily budget line for Cost Over Time overlay.
		// When a specific project is filtered, use its daily limit;
		// otherwise sum all project daily limits.
		var dailySum float64
		for _, bs := range budgets {
			if project == "" || bs.ProjectPath == project {
				dailySum += bs.DailyLimit
			}
		}
		if dailySum > 0 {
			data.BudgetDailyLine = dailySum
			data.HasBudgetLine = true
			// Compute budget line height as % of chart (relative to max bucket cost).
			maxBucketCost := 0.0
			for _, b := range data.Buckets {
				if b.Cost > maxBucketCost {
					maxBucketCost = b.Cost
				}
			}
			// Use the larger of maxBucketCost and dailySum as the chart ceiling
			// so the budget line is always visible even if above all bars.
			ceiling := maxBucketCost
			if dailySum > ceiling {
				ceiling = dailySum
			}
			if ceiling > 0 {
				data.BudgetLineHeightPct = (dailySum / ceiling) * 100
				// Re-scale all bar heights when budget line exceeds max cost.
				if dailySum > maxBucketCost && maxBucketCost > 0 {
					for i := range data.Buckets {
						data.Buckets[i].HeightPct = (data.Buckets[i].Cost / ceiling) * 100
						if data.Buckets[i].HeightPct < 2 && data.Buckets[i].Cost > 0 {
							data.Buckets[i].HeightPct = 2
						}
					}
				}
			}
		}
	}

	return data
}

// handleCostOverviewPartial renders the Overview tab content.
// Uses cachedCostsPage to avoid recomputing the full cost page on each tab switch.
func (s *Server) handleCostOverviewPartial(w http.ResponseWriter, r *http.Request) {
	data := s.cachedCostsPage(r)
	s.renderPartial(w, "cost_overview_partial", data)
}

// handleCostToolsPartial renders the Tools & Agents tab content.
// Uses cachedCostsPage to avoid recomputing the full cost page on each tab switch.
func (s *Server) handleCostToolsPartial(w http.ResponseWriter, r *http.Request) {
	data := s.cachedCostsPage(r)
	s.renderPartial(w, "cost_tools_partial", data)
}

// handleCostOptimizationPartial renders the Optimization tab content.
// Uses cachedCostsPage to avoid recomputing the full cost page on each tab switch.
func (s *Server) handleCostOptimizationPartial(w http.ResponseWriter, r *http.Request) {
	data := s.cachedCostsPage(r)
	s.renderPartial(w, "cost_optimization_partial", data)
}

func (s *Server) handleCostTreemapPartial(w http.ResponseWriter, r *http.Request) {
	data := s.cachedCostsPage(r)
	s.renderPartial(w, "cost_treemap_partial", data)
}

// buildTreemapViews converts domain CostTreemapNode to view-layer treemapNodeView.
func buildTreemapViews(nodes []session.CostTreemapNode) []treemapNodeView {
	views := make([]treemapNodeView, len(nodes))
	for i, n := range nodes {
		views[i] = treemapNodeView{
			Name:         n.Name,
			Cost:         formatCostValue(n.Cost),
			CostRaw:      n.Cost,
			Tokens:       formatTokensShort(n.Tokens),
			SessionCount: n.SessionCount,
			Share:        n.Share,
			WidthPct:     fmt.Sprintf("%.1f%%", clampTreemapWidth(n.Share)),
			ColorIdx:     i % 10, // cycle through --pie-color-0 to --pie-color-9
		}
		if len(n.Children) > 0 {
			views[i].Children = make([]treemapNodeView, len(n.Children))
			for j, c := range n.Children {
				views[i].Children[j] = treemapNodeView{
					Name:         c.Name,
					Cost:         formatCostValue(c.Cost),
					CostRaw:      c.Cost,
					Tokens:       formatTokensShort(c.Tokens),
					SessionCount: c.SessionCount,
					Share:        c.Share,
					WidthPct:     fmt.Sprintf("%.1f%%", clampTreemapWidth(c.Share)),
					ColorIdx:     i % 10, // inherit parent color
				}
			}
		}
	}
	return views
}

// clampTreemapWidth ensures minimum visible width for small-share nodes.
func clampTreemapWidth(share float64) float64 {
	if share < 3 && share > 0 {
		return 3 // minimum 3% width for visibility
	}
	return share
}

// formatCostValue formats a cost as "$X.XX".
func formatCostValue(cost float64) string {
	if cost >= 1 {
		return fmt.Sprintf("$%.2f", cost)
	}
	return fmt.Sprintf("$%.4f", cost)
}

// formatTokensShort formats tokens as "123K" or "1.2M".
func formatTokensShort(tokens int) string {
	if tokens >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(tokens)/1000000)
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%.0fK", float64(tokens)/1000)
	}
	return fmt.Sprintf("%d", tokens)
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

// ── File Explorer ──

type fileExplorerPage struct {
	Nav             string
	SidebarProjects []sidebarProject
	Projects        []projectItem
	ProjectPath     string
	ProjectName     string
	DirPrefix       string // current directory filter (empty = root)
	Breadcrumbs     []breadcrumb
	Rows            []fileRow // unified list: directories first, then files
	HasRows         bool
	TotalFiles      int // count of file rows only
	TotalDirs       int // count of directory rows only
}

type breadcrumb struct {
	Name      string
	Path      string // cumulative path for the link (e.g. "internal/web")
	IsCurrent bool
}

// fileRow is a unified row in the GitHub-style file list.
// It can be either a directory (IsDir=true) or a file.
type fileRow struct {
	IsDir          bool
	Name           string // display name (e.g. "internal" or "handlers.go")
	DirPath        string // for dirs: link target (relative, e.g. "internal/web")
	FileCount      int    // for dirs: number of files inside
	SessionCount   int    // for files: number of AI sessions that wrote this file
	HasAI          bool   // true if this file was modified by at least one AI session
	LastChangeType string
	ChangeClass    string // CSS class for badge
	LastSessionID  string
	LastSummary    string // AI session summary
	LastBranch     string
	LastTimeAgo    string // time since last AI modification
	LastCommitSHA  string // commit SHA from the AI session (short, 7 chars)
	// Git commit info (from TreeProvider).
	LastCommitMsg string // last git commit message for this file
	LastCommitAgo string // time since last git commit
}

func (s *Server) handleFileExplorer(w http.ResponseWriter, r *http.Request) {
	projectPath := "/" + r.PathValue("path")
	if projectPath == "/" {
		// No project selected — redirect to first available project.
		projects := s.buildProjectList("")
		if len(projects) > 0 {
			http.Redirect(w, r, "/files"+projects[0].Path, http.StatusFound)
			return
		}
		s.render(w, "file_explorer.html", fileExplorerPage{Nav: "files"})
		return
	}

	dirPrefix := strings.TrimSuffix(r.URL.Query().Get("dir"), "/")
	if dirPrefix == "." {
		dirPrefix = ""
	}

	data := fileExplorerPage{
		Nav:             "files",
		SidebarProjects: s.buildSidebarProjects(r.Context(), projectPath),
		Projects:        s.buildProjectList(projectPath),
		ProjectPath:     projectPath,
		ProjectName:     projectBaseName(projectPath),
		DirPrefix:       dirPrefix,
	}

	// Build clickable breadcrumbs from dirPrefix segments.
	if dirPrefix != "" {
		segments := strings.Split(dirPrefix, "/")
		cumulative := ""
		for i, seg := range segments {
			if seg == "" {
				continue
			}
			if cumulative == "" {
				cumulative = seg
			} else {
				cumulative += "/" + seg
			}
			data.Breadcrumbs = append(data.Breadcrumbs, breadcrumb{
				Name:      seg,
				Path:      cumulative,
				IsCurrent: i == len(segments)-1,
			})
		}
	}

	if s.store == nil {
		s.render(w, "file_explorer.html", data)
		return
	}

	// File paths in the DB are absolute. Convert the relative dirPrefix
	// the user sees into an absolute prefix for the SQL query.
	absPrefix := ""
	if dirPrefix != "" {
		absPrefix = strings.TrimSuffix(projectPath, "/") + "/" + dirPrefix
	}

	entries, err := s.cachedFilesForProject(projectPath, absPrefix, 500)
	if err != nil {
		s.logger.Printf("file explorer: %v", err)
		s.render(w, "file_explorer.html", data)
		return
	}

	// Build directory aggregation and file list.
	// File paths from DB are absolute — strip project path prefix for display.
	projPrefix := strings.TrimSuffix(projectPath, "/") + "/"
	currentPfx := "" // e.g. "" or "internal/web/"
	if dirPrefix != "" {
		currentPfx = dirPrefix + "/"
	}

	// Compute relative sub-paths for all entries.
	type relEntry struct {
		entry   session.ProjectFileEntry
		relPath string // relative to project root
		subPath string // relative to current dirPrefix
	}
	var relEntries []relEntry
	dirCounts := make(map[string]int) // relative dir → file count

	for _, e := range entries {
		// Make path relative to project root.
		rel := e.FilePath
		if strings.HasPrefix(rel, projPrefix) {
			rel = rel[len(projPrefix):]
		} else {
			continue // outside project tree
		}

		// Strip the current dir prefix.
		sub := rel
		if currentPfx != "" {
			if strings.HasPrefix(sub, currentPfx) {
				sub = sub[len(currentPfx):]
			} else {
				continue // doesn't match the current dir filter
			}
		}

		// Count sub-directories.
		subParts := splitPath(sub)
		if len(subParts) > 1 {
			topDir := dirPrefix
			if topDir != "" {
				topDir += "/"
			}
			topDir += subParts[0]
			dirCounts[topDir]++
		}

		relEntries = append(relEntries, relEntry{entry: e, relPath: rel, subPath: sub})
	}

	// Build AI overlay map: relative path → AI info.
	dirNames := make(map[string]struct{}, len(dirCounts))
	for dir := range dirCounts {
		dirNames[filepath.Base(dir)] = struct{}{}
	}

	type aiInfo struct {
		SessionCount   int
		LastChangeType string
		LastSessionID  string
		LastSummary    string
		LastBranch     string
		LastTimeAgo    string
		LastCommitSHA  string
	}
	aiMap := make(map[string]aiInfo) // basename → AI data
	for _, re := range relEntries {
		subParts := splitPath(re.subPath)
		if len(subParts) > 1 {
			continue // in a subdirectory, not at current level
		}
		base := filepath.Base(re.relPath)
		if _, isDirName := dirNames[base]; isDirName && filepath.Ext(base) == "" {
			continue
		}
		aiMap[base] = aiInfo{
			SessionCount:   re.entry.SessionCount,
			LastChangeType: string(re.entry.LastChangeType),
			LastSessionID:  string(re.entry.LastSessionID),
			LastSummary:    truncStr(re.entry.LastSummary, 80),
			LastBranch:     re.entry.LastBranch,
			LastTimeAgo:    timeAgoString(re.entry.LastSessionTime),
			LastCommitSHA:  truncateID(re.entry.LastCommitSHA, 7),
		}
	}

	// Try to use git tree for the full listing.
	useGitTree := s.gitTree != nil && s.gitTree.Available(projectPath)

	var dirRows, fileRows []fileRow

	if useGitTree {
		// Full git tree + AI overlay.
		gitEntries, gitErr := s.gitTree.ListFiles(projectPath, "", dirPrefix)
		if gitErr != nil {
			s.logger.Printf("file explorer git tree: %v", gitErr)
			useGitTree = false
		} else {
			// Collect file paths for batch last-commit lookup.
			var filePaths []string
			for _, ge := range gitEntries {
				if ge.IsDir {
					dp := ge.Path
					dirRows = append(dirRows, fileRow{
						IsDir:   true,
						Name:    filepath.Base(dp),
						DirPath: dp,
					})
				} else {
					filePaths = append(filePaths, ge.Path)
				}
			}

			// Fetch last commit for all files in one batch.
			commitMap := make(map[string]gittree.FileCommit)
			if len(filePaths) > 0 {
				if commits, err := s.gitTree.LastCommitForFiles(projectPath, "", filePaths); err == nil {
					for _, fc := range commits {
						commitMap[fc.Path] = fc
					}
				}
			}

			for _, ge := range gitEntries {
				if ge.IsDir {
					continue
				}
				base := filepath.Base(ge.Path)
				row := fileRow{
					Name: base,
				}

				// Git commit info.
				if fc, ok := commitMap[ge.Path]; ok {
					row.LastCommitMsg = truncStr(fc.Message, 80)
					row.LastCommitAgo = timeAgoString(fc.Date)
				}

				// AI overlay.
				if ai, ok := aiMap[base]; ok {
					row.HasAI = true
					row.SessionCount = ai.SessionCount
					row.LastChangeType = ai.LastChangeType
					row.LastSessionID = ai.LastSessionID
					row.LastSummary = ai.LastSummary
					row.LastBranch = ai.LastBranch
					row.LastTimeAgo = ai.LastTimeAgo
					row.LastCommitSHA = ai.LastCommitSHA

					switch ai.LastChangeType {
					case "created":
						row.ChangeClass = "file-created"
					case "deleted":
						row.ChangeClass = "file-deleted"
					default:
						row.ChangeClass = "file-modified"
					}
				}

				fileRows = append(fileRows, row)
			}
		}
	}

	if !useGitTree {
		// Fallback: AI-only listing (no git tree available).
		for dir, count := range dirCounts {
			dirRows = append(dirRows, fileRow{
				IsDir:     true,
				Name:      filepath.Base(dir),
				DirPath:   dir,
				FileCount: count,
			})
		}

		for _, re := range relEntries {
			subParts := splitPath(re.subPath)
			if len(subParts) > 1 {
				continue
			}
			base := filepath.Base(re.relPath)
			if _, isDirName := dirNames[base]; isDirName && filepath.Ext(base) == "" {
				continue
			}
			changeClass := "file-modified"
			switch re.entry.LastChangeType {
			case "created":
				changeClass = "file-created"
			case "deleted":
				changeClass = "file-deleted"
			}
			fileRows = append(fileRows, fileRow{
				Name:           base,
				HasAI:          true,
				SessionCount:   re.entry.SessionCount,
				LastChangeType: string(re.entry.LastChangeType),
				ChangeClass:    changeClass,
				LastSessionID:  string(re.entry.LastSessionID),
				LastSummary:    truncStr(re.entry.LastSummary, 80),
				LastBranch:     re.entry.LastBranch,
				LastTimeAgo:    timeAgoString(re.entry.LastSessionTime),
			})
		}
	}

	// Sort dirs and files alphabetically.
	sort.Slice(dirRows, func(i, j int) bool { return dirRows[i].Name < dirRows[j].Name })
	sort.Slice(fileRows, func(i, j int) bool { return fileRows[i].Name < fileRows[j].Name })

	// Merge: dirs first, then files.
	data.Rows = append(data.Rows, dirRows...)
	data.Rows = append(data.Rows, fileRows...)
	data.HasRows = len(data.Rows) > 0
	data.TotalFiles = len(fileRows)
	data.TotalDirs = len(dirRows)

	s.render(w, "file_explorer.html", data)
}

// truncStr truncates a string to maxLen characters, appending "…" if shortened.
func truncStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// splitPath splits a file path into its components.
func splitPath(p string) []string {
	var parts []string
	for p != "" && p != "." && p != "/" {
		dir, file := filepath.Split(p)
		if file != "" {
			parts = append([]string{file}, parts...)
		}
		p = filepath.Clean(dir)
	}
	return parts
}

// ── File Blame ──

type blamePage struct {
	Nav           string
	ProjectPath   string
	DirPrefix     string
	FileName      string // basename
	RelPath       string // relative path for display
	TotalSessions int
	Entries       []blameEntryView
}

type blameEntryView struct {
	SessionID   string
	Summary     string
	Branch      string
	Provider    string
	ChangeType  string
	ChangeClass string // CSS class: created, modified, deleted, read
	TimeAgo     string
}

// handleBlame renders the blame page for a specific file,
// showing all AI sessions that modified it.
// Query params: ?file=relative/path&project=/abs/project/path
func (s *Server) handleBlame(w http.ResponseWriter, r *http.Request) {
	relFile := r.URL.Query().Get("file")
	projectPath := r.URL.Query().Get("project")
	if relFile == "" {
		http.Error(w, "missing ?file= parameter", http.StatusBadRequest)
		return
	}

	data := blamePage{
		Nav:         "files",
		ProjectPath: projectPath,
		FileName:    filepath.Base(relFile),
		RelPath:     relFile,
	}

	// Build dir prefix for back navigation
	dir := filepath.Dir(relFile)
	if dir != "." && dir != "/" && dir != "" {
		data.DirPrefix = dir
	}

	if s.store != nil {
		// DB stores absolute paths — construct from project path + relative file.
		absFile := relFile
		if projectPath != "" && !filepath.IsAbs(relFile) {
			absFile = strings.TrimSuffix(projectPath, "/") + "/" + relFile
		}

		entries, err := s.store.GetSessionsByFile(session.BlameQuery{
			FilePath: absFile,
		})
		if err != nil {
			s.logger.Printf("blame: %v", err)
		}

		data.TotalSessions = len(entries)
		for _, e := range entries {
			changeClass := "modified"
			switch string(e.ChangeType) {
			case "created":
				changeClass = "created"
			case "deleted":
				changeClass = "deleted"
			case "read":
				changeClass = "read"
			}
			data.Entries = append(data.Entries, blameEntryView{
				SessionID:   string(e.SessionID),
				Summary:     truncStr(e.Summary, 120),
				Branch:      e.Branch,
				Provider:    string(e.Provider),
				ChangeType:  string(e.ChangeType),
				ChangeClass: changeClass,
				TimeAgo:     timeAgoString(e.CreatedAt),
			})
		}
	}

	s.render(w, "blame.html", data)
}

// ── File Tree ──

// fileTreePage is the view model for the interactive file tree page.
type fileTreePage struct {
	Nav             string
	SidebarProjects []sidebarProject
	Projects        []projectItem
	ProjectPath     string
	ProjectName     string
	HasTree         bool
	Tree            []fileTreeNode
	Stats           *fileTreeStats
	MaxSessions     int // maximum session count across all files (for heatmap)
}

type fileTreeStats struct {
	TotalFiles    int
	TotalDirs     int
	TotalSessions int
}

// fileTreeNode represents a directory or file in the recursive tree.
type fileTreeNode struct {
	IsDir         bool
	Name          string
	DirPath       string // dirs: relative dir path (for HTMX children loading)
	FilePath      string // relative path (for HTMX session loading)
	ProjectPath   string // absolute project path (for HTMX query param)
	IndentPx      int
	Children      []fileTreeNode
	FileCount     int    // dirs: number of AI-touched files inside
	HasAI         bool   // files: has at least one AI session
	SessionCount  int    // files: number of AI sessions
	LastSessionID string // files: most recent session ID
	LastCommitSHA string // files: commit SHA from the AI session (short)
	LastTimeAgo   string // files: time since last AI modification
	HeatClass     string // heatmap CSS class: ft-heat-cold, ft-heat-warm, ft-heat-hot, ft-heat-fire
	Lazy          bool   // dirs: true if children should be loaded via HTMX (not pre-rendered)
	MaxSessions   int    // global max sessions (for heatmap consistency across lazy loads)
}

// handleFileTree renders the interactive file tree page with a full
// recursive tree of files that have AI session activity.
func (s *Server) handleFileTree(w http.ResponseWriter, r *http.Request) {
	projectPath := "/" + r.PathValue("path")
	if projectPath == "/" {
		projects := s.buildProjectList("")
		if len(projects) > 0 {
			http.Redirect(w, r, "/tree"+projects[0].Path, http.StatusFound)
			return
		}
		s.render(w, "file_tree.html", fileTreePage{Nav: "filetree"})
		return
	}

	data := fileTreePage{
		Nav:             "filetree",
		SidebarProjects: s.buildSidebarProjects(r.Context(), projectPath),
		Projects:        s.buildProjectList(projectPath),
		ProjectPath:     projectPath,
		ProjectName:     projectBaseName(projectPath),
	}

	if s.store == nil {
		s.render(w, "file_tree.html", data)
		return
	}

	// Fetch all files with AI activity for this project (no dirPrefix = full tree).
	entries, err := s.store.FilesForProject(projectPath, "", 5000)
	if err != nil {
		s.logger.Printf("file tree: %v", err)
		s.render(w, "file_tree.html", data)
		return
	}

	if len(entries) == 0 {
		s.render(w, "file_tree.html", data)
		return
	}

	// Build the recursive tree from flat file entries.
	projPrefix := strings.TrimSuffix(projectPath, "/") + "/"
	data.Tree, data.Stats, data.MaxSessions = buildFileTree(entries, projPrefix, projectPath, true)
	data.HasTree = len(data.Tree) > 0

	s.render(w, "file_tree.html", data)
}

// buildFileTree converts a flat list of ProjectFileEntry into a recursive
// tree of fileTreeNode, grouping files by directory.
// projPrefix is stripped from file paths to compute relative paths.
// relBase is prepended to dir paths for HTMX URLs (e.g. "internal/web" when
// building a subtree for that directory, so child dirs get "internal/web/templates").
// If lazyDirs is true, only root-level nodes are returned; subdirectories
// are marked Lazy=true so the template can use HTMX to load children on expand.
func buildFileTree(entries []session.ProjectFileEntry, projPrefix, projectPath string, lazyDirs bool, relBase ...string) ([]fileTreeNode, *fileTreeStats, int) {
	base := ""
	if len(relBase) > 0 {
		base = relBase[0]
	}

	// dir → children map for building the tree.
	type dirInfo struct {
		children map[string]*dirInfo // subdirs
		files    []fileTreeNode
		dirPath  string // relative dir path from project root
	}

	root := &dirInfo{children: make(map[string]*dirInfo), dirPath: base}

	stats := &fileTreeStats{}
	totalSessions := 0
	maxSessions := 0

	for _, e := range entries {
		rel := e.FilePath
		if strings.HasPrefix(rel, projPrefix) {
			rel = rel[len(projPrefix):]
		} else {
			continue
		}

		parts := splitPath(rel)
		if len(parts) == 0 {
			continue
		}

		stats.TotalFiles++
		totalSessions += e.SessionCount
		if e.SessionCount > maxSessions {
			maxSessions = e.SessionCount
		}

		// Navigate/create directory nodes.
		current := root
		pathSoFar := base
		for _, dir := range parts[:len(parts)-1] {
			if pathSoFar == "" {
				pathSoFar = dir
			} else {
				pathSoFar = pathSoFar + "/" + dir
			}
			if current.children[dir] == nil {
				current.children[dir] = &dirInfo{children: make(map[string]*dirInfo), dirPath: pathSoFar}
			}
			current = current.children[dir]
		}

		// Build relative file path from project root (for HTMX session loading).
		var filePath string
		if base == "" {
			filePath = rel
		} else {
			filePath = base + "/" + rel
		}

		// Add file leaf.
		fileName := parts[len(parts)-1]
		current.files = append(current.files, fileTreeNode{
			IsDir:         false,
			Name:          fileName,
			FilePath:      filePath,
			ProjectPath:   projectPath,
			HasAI:         true,
			SessionCount:  e.SessionCount,
			LastSessionID: string(e.LastSessionID),
			LastCommitSHA: truncateID(e.LastCommitSHA, 7),
			LastTimeAgo:   timeAgoString(e.LastSessionTime),
			HeatClass:     sessionHeatClass(e.SessionCount, maxSessions),
		})
	}

	stats.TotalSessions = totalSessions

	// Second pass: recompute heatmap classes now that maxSessions is known.
	// (files added before maxSessions was finalized need re-classification.)
	var fixHeat func(d *dirInfo)
	fixHeat = func(d *dirInfo) {
		for i := range d.files {
			d.files[i].HeatClass = sessionHeatClass(d.files[i].SessionCount, maxSessions)
		}
		for _, child := range d.children {
			fixHeat(child)
		}
	}
	fixHeat(root)

	// countDirFiles recursively counts files in a dirInfo tree.
	var countDirFiles func(d *dirInfo) int
	countDirFiles = func(d *dirInfo) int {
		n := len(d.files)
		for _, child := range d.children {
			n += countDirFiles(child)
		}
		return n
	}

	// Recursively convert dirInfo into []fileTreeNode.
	var buildNodes func(d *dirInfo, depth int, lazy bool) []fileTreeNode
	buildNodes = func(d *dirInfo, depth int, lazy bool) []fileTreeNode {
		var nodes []fileTreeNode
		indent := depth * 16

		// Directories first (sorted).
		dirNames := make([]string, 0, len(d.children))
		for name := range d.children {
			dirNames = append(dirNames, name)
		}
		sort.Strings(dirNames)

		for _, name := range dirNames {
			child := d.children[name]
			stats.TotalDirs++

			if lazy && depth > 0 {
				// Don't recurse — mark as lazy-loadable.
				fileCount := countDirFiles(child)
				nodes = append(nodes, fileTreeNode{
					IsDir:       true,
					Name:        name,
					DirPath:     child.dirPath,
					ProjectPath: projectPath,
					IndentPx:    indent,
					FileCount:   fileCount,
					Lazy:        true,
					MaxSessions: maxSessions,
				})
			} else {
				childNodes := buildNodes(child, depth+1, lazy)
				fileCount := countFilesInTree(childNodes)
				nodes = append(nodes, fileTreeNode{
					IsDir:       true,
					Name:        name,
					DirPath:     child.dirPath,
					ProjectPath: projectPath,
					IndentPx:    indent,
					Children:    childNodes,
					FileCount:   fileCount,
					Lazy:        false,
				})
			}
		}

		// Files (sorted).
		sort.Slice(d.files, func(i, j int) bool { return d.files[i].Name < d.files[j].Name })
		for _, f := range d.files {
			f.IndentPx = indent
			nodes = append(nodes, f)
		}

		return nodes
	}

	return buildNodes(root, 0, lazyDirs), stats, maxSessions
}

// sessionHeatClass returns a CSS class for the heatmap based on the session count
// relative to the maximum session count across all files.
func sessionHeatClass(count, maxCount int) string {
	if maxCount <= 0 || count <= 0 {
		return "ft-heat-cold"
	}
	ratio := float64(count) / float64(maxCount)
	switch {
	case ratio >= 0.75:
		return "ft-heat-fire"
	case ratio >= 0.5:
		return "ft-heat-hot"
	case ratio >= 0.25:
		return "ft-heat-warm"
	default:
		return "ft-heat-cold"
	}
}

// countFilesInTree recursively counts file nodes in a tree.
func countFilesInTree(nodes []fileTreeNode) int {
	count := 0
	for _, n := range nodes {
		if n.IsDir {
			count += countFilesInTree(n.Children)
		} else {
			count++
		}
	}
	return count
}

// fileTreeSessionsData is the view model for the HTMX sessions partial.
type fileTreeSessionsData struct {
	Entries []blameEntryView
}

// handleFileTreeSessions is an HTMX partial that returns the session list
// for a specific file in the file tree (lazy-loaded on click).
func (s *Server) handleFileTreeSessions(w http.ResponseWriter, r *http.Request) {
	relFile := r.URL.Query().Get("file")
	projectPath := r.URL.Query().Get("project")
	if relFile == "" || projectPath == "" {
		http.Error(w, "missing file or project parameter", http.StatusBadRequest)
		return
	}

	data := fileTreeSessionsData{}

	if s.store != nil {
		absFile := strings.TrimSuffix(projectPath, "/") + "/" + relFile
		entries, err := s.store.GetSessionsByFile(session.BlameQuery{
			FilePath:     absFile,
			ExcludeReads: true, // match badge counts which exclude reads
		})
		if err != nil {
			s.logger.Printf("file tree sessions: %v", err)
		}

		for _, e := range entries {
			changeClass := "modified"
			switch string(e.ChangeType) {
			case "created":
				changeClass = "created"
			case "deleted":
				changeClass = "deleted"
			case "read":
				changeClass = "read"
			}
			data.Entries = append(data.Entries, blameEntryView{
				SessionID:   string(e.SessionID),
				Summary:     truncStr(e.Summary, 120),
				Branch:      e.Branch,
				Provider:    string(e.Provider),
				ChangeType:  string(e.ChangeType),
				ChangeClass: changeClass,
				TimeAgo:     timeAgoString(e.CreatedAt),
			})
		}
	}

	s.renderPartial(w, "ft-sessions-partial", data)
}

// fileTreeChildrenData is the view model for the HTMX directory children partial.
type fileTreeChildrenData struct {
	Nodes []fileTreeNode
}

// handleFileTreeChildren is an HTMX partial that returns the children nodes
// of a directory in the file tree (lazy-loaded on dir expand).
func (s *Server) handleFileTreeChildren(w http.ResponseWriter, r *http.Request) {
	dirPath := r.URL.Query().Get("dir")
	projectPath := r.URL.Query().Get("project")
	maxStr := r.URL.Query().Get("max")
	if dirPath == "" || projectPath == "" {
		http.Error(w, "missing dir or project parameter", http.StatusBadRequest)
		return
	}

	maxSessions := 1
	if v, err := strconv.Atoi(maxStr); err == nil && v > 0 {
		maxSessions = v
	}

	data := fileTreeChildrenData{}

	if s.store == nil {
		s.renderPartial(w, "ft-children-partial", data)
		return
	}

	// Query files under this specific directory prefix.
	absDir := strings.TrimSuffix(projectPath, "/") + "/" + dirPath
	entries, err := s.store.FilesForProject(projectPath, absDir, 5000)
	if err != nil {
		s.logger.Printf("file tree children: %v", err)
		s.renderPartial(w, "ft-children-partial", data)
		return
	}

	if len(entries) == 0 {
		s.renderPartial(w, "ft-children-partial", data)
		return
	}

	// Build a subtree using dirPath as the new root prefix.
	// This makes the returned nodes start at depth 0 relative to the target directory,
	// while keeping dirPath references relative to the project root for HTMX URLs.
	dirPrefix := strings.TrimSuffix(projectPath, "/") + "/" + dirPath + "/"
	nodes, _, subMax := buildFileTree(entries, dirPrefix, projectPath, true, dirPath)
	if subMax > maxSessions {
		maxSessions = subMax
	}

	// Re-apply heatmap classes and propagate maxSessions for lazy child dirs.
	var fixNodeHeat func(nodes []fileTreeNode)
	fixNodeHeat = func(nodes []fileTreeNode) {
		for i := range nodes {
			if nodes[i].IsDir && nodes[i].Lazy {
				nodes[i].MaxSessions = maxSessions
			} else if !nodes[i].IsDir {
				nodes[i].HeatClass = sessionHeatClass(nodes[i].SessionCount, maxSessions)
			}
			fixNodeHeat(nodes[i].Children)
		}
	}
	fixNodeHeat(nodes)

	data.Nodes = nodes
	s.renderPartial(w, "ft-children-partial", data)
}

// ── Analysis ──

// analysisPartialData is the data structure for the analysis HTMX partial.
type analysisPartialData struct {
	SessionID        string
	HasAnalysis      bool
	CanAnalyze       bool
	Analysis         *analysisView
	AvailableModules []moduleInfoView // for checkbox selection
}

// moduleInfoView is a template-friendly module descriptor.
type moduleInfoView struct {
	Name        string
	Label       string
	Description string
}

// moduleResultView is a template-friendly per-module result.
type moduleResultView struct {
	Module     string
	Label      string
	DurationMs int
	TokensUsed int
	Error      string
	// Tool efficiency specific fields (populated only for tool_efficiency module)
	HasToolEfficiency bool
	ToolEfficiency    *toolEfficiencyView
}

// toolEfficiencyView is a template-friendly tool efficiency report.
type toolEfficiencyView struct {
	Summary        string
	OverallScore   int
	ScoreClass     string
	UsefulCalls    int
	RedundantCalls int
	Patterns       []string
	HasPatterns    bool
	Evaluations    []toolEvalView
	HasEvaluations bool
}

// toolEvalView is a single tool call evaluation for the template.
type toolEvalView struct {
	Index        int
	ToolName     string
	Usefulness   string
	UsefulClass  string // CSS class: "useful", "partial", "redundant", "wasteful"
	Reason       string
	InputTokens  int
	OutputTokens int
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
		data.AvailableModules = s.buildModuleInfoViews()
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

	// Parse selected modules from form checkboxes.
	r.ParseForm()
	var modules []analysis.ModuleName
	for _, name := range r.Form["modules"] {
		modules = append(modules, analysis.ModuleName(name))
	}
	// If no modules selected, default to session_quality only (backward compat).

	// Run analysis synchronously — the UI shows a spinner via htmx-indicator.
	_, err := s.analysisSvc.Analyze(r.Context(), service.AnalysisRequest{
		SessionID: session.ID(id),
		Trigger:   analysis.TriggerManual,
		Modules:   modules,
	})
	if err != nil {
		s.logger.Printf("analysis for %s: %v", id, err)
		// Even on error, the analysis entity is persisted (with error field set).
		// Fall through to render whatever is stored.
	}

	// Return the updated partial.
	data := analysisPartialData{
		SessionID:        id,
		CanAnalyze:       true,
		AvailableModules: s.buildModuleInfoViews(),
	}
	sa, aErr := s.analysisSvc.GetLatestAnalysis(id)
	if aErr == nil && sa != nil {
		data.HasAnalysis = true
		data.Analysis = buildAnalysisView(sa)
	}

	s.renderPartial(w, "analysis_partial", data)
}

// buildModuleInfoViews returns module info views for the analysis checkbox UI.
func (s *Server) buildModuleInfoViews() []moduleInfoView {
	if s.analysisSvc == nil {
		return nil
	}
	available := s.analysisSvc.AvailableModules()
	views := make([]moduleInfoView, len(available))
	for i, info := range available {
		views[i] = moduleInfoView{
			Name:        string(info.Name),
			Label:       info.Label,
			Description: info.Description,
		}
	}
	return views
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

	// Module results.
	if len(sa.Report.ModuleResults) > 0 {
		v.HasModuleResults = true
		for _, mr := range sa.Report.ModuleResults {
			mrv := moduleResultView{
				Module:     string(mr.Module),
				DurationMs: mr.DurationMs,
				TokensUsed: mr.TokensUsed,
				Error:      mr.Error,
			}
			// Set human-readable label.
			for _, info := range analysis.ModuleRegistry() {
				if info.Name == mr.Module {
					mrv.Label = info.Label
					break
				}
			}
			if mrv.Label == "" {
				mrv.Label = string(mr.Module)
			}
			// Parse module-specific payloads.
			if mr.Module == analysis.ModuleToolEfficiency && mr.Error == "" && len(mr.Payload) > 0 {
				var report analysis.ToolEfficiencyReport
				if err := json.Unmarshal(mr.Payload, &report); err == nil {
					mrv.HasToolEfficiency = true
					tev := &toolEfficiencyView{
						Summary:        report.Summary,
						OverallScore:   report.OverallScore,
						ScoreClass:     scoreClass(report.OverallScore),
						UsefulCalls:    report.UsefulCalls,
						RedundantCalls: report.RedundantCalls,
						Patterns:       report.Patterns,
						HasPatterns:    len(report.Patterns) > 0,
					}
					if len(report.ToolEvaluations) > 0 {
						tev.HasEvaluations = true
						for _, eval := range report.ToolEvaluations {
							tev.Evaluations = append(tev.Evaluations, toolEvalView{
								Index:        eval.Index,
								ToolName:     eval.ToolName,
								Usefulness:   eval.Usefulness,
								UsefulClass:  eval.Usefulness, // maps to CSS class directly
								Reason:       eval.Reason,
								InputTokens:  eval.InputTokens,
								OutputTokens: eval.OutputTokens,
							})
						}
					}
					mrv.ToolEfficiency = tev
				}
			}
			v.ModuleResults = append(v.ModuleResults, mrv)
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

	// Activity sparklines (14-day mini bar charts)
	HasSparklines     bool
	SparklineSessions []sparklineBar
	SparklineTokens   []sparklineBar
	SparklineCost     []sparklineBar
	SparklineErrors   []sparklineBar
}

// securityCatView is a template-friendly security category count.
type securityCatView struct {
	Category string
	Count    int
}

// insightView is a template-friendly auto-generated recommendation.
type insightView struct {
	ID        string // recommendation record ID (empty for non-persisted)
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

	// Find project metadata from cached sidebar groups (avoids redundant ListProjects).
	groups := s.cachedSidebarGroups(ctx)
	for _, g := range groups {
		if g.ProjectPath == projectPath {
			data.DisplayName = g.DisplayName
			data.RemoteURL = g.RemoteURL
			data.Provider = string(g.Provider)
			data.Category = g.Category
			break
		}
	}

	// KPIs (filtered by project) — must complete before rendering.
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

	// ── Concurrent data fetches ──
	// All sections below are independent — run them in parallel to cut latency.
	var mu sync.Mutex
	var wg sync.WaitGroup
	since7d := time.Now().AddDate(0, 0, -7)
	since90d := time.Now().AddDate(0, 0, -90)

	// 1. Recent sessions + agent distribution + error/tool counts.
	wg.Add(1)
	go func() {
		defer wg.Done()
		listReq := service.ListRequest{ProjectPath: projectPath}
		summaries, listErr := s.sessionSvc.List(listReq)
		if listErr != nil {
			return
		}

		var totalErrors, totalToolCalls, sessionsWithErrors int
		for _, sm := range summaries {
			totalErrors += sm.ErrorCount
			totalToolCalls += sm.ToolCallCount
			if sm.ErrorCount > 0 {
				sessionsWithErrors++
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

		agentCounts := make(map[string]int)
		for _, sm := range summaries {
			if sm.Agent != "" {
				agentCounts[sm.Agent]++
			}
		}
		var agents []agentChip
		for name, count := range agentCounts {
			agents = append(agents, agentChip{Name: name, Count: count})
		}
		sort.Slice(agents, func(i, j int) bool {
			return agents[i].Count > agents[j].Count
		})

		mu.Lock()
		data.TotalErrors = totalErrors
		data.TotalToolCalls = totalToolCalls
		data.SessionsWithErrors = sessionsWithErrors
		data.RecentSessions = summaries[:recentLimit]
		data.Agents = agents
		mu.Unlock()
	}()

	// 2. Weekly trends.
	wg.Add(1)
	go func() {
		defer wg.Done()
		trends, trendsErr := s.cachedTrends(ctx, service.TrendRequest{
			Period:      7 * 24 * time.Hour,
			ProjectPath: projectPath,
		})
		if trendsErr != nil {
			return
		}
		if trends.Current.SessionCount == 0 && trends.Previous.SessionCount == 0 {
			return
		}

		mu.Lock()
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
		mu.Unlock()
	}()

	// 3. Forecast.
	wg.Add(1)
	go func() {
		defer wg.Done()
		forecast, fErr := s.sessionSvc.Forecast(ctx, service.ForecastRequest{
			Period:      "weekly",
			Days:        90,
			ProjectPath: projectPath,
		})
		if fErr != nil || forecast.SessionCount == 0 {
			return
		}

		mu.Lock()
		data.HasForecast = true
		data.Projected30d = forecast.Projected30d
		data.Projected90d = forecast.Projected90d
		data.TrendPerDay = forecast.TrendPerDay
		data.TrendDir = forecast.TrendDir
		if forecast.TotalReal30d > 0 || forecast.SubscriptionMonthly > 0 {
			data.HasRealForecast = true
			data.TotalReal30d = forecast.TotalReal30d
			data.SubscriptionMonthly = forecast.SubscriptionMonthly
			data.APIProjected30d = forecast.APIProjected30d
		}
		mu.Unlock()
	}()

	// 4. Cache efficiency (7-day window).
	wg.Add(1)
	go func() {
		defer wg.Done()
		cacheEff, cacheErr := s.sessionSvc.CacheEfficiency(ctx, projectPath, since7d)
		if cacheErr != nil || cacheEff == nil || cacheEff.TotalInputTokens == 0 {
			return
		}

		mu.Lock()
		data.HasCacheStats = true
		data.DashCacheHitRate = cacheEff.CacheHitRate
		data.DashCacheMissSessions = cacheEff.SessionsWithMiss
		data.DashCacheTotalMisses = cacheEff.TotalCacheMisses
		data.DashCacheWaste = cacheEff.EstimatedWaste
		mu.Unlock()
	}()

	// 5. Analytics (event buckets — single indexed query, fast).
	wg.Add(1)
	go func() {
		defer wg.Done()
		if s.sessionEventSvc == nil {
			return
		}
		now := time.Now().UTC()
		since := now.AddDate(0, 0, -30)
		buckets, bErr := s.sessionEventSvc.QueryBuckets(sessionevent.BucketQuery{
			ProjectPath: projectPath,
			Granularity: "1d",
			Since:       since,
			Until:       now,
		})
		if bErr != nil || len(buckets) == 0 {
			return
		}

		var analyticsTools, analyticsSkills int
		allTools := make(map[string]int)
		var dailyBuckets []dailyActivity
		for _, b := range buckets {
			analyticsTools += b.ToolCallCount
			analyticsSkills += b.SkillLoadCount
			for k, v := range b.TopTools {
				allTools[k] += v
			}
			dailyBuckets = append(dailyBuckets, dailyActivity{
				Date:      b.BucketStart.Format("Jan 2"),
				ToolCalls: b.ToolCallCount,
				Errors:    b.ErrorCount,
				Sessions:  b.SessionCount,
			})
		}

		mu.Lock()
		data.HasAnalytics = true
		data.AnalyticsTools = analyticsTools
		data.AnalyticsSkills = analyticsSkills
		data.DailyBuckets = dailyBuckets
		data.TopTools = topN(allTools, analyticsTools, 8)
		mu.Unlock()
	}()

	// 6. Capabilities (filesystem only, fast).
	wg.Add(1)
	go func() {
		defer wg.Done()
		if s.registrySvc == nil {
			return
		}
		proj, scanErr := s.registrySvc.ScanProject(projectPath)
		if scanErr != nil {
			return
		}

		var capStats []capabilityStat
		for _, cs := range proj.CapabilityStats() {
			capStats = append(capStats, capabilityStat{
				Kind:  string(cs.Kind),
				Count: cs.Count,
			})
		}

		mu.Lock()
		data.HasCapabilities = true
		data.ProjectName = proj.Name
		data.MCPServerCount = len(proj.MCPServers)
		data.CapabilityStats = capStats
		mu.Unlock()
	}()

	// 7. Budget status.
	wg.Add(1)
	go func() {
		defer wg.Done()
		budgets, budgetErr := s.sessionSvc.BudgetStatus(ctx)
		if budgetErr != nil {
			return
		}

		mu.Lock()
		remoteURL := data.RemoteURL
		mu.Unlock()

		for _, bs := range budgets {
			if bs.ProjectPath == projectPath || bs.RemoteURL == remoteURL {
				mu.Lock()
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
				mu.Unlock()
				break
			}
		}
	}()

	// 8. Context saturation (cached — pre-warmed by scheduler every 2h).
	wg.Add(1)
	go func() {
		defer wg.Done()
		satResult, satErr := s.sessionSvc.ContextSaturation(ctx, projectPath, since90d)
		if satErr != nil || satResult == nil || satResult.TotalSessions == 0 {
			return
		}

		mu.Lock()
		data.HasSaturation = true
		data.SatAvgPeak = satResult.AvgPeakSaturation
		data.SatAbove80 = satResult.SessionsAbove80
		data.SatCompacted = satResult.SessionsCompacted
		data.SatTotalSessions = satResult.TotalSessions
		mu.Unlock()
	}()

	// 9. Agent ROI (cached — expensive, loads all session payloads on cold cache).
	wg.Add(1)
	go func() {
		defer wg.Done()
		agentROI, roiErr := s.sessionSvc.AgentROIAnalysis(ctx, projectPath, since90d)
		if roiErr != nil || agentROI == nil || len(agentROI.Agents) == 0 {
			return
		}

		var views []agentROIView
		for _, a := range agentROI.Agents {
			gradeClass := "badge-good"
			switch a.ROIGrade {
			case "C":
				gradeClass = "badge-warning"
			case "D", "F":
				gradeClass = "badge-poor"
			}
			views = append(views, agentROIView{
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

		mu.Lock()
		data.HasAgentROI = true
		data.AgentROI = views
		mu.Unlock()
	}()

	// 10. Skill ROI (cached — expensive, queries events for every session on cold cache).
	wg.Add(1)
	go func() {
		defer wg.Done()
		skillROI, skillErr := s.cachedSkillROI(ctx, projectPath, since90d)
		if skillErr != nil || skillROI == nil || len(skillROI.Skills) == 0 {
			return
		}

		var views []skillROIView
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
			views = append(views, skillROIView{
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

		mu.Lock()
		data.HasSkillROI = true
		data.SkillROI = views
		mu.Unlock()
	}()

	// 11. Security — read from cache only (ScanProject loads all session payloads).
	// The SecurityScanTask should pre-warm this via the scheduler.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if s.store == nil {
			return
		}
		cacheKey := "security:" + projectPath
		cacheData, _ := s.store.GetCache(cacheKey, securityCacheTTL)
		if cacheData == nil {
			// Cold cache: skip rather than blocking the page for 30+ seconds.
			// The scheduler will compute and cache this in the background.
			return
		}
		type cachedSecuritySummary struct {
			TotalAlerts    int               `json:"total_alerts"`
			CriticalAlerts int               `json:"critical_alerts"`
			HighAlerts     int               `json:"high_alerts"`
			AvgRiskScore   float64           `json:"avg_risk_score"`
			TopCategories  []securityCatView `json:"top_categories"`
		}
		var summary cachedSecuritySummary
		if err := json.Unmarshal(cacheData, &summary); err != nil || summary.TotalAlerts == 0 {
			return
		}

		mu.Lock()
		data.HasSecurity = true
		data.SecurityAlertCount = summary.TotalAlerts
		data.SecurityCritical = summary.CriticalAlerts
		data.SecurityHigh = summary.HighAlerts
		data.SecurityAvgRisk = summary.AvgRiskScore
		data.SecurityTopCats = summary.TopCategories
		mu.Unlock()
	}()

	// 12. Recommendations — read from store only (pre-computed by scheduler).
	// Removed the on-the-fly GenerateRecommendations fallback which was catastrophically
	// expensive: it calls AgentROI + SkillROI + CacheEfficiency + ContextSaturation
	// all inline, causing 30s+ timeouts for large projects.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if s.store == nil {
			return
		}
		storedRecs, storeRecErr := s.store.ListRecommendations(session.RecommendationFilter{
			ProjectPath: projectPath,
			Status:      session.RecStatusActive,
			Limit:       50,
		})
		if storeRecErr != nil || len(storedRecs) == 0 {
			return
		}

		var recViews []insightView
		for _, rec := range storedRecs {
			prioClass := "rec-low"
			switch rec.Priority {
			case "high":
				prioClass = "rec-high"
			case "medium":
				prioClass = "rec-medium"
			}
			recViews = append(recViews, insightView{
				ID:        rec.ID,
				Icon:      rec.Icon,
				Title:     rec.Title,
				Message:   rec.Message,
				Impact:    rec.Impact,
				Priority:  rec.Priority,
				PrioClass: prioClass,
			})
		}

		mu.Lock()
		data.HasRecommendations = true
		data.Recommendations = recViews
		mu.Unlock()
	}()

	// 13. Top files (single indexed query, fast).
	wg.Add(1)
	go func() {
		defer wg.Done()
		if s.store == nil {
			return
		}
		topFiles, tfErr := s.store.TopFilesForProject(projectPath, 10)
		if tfErr != nil || len(topFiles) == 0 {
			return
		}

		var views []topFileView
		for _, tf := range topFiles {
			views = append(views, topFileView{
				FilePath:     tf.FilePath,
				SessionCount: tf.SessionCount,
				WriteCount:   tf.WriteCount,
			})
		}

		mu.Lock()
		data.HasTopFiles = true
		data.TopFileEntries = views
		mu.Unlock()
	}()

	// 14. Activity sparklines (single indexed query, fast).
	wg.Add(1)
	go func() {
		defer wg.Done()
		if s.store == nil {
			return
		}
		sparkSince := time.Now().AddDate(0, 0, -14)
		sparkUntil := time.Now()
		buckets, sparkErr := s.store.QueryTokenBuckets("1d", sparkSince, sparkUntil, projectPath)
		if sparkErr != nil || len(buckets) == 0 {
			return
		}

		type dayAgg struct {
			sessions int
			tokens   int
			cost     float64
			errors   int
			label    string
		}
		byDay := make(map[string]*dayAgg)
		dayOrder := make([]string, 0, 14)
		for _, b := range buckets {
			key := b.BucketStart.Format("2006-01-02")
			agg, ok := byDay[key]
			if !ok {
				agg = &dayAgg{label: b.BucketStart.Format("Jan 2")}
				byDay[key] = agg
				dayOrder = append(dayOrder, key)
			}
			agg.sessions += b.SessionCount
			agg.tokens += b.InputTokens + b.OutputTokens
			agg.cost += b.EstimatedCost
			if b.ActualCost > 0 {
				agg.cost = b.ActualCost + agg.cost - b.EstimatedCost
			}
			agg.errors += b.ToolErrorCount
		}
		sort.Strings(dayOrder)

		sessions := make([]int, len(dayOrder))
		tokens := make([]int, len(dayOrder))
		costs := make([]float64, len(dayOrder))
		errors := make([]int, len(dayOrder))
		labels := make([]string, len(dayOrder))
		for i, key := range dayOrder {
			a := byDay[key]
			sessions[i] = a.sessions
			tokens[i] = a.tokens
			costs[i] = a.cost
			errors[i] = a.errors
			labels[i] = a.label
		}

		mu.Lock()
		data.HasSparklines = true
		data.SparklineSessions = buildSparklineBars(sessions, labels)
		data.SparklineTokens = buildSparklineBars(tokens, labels)
		data.SparklineCost = buildSparklineBarsFloat(costs, labels)
		data.SparklineErrors = buildSparklineBars(errors, labels)
		mu.Unlock()
	}()

	wg.Wait()

	// Default trend direction after all goroutines complete.
	if data.TrendDir == "" {
		data.TrendDir = "stable"
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

	// Compaction summary.
	CompactionCount   int
	TotalTokensLost   int
	TotalTokensLostK  string // formatted "170K"
	AvgDropPercent    float64
	SawtoothCycles    int
	TotalRebuildCost  float64
	AvgRecoveryTokens int
	AvgMessagesToFill int
	HasCompactions    bool // true if CompactionCount > 0

	// Overload detection (3.2).
	HasOverload      bool
	OverloadVerdict  string // "healthy", "declining", "overloaded"
	OverloadClass    string // CSS class: "good", "warning", "poor"
	OverloadScore    int    // 0-100 health score
	OverloadReason   string
	InflectionAt     int // message index where decline starts
	HasInflection    bool
	EarlyOutputRatio float64
	LateOutputRatio  float64
	OutputDecay      float64 // % decline
	EarlyErrorRate   float64
	LateErrorRate    float64
	ErrorGrowth      float64 // percentage points increase
	RetryCount       int
}

type saturationPointView struct {
	Index        int
	Role         string
	Tokens       int
	Percent      float64
	Zone         string // "optimal", "degraded", "critical"
	Delta        int
	Label        string
	BarWidth     int    // 0-100 for CSS width
	ZoneClass    string // CSS class
	IsCompaction bool   // true if this point is a compaction event
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

	// Populate compaction summary.
	cs := curve.Compactions
	view.CompactionCount = cs.TotalCompactions
	view.HasCompactions = cs.TotalCompactions > 0
	view.TotalTokensLost = cs.TotalTokensLost
	if cs.TotalTokensLost >= 1000 {
		view.TotalTokensLostK = fmt.Sprintf("%dK", cs.TotalTokensLost/1000)
	} else {
		view.TotalTokensLostK = fmt.Sprintf("%d", cs.TotalTokensLost)
	}
	view.AvgDropPercent = cs.AvgDropPercent
	view.SawtoothCycles = cs.SawtoothCycles
	view.TotalRebuildCost = cs.TotalRebuildCost
	view.AvgRecoveryTokens = cs.AvgRecoveryTokens
	view.AvgMessagesToFill = cs.AvgMessagesToFill

	// Build set of compaction point indices for marking.
	compactionAtIdx := make(map[int]bool, len(cs.Events))
	for _, e := range cs.Events {
		compactionAtIdx[e.AfterMessageIdx] = true
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
			Index:        pt.MessageIndex + 1,
			Role:         pt.Role,
			Tokens:       pt.InputTokens,
			Percent:      pt.Percent,
			Zone:         pt.Zone,
			Delta:        pt.Delta,
			Label:        pt.Label,
			BarWidth:     barWidth,
			ZoneClass:    zoneClass,
			IsCompaction: compactionAtIdx[pt.MessageIndex],
		})
	}

	// Populate overload detection (3.2).
	ol := curve.Overload
	if ol.TotalMessages >= 10 {
		view.HasOverload = true
		view.OverloadVerdict = ol.Verdict
		view.OverloadScore = ol.HealthScore
		view.OverloadReason = ol.Reason
		view.InflectionAt = ol.InflectionAt
		view.HasInflection = ol.InflectionAt > 0
		view.EarlyOutputRatio = ol.EarlyOutputRatio
		view.LateOutputRatio = ol.LateOutputRatio
		view.OutputDecay = ol.OutputRatioDecay
		view.EarlyErrorRate = ol.EarlyErrorRate
		view.LateErrorRate = ol.LateErrorRate
		view.ErrorGrowth = ol.ErrorRateGrowth
		view.RetryCount = ol.RetryCount
		switch ol.Verdict {
		case "healthy":
			view.OverloadClass = "good"
		case "declining":
			view.OverloadClass = "warning"
		case "overloaded":
			view.OverloadClass = "poor"
		}
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

// ── Settings Page ──

// settingItem is a single configuration key-value pair.
type settingItem struct {
	Key         string // display label
	Value       string // current value
	Description string // short explanation
	IsSecret    bool   // mask the value
	ConfigKey   string // config key for Set() (empty = read-only)
	InputType   string // "toggle", "text", "number", "select" (empty = read-only)
	Options     string // comma-separated options for select type
}

// settingSection groups related settings.
type settingSection struct {
	Title   string
	Icon    string
	Items   []settingItem
	IsEmpty bool
}

type settingsPage struct {
	Nav                string
	SidebarProjects    []sidebarProject
	Sections           []settingSection
	ProjectClassifiers []projectClassifierView
	ConfigPath         string // path to config file for reference
}

// ── Team Dashboard ──

type teamPage struct {
	Nav             string
	SidebarProjects []sidebarProject
	Period          string           // display label: "Today", "Yesterday", "This Week", "This Month"
	PeriodKey       string           // query param value: "today", "yesterday", "week", "month"
	Members         []teamMemberView // sorted by sessions DESC
	TotalSessions   int
	TotalTokens     int
	TotalErrors     int
	TeamSize        int
}

type teamMemberView struct {
	Name         string
	Email        string
	Kind         string // "human", "machine", "unknown"
	SessionCount int
	TotalTokens  int
	ErrorCount   int
	ErrorRate    float64 // errors per session
	Percent      int     // % of total team sessions (for progress bar)
}

func (s *Server) handleTeam(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse period from query: ?period=today|yesterday|week|month
	periodKey := r.URL.Query().Get("period")
	if periodKey == "" {
		periodKey = "today"
	}

	now := time.Now()
	var since, until time.Time
	var periodLabel string

	switch periodKey {
	case "yesterday":
		yesterday := now.AddDate(0, 0, -1)
		since = time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, now.Location())
		until = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		periodLabel = "Yesterday"
	case "week":
		// Go back to most recent Monday
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7 // Sunday
		}
		monday := now.AddDate(0, 0, -(weekday - 1))
		since = time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, now.Location())
		until = now
		periodLabel = "This Week"
	case "month":
		since = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		until = now
		periodLabel = "This Month"
	default: // "today"
		periodKey = "today"
		since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		until = now
		periodLabel = "Today"
	}

	data := teamPage{
		Nav:       "team",
		PeriodKey: periodKey,
		Period:    periodLabel,
	}

	data.SidebarProjects = s.buildSidebarProjects(ctx, "")

	// Get owner stats for the period.
	if s.store != nil {
		ownerStats, err := s.store.OwnerStats("", since, until)
		if err != nil {
			s.logger.Printf("[team] OwnerStats error: %v", err)
		}

		// Also get full user list for enrichment.
		users, _ := s.store.ListUsers()
		userMap := make(map[string]*session.User, len(users))
		for _, u := range users {
			userMap[string(u.ID)] = u
		}

		// Compute totals and build member views.
		for _, os := range ownerStats {
			data.TotalSessions += os.SessionCount
			data.TotalTokens += os.TotalTokens
			data.TotalErrors += os.ErrorCount
		}

		members := make([]teamMemberView, 0, len(ownerStats))
		for _, os := range ownerStats {
			mv := teamMemberView{
				Name:         os.OwnerName,
				Email:        os.OwnerEmail,
				Kind:         os.OwnerKind,
				SessionCount: os.SessionCount,
				TotalTokens:  os.TotalTokens,
				ErrorCount:   os.ErrorCount,
			}
			if os.SessionCount > 0 {
				mv.ErrorRate = float64(os.ErrorCount) / float64(os.SessionCount)
			}
			if data.TotalSessions > 0 {
				mv.Percent = os.SessionCount * 100 / data.TotalSessions
			}

			// Enrich with user data if available.
			if u, ok := userMap[string(os.OwnerID)]; ok {
				if mv.Name == "" {
					mv.Name = u.Name
				}
				if mv.Email == "" {
					mv.Email = u.Email
				}
				if mv.Kind == "" {
					mv.Kind = string(u.Kind)
				}
			}

			members = append(members, mv)
		}
		data.Members = members
		data.TeamSize = len(members)
	}

	s.render(w, "team.html", data)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	data := settingsPage{
		Nav:             "settings",
		SidebarProjects: s.buildSidebarProjects(r.Context(), ""),
	}

	if s.cfg == nil {
		data.Sections = []settingSection{{
			Title:   "Configuration",
			Icon:    "⚙",
			IsEmpty: true,
			Items:   []settingItem{{Key: "Status", Value: "No configuration loaded (using defaults)"}},
		}}
		s.render(w, "settings.html", data)
		return
	}

	data.ConfigPath = s.cfg.GlobalDir()

	// ── General ──
	general := settingSection{
		Title: "General",
		Icon:  "⚙",
		Items: []settingItem{
			{Key: "Storage Mode", Value: string(s.cfg.GetStorageMode()), Description: "How sessions are stored (compact, full)", ConfigKey: "storage_mode", InputType: "select", Options: "compact,full"},
			{Key: "Auto Capture", Value: boolStr(s.cfg.IsAutoCapture()), Description: "Automatically capture new sessions", ConfigKey: "auto_capture", InputType: "toggle"},
			{Key: "Providers", Value: joinProviders(s.cfg.GetProviders()), Description: "Enabled session providers"},
		},
	}

	// ── Features ──
	features := settingSection{
		Title: "Features",
		Icon:  "🚩",
		Items: []settingItem{
			{Key: "File Blame", Value: boolStr(s.cfg.IsFileBlameEnabled()), Description: "Extract file-level blame from tool calls (opt-in)", ConfigKey: "features.file_blame", InputType: "toggle"},
			{Key: "Telemetry", Value: boolStr(s.cfg.IsTelemetryEnabled()), Description: "Anonymous usage statistics", ConfigKey: "telemetry.enabled", InputType: "toggle"},
		},
	}

	// ── Search ──
	searchSec := settingSection{
		Title: "Search",
		Icon:  "🔍",
		Items: []settingItem{
			{Key: "Engine", Value: s.cfg.GetSearchEngine(), Description: "Primary search engine (like, fts5, elasticsearch, postgres)", ConfigKey: "search.engine", InputType: "select", Options: "like,fts5"},
			{Key: "Fallback", Value: s.cfg.GetSearchFallback(), Description: "Fallback engine when primary fails", ConfigKey: "search.fallback", InputType: "select", Options: "like,fts5,none"},
			{Key: "Index Content", Value: boolStr(s.cfg.GetSearchIndexContent()), Description: "Index full message content", ConfigKey: "search.index_content", InputType: "toggle"},
			{Key: "Max Content Length", Value: fmt.Sprintf("%d chars", s.cfg.GetSearchMaxContentLength()), Description: "Max indexed content per session"},
		},
	}

	// ── Analysis ──
	analysisSec := settingSection{
		Title: "Analysis (LLM)",
		Icon:  "🧠",
		Items: []settingItem{
			{Key: "Auto Analyze", Value: boolStr(s.cfg.IsAnalysisAutoEnabled()), Description: "Run analysis automatically after capture", ConfigKey: "analysis.auto", InputType: "toggle"},
			{Key: "Adapter", Value: s.cfg.GetAnalysisAdapter(), Description: "LLM adapter for analysis", ConfigKey: "analysis.adapter", InputType: "select", Options: "llm,opencode,ollama,anthropic"},
			{Key: "Model", Value: valueOrDefault(s.cfg.GetAnalysisModel(), "(default)"), Description: "Model override", ConfigKey: "analysis.model", InputType: "text"},
			{Key: "Error Threshold", Value: fmt.Sprintf("%.0f%%", s.cfg.GetAnalysisErrorThreshold()), Description: "Error rate threshold for flagging"},
			{Key: "Min Tool Calls", Value: fmt.Sprintf("%d", s.cfg.GetAnalysisMinToolCalls()), Description: "Minimum tool calls for analysis", ConfigKey: "analysis.min_tool_calls", InputType: "number"},
			{Key: "Schedule", Value: valueOrDefault(s.cfg.GetAnalysisSchedule(), "(disabled)"), Description: "Cron schedule for batch analysis", ConfigKey: "analysis.schedule", InputType: "text"},
		},
	}

	// ── Tagging ──
	taggingSec := settingSection{
		Title: "Tagging",
		Icon:  "🏷",
		Items: []settingItem{
			{Key: "Auto Tag", Value: boolStr(s.cfg.IsTaggingAutoEnabled()), Description: "Auto-classify session types after capture", ConfigKey: "tagging.auto", InputType: "toggle"},
			{Key: "Profile", Value: valueOrDefault(s.cfg.GetTaggingProfile(), "(default)"), Description: "LLM profile for classification", ConfigKey: "tagging.profile", InputType: "text"},
			{Key: "Custom Tags", Value: joinStrings(s.cfg.GetTaggingTags()), Description: "Custom tag vocabulary (empty = defaults)", ConfigKey: "tagging.tags", InputType: "text"},
		},
	}

	// ── Secrets ──
	secretsSec := settingSection{
		Title: "Secrets Detection",
		Icon:  "🔒",
		Items: []settingItem{
			{Key: "Mode", Value: string(s.cfg.GetSecretsMode()), Description: "How secrets are handled (mask, redact, none)", ConfigKey: "secrets.mode", InputType: "select", Options: "mask,redact,none"},
			{Key: "Custom Patterns", Value: fmt.Sprintf("%d patterns", len(s.cfg.GetCustomPatterns())), Description: "Additional regex patterns for secret detection"},
			{Key: "Scan Tool Outputs", Value: boolStr(s.cfg.IsScanToolOutputs()), Description: "Scan tool call outputs for secrets"},
		},
	}

	// ── Errors ──
	errorsSec := settingSection{
		Title: "Error Classification",
		Icon:  "⚠",
		Items: []settingItem{
			{Key: "Classifier", Value: s.cfg.GetErrorsClassifier(), Description: "Error classifier (deterministic, composite)", ConfigKey: "errors.classifier", InputType: "select", Options: "deterministic,composite"},
			{Key: "LLM Fallback", Value: boolStr(s.cfg.IsErrorsLLMFallbackEnabled()), Description: "Use LLM for unclassified errors", ConfigKey: "errors.llm_fallback", InputType: "toggle"},
			{Key: "LLM Schedule", Value: valueOrDefault(s.cfg.GetErrorsLLMSchedule(), "(disabled)"), Description: "Cron schedule for LLM reclassification", ConfigKey: "errors.llm_schedule", InputType: "text"},
		},
	}

	// ── Dashboard ──
	dashSec := settingSection{
		Title: "Dashboard",
		Icon:  "📊",
		Items: []settingItem{
			{Key: "Page Size", Value: fmt.Sprintf("%d", s.cfg.GetDashboardPageSize()), Description: "Sessions per page", ConfigKey: "dashboard.page_size", InputType: "number"},
			{Key: "Columns", Value: joinStrings(s.cfg.GetDashboardColumns()), Description: "Visible columns in sessions table", ConfigKey: "dashboard.columns", InputType: "text"},
			{Key: "Sort By", Value: s.cfg.GetDashboardSortBy(), Description: "Default sort field", ConfigKey: "dashboard.sort_by", InputType: "select", Options: "created_at,provider,branch,tokens,messages"},
			{Key: "Sort Order", Value: s.cfg.GetDashboardSortOrder(), Description: "Default sort direction", ConfigKey: "dashboard.sort_order", InputType: "select", Options: "asc,desc"},
		},
	}

	// ── Server ──
	serverSec := settingSection{
		Title: "Server",
		Icon:  "🖥",
		Items: []settingItem{
			{Key: "URL", Value: valueOrDefault(s.cfg.GetServerURL(), "(standalone)"), Description: "Server URL for daemon mode"},
			{Key: "Auth Enabled", Value: boolStr(s.cfg.IsAuthEnabled()), Description: "Require authentication"},
			{Key: "Database Path", Value: valueOrDefault(s.cfg.GetDatabasePath(), "(default)"), Description: "SQLite database location"},
			{Key: "Database Driver", Value: s.cfg.GetDatabaseDriver(), Description: "Storage driver"},
		},
	}

	// ── Scheduler ──
	schedulerSec := settingSection{
		Title: "Scheduler",
		Icon:  "⏰",
		Items: []settingItem{
			{Key: "GC Enabled", Value: boolStr(s.cfg.GetSchedulerGCEnabled()), Description: "Garbage collection for old sessions", ConfigKey: "scheduler.gc.enabled", InputType: "toggle"},
			{Key: "GC Schedule", Value: valueOrDefault(s.cfg.GetSchedulerGCCron(), "(disabled)"), Description: "GC cron expression", ConfigKey: "scheduler.gc.cron", InputType: "text"},
			{Key: "GC Retention", Value: fmt.Sprintf("%d days", s.cfg.GetSchedulerGCRetentionDays()), Description: "Keep sessions for N days", ConfigKey: "scheduler.gc.retention_days", InputType: "number"},
			{Key: "Capture All", Value: boolStr(s.cfg.GetSchedulerCaptureAllEnabled()), Description: "Scheduled capture of all providers", ConfigKey: "scheduler.capture_all.enabled", InputType: "toggle"},
			{Key: "Capture Schedule", Value: valueOrDefault(s.cfg.GetSchedulerCaptureAllCron(), "(disabled)"), Description: "Capture cron expression", ConfigKey: "scheduler.capture_all.cron", InputType: "text"},
			{Key: "Stats Report", Value: boolStr(s.cfg.GetSchedulerStatsReportEnabled()), Description: "Scheduled stats report", ConfigKey: "scheduler.stats_report.enabled", InputType: "toggle"},
			{Key: "Stats Schedule", Value: valueOrDefault(s.cfg.GetSchedulerStatsReportCron(), "(disabled)"), Description: "Stats report cron expression", ConfigKey: "scheduler.stats_report.cron", InputType: "text"},
		},
	}

	// ── Notifications ──
	notifSec := settingSection{
		Title: "Notifications",
		Icon:  "🔔",
		Items: []settingItem{
			// Integrations
			{Key: "Slack Webhook URL", Value: maskSecret(s.cfg.GetNotificationSlackWebhookURL(), 8), Description: "Incoming Webhook URL for channel posts", ConfigKey: "notification.slack.webhook_url", InputType: "text", IsSecret: true},
			{Key: "Slack Bot Token", Value: maskSecret(s.cfg.GetNotificationSlackBotToken(), 4), Description: "Bot token (xoxb-...) for DMs and multi-channel", ConfigKey: "notification.slack.bot_token", InputType: "text", IsSecret: true},
			{Key: "Webhook URL", Value: s.cfg.GetNotificationWebhookURL(), Description: "Generic webhook endpoint (HTTP POST)", ConfigKey: "notification.webhook.url", InputType: "text"},
			{Key: "Webhook Secret", Value: maskSecret(s.cfg.GetNotificationWebhookSecret(), 4), Description: "HMAC secret for webhook signatures", ConfigKey: "notification.webhook.secret", InputType: "text", IsSecret: true},
			// Routing
			{Key: "Default Channel", Value: valueOrDefault(s.cfg.GetNotificationDefaultChannel(), "(none)"), Description: "Default Slack channel for notifications", ConfigKey: "notification.default_channel", InputType: "text"},
			{Key: "Dashboard URL", Value: valueOrDefault(s.cfg.GetNotificationDashboardURL(), "(none)"), Description: "Base URL for 'View in Dashboard' links", ConfigKey: "notification.dashboard_url", InputType: "text"},
			// Alerts
			{Key: "Budget Alerts", Value: boolStr(s.cfg.IsNotificationAlertBudgetEnabled()), Description: "Alert when budget thresholds are reached", ConfigKey: "notification.alert_budget", InputType: "toggle"},
			{Key: "Error Spike Alerts", Value: boolStr(s.cfg.IsNotificationAlertErrorsEnabled()), Description: "Alert on error count spikes", ConfigKey: "notification.alert_errors", InputType: "toggle"},
			{Key: "Error Threshold", Value: fmt.Sprintf("%d", s.cfg.GetNotificationErrorThreshold()), Description: "Errors needed to trigger spike alert", ConfigKey: "notification.error_threshold", InputType: "number"},
			{Key: "Error Window", Value: fmt.Sprintf("%d min", s.cfg.GetNotificationErrorWindowMins()), Description: "Time window for spike detection", ConfigKey: "notification.error_window_mins", InputType: "number"},
			{Key: "Capture Alerts", Value: boolStr(s.cfg.IsNotificationAlertCaptureEnabled()), Description: "Notify on each session capture (noisy)", ConfigKey: "notification.alert_capture", InputType: "toggle"},
			// Digests
			{Key: "Daily Digest", Value: boolStr(s.cfg.IsNotificationDigestDailyEnabled()), Description: "Daily summary to channel", ConfigKey: "notification.digest_daily", InputType: "toggle"},
			{Key: "Daily Schedule", Value: s.cfg.GetNotificationDailyDigestCron(), Description: "Cron for daily digest", ConfigKey: "notification.daily_digest_cron", InputType: "text"},
			{Key: "Weekly Report", Value: boolStr(s.cfg.IsNotificationDigestWeeklyEnabled()), Description: "Weekly report to channel", ConfigKey: "notification.digest_weekly", InputType: "toggle"},
			{Key: "Weekly Schedule", Value: s.cfg.GetNotificationWeeklyReportCron(), Description: "Cron for weekly report", ConfigKey: "notification.weekly_report_cron", InputType: "text"},
			{Key: "Personal DMs", Value: boolStr(s.cfg.IsNotificationDigestPersonalEnabled()), Description: "Daily DM to each user (requires bot token)", ConfigKey: "notification.digest_personal", InputType: "toggle"},
		},
	}

	// ── Projects (per-project classifiers) — rendered via dedicated partial ──
	data.ProjectClassifiers = buildProjectClassifierViews(s.cfg.GetAllProjectClassifiers(), s.cfg.GetNotificationProjectChannels())

	data.Sections = []settingSection{
		general,
		features,
		searchSec,
		analysisSec,
		taggingSec,
		secretsSec,
		errorsSec,
		dashSec,
		serverSec,
		schedulerSec,
		notifSec,
	}

	s.render(w, "settings.html", data)
}

// boolStr returns "enabled" or "disabled" for a bool value.
func boolStr(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled"
}

// valueOrDefault returns val if non-empty, otherwise def.
func valueOrDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}

// maskSecret masks a secret value, showing only the last N characters.
// Returns "(not set)" if the value is empty.
func maskSecret(val string, showLast int) string {
	if val == "" {
		return "(not set)"
	}
	if len(val) <= showLast {
		return "****"
	}
	return "****" + val[len(val)-showLast:]
}

// joinProviders converts a slice of ProviderName to a comma-separated string.
func joinProviders(providers []session.ProviderName) string {
	parts := make([]string, len(providers))
	for i, p := range providers {
		parts[i] = string(p)
	}
	return strings.Join(parts, ", ")
}

// joinStrings joins a string slice for display.
func joinStrings(ss []string) string {
	if len(ss) == 0 {
		return "(default)"
	}
	return strings.Join(ss, ", ")
}

// handleSettingsUpdate handles POST /api/settings to update a single config key.
// Returns an HTMX partial with the updated setting row.
func (s *Server) handleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	if s.cfg == nil {
		http.Error(w, "no config loaded", http.StatusServiceUnavailable)
		return
	}

	key := r.FormValue("key")
	value := r.FormValue("value")

	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}

	// For toggle inputs, the form sends "true"/"false" — but some config keys
	// expect "true"/"false" while the display shows "enabled"/"disabled".
	if err := s.cfg.Set(key, value); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `<span class="text-error">%s</span>`, template.HTMLEscapeString(err.Error()))
		return
	}

	if err := s.cfg.Save(); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `<span class="text-error">save failed: %s</span>`, template.HTMLEscapeString(err.Error()))
		return
	}

	// Return the updated value display.
	displayValue := value
	if value == "true" {
		displayValue = "enabled"
	} else if value == "false" {
		displayValue = "disabled"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	cssClass := "font-mono settings-value"
	if displayValue == "enabled" {
		cssClass += " settings-value--enabled"
	} else if displayValue == "disabled" {
		cssClass += " settings-value--disabled"
	}
	fmt.Fprintf(w, `<span class="%s">%s</span> <span class="settings-saved-indicator">saved</span>`, cssClass, template.HTMLEscapeString(displayValue))
}

// ── Project Classifier CRUD ──────────────────────────────────────────────

// projectClassifierView is the view model for a single project classifier card.
type projectClassifierView struct {
	Name          string
	TicketPattern string
	TicketSource  string
	TicketURL     string
	BranchRules   []ruleView // sorted for stable rendering
	AgentRules    []ruleView
	CommitRules   []ruleView
	StatusRules   []ruleView
	Tags          string // comma-separated
	HasBudget     bool
	MonthlyLimit  string
	DailyLimit    string
	AlertPercent  string
	CostMode      string
	AlertWebhook  string
	NotifChannel  string // per-project Slack channel override (e.g. "#backend-ai")
}

type ruleView struct {
	Pattern string // the key (branch glob, agent name, commit prefix, summary prefix)
	Type    string // the value (session type or status)
}

// buildProjectClassifierViews builds sorted view models from config.
func buildProjectClassifierViews(projects map[string]config.ProjectClassifierConf, projectChannels map[string]string) []projectClassifierView {
	if len(projects) == 0 {
		return nil
	}
	names := make([]string, 0, len(projects))
	for n := range projects {
		names = append(names, n)
	}
	sort.Strings(names)

	views := make([]projectClassifierView, 0, len(names))
	for _, name := range names {
		pc := projects[name]
		v := projectClassifierView{
			Name:          name,
			TicketPattern: pc.TicketPattern,
			TicketSource:  pc.TicketSource,
			TicketURL:     pc.TicketURL,
			Tags:          strings.Join(pc.Tags, ", "),
		}
		v.BranchRules = mapToRuleViews(pc.BranchRules)
		v.AgentRules = mapToRuleViews(pc.AgentRules)
		v.CommitRules = mapToRuleViews(pc.CommitRules)
		v.StatusRules = mapToRuleViews(pc.StatusRules)
		if pc.Budget != nil {
			v.HasBudget = true
			v.MonthlyLimit = formatBudgetValue(pc.Budget.MonthlyLimit)
			v.DailyLimit = formatBudgetValue(pc.Budget.DailyLimit)
			v.AlertPercent = formatBudgetValue(pc.Budget.AlertAtPercent)
			v.CostMode = pc.Budget.CostMode
			if v.CostMode == "" {
				v.CostMode = "actual"
			}
			v.AlertWebhook = pc.Budget.AlertWebhook
		}
		if projectChannels != nil {
			v.NotifChannel = projectChannels[name]
		}
		views = append(views, v)
	}
	return views
}

func mapToRuleViews(m map[string]string) []ruleView {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	views := make([]ruleView, 0, len(keys))
	for _, k := range keys {
		views = append(views, ruleView{Pattern: k, Type: m[k]})
	}
	return views
}

func formatBudgetValue(v float64) string {
	if v == 0 {
		return ""
	}
	if v == float64(int(v)) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

// handleProjectClassifierSave handles POST /api/settings/project — create/update a project classifier.
// Returns the HTMX partial for the updated project classifiers section.
func (s *Server) handleProjectClassifierSave(w http.ResponseWriter, r *http.Request) {
	if s.cfg == nil {
		http.Error(w, "no config loaded", http.StatusServiceUnavailable)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `<span class="text-error">project name is required</span>`)
		return
	}

	pc := config.ProjectClassifierConf{
		TicketPattern: strings.TrimSpace(r.FormValue("ticket_pattern")),
		TicketSource:  strings.TrimSpace(r.FormValue("ticket_source")),
		TicketURL:     strings.TrimSpace(r.FormValue("ticket_url")),
	}

	// Parse tags.
	if tags := strings.TrimSpace(r.FormValue("tags")); tags != "" {
		var parsed []string
		for _, t := range strings.Split(tags, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				parsed = append(parsed, t)
			}
		}
		pc.Tags = parsed
	}

	// Parse rule maps from repeated form fields: branch_rule_pattern[], branch_rule_type[].
	pc.BranchRules = parseRulesFromForm(r, "branch_rule")
	pc.AgentRules = parseRulesFromForm(r, "agent_rule")
	pc.CommitRules = parseRulesFromForm(r, "commit_rule")
	pc.StatusRules = parseRulesFromForm(r, "status_rule")

	// Parse budget.
	monthlyStr := strings.TrimSpace(r.FormValue("budget_monthly"))
	dailyStr := strings.TrimSpace(r.FormValue("budget_daily"))
	alertStr := strings.TrimSpace(r.FormValue("budget_alert_percent"))
	costMode := strings.TrimSpace(r.FormValue("budget_cost_mode"))
	alertWebhook := strings.TrimSpace(r.FormValue("budget_alert_webhook"))

	if monthlyStr != "" || dailyStr != "" || alertStr != "" || costMode != "" || alertWebhook != "" {
		pc.Budget = &config.ProjectBudgetConf{}
		if monthlyStr != "" {
			v, err := strconv.ParseFloat(monthlyStr, 64)
			if err != nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, `<span class="text-error">invalid monthly limit: %s</span>`, template.HTMLEscapeString(err.Error()))
				return
			}
			pc.Budget.MonthlyLimit = v
		}
		if dailyStr != "" {
			v, err := strconv.ParseFloat(dailyStr, 64)
			if err != nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, `<span class="text-error">invalid daily limit: %s</span>`, template.HTMLEscapeString(err.Error()))
				return
			}
			pc.Budget.DailyLimit = v
		}
		if alertStr != "" {
			v, err := strconv.ParseFloat(alertStr, 64)
			if err != nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, `<span class="text-error">invalid alert percent: %s</span>`, template.HTMLEscapeString(err.Error()))
				return
			}
			pc.Budget.AlertAtPercent = v
		}
		pc.Budget.CostMode = costMode
		pc.Budget.AlertWebhook = alertWebhook
	}

	if err := s.cfg.SetProjectClassifier(name, pc); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `<span class="text-error">%s</span>`, template.HTMLEscapeString(err.Error()))
		return
	}

	// Save notification channel override for this project.
	notifChannel := strings.TrimSpace(r.FormValue("notif_channel"))
	s.cfg.SetNotificationProjectChannel(name, notifChannel)

	if err := s.cfg.Save(); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `<span class="text-error">save failed: %s</span>`, template.HTMLEscapeString(err.Error()))
		return
	}

	// Return updated project classifiers section.
	s.renderProjectClassifiersPartial(w)
}

// handleProjectClassifierDelete handles DELETE /api/settings/project — remove a project classifier.
// Note: Go's ParseForm only reads body for POST/PUT/PATCH, so we parse manually for DELETE.
func (s *Server) handleProjectClassifierDelete(w http.ResponseWriter, r *http.Request) {
	if s.cfg == nil {
		http.Error(w, "no config loaded", http.StatusServiceUnavailable)
		return
	}

	// For DELETE, manually parse the URL-encoded body since Go only parses body for POST/PUT/PATCH.
	name := r.URL.Query().Get("name")
	if name == "" {
		// Try body parsing for HTMX hx-vals (sends as form-encoded body).
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		if len(body) > 0 {
			vals, err := url.ParseQuery(string(body))
			if err == nil {
				name = vals.Get("name")
			}
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `<span class="text-error">project name is required</span>`)
		return
	}

	if err := s.cfg.DeleteProjectClassifier(name); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `<span class="text-error">%s</span>`, template.HTMLEscapeString(err.Error()))
		return
	}
	if err := s.cfg.Save(); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `<span class="text-error">save failed: %s</span>`, template.HTMLEscapeString(err.Error()))
		return
	}

	s.renderProjectClassifiersPartial(w)
}

// renderProjectClassifiersPartial renders the project-classifiers partial
// and writes it to the response. Used by both save and delete handlers.
func (s *Server) renderProjectClassifiersPartial(w http.ResponseWriter) {
	projects := s.cfg.GetAllProjectClassifiers()
	views := buildProjectClassifierViews(projects, s.cfg.GetNotificationProjectChannels())

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.partials.ExecuteTemplate(w, "project-classifiers", views); err != nil {
		s.logger.Printf("render project-classifiers partial: %v", err)
	}
}

// ── Classification Rules Live Preview ─────────────────────────────────────

// classificationPreviewRow is a single session row in the preview results.
type classificationPreviewRow struct {
	ID            string
	Summary       string
	Branch        string
	Agent         string
	CurrentType   string
	NewType       string
	TypeChanged   bool
	CurrentStatus string
	NewStatus     string
	StatusChanged bool
}

// classificationPreviewData is passed to the classification_preview_partial template.
type classificationPreviewData struct {
	Rows          []classificationPreviewRow
	Total         int
	Matched       int
	TypeChanges   int
	StatusChanges int
}

// handleClassificationPreview handles POST /api/settings/project/preview — dry-run classification.
// It parses proposed rules from the form, queries sessions for the project, and returns
// an HTMX partial showing how sessions would be reclassified.
func (s *Server) handleClassificationPreview(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "no store", http.StatusServiceUnavailable)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	projectName := strings.TrimSpace(r.FormValue("name"))
	if projectName == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `<span class="text-error">project name is required for preview</span>`)
		return
	}

	// Parse proposed rules from form (same parsing as the save handler).
	branchRules := parseRulesFromForm(r, "branch_rule")
	agentRules := parseRulesFromForm(r, "agent_rule")
	commitRules := parseRulesFromForm(r, "commit_rule")
	statusRules := parseRulesFromForm(r, "status_rule")

	// Fall back to defaults when the form has no rules for a category.
	if len(commitRules) == 0 {
		commitRules = config.DefaultCommitRules
	}
	if len(agentRules) == 0 {
		agentRules = config.DefaultAgentRules
	}
	if len(statusRules) == 0 {
		statusRules = config.DefaultStatusRules
	}

	// Fetch all sessions and filter to those matching the project name.
	allSessions, err := s.store.List(session.ListOptions{All: true})
	if err != nil {
		s.logger.Printf("[preview] error listing sessions: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var matching []session.Summary
	for _, sm := range allSessions {
		if sessionMatchesProject(sm, projectName) {
			matching = append(matching, sm)
		}
	}

	// Apply proposed rules to each matching session (dry-run, no persistence).
	const maxPreviewRows = 50
	data := classificationPreviewData{
		Total: len(matching),
	}
	for i, sm := range matching {
		if i >= maxPreviewRows {
			break
		}
		row := classifySessionPreview(sm, branchRules, agentRules, commitRules, statusRules)
		if row.TypeChanged || row.StatusChanged {
			data.Matched++
		}
		if row.TypeChanged {
			data.TypeChanges++
		}
		if row.StatusChanged {
			data.StatusChanges++
		}
		data.Rows = append(data.Rows, row)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.partials.ExecuteTemplate(w, "classification-preview", data); err != nil {
		s.logger.Printf("render classification-preview partial: %v", err)
	}
}

// sessionMatchesProject checks if a session summary belongs to a project name.
// The project name can match: the remote URL display name, the raw remote URL,
// the project path, or the basename of the project path.
func sessionMatchesProject(sm session.Summary, projectName string) bool {
	if projectName == "" {
		return false
	}
	displayName := config.RemoteDisplayName(sm.RemoteURL)
	candidates := []string{displayName, sm.RemoteURL, sm.ProjectPath}
	if sm.ProjectPath != "" {
		candidates = append(candidates, filepath.Base(sm.ProjectPath))
	}
	for _, c := range candidates {
		if c != "" && c == projectName {
			return true
		}
	}
	return false
}

// classifySessionPreview applies proposed rules to a session summary in dry-run mode
// using the same priority cascade as the real classifier:
//  1. Conventional Commit prefix in summary (highest priority)
//  2. Branch name rules
//  3. Agent name rules (lowest priority)
func classifySessionPreview(sm session.Summary, branchRules, agentRules, commitRules, statusRules map[string]string) classificationPreviewRow {
	row := classificationPreviewRow{
		ID:            string(sm.ID),
		Summary:       sm.Summary,
		Branch:        sm.Branch,
		Agent:         sm.Agent,
		CurrentType:   sm.SessionType,
		CurrentStatus: string(sm.Status),
	}
	if len(row.ID) > 12 {
		row.ID = row.ID[:12]
	}
	if len(row.Summary) > 60 {
		row.Summary = row.Summary[:57] + "..."
	}

	// Priority 1: Conventional Commit prefix.
	newType := service.MatchConventionalCommit(commitRules, sm.Summary)
	// Priority 2: Branch rules.
	if newType == "" && len(branchRules) > 0 && sm.Branch != "" {
		newType = service.MatchBranchRule(branchRules, sm.Branch)
	}
	// Priority 3: Agent rules.
	if newType == "" && sm.Agent != "" {
		if t, ok := agentRules[sm.Agent]; ok {
			newType = t
		}
	}

	row.NewType = newType
	row.TypeChanged = newType != "" && newType != sm.SessionType

	// Status from summary prefix.
	newStatus := service.MatchSummaryPrefix(statusRules, sm.Summary)
	row.NewStatus = newStatus
	row.StatusChanged = newStatus != "" && newStatus != string(sm.Status)

	return row
}

// parseRulesFromForm reads repeated form fields like "prefix_pattern[]" and "prefix_type[]"
// and returns a map. Empty patterns are skipped.
func parseRulesFromForm(r *http.Request, prefix string) map[string]string {
	patterns := r.Form[prefix+"_pattern[]"]
	types := r.Form[prefix+"_type[]"]
	if len(patterns) == 0 {
		return nil
	}
	m := make(map[string]string)
	for i, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		t := ""
		if i < len(types) {
			t = strings.TrimSpace(types[i])
		}
		if t != "" {
			m[p] = t
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// ── Session Timeline Partial ─────────────────────────────────────────────

// timelineData is the top-level data passed to session_timeline_partial.html.
type timelineData struct {
	SessionID string

	// Time axis metadata.
	StartTime  time.Time
	EndTime    time.Time
	DurationMs int64 // total session duration in ms

	// Per-message entries on the Gantt chart.
	Messages []timelineMessage

	// Dependency graph: parent → this → children/forks.
	HasGraph     bool
	GraphEntries []timelineGraphEntry

	// Saturation overlay.
	HasSaturation   bool
	HasCompactions  bool
	HasOverload     bool
	InflectionAt    int // message index (1-based, 0 = none)
	InflectionPct   float64
	OverloadVerdict string
	OverloadScore   int
}

// timelineMessage represents one message bar on the Gantt chart.
type timelineMessage struct {
	Index     int    // 0-based
	IndexHum  int    // 1-based for display
	Role      string // "user", "assistant", "system", "tool"
	RoleClass string // CSS class: "tl-user", "tl-assistant", "tl-system", "tl-tool"

	// Position on the timeline (percentage of total duration).
	OffsetPct float64
	WidthPct  float64

	// Token info.
	InputTokens  string
	OutputTokens string
	Tokens       string // combined display

	// Saturation zone (if available).
	Zone      string // "optimal", "degraded", "critical", ""
	ZoneClass string // "tl-zone-optimal", etc.
	Percent   float64

	// Tool calls (sub-bars).
	HasTools bool
	Tools    []timelineTool

	// Error flag.
	HasError bool

	// Compaction marker.
	IsCompaction bool

	// Fork point marker.
	IsForkPoint bool
}

// timelineTool represents a tool call sub-bar within a message.
type timelineTool struct {
	Name       string
	State      string // "completed", "error", "pending", "running"
	StateIcon  string // "✅", "❌", "⏳", "⏳"
	StateClass string // "tl-tool-ok", "tl-tool-err", "tl-tool-pending"
	DurationMs int
}

// timelineGraphEntry represents a node in the dependency mini-tree.
type timelineGraphEntry struct {
	SessionID string
	Label     string // truncated summary or ID
	Relation  string // "parent", "self", "child", "fork"
	Class     string // "tl-graph-parent", "tl-graph-self", etc.
	Link      string // URL to session detail
}

// handleSessionTimelinePartial renders the timeline Gantt chart for a session.
func (s *Server) handleSessionTimelinePartial(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		w.Write([]byte(`<div class="text-muted" style="padding:1rem;">Timeline unavailable.</div>`))
		return
	}

	sess, err := s.sessionSvc.Get(id)
	if err != nil || len(sess.Messages) == 0 {
		w.Write([]byte(`<div class="text-muted" style="padding:1rem;">Timeline unavailable — no messages.</div>`))
		return
	}

	// Load saturation curve (optional — may fail for short sessions).
	curve, _ := s.sessionSvc.SessionSaturationCurve(r.Context(), sess.ID)

	// Build saturation index: msgIndex → SaturationPoint.
	satMap := make(map[int]*session.SaturationPoint)
	compactionSet := make(map[int]bool)
	if curve != nil && len(curve.Points) > 0 {
		for i := range curve.Points {
			satMap[curve.Points[i].MessageIndex] = &curve.Points[i]
		}
		for _, e := range curve.Compactions.Events {
			compactionSet[e.AfterMessageIdx] = true
		}
	}

	// Determine time bounds.
	startTime := sess.Messages[0].Timestamp
	endTime := sess.Messages[len(sess.Messages)-1].Timestamp
	totalDur := endTime.Sub(startTime)
	if totalDur <= 0 {
		totalDur = time.Second // prevent div-by-zero for single-message sessions
	}

	data := timelineData{
		SessionID:  id,
		StartTime:  startTime,
		EndTime:    endTime,
		DurationMs: totalDur.Milliseconds(),
	}

	// Fork point detection: if this session is a fork, mark the ForkedAtMessage.
	forkPoint := sess.ForkedAtMessage // 1-based, 0 means not a fork

	// Build message bars.
	for i := range sess.Messages {
		msg := &sess.Messages[i]
		offset := msg.Timestamp.Sub(startTime)
		offsetPct := float64(offset) / float64(totalDur) * 100
		if offsetPct < 0 {
			offsetPct = 0
		}
		if offsetPct > 100 {
			offsetPct = 100
		}

		// Width: use gap to next message, or min 0.5%.
		var widthPct float64
		if i < len(sess.Messages)-1 {
			gap := sess.Messages[i+1].Timestamp.Sub(msg.Timestamp)
			widthPct = float64(gap) / float64(totalDur) * 100
		} else {
			widthPct = 100 - offsetPct
		}
		if widthPct < 0.5 {
			widthPct = 0.5
		}
		if offsetPct+widthPct > 100 {
			widthPct = 100 - offsetPct
		}

		// Role class.
		role := string(msg.Role)
		roleClass := "tl-assistant"
		switch msg.Role {
		case session.RoleUser:
			roleClass = "tl-user"
		case session.RoleSystem:
			roleClass = "tl-system"
		}

		tm := timelineMessage{
			Index:        i,
			IndexHum:     i + 1,
			Role:         role,
			RoleClass:    roleClass,
			OffsetPct:    offsetPct,
			WidthPct:     widthPct,
			InputTokens:  formatTokens(msg.InputTokens),
			OutputTokens: formatTokens(msg.OutputTokens),
			Tokens:       formatTokens(msg.InputTokens + msg.OutputTokens),
			IsCompaction: compactionSet[i],
			IsForkPoint:  forkPoint > 0 && i+1 == forkPoint,
		}

		// Saturation overlay.
		if pt, ok := satMap[i]; ok {
			tm.Zone = pt.Zone
			tm.Percent = pt.Percent
			switch pt.Zone {
			case "degraded":
				tm.ZoneClass = "tl-zone-degraded"
			case "critical":
				tm.ZoneClass = "tl-zone-critical"
			default:
				tm.ZoneClass = "tl-zone-optimal"
			}
		}

		// Tool calls.
		for _, tc := range msg.ToolCalls {
			stateIcon := "✅"
			stateClass := "tl-tool-ok"
			switch tc.State {
			case session.ToolStateError:
				stateIcon = "❌"
				stateClass = "tl-tool-err"
				tm.HasError = true
			case session.ToolStatePending:
				stateIcon = "⏳"
				stateClass = "tl-tool-pending"
			case session.ToolStateRunning:
				stateIcon = "⏳"
				stateClass = "tl-tool-pending"
			}
			tm.Tools = append(tm.Tools, timelineTool{
				Name:       tc.Name,
				State:      string(tc.State),
				StateIcon:  stateIcon,
				StateClass: stateClass,
				DurationMs: tc.DurationMs,
			})
		}
		tm.HasTools = len(tm.Tools) > 0

		data.Messages = append(data.Messages, tm)
	}

	// Saturation summary.
	if curve != nil {
		data.HasSaturation = len(curve.Points) > 0
		data.HasCompactions = curve.Compactions.TotalCompactions > 0
		if curve.Overload.TotalMessages >= 10 {
			data.HasOverload = true
			data.OverloadVerdict = curve.Overload.Verdict
			data.OverloadScore = curve.Overload.HealthScore
			if curve.Overload.InflectionAt > 0 {
				data.InflectionAt = curve.Overload.InflectionAt + 1 // 1-based
				// Calculate inflection position as percentage.
				if curve.Overload.InflectionAt < len(data.Messages) {
					data.InflectionPct = data.Messages[curve.Overload.InflectionAt].OffsetPct
				}
			}
		}
	}

	// Build dependency graph.
	data.GraphEntries = s.buildTimelineGraph(sess)
	data.HasGraph = len(data.GraphEntries) > 1 // only show if there's more than just "self"

	s.renderPartial(w, "session_timeline_partial.html", data)
}

// buildTimelineGraph creates the mini dependency tree for the session.
func (s *Server) buildTimelineGraph(sess *session.Session) []timelineGraphEntry {
	var entries []timelineGraphEntry

	// Parent (if any).
	if sess.ParentID != "" {
		label := string(sess.ParentID)
		if len(label) > 12 {
			label = label[:12]
		}
		entries = append(entries, timelineGraphEntry{
			SessionID: string(sess.ParentID),
			Label:     label,
			Relation:  "parent",
			Class:     "tl-graph-parent",
			Link:      "/sessions/" + string(sess.ParentID),
		})
	}

	// Self.
	selfLabel := string(sess.ID)
	if len(selfLabel) > 12 {
		selfLabel = selfLabel[:12]
	}
	entries = append(entries, timelineGraphEntry{
		SessionID: string(sess.ID),
		Label:     selfLabel,
		Relation:  "self",
		Class:     "tl-graph-self",
		Link:      "/sessions/" + string(sess.ID),
	})

	// Children.
	for _, child := range sess.Children {
		childLabel := string(child.ID)
		if len(childLabel) > 12 {
			childLabel = childLabel[:12]
		}
		entries = append(entries, timelineGraphEntry{
			SessionID: string(child.ID),
			Label:     childLabel,
			Relation:  "child",
			Class:     "tl-graph-child",
			Link:      "/sessions/" + string(child.ID),
		})
	}

	// Fork relations.
	if s.store != nil {
		forkRels, err := s.store.GetForkRelations(sess.ID)
		if err == nil {
			for _, rel := range forkRels {
				var linkedID session.ID
				relation := "fork"
				if rel.OriginalID == sess.ID {
					linkedID = rel.ForkID
				} else {
					linkedID = rel.OriginalID
					relation = "parent" // already shown above? skip duplicates
				}
				// Avoid duplicating parent/children already shown.
				dup := false
				for _, e := range entries {
					if e.SessionID == string(linkedID) {
						dup = true
						break
					}
				}
				if dup {
					continue
				}
				fLabel := string(linkedID)
				if len(fLabel) > 12 {
					fLabel = fLabel[:12]
				}
				cssClass := "tl-graph-fork"
				if relation == "parent" {
					cssClass = "tl-graph-parent"
				}
				entries = append(entries, timelineGraphEntry{
					SessionID: string(linkedID),
					Label:     fLabel,
					Relation:  relation,
					Class:     cssClass,
					Link:      "/sessions/" + string(linkedID),
				})
			}
		}
	}

	return entries
}

// ── Pull Requests page ──

type pullsPage struct {
	Nav                 string
	SidebarProjects     []sidebarProject
	PRs                 []prView
	HasPRs              bool
	NoPlatform          bool
	StateFilter         string
	TotalPRs            int
	TotalLinkedSessions int
	TotalTokensStr      string
}

type prView struct {
	Number          int
	Title           string
	Branch          string
	BaseBranch      string
	State           string
	StateIcon       string
	Author          string
	URL             string
	Additions       int
	Deletions       int
	Comments        int
	TimeAgo         string
	SessionCount    int
	TokensStr       string
	HasSessions     bool
	Sessions        []prSessionView
	LatestSessionID string // ID of the most recent session (for restore shortcut)
}

type prSessionView struct {
	ID           string
	IDShort      string
	Summary      string
	Agent        string
	SessionType  string
	MessageCount int
	TokensStr    string
	TimeAgo      string
	IsLatest     bool // true for the most recent session (first in list)
}

// handlePulls renders the /pulls page with PR ↔ session associations.
func (s *Server) handlePulls(w http.ResponseWriter, r *http.Request) {
	stateFilter := r.URL.Query().Get("state")

	data := pullsPage{
		Nav:             "pulls",
		SidebarProjects: s.buildSidebarProjects(r.Context(), ""),
		StateFilter:     stateFilter,
	}

	if s.store == nil {
		data.NoPlatform = true
		s.render(w, "pulls.html", data)
		return
	}

	// Check if any PRs exist in the store.
	prs, err := s.store.ListPRsWithSessions("", "", stateFilter, 100)
	if err != nil {
		s.logger.Printf("[pulls] error listing PRs: %v", err)
	}

	if len(prs) == 0 {
		// If no PRs found, check if sync has ever run by checking if table has any PRs at all.
		allPRs, _ := s.store.ListPullRequests("", "", "", 1)
		if len(allPRs) == 0 {
			data.NoPlatform = true
		}
		s.render(w, "pulls.html", data)
		return
	}

	var totalLinked, totalTokens int
	for _, prws := range prs {
		var sessions []prSessionView
		for i, sm := range prws.Sessions {
			sessions = append(sessions, prSessionView{
				ID:           string(sm.ID),
				IDShort:      truncateID(string(sm.ID), 12),
				Summary:      sm.Summary,
				Agent:        string(sm.Agent),
				SessionType:  sm.SessionType,
				MessageCount: sm.MessageCount,
				TokensStr:    formatTokens(sm.TotalTokens),
				TimeAgo:      timeAgoString(sm.CreatedAt),
				IsLatest:     i == 0, // sessions are sorted by created_at DESC
			})
		}

		totalLinked += prws.SessionCount
		totalTokens += prws.TotalTokens

		stateIcon := "&#x1F7E2;" // green circle
		switch prws.PR.State {
		case "merged":
			stateIcon = "&#x1F7E3;" // purple circle
		case "closed":
			stateIcon = "&#x1F534;" // red circle
		}

		data.PRs = append(data.PRs, prView{
			Number:       prws.PR.Number,
			Title:        prws.PR.Title,
			Branch:       prws.PR.Branch,
			BaseBranch:   prws.PR.BaseBranch,
			State:        prws.PR.State,
			StateIcon:    stateIcon,
			Author:       prws.PR.Author,
			URL:          prws.PR.URL,
			Additions:    prws.PR.Additions,
			Deletions:    prws.PR.Deletions,
			Comments:     prws.PR.Comments,
			TimeAgo:      timeAgoString(prws.PR.UpdatedAt),
			SessionCount: prws.SessionCount,
			TokensStr:    formatTokens(prws.TotalTokens),
			HasSessions:  prws.SessionCount > 0,
			Sessions:     sessions,
			LatestSessionID: func() string {
				if len(prws.Sessions) > 0 {
					return string(prws.Sessions[0].ID)
				}
				return ""
			}(),
		})
	}

	data.HasPRs = len(data.PRs) > 0
	data.TotalPRs = len(data.PRs)
	data.TotalLinkedSessions = totalLinked
	data.TotalTokensStr = formatTokens(totalTokens)

	s.render(w, "pulls.html", data)
}

// ── Recommendation API Handlers ──

// handleRecommendationDismiss handles POST /api/recommendations/dismiss.
// Expects form field "id" with the recommendation record ID.
func (s *Server) handleRecommendationDismiss(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "store unavailable", http.StatusServiceUnavailable)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	id := r.FormValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	if err := s.store.DismissRecommendation(id); err != nil {
		http.Error(w, "dismiss failed", http.StatusInternalServerError)
		return
	}

	// Return empty 200 — HTMX will remove the element.
	w.WriteHeader(http.StatusOK)
}

// handleRecommendationSnooze handles POST /api/recommendations/snooze.
// Expects form fields "id" and optional "days" (default: 7).
func (s *Server) handleRecommendationSnooze(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "store unavailable", http.StatusServiceUnavailable)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	id := r.FormValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	days := 7
	if d := r.FormValue("days"); d != "" {
		if v, err := strconv.Atoi(d); err == nil && v > 0 && v <= 90 {
			days = v
		}
	}

	until := time.Now().AddDate(0, 0, days)
	if err := s.store.SnoozeRecommendation(id, until); err != nil {
		http.Error(w, "snooze failed", http.StatusInternalServerError)
		return
	}

	// Return empty 200 — HTMX will remove the element.
	w.WriteHeader(http.StatusOK)
}

// ── Session Graph ──

// sessionGraphPage is the view model for the interactive session graph page.
type sessionGraphPage struct {
	Nav             string
	SidebarProjects []sidebarProject
	Mode            string // "pr", "project", "branch"
	HasGroups       bool
	Groups          []sgGroup
	Stats           sgStats
}

// sgStats holds aggregate stats for the graph.
type sgStats struct {
	TotalGroups   int
	TotalSessions int
	TotalForks    int
	TotalLinks    int
}

// sgGroup is a top-level grouping node (PR, project, or branch).
type sgGroup struct {
	Title        string
	Subtitle     string
	Icon         string
	State        string // PR state: "open", "merged", "closed" (empty for non-PR)
	SearchName   string // lowercase searchable text
	SessionCount int
	TokensStr    string
	Nodes        []sgNode
}

// sgNode is a session node in the tree.
type sgNode struct {
	ID            string
	IDShort       string
	Summary       string
	Agent         string
	Provider      string
	SessionType   string
	Status        string
	Messages      int
	TokensStr     string
	Errors        int
	TimeAgo       string
	IsFork        bool
	Depth         int
	IndentPx      int // Depth * 24
	SummaryIndent int // indent for summary line
	HasChildren   bool
	IsCollapsed   bool
	Children      []sgNode
	SearchText    string    // for JS filtering
	LinkBadges    []sgBadge // delegation/continuation badges
}

// sgBadge represents a link type badge on a node.
type sgBadge struct {
	Type  string // CSS modifier: "delegation", "continuation", "related"
	Label string // display text
}

// handleSessionGraph renders the interactive session graph page.
func (s *Server) handleSessionGraph(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "project"
	}

	projectFilter := r.URL.Query().Get("project")

	data := sessionGraphPage{
		Nav:             "graph",
		SidebarProjects: s.buildSidebarProjects(r.Context(), projectFilter),
		Mode:            mode,
	}

	if s.store == nil {
		s.render(w, "session_graph.html", data)
		return
	}

	switch mode {
	case "pr":
		s.buildGraphByPR(&data, projectFilter)
	case "project":
		s.buildGraphByProject(&data, projectFilter)
	case "branch":
		s.buildGraphByBranch(&data, projectFilter)
	default:
		s.buildGraphByPR(&data, projectFilter)
	}

	data.HasGroups = len(data.Groups) > 0
	s.render(w, "session_graph.html", data)
}

// buildGraphByPR groups sessions by pull request.
func (s *Server) buildGraphByPR(data *sessionGraphPage, projectFilter string) {
	prs, err := s.store.ListPRsWithSessions("", "", "", 50)
	if err != nil {
		s.logger.Printf("[graph] error listing PRs: %v", err)
		return
	}

	var totalSessions, totalForks, totalLinks int

	for _, prws := range prs {
		if len(prws.Sessions) == 0 {
			continue
		}

		// Optional project filter.
		if projectFilter != "" {
			match := false
			for _, sm := range prws.Sessions {
				if sm.ProjectPath == projectFilter {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}

		stateIcon := "\U0001F7E2" // green circle
		switch prws.PR.State {
		case "merged":
			stateIcon = "\U0001F7E3" // purple circle
		case "closed":
			stateIcon = "\U0001F534" // red circle
		}

		// Build session tree for this PR's sessions.
		tree := buildGraphTree(prws.Sessions)
		nodes, forks := s.convertTreeToSGNodes(tree, 0)

		// Collect session links for this group.
		links := s.enrichWithLinks(prws.Sessions, nodes)

		totalSessions += len(prws.Sessions)
		totalForks += forks
		totalLinks += links

		subtitle := prws.PR.Branch
		if prws.PR.BaseBranch != "" {
			subtitle += " → " + prws.PR.BaseBranch
		}

		data.Groups = append(data.Groups, sgGroup{
			Title:        fmt.Sprintf("#%d %s", prws.PR.Number, prws.PR.Title),
			Subtitle:     subtitle,
			Icon:         stateIcon,
			State:        prws.PR.State,
			SearchName:   strings.ToLower(fmt.Sprintf("#%d %s %s", prws.PR.Number, prws.PR.Title, prws.PR.Branch)),
			SessionCount: len(prws.Sessions),
			TokensStr:    formatTokens(prws.TotalTokens),
			Nodes:        nodes,
		})
	}

	data.Stats = sgStats{
		TotalGroups:   len(data.Groups),
		TotalSessions: totalSessions,
		TotalForks:    totalForks,
		TotalLinks:    totalLinks,
	}
}

// buildGraphByProject groups sessions by project path.
func (s *Server) buildGraphByProject(data *sessionGraphPage, projectFilter string) {
	// Get recent sessions.
	req := service.ListRequest{All: true}
	if projectFilter != "" {
		req.ProjectPath = projectFilter
	}
	summaries, err := s.sessionSvc.List(req)
	if err != nil {
		s.logger.Printf("[graph] error listing sessions: %v", err)
		return
	}

	// Group by project path.
	byProject := make(map[string][]session.Summary)
	for _, sm := range summaries {
		proj := sm.ProjectPath
		if proj == "" {
			proj = "(unknown)"
		}
		byProject[proj] = append(byProject[proj], sm)
	}

	var totalSessions, totalForks, totalLinks int

	// Sort project names.
	projNames := make([]string, 0, len(byProject))
	for p := range byProject {
		projNames = append(projNames, p)
	}
	sort.Strings(projNames)

	for _, proj := range projNames {
		sms := byProject[proj]
		tree := buildGraphTree(sms)
		nodes, forks := s.convertTreeToSGNodes(tree, 0)
		links := s.enrichWithLinks(sms, nodes)

		totalSessions += len(sms)
		totalForks += forks
		totalLinks += links

		var totalTokens int
		for _, sm := range sms {
			totalTokens += sm.TotalTokens
		}

		data.Groups = append(data.Groups, sgGroup{
			Title:        projectBaseName(proj),
			Subtitle:     proj,
			Icon:         "\U0001F4C2",
			SearchName:   strings.ToLower(proj),
			SessionCount: len(sms),
			TokensStr:    formatTokens(totalTokens),
			Nodes:        nodes,
		})
	}

	data.Stats = sgStats{
		TotalGroups:   len(data.Groups),
		TotalSessions: totalSessions,
		TotalForks:    totalForks,
		TotalLinks:    totalLinks,
	}
}

// buildGraphByBranch groups sessions by branch name.
func (s *Server) buildGraphByBranch(data *sessionGraphPage, projectFilter string) {
	req := service.ListRequest{All: true}
	if projectFilter != "" {
		req.ProjectPath = projectFilter
	}
	summaries, err := s.sessionSvc.List(req)
	if err != nil {
		s.logger.Printf("[graph] error listing sessions: %v", err)
		return
	}

	// Group by branch.
	byBranch := make(map[string][]session.Summary)
	for _, sm := range summaries {
		branch := sm.Branch
		if branch == "" {
			branch = "(no branch)"
		}
		byBranch[branch] = append(byBranch[branch], sm)
	}

	var totalSessions, totalForks, totalLinks int

	branchNames := make([]string, 0, len(byBranch))
	for b := range byBranch {
		branchNames = append(branchNames, b)
	}
	sort.Strings(branchNames)

	for _, branch := range branchNames {
		sms := byBranch[branch]
		tree := buildGraphTree(sms)
		nodes, forks := s.convertTreeToSGNodes(tree, 0)
		links := s.enrichWithLinks(sms, nodes)

		totalSessions += len(sms)
		totalForks += forks
		totalLinks += links

		var totalTokens int
		for _, sm := range sms {
			totalTokens += sm.TotalTokens
		}

		data.Groups = append(data.Groups, sgGroup{
			Title:        branch,
			Icon:         "\U0001F33F",
			SearchName:   strings.ToLower(branch),
			SessionCount: len(sms),
			TokensStr:    formatTokens(totalTokens),
			Nodes:        nodes,
		})
	}

	data.Stats = sgStats{
		TotalGroups:   len(data.Groups),
		TotalSessions: totalSessions,
		TotalForks:    totalForks,
		TotalLinks:    totalLinks,
	}
}

// buildGraphTree constructs a tree from a flat list of summaries (same algo as service.buildTree).
func buildGraphTree(summaries []session.Summary) []session.SessionTreeNode {
	if len(summaries) == 0 {
		return nil
	}

	type treeNode struct {
		summary  session.Summary
		children []*treeNode
		isFork   bool
	}

	byID := make(map[session.ID]*treeNode, len(summaries))
	for _, sm := range summaries {
		byID[sm.ID] = &treeNode{summary: sm}
	}

	var roots []*treeNode
	for _, sm := range summaries {
		node := byID[sm.ID]
		if sm.ParentID != "" {
			parent, ok := byID[sm.ParentID]
			if ok {
				node.isFork = true
				parent.children = append(parent.children, node)
				continue
			}
		}
		roots = append(roots, node)
	}

	var convert func(n *treeNode) session.SessionTreeNode
	convert = func(n *treeNode) session.SessionTreeNode {
		out := session.SessionTreeNode{
			Summary: n.summary,
			IsFork:  n.isFork,
		}
		for _, child := range n.children {
			out.Children = append(out.Children, convert(child))
		}
		return out
	}

	result := make([]session.SessionTreeNode, 0, len(roots))
	for _, r := range roots {
		result = append(result, convert(r))
	}
	return result
}

// convertTreeToSGNodes recursively converts SessionTreeNode to sgNode view models.
// Returns (nodes, forkCount).
func (s *Server) convertTreeToSGNodes(tree []session.SessionTreeNode, depth int) ([]sgNode, int) {
	var nodes []sgNode
	forks := 0

	for _, t := range tree {
		if t.IsFork {
			forks++
		}

		node := sgNode{
			ID:            string(t.Summary.ID),
			IDShort:       truncateID(string(t.Summary.ID), 12),
			Summary:       t.Summary.Summary,
			Agent:         string(t.Summary.Agent),
			Provider:      string(t.Summary.Provider),
			SessionType:   t.Summary.SessionType,
			Status:        string(t.Summary.Status),
			Messages:      t.Summary.MessageCount,
			TokensStr:     formatTokens(t.Summary.TotalTokens),
			Errors:        t.Summary.ErrorCount,
			TimeAgo:       timeAgoString(t.Summary.CreatedAt),
			IsFork:        t.IsFork,
			Depth:         depth,
			IndentPx:      depth * 24,
			SummaryIndent: depth*24 + 28,
			HasChildren:   len(t.Children) > 0,
			SearchText:    strings.ToLower(string(t.Summary.ID) + " " + t.Summary.Summary + " " + string(t.Summary.Provider) + " " + string(t.Summary.Agent)),
		}

		if len(t.Children) > 0 {
			childNodes, childForks := s.convertTreeToSGNodes(t.Children, depth+1)
			node.Children = childNodes
			forks += childForks
		}

		nodes = append(nodes, node)
	}

	return nodes, forks
}

// enrichWithLinks adds link badges (delegation, continuation) to matching nodes.
// Returns the total number of links found.
func (s *Server) enrichWithLinks(_ []session.Summary, nodes []sgNode) int {
	if s.store == nil {
		return 0
	}

	totalLinks := 0
	for i := range nodes {
		s.addLinkBadges(&nodes[i], &totalLinks)
	}
	return totalLinks
}

// addLinkBadges recursively adds link badges to a node and its children.
func (s *Server) addLinkBadges(node *sgNode, totalLinks *int) {
	links, err := s.store.GetLinkedSessions(session.ID(node.ID))
	if err == nil && len(links) > 0 {
		for _, link := range links {
			badge := sgBadge{}
			if string(link.SourceSessionID) == node.ID {
				badge.Type = linkTypeCSS(string(link.LinkType))
				badge.Label = "→ " + shortLinkType(string(link.LinkType))
			} else {
				badge.Type = linkTypeCSS(string(link.LinkType))
				badge.Label = "← " + shortLinkType(string(link.LinkType))
			}
			node.LinkBadges = append(node.LinkBadges, badge)
			*totalLinks++
		}
	}

	for i := range node.Children {
		s.addLinkBadges(&node.Children[i], totalLinks)
	}
}

// linkTypeCSS returns a CSS-friendly class modifier for a link type.
func linkTypeCSS(linkType string) string {
	switch linkType {
	case "delegated_to", "delegated_from":
		return "delegation"
	case "continuation", "follow_up":
		return "continuation"
	case "replay_of":
		return "replay"
	default:
		return "related"
	}
}

// ── Command Patterns (Section 8.5) ──

// commandPatternsPage is the template data for /investigate/command-patterns.
type commandPatternsPage struct {
	Nav       string
	Patterns  []commandPatternView
	Total     int // total patterns found
	MinLength int // filter: minimum command length
	MinCount  int // filter: minimum occurrence count
}

// commandPatternView is a template-friendly view of a normalized command pattern.
type commandPatternView struct {
	Normalized   string
	Occurrences  int
	SessionCount int
	ProjectCount int
	AvgLength    int
	TotalChars   string // formatted
	MaxLength    int
	TotalOutput  string // formatted bytes
	AvgOutput    string // formatted bytes
}

// handleCommandPatterns renders the cross-session command pattern analysis page.
func (s *Server) handleCommandPatterns(w http.ResponseWriter, r *http.Request) {
	// Parse filter params with defaults.
	minLength := 100
	minCount := 3
	if v := r.URL.Query().Get("min_length"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			minLength = n
		}
	}
	if v := r.URL.Query().Get("min_count"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			minCount = n
		}
	}

	// Try cache first.
	cacheKey := fmt.Sprintf("command_patterns:%d:%d", minLength, minCount)
	if s.store != nil {
		if cached, cErr := s.store.GetCache(cacheKey, 2*time.Hour); cErr == nil && cached != nil {
			var patterns []session.CommandPattern
			if json.Unmarshal(cached, &patterns) == nil {
				data := s.buildCommandPatternsData(patterns, minLength, minCount)
				s.render(w, "command_patterns.html", data)
				return
			}
		}
	}

	// Compute: load all sessions and filter recent ones (last 90 days).
	summaries, err := s.sessionSvc.List(service.ListRequest{All: true})
	if err != nil {
		http.Error(w, "Failed to load sessions", http.StatusInternalServerError)
		return
	}
	since := time.Now().AddDate(0, 0, -90)
	var recentSummaries []session.Summary
	for _, sum := range summaries {
		if sum.CreatedAt.After(since) {
			recentSummaries = append(recentSummaries, sum)
		}
		if len(recentSummaries) >= 500 {
			break
		}
	}
	summaries = recentSummaries

	var inputs []session.CommandPatternInput
	for _, sum := range summaries {
		sess, gErr := s.sessionSvc.Get(string(sum.ID))
		if gErr != nil {
			continue
		}
		for i := range sess.Messages {
			for j := range sess.Messages[i].ToolCalls {
				tc := &sess.Messages[i].ToolCalls[j]
				cmd := extractFullCommand(tc.Name, tc.Input)
				if cmd == "" {
					continue
				}
				inputs = append(inputs, session.CommandPatternInput{
					FullCommand: cmd,
					SessionID:   sess.ID,
					ProjectPath: sess.ProjectPath,
					OutputBytes: len(tc.Output),
				})
			}
		}
	}

	patterns := session.FindCommandPatterns(inputs, minLength, minCount)

	// Cache for 2 hours.
	if s.store != nil {
		if raw, mErr := json.Marshal(patterns); mErr == nil {
			_ = s.store.SetCache(cacheKey, raw)
		}
	}

	data := s.buildCommandPatternsData(patterns, minLength, minCount)
	s.render(w, "command_patterns.html", data)
}

// buildCommandPatternsData converts raw patterns into template data.
func (s *Server) buildCommandPatternsData(patterns []session.CommandPattern, minLength, minCount int) commandPatternsPage {
	data := commandPatternsPage{
		Nav:       "investigate",
		Total:     len(patterns),
		MinLength: minLength,
		MinCount:  minCount,
	}
	// Cap at 50 for display.
	limit := 50
	if len(patterns) < limit {
		limit = len(patterns)
	}
	for _, p := range patterns[:limit] {
		data.Patterns = append(data.Patterns, commandPatternView{
			Normalized:   p.Normalized,
			Occurrences:  p.Occurrences,
			SessionCount: p.SessionCount,
			ProjectCount: p.ProjectCount,
			AvgLength:    p.AvgLength,
			TotalChars:   formatBytes(p.TotalChars),
			MaxLength:    p.MaxLength,
			TotalOutput:  formatBytes(p.TotalOutput),
			AvgOutput:    formatBytes(p.AvgOutput),
		})
	}
	return data
}

// extractFullCommand extracts the full command string from a tool call
// (not just the base command, but the entire invocation line).
func extractFullCommand(toolName, toolInput string) string {
	lower := strings.ToLower(toolName)
	if lower != "bash" && lower != "shell" && lower != "terminal" && lower != "execute_command" {
		return ""
	}
	// Try JSON format: {"command": "..."}.
	input := strings.TrimSpace(toolInput)
	if strings.HasPrefix(input, "{") {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(input), &obj); err == nil {
			if cmd, ok := obj["command"].(string); ok {
				return strings.TrimSpace(cmd)
			}
			if cmd, ok := obj["cmd"].(string); ok {
				return strings.TrimSpace(cmd)
			}
			return ""
		}
	}
	// Plain string — treat as command directly.
	return input
}

// shortLinkType returns a short display label for a link type.
func shortLinkType(linkType string) string {
	switch linkType {
	case "delegated_to":
		return "delegated"
	case "delegated_from":
		return "from"
	case "continuation":
		return "continued"
	case "follow_up":
		return "follow-up"
	case "replay_of":
		return "replay"
	default:
		return linkType
	}
}

// ── HTMX: Session Inspect Partial ──

// inspectProblemView is the template-friendly view of a detected problem.
type inspectProblemView struct {
	SeverityIcon  string
	SeverityLabel string
	SeverityClass string
	Title         string
	Category      string
	Observation   string
	Impact        string
}

// inspectTokenView is the template-friendly token section data.
type inspectTokenView struct {
	InputStr     string
	OutputStr    string
	RatioStr     string
	CacheRead    int
	CacheReadStr string
	CachePctStr  string
	Models       []inspectModelView
}

// inspectModelView is a single model's usage.
type inspectModelView struct {
	Model     string
	InputStr  string
	OutputStr string
	Msgs      int
}

// inspectImageView is the template-friendly image section data.
type inspectImageView struct {
	Total          int
	ToolReadImages int
	SimctlCaptures int
	SipsResizes    int
	AvgTurnsStr    string
	BilledTokStr   string
	CostStr        string
}

// inspectToolErrorView is the template-friendly tool error section data.
type inspectToolErrorView struct {
	TotalToolCalls int
	ErrorCount     int
	ErrorRateStr   string
	LoopCount      int
	TopTools       []inspectToolEntry
}

// inspectToolEntry is a single tool's error stats.
type inspectToolEntry struct {
	Name    string
	Calls   int
	Errors  int
	RateStr string
}

// inspectPatternView is the template-friendly pattern section data.
type inspectPatternView struct {
	WriteWithoutRead int
	UserCorrections  int
	GlobStorms       int
	LongRuns         int
	LongestRun       int
}

// inspectPartialData is the data passed to the inspect_partial template.
type inspectPartialData struct {
	HasProblems  bool
	ProblemCount int
	Problems     []inspectProblemView
	Tokens       *inspectTokenView
	Images       *inspectImageView
	ToolErrors   *inspectToolErrorView
	Patterns     *inspectPatternView
}

// handleInspectPartial renders the diagnostic inspect partial for a session.
// GET /partials/inspect/{id}
func (s *Server) handleInspectPartial(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "session id required", http.StatusBadRequest)
		return
	}

	sess, err := s.sessionSvc.Get(id)
	if err != nil {
		s.logger.Printf("inspect partial: session %q: %v", id, err)
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	var events []sessionevent.Event
	if s.sessionEventSvc != nil {
		events, _ = s.sessionEventSvc.GetSessionEvents(sess.ID)
	}

	report := diagnostic.BuildReport(sess, events)

	// Build template data
	data := inspectPartialData{
		HasProblems:  len(report.Problems) > 0,
		ProblemCount: len(report.Problems),
	}

	// Map problems
	for _, p := range report.Problems {
		data.Problems = append(data.Problems, inspectProblemView{
			SeverityIcon:  severityIcon(p.Severity),
			SeverityLabel: strings.ToUpper(string(p.Severity)),
			SeverityClass: string(p.Severity),
			Title:         p.Title,
			Category:      string(p.Category),
			Observation:   p.Observation,
			Impact:        p.Impact,
		})
	}

	// Tokens
	if report.Tokens != nil {
		t := report.Tokens
		tv := &inspectTokenView{
			InputStr:     formatTokens(t.Input),
			OutputStr:    formatTokens(t.Output),
			RatioStr:     fmt.Sprintf("%.0f:1", t.InputOutputRatio),
			CacheRead:    t.CacheRead,
			CacheReadStr: formatTokens(t.CacheRead),
			CachePctStr:  fmt.Sprintf("%.1f%%", t.CachePct),
		}
		for _, m := range t.Models {
			tv.Models = append(tv.Models, inspectModelView{
				Model:     m.Model,
				InputStr:  formatTokens(m.Input),
				OutputStr: formatTokens(m.Output),
				Msgs:      m.Msgs,
			})
		}
		data.Tokens = tv
	}

	// Images
	if report.Images != nil {
		img := report.Images
		total := img.InlineImages + img.ToolReadImages
		if total > 0 {
			data.Images = &inspectImageView{
				Total:          total,
				ToolReadImages: img.ToolReadImages,
				SimctlCaptures: img.SimctlCaptures,
				SipsResizes:    img.SipsResizes,
				AvgTurnsStr:    fmt.Sprintf("%.1f", img.AvgTurnsInCtx),
				BilledTokStr:   formatTokens(int(img.TotalBilledTok)),
				CostStr:        fmt.Sprintf("%.2f", img.EstImageCost),
			}
		}
	}

	// Tool errors
	if report.ToolErrors != nil && report.ToolErrors.ErrorCount > 0 {
		te := report.ToolErrors
		tev := &inspectToolErrorView{
			TotalToolCalls: te.TotalToolCalls,
			ErrorCount:     te.ErrorCount,
			ErrorRateStr:   fmt.Sprintf("%.1f%%", te.ErrorRate*100),
			LoopCount:      len(te.ErrorLoops),
		}
		for _, t := range te.TopErrorTools {
			tev.TopTools = append(tev.TopTools, inspectToolEntry{
				Name:    t.Name,
				Calls:   t.TotalCalls,
				Errors:  t.Errors,
				RateStr: fmt.Sprintf("%.1f%%", t.ErrorRate*100),
			})
		}
		data.ToolErrors = tev
	}

	// Patterns
	if report.Patterns != nil {
		data.Patterns = &inspectPatternView{
			WriteWithoutRead: report.Patterns.WriteWithoutReadCount,
			UserCorrections:  report.Patterns.UserCorrectionCount,
			GlobStorms:       report.Patterns.GlobStormCount,
			LongRuns:         report.Patterns.LongRunCount,
			LongestRun:       report.Patterns.LongestRunLength,
		}
	}

	s.renderPartial(w, "inspect_partial", data)
}

func severityIcon(s diagnostic.Severity) string {
	switch s {
	case diagnostic.SeverityHigh:
		return "\U0001F534" // 🔴
	case diagnostic.SeverityMedium:
		return "\U0001F7E0" // 🟠
	case diagnostic.SeverityLow:
		return "\U0001F7E1" // 🟡
	default:
		return "\u26AA" // ⚪
	}
}

// ── API: Session Inspect ──

// handleAPISessionInspect returns a full diagnostic InspectReport for a single session.
// GET /api/sessions/{id}/inspect
func (s *Server) handleAPISessionInspect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"session id required"}`, http.StatusBadRequest)
		return
	}

	sess, err := s.sessionSvc.Get(id)
	if err != nil {
		s.logger.Printf("api inspect: session %q: %v", id, err)
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	// Load session events for richer command data (optional).
	var events []sessionevent.Event
	if s.sessionEventSvc != nil {
		events, _ = s.sessionEventSvc.GetSessionEvents(sess.ID)
	}

	report := diagnostic.BuildReport(sess, events)

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		s.logger.Printf("api inspect: encode: %v", err)
	}
}

// ── API: Project Inspect ──

// ProjectInspectReport aggregates diagnostic data across multiple sessions.
type ProjectInspectReport struct {
	ProjectPath  string                    `json:"project_path"`
	SessionCount int                       `json:"session_count"`
	TotalCost    float64                   `json:"total_estimated_cost"`
	TotalInput   int64                     `json:"total_input_tokens"`
	TotalOutput  int64                     `json:"total_output_tokens"`
	Sessions     []ProjectSessionSummary   `json:"sessions"`
	TopProblems  []ProjectProblemAggregate `json:"top_problems"`
}

// ProjectSessionSummary is a lightweight per-session entry in the project inspect report.
type ProjectSessionSummary struct {
	SessionID    string               `json:"session_id"`
	Provider     string               `json:"provider"`
	Agent        string               `json:"agent"`
	Messages     int                  `json:"messages"`
	EstCost      float64              `json:"estimated_cost"`
	ProblemCount int                  `json:"problem_count"`
	TopSeverity  diagnostic.Severity  `json:"top_severity"`
	Problems     []diagnostic.Problem `json:"problems"`
}

// ProjectProblemAggregate counts how many sessions exhibit each problem ID.
type ProjectProblemAggregate struct {
	ID          diagnostic.ProblemID `json:"id"`
	Title       string               `json:"title"`
	Category    diagnostic.Category  `json:"category"`
	Occurrences int                  `json:"occurrences"`
	MaxSeverity diagnostic.Severity  `json:"max_severity"`
}

// handleAPIProjectInspect returns aggregated diagnostic data for the last N sessions of a project.
// GET /api/inspect/project?path=/Users/foo/myproject&limit=10
func (s *Server) handleAPIProjectInspect(w http.ResponseWriter, r *http.Request) {
	projectPath := r.URL.Query().Get("path")
	if projectPath == "" {
		http.Error(w, `{"error":"path query parameter required"}`, http.StatusBadRequest)
		return
	}

	limit := 10
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if n, err := strconv.Atoi(lStr); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}

	// List sessions for this project.
	summaries, err := s.sessionSvc.List(service.ListRequest{
		ProjectPath: projectPath,
		All:         true,
	})
	if err != nil {
		s.logger.Printf("api project inspect: list: %v", err)
		http.Error(w, `{"error":"failed to list sessions"}`, http.StatusInternalServerError)
		return
	}

	// Cap to limit (summaries are typically sorted newest-first).
	if len(summaries) > limit {
		summaries = summaries[:limit]
	}

	report := ProjectInspectReport{
		ProjectPath:  projectPath,
		SessionCount: len(summaries),
	}

	// Aggregate problem counts across sessions.
	problemCounts := make(map[diagnostic.ProblemID]*ProjectProblemAggregate)

	for _, sm := range summaries {
		sess, err := s.sessionSvc.Get(string(sm.ID))
		if err != nil {
			continue
		}

		var events []sessionevent.Event
		if s.sessionEventSvc != nil {
			events, _ = s.sessionEventSvc.GetSessionEvents(sess.ID)
		}

		ir := diagnostic.BuildReport(sess, events)

		topSev := diagnostic.Severity("")
		if len(ir.Problems) > 0 {
			topSev = ir.Problems[0].Severity // already sorted by severity
		}

		entry := ProjectSessionSummary{
			SessionID:    ir.SessionID,
			Provider:     ir.Provider,
			Agent:        ir.Agent,
			Messages:     ir.Messages,
			ProblemCount: len(ir.Problems),
			TopSeverity:  topSev,
			Problems:     ir.Problems,
		}
		if ir.Tokens != nil {
			entry.EstCost = ir.Tokens.EstCost
			report.TotalCost += ir.Tokens.EstCost
			report.TotalInput += int64(ir.Tokens.Input)
			report.TotalOutput += int64(ir.Tokens.Output)
		}
		report.Sessions = append(report.Sessions, entry)

		// Aggregate problems.
		for _, p := range ir.Problems {
			agg, ok := problemCounts[p.ID]
			if !ok {
				agg = &ProjectProblemAggregate{
					ID:          p.ID,
					Title:       p.Title,
					Category:    p.Category,
					MaxSeverity: p.Severity,
				}
				problemCounts[p.ID] = agg
			}
			agg.Occurrences++
			if severityRank(p.Severity) < severityRank(agg.MaxSeverity) {
				agg.MaxSeverity = p.Severity
			}
		}
	}

	// Sort aggregated problems by occurrence count descending.
	for _, agg := range problemCounts {
		report.TopProblems = append(report.TopProblems, *agg)
	}
	sort.Slice(report.TopProblems, func(i, j int) bool {
		si, sj := severityRank(report.TopProblems[i].MaxSeverity), severityRank(report.TopProblems[j].MaxSeverity)
		if si != sj {
			return si < sj
		}
		return report.TopProblems[i].Occurrences > report.TopProblems[j].Occurrences
	})

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		s.logger.Printf("api project inspect: encode: %v", err)
	}
}

// severityRank maps severity to sort order (lower = more severe).
func severityRank(s diagnostic.Severity) int {
	switch s {
	case diagnostic.SeverityHigh:
		return 0
	case diagnostic.SeverityMedium:
		return 1
	case diagnostic.SeverityLow:
		return 2
	default:
		return 3
	}
}
