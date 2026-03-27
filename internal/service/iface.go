// Package service implements the core business logic for aisync.
// This file defines the SessionServicer interface — the Use Case Port
// that all driving adapters (CLI, API, MCP, Web) depend on.
//
// Two implementations exist:
//   - *SessionService — local mode (direct SQLite access)
//   - *remote.SessionService — remote mode (HTTP client → aisync serve)
package service

import (
	"context"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Role Interfaces (Interface Segregation Principle) ──
//
// Each interface below groups methods by domain responsibility.
// Consumers SHOULD depend on the smallest interface they need.
// SessionServicer composes all of them for DI convenience.
//
// Example: a CLI command that only needs to list sessions can accept
// SessionCRUD instead of the full SessionServicer.

// SessionCapturer handles session capture from AI coding providers.
type SessionCapturer interface {
	// Capture exports the most recent session from auto-detected or specified provider.
	Capture(req CaptureRequest) (*CaptureResult, error)

	// CaptureAll exports all sessions from the specified provider.
	CaptureAll(req CaptureRequest) ([]*CaptureResult, error)

	// CaptureByID exports a specific session by its provider-side ID.
	CaptureByID(req CaptureRequest, sessionID session.ID) (*CaptureResult, error)
}

// SessionRestorer handles session restoration (replay) to AI coding providers.
type SessionRestorer interface {
	Restore(req RestoreRequest) (*RestoreResult, error)
}

// SessionCRUD provides basic session read/write/delete operations.
type SessionCRUD interface {
	// Get retrieves a session by ID or commit SHA prefix.
	Get(idOrSHA string) (*session.Session, error)

	// List returns session summaries matching the given filter criteria.
	List(req ListRequest) ([]session.Summary, error)

	// ListTree returns sessions organized as a parent-child tree.
	ListTree(ctx context.Context, req ListRequest) ([]session.SessionTreeNode, error)

	// Delete removes a session by its ID.
	Delete(id session.ID) error

	// TagSession sets the session_type classification on a session.
	TagSession(ctx context.Context, id session.ID, sessionType string) error
}

// SessionExporter handles session export (to JSON/Markdown) and import.
type SessionExporter interface {
	Export(req ExportRequest) (*ExportResult, error)
	Import(req ImportRequest) (*ImportResult, error)
}

// SessionLinker manages git-integration features (linking sessions to PRs, posting comments).
type SessionLinker interface {
	Link(req LinkRequest) (*LinkResult, error)
	Comment(req CommentRequest) (*CommentResult, error)
}

// SessionAnalytics provides read-only analytical queries over sessions.
type SessionAnalytics interface {
	Stats(req StatsRequest) (*StatsResult, error)
	Search(req SearchRequest) (*session.SearchResult, error)
	Blame(ctx context.Context, req BlameRequest) (*BlameResult, error)
	EstimateCost(ctx context.Context, idOrSHA string) (*session.CostEstimate, error)
	ToolUsage(ctx context.Context, idOrSHA string) (*session.ToolUsageStats, error)
	Forecast(ctx context.Context, req ForecastRequest) (*session.ForecastResult, error)

	// ListProjects returns all distinct projects, grouped by git remote URL
	// (for git repos) or by project path (for non-git projects).
	ListProjects(ctx context.Context) ([]session.ProjectGroup, error)

	// Trends compares session metrics between current and previous period.
	Trends(ctx context.Context, req TrendRequest) (*TrendResult, error)

	// BranchTimeline builds an interleaved timeline of sessions and commits for a branch.
	BranchTimeline(ctx context.Context, req TimelineRequest) ([]TimelineEntry, error)

	// ComputeTokenBuckets pre-computes token usage per time bucket.
	ComputeTokenBuckets(ctx context.Context, req ComputeTokenBucketsRequest) (*ComputeTokenBucketsResult, error)

	// QueryTokenUsage retrieves pre-computed token usage buckets.
	QueryTokenUsage(ctx context.Context, req QueryTokenUsageRequest) ([]session.TokenUsageBucket, error)

	// ToolCostSummary returns per-tool and per-MCP-server cost aggregation.
	ToolCostSummary(ctx context.Context, projectPath string, since, until time.Time) (*session.ToolCostSummary, error)

	// AgentCostSummary returns per-agent cost aggregation for a project.
	AgentCostSummary(ctx context.Context, projectPath string, since, until time.Time) ([]session.AgentCostEntry, error)

	// CacheEfficiency computes prompt cache usage stats and identifies waste.
	CacheEfficiency(ctx context.Context, projectPath string, since time.Time) (*session.CacheEfficiency, error)

	// MCPCostMatrix builds a cross-project matrix of MCP server usage and costs.
	MCPCostMatrix(ctx context.Context, since, until time.Time) (*session.MCPProjectMatrix, error)

	// ContextSaturation computes how close sessions get to their model's context window limit.
	ContextSaturation(ctx context.Context, projectPath string, since time.Time) (*session.ContextSaturation, error)

	// ClassifySession applies per-project classifier rules (ticket extraction, branch rules).
	ClassifySession(sess *session.Session) int

	// ClassifyProjectSessions runs classifiers on all sessions for a project.
	ClassifyProjectSessions(remoteURL, projectPath string) (classified, total int, err error)

	// BudgetStatus computes spending vs budget for all projects with budgets.
	BudgetStatus(ctx context.Context) ([]session.BudgetStatus, error)
}

// SessionAI provides LLM-powered analysis features.
// These methods require a configured LLM client and return an error if none is set.
type SessionAI interface {
	Summarize(ctx context.Context, req SummarizeRequest) (*SummarizeResult, error)
	Explain(ctx context.Context, req ExplainRequest) (*ExplainResult, error)
	AnalyzeEfficiency(ctx context.Context, req EfficiencyRequest) (*EfficiencyResult, error)
	ComputeObjective(ctx context.Context, req ComputeObjectiveRequest) (*session.SessionObjective, error)
	GetObjective(ctx context.Context, sessionID string) (*session.SessionObjective, error)
}

// SessionManager provides session lifecycle management operations.
type SessionManager interface {
	Rewind(ctx context.Context, req RewindRequest) (*RewindResult, error)
	GarbageCollect(ctx context.Context, req GCRequest) (*GCResult, error)
	Diff(ctx context.Context, req DiffRequest) (*session.DiffResult, error)
	DetectOffTopic(ctx context.Context, req OffTopicRequest) (*session.OffTopicResult, error)

	// BackfillRemoteURLs resolves and persists git remote URLs for sessions
	// that have an empty remote_url. Returns the number of sessions updated.
	BackfillRemoteURLs(ctx context.Context) (*BackfillResult, error)

	// DetectForksBatch runs fork detection on all sessions and persists
	// the results to the session_forks table. Returns the number of fork
	// relations detected.
	DetectForksBatch(ctx context.Context) (*ForkDetectionResult, error)
}

// SessionIngester handles externally-pushed sessions and session-to-session links.
type SessionIngester interface {
	// Ingest stores a session pushed by an external client (Parlay, Ollama, etc.)
	// without provider detection or file-system reads.
	Ingest(ctx context.Context, req IngestRequest) (*IngestResult, error)

	// LinkSessions creates a link between two sessions (e.g. delegation, continuation).
	LinkSessions(ctx context.Context, req SessionLinkRequest) (*session.SessionLink, error)

	// GetLinkedSessions retrieves all session-to-session links for a given session.
	GetLinkedSessions(ctx context.Context, sessionID session.ID) ([]session.SessionLink, error)

	// DeleteSessionLink removes a session-to-session link by its ID.
	DeleteSessionLink(ctx context.Context, id session.ID) error
}

// ── Composed Interface ──

// SessionServicer composes all role interfaces into a single Use Case Port.
// It decouples driving adapters (CLI, API, MCP, Web) from the implementation
// details of whether sessions are managed locally (SQLite) or remotely (HTTP).
//
// The local implementation (*SessionService) and the remote implementation
// (*remote.SessionService) both satisfy this interface.
//
// Services that need the full SessionServicer can accept it directly;
// services that need only a subset should accept a narrower role interface.
type SessionServicer interface {
	SessionCapturer
	SessionRestorer
	SessionCRUD
	SessionExporter
	SessionLinker
	SessionAnalytics
	SessionAI
	SessionManager
	SessionIngester
}

// Compile-time check: *SessionService implements SessionServicer.
var _ SessionServicer = (*SessionService)(nil)

// ── Analysis Bounded Context ──

// AnalysisServicer is the Use Case Port for the Analysis bounded context.
// It is a separate interface from SessionServicer because analysis is a
// distinct domain with its own lifecycle (separate adapter, cron trigger, etc.).
//
// Two implementations exist:
//   - *AnalysisService        — local mode (analyzer adapter + SQLite)
//   - *remote.AnalysisService — remote mode (HTTP client → aisync serve)
type AnalysisServicer interface {
	// Analyze runs a full session analysis and persists the result.
	Analyze(ctx context.Context, req AnalysisRequest) (*AnalysisResult, error)

	// GetLatestAnalysis retrieves the most recent analysis for a session.
	GetLatestAnalysis(sessionID string) (*analysis.SessionAnalysis, error)

	// ListAnalyses returns all analyses for a session, newest first.
	ListAnalyses(sessionID string) ([]*analysis.SessionAnalysis, error)
}

// Compile-time check: *AnalysisService implements AnalysisServicer.
var _ AnalysisServicer = (*AnalysisService)(nil)

// ── Error Bounded Context ──

// ErrorServicer is the Use Case Port for the Error bounded context.
// It provides error classification, persistence, and querying for session errors.
//
// Current implementation:
//   - *ErrorService — local mode (deterministic classifier + SQLite)
type ErrorServicer interface {
	// ProcessSession classifies and persists all errors from a captured session.
	ProcessSession(sess *session.Session) (*ProcessSessionResult, error)

	// GetErrors retrieves all classified errors for a session.
	GetErrors(sessionID session.ID) ([]session.SessionError, error)

	// GetSummary computes aggregated error statistics for a session.
	GetSummary(sessionID session.ID) (*session.SessionErrorSummary, error)

	// ListRecent returns recent errors across all sessions, optionally filtered by category.
	ListRecent(limit int, category session.ErrorCategory) ([]session.SessionError, error)
}

// Compile-time check: *ErrorService implements ErrorServicer.
var _ ErrorServicer = (*ErrorService)(nil)
