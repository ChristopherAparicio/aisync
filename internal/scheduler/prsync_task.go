package scheduler

import (
	"context"
	"log"

	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/platform"
	"github.com/ChristopherAparicio/aisync/internal/platform/github"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// PRSyncTask fetches recent PRs from git platforms and links them
// to aisync sessions by matching branch names.
//
// Two modes:
//   - Single-platform (legacy): uses a single platform.Platform
//   - Multi-repo: iterates over projects with pr_enabled in config
type PRSyncTask struct {
	platform platform.Platform // single-platform mode (may be nil)
	cfg      *config.Config    // multi-repo mode (may be nil)
	store    storage.Store
	logger   *log.Logger
	limit    int // max PRs to fetch per run per project
}

// NewPRSyncTask creates a PR sync task in single-platform mode.
// Kept for backward compatibility.
func NewPRSyncTask(plat platform.Platform, store storage.Store, logger *log.Logger) *PRSyncTask {
	return &PRSyncTask{
		platform: plat,
		store:    store,
		logger:   logger,
		limit:    100,
	}
}

// NewMultiRepoPRSyncTask creates a PR sync task that iterates over all
// projects with pr_enabled in the config.
func NewMultiRepoPRSyncTask(cfg *config.Config, store storage.Store, logger *log.Logger) *PRSyncTask {
	return &PRSyncTask{
		cfg:    cfg,
		store:  store,
		logger: logger,
		limit:  100,
	}
}

// Name returns the task identifier.
func (t *PRSyncTask) Name() string { return "pr_sync" }

// Run fetches recent PRs and links sessions to them by branch name.
func (t *PRSyncTask) Run(_ context.Context) error {
	// Multi-repo mode: iterate projects with pr_enabled.
	if t.cfg != nil {
		return t.runMultiRepo()
	}

	// Single-platform mode (legacy).
	return t.runSinglePlatform()
}

// runSinglePlatform is the original single-repo sync.
func (t *PRSyncTask) runSinglePlatform() error {
	if t.platform == nil {
		t.logger.Printf("[pr_sync] no platform configured, skipping")
		return nil
	}

	prs, err := t.platform.ListRecentPRs("all", t.limit)
	if err != nil {
		return err
	}

	t.logger.Printf("[pr_sync] fetched %d PRs from %s", len(prs), t.platform.Name())
	saved, linked := t.savePRs(prs)
	t.logger.Printf("[pr_sync] done: %d PRs saved, %d session-PR links created", saved, linked)
	return nil
}

// runMultiRepo iterates over all projects with pr_enabled and syncs PRs for each.
func (t *PRSyncTask) runMultiRepo() error {
	classifiers := t.cfg.GetAllProjectClassifiers()
	if len(classifiers) == 0 {
		t.logger.Printf("[pr_sync] no projects configured, skipping")
		return nil
	}

	totalSaved, totalLinked := 0, 0
	for name, pc := range classifiers {
		if !pc.PREnabled {
			continue
		}
		if pc.ProjectPath == "" {
			t.logger.Printf("[pr_sync] %s: no project_path, skipping", name)
			continue
		}

		plat := github.New(pc.ProjectPath)
		if !plat.Available() {
			t.logger.Printf("[pr_sync] %s: gh CLI not available, skipping", name)
			continue
		}

		prs, err := plat.ListRecentPRs("all", t.limit)
		if err != nil {
			t.logger.Printf("[pr_sync] %s: %v", name, err)
			continue
		}

		t.logger.Printf("[pr_sync] %s: fetched %d PRs", name, len(prs))
		saved, linked := t.savePRs(prs)
		totalSaved += saved
		totalLinked += linked
	}

	t.logger.Printf("[pr_sync] multi-repo done: %d PRs saved, %d session-PR links created", totalSaved, totalLinked)
	return nil
}

// savePRs persists PRs and links sessions by branch. Returns (saved, linked) counts.
func (t *PRSyncTask) savePRs(prs []session.PullRequest) (int, int) {
	var saved, linked int
	for i := range prs {
		pr := &prs[i]

		if saveErr := t.store.SavePullRequest(pr); saveErr != nil {
			t.logger.Printf("[pr_sync] save PR #%d: %v", pr.Number, saveErr)
			continue
		}
		saved++

		if pr.RepoOwner == "" || pr.RepoName == "" || pr.Branch == "" {
			continue
		}

		summaries, listErr := t.store.List(session.ListOptions{
			Branch: pr.Branch,
			All:    true,
		})
		if listErr != nil {
			continue
		}

		for _, sm := range summaries {
			if linkErr := t.store.LinkSessionPR(sm.ID, pr.RepoOwner, pr.RepoName, pr.Number); linkErr != nil {
				t.logger.Printf("[pr_sync] link session %s → PR #%d: %v", sm.ID, pr.Number, linkErr)
				continue
			}
			linked++
		}
	}
	return saved, linked
}
