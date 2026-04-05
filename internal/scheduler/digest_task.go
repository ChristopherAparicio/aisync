package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/notification"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// ── Daily Digest Task ──

// DailyDigestTask collects yesterday's data and fires an EventDailyDigest notification.
type DailyDigestTask struct {
	sessionSvc   service.SessionServicer
	store        storage.Store
	notifSvc     *notification.Service
	dashboardURL string
	logger       *log.Logger
}

// DailyDigestConfig holds the configuration for creating a DailyDigestTask.
type DailyDigestConfig struct {
	SessionService service.SessionServicer
	Store          storage.Store
	NotifService   *notification.Service
	DashboardURL   string
	Logger         *log.Logger
}

// NewDailyDigestTask creates a daily digest task.
func NewDailyDigestTask(cfg DailyDigestConfig) *DailyDigestTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &DailyDigestTask{
		sessionSvc:   cfg.SessionService,
		store:        cfg.Store,
		notifSvc:     cfg.NotifService,
		dashboardURL: cfg.DashboardURL,
		logger:       logger,
	}
}

func (t *DailyDigestTask) Name() string { return "daily_digest" }

func (t *DailyDigestTask) Run(ctx context.Context) error {
	if t.notifSvc == nil {
		return nil
	}

	now := time.Now()
	yesterday := now.AddDate(0, 0, -1)
	since := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, now.Location())
	until := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	period := yesterday.Format("2006-01-02")

	// Global stats for the day
	stats, err := t.sessionSvc.Stats(service.StatsRequest{All: true, Since: since, Until: until})
	if err != nil {
		return fmt.Errorf("daily_digest: stats: %w", err)
	}

	if stats.TotalSessions == 0 {
		t.logger.Println("[daily_digest] no sessions yesterday, skipping")
		return nil
	}

	// Owner breakdown
	var owners []notification.DigestOwnerData
	if t.store != nil {
		ownerStats, ownerErr := t.store.OwnerStats("", since, until)
		if ownerErr == nil {
			for _, os := range ownerStats {
				owners = append(owners, notification.DigestOwnerData{
					Name:         os.OwnerName,
					Kind:         os.OwnerKind,
					SessionCount: os.SessionCount,
					ErrorCount:   os.ErrorCount,
				})
			}
		}
	}

	// Per-project breakdown
	var projects []notification.DigestProjectData
	projectGroups, projErr := t.sessionSvc.ListProjects(ctx)
	if projErr == nil {
		for _, pg := range projectGroups {
			if ctx.Err() != nil {
				break
			}
			pStats, pErr := t.sessionSvc.Stats(service.StatsRequest{ProjectPath: pg.ProjectPath, Since: since, Until: until})
			if pErr != nil {
				continue
			}
			if pStats.TotalSessions == 0 {
				continue
			}
			name := pg.ProjectPath
			if pg.RemoteURL != "" {
				name = pg.RemoteURL
			}
			projects = append(projects, notification.DigestProjectData{
				Name:         name,
				SessionCount: pStats.TotalSessions,
				TotalTokens:  pStats.TotalTokens,
				TotalCost:    pStats.TotalCost,
			})
		}
	}

	digestData := notification.DigestData{
		Period:       period,
		SessionCount: stats.TotalSessions,
		TotalTokens:  stats.TotalTokens,
		TotalCost:    stats.TotalCost,
		ErrorCount:   countErrors(stats),
		Projects:     projects,
		Owners:       owners,
	}

	event := notification.Event{
		Type:         notification.EventDailyDigest,
		Severity:     notification.SeverityInfo,
		Timestamp:    now,
		DashboardURL: t.dashboardURL,
		Data:         digestData,
	}

	if err := t.notifSvc.NotifySync(event); err != nil {
		return fmt.Errorf("daily_digest: notify: %w", err)
	}

	t.logger.Printf("[daily_digest] sent for %s: %d sessions, %d tokens, $%.2f",
		period, stats.TotalSessions, stats.TotalTokens, stats.TotalCost)
	return nil
}

// ── Weekly Report Task ──

