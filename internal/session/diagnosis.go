// Package session — diagnosis.go provides unified diagnostic analysis for sessions.
//
// DiagnosisReport combines health scoring, error timeline positioning, phase
// analysis, and tool usage into a single cohesive report. All functions in this
// file are pure — they depend only on session data, no I/O or LLM calls.
//
// The "quick scan" (BuildErrorTimeline, AnalyzePhases, BuildToolReport,
// ComputeVerdict) is deterministic and instant. The optional "deep analysis"
// (root cause, suggestions) is handled by the service layer via LLM.
package session

import "fmt"

// ── DiagnosisReport ──

// DiagnosisReport is the unified diagnostic output for a single session.
// The Quick Scan fields are always populated (pure functions, no LLM).
// The Deep Analysis fields are only populated when the caller requests --deep.
type DiagnosisReport struct {
	// ── Quick Scan (always populated) ──

	HealthScore   HealthScore          `json:"health_score"`
	Overload      OverloadAnalysis     `json:"overload"`
	ErrorTimeline []ErrorTimelineEntry `json:"error_timeline"`
	ErrorSummary  SessionErrorSummary  `json:"error_summary"`
	ToolReport    ToolReport           `json:"tool_report"`
	Phases        PhaseAnalysis        `json:"phases"`
	Verdict       DiagnosisVerdict     `json:"verdict"`

	// ── Deep Analysis (optional, LLM-powered) ──

	RootCause     string            `json:"root_cause,omitempty"`
	Efficiency    *EfficiencyReport `json:"efficiency,omitempty"`
	Suggestions   []string          `json:"suggestions,omitempty"`
	RestoreAdvice *RestoreAdvice    `json:"restore_advice,omitempty"`
}

// ── Error Timeline ──

// ErrorTimelineEntry positions an error within the session's message flow.
type ErrorTimelineEntry struct {
	MessageIndex int          `json:"message_index"`
	MessageRole  string       `json:"message_role"` // "user", "assistant", "system"
	Error        SessionError `json:"error"`
	Phase        string       `json:"phase"`         // "early" (<25%), "middle" (25-75%), "late" (>75%)
	IsEscalation bool         `json:"is_escalation"` // true if previous error was same category → cascade
}

// BuildErrorTimeline positions session errors within the message flow,
// annotating each with its phase and whether it escalates a cascade.
func BuildErrorTimeline(sess *Session) []ErrorTimelineEntry {
	if len(sess.Errors) == 0 {
		return nil
	}

	total := len(sess.Messages)
	if total == 0 {
		total = 1 // avoid division by zero
	}

	entries := make([]ErrorTimelineEntry, 0, len(sess.Errors))
	var prevCategory ErrorCategory

	for _, se := range sess.Errors {
		phase := classifyPhase(se.MessageIndex, total)
		isEsc := prevCategory != "" && se.Category == prevCategory
		prevCategory = se.Category

		role := ""
		if se.MessageIndex >= 0 && se.MessageIndex < len(sess.Messages) {
			role = string(sess.Messages[se.MessageIndex].Role)
		}

		entries = append(entries, ErrorTimelineEntry{
			MessageIndex: se.MessageIndex,
			MessageRole:  role,
			Error:        se,
			Phase:        phase,
			IsEscalation: isEsc,
		})
	}
	return entries
}

// classifyPhase returns "early", "middle", or "late" based on the message
// position relative to the total session length.
func classifyPhase(msgIndex, totalMessages int) string {
	if totalMessages <= 0 {
		return "early"
	}
	pct := float64(msgIndex) / float64(totalMessages) * 100
	switch {
	case pct < 25:
		return "early"
	case pct < 75:
		return "middle"
	default:
		return "late"
	}
}

// ── Phase Analysis ──

// PhaseAnalysis segments a session into quality phases and identifies the
// turning point where degradation begins.
type PhaseAnalysis struct {
	Phases       []SessionPhase `json:"phases"`
	TurningPoint int            `json:"turning_point"` // message index where quality degrades (0 = none)
	Pattern      string         `json:"pattern"`       // "clean-start-late-crash", "error-from-start", "steady-decline", "healthy"
}

// SessionPhase represents a contiguous segment of messages with similar quality.
type SessionPhase struct {
	StartMsg    int     `json:"start_msg"`
	EndMsg      int     `json:"end_msg"`
	ErrorRate   float64 `json:"error_rate"` // tool error rate (0-100)
	ToolErrors  int     `json:"tool_errors"`
	ToolCalls   int     `json:"tool_calls"`
	OutputRatio float64 `json:"output_ratio"` // output/input tokens ratio
	Label       string  `json:"label"`        // "clean", "degrading", "broken"
}

