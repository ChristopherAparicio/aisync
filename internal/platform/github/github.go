// Package github implements domain.Platform for GitHub using the gh CLI.
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

	"github.com/ChristopherAparicio/aisync/internal/domain"
)

// Client implements domain.Platform using the gh CLI.
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
func (c *Client) Name() domain.PlatformName {
	return domain.PlatformGitHub
}

// Available checks whether the gh CLI is installed and authenticated.
func (c *Client) Available() bool {
	_, err := c.run("auth", "status")
	return err == nil
}

// GetPRForBranch finds the most recent open PR for a given branch.
func (c *Client) GetPRForBranch(branch string) (*domain.PullRequest, error) {
	out, err := c.run(
		"pr", "list",
		"--head", branch,
		"--state", "open",
		"--limit", "1",
		"--json", "number,title,headRefName,baseRefName,state,url,author,createdAt,updatedAt",
	)
	if err != nil {
		return nil, fmt.Errorf("listing PRs for branch %q: %w", branch, err)
	}

	var prs []ghPR
	if jsonErr := json.Unmarshal([]byte(out), &prs); jsonErr != nil {
		return nil, fmt.Errorf("parsing PR list: %w", jsonErr)
	}

	if len(prs) == 0 {
		return nil, domain.ErrPRNotFound
	}

	return prs[0].toDomain(), nil
}

// GetPR retrieves a PR by number.
func (c *Client) GetPR(number int) (*domain.PullRequest, error) {
	out, err := c.run(
		"pr", "view",
		strconv.Itoa(number),
		"--json", "number,title,headRefName,baseRefName,state,url,author,createdAt,updatedAt",
	)
	if err != nil {
		return nil, fmt.Errorf("getting PR #%d: %w", number, domain.ErrPRNotFound)
	}

	var pr ghPR
	if jsonErr := json.Unmarshal([]byte(out), &pr); jsonErr != nil {
		return nil, fmt.Errorf("parsing PR #%d: %w", number, jsonErr)
	}

	return pr.toDomain(), nil
}

// ListPRsForBranch returns all PRs (open, closed, merged) for a branch.
func (c *Client) ListPRsForBranch(branch string) ([]domain.PullRequest, error) {
	out, err := c.run(
		"pr", "list",
		"--head", branch,
		"--state", "all",
		"--limit", "100",
		"--json", "number,title,headRefName,baseRefName,state,url,author,createdAt,updatedAt",
	)
	if err != nil {
		return nil, fmt.Errorf("listing PRs for branch %q: %w", branch, err)
	}

	var prs []ghPR
	if jsonErr := json.Unmarshal([]byte(out), &prs); jsonErr != nil {
		return nil, fmt.Errorf("parsing PR list: %w", jsonErr)
	}

	result := make([]domain.PullRequest, 0, len(prs))
	for _, pr := range prs {
		result = append(result, *pr.toDomain())
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
func (c *Client) ListComments(prNumber int) ([]domain.PRComment, error) {
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

	result := make([]domain.PRComment, 0, len(ghComments))
	for _, c := range ghComments {
		result = append(result, domain.PRComment{
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
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Title       string    `json:"title"`
	HeadRefName string    `json:"headRefName"`
	BaseRefName string    `json:"baseRefName"`
	State       string    `json:"state"`
	URL         string    `json:"url"`
	Author      ghAuthor  `json:"author"`
	Number      int       `json:"number"`
}

func (p *ghPR) toDomain() *domain.PullRequest {
	return &domain.PullRequest{
		Number:     p.Number,
		Title:      p.Title,
		Branch:     p.HeadRefName,
		BaseBranch: p.BaseRefName,
		State:      strings.ToLower(p.State),
		URL:        p.URL,
		Author:     p.Author.Login,
		CreatedAt:  p.CreatedAt,
		UpdatedAt:  p.UpdatedAt,
	}
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
