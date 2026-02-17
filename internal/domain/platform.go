package domain

import "time"

// Platform abstracts code hosting operations (GitHub, GitLab, Bitbucket).
// It provides a minimal PR-focused surface for session ↔ PR linking.
type Platform interface {
	// Name returns the platform identifier.
	Name() PlatformName

	// GetPRForBranch finds the most recent open PR for a given branch.
	// Returns ErrPRNotFound if no open PR exists.
	GetPRForBranch(branch string) (*PullRequest, error)

	// GetPR retrieves a pull request by number.
	// Returns ErrPRNotFound if it doesn't exist.
	GetPR(number int) (*PullRequest, error)

	// ListPRsForBranch returns all PRs (open, closed, merged) for a branch.
	ListPRsForBranch(branch string) ([]PullRequest, error)

	// AddComment posts a comment on a PR.
	AddComment(prNumber int, body string) error

	// UpdateComment updates an existing comment by ID.
	UpdateComment(commentID int64, body string) error

	// ListComments returns comments on a PR.
	ListComments(prNumber int) ([]PRComment, error)
}

// PullRequest represents a PR/MR on a code hosting platform.
type PullRequest struct {
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	URL        string    `json:"url"`
	Title      string    `json:"title"`
	Branch     string    `json:"branch"`
	BaseBranch string    `json:"base_branch"`
	State      string    `json:"state"` // "open", "closed", "merged"
	Author     string    `json:"author"`
	Number     int       `json:"number"`
}

// PRComment represents a comment on a pull request.
type PRComment struct {
	CreatedAt time.Time `json:"created_at"`
	Body      string    `json:"body"`
	Author    string    `json:"author"`
	ID        int64     `json:"id"`
}
