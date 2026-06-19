// Package stalldetector — Phase 3 alerting helpers.
//
// This file contains the pure threshold-evaluation logic used by the
// scheduler StallAlertTask. It is split out so the rules can be unit-tested
// in isolation from the scheduler / notification machinery.
package stalldetector

import (
	"sort"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// AlertThresholds describes when a stall snapshot is considered alert-worthy.
//
// A stall snapshot fires an alert when ANY of the configured thresholds are met:
//
//   - LiveCount: number of currently-live (un-sealed) stalls is >= threshold.
//   - NewStalls24h: number of stalls detected in the last 24h is >= threshold.
//   - CostLost24h: cumulative cost lost in the last 24h is >= threshold (USD).
//
// A threshold value <= 0 disables that rule. If all thresholds are disabled,
// Evaluate always returns nil (no alert).
type AlertThresholds struct {
	LiveCount    int
	NewStalls24h int
	CostLost24h  float64
}

// AlertSeverity classifies how loud the alert should be.
type AlertSeverity string

const (
	AlertSeverityInfo     AlertSeverity = "info"
	AlertSeverityWarning  AlertSeverity = "warning"
	AlertSeverityCritical AlertSeverity = "critical"
)

// AlertDecision is the result of evaluating a stall snapshot against thresholds.
//
// Reasons is a human-readable list (one entry per fired rule) suitable for
// inclusion in a notification body.
type AlertDecision struct {
	Severity AlertSeverity
	Reasons  []string

	LiveCount       int
	NewStalls24h    int
	CostLost24h     float64
	TokensLost24h   int64
	TopRootCause    session.StallRootCause
	TopProvider     string
	RootCauseCounts map[session.StallRootCause]int
	ProviderCounts  map[string]int
}

// Evaluate checks whether liveStats (all currently-live stalls, never sealed)
// and recentStats (everything detected in the last 24h, live + sealed) cross
// any configured threshold. It returns nil when no rule fires.
//
// Severity escalates to critical when a rule overshoots its threshold by 3x.
func Evaluate(thresholds AlertThresholds, liveStats *session.StallStats, recentStats *session.StallStats) *AlertDecision {
	if liveStats == nil {
		liveStats = &session.StallStats{}
	}
	if recentStats == nil {
		recentStats = &session.StallStats{}
	}

	var reasons []string
	severity := AlertSeverityWarning

	escalate := func(actual, threshold float64) {
		if threshold > 0 && actual >= threshold*3 {
			severity = AlertSeverityCritical
		}
	}

	if thresholds.LiveCount > 0 && liveStats.LiveCount >= thresholds.LiveCount {
		reasons = append(reasons,
			formatReason("live stalls", float64(liveStats.LiveCount), float64(thresholds.LiveCount)))
		escalate(float64(liveStats.LiveCount), float64(thresholds.LiveCount))
	}

	if thresholds.NewStalls24h > 0 && recentStats.TotalCount >= thresholds.NewStalls24h {
		reasons = append(reasons,
			formatReason("new stalls (24h)", float64(recentStats.TotalCount), float64(thresholds.NewStalls24h)))
		escalate(float64(recentStats.TotalCount), float64(thresholds.NewStalls24h))
	}

	if thresholds.CostLost24h > 0 && recentStats.CostLostUSD >= thresholds.CostLost24h {
		reasons = append(reasons,
			formatReasonUSD("cost lost (24h)", recentStats.CostLostUSD, thresholds.CostLost24h))
		escalate(recentStats.CostLostUSD, thresholds.CostLost24h)
	}

	if len(reasons) == 0 {
		return nil
	}

	rootCauseCounts := make(map[session.StallRootCause]int, len(recentStats.ByRootCause))
	for k, v := range recentStats.ByRootCause {
		rootCauseCounts[k] = v.Count
	}
	providerCounts := make(map[string]int, len(recentStats.ByProvider))
	for k, v := range recentStats.ByProvider {
		providerCounts[k] = v.Count
	}

	return &AlertDecision{
		Severity:        severity,
		Reasons:         reasons,
		LiveCount:       liveStats.LiveCount,
		NewStalls24h:    recentStats.TotalCount,
		CostLost24h:     recentStats.CostLostUSD,
		TokensLost24h:   recentStats.TokensLost,
		TopRootCause:    topRootCause(recentStats.ByRootCause),
		TopProvider:     topProvider(recentStats.ByProvider),
		RootCauseCounts: rootCauseCounts,
		ProviderCounts:  providerCounts,
	}
}

func formatReason(label string, actual, threshold float64) string {
	return labelf(label, int(actual), int(threshold))
}

func formatReasonUSD(label string, actual, threshold float64) string {
	return usdReason(label, actual, threshold)
}

func labelf(label string, actual, threshold int) string {
	return label + " = " + itoa(actual) + " (threshold " + itoa(threshold) + ")"
}

func usdReason(label string, actual, threshold float64) string {
	return label + " = $" + ftoa(actual) + " (threshold $" + ftoa(threshold) + ")"
}

// topRootCause returns the StallRootCause with the highest Count, deterministic
// on ties (sorted by name).
func topRootCause(m map[session.StallRootCause]session.StallStatsRow) session.StallRootCause {
	if len(m) == 0 {
		return ""
	}
	type kv struct {
		k session.StallRootCause
		v int
	}
	items := make([]kv, 0, len(m))
	for k, row := range m {
		items = append(items, kv{k, row.Count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].v != items[j].v {
			return items[i].v > items[j].v
		}
		return items[i].k < items[j].k
	})
	return items[0].k
}

// topProvider mirrors topRootCause for the by-provider map.
func topProvider(m map[string]session.StallStatsRow) string {
	if len(m) == 0 {
		return ""
	}
	type kv struct {
		k string
		v int
	}
	items := make([]kv, 0, len(m))
	for k, row := range m {
		items = append(items, kv{k, row.Count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].v != items[j].v {
			return items[i].v > items[j].v
		}
		return items[i].k < items[j].k
	})
	return items[0].k
}

// itoa / ftoa: tiny local stringifiers to avoid pulling fmt into a hot eval path.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func ftoa(f float64) string {
	// 2 decimals, no scientific notation. Good enough for USD display.
	neg := f < 0
	if neg {
		f = -f
	}
	whole := int64(f)
	frac := int64((f - float64(whole)) * 100)
	if frac < 0 {
		frac = -frac
	}
	s := itoa(int(whole)) + "."
	if frac < 10 {
		s += "0"
	}
	s += itoa(int(frac))
	if neg {
		return "-" + s
	}
	return s
}
