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
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/ChristopherAparicio/aisync/internal/search"
)

const ftsSchema = `(
	session_id UNINDEXED,
	summary,
	content,
	tool_names,
	project_path,
	remote_url,
	branch,
	agent,
	provider UNINDEXED,
	session_type UNINDEXED,
	created_at UNINDEXED,
	total_tokens UNINDEXED,
	message_count UNINDEXED,
	error_count UNINDEXED,
	tokenize='unicode61 remove_diacritics 2'
)`

// bm25Weights ranks summary highest so a short, on-topic summary outranks a
// long session whose 50k-character content dilutes its BM25 score. Path and
// remote URL are boosted above raw content for project-alias retrieval.
// Positions map to the indexed columns in ftsSchema:
// session_id, summary, content, tool_names, project_path, remote_url, branch, agent.
const bm25Weights = "bm25(sessions_fts, 1.0, 10.0, 1.0, 2.0, 6.0, 4.0, 3.0, 2.0)"

// Boost values are applied as boolean signals (matched / not matched) rather
// than tuned scalars, which keeps the re-ranker from overfitting the eval set.
const (
	rerankPoolSize     = 50
	pathMatchBoost     = 20.0
	summaryExactBoost  = 15.0
	summaryTokensBoost = 8.0
	recencyDecayPerDay = 0.01
)

// Engine implements search.Engine using SQLite FTS5.
type Engine struct {
	db *sql.DB
}

// New creates an FTS5 search engine. It creates the virtual table if it doesn't exist.
func New(db *sql.DB) (*Engine, error) {
	if err := migrateSchema(db); err != nil {
		return nil, err
	}

	// Create the FTS5 virtual table.
	// tokenize='unicode61' handles accented characters (French, etc.).
	_, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS sessions_fts USING fts5` + ftsSchema)
	if err != nil {
		return nil, fmt.Errorf("creating FTS5 table: %w", err)
	}

	return &Engine{db: db}, nil
}

// migrateSchema rebuilds the FTS5 table in place when an older schema is
// detected. Older versions created project_path and remote_url as UNINDEXED,
// which made project name and path tokens unsearchable. The rebuild copies the
// already-stored column values into a table with the new column configuration,
// so the index is upgraded without re-reading every session from the store.
func migrateSchema(db *sql.DB) error {
	var existingSQL string
	err := db.QueryRow(
		"SELECT sql FROM sqlite_master WHERE type='table' AND name='sessions_fts'",
	).Scan(&existingSQL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspecting FTS5 schema: %w", err)
	}
	if !strings.Contains(existingSQL, "project_path UNINDEXED") {
		return nil
	}

	const columns = `session_id, summary, content, tool_names,
		project_path, remote_url, branch, agent, provider, session_type,
		created_at, total_tokens, message_count, error_count`

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin FTS5 migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`CREATE VIRTUAL TABLE sessions_fts_v2 USING fts5` + ftsSchema); err != nil {
		return fmt.Errorf("creating migrated FTS5 table: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO sessions_fts_v2 (` + columns + `) SELECT ` + columns + ` FROM sessions_fts`); err != nil {
		return fmt.Errorf("copying FTS5 rows during migration: %w", err)
	}
	if _, err := tx.Exec("DROP TABLE sessions_fts"); err != nil {
		return fmt.Errorf("dropping old FTS5 table: %w", err)
	}
	if _, err := tx.Exec("ALTER TABLE sessions_fts_v2 RENAME TO sessions_fts"); err != nil {
		return fmt.Errorf("renaming migrated FTS5 table: %w", err)
	}

	return tx.Commit()
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

	// Fetch a candidate pool, not just one page: paginating before re-ranking
	// would let SQL OFFSET discard rows the re-ranker should have promoted.
	poolLimit := max(query.Offset+limit, rerankPoolSize)

	selectSQL := fmt.Sprintf(`SELECT
		session_id,
		%s AS score,
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
		ORDER BY score
		LIMIT ?`, bm25Weights, where)

	args = append(args, poolLimit)

	rows, err := e.db.Query(selectSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("FTS5 query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := &search.Result{
		TotalCount: totalCount,
		Engine:     "fts5",
	}

	var pool []search.Hit
	for rows.Next() {
		var (
			hit       search.Hit
			score     float64
			summaryHL string
			contentHL string
			createdAt string
		)
		if err := rows.Scan(
			&hit.SessionID, &score, &summaryHL, &contentHL,
			&hit.Summary, &hit.ProjectPath, &hit.RemoteURL,
			&hit.Branch, &hit.Agent, &hit.Provider,
			&createdAt, &hit.Tokens, &hit.Messages, &hit.Errors,
		); err != nil {
			continue
		}
		hit.Score = -score // bm25 score is negative (lower = better)
		hit.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		hit.Highlights = make(map[string]string)
		if summaryHL != "" && summaryHL != hit.Summary {
			hit.Highlights["summary"] = summaryHL
		}
		if contentHL != "" {
			hit.Highlights["content"] = contentHL
		}
		pool = append(pool, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rerank(pool, query.Text)
	result.Hits = paginate(pool, query.Offset, limit)

	result.Took = time.Since(start)
	return result, nil
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

// IndexedSessionIDs returns the set of session IDs currently in the FTS5 index.
// Used for incremental indexing — only sessions NOT in this set need indexing.
func (e *Engine) IndexedSessionIDs() (map[string]bool, error) {
	rows, err := e.db.Query("SELECT session_id FROM sessions_fts")
	if err != nil {
		return nil, fmt.Errorf("query indexed sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	ids := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids[id] = true
	}
	return ids, rows.Err()
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

func rerank(pool []search.Hit, queryText string) {
	queryLower := strings.ToLower(strings.TrimSpace(queryText))
	queryTokens := tokenize(queryText)
	now := time.Now()
	for i := range pool {
		pool[i].Score = rerankScore(pool[i], queryTokens, queryLower, now)
	}
	sort.SliceStable(pool, func(a, b int) bool {
		return pool[a].Score > pool[b].Score
	})
}

func rerankScore(h search.Hit, queryTokens []string, queryLower string, now time.Time) float64 {
	score := h.Score
	if allTokensPresent(queryTokens, tokenSet(h.ProjectPath)) {
		score += pathMatchBoost
	}
	if queryLower != "" && strings.Contains(strings.ToLower(h.Summary), queryLower) {
		score += summaryExactBoost
	}
	if allTokensPresent(queryTokens, tokenSet(h.Summary)) {
		score += summaryTokensBoost
	}
	if !h.CreatedAt.IsZero() {
		if days := now.Sub(h.CreatedAt).Hours() / 24; days > 0 {
			score -= recencyDecayPerDay * days
		}
	}
	return score
}

func paginate(hits []search.Hit, offset, limit int) []search.Hit {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(hits) {
		return nil
	}
	end := min(offset+limit, len(hits))
	return hits[offset:end]
}

func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

func tokenSet(s string) map[string]struct{} {
	tokens := tokenize(s)
	set := make(map[string]struct{}, len(tokens))
	for _, t := range tokens {
		set[t] = struct{}{}
	}
	return set
}

func allTokensPresent(tokens []string, set map[string]struct{}) bool {
	if len(tokens) == 0 {
		return false
	}
	for _, t := range tokens {
		if _, ok := set[t]; !ok {
			return false
		}
	}
	return true
}