// AnalyzePhases segments the session into quarters and measures quality per segment.
func AnalyzePhases(sess *Session) PhaseAnalysis {
	msgs := sess.Messages
	if len(msgs) < 4 {
		return PhaseAnalysis{Pattern: "too-short"}
	}

	// Split into 4 quarters.
	qSize := len(msgs) / 4
	if qSize < 1 {
		qSize = 1
	}

	var phases []SessionPhase
	for q := 0; q < 4; q++ {
		start := q * qSize
		end := start + qSize
		if q == 3 {
			end = len(msgs) // last quarter absorbs remainder
		}
		if start >= len(msgs) {
			break
		}

		phase := computePhaseStats(msgs[start:end], start)
		phases = append(phases, phase)
	}

	// Determine the turning point: first quarter where label changes to "degrading" or "broken".
	turningPoint := 0
	for _, p := range phases {
		if p.Label == "degrading" || p.Label == "broken" {
			turningPoint = p.StartMsg
			break
		}
	}

	pattern := classifyPattern(phases)
	return PhaseAnalysis{
		Phases:       phases,
		TurningPoint: turningPoint,
		Pattern:      pattern,
	}
}

// computePhaseStats measures quality for a slice of messages.
func computePhaseStats(msgs []Message, globalOffset int) SessionPhase {
	var inputTok, outputTok int64
	var toolCalls, toolErrors int

	for i := range msgs {
		inputTok += int64(msgs[i].InputTokens)
		outputTok += int64(msgs[i].OutputTokens)
		for j := range msgs[i].ToolCalls {
			toolCalls++
			if msgs[i].ToolCalls[j].State == ToolStateError {
				toolErrors++
			}
		}
	}

	errorRate := 0.0
	if toolCalls > 0 {
		errorRate = float64(toolErrors) / float64(toolCalls) * 100
	}
	outputRatio := 0.0
	if inputTok > 0 {
		outputRatio = float64(outputTok) / float64(inputTok)
	}

	label := "clean"
	if errorRate > 30 {
		label = "broken"
	} else if errorRate > 10 {
		label = "degrading"
	}

	return SessionPhase{
		StartMsg:    globalOffset,
		EndMsg:      globalOffset + len(msgs) - 1,
		ErrorRate:   errorRate,
		ToolErrors:  toolErrors,
		ToolCalls:   toolCalls,
		OutputRatio: outputRatio,
		Label:       label,
	}
}

// classifyPattern assigns a pattern label based on phase quality progression.
func classifyPattern(phases []SessionPhase) string {
	if len(phases) == 0 {
		return "unknown"
	}

	firstBroken := phases[0].Label == "broken" || phases[0].Label == "degrading"
	lastBroken := phases[len(phases)-1].Label == "broken" || phases[len(phases)-1].Label == "degrading"
	allClean := true
	degradeCount := 0
	for _, p := range phases {
		if p.Label != "clean" {
			allClean = false
			degradeCount++
		}
	}

	switch {
	case allClean:
		return "healthy"
	case firstBroken && lastBroken:
		return "error-from-start"
	case !firstBroken && lastBroken && degradeCount <= 2:
		return "clean-start-late-crash"
	case degradeCount >= 3:
		return "steady-decline"
	default:
		return "intermittent"
	}
}

// ── Tool Report ──

// ToolReport is a lightweight per-tool breakdown for the diagnosis.
// Unlike the full ToolUsageStats (computed in the service layer with pricing),
// this is a pure domain function that works from messages alone.
type ToolReport struct {
	Tools       []ToolReportEntry `json:"tools"`
	TotalCalls  int               `json:"total_calls"`
	TotalErrors int               `json:"total_errors"`
}

// ToolReportEntry summarizes a single tool's usage in the session.
type ToolReportEntry struct {
	Name      string  `json:"name"`
	Calls     int     `json:"calls"`
	Errors    int     `json:"errors"`
	ErrorRate float64 `json:"error_rate"` // 0-100
}

// BuildToolReport computes per-tool call counts and error rates from messages.
func BuildToolReport(sess *Session) ToolReport {
	type toolAgg struct {
		calls  int
		errors int
	}
	perTool := make(map[string]*toolAgg)
	totalCalls, totalErrors := 0, 0

	for i := range sess.Messages {
		for j := range sess.Messages[i].ToolCalls {
			tc := &sess.Messages[i].ToolCalls[j]
			totalCalls++
			agg, ok := perTool[tc.Name]
			if !ok {
				agg = &toolAgg{}
				perTool[tc.Name] = agg
			}
			agg.calls++
			if tc.State == ToolStateError {
				agg.errors++
				totalErrors++
			}
		}
	}

	// Sort by error count desc, then by calls desc.
	entries := make([]ToolReportEntry, 0, len(perTool))
	for name, agg := range perTool {
		rate := 0.0
		if agg.calls > 0 {
			rate = float64(agg.errors) / float64(agg.calls) * 100
		}
		entries = append(entries, ToolReportEntry{
			Name:      name,
			Calls:     agg.calls,
			Errors:    agg.errors,
			ErrorRate: rate,
		})
	}
	sortToolReportEntries(entries)

	return ToolReport{
		Tools:       entries,
		TotalCalls:  totalCalls,
		TotalErrors: totalErrors,
	}
}

