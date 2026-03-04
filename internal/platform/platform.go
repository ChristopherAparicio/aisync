package platform

import "github.com/ChristopherAparicio/aisync/internal/session"

// Platform abstracts code hosting operations (GitHub, GitLab, Bitbucket).
// It provides a minimal PR-focused surface for session-PR linking.
type Platform interface {
	// Name returns the platform identifier.
	Name() session.PlatformName

	// GetPRForBranch finds the most recent open PR for a given branch.
	// Returns ErrPRNotFound if no open PR exists.
	GetPRForBranch(branch string) (*session.PullRequest, error)

	// GetPR retrieves a pull request by number.
	// Returns ErrPRNotFound if it doesn't exist.
	GetPR(number int) (*session.PullRequest, error)

	// ListPRsForBranch returns all PRs (open, closed, merged) for a branch.
	ListPRsForBranch(branch string) ([]session.PullRequest, error)

	// AddComment posts a comment on a PR.
	AddComment(prNumber int, body string) error

	// UpdateComment updates an existing comment by ID.
	UpdateComment(commentID int64, body string) error

	// ListComments returns comments on a PR.
	ListComments(prNumber int) ([]session.PRComment, error)
}
