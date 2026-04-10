package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/diagnostic"
	"github.com/ChristopherAparicio/aisync/internal/registry"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/skillobs"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// ── AnalysisService ──

// AnalysisService orchestrates session analysis using configurable analyzers
// and pluggable analysis modules. Modules run in parallel when selected.
type AnalysisService struct {
	store    storage.Store
	analyzer analysis.Analyzer                               // active analyzer adapter (llm or opencode)
	modules  map[analysis.ModuleName]analysis.AnalysisModule // registered modules
}

// AnalysisServiceConfig holds all dependencies for creating an AnalysisService.
type AnalysisServiceConfig struct {
	Store    storage.Store
	Analyzer analysis.Analyzer                               // the analyzer adapter to use
	Modules  map[analysis.ModuleName]analysis.AnalysisModule // optional pluggable modules
}

// NewAnalysisService creates an AnalysisService with all dependencies.
func NewAnalysisService(cfg AnalysisServiceConfig) *AnalysisService {
	modules := cfg.Modules
	if modules == nil {
		modules = make(map[analysis.ModuleName]analysis.AnalysisModule)
	}
	return &AnalysisService{
		store:    cfg.Store,
		analyzer: cfg.Analyzer,
		modules:  modules,
	}
}

// ── Public API ──

// AnalysisRequest contains inputs for running a session analysis.
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

	// Modules lists which analysis modules to run. If empty, only the core
	// analyzer is invoked (backward compatible). Each module runs in parallel.
	Modules []analysis.ModuleName
}

// AnalysisResult contains the outcome of an analysis run.
type AnalysisResult struct {
	Analysis *analysis.SessionAnalysis
}

// Analyze runs a full session analysis: loads the session, delegates to the
// analyzer adapter, runs selected modules in parallel, and persists the result.
func (s *AnalysisService) Analyze(ctx context.Context, req AnalysisRequest) (*AnalysisResult, error) {
	if s.analyzer == nil && len(req.Modules) == 0 {
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

	// Build deterministic diagnostic report to enrich the LLM analysis prompt.
	// This bridges diagnostic/ (deterministic detection) → analysis/ (LLM analysis).
	// Best-effort: failure here should not block the analysis.
	var diagSummary *analysis.DiagnosticSummary
	events, evtErr := s.store.GetSessionEvents(req.SessionID)
	if evtErr != nil {
		events = nil // proceed without events
	}
	inspectReport := diagnostic.BuildReport(sess, events)
	diagSummary = toDiagnosticSummary(inspectReport)

	// Build the persisted entity.
	sa := &analysis.SessionAnalysis{
		ID:        generateAnalysisID(),
		SessionID: string(req.SessionID),
		CreatedAt: time.Now(),
		Trigger:   req.Trigger,
	}

	// Determine what to run: core analyzer + selected modules.
	runCore := s.analyzer != nil && (len(req.Modules) == 0 || containsModule(req.Modules, analysis.ModuleSessionQuality))
	modulesToRun := filterModules(req.Modules, s.modules)

	// Run core analyzer and modules in parallel.
	var wg sync.WaitGroup
	var coreReport *analysis.AnalysisReport
	var coreErr error
	var moduleResults []analysis.ModuleResult
	var mu sync.Mutex

	if runCore {
		wg.Add(1)
		go func() {
			defer wg.Done()
			analyzeReq := analysis.AnalyzeRequest{
				Session:        *sess,
				Capabilities:   req.Capabilities,
				MCPServers:     req.MCPServers,
				ErrorThreshold: req.ErrorThreshold,
				MinToolCalls:   req.MinToolCalls,
				Diagnostic:     diagSummary,
			}

			// Provide a ToolExecutor for adapters that support agentic analysis.
			// The executor is pre-scoped to this session — the LLM cannot access
			// other sessions' data. Only the Anthropic adapter uses this today.
			if s.analyzer.Name() == analysis.AdapterAnthropic {
				analyzeReq.ToolExecutor = analysis.NewSessionToolExecutor(sess)
			}

			report, analyzeErr := s.analyzer.Analyze(ctx, analyzeReq)
			mu.Lock()
			coreReport = report
			coreErr = analyzeErr
			mu.Unlock()
		}()
	}

	for _, mod := range modulesToRun {
		mod := mod // capture
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, modErr := mod.Analyze(ctx, analysis.ModuleRequest{
				Session: *sess,
			})
			mu.Lock()
			if modErr != nil {
				moduleResults = append(moduleResults, analysis.ModuleResult{
					Module: mod.Name(),
					Error:  modErr.Error(),
				})
			} else if result != nil {
				moduleResults = append(moduleResults, *result)
			}
			mu.Unlock()
		}()
	}

	wg.Wait()
	durationMs := int(time.Since(start).Milliseconds())
	sa.DurationMs = durationMs

	// Assemble report.
	if runCore {
		if s.analyzer != nil {
			sa.Adapter = s.analyzer.Name()
		}
		if coreErr != nil {
			sa.Error = coreErr.Error()
		} else if coreReport != nil {
			sa.Report = *coreReport
			// Enrich with skill observation (best-effort).
			if obs := skillobs.Observe(sess.Messages, req.Capabilities); obs != nil {
				sa.Report.SkillObservation = obs
			}
		}
	} else if len(modulesToRun) > 0 {
		// No core analyzer — set adapter to "modules".
		sa.Adapter = "modules"
	}

	// Attach module results.
	if len(moduleResults) > 0 {
		sa.Report.ModuleResults = moduleResults
	}

	// Persist the analysis (success or failure).
	if saveErr := s.store.SaveAnalysis(sa); saveErr != nil {
		return nil, fmt.Errorf("saving analysis: %w", saveErr)
	}

	return &AnalysisResult{Analysis: sa}, nil
}

