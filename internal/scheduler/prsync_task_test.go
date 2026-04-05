package scheduler

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// ── Mock Platform for PRSyncTask ──

type mockPlatform struct {
	name string
	prs  []session.PullRequest
	err  error
}

func (m *mockPlatform) Name() session.PlatformName                               { return session.PlatformName(m.name) }
func (m *mockPlatform) GetPR(_ int) (*session.PullRequest, error)                { return nil, nil }
func (m *mockPlatform) GetPRForBranch(_ string) (*session.PullRequest, error)    { return nil, nil }
func (m *mockPlatform) ListPRsForBranch(_ string) ([]session.PullRequest, error) { return nil, nil }
func (m *mockPlatform) AddComment(_ int, _ string) error                         { return nil }
func (m *mockPlatform) UpdateComment(_ int64, _ string) error                    { return nil }
func (m *mockPlatform) ListComments(_ int) ([]session.PRComment, error)          { return nil, nil }
func (m *mockPlatform) ListRecentPRs(_ string, _ int) ([]session.PullRequest, error) {
	return m.prs, m.err
}

// ── Mock Store for PRSyncTask ──
// Only implements the methods used by PRSyncTask.

type mockPRStore struct {
	storage.Store // embed to satisfy interface (panics on unused methods)

	savedPRs []session.PullRequest
	links    []prLink
	sessions []session.Summary
	listErr  error
}

type prLink struct {
	sessionID session.ID
	owner     string
	repo      string
	number    int
}

func (m *mockPRStore) SavePullRequest(pr *session.PullRequest) error {
	m.savedPRs = append(m.savedPRs, *pr)
	return nil
}

func (m *mockPRStore) LinkSessionPR(id session.ID, owner, repo string, number int) error {
	m.links = append(m.links, prLink{id, owner, repo, number})
	return nil
}

func (m *mockPRStore) List(_ session.ListOptions) ([]session.Summary, error) {
	return m.sessions, m.listErr
}

// ── Tests ──

func TestPRSyncTask_Name(t *testing.T) {
	task := NewPRSyncTask(nil, nil, log.Default())
	if task.Name() != "pr_sync" {
		t.Errorf("Name() = %q, want %q", task.Name(), "pr_sync")
	}
}

func TestPRSyncTask_NoPlatform(t *testing.T) {
	task := NewPRSyncTask(nil, nil, log.Default())
	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() with nil platform should not error, got %v", err)
	}
}

func TestPRSyncTask_FetchAndSave(t *testing.T) {
	now := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)

	plat := &mockPlatform{
		name: "github",
		prs: []session.PullRequest{
			{
				Number:    42,
				Title:     "Add auth",
				Branch:    "feature/auth",
				State:     "open",
				RepoOwner: "org",
				RepoName:  "repo",
				CreatedAt: now,
				UpdatedAt: now,
			},
			{
				Number:    43,
				Title:     "Fix bug",
				Branch:    "fix/bug",
				State:     "merged",
				RepoOwner: "org",
				RepoName:  "repo",
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}

	store := &mockPRStore{
		sessions: []session.Summary{
			{ID: "sess-1", Branch: "feature/auth"},
		},
	}

	task := NewPRSyncTask(plat, store, log.Default())
	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Check PRs were saved.
	if len(store.savedPRs) != 2 {
		t.Errorf("saved PRs = %d, want 2", len(store.savedPRs))
	}

	// Check sessions were linked (store returns same sessions for every List call).
	// Both PRs have branches, so both will try to link. Each PR gets 1 session.
	if len(store.links) != 2 {
		t.Errorf("links = %d, want 2", len(store.links))
	}
}

func TestPRSyncTask_PlatformError(t *testing.T) {
	plat := &mockPlatform{
		name: "github",
		err:  context.DeadlineExceeded,
	}

	task := NewPRSyncTask(plat, &mockPRStore{}, log.Default())
	err := task.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from platform")
	}
}

func TestPRSyncTask_SkipsMissingRepoInfo(t *testing.T) {
	plat := &mockPlatform{
		name: "github",
		prs: []session.PullRequest{
			{
				Number: 1,
				Title:  "No repo info",
				Branch: "some-branch",
				State:  "open",
				// RepoOwner and RepoName are empty
			},
		},
	}

	store := &mockPRStore{
		sessions: []session.Summary{{ID: "sess-x"}},
	}

	task := NewPRSyncTask(plat, store, log.Default())
	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// PR saved, but no links because RepoOwner is empty.
	if len(store.savedPRs) != 1 {
		t.Errorf("saved PRs = %d, want 1", len(store.savedPRs))
	}
	if len(store.links) != 0 {
		t.Errorf("links = %d, want 0 (no repo info)", len(store.links))
	}
}
