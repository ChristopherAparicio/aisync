package service

import (
	"context"
	"math"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// BudgetStatus computes the current budget status for all projects that have budgets configured.
func (s *SessionService) BudgetStatus(ctx context.Context) ([]session.BudgetStatus, error) {
	if s.cfg == nil {
		return nil, nil
	}

	projects, err := s.ListProjects(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	daysInMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, now.Location()).Day()
	dayOfMonth := now.Day()
	daysRemaining := daysInMonth - dayOfMonth

	var results []session.BudgetStatus

	for _, proj := range projects {
		pc := s.cfg.GetProjectClassifier(proj.RemoteURL, proj.ProjectPath)
		if pc == nil || pc.Budget == nil {
			continue
		}
		budget := pc.Budget
		if budget.MonthlyLimit <= 0 && budget.DailyLimit <= 0 {
			continue
		}

		alertThreshold := budget.AlertAtPercent
		if alertThreshold <= 0 {
			alertThreshold = 80
		}

		// Query token buckets for this project's monthly spend.
		buckets, qErr := s.store.QueryTokenBuckets("1h", monthStart, time.Time{}, proj.ProjectPath)
		if qErr != nil {
			continue
		}

		var monthlySpent, dailySpent float64
		var sessionCount int
		seenSessions := make(map[string]bool)
		for _, b := range buckets {
			monthlySpent += b.EstimatedCost
			if !b.BucketStart.Before(dayStart) {
				dailySpent += b.EstimatedCost
			}
			// Approximate session count from bucket data.
			if b.SessionCount > 0 {
				key := b.BucketStart.Format("2006-01-02") + b.ProjectPath
				if !seenSessions[key] {
					seenSessions[key] = true
					sessionCount += b.SessionCount
				}
			}
		}

		bs := session.BudgetStatus{
			ProjectName:    proj.DisplayName,
			ProjectPath:    proj.ProjectPath,
			RemoteURL:      proj.RemoteURL,
			AlertThreshold: alertThreshold,
			SessionCount:   sessionCount,
			DaysRemaining:  daysRemaining,
		}

		// Monthly budget.
		if budget.MonthlyLimit > 0 {
			bs.MonthlyLimit = budget.MonthlyLimit
			bs.MonthlySpent = monthlySpent
			bs.MonthlyPercent = math.Min(100, monthlySpent/budget.MonthlyLimit*100)
			if bs.MonthlyPercent >= 100 {
				bs.MonthlyAlert = "exceeded"
			} else if bs.MonthlyPercent >= alertThreshold {
				bs.MonthlyAlert = "warning"
			}

			// Project end-of-month spend.
			if dayOfMonth > 0 {
				dailyRate := monthlySpent / float64(dayOfMonth)
				bs.ProjectedMonth = dailyRate * float64(daysInMonth)
			}
		}

		// Daily budget.
		if budget.DailyLimit > 0 {
			bs.DailyLimit = budget.DailyLimit
			bs.DailySpent = dailySpent
			bs.DailyPercent = math.Min(100, dailySpent/budget.DailyLimit*100)
			if bs.DailyPercent >= 100 {
				bs.DailyAlert = "exceeded"
			} else if bs.DailyPercent >= alertThreshold {
				bs.DailyAlert = "warning"
			}
		}

		results = append(results, bs)
	}

	return results, nil
}
