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

	// Diagnostic contains the pre-computed deterministic diagnostic report.
	// Built by the service layer from the diagnostic package before calling
	// the analyzer. Nil if diagnostic data is unavailable.
	Diagnostic *DiagnosticSummary

	// ToolExecutor provides investigation tools for the LLM analyst during
	// agentic analysis. Pre-scoped to the session being analyzed. Nil when
	// agentic analysis is not supported (non-Anthropic adapters).
	ToolExecutor ToolExecutor
}

// DiagnosticSummary is a port-level representation of the deterministic
// diagnostic findings (from internal/diagnostic). This decouples the analysis
// port from the diagnostic implementation while passing all data the LLM
// needs to produce contextualised recommendations.
type DiagnosticSummary struct {
	// Token economy
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	ImageTokens      int     `json:"image_tokens"`
	CacheReadPct     float64 `json:"cache_read_pct"`
	InputOutputRatio float64 `json:"input_output_ratio"`
	EstimatedCost    float64 `json:"estimated_cost_usd"`

	// Images
	InlineImages   int     `json:"inline_images"`
	ToolReadImages int     `json:"tool_read_images"`
	SimctlCaptures int     `json:"simctl_captures"`
	SipsResizes    int     `json:"sips_resizes"`
	ImageBilledTok int64   `json:"image_billed_tokens"`
	ImageCost      float64 `json:"image_cost_usd"`
	AvgTurnsInCtx  float64 `json:"avg_turns_in_context"`

	// Compaction
	CompactionCount    int     `json:"compaction_count"`
	CascadeCount       int     `json:"cascade_count"`
	CompactionsPerUser float64 `json:"compactions_per_user_msg"`
	MedianInterval     int     `json:"median_compaction_interval"`
	AvgBeforeTokens    int     `json:"avg_tokens_before_compaction"`

	// Tool errors
	TotalToolCalls  int     `json:"total_tool_calls"`
	ErrorToolCalls  int     `json:"error_tool_calls"`
	ToolErrorRate   float64 `json:"tool_error_rate_pct"`
	MaxConsecErrors int     `json:"max_consecutive_errors"`

	// Behavioural patterns
	CorrectionCount       int `json:"correction_count"`
	WriteWithoutReadCount int `json:"write_without_read_count"`
	GlobStormCount        int `json:"glob_storm_count"`
	LongestUnguided       int `json:"longest_unguided_run"`

	// Detected problems (deterministic)
	Problems []DiagnosticProblem `json:"problems"`
}

// DiagnosticProblem is a single problem detected by the deterministic pipeline.
type DiagnosticProblem struct {
	ID          string  `json:"id"`
	Severity    string  `json:"severity"`
	Category    string  `json:"category"`
	Title       string  `json:"title"`
	Observation string  `json:"observation"`
	Impact      string  `json:"impact"`
	Metric      float64 `json:"metric"`
	MetricUnit  string  `json:"metric_unit"`
}
