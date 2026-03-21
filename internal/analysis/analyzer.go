package analysis

import (
	"context"

	"github.com/ChristopherAparicio/aisync/internal/registry"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// Analyzer is the port for session analysis.
// Adapters implement this to produce analysis reports from session data.
//
// Current adapters:
//   - llm/      — uses the internal llm.Client (Claude CLI)
//   - opencode/ — spawns an external OpenCode agent process
type Analyzer interface {
	// Analyze examines a session and returns an analysis report.
	// The implementation decides how to perform the analysis (LLM prompt, external agent, etc.).
	Analyze(ctx context.Context, req AnalyzeRequest) (*AnalysisReport, error)

	// Name returns the adapter identifier (e.g. "llm", "opencode").
	Name() AdapterName
}

// AnalyzeRequest contains all inputs needed to analyze a session.
type AnalyzeRequest struct {
	// Session is the full session data to analyze.
	Session session.Session

	// Capabilities lists the project's known agent capabilities (may be nil).
	Capabilities []registry.Capability

	// MCPServers lists the project's configured MCP servers (may be nil).
	MCPServers []registry.MCPServer

	// ErrorThreshold is the configured threshold that triggered this analysis.
	// Provided for context so the analyzer can reference it.
	ErrorThreshold float64

	// MinToolCalls is the configured minimum tool calls threshold.
	MinToolCalls int
}
