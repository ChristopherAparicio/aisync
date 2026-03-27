package security

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// Detector is the composite security analyzer that runs all registered rules.
type Detector struct {
	rules []Rule
	store storage.Store
}

// NewDetector creates a new security detector with the given rules.
func NewDetector(store storage.Store, rules ...Rule) *Detector {
	return &Detector{rules: rules, store: store}
}

// ScanSession runs all rules against a session and returns the results.
func (d *Detector) ScanSession(sess *session.Session) *ScanResult {
	result := &ScanResult{
		SessionID: sess.ID,
		ScannedAt: time.Now(),
	}

	for _, rule := range d.rules {
		alerts := rule.Scan(sess)
		for i := range alerts {
			a := &alerts[i]
			// Generate deterministic ID from session + rule + message index.
			a.ID = alertID(sess.ID, a.Rule, a.MessageIndex)
			if a.Timestamp.IsZero() {
				a.Timestamp = time.Now()
			}
			// Truncate evidence to prevent storing huge payloads.
			if len(a.Evidence) > 500 {
				a.Evidence = a.Evidence[:500] + "..."
			}
		}
		result.Alerts = append(result.Alerts, alerts...)
	}

	// Count by severity.
	for _, a := range result.Alerts {
		switch a.Severity {
		case SeverityCritical:
			result.CriticalCount++
		case SeverityHigh:
			result.HighCount++
		case SeverityMedium:
			result.MediumCount++
		case SeverityLow:
			result.LowCount++
		}
	}
	result.AlertCount = len(result.Alerts)
	result.RiskScore = computeRiskScore(result)

	return result
}

// ScanProject analyzes all sessions for a project.
func (d *Detector) ScanProject(projectPath string) (*ProjectSecuritySummary, error) {
	opts := session.ListOptions{ProjectPath: projectPath}
	if projectPath == "" {
		opts.All = true
	}
	summaries, err := d.store.List(opts)
	if err != nil {
		return nil, err
	}

	summary := &ProjectSecuritySummary{
		TotalSessions: len(summaries),
	}
	catCounts := make(map[AlertCategory]int)
	var riskySessions []SessionRisk

	for _, sm := range summaries {
		sess, getErr := d.store.Get(sm.ID)
		if getErr != nil {
			continue
		}

		result := d.ScanSession(sess)
		if result.AlertCount > 0 {
			summary.SessionsWithAlerts++
			summary.TotalAlerts += result.AlertCount
			summary.CriticalAlerts += result.CriticalCount
			summary.HighAlerts += result.HighCount

			// Track categories.
			for _, a := range result.Alerts {
				catCounts[a.Category]++
			}

			// Track top category for this session.
			topCat := result.Alerts[0].Category
			riskySessions = append(riskySessions, SessionRisk{
				SessionID:   sess.ID,
				Summary:     sess.Summary,
				RiskScore:   result.RiskScore,
				AlertCount:  result.AlertCount,
				TopCategory: topCat,
			})

			// Keep recent alerts (last 10).
			for _, a := range result.Alerts {
				summary.RecentAlerts = append(summary.RecentAlerts, a)
			}
		}
		summary.AvgRiskScore += float64(result.RiskScore)
	}

	if summary.TotalSessions > 0 {
		summary.AvgRiskScore /= float64(summary.TotalSessions)
	}

	// Sort and limit.
	for cat, count := range catCounts {
		summary.TopCategories = append(summary.TopCategories, CategoryCount{
			Category: cat, Count: count,
		})
	}
	sort.Slice(summary.TopCategories, func(i, j int) bool {
		return summary.TopCategories[i].Count > summary.TopCategories[j].Count
	})

	sort.Slice(riskySessions, func(i, j int) bool {
		return riskySessions[i].RiskScore > riskySessions[j].RiskScore
	})
	if len(riskySessions) > 10 {
		riskySessions = riskySessions[:10]
	}
	summary.RiskiestSessions = riskySessions

	if len(summary.RecentAlerts) > 10 {
		summary.RecentAlerts = summary.RecentAlerts[len(summary.RecentAlerts)-10:]
	}

	return summary, nil
}

func alertID(sessID session.ID, rule string, msgIdx int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", sessID, rule, msgIdx)))
	return fmt.Sprintf("alert_%x", h[:8])
}

func computeRiskScore(r *ScanResult) int {
	score := r.CriticalCount*40 + r.HighCount*20 + r.MediumCount*10 + r.LowCount*3
	if score > 100 {
		score = 100
	}
	return score
}
