package sqlite

import (
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func testPR(number int) *session.PullRequest {
	now := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)
	return &session.PullRequest{
		Number:     number,
		Title:      "Add feature",
		Branch:     "feature/auth",
		BaseBranch: "main",
		State:      "open",
		Author:     "chris",
		URL:        "https://github.com/org/repo/pull/" + string(rune('0'+number)),
		RepoOwner:  "org",
		RepoName:   "repo",
		Additions:  42,
		Deletions:  10,
		Comments:   3,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func TestSavePullRequest(t *testing.T) {
	store := mustOpenStore(t)
	pr := testPR(1)

	if err := store.SavePullRequest(pr); err != nil {
		t.Fatalf("SavePullRequest() error = %v", err)
	}

	// Upsert: save again with different title.
	pr.Title = "Updated title"
	if err := store.SavePullRequest(pr); err != nil {
		t.Fatalf("SavePullRequest() upsert error = %v", err)
	}

	got, err := store.GetPullRequest("org", "repo", 1)
	if err != nil {
		t.Fatalf("GetPullRequest() error = %v", err)
	}
	if got.Title != "Updated title" {
		t.Errorf("Title = %q, want %q", got.Title, "Updated title")
	}
}

func TestGetPullRequest_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	_, err := store.GetPullRequest("org", "repo", 999)
	if err != session.ErrPRNotFound {
		t.Errorf("GetPullRequest() error = %v, want ErrPRNotFound", err)
	}
}

func TestListPullRequests(t *testing.T) {
	store := mustOpenStore(t)

	// Save 3 PRs with different states.
	for i, state := range []string{"open", "merged", "closed"} {
		pr := testPR(i + 1)
		pr.State = state
		pr.Number = i + 1
		pr.URL = "https://github.com/org/repo/pull/" + string(rune('1'+i))
		if err := store.SavePullRequest(pr); err != nil {
			t.Fatalf("SavePullRequest(%d) error = %v", i+1, err)
		}
	}

	// List all
	all, err := store.ListPullRequests("org", "repo", "", 100)
	if err != nil {
		t.Fatalf("ListPullRequests(all) error = %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListPullRequests(all) count = %d, want 3", len(all))
	}

	// Filter by state
	open, err := store.ListPullRequests("org", "repo", "open", 100)
	if err != nil {
		t.Fatalf("ListPullRequests(open) error = %v", err)
	}
	if len(open) != 1 {
		t.Errorf("ListPullRequests(open) count = %d, want 1", len(open))
	}

	// Limit
	limited, err := store.ListPullRequests("", "", "", 2)
	if err != nil {
		t.Fatalf("ListPullRequests(limit=2) error = %v", err)
	}
	if len(limited) != 2 {
		t.Errorf("ListPullRequests(limit=2) count = %d, want 2", len(limited))
	}
}

func TestLinkSessionPR(t *testing.T) {
	store := mustOpenStore(t)

	// Save a session and a PR.
	sess := testSession("sess-pr-1")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() session error = %v", err)
	}

	pr := testPR(42)
	pr.Number = 42
	if err := store.SavePullRequest(pr); err != nil {
		t.Fatalf("SavePullRequest() error = %v", err)
	}

	// Link them.
	if err := store.LinkSessionPR(sess.ID, "org", "repo", 42); err != nil {
		t.Fatalf("LinkSessionPR() error = %v", err)
	}

	// Idempotent: linking again should not error.
	if err := store.LinkSessionPR(sess.ID, "org", "repo", 42); err != nil {
		t.Fatalf("LinkSessionPR() duplicate error = %v", err)
	}
}

func TestGetSessionsForPR(t *testing.T) {
	store := mustOpenStore(t)

	// Save 2 sessions.
	for _, id := range []string{"sess-pr-a", "sess-pr-b"} {
		s := testSession(id)
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", id, err)
		}
	}

	// Save a PR and link both sessions.
	pr := testPR(10)
	pr.Number = 10
	if err := store.SavePullRequest(pr); err != nil {
		t.Fatalf("SavePullRequest() error = %v", err)
	}

	for _, id := range []string{"sess-pr-a", "sess-pr-b"} {
		if err := store.LinkSessionPR(session.ID(id), "org", "repo", 10); err != nil {
			t.Fatalf("LinkSessionPR(%s) error = %v", id, err)
		}
	}

	sessions, err := store.GetSessionsForPR("org", "repo", 10)
	if err != nil {
		t.Fatalf("GetSessionsForPR() error = %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("GetSessionsForPR() count = %d, want 2", len(sessions))
	}
}

func TestGetPRsForSession(t *testing.T) {
	store := mustOpenStore(t)

	// Save session.
	sess := testSession("sess-multipr")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Save 2 PRs and link both to the session.
	for _, n := range []int{20, 21} {
		pr := testPR(n)
		pr.Number = n
		if err := store.SavePullRequest(pr); err != nil {
			t.Fatalf("SavePullRequest(%d) error = %v", n, err)
		}
		if err := store.LinkSessionPR(sess.ID, "org", "repo", n); err != nil {
			t.Fatalf("LinkSessionPR(%d) error = %v", n, err)
		}
	}

	prs, err := store.GetPRsForSession(sess.ID)
	if err != nil {
		t.Fatalf("GetPRsForSession() error = %v", err)
	}
	if len(prs) != 2 {
		t.Errorf("GetPRsForSession() count = %d, want 2", len(prs))
	}
}

