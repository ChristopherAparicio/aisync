// Package fts5 implements a full-text search engine using SQLite FTS5.
//
// FTS5 provides:
//   - Full-text search across summary + message content
//   - BM25 relevance ranking
//   - Highlighted snippets
//   - Zero external dependencies
//
// The FTS5 virtual table is automatically created and populated.
// It stays in sync via explicit Index/Delete calls from the post-capture hook.
package fts5

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/search"
)

// Engine implements search.Engine using SQLite FTS5.
type Engine struct {
	db *sql.DB
}

// New creates an FTS5 search engine. It creates the virtual table if it doesn't exist.
func New(db *sql.DB) (*Engine, error) {
	// Create the FTS5 virtual table.
	// tokenize='unicode61' handles accented characters (French, etc.).
	_, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS sessions_fts USING fts5(
		session_id UNINDEXED,
		summary,
		content,
		tool_names,
		project_path UNINDEXED,
		remote_url UNINDEXED,
		branch,
		agent,
		provider UNINDEXED,
		session_type UNINDEXED,
		created_at UNINDEXED,
		total_tokens UNINDEXED,
		message_count UNINDEXED,
		error_count UNINDEXED,
		tokenize='unicode61 remove_diacritics 2'
	)`)
	if err != nil {
		return nil, fmt.Errorf("creating FTS5 table: %w", err)
	}

	return &Engine{db: db}, nil
}

func (e *Engine) Name() string { return "fts5" }

func (e *Engine) Capabilities() search.Capabilities {
	return search.Capabilities{
		FullText:   true,
		Semantic:   false,
		Facets:     false,
		Highlights: true,
		FuzzyMatch: false,
		Ranking:    true,
	}
}

func (e *Engine) Search(_ context.Context, query search.Query) (*search.Result, error) {
	start := time.Now()

	if query.Text == "" {
		return &search.Result{Engine: "fts5", Took: time.Since(start)}, nil
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}

	// Build FTS5 MATCH expression.
	// Escape special FTS5 characters and search across summary + content + branch + tool_names.
	matchExpr := escapeFTS5(query.Text)

	// Build WHERE filters for non-FTS columns.
	var conditions []string
	var args []interface{}

	conditions = append(conditions, "sessions_fts MATCH ?")
	args = append(args, matchExpr)

	if f := query.Filters; f.ProjectPath != "" {
		conditions = append(conditions, "project_path = ?")
		args = append(args, f.ProjectPath)
	}
	if f := query.Filters; f.Branch != "" {
		conditions = append(conditions, "branch = ?")
		args = append(args, f.Branch)
	}
	if f := query.Filters; f.Provider != "" {
		conditions = append(conditions, "provider = ?")
		args = append(args, f.Provider)
	}
	if f := query.Filters; f.SessionType != "" {
		conditions = append(conditions, "session_type = ?")
		args = append(args, f.SessionType)
	}

	where := "WHERE " + strings.Join(conditions, " AND ")

	// Count total matches.
	var totalCount int
	countSQL := "SELECT COUNT(*) FROM sessions_fts " + where
	if err := e.db.QueryRow(countSQL, args...).Scan(&totalCount); err != nil {
		return nil, fmt.Errorf("FTS5 count: %w", err)
	}

	// Fetch results with BM25 ranking and highlights.
	selectSQL := fmt.Sprintf(`SELECT
		session_id,
		rank,
		highlight(sessions_fts, 1, '<mark>', '</mark>') as summary_hl,
		snippet(sessions_fts, 2, '<mark>', '</mark>', '…', 40) as content_hl,
		summary,
		project_path,
		remote_url,
		branch,
		agent,
		provider,
		created_at,
		total_tokens,
		message_count,
		error_count
		FROM sessions_fts %s
		ORDER BY rank
		LIMIT ? OFFSET ?`, where)

	args = append(args, limit, query.Offset)

	rows, err := e.db.Query(selectSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("FTS5 query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := &search.Result{
		TotalCount: totalCount,
		Engine:     "fts5",
	}

	for rows.Next() {
		var (
			hit       search.Hit
			rank      float64
			summaryHL string
			contentHL string
			createdAt string
		)
		if err := rows.Scan(
			&hit.SessionID, &rank, &summaryHL, &contentHL,
			&hit.Summary, &hit.ProjectPath, &hit.RemoteURL,
			&hit.Branch, &hit.Agent, &hit.Provider,
			&createdAt, &hit.Tokens, &hit.Messages, &hit.Errors,
		); err != nil {
			continue
		}
		hit.Score = -rank // FTS5 rank is negative (lower = better)
		hit.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		hit.Highlights = make(map[string]string)
		if summaryHL != "" && summaryHL != hit.Summary {
			hit.Highlights["summary"] = summaryHL
		}
		if contentHL != "" {
			hit.Highlights["content"] = contentHL
		}
		result.Hits = append(result.Hits, hit)
	}

	result.Took = time.Since(start)
	return result, rows.Err()
}

func (e *Engine) Index(_ context.Context, doc search.Document) error {
	// Upsert: delete then insert (FTS5 doesn't support ON CONFLICT).
	_, _ = e.db.Exec("DELETE FROM sessions_fts WHERE session_id = ?", doc.ID)

	_, err := e.db.Exec(`INSERT INTO sessions_fts (
		session_id, summary, content, tool_names,
		project_path, remote_url, branch, agent, provider, session_type,
		created_at, total_tokens, message_count, error_count
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		doc.ID, doc.Summary, doc.Content, doc.ToolNames,
		doc.ProjectPath, doc.RemoteURL, doc.Branch, doc.Agent, doc.Provider, doc.SessionType,
		doc.CreatedAt.Format(time.RFC3339), doc.TotalTokens, doc.MessageCount, doc.ErrorCount,
	)
	return err
}

func (e *Engine) Delete(_ context.Context, id string) error {
	_, err := e.db.Exec("DELETE FROM sessions_fts WHERE session_id = ?", id)
	return err
}

func (e *Engine) Close() error {
	return nil // DB is owned by the caller
}

// IndexedCount returns the number of documents in the FTS5 index.
func (e *Engine) IndexedCount() (int, error) {
	var count int
	err := e.db.QueryRow("SELECT COUNT(*) FROM sessions_fts").Scan(&count)
	return count, err
}

// escapeFTS5 escapes special characters for FTS5 MATCH syntax.
// Wraps each word in double-quotes to treat it as a literal token.
func escapeFTS5(text string) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return `""`
	}
	// Quote each word to prevent FTS5 syntax errors from special chars.
	quoted := make([]string, len(words))
	for i, w := range words {
		// Remove any existing quotes from user input.
		w = strings.ReplaceAll(w, `"`, ``)
		quoted[i] = `"` + w + `"`
	}
	return strings.Join(quoted, " ")
}