// WeeklyReportTask collects last week's data and fires an EventWeeklyReport notification.
type WeeklyReportTask struct {
	sessionSvc   service.SessionServicer
	store        storage.Store
	notifSvc     *notification.Service
	dashboardURL string
	logger       *log.Logger
}

// WeeklyReportConfig holds the configuration for creating a WeeklyReportTask.
type WeeklyReportConfig struct {
	SessionService service.SessionServicer
	Store          storage.Store
	NotifService   *notification.Service
	DashboardURL   string
	Logger         *log.Logger
}

// NewWeeklyReportTask creates a weekly report task.
func NewWeeklyReportTask(cfg WeeklyReportConfig) *WeeklyReportTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &WeeklyReportTask{
		sessionSvc:   cfg.SessionService,
		store:        cfg.Store,
		notifSvc:     cfg.NotifService,
		dashboardURL: cfg.DashboardURL,
		logger:       logger,
	}
}

func (t *WeeklyReportTask) Name() string { return "weekly_report" }

func (t *WeeklyReportTask) Run(ctx context.Context) error {
	if t.notifSvc == nil {
		return nil
	}

	now := time.Now()

	// Last week: Monday 00:00 to this Monday 00:00
	thisMonday := mostRecentMonday(now)
	lastMonday := thisMonday.AddDate(0, 0, -7)
	_, week := lastMonday.ISOWeek()
	period := fmt.Sprintf("W%02d %d", week, lastMonday.Year())

	// This week stats
	thisWeekStats, err := t.sessionSvc.Stats(service.StatsRequest{All: true, Since: lastMonday, Until: thisMonday})
	if err != nil {
		return fmt.Errorf("weekly_report: stats this week: %w", err)
	}

	// For trend deltas, we'd need last week's stats too.
	// For now, we report this week's stats and omit deltas.
	// TODO: Compare with the previous week when store supports time-ranged stats.

	if thisWeekStats.TotalSessions == 0 {
		t.logger.Println("[weekly_report] no sessions this week, skipping")
		return nil
	}

	// Owner breakdown
	var owners []notification.DigestOwnerData
	if t.store != nil {
		ownerStats, ownerErr := t.store.OwnerStats("", lastMonday, thisMonday)
		if ownerErr == nil {
			for _, os := range ownerStats {
				owners = append(owners, notification.DigestOwnerData{
					Name:         os.OwnerName,
					Kind:         os.OwnerKind,
					SessionCount: os.SessionCount,
					ErrorCount:   os.ErrorCount,
				})
			}
		}
	}

	digestData := notification.DigestData{
		Period:       period,
		SessionCount: thisWeekStats.TotalSessions,
		TotalTokens:  thisWeekStats.TotalTokens,
		TotalCost:    thisWeekStats.TotalCost,
		ErrorCount:   countErrors(thisWeekStats),
		Owners:       owners,
	}

	event := notification.Event{
		Type:         notification.EventWeeklyReport,
		Severity:     notification.SeverityInfo,
		Timestamp:    now,
		DashboardURL: t.dashboardURL,
		Data:         digestData,
	}

	if err := t.notifSvc.NotifySync(event); err != nil {
		return fmt.Errorf("weekly_report: notify: %w", err)
	}

	t.logger.Printf("[weekly_report] sent for %s: %d sessions, %d tokens, $%.2f",
		period, thisWeekStats.TotalSessions, thisWeekStats.TotalTokens, thisWeekStats.TotalCost)
	return nil
}

// ── Helpers ──

// countErrors returns the total error count across all matched sessions.
//
// Historically this summed errors from PerType buckets, which silently
// excluded sessions without a SessionType classification. Since Fix #10,
// StatsResult.TotalErrors is populated unconditionally in the main Stats()
// loop, so we can now return it directly — correct and O(1).
func countErrors(stats *service.StatsResult) int {
	return stats.TotalErrors
}

// mostRecentMonday returns the most recent Monday at 00:00:00 local time.
func mostRecentMonday(t time.Time) time.Time {
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7 // Sunday = 7
	}
	daysBack := weekday - 1 // Monday = 1, so 0 days back on Monday
	monday := t.AddDate(0, 0, -daysBack)
	return time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, t.Location())
}
