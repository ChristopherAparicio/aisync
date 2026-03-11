package web

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/pricing"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Shared ──

type branchStat struct {
	Branch       string
	SessionCount int
	TotalTokens  int
	TotalCost    float64
}

// ── Dashboard ──

type dashboardPage struct {
	Nav            string
	TotalSessions  int
	TotalTokens    int
	TotalCost      float64
	TrendDir       string
	RecentSessions []session.Summary
	TopBranches    []branchStat
	HasForecast    bool
	Projected30d   float64
	Projected90d   float64
	TrendPerDay    float64
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data := s.buildDashboardData(r.Context())
	s.render(w, "dashboard.html", data)
}

func (s *Server) buildDashboardData(ctx context.Context) dashboardPage {
	data := dashboardPage{Nav: "dashboard"}

	stats, err := s.sessionSvc.Stats(service.StatsRequest{All: true})
	if err != nil {
		s.logger.Printf("dashboard stats error: %v", err)
		return data
	}

	data.TotalSessions = stats.TotalSessions
	data.TotalTokens = stats.TotalTokens
	data.TotalCost = stats.TotalCost

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
		})
	}

	summaries, err := s.sessionSvc.List(service.ListRequest{All: true})
	if err != nil {
		s.logger.Printf("dashboard list error: %v", err)
	} else {
		limit := 10
		if len(summaries) < limit {
			limit = len(summaries)
		}
		data.RecentSessions = summaries[:limit]
	}

	forecast, err := s.sessionSvc.Forecast(ctx, service.ForecastRequest{
		Period: "weekly",
		Days:   90,
	})
	if err == nil && forecast.SessionCount > 0 {
		data.HasForecast = true
		data.Projected30d = forecast.Projected30d
		data.Projected90d = forecast.Projected90d
		data.TrendPerDay = forecast.TrendPerDay
		data.TrendDir = forecast.TrendDir
	}

	if data.TrendDir == "" {
		data.TrendDir = "stable"
	}

	return data
}

// ── Sessions List ──

const defaultPageSize = 25

type sessionsPage struct {
	Nav string

	// Filter state (echoed back for form pre-fill).
	FilterKeyword  string
	FilterBranch   string
	FilterProvider string

	// Results.
	Sessions   []session.Summary
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

	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		page = p
	}

	pageSize := defaultPageSize
	offset := (page - 1) * pageSize

	var providerName session.ProviderName
	if provider != "" {
		parsed, err := session.ParseProviderName(provider)
		if err == nil {
			providerName = parsed
		}
	}

	result, err := s.sessionSvc.Search(service.SearchRequest{
		Keyword:  keyword,
		Branch:   branch,
		Provider: providerName,
		Limit:    pageSize,
		Offset:   offset,
	})
	if err != nil {
		s.logger.Printf("sessions search error: %v", err)
		return sessionsPage{Nav: "sessions"}
	}

	totalPages := (result.TotalCount + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}

	return sessionsPage{
		Nav:            "sessions",
		FilterKeyword:  keyword,
		FilterBranch:   branch,
		FilterProvider: provider,
		Sessions:       result.Sessions,
		TotalCount:     result.TotalCount,
		Page:           page,
		PageSize:       pageSize,
		TotalPages:     totalPages,
		HasPrev:        page > 1,
		HasNext:        page < totalPages,
	}
}

// ── Session Detail ──

type sessionDetailPage struct {
	Nav           string
	Session       *session.Session
	TotalCost     float64
	CostBreakdown []session.ModelCost
	ToolCallCount int
	RestoreCmd    string
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

	// Count tool calls across all messages.
	for i := range sess.Messages {
		data.ToolCallCount += len(sess.Messages[i].ToolCalls)
	}

	// Compute cost breakdown via pricing calculator.
	calc := pricing.NewCalculator()
	estimate := calc.SessionCost(sess)
	if estimate != nil {
		data.TotalCost = estimate.TotalCost.TotalCost
		data.CostBreakdown = estimate.PerModel
	}

	data.RestoreCmd = buildRestoreCmd(string(sess.ID), "", false)

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
	Nav      string
	Branches []branchData
}

// handleBranches renders the branch explorer page.
func (s *Server) handleBranches(w http.ResponseWriter, r *http.Request) {
	data := s.buildBranchesData(r.Context())
	s.render(w, "branch_explorer.html", data)
}

func (s *Server) buildBranchesData(ctx context.Context) branchExplorerPage {
	data := branchExplorerPage{Nav: "branches"}

	// Get per-branch stats for the summary cards.
	stats, err := s.sessionSvc.Stats(service.StatsRequest{All: true})
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

	for _, bs := range stats.PerBranch {
		tree, treeErr := s.sessionSvc.ListTree(ctx, service.ListRequest{
			Branch: bs.Branch,
			All:    true,
		})
		if treeErr != nil {
			s.logger.Printf("branches tree %q: %v", bs.Branch, treeErr)
			continue
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

	return data
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
	Nav           string
	HasData       bool
	TotalCost     float64
	TotalSessions int
	AvgPerSession float64

	// Forecast
	HasForecast  bool
	Projected30d float64
	Projected90d float64
	TrendPerDay  float64
	TrendDir     string

	// Time buckets
	Period  string
	Buckets []costBucket

	// Model breakdown
	Models []session.ModelForecast

	// Per-branch cost
	BranchCosts []branchCostEntry
}

// handleCosts renders the cost dashboard.
func (s *Server) handleCosts(w http.ResponseWriter, r *http.Request) {
	data := s.buildCostsData(r.Context())
	s.render(w, "cost_dashboard.html", data)
}

func (s *Server) buildCostsData(ctx context.Context) costDashboardPage {
	data := costDashboardPage{Nav: "costs"}

	// Stats for totals + per-branch.
	stats, err := s.sessionSvc.Stats(service.StatsRequest{All: true})
	if err != nil {
		s.logger.Printf("costs stats error: %v", err)
		return data
	}

	if stats.TotalSessions == 0 {
		return data
	}

	data.HasData = true
	data.TotalCost = stats.TotalCost
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

	// Forecast for trend + model breakdown + time buckets.
	forecast, fErr := s.sessionSvc.Forecast(ctx, service.ForecastRequest{
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

	return data
}
