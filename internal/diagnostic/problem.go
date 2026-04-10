// Package diagnostic implements session problem detection and inspection reporting.
//
// It analyzes session data to identify token economy problems: expensive screenshots,
// verbose command output, frequent compaction, context thrashing, tool error loops,
// and more. Each detected problem includes a factual impact assessment.
//
// All detection is observational — facts, counts, ratios. Fix generation (Section 9.3)
// is a separate concern.
package diagnostic

// Severity indicates how impactful a detected problem is.
type Severity string

const (
	SeverityHigh   Severity = "high"
	SeverityMedium Severity = "medium"
	SeverityLow    Severity = "low"
)

// Category groups problems by the subsystem they affect.
type Category string

const (
	CategoryImages     Category = "images"
	CategoryCompaction Category = "compaction"
	CategoryCommands   Category = "commands"
	CategoryTokens     Category = "tokens"
	CategoryToolErrors Category = "tool_errors"
	CategoryPatterns   Category = "patterns"
)

// ProblemID uniquely identifies a type of problem.
type ProblemID string

const (
	// Image-related problems.
	ProblemExpensiveScreenshots ProblemID = "expensive-screenshots"
	ProblemOversizedScreenshots ProblemID = "oversized-screenshots"
	ProblemUnresizedScreenshots ProblemID = "unresized-screenshots"

	// Compaction-related problems.
	ProblemFrequentCompaction     ProblemID = "frequent-compaction"
	ProblemContextNearLimit       ProblemID = "context-near-limit"
	ProblemCompactionAccelerating ProblemID = "compaction-accelerating"

	// Command-related problems.
	ProblemVerboseCommands     ProblemID = "verbose-commands"
	ProblemRepeatedCommands    ProblemID = "repeated-commands"
	ProblemLongRunningCommands ProblemID = "long-running-commands"

	// Token-related problems.
	ProblemLowCacheUtilization ProblemID = "low-cache-utilization"
	ProblemHighInputRatio      ProblemID = "high-input-ratio"
	ProblemContextThrashing    ProblemID = "context-thrashing"

	// Tool error patterns.
	ProblemToolErrorLoops      ProblemID = "tool-error-loops"
	ProblemToolErrorEscalation ProblemID = "tool-error-escalation"
	ProblemAbandonedToolCalls  ProblemID = "abandoned-tool-calls"

	// Behavioral patterns.
	ProblemYoloEditing        ProblemID = "yolo-editing"
	ProblemReadWithoutPurpose ProblemID = "read-without-purpose"
	ProblemExcessiveGlobbing  ProblemID = "excessive-globbing"
	ProblemConversationDrift  ProblemID = "conversation-drift"

	// RTK-specific problems (module: rtk).
	ProblemRTKCurlConflict    ProblemID = "rtk-curl-conflict"
	ProblemRTKSecretRedaction ProblemID = "rtk-secret-redaction"
	ProblemRTKIdenticalRetry  ProblemID = "rtk-identical-retry"

	// API-specific problems (module: api).
	ProblemAPIRetryLoop          ProblemID = "api-retry-loop"
	ProblemIdenticalCommandBurst ProblemID = "identical-command-burst"
)

// Problem represents a detected issue in a session.
type Problem struct {
	ID          ProblemID `json:"id"`
	Severity    Severity  `json:"severity"`
	Category    Category  `json:"category"`
	Title       string    `json:"title"`       // short display title
	Observation string    `json:"observation"` // factual description of what was observed
	Impact      string    `json:"impact"`      // quantified impact (tokens, cost, time)
	Metric      float64   `json:"metric"`      // primary numeric metric (for sorting/comparison)
	MetricUnit  string    `json:"metric_unit"` // e.g. "tokens", "USD", "count", "ratio"
}
