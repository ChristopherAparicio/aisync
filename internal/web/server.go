// Package web implements the aisync web dashboard.
// It serves a local HTML UI for browsing sessions, viewing stats, and
// analyzing costs. Templates and static assets are embedded via go:embed.
package web

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/benchmark"
	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Server is the aisync web dashboard server.
type Server struct {
	sessionSvc      service.SessionServicer
	analysisSvc     service.AnalysisServicer // optional — nil disables analysis features
	registrySvc     *service.RegistryService // optional — nil disables project/capability features
	sessionEventSvc *sessionevent.Service    // optional — nil disables event analytics features
	benchmarkRec    *benchmark.Recommender   // optional — nil disables model alternatives
	store           storage.Store            // optional — nil disables user preferences
	cfg             *config.Config           // optional — nil uses defaults
	httpServer      *http.Server
	pages           map[string]*template.Template // page templates (layout + page)
	partials        *template.Template            // standalone partials (no layout)
	logger          *log.Logger
}

// Config holds the configuration for creating a new web Server.
type Config struct {
	SessionService       service.SessionServicer
	AnalysisService      service.AnalysisServicer // optional — nil disables analysis features
	RegistryService      *service.RegistryService // optional — nil disables project/capability features
	SessionEventService  *sessionevent.Service    // optional — nil disables event analytics features
	BenchmarkRecommender *benchmark.Recommender   // optional — nil disables model alternatives
	Store                storage.Store            // optional — nil disables user preferences
	AppConfig            *config.Config           // optional — nil uses defaults
	Addr                 string                   // e.g. ":8372" or "127.0.0.1:8372"
	Logger               *log.Logger              // optional — defaults to stderr
}

// New creates a web Server with the given configuration.
func New(cfg Config) (*Server, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = log.New(os.Stderr, "[aisync-web] ", log.LstdFlags)
	}

	// Parse page templates: each page = layout.html + page.html.
	// This avoids the "define content" collision between pages.
	funcs := templateFuncs()
	layoutFile := "templates/layout.html"

	// Each page entry: [page file, optional extra includes...]
	pageSpecs := [][]string{
		{"templates/dashboard.html"},
		{"templates/sessions.html", "templates/sessions_table.html"},
		{"templates/session_detail.html", "templates/restore_command.html", "templates/analysis_partial.html"},
		{"templates/branch_explorer.html"},
		{"templates/cost_dashboard.html"},
		{"templates/projects.html"},
		{"templates/project_detail.html"},
		{"templates/branch_timeline.html"},
		{"templates/usage.html"},
		{"templates/analytics.html"},
		{"templates/analytics_events.html"},
	}

	pages := make(map[string]*template.Template, len(pageSpecs))
	for _, spec := range pageSpecs {
		files := append([]string{layoutFile}, spec...)
		tmpl, err := template.New("").Funcs(funcs).ParseFS(templateFS, files...)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", spec[0], err)
		}
		name := spec[0][len("templates/"):]
		pages[name] = tmpl
	}

	// Parse partials (standalone, no layout wrapper).
	partials, err := template.New("").Funcs(funcs).ParseFS(templateFS,
		"templates/sessions_table.html",
		"templates/restore_command.html",
		"templates/analysis_partial.html",
		"templates/event_partials.html",
		"templates/search_results.html",
		"templates/project_sessions_partial.html",
	)
	if err != nil {
		return nil, fmt.Errorf("parse partials: %w", err)
	}

	s := &Server{
		sessionSvc:      cfg.SessionService,
		analysisSvc:     cfg.AnalysisService,
		registrySvc:     cfg.RegistryService,
		sessionEventSvc: cfg.SessionEventService,
		benchmarkRec:    cfg.BenchmarkRecommender,
		store:           cfg.Store,
		cfg:             cfg.AppConfig,
		pages:           pages,
		partials:        partials,
		logger:          logger,
	}

	mux := http.NewServeMux()
	s.RegisterRoutes(mux)

	s.httpServer = &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.loggingMiddleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	return s, nil
}

// RegisterRoutes registers all web dashboard routes on the given ServeMux.
// This allows external callers (e.g., a unified server) to mount web routes
// alongside API routes on a shared mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Static assets
	staticSub, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// Pages
	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.HandleFunc("GET /sessions", s.handleSessions)
	mux.HandleFunc("GET /sessions/{id}", s.handleSessionDetail)
	mux.HandleFunc("GET /branches", s.handleBranches)
	mux.HandleFunc("GET /branches/{name...}", s.handleBranchTimeline)
	mux.HandleFunc("GET /projects", s.handleProjects)
	mux.HandleFunc("GET /projects/{path...}", s.handleProjectDetail)
	mux.HandleFunc("GET /costs", s.handleCosts)
	mux.HandleFunc("GET /usage", s.handleUsage)
	mux.HandleFunc("GET /analytics", s.handleAnalytics)

	// HTMX partials
	mux.HandleFunc("GET /partials/sessions-table", s.handleSessionsTable)
	mux.HandleFunc("GET /partials/project-sessions/{path...}", s.handleProjectSessionsPartial)
	mux.HandleFunc("GET /partials/restore-command/{id}", s.handleRestoreCommand)
	mux.HandleFunc("GET /partials/analysis/{id}", s.handleAnalysisPartial)
	mux.HandleFunc("POST /partials/analyze/{id}", s.handleRunAnalysis)
	mux.HandleFunc("GET /partials/agent-detail", s.handleAgentDetailPartial)
	mux.HandleFunc("GET /partials/session-events/{id}", s.handleSessionEventsPartial)
	mux.HandleFunc("GET /partials/search-results", s.handleSearchResults)

	// API endpoints (JSON)
	mux.HandleFunc("GET /api/projects", s.handleAPIProjects)
}

// ListenAndServe starts the web server and blocks until shutdown.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.httpServer.Addr, err)
	}

	s.logger.Printf("dashboard available at http://%s", ln.Addr())

	errCh := make(chan error, 1)
	go func() {
		if serveErr := s.httpServer.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			errCh <- serveErr
		}
		close(errCh)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		s.logger.Printf("received %s, shutting down...", sig)
	case err := <-errCh:
		return err
	}

	return s.Shutdown()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

// Handler returns the underlying http.Handler for testing.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// loggingMiddleware logs each request.
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		s.logger.Printf("%s %s %d %s", r.Method, r.URL.Path, rw.status, time.Since(start).Round(time.Millisecond))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// render executes a page template (layout + page) with the given data.
// The name should be e.g. "dashboard.html" — the page is rendered via its
// top-level "define" block which invokes the layout.
func (s *Server) render(w http.ResponseWriter, name string, data any) {
	tmpl, ok := s.pages[name]
	if !ok {
		s.logger.Printf("render %s: page template not found", name)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.logger.Printf("render %s: %v", name, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// renderPartial executes a standalone partial template (no layout).
func (s *Server) renderPartial(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.partials.ExecuteTemplate(w, name, data); err != nil {
		s.logger.Printf("render partial %s: %v", name, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}
