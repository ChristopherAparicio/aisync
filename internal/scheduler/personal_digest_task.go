package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/notification"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// ── Personal Daily Digest Task ──

// PersonalDigestTask sends personal daily DM digests to each human user
// with a Slack ID. Each digest includes the user's individual stats
// (sessions, tokens, cost estimate, errors) plus a team average for comparison.
type PersonalDigestTask struct {
	store        storage.Store
	notifSvc     *notification.Service
	dashboardURL string
	logger       *log.Logger
}

// PersonalDigestConfig holds the configuration for creating a PersonalDigestTask.
type PersonalDigestConfig struct {
	Store        storage.Store
	NotifService *notification.Service
	DashboardURL string
	Logger       *log.Logger
}

// NewPersonalDigestTask creates a personal daily digest task.
func NewPersonalDigestTask(cfg PersonalDigestConfig) *PersonalDigestTask {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &PersonalDigestTask{
		store:        cfg.Store,
		notifSvc:     cfg.NotifService,
		dashboardURL: cfg.DashboardURL,
		logger:       logger,
	}
}

func (t *PersonalDigestTask) Name() string { return "personal_digest" }

func (t *PersonalDigestTask) Run(_ context.Context) error {
	if t.notifSvc == nil {
		return nil
	}
	if t.store == nil {
		return nil
	}

	now := time.Now()
	yesterday := now.AddDate(0, 0, -1)
	since := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, now.Location())
	until := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	period := yesterday.Format("2006-01-02")

	// Get all human users.
	humans, err := t.store.ListUsersByKind("human")
	if err != nil {
		return fmt.Errorf("personal_digest: list humans: %w", err)
	}

	// Filter to users who have a SlackID (required for DMs).
	var targets []*userTarget
	for _, u := range humans {
		if u.SlackID != "" {
			targets = append(targets, &userTarget{
				id:      string(u.ID),
				name:    u.Name,
				slackID: u.SlackID,
			})
		}
	}

	if len(targets) == 0 {
		t.logger.Println("[personal_digest] no human users with SlackID, skipping")
		return nil
	}

	// Get all owner stats for yesterday (global, no project filter).
	ownerStats, err := t.store.OwnerStats("", since, until)
	if err != nil {
		return fmt.Errorf("personal_digest: owner stats: %w", err)
	}

	// Build per-owner lookup and compute team average cost.
	ownerMap := make(map[string]*ownerData, len(ownerStats))
	var totalTeamCost float64
	var teamMembers int
	for _, os := range ownerStats {
		cost := estimateCostFromTokens(os.TotalTokens)
		ownerMap[string(os.OwnerID)] = &ownerData{
			sessionCount: os.SessionCount,
			totalTokens:  os.TotalTokens,
			totalCost:    cost,
			errorCount:   os.ErrorCount,
		}
		totalTeamCost += cost
		teamMembers++
	}

	teamAvgCost := 0.0
	if teamMembers > 0 {
		teamAvgCost = totalTeamCost / float64(teamMembers)
	}

	// Send a personal digest to each target user.
	var sent, skipped int
	for _, tgt := range targets {
		od, ok := ownerMap[tgt.id]
		if !ok || od.sessionCount == 0 {
			skipped++
			continue
		}

		event := notification.Event{
			Type:         notification.EventPersonalDaily,
			Severity:     notification.SeverityInfo,
			Timestamp:    now,
			OwnerID:      tgt.slackID, // Router uses OwnerID as DM target
			DashboardURL: t.dashboardURL,
			Data: notification.PersonalDigestData{
				Period:       period,
				OwnerName:    tgt.name,
				SessionCount: od.sessionCount,
				TotalTokens:  od.totalTokens,
				TotalCost:    od.totalCost,
				ErrorCount:   od.errorCount,
				TeamAvgCost:  teamAvgCost,
			},
		}

		// Sync dispatch — we want to confirm delivery for each user's DM.
		if err := t.notifSvc.NotifySync(event); err != nil {
			t.logger.Printf("[personal_digest] failed to send to %s (%s): %v",
				tgt.name, tgt.slackID, err)
			continue
		}
		sent++
	}

	t.logger.Printf("[personal_digest] sent %d DMs for %s (%d skipped, %d targets)",
		sent, period, skipped, len(targets))
	return nil
}

// ── Internal types ──

type userTarget struct {
	id      string
	name    string
	slackID string
}

type ownerData struct {
	sessionCount int
	totalTokens  int
	totalCost    float64
	errorCount   int
}

// estimateCostFromTokens provides a rough cost estimate based on token count.
// Uses a blended rate of $3 per million tokens (mix of input/output).
// This is a best-effort estimate when actual cost data isn't available.
func estimateCostFromTokens(tokens int) float64 {
	const blendedRatePerMillion = 3.0
	return float64(tokens) / 1_000_000.0 * blendedRatePerMillion
}
