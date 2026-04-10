package diagnostic

import (
	"fmt"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func init() { RegisterModule(&APIModule{}) }

// APIModule activates when the session contains HTTP client commands (curl,
// httpie, wget). It detects API retry loops and dense identical command bursts
// that waste tokens on repeated requests.
type APIModule struct{}

func (m *APIModule) Name() string { return "api" }

// ShouldActivate returns true if any bash command uses curl, httpie, or wget.
func (m *APIModule) ShouldActivate(sess *session.Session) bool {
	return SessionHasAPICalls(sess)
}

// Detect builds API-specific stats from the session, then runs API detectors.
func (m *APIModule) Detect(r *InspectReport, sess *session.Session) []Problem {
	// Build stats on first call — the module owns its own data.
	if GetAPIStats(r) == nil {
		r.SetModuleData("api", buildAPIStats(sess))
	}

	var problems []Problem
	problems = append(problems, detectAPIRetryLoop(r)...)
	problems = append(problems, detectIdenticalCommandBurst(r)...)

	return problems
}

// ── API detectors ───────────────────────────────────────────────────────────

// detectAPIRetryLoop identifies when the same API endpoint (URL) is called
// many times in the session, indicating the agent is stuck retrying an API call.
func detectAPIRetryLoop(r *InspectReport) []Problem {
	stats := GetAPIStats(r)
	if stats == nil || len(stats.EndpointCalls) == 0 {
		return nil
	}

	// Find endpoints called excessively (>= 5 times)
	var excessive []EndpointCall
	for _, ep := range stats.EndpointCalls {
		if ep.Count >= 5 {
			excessive = append(excessive, ep)
		}
	}
	if len(excessive) == 0 {
		return nil
	}

	totalWasted := 0
	var details []string
	for _, ep := range excessive {
		totalWasted += ep.Count
		details = append(details, fmt.Sprintf("%s called %d times", ep.URL, ep.Count))
	}

	sev := SeverityMedium
	if totalWasted > 15 {
		sev = SeverityHigh
	}

	return []Problem{{
		ID:       ProblemAPIRetryLoop,
		Severity: sev,
		Category: CategoryCommands,
		Title:    "API endpoint retry loops detected",
		Observation: fmt.Sprintf("%d endpoints called 5+ times: %s.",
			len(excessive), JoinMax(details, 3)),
		Impact: fmt.Sprintf("%d total API calls to heavily-retried endpoints. Each call "+
			"consumes tokens for command input and response output.", totalWasted),
		Metric:     float64(totalWasted),
		MetricUnit: "count",
	}}
}

// detectIdenticalCommandBurst detects dense clusters of 3+ identical commands
// within a short message window (≤ 5 messages apart). Unlike the global
// "repeated-commands" detector, this catches local bursts that indicate
// the agent is stuck.
func detectIdenticalCommandBurst(r *InspectReport) []Problem {
	stats := GetAPIStats(r)
	if stats == nil || len(stats.CommandBursts) == 0 {
		return nil
	}

	maxBurst := 0
	totalBursted := 0
	for _, b := range stats.CommandBursts {
		totalBursted += b.Count
		if b.Count > maxBurst {
			maxBurst = b.Count
		}
	}

	sev := SeverityMedium
	if len(stats.CommandBursts) > 3 || maxBurst > 5 {
		sev = SeverityHigh
	}

	return []Problem{{
		ID:       ProblemIdenticalCommandBurst,
		Severity: sev,
		Category: CategoryCommands,
		Title:    "Dense identical command bursts",
		Observation: fmt.Sprintf("%d burst clusters of 3+ identical commands within 5-message windows. "+
			"Largest burst: %d identical calls.", len(stats.CommandBursts), maxBurst),
		Impact: fmt.Sprintf("%d total commands in burst clusters. Dense repetition indicates "+
			"the agent is retrying without changing approach.", totalBursted),
		Metric:     float64(totalBursted),
		MetricUnit: "count",
	}}
}

// ── API stats builder ───────────────────────────────────────────────────────

// buildAPIStats scans tool calls for API-specific patterns.
func buildAPIStats(sess *session.Session) *APIAnalysis {
	stats := &APIAnalysis{}

	// Track endpoint URLs
	urlCounts := make(map[string]int)

	type cmdRecord struct {
		fullCmd string
		msgIdx  int
	}
	var httpCmds []cmdRecord

	for i, msg := range sess.Messages {
		for _, tc := range msg.ToolCalls {
			name := strings.ToLower(tc.Name)
			if name != "bash" && name != "mcp_bash" && name != "execute_command" {
				continue
			}
			input := tc.Input
			isCurl := strings.Contains(input, "curl ")
			isHTTP := isCurl || strings.Contains(input, "http ") || strings.Contains(input, "wget ")
			if !isHTTP {
				continue
			}

			// Extract URL from curl command
			if isCurl {
				url := ExtractCurlURL(input)
				if url != "" {
					urlCounts[url]++
				}
			}

			cmdStr := ExtractCommandFull(input)
			httpCmds = append(httpCmds, cmdRecord{fullCmd: cmdStr, msgIdx: i})
		}
	}

	// Build endpoint call list (sorted by count desc)
	for url, count := range urlCounts {
		stats.EndpointCalls = append(stats.EndpointCalls, EndpointCall{URL: url, Count: count})
	}

	// Detect command bursts: 3+ identical HTTP commands within 5-message windows
	if len(httpCmds) >= 3 {
		for i := 0; i < len(httpCmds); i++ {
			j := i + 1
			for j < len(httpCmds) &&
				httpCmds[j].fullCmd == httpCmds[i].fullCmd &&
				httpCmds[j].msgIdx-httpCmds[i].msgIdx <= 5 {
				j++
			}
			burstLen := j - i
			if burstLen >= 3 {
				stats.CommandBursts = append(stats.CommandBursts, CommandBurst{
					Command:     TruncateStr(httpCmds[i].fullCmd, 80),
					Count:       burstLen,
					StartMsgIdx: httpCmds[i].msgIdx,
					EndMsgIdx:   httpCmds[j-1].msgIdx,
				})
				i = j - 1 // skip past this burst
			}
		}
	}

	return stats
}
