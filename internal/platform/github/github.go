// Package github implements the Platform operations for GitHub using the gh CLI.
// It shells out to `gh` instead of using the API directly, which avoids
// token management — users already have `gh auth login` configured.
package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// prJSONFields is the set of fields requested from gh CLI for PR queries.
const prJSONFields = "number,title,headRefName,baseRefName,state,url,author,createdAt,updatedAt,mergedAt,closedAt,additions,deletions,comments"

// Client implements the Platform operations for GitHub.
type Client struct {
	// repoDir is the git repository directory (for running gh commands in context).
	repoDir string
}

// New creates a GitHub platform client.
// The repoDir should be the root of the git repository so gh can infer
// the owner/repo from the git remote.
func New(repoDir string) *Client {
	return &Client{repoDir: repoDir}
}

// Name returns the platform identifier.
func (c *Client) Name() session.PlatformName {
	return session.PlatformGitHub
}

// Available checks whether the gh CLI is installed and authenticated.
func (c *Client) Available() bool {
	_, err := c.run("auth", "status")
	return err == nil
}

// GetPRForBranch finds the most recent open PR for a given branch.
func (c *Client) GetPRForBranch(branch string) (*session.PullRequest, error) {
	out, err := c.run(
		"pr", "list",
		"--head", branch,
		"--state", "open",
		"--limit", "1",
		"--json", prJSONFields,
	)
	if err != nil {
		return nil, fmt.Errorf("listing PRs for branch %q: %w", branch, err)
	}

	var prs []ghPR
	if jsonErr := json.Unmarshal([]byte(out), &prs); jsonErr != nil {
		return nil, fmt.Errorf("parsing PR list: %w", jsonErr)
	}

	if len(prs) == 0 {
		return nil, session.ErrPRNotFound
	}

	pr := prs[0].toDomain()
	if owner, repo := extractRepoFromURL(pr.URL); owner != "" {
		pr.RepoOwner = owner
		pr.RepoName = repo
	}
	return pr, nil
}

// GetPR retrieves a PR by number.
func (c *Client) GetPR(number int) (*session.PullRequest, error) {
	out, err := c.run(
		"pr", "view",
		strconv.Itoa(number),
		"--json", prJSONFields,
	)
	if err != nil {
		return nil, fmt.Errorf("getting PR #%d: %w", number, session.ErrPRNotFound)
	}

	var pr ghPR
	if jsonErr := json.Unmarshal([]byte(out), &pr); jsonErr != nil {
		return nil, fmt.Errorf("parsing PR #%d: %w", number, jsonErr)
	}

	result := pr.toDomain()
	if owner, repo := extractRepoFromURL(result.URL); owner != "" {
		result.RepoOwner = owner
		result.RepoName = repo
	}
	return result, nil
}

// ListPRsForBranch returns all PRs (open, closed, merged) for a branch.
func (c *Client) ListPRsForBranch(branch string) ([]session.PullRequest, error) {
	out, err := c.run(
		"pr", "list",
		"--head", branch,
		"--state", "all",
		"--limit", "100",
		"--json", prJSONFields,
	)
	if err != nil {
		return nil, fmt.Errorf("listing PRs for branch %q: %w", branch, err)
	}

	var prs []ghPR
	if jsonErr := json.Unmarshal([]byte(out), &prs); jsonErr != nil {
		return nil, fmt.Errorf("parsing PR list: %w", jsonErr)
	}

	result := make([]session.PullRequest, 0, len(prs))
	for _, pr := range prs {
		domPR := pr.toDomain()
		if owner, repo := extractRepoFromURL(domPR.URL); owner != "" {
			domPR.RepoOwner = owner
			domPR.RepoName = repo
		}
		result = append(result, *domPR)
	}

	return result, nil
}

// ListRecentPRs returns recent PRs for the repository (all branches).
func (c *Client) ListRecentPRs(state string, limit int) ([]session.PullRequest, error) {
	if limit <= 0 {
		limit = 50
	}
	if state == "" {
		state = "all"
	}
	out, err := c.run(
		"pr", "list",
		"--state", state,
		"--limit", strconv.Itoa(limit),
		"--json", prJSONFields,
	)
	if err != nil {
		return nil, fmt.Errorf("listing recent PRs: %w", err)
	}

	var prs []ghPR
	if jsonErr := json.Unmarshal([]byte(out), &prs); jsonErr != nil {
		return nil, fmt.Errorf("parsing PR list: %w", jsonErr)
	}

	result := make([]session.PullRequest, 0, len(prs))
	for _, pr := range prs {
		domPR := pr.toDomain()
		if owner, repo := extractRepoFromURL(domPR.URL); owner != "" {
			domPR.RepoOwner = owner
			domPR.RepoName = repo
		}
		result = append(result, *domPR)
	}

	return result, nil
}