// sortToolReportEntries sorts by errors desc, then calls desc, then name asc.
func sortToolReportEntries(entries []ToolReportEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0; j-- {
			swap := false
			if entries[j].Errors > entries[j-1].Errors {
				swap = true
			} else if entries[j].Errors == entries[j-1].Errors {
				if entries[j].Calls > entries[j-1].Calls {
					swap = true
				} else if entries[j].Calls == entries[j-1].Calls && entries[j].Name < entries[j-1].Name {
					swap = true
				}
			}
			if swap {
				entries[j], entries[j-1] = entries[j-1], entries[j]
			} else {
				break
			}
		}
	}
}

// ── Verdict ──

// DiagnosisVerdict is the one-line summary of the session's health.
type DiagnosisVerdict struct {
	Status   string `json:"status"`    // "healthy", "degraded", "broken"
	OneLiner string `json:"one_liner"` // human-readable summary sentence
	Score    int    `json:"score"`     // 0-100
}

// ComputeVerdict derives the final verdict from the health score, overload
// analysis, and phase analysis.
func ComputeVerdict(hs HealthScore, ol OverloadAnalysis, pa PhaseAnalysis) DiagnosisVerdict {
	score := hs.Total

	// Status thresholds aligned with health score grades.
	status := "healthy"
	switch {
	case score < 40:
		status = "broken"
	case score < 70:
		status = "degraded"
	}

	oneLiner := buildVerdictOneLiner(status, ol, pa)
	return DiagnosisVerdict{
		Status:   status,
		OneLiner: oneLiner,
		Score:    score,
	}
}

// buildVerdictOneLiner generates a human-readable summary sentence.
func buildVerdictOneLiner(status string, ol OverloadAnalysis, pa PhaseAnalysis) string {
	if status == "healthy" {
		return "Session completed without significant issues"
	}

	// Use overload reason if available.
	if ol.Reason != "" && ol.Verdict != "healthy" {
		if pa.TurningPoint > 0 {
			return fmt.Sprintf("Quality declined at message %d: %s", pa.TurningPoint, ol.Reason)
		}
		return ol.Reason
	}

	// Fall back to phase pattern.
	switch pa.Pattern {
	case "clean-start-late-crash":
		return fmt.Sprintf("Session started clean but degraded at message %d", pa.TurningPoint)
	case "error-from-start":
		return "Session had errors from the beginning"
	case "steady-decline":
		return "Session quality declined steadily throughout"
	case "intermittent":
		return "Session had intermittent quality issues"
	default:
		if status == "broken" {
			return "Session experienced significant quality issues"
		}
		return "Session had moderate quality degradation"
	}
}

// ── Restore Advice ──

// RestoreAdvice suggests how to best restore a degraded session.
type RestoreAdvice struct {
	RecommendedRewindTo int      `json:"recommended_rewind_to"` // message index to rewind to (0 = don't rewind)
	SuggestedFilters    []string `json:"suggested_filters"`     // e.g. ["--clean-errors", "--fix-orphans"]
	Reason              string   `json:"reason"`
}

// ComputeRestoreAdvice generates actionable restore suggestions from the diagnosis.
func ComputeRestoreAdvice(report *DiagnosisReport) *RestoreAdvice {
	if report.Verdict.Status == "healthy" {
		return nil // no advice needed
	}

	advice := &RestoreAdvice{}

	// Suggest rewind to turning point (a few messages before degradation starts).
	if report.Phases.TurningPoint > 0 {
		rewindTo := report.Phases.TurningPoint - 2
		if rewindTo < 0 {
			rewindTo = 0
		}
		advice.RecommendedRewindTo = rewindTo
		advice.Reason = fmt.Sprintf("Messages after %d contain degraded output", report.Phases.TurningPoint)
	}

	// Suggest filters based on error types.
	if report.ErrorSummary.TotalErrors > 0 {
		advice.SuggestedFilters = append(advice.SuggestedFilters, "--clean-errors")
	}

	// Check for orphan tool calls (tool_use without result).
	hasOrphans := false
	for _, entry := range report.ErrorTimeline {
		if entry.Error.Category == ErrorCategoryToolError && entry.Error.ToolCallID != "" {
			hasOrphans = true
			break
		}
	}
	if hasOrphans {
		advice.SuggestedFilters = append(advice.SuggestedFilters, "--fix-orphans")
	}

	// If no specific advice was generated, provide generic guidance.
	if advice.RecommendedRewindTo == 0 && len(advice.SuggestedFilters) == 0 {
		return nil
	}

	return advice
}
