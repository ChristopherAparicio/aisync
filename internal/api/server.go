// Package api implements the HTTP API server for aisync.
// It exposes SessionService and SyncService operations as JSON endpoints
// using Go's stdlib net/http with Go 1.22+ ServeMux method-based routing.
package api

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/auth"
	"github.com/ChristopherAparicio/aisync/internal/replay"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
	"github.com/ChristopherAparicio/aisync/internal/skillresolver"
)

// Server is the aisync HTTP API server.
type Server struct {
	sessionSvc      service.SessionServicer
	syncSvc         *service.SyncService           // optional — nil when git sync is unavailable
	analysisSvc     service.AnalysisServicer       // optional — nil when analysis is unavailable
	errorSvc        service.ErrorServicer          // optional — nil when error analysis is unavailable
	sessionEventSvc *sessionevent.Service          // optional — nil when session event tracking is unavailable
	replayEngine    *replay.Engine                 // optional — nil when replay is unavailable
	skillResolver   skillresolver.ResolverServicer // optional — nil when skill resolver is unavailable
	authSvc         auth.Servicer                  // optional — nil disables authentication
	httpServer      *http.Server
	logger          *log.Logger

	// authHasUsers caches whether any auth users exist.
	// 0 = unknown, 1 = no users (open access), 2 = has users (enforce auth).
	// Used by authMiddleware for "open until first registration" dev mode.
	// Reset to 0 by handleAuthRegister so the next request re-checks.
	authHasUsers atomic.Int32
}

// Config holds the configuration for creating a new Server.
type Config struct {
	SessionService      service.SessionServicer
	SyncService         *service.SyncService           // optional
	AnalysisService     service.AnalysisServicer       // optional — nil disables analysis endpoints
	ErrorService        service.ErrorServicer          // optional — nil disables error endpoints
	SessionEventService *sessionevent.Service          // optional — nil disables session event endpoints
	ReplayEngine        *replay.Engine                 // optional — nil disables replay endpoint
	SkillResolver       skillresolver.ResolverServicer // optional — nil disables skill resolver endpoint
	AuthService         auth.Servicer                  // optional — nil disables authentication
	Addr                string                         // e.g. ":8371" or "127.0.0.1:8371"
	Logger              *log.Logger                    // optional — defaults to stderr
}

// New creates a Server with the given configuration.
func New(cfg Config) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = log.New(os.Stderr, "[aisync-api] ", log.LstdFlags)
	}

	s := &Server{
		sessionSvc:      cfg.SessionService,
		syncSvc:         cfg.SyncService,
		analysisSvc:     cfg.AnalysisService,
		errorSvc:        cfg.ErrorService,
		sessionEventSvc: cfg.SessionEventService,
		replayEngine:    cfg.ReplayEngine,
		skillResolver:   cfg.SkillResolver,
		authSvc:         cfg.AuthService,
		logger:          logger,
	}

	mux := http.NewServeMux()
	s.RegisterRoutes(mux)

	s.httpServer = &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.withMiddleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      180 * time.Second, // generous for LLM analysis (Ollama can be slow)
		IdleTimeout:       120 * time.Second,
	}

	return s
}

// ListenAndServe starts the HTTP server and blocks until it's shut down.
// It listens for SIGINT/SIGTERM to initiate graceful shutdown.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.httpServer.Addr, err)
	}

	s.logger.Printf("listening on %s", ln.Addr())

	// Graceful shutdown on signal
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

// Shutdown gracefully stops the server with a 10-second deadline.
func (s *Server) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

// Handler returns the underlying http.Handler for testing purposes.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}