func TestGetPRsForSession_NoPRs(t *testing.T) {
	store := mustOpenStore(t)

	sess := testSession("sess-nopr")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	prs, err := store.GetPRsForSession(sess.ID)
	if err != nil {
		t.Fatalf("GetPRsForSession() error = %v", err)
	}
	if len(prs) != 0 {
		t.Errorf("GetPRsForSession() count = %d, want 0", len(prs))
	}
}

func TestListPRsWithSessions(t *testing.T) {
	store := mustOpenStore(t)

	// Save 2 sessions on different branches.
	sess1 := testSession("sess-lps-1")
	sess1.Branch = "feature/auth"
	sess2 := testSession("sess-lps-2")
	sess2.ID = "sess-lps-2"
	sess2.Branch = "fix/bug"
	for _, s := range []*session.Session{sess1, sess2} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	// Save 2 PRs with different states.
	pr1 := testPR(100)
	pr1.Number = 100
	pr1.Branch = "feature/auth"
	pr1.State = "open"

	pr2 := testPR(101)
	pr2.Number = 101
	pr2.Branch = "fix/bug"
	pr2.State = "merged"

	for _, p := range []*session.PullRequest{pr1, pr2} {
		if err := store.SavePullRequest(p); err != nil {
			t.Fatalf("SavePullRequest(%d) error = %v", p.Number, err)
		}
	}

	// Link sessions to PRs.
	if err := store.LinkSessionPR(sess1.ID, "org", "repo", 100); err != nil {
		t.Fatal(err)
	}
	if err := store.LinkSessionPR(sess2.ID, "org", "repo", 101); err != nil {
		t.Fatal(err)
	}

	// List all with sessions.
	results, err := store.ListPRsWithSessions("", "", "", 100)
	if err != nil {
		t.Fatalf("ListPRsWithSessions() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("ListPRsWithSessions() count = %d, want 2", len(results))
	}

	// Check that sessions are populated.
	for _, r := range results {
		if r.SessionCount != 1 {
			t.Errorf("PR #%d: SessionCount = %d, want 1", r.PR.Number, r.SessionCount)
		}
		if len(r.Sessions) != 1 {
			t.Errorf("PR #%d: len(Sessions) = %d, want 1", r.PR.Number, len(r.Sessions))
		}
	}

	// Filter by state.
	openOnly, err := store.ListPRsWithSessions("", "", "open", 100)
	if err != nil {
		t.Fatalf("ListPRsWithSessions(open) error = %v", err)
	}
	if len(openOnly) != 1 {
		t.Errorf("ListPRsWithSessions(open) count = %d, want 1", len(openOnly))
	}
}

func TestGetPRByBranch(t *testing.T) {
	store := mustOpenStore(t)

	// Save 2 PRs on different branches.
	pr1 := testPR(60)
	pr1.Number = 60
	pr1.Branch = "feature/auth"
	pr1.Title = "Auth PR"
	pr1.UpdatedAt = time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)

	pr2 := testPR(61)
	pr2.Number = 61
	pr2.Branch = "feature/auth"
	pr2.Title = "Auth PR v2"
	pr2.UpdatedAt = time.Date(2026, 3, 16, 10, 0, 0, 0, time.UTC) // more recent

	if err := store.SavePullRequest(pr1); err != nil {
		t.Fatalf("SavePullRequest(60) error = %v", err)
	}
	if err := store.SavePullRequest(pr2); err != nil {
		t.Fatalf("SavePullRequest(61) error = %v", err)
	}

	// GetPRByBranch should return the most recently updated PR.
	got, err := store.GetPRByBranch("feature/auth")
	if err != nil {
		t.Fatalf("GetPRByBranch() error = %v", err)
	}
	if got.Number != 61 {
		t.Errorf("Number = %d, want 61 (most recent)", got.Number)
	}
	if got.Title != "Auth PR v2" {
		t.Errorf("Title = %q, want %q", got.Title, "Auth PR v2")
	}
}

func TestGetPRByBranch_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	_, err := store.GetPRByBranch("nonexistent-branch")
	if err != session.ErrPRNotFound {
		t.Errorf("GetPRByBranch() error = %v, want ErrPRNotFound", err)
	}
}

func TestGetPRByBranch_SingleMatch(t *testing.T) {
	store := mustOpenStore(t)

	pr := testPR(70)
	pr.Number = 70
	pr.Branch = "fix/unique-bug"
	pr.Title = "Fix unique bug"

	if err := store.SavePullRequest(pr); err != nil {
		t.Fatalf("SavePullRequest() error = %v", err)
	}

	got, err := store.GetPRByBranch("fix/unique-bug")
	if err != nil {
		t.Fatalf("GetPRByBranch() error = %v", err)
	}
	if got.Number != 70 {
		t.Errorf("Number = %d, want 70", got.Number)
	}
	if got.Branch != "fix/unique-bug" {
		t.Errorf("Branch = %q, want %q", got.Branch, "fix/unique-bug")
	}
}

func TestSavePullRequest_MergedAt(t *testing.T) {
	store := mustOpenStore(t)

	now := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)
	merged := now.Add(2 * time.Hour)

	pr := testPR(50)
	pr.Number = 50
	pr.State = "merged"
	pr.MergedAt = merged

	if err := store.SavePullRequest(pr); err != nil {
		t.Fatalf("SavePullRequest() error = %v", err)
	}

	got, err := store.GetPullRequest("org", "repo", 50)
	if err != nil {
		t.Fatalf("GetPullRequest() error = %v", err)
	}
	if got.MergedAt.IsZero() {
		t.Error("MergedAt is zero, expected non-zero")
	}
	if got.State != "merged" {
		t.Errorf("State = %q, want %q", got.State, "merged")
	}
}
