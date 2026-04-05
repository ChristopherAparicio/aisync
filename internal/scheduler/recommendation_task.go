package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/notification"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// ── Recommendation Task ──

// RecommendationTask generates recommendations for each project, persists them
// to the store (with fingerprint dedup), expires stale ones, reactivates snoozed,
// and fires notifications for high-priority items.
type RecommendationTask struct {
	sessionSvc   service.SessionServicer
	store        storage.Store
	notifSvc     *notification.Service
	dashboardURL string
	logger       *log.Logger

	// expireAfter controls how long an active recommendation lives
	// without being refreshed before it's marked expired. Default: 14 days.
	expireAfter time.Duration
}

// RecommendationConfig holds the configuration for creating a RecommendationTask.
type RecommendationConfig struct {
	SessionService service.SessionServicer
	Store          storage.Store
	NotifService   *notification.Service
	DashboardURL   string
	Logger         *log.Logger

	// ExpireAfter controls stale recommendation TTL. Default: 14 days.
	ExpireAfter time.Duration
}

// NewRecommendationTask creates a recommendation task.
func NewRecommendationTask(cfg RecommendationConfig) *RecommendationTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	expiry := cfg.ExpireAfter
	if expiry == 0 {
		expiry = 14 * 24 * time.Hour // 14 days default
	}
	return &RecommendationTask{
		sessionSvc:   cfg.SessionService,
		store:        cfg.Store,
		notifSvc:     cfg.NotifService,
		dashboardURL: cfg.DashboardURL,
		logger:       logger,
		expireAfter:  expiry,
	}
}

func (t *RecommendationTask) Name() string { return "recommendations" }

func (t *RecommendationTask) Run(ctx context.Context) error {
	// Step 1: Housekeeping — expire stale recs and reactivate snoozed ones.
	if t.store != nil {
		if n, err := t.store.ExpireRecommendations(t.expireAfter); err != nil {
			t.logger.Printf("[recommendations] expire error: %v", err)
		} else if n > 0 {
			t.logger.Printf("[recommendations] expired %d stale recommendations", n)
		}

		if n, err := t.store.ReactivateSnoozed(); err != nil {
			t.logger.Printf("[recommendations] reactivate snoozed error: %v", err)
		} else if n > 0 {
			t.logger.Printf("[recommendations] reactivated %d snoozed recommendations", n)
		}
	}

	// Step 2: Generate fresh recommendations for all projects.
	projects, err := t.sessionSvc.ListProjects(ctx)
	if err != nil {
		return fmt.Errorf("recommendations: list projects: %w", err)
	}

	if len(projects) == 0 {
		t.logger.Println("[recommendations] no projects found, skipping")
		return nil
	}

	now := time.Now()
	var totalPersisted, totalSent int

	for _, pg := range projects {
		if ctx.Err() != nil {
			break
		}

		recs, recErr := t.sessionSvc.GenerateRecommendations(ctx, pg.ProjectPath)
		if recErr != nil {
			t.logger.Printf("[recommendations] error for %s: %v", pg.ProjectPath, recErr)
			continue
		}

		if len(recs) == 0 {
			continue
		}

		// Step 3: Persist recommendations to store (upsert by fingerprint).
		if t.store != nil {
			for _, r := range recs {
				fp := session.RecommendationFingerprint(r.Type, pg.ProjectPath, r.Agent, r.Skill)
				rec := &session.RecommendationRecord{
					ProjectPath: pg.ProjectPath,
					Type:        r.Type,
					Priority:    r.Priority,
					Source:      session.RecSourceDeterministic,
					Icon:        r.Icon,
					Title:       r.Title,
					Message:     r.Message,
					Impact:      r.Impact,
					Agent:       r.Agent,
					Skill:       r.Skill,
					Status:      session.RecStatusActive,
					Fingerprint: fp,
					CreatedAt:   now,
					UpdatedAt:   now,
				}
				if upsertErr := t.store.UpsertRecommendation(rec); upsertErr != nil {
					t.logger.Printf("[recommendations] upsert error for %s/%s: %v",
						pg.ProjectPath, r.Type, upsertErr)
					continue
				}
				totalPersisted++
			}
		}

		// Step 4: Notify if there are high-priority recommendations.
		if t.notifSvc == nil {
			continue
		}

		var highCount int
		for _, r := range recs {
			if r.Priority == "high" {
				highCount++
			}
		}

		if highCount == 0 {
			continue
		}

		// Build notification payload — include all high and medium priority items (max 10).
		var items []notification.RecommendationItem
		for _, r := range recs {
			if r.Priority == "low" {
				continue
			}
			items = append(items, notification.RecommendationItem{
				Type:     r.Type,
				Priority: r.Priority,
				Icon:     r.Icon,
				Title:    r.Title,
				Message:  r.Message,
				Impact:   r.Impact,
			})
			if len(items) >= 10 {
				break
			}
		}

		projectName := pg.ProjectPath
		if pg.RemoteURL != "" {
			projectName = pg.RemoteURL
		}

		severity := notification.SeverityInfo
		if highCount >= 3 {
			severity = notification.SeverityWarning
		}

		event := notification.Event{
			Type:         notification.EventRecommendation,
			Severity:     severity,
			Timestamp:    now,
			Project:      projectName,
			ProjectPath:  pg.ProjectPath,
			DashboardURL: t.dashboardURL,
			Data: notification.RecommendationData{
				TotalCount: len(recs),
				HighCount:  highCount,
				Items:      items,
			},
		}

		if notifErr := t.notifSvc.NotifySync(event); notifErr != nil {
			t.logger.Printf("[recommendations] notify error for %s: %v", projectName, notifErr)
			continue
		}

		totalSent++
		t.logger.Printf("[recommendations] sent for %s: %d total, %d high, %d items",
			projectName, len(recs), highCount, len(items))
	}

	t.logger.Printf("[recommendations] done: %d persisted, %d notifications sent",
		totalPersisted, totalSent)
	return nil
}
