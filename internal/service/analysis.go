package service

import (
	"context"
	"fmt"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/registry"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/skillobs"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// ── AnalysisService ──

// AnalysisService orchestrates session analysis using configurable analyzers.
// It computes whether analysis is needed, delegates to an Analyzer adapter,
// and persists results via the Store.
type AnalysisService struct {
	store    storage.Store
	analyzer analysis.Analyzer // active analyzer adapter (llm or opencode)
}

// AnalysisServiceConfig holds all dependencies for creating an AnalysisService.
type AnalysisServiceConfig struct {
	Store    storage.Store
	Analyzer analysis.Analyzer // the analyzer adapter to use
}

// NewAnalysisService creates an AnalysisService with all dependencies.
func NewAnalysisService(cfg AnalysisServiceConfig) *AnalysisService {
	return &AnalysisService{
		store:    cfg.Store,
		analyzer: cfg.Analyzer,
	}
}

// ── Public API ──

// AnalyzeRequest contains inputs for running a session analysis.
type AnalysisRequest struct {
	// SessionID is the session to analyze.
	SessionID session.ID

	// Trigger indicates what initiated this analysis.
	Trigger analysis.Trigger

	// Capabilities is the optional list of project capabilities for context.
	Capabilities []registry.Capability

	// MCPServers is the optional list of configured MCP servers for context.
	MCPServers []registry.MCPServer

	// ErrorThreshold is the configured error rate threshold (for context in the prompt).
	ErrorThreshold float64

	// MinToolCalls is the configured minimum tool calls threshold.
	MinToolCalls int
}

// AnalysisResult contains the outcome of an analysis run.
type AnalysisResult struct {
	Analysis *analysis.SessionAnalysis
}

// Analyze runs a full session analysis: loads the session, delegates to the
// analyzer adapter, and persists the result.
func (s *AnalysisService) Analyze(ctx context.Context, req AnalysisRequest) (*AnalysisResult, error) {
	if s.analyzer == nil {
		return nil, fmt.Errorf("no analyzer configured")
	}

	sess, err := s.store.Get(req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("loading session %s: %w", req.SessionID, err)
	}

	if len(sess.Messages) == 0 {
		return nil, fmt.Errorf("session %s has no messages to analyze", req.SessionID)
	}

	start := time.Now()

	report, err := s.analyzer.Analyze(ctx, analysis.AnalyzeRequest{
		Session:        *sess,
		Capabilities:   req.Capabilities,
		MCPServers:     req.MCPServers,
		ErrorThreshold: req.ErrorThreshold,
		MinToolCalls:   req.MinToolCalls,
	})

	durationMs := int(time.Since(start).Milliseconds())

	// Build the persisted entity.
	sa := &analysis.SessionAnalysis{
		ID:         generateAnalysisID(),
		SessionID:  string(req.SessionID),
		CreatedAt:  time.Now(),
		Trigger:    req.Trigger,
		Adapter:    s.analyzer.Name(),
		DurationMs: durationMs,
	}

	if err != nil {
		// Persist the error so the user can see what went wrong.
		sa.Error = err.Error()
	} else {
		sa.Report = *report

		// Enrich with skill observation (best-effort — never fails the analysis).
		if obs := skillobs.Observe(sess.Messages, req.Capabilities); obs != nil {
			sa.Report.SkillObservation = obs
		}
	}

	// Persist the analysis (success or failure).
	if saveErr := s.store.SaveAnalysis(sa); saveErr != nil {
		return nil, fmt.Errorf("saving analysis: %w", saveErr)
	}

	return &AnalysisResult{Analysis: sa}, nil
}

// GetAnalysis retrieves a specific analysis by ID.
func (s *AnalysisService) GetAnalysis(id string) (*analysis.SessionAnalysis, error) {
	return s.store.GetAnalysis(id)
}

// GetLatestAnalysis retrieves the most recent analysis for a session.
func (s *AnalysisService) GetLatestAnalysis(sessionID string) (*analysis.SessionAnalysis, error) {
	return s.store.GetAnalysisBySession(sessionID)
}

// ListAnalyses returns all analyses for a session.
func (s *AnalysisService) ListAnalyses(sessionID string) ([]*analysis.SessionAnalysis, error) {
	return s.store.ListAnalyses(sessionID)
}

// ShouldAutoAnalyze determines whether a session warrants automatic analysis
// based on its error rate and tool call count relative to configured thresholds.
func ShouldAutoAnalyze(sess *session.Session, errorThreshold float64, minToolCalls int) bool {
	if len(sess.Messages) == 0 {
		return false
	}

	var totalToolCalls, errorToolCalls int
	for i := range sess.Messages {
		for j := range sess.Messages[i].ToolCalls {
			totalToolCalls++
			if sess.Messages[i].ToolCalls[j].State == session.ToolStateError {
				errorToolCalls++
			}
		}
	}

	// Must have enough tool calls to be meaningful.
	if totalToolCalls < minToolCalls {
		return false
	}

	// Check error rate against threshold.
	errorRate := float64(errorToolCalls) / float64(totalToolCalls) * 100
	return errorRate >= errorThreshold
}

// ── Helpers ──

// generateAnalysisID creates a unique ID for a new analysis.
func generateAnalysisID() string {
	return fmt.Sprintf("analysis-%d", time.Now().UnixNano())
}
