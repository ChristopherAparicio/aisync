package sqlite

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

const prTimeFormat = "2006-01-02T15:04:05Z"

// ── PullRequestStore implementation ──

// SavePullRequest creates or updates a pull request (upsert by repo_owner/repo_name/number).
func (s *Store) SavePullRequest(pr *session.PullRequest) error {
	_, err := s.db.Exec(`
		INSERT INTO pull_requests (repo_owner, repo_name, number, title, branch, base_branch, state, author, url, additions, deletions, comments, created_at, updated_at, merged_at, closed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(repo_owner, repo_name, number) DO UPDATE SET
			title = excluded.title,
			branch = excluded.branch,
			base_branch = excluded.base_branch,
			state = excluded.state,
			author = excluded.author,
			url = excluded.url,
			additions = excluded.additions,
			deletions = excluded.deletions,
			comments = excluded.comments,
			updated_at = excluded.updated_at,
			merged_at = excluded.merged_at,
			closed_at = excluded.closed_at`,
		pr.RepoOwner, pr.RepoName, pr.Number,
		pr.Title, pr.Branch, pr.BaseBranch, pr.State, pr.Author, pr.URL,
		pr.Additions, pr.Deletions, pr.Comments,
		fmtPRTime(pr.CreatedAt), fmtPRTime(pr.UpdatedAt),
		fmtPRTime(pr.MergedAt), fmtPRTime(pr.ClosedAt),
	)
	return err
}

// GetPullRequest retrieves a pull request by owner, repo and number.
func (s *Store) GetPullRequest(repoOwner, repoName string, number int) (*session.PullRequest, error) {
	row := s.db.QueryRow(`
		SELECT repo_owner, repo_name, number, title, branch, base_branch, state, author, url,
		       additions, deletions, comments, created_at, updated_at, merged_at, closed_at
		FROM pull_requests
		WHERE repo_owner = ? AND repo_name = ? AND number = ?`,
		repoOwner, repoName, number,
	)

	pr, err := scanPR(row)
	if err == sql.ErrNoRows {
		return nil, session.ErrPRNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get PR %s/%s#%d: %w", repoOwner, repoName, number, err)
	}
	return pr, nil
}

// ListPullRequests returns pull requests matching optional filters.
func (s *Store) ListPullRequests(repoOwner, repoName, state string, limit int) ([]session.PullRequest, error) {
	query := "SELECT repo_owner, repo_name, number, title, branch, base_branch, state, author, url, additions, deletions, comments, created_at, updated_at, merged_at, closed_at FROM pull_requests WHERE 1=1"
	var args []interface{}

	if repoOwner != "" && repoName != "" {
		query += " AND repo_owner = ? AND repo_name = ?"
		args = append(args, repoOwner, repoName)
	}
	if state != "" {
		query += " AND state = ?"
		args = append(args, state)
	}

	query += " ORDER BY updated_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list PRs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []session.PullRequest
	for rows.Next() {
		pr, scanErr := scanPRFromRows(rows)
		if scanErr != nil {
			continue
		}
		result = append(result, *pr)
	}
	return result, rows.Err()
}