// AddComment posts a comment on a PR.
func (c *Client) AddComment(prNumber int, body string) error {
	_, err := c.run("pr", "comment", strconv.Itoa(prNumber), "--body", body)
	if err != nil {
		return fmt.Errorf("commenting on PR #%d: %w", prNumber, err)
	}
	return nil
}

// UpdateComment updates an existing comment by ID using the GitHub API via gh.
func (c *Client) UpdateComment(commentID int64, body string) error {
	// gh api doesn't have a direct "update comment" command,
	// so we use the REST API endpoint via gh api.
	endpoint := fmt.Sprintf("repos/{owner}/{repo}/issues/comments/%d", commentID)
	payload := fmt.Sprintf(`{"body": %s}`, jsonEscape(body))
	_, err := c.run("api", endpoint, "--method", "PATCH", "--input", "-", "--silent")
	if err != nil {
		// Fallback: use --field directly
		_, err = c.run("api", endpoint, "--method", "PATCH", "-f", "body="+payload)
		if err != nil {
			return fmt.Errorf("updating comment %d: %w", commentID, err)
		}
	}
	return nil
}

// ListComments returns comments on a PR.
func (c *Client) ListComments(prNumber int) ([]session.PRComment, error) {
	endpoint := fmt.Sprintf("repos/{owner}/{repo}/issues/%d/comments", prNumber)
	out, err := c.run("api", endpoint, "--paginate", "--jq", ".[].id,.[].body,.[].user.login,.[].created_at")
	if err != nil {
		// Try a simpler approach: get full JSON
		out, err = c.run("api", endpoint, "--paginate")
		if err != nil {
			return nil, fmt.Errorf("listing comments on PR #%d: %w", prNumber, err)
		}
	}

	var ghComments []ghComment
	if jsonErr := json.Unmarshal([]byte(out), &ghComments); jsonErr != nil {
		return nil, fmt.Errorf("parsing comments: %w", jsonErr)
	}

	result := make([]session.PRComment, 0, len(ghComments))
	for _, c := range ghComments {
		result = append(result, session.PRComment{
			ID:        c.ID,
			Body:      c.Body,
			Author:    c.User.Login,
			CreatedAt: c.CreatedAt,
		})
	}

	return result, nil
}

// run executes a gh CLI command and returns the trimmed output.
func (c *Client) run(args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = c.repoDir

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}

	return strings.TrimSpace(string(out)), nil
}

// jsonEscape returns a JSON-encoded string value.
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// --- gh CLI JSON structures ---

type ghPR struct {
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
	MergedAt    time.Time         `json:"mergedAt"`
	ClosedAt    time.Time         `json:"closedAt"`
	Title       string            `json:"title"`
	HeadRefName string            `json:"headRefName"`
	BaseRefName string            `json:"baseRefName"`
	State       string            `json:"state"`
	URL         string            `json:"url"`
	Author      ghAuthor          `json:"author"`
	Comments    []json.RawMessage `json:"comments"` // gh returns comment objects, we just count them
	Number      int               `json:"number"`
	Additions   int               `json:"additions"`
	Deletions   int               `json:"deletions"`
}

func (p *ghPR) toDomain() *session.PullRequest {
	state := strings.ToLower(p.State)
	// gh CLI returns "MERGED" as a state
	if state == "merged" && p.MergedAt.IsZero() {
		// state is merged but mergedAt not present — keep state as-is
	}
	return &session.PullRequest{
		Number:     p.Number,
		Title:      p.Title,
		Branch:     p.HeadRefName,
		BaseBranch: p.BaseRefName,
		State:      state,
		URL:        p.URL,
		Author:     p.Author.Login,
		CreatedAt:  p.CreatedAt,
		UpdatedAt:  p.UpdatedAt,
		MergedAt:   p.MergedAt,
		ClosedAt:   p.ClosedAt,
		Additions:  p.Additions,
		Deletions:  p.Deletions,
		Comments:   len(p.Comments),
	}
}

// extractRepoFromURL extracts owner and repo from a GitHub URL.
// e.g. "https://github.com/owner/repo/pull/123" → ("owner", "repo")
func extractRepoFromURL(ghURL string) (owner, repo string) {
	// URL format: https://github.com/{owner}/{repo}/pull/{number}
	parts := strings.Split(ghURL, "/")
	for i, p := range parts {
		if p == "github.com" && i+2 < len(parts) {
			return parts[i+1], parts[i+2]
		}
	}
	return "", ""
}

type ghAuthor struct {
	Login string `json:"login"`
}

type ghComment struct {
	CreatedAt time.Time `json:"created_at"`
	Body      string    `json:"body"`
	User      ghAuthor  `json:"user"`
	ID        int64     `json:"id"`
}
