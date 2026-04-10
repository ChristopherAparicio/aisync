package diagnostic

import (
	"fmt"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func init() { RegisterModule(&RTKModule{}) }

// RTKModule activates when the session uses RTK (Run Tool Kit) as a command
// proxy. It detects RTK-specific failure modes: curl JSON corruption,
// secret redaction breaking authentication, and identical retry bursts caused
// by RTK compressing error details away.
type RTKModule struct{}

func (m *RTKModule) Name() string { return "rtk" }

// ShouldActivate returns true if any bash command in the session invokes RTK.
func (m *RTKModule) ShouldActivate(sess *session.Session) bool {
	return SessionHasRTK(sess)
}

// Detect builds RTK-specific stats from the session, then runs RTK detectors.
func (m *RTKModule) Detect(r *InspectReport, sess *session.Session) []Problem {
	// Build stats on first call — the module owns its own data.
	if GetRTKStats(r) == nil {
		r.SetModuleData("rtk", buildRTKStats(sess))
	}

	var problems []Problem
	problems = append(problems, detectRTKCurlConflict(r)...)
	problems = append(problems, detectRTKSecretRedaction(r)...)
	problems = append(problems, detectRTKIdenticalRetry(r)...)

	return problems
}

// ── RTK detectors ───────────────────────────────────────────────────────────

// detectRTKCurlConflict detects sessions where curl is run through RTK.
// RTK compresses JSON responses to schema-only representations (e.g.
// {code: string, detail: string}), hiding actual error messages. This prevents
// the agent from distinguishing success from failure.
func detectRTKCurlConflict(r *InspectReport) []Problem {
	if GetRTKStats(r) == nil {
		return nil
	}
	stats := GetRTKStats(r)
	if stats.CurlViaRTK < 3 {
		return nil
	}
	totalCurl := stats.CurlViaRTK + stats.CurlDirect
	rtkPct := float64(stats.CurlViaRTK) / float64(totalCurl) * 100

	sev := SeverityMedium
	if stats.CurlViaRTK > 10 {
		sev = SeverityHigh
	}

	return []Problem{{
		ID:       ProblemRTKCurlConflict,
		Severity: sev,
		Category: CategoryCommands,
		Title:    "RTK proxying curl — JSON responses compressed",
		Observation: fmt.Sprintf("%d of %d curl calls (%.0f%%) routed through RTK. RTK compresses JSON "+
			"to schema-only output (e.g. {code: string}), hiding actual error messages and values.",
			stats.CurlViaRTK, totalCurl, rtkPct),
		Impact: fmt.Sprintf("%d curl calls returned compressed output. Agent cannot distinguish "+
			"success from failure, leading to retry loops.", stats.CurlViaRTK),
		Metric:     float64(stats.CurlViaRTK),
		MetricUnit: "count",
	}}
}

// detectRTKSecretRedaction detects RTK replacing secrets (JWT tokens, API keys)
// with REDACTED placeholders, which produces invalid JSON when the agent tries
// to use the redacted values.
func detectRTKSecretRedaction(r *InspectReport) []Problem {
	stats := GetRTKStats(r)
	if stats == nil || stats.RedactedOutputs < 2 {
		return nil
	}
	sev := SeverityMedium
	if stats.RedactedOutputs > 5 {
		sev = SeverityHigh
	}
	return []Problem{{
		ID:       ProblemRTKSecretRedaction,
		Severity: sev,
		Category: CategoryCommands,
		Title:    "RTK secret redaction breaking authentication flows",
		Observation: fmt.Sprintf("%d command outputs contain RTK redaction markers (***REDACTED***). "+
			"Redacted JWT tokens and API keys produce invalid JSON when parsed downstream.",
			stats.RedactedOutputs),
		Impact: fmt.Sprintf("%d tool outputs with redacted secrets. Agent may retry authentication "+
			"calls repeatedly because responses appear malformed.", stats.RedactedOutputs),
		Metric:     float64(stats.RedactedOutputs),
		MetricUnit: "count",
	}}
}

// detectRTKIdenticalRetry detects bursts of 3+ identical RTK commands in a row,
// which indicate the agent is retrying because RTK's compressed output doesn't
// provide enough information to diagnose the failure.
func detectRTKIdenticalRetry(r *InspectReport) []Problem {
	stats := GetRTKStats(r)
	if stats == nil || len(stats.RetryBursts) == 0 {
		return nil
	}
	totalWasted := 0
	maxBurst := 0
	for _, b := range stats.RetryBursts {
		totalWasted += b.Count
		if b.Count > maxBurst {
			maxBurst = b.Count
		}
	}

	sev := SeverityMedium
	if len(stats.RetryBursts) > 3 || maxBurst > 5 {
		sev = SeverityHigh
	}

	return []Problem{{
		ID:       ProblemRTKIdenticalRetry,
		Severity: sev,
		Category: CategoryCommands,
		Title:    "Identical RTK command retry bursts",
		Observation: fmt.Sprintf("%d retry bursts detected (3+ identical rtk commands in sequence). "+
			"Largest burst: %d identical calls. Total retried: %d commands.",
			len(stats.RetryBursts), maxBurst, totalWasted),
		Impact: fmt.Sprintf("%d wasted commands from retry bursts. RTK's compressed output "+
			"prevented the agent from diagnosing the underlying failure.", totalWasted),
		Metric:     float64(totalWasted),
		MetricUnit: "count",
	}}
}

// ── RTK stats builder ───────────────────────────────────────────────────────

// buildRTKStats scans tool calls for RTK-specific patterns.
func buildRTKStats(sess *session.Session) *RTKAnalysis {
	stats := &RTKAnalysis{}

	type cmdRecord struct {
		fullCmd string
		msgIdx  int
		isRTK   bool
	}
	var history []cmdRecord

	for i, msg := range sess.Messages {
		for _, tc := range msg.ToolCalls {
			name := strings.ToLower(tc.Name)
			if name != "bash" && name != "mcp_bash" && name != "execute_command" {
				continue
			}

			input := tc.Input
			isRTK := strings.Contains(input, "rtk ") || strings.Contains(input, "/rtk ")
			isCurl := strings.Contains(input, "curl ")

			if isRTK {
				stats.TotalRTKCmds++
			}
			if isCurl {
				if isRTK {
					stats.CurlViaRTK++
				} else {
					stats.CurlDirect++
				}
			}

			// Check for redaction markers in output
			if strings.Contains(tc.Output, "REDACTED") || strings.Contains(tc.Output, "***") {
				stats.RedactedOutputs++
			}

			if isRTK {
				cmdStr := ExtractCommandFull(input)
				history = append(history, cmdRecord{fullCmd: cmdStr, msgIdx: i, isRTK: true})
			}
		}
	}

	// Detect retry bursts: 3+ identical RTK commands in sequence
	if len(history) >= 3 {
		i := 0
		for i < len(history) {
			j := i + 1
			for j < len(history) && history[j].fullCmd == history[i].fullCmd {
				j++
			}
			burstLen := j - i
			if burstLen >= 3 {
				stats.RetryBursts = append(stats.RetryBursts, RetryBurst{
					Command:     TruncateStr(history[i].fullCmd, 80),
					Count:       burstLen,
					StartMsgIdx: history[i].msgIdx,
					EndMsgIdx:   history[j-1].msgIdx,
				})
			}
			i = j
		}
	}

	return stats
}