// LinkSessionPR links a session to a pull request.
func (s *Store) LinkSessionPR(sessionID session.ID, repoOwner, repoName string, prNumber int) error {
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO session_pull_requests (session_id, repo_owner, repo_name, pr_number, linked_at)
		VALUES (?, ?, ?, ?, ?)`,
		string(sessionID), repoOwner, repoName, prNumber, time.Now().UTC().Format(prTimeFormat),
	)
	return err
}

// GetSessionsForPR returns all sessions linked to a specific PR.
func (s *Store) GetSessionsForPR(repoOwner, repoName string, prNumber int) ([]session.Summary, error) {
	rows, err := s.db.Query(`
		SELECT s.id, s.provider, s.summary, s.branch, s.project_path,
		       s.created_at, s.total_tokens, s.message_count, s.session_type, s.status, s.agent
		FROM sessions s
		JOIN session_pull_requests spr ON s.id = spr.session_id
		WHERE spr.repo_owner = ? AND spr.repo_name = ? AND spr.pr_number = ?
		ORDER BY s.created_at DESC`,
		repoOwner, repoName, prNumber,
	)
	if err != nil {
		return nil, fmt.Errorf("get sessions for PR %s/%s#%d: %w", repoOwner, repoName, prNumber, err)
	}
	defer func() { _ = rows.Close() }()

	var result []session.Summary
	for rows.Next() {
		var sm session.Summary
		var createdAt, status string
		if scanErr := rows.Scan(
			&sm.ID, &sm.Provider, &sm.Summary, &sm.Branch, &sm.ProjectPath,
			&createdAt, &sm.TotalTokens, &sm.MessageCount, &sm.SessionType, &status, &sm.Agent,
		); scanErr != nil {
			continue
		}
		sm.CreatedAt, _ = time.Parse(prTimeFormat, createdAt)
		sm.Status = session.SessionStatus(status)
		result = append(result, sm)
	}
	return result, rows.Err()
}

// GetPRsForSession returns all PRs linked to a specific session.
func (s *Store) GetPRsForSession(sessionID session.ID) ([]session.PullRequest, error) {
	rows, err := s.db.Query(`
		SELECT p.repo_owner, p.repo_name, p.number, p.title, p.branch, p.base_branch,
		       p.state, p.author, p.url, p.additions, p.deletions, p.comments,
		       p.created_at, p.updated_at, p.merged_at, p.closed_at
		FROM pull_requests p
		JOIN session_pull_requests spr ON p.repo_owner = spr.repo_owner
			AND p.repo_name = spr.repo_name AND p.number = spr.pr_number
		WHERE spr.session_id = ?
		ORDER BY p.updated_at DESC`,
		string(sessionID),
	)
	if err != nil {
		return nil, fmt.Errorf("get PRs for session %s: %w", sessionID, err)
	}
	defer func() { _ = rows.Close() }()

	var result []session.PullRequest
	for rows.Next() {
		pr, scanErr := scanPRFromRows(rows)
		if scanErr != nil {
			continue
		}
		result = append(result, *pr)
	}
	return result, rows.Err()
}

// GetPRByBranch finds a pull request by its source branch name.
// Returns the most recently updated PR matching the branch.
func (s *Store) GetPRByBranch(branch string) (*session.PullRequest, error) {
	row := s.db.QueryRow(`
		SELECT repo_owner, repo_name, number, title, branch, base_branch, state, author, url,
		       additions, deletions, comments, created_at, updated_at, merged_at, closed_at
		FROM pull_requests
		WHERE branch = ?
		ORDER BY updated_at DESC
		LIMIT 1`,
		branch,
	)

	pr, err := scanPR(row)
	if err == sql.ErrNoRows {
		return nil, session.ErrPRNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get PR by branch %q: %w", branch, err)
	}
	return pr, nil
}

// ListPRsWithSessions returns PRs enriched with linked session data.
func (s *Store) ListPRsWithSessions(repoOwner, repoName, state string, limit int) ([]session.PRWithSessions, error) {
	prs, err := s.ListPullRequests(repoOwner, repoName, state, limit)
	if err != nil {
		return nil, err
	}

	result := make([]session.PRWithSessions, 0, len(prs))
	for _, pr := range prs {
		sessions, sessErr := s.GetSessionsForPR(pr.RepoOwner, pr.RepoName, pr.Number)
		if sessErr != nil {
			sessions = nil
		}

		var totalTokens int
		for _, sm := range sessions {
			totalTokens += sm.TotalTokens
		}

		result = append(result, session.PRWithSessions{
			PR:           pr,
			Sessions:     sessions,
			SessionCount: len(sessions),
			TotalTokens:  totalTokens,
		})
	}

	return result, nil
}

// ── helpers ──

func fmtPRTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(prTimeFormat)
}

func parsePRTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(prTimeFormat, s)
	return t
}

func scanPR(row *sql.Row) (*session.PullRequest, error) {
	var pr session.PullRequest
	var createdAt, updatedAt, mergedAt, closedAt string
	err := row.Scan(
		&pr.RepoOwner, &pr.RepoName, &pr.Number,
		&pr.Title, &pr.Branch, &pr.BaseBranch, &pr.State, &pr.Author, &pr.URL,
		&pr.Additions, &pr.Deletions, &pr.Comments,
		&createdAt, &updatedAt, &mergedAt, &closedAt,
	)
	if err != nil {
		return nil, err
	}
	pr.CreatedAt = parsePRTime(createdAt)
	pr.UpdatedAt = parsePRTime(updatedAt)
	pr.MergedAt = parsePRTime(mergedAt)
	pr.ClosedAt = parsePRTime(closedAt)
	return &pr, nil
}

func scanPRFromRows(rows *sql.Rows) (*session.PullRequest, error) {
	var pr session.PullRequest
	var createdAt, updatedAt, mergedAt, closedAt string
	err := rows.Scan(
		&pr.RepoOwner, &pr.RepoName, &pr.Number,
		&pr.Title, &pr.Branch, &pr.BaseBranch, &pr.State, &pr.Author, &pr.URL,
		&pr.Additions, &pr.Deletions, &pr.Comments,
		&createdAt, &updatedAt, &mergedAt, &closedAt,
	)
	if err != nil {
		return nil, err
	}
	pr.CreatedAt = parsePRTime(createdAt)
	pr.UpdatedAt = parsePRTime(updatedAt)
	pr.MergedAt = parsePRTime(mergedAt)
	pr.ClosedAt = parsePRTime(closedAt)
	return &pr, nil
}
