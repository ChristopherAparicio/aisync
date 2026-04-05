package github

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestClient_Name(t *testing.T) {
	c := New("/tmp/test")
	if c.Name() != session.PlatformGitHub {
		t.Errorf("Name() = %q, want %q", c.Name(), session.PlatformGitHub)
	}
}

func TestGhPR_toDomain(t *testing.T) {
	now := time.Date(2026, 2, 17, 10, 0, 0, 0, time.UTC)
	pr := ghPR{
		Number:      42,
		Title:       "Add OAuth2 support",
		HeadRefName: "feature/auth",
		BaseRefName: "main",
		State:       "OPEN",
		URL:         "https://github.com/org/repo/pull/42",
		Author:      ghAuthor{Login: "chris"},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	result := pr.toDomain()

	if result.Number != 42 {
		t.Errorf("Number = %d, want 42", result.Number)
	}
	if result.Title != "Add OAuth2 support" {
		t.Errorf("Title = %q, want %q", result.Title, "Add OAuth2 support")
	}
	if result.Branch != "feature/auth" {
		t.Errorf("Branch = %q, want %q", result.Branch, "feature/auth")
	}
	if result.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q, want %q", result.BaseBranch, "main")
	}
	if result.State != "open" {
		t.Errorf("State = %q, want %q (should be lowercased)", result.State, "open")
	}
	if result.Author != "chris" {
		t.Errorf("Author = %q, want %q", result.Author, "chris")
	}
	if result.URL != "https://github.com/org/repo/pull/42" {
		t.Errorf("URL = %q, want full URL", result.URL)
	}
}

func TestGhPR_parseJSON(t *testing.T) {
	// Simulate real gh output
	jsonData := `[
		{
			"number": 42,
			"title": "Add OAuth2 support",
			"headRefName": "feature/auth",
			"baseRefName": "main",
			"state": "OPEN",
			"url": "https://github.com/org/repo/pull/42",
			"author": {"login": "chris"},
			"createdAt": "2026-02-17T10:00:00Z",
			"updatedAt": "2026-02-17T12:00:00Z"
		}
	]`

	var prs []ghPR
	if err := json.Unmarshal([]byte(jsonData), &prs); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if len(prs) != 1 {
		t.Fatalf("len(prs) = %d, want 1", len(prs))
	}

	pr := prs[0].toDomain()
	if pr.Number != 42 {
		t.Errorf("Number = %d, want 42", pr.Number)
	}
	if pr.State != "open" {
		t.Errorf("State = %q, want %q", pr.State, "open")
	}
}

func TestGhComment_parseJSON(t *testing.T) {
	jsonData := `[
		{
			"id": 12345,
			"body": "LGTM!",
			"user": {"login": "reviewer"},
			"created_at": "2026-02-17T14:00:00Z"
		},
		{
			"id": 12346,
			"body": "<!-- aisync --> Session summary here",
			"user": {"login": "aisync-bot"},
			"created_at": "2026-02-17T14:30:00Z"
		}
	]`

	var comments []ghComment
	if err := json.Unmarshal([]byte(jsonData), &comments); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if len(comments) != 2 {
		t.Fatalf("len(comments) = %d, want 2", len(comments))
	}

	if comments[0].ID != 12345 {
		t.Errorf("comments[0].ID = %d, want 12345", comments[0].ID)
	}
	if comments[0].User.Login != "reviewer" {
		t.Errorf("comments[0].User.Login = %q, want %q", comments[0].User.Login, "reviewer")
	}
	if comments[1].Body != "<!-- aisync --> Session summary here" {
		t.Errorf("comments[1].Body = %q", comments[1].Body)
	}
}

func TestJsonEscape(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`hello`, `"hello"`},
		{`"quoted"`, `"\"quoted\""`},
		{"line1\nline2", `"line1\nline2"`},
	}

	for _, tt := range tests {
		got := jsonEscape(tt.input)
		if got != tt.want {
			t.Errorf("jsonEscape(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractRepoFromURL(t *testing.T) {
	tests := []struct {
		url       string
		wantOwner string
		wantRepo  string
	}{
		{
			url:       "https://github.com/org/repo/pull/42",
			wantOwner: "org",
			wantRepo:  "repo",
		},
		{
			url:       "https://github.com/ChristopherAparicio/aisync/pull/1",
			wantOwner: "ChristopherAparicio",
			wantRepo:  "aisync",
		},
		{
			url:       "https://github.com/owner/repo",
			wantOwner: "owner",
			wantRepo:  "repo",
		},
		{
			url:       "not-a-url",
			wantOwner: "",
			wantRepo:  "",
		},
		{
			url:       "",
			wantOwner: "",
			wantRepo:  "",
		},
		{
			url:       "https://gitlab.com/org/repo/merge_requests/1",
			wantOwner: "",
			wantRepo:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			owner, repo := extractRepoFromURL(tt.url)
			if owner != tt.wantOwner {
				t.Errorf("owner = %q, want %q", owner, tt.wantOwner)
			}
			if repo != tt.wantRepo {
				t.Errorf("repo = %q, want %q", repo, tt.wantRepo)
			}
		})
	}
}

func TestGhPR_toDomain_EnrichedFields(t *testing.T) {
	now := time.Date(2026, 2, 17, 10, 0, 0, 0, time.UTC)
	merged := now.Add(2 * time.Hour)

	pr := ghPR{
		Number:      99,
		Title:       "Big refactor",
		HeadRefName: "refactor/core",
		BaseRefName: "main",
		State:       "MERGED",
		URL:         "https://github.com/acme/app/pull/99",
		Author:      ghAuthor{Login: "dev"},
		CreatedAt:   now,
		UpdatedAt:   now,
		MergedAt:    merged,
		Additions:   150,
		Deletions:   42,
		Comments:    make([]json.RawMessage, 7), // 7 comment objects
	}

	result := pr.toDomain()

	if result.State != "merged" {
		t.Errorf("State = %q, want %q", result.State, "merged")
	}
	// RepoOwner/RepoName are set by the public methods (GetPR, ListRecentPRs)
	// after calling toDomain() + extractRepoFromURL. toDomain() itself doesn't set them.
	// We test extractRepoFromURL separately above.
	if result.Additions != 150 {
		t.Errorf("Additions = %d, want 150", result.Additions)
	}
	if result.Deletions != 42 {
		t.Errorf("Deletions = %d, want 42", result.Deletions)
	}
	if result.Comments != 7 {
		t.Errorf("Comments = %d, want 7", result.Comments)
	}
	if result.MergedAt.IsZero() {
		t.Error("MergedAt is zero, expected non-zero")
	}
}

// Client is a concrete type that implements the Platform operations for GitHub.
// No compile-time interface check — there is no central Platform interface.