// AvailableModules returns info about all registered modules.
func (s *AnalysisService) AvailableModules() []analysis.ModuleInfo {
	var infos []analysis.ModuleInfo
	for _, info := range analysis.ModuleRegistry() {
		if _, ok := s.modules[info.Name]; ok {
			infos = append(infos, info)
		}
	}
	// Always include session_quality if the core analyzer is configured.
	if s.analyzer != nil {
		hasCore := false
		for _, info := range infos {
			if info.Name == analysis.ModuleSessionQuality {
				hasCore = true
				break
			}
		}
		if !hasCore {
			// Prepend core module info.
			coreInfo := analysis.ModuleInfo{
				Name:        analysis.ModuleSessionQuality,
				Label:       "Session Quality",
				Description: "Overall efficiency score, problems, and recommendations",
				RequiresLLM: true,
			}
			infos = append([]analysis.ModuleInfo{coreInfo}, infos...)
		}
	}
	return infos
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

// containsModule checks if a module name is in the list.
func containsModule(modules []analysis.ModuleName, name analysis.ModuleName) bool {
	for _, m := range modules {
		if m == name {
			return true
		}
	}
	return false
}

// filterModules returns the registered AnalysisModule instances that match
// the requested module names, excluding the core session_quality module
// (which is handled separately by the core analyzer).
func filterModules(requested []analysis.ModuleName, registered map[analysis.ModuleName]analysis.AnalysisModule) []analysis.AnalysisModule {
	if len(requested) == 0 {
		return nil
	}
	var result []analysis.AnalysisModule
	for _, name := range requested {
		if name == analysis.ModuleSessionQuality {
			continue // handled by core analyzer
		}
		if mod, ok := registered[name]; ok {
			result = append(result, mod)
		}
	}
	return result
}

// toDiagnosticSummary converts a diagnostic.InspectReport (from the diagnostic
// bounded context) into an analysis.DiagnosticSummary (port-level type).
// This bridges the deterministic detection pipeline with the LLM analysis
// without creating a direct dependency from analysis → diagnostic.
func toDiagnosticSummary(r *diagnostic.InspectReport) *analysis.DiagnosticSummary {
	if r == nil {
		return nil
	}

	ds := &analysis.DiagnosticSummary{}

	// Token economy.
	if r.Tokens != nil {
		ds.InputTokens = r.Tokens.Input
		ds.OutputTokens = r.Tokens.Output
		ds.ImageTokens = r.Tokens.Image
		ds.CacheReadPct = r.Tokens.CachePct
		ds.InputOutputRatio = r.Tokens.InputOutputRatio
		ds.EstimatedCost = r.Tokens.EstCost
	}

	// Images.
	if r.Images != nil {
		ds.InlineImages = r.Images.InlineImages
		ds.ToolReadImages = r.Images.ToolReadImages
		ds.SimctlCaptures = r.Images.SimctlCaptures
		ds.SipsResizes = r.Images.SipsResizes
		ds.ImageBilledTok = r.Images.TotalBilledTok
		ds.ImageCost = r.Images.EstImageCost
		ds.AvgTurnsInCtx = r.Images.AvgTurnsInCtx
	}

	// Compaction.
	if r.Compaction != nil {
		ds.CompactionCount = r.Compaction.Count
		ds.CascadeCount = r.Compaction.CascadeCount
		ds.CompactionsPerUser = r.Compaction.PerUserMsg
		ds.MedianInterval = r.Compaction.IntervalMedian
		ds.AvgBeforeTokens = r.Compaction.AvgBeforeTokens
	}

	// Tool errors.
	if r.ToolErrors != nil {
		ds.TotalToolCalls = r.ToolErrors.TotalToolCalls
		ds.ErrorToolCalls = r.ToolErrors.ErrorCount
		ds.ToolErrorRate = r.ToolErrors.ErrorRate
		ds.MaxConsecErrors = r.ToolErrors.ConsecutiveMax
	}

	// Behavioural patterns.
	if r.Patterns != nil {
		ds.CorrectionCount = r.Patterns.UserCorrectionCount
		ds.WriteWithoutReadCount = r.Patterns.WriteWithoutReadCount
		ds.GlobStormCount = r.Patterns.GlobStormCount
		ds.LongestUnguided = r.Patterns.LongestRunLength
	}

	// Detected problems.
	for _, p := range r.Problems {
		ds.Problems = append(ds.Problems, analysis.DiagnosticProblem{
			ID:          string(p.ID),
			Severity:    string(p.Severity),
			Category:    string(p.Category),
			Title:       p.Title,
			Observation: p.Observation,
			Impact:      p.Impact,
			Metric:      p.Metric,
			MetricUnit:  p.MetricUnit,
		})
	}

	return ds
}
