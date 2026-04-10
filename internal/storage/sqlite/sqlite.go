// Package sqlite implements session.Store using a local SQLite database.
package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/auth"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"

	_ "modernc.org/sqlite" // SQLite driver registration
)

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    agent TEXT NOT NULL DEFAULT 'claude',
    branch TEXT,
    commit_sha TEXT,
    project_path TEXT NOT NULL,
    parent_id TEXT,
    storage_mode TEXT NOT NULL DEFAULT 'compact',
    summary TEXT,
    message_count INTEGER,
    total_tokens INTEGER,
    payload BLOB,
    created_at TEXT,
    exported_at TEXT,
    exported_by TEXT
);

CREATE TABLE IF NOT EXISTS session_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    link_type TEXT NOT NULL,
    link_ref TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS file_changes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    file_path TEXT NOT NULL,
    change_type TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_branch ON sessions(branch);
CREATE INDEX IF NOT EXISTS idx_sessions_commit ON sessions(commit_sha);
CREATE INDEX IF NOT EXISTS idx_sessions_project ON sessions(project_path);
CREATE INDEX IF NOT EXISTS idx_links_ref ON session_links(link_ref);
CREATE INDEX IF NOT EXISTS idx_files_path ON file_changes(file_path);
`

// migration001 adds the users table and owner_id column to sessions.
// Uses IF NOT EXISTS / safe ALTER TABLE so it can run on both new and existing databases.
const migration001 = `
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT NOT NULL UNIQUE,
    source TEXT NOT NULL DEFAULT 'git',
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
`

// migration002 adds the session_analyses table for the Session Analysis BC.
const migration002 = `
CREATE TABLE IF NOT EXISTS session_analyses (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    trigger TEXT NOT NULL,
    adapter TEXT NOT NULL,
    model TEXT,
    tokens_used INTEGER DEFAULT 0,
    duration_ms INTEGER DEFAULT 0,
    error TEXT,
    report BLOB,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_analyses_session ON session_analyses(session_id);
CREATE INDEX IF NOT EXISTS idx_analyses_created ON session_analyses(created_at);
`

// migration004 adds the user_preferences table for per-user dashboard settings.
const migration004 = `
CREATE TABLE IF NOT EXISTS user_preferences (
    user_id    TEXT NOT NULL DEFAULT '',
    preferences TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT NOT NULL,
    UNIQUE(user_id)
);
`

// migration006StatsCache adds the stats_cache table for lazy-computed dashboard statistics.
const migration006StatsCache = `
CREATE TABLE IF NOT EXISTS stats_cache (
    key        TEXT PRIMARY KEY,
    value      BLOB NOT NULL,
    updated_at TEXT NOT NULL
);
`

// migration003 adds tool_call_count and error_count columns to sessions.
// These are denormalized counters computed at save-time from the full session payload.
const migration003 = `-- placeholder: columns added via ALTER TABLE in runMigrations`

// migration007 adds the session_session_links table for session-to-session relationships
// (e.g. delegation, continuation, follow-up).
const migration007SessionLinks = `
CREATE TABLE IF NOT EXISTS session_session_links (
    id TEXT PRIMARY KEY,
    source_session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    target_session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    link_type TEXT NOT NULL,
    description TEXT,
    created_at TEXT NOT NULL,
    UNIQUE(source_session_id, target_session_id, link_type)
);

CREATE INDEX IF NOT EXISTS idx_sslinks_source ON session_session_links(source_session_id);
CREATE INDEX IF NOT EXISTS idx_sslinks_target ON session_session_links(target_session_id);
CREATE INDEX IF NOT EXISTS idx_sslinks_type ON session_session_links(link_type);
`

// migration010Auth adds authentication tables: auth_users and auth_api_keys.
const migration010Auth = `
CREATE TABLE IF NOT EXISTS auth_users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'user',
    active        INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_auth_users_username ON auth_users(username);

CREATE TABLE IF NOT EXISTS auth_api_keys (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL DEFAULT '',
    key_hash     TEXT NOT NULL UNIQUE,
    key_prefix   TEXT NOT NULL DEFAULT '',
    active       INTEGER NOT NULL DEFAULT 1,
    expires_at   TEXT,
    last_used_at TEXT,
    created_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_auth_api_keys_user_id ON auth_api_keys(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_auth_api_keys_hash ON auth_api_keys(key_hash);
`

// Store implements session.Store with SQLite.
type Store struct {
	db *sql.DB
}

// DB returns the underlying *sql.DB for advanced use cases (e.g. FTS5 search engine).
func (s *Store) DB() *sql.DB {
	return s.db
}

// New opens (or creates) a SQLite database at the given path and runs migrations.
func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if _, execErr := db.Exec(schema); execErr != nil {
		_ = db.Close()
		return nil, fmt.Errorf("creating schema: %w", execErr)
	}

	// Run migrations for schema evolution
	if migErr := runMigrations(db); migErr != nil {
		_ = db.Close()
		return nil, fmt.Errorf("running migrations: %w", migErr)
	}

	return &Store{db: db}, nil
}

// Close releases the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// Save stores a session. If a session with the same ID exists, it is replaced.
func (s *Store) Save(session *session.Session) error {
	payload, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("marshaling session: %w", err)
	}

	payload, err = compressPayload(payload)
	if err != nil {
		return fmt.Errorf("compressing session payload: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Compute denormalized tool counters
	toolCallCount, errorCount := countToolCalls(session)

	// Compute denormalized actual cost from provider-reported per-message costs.
	// Estimated cost (API-equivalent) is set by the service layer before calling Save.
	actualCost := computeActualCost(session)

	// Upsert session
	_, err = tx.Exec(`
		INSERT INTO sessions (id, provider, agent, branch, commit_sha, project_path, remote_url, session_type, project_category, status, parent_id, owner_id, storage_mode, summary, message_count, total_tokens, tool_call_count, error_count, estimated_cost, actual_cost, source_updated_at, payload, created_at, exported_at, exported_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			provider=excluded.provider, agent=excluded.agent, branch=excluded.branch,
			commit_sha=excluded.commit_sha, project_path=excluded.project_path,
			remote_url=excluded.remote_url, session_type=excluded.session_type,
			project_category=excluded.project_category, status=excluded.status,
			parent_id=excluded.parent_id, owner_id=excluded.owner_id,
			storage_mode=excluded.storage_mode,
			summary=excluded.summary, message_count=excluded.message_count,
			total_tokens=excluded.total_tokens,
			tool_call_count=excluded.tool_call_count, error_count=excluded.error_count,
			estimated_cost=excluded.estimated_cost, actual_cost=excluded.actual_cost,
			source_updated_at=excluded.source_updated_at,
			payload=excluded.payload,
			created_at=excluded.created_at, exported_at=excluded.exported_at,
			exported_by=excluded.exported_by`,
		session.ID, session.Provider, session.Agent, session.Branch,
		session.CommitSHA, session.ProjectPath, session.RemoteURL, session.SessionType, session.ProjectCategory, session.Status, session.ParentID, session.OwnerID,
		session.StorageMode, session.Summary, len(session.Messages),
		session.TokenUsage.TotalTokens, toolCallCount, errorCount, session.EstimatedCost, actualCost, session.SourceUpdatedAt, payload,
		session.CreatedAt.Format("2006-01-02T15:04:05Z"),
		session.ExportedAt.Format("2006-01-02T15:04:05Z"),
		session.ExportedBy,
	)
	if err != nil {
		return fmt.Errorf("upserting session: %w", err)
	}

	// Replace file changes
	if _, err := tx.Exec("DELETE FROM file_changes WHERE session_id = ?", session.ID); err != nil {
		return fmt.Errorf("deleting old file changes: %w", err)
	}
	for _, fc := range session.FileChanges {
		if _, err := tx.Exec("INSERT INTO file_changes (session_id, file_path, change_type, tool_name) VALUES (?, ?, ?, '')",
			session.ID, fc.FilePath, fc.ChangeType); err != nil {
			return fmt.Errorf("inserting file change: %w", err)
		}
	}

	// Replace links
	if _, err := tx.Exec("DELETE FROM session_links WHERE session_id = ?", session.ID); err != nil {
		return fmt.Errorf("deleting old links: %w", err)
	}
	for _, link := range session.Links {
		if _, err := tx.Exec("INSERT INTO session_links (session_id, link_type, link_ref) VALUES (?, ?, ?)",
			session.ID, link.LinkType, link.Ref); err != nil {
			return fmt.Errorf("inserting link: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Invalidate stats cache — session data changed.
	_ = s.InvalidateCache("stats:")
	_ = s.InvalidateCache("forecast:")
	return nil
}

// Get retrieves a session by its ID.
func (s *Store) Get(id session.ID) (*session.Session, error) {
	var payload []byte
	var remoteURL, sessionType, projectCategory, status string
	var sourceUpdatedAt int64
	err := s.db.QueryRow(
		"SELECT payload, COALESCE(remote_url, ''), COALESCE(session_type, ''), COALESCE(project_category, ''), COALESCE(status, ''), COALESCE(source_updated_at, 0) FROM sessions WHERE id = ?", id,
	).Scan(&payload, &remoteURL, &sessionType, &projectCategory, &status, &sourceUpdatedAt)
	if err == sql.ErrNoRows {
		return nil, session.ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying session: %w", err)
	}

	payload, err = decompressPayload(payload)
	if err != nil {
		return nil, fmt.Errorf("decompressing session payload: %w", err)
	}

	var sess session.Session
	if unmarshalErr := json.Unmarshal(payload, &sess); unmarshalErr != nil {
		return nil, fmt.Errorf("unmarshaling session: %w", unmarshalErr)
	}

	// SourceUpdatedAt has json:"-" so it is never in the payload — restore it
	// from the dedicated DB column.
	sess.SourceUpdatedAt = sourceUpdatedAt

	// Overlay mutable columns that may have been updated after the payload was saved.
	// These columns can be changed via UpdateRemoteURL, UpdateSessionType, etc.
	if remoteURL != "" && sess.RemoteURL == "" {
		sess.RemoteURL = remoteURL
	}
	if sessionType != "" && sess.SessionType == "" {
		sess.SessionType = sessionType
	}
	if projectCategory != "" && sess.ProjectCategory == "" {
		sess.ProjectCategory = projectCategory
	}
	if status != "" && sess.Status == "" {
		sess.Status = session.SessionStatus(status)
	}

	// Load links from DB (they may have been added after save)
	links, err := s.loadLinks(id)
	if err != nil {
		return nil, err
	}
	sess.Links = links

	// Load file changes from DB
	fileChanges, err := s.loadFileChanges(id)
	if err != nil {
		return nil, err
	}
	sess.FileChanges = fileChanges

	return &sess, nil
}

// GetBatch retrieves multiple sessions by ID in 3 queries total, regardless of len(ids).
//
// This is the batch-aware counterpart to Get(). It is designed to eliminate the N+1
// query pattern where callers loop over a Summary slice and call Get() per session
// (which costs 3×N queries: sessions + session_links + file_changes).
//
// Query plan:
//  1. SELECT ... FROM sessions WHERE id IN (?, ?, ...)  — one query for all payloads
//  2. SELECT ... FROM session_links WHERE session_id IN (?, ?, ...)  — all links at once
//  3. SELECT ... FROM file_changes  WHERE session_id IN (?, ?, ...)  — all file changes at once
//
// Missing IDs are silently omitted from the result map (consistent with the
// documented "missing IDs are absent" contract). Decompression and unmarshal errors
// for individual sessions are skipped with a log so that one corrupt row does not
// poison an entire batch.
func (s *Store) GetBatch(ids []session.ID) (map[session.ID]*session.Session, error) {
	if len(ids) == 0 {
		return map[session.ID]*session.Session{}, nil
	}

	// De-duplicate IDs to keep the IN-clause compact and avoid double-loading.
	seen := make(map[session.ID]struct{}, len(ids))
	uniqueIDs := make([]session.ID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}

	// Build placeholders and args shared by all three queries.
	placeholders := make([]string, len(uniqueIDs))
	args := make([]interface{}, len(uniqueIDs))
	for i, id := range uniqueIDs {
		placeholders[i] = "?"
		args[i] = string(id)
	}
	inClause := strings.Join(placeholders, ",")

	result := make(map[session.ID]*session.Session, len(uniqueIDs))

	// ── Query 1: session payloads + mutable columns ──
	rows, err := s.db.Query(
		"SELECT id, payload, COALESCE(remote_url, ''), COALESCE(session_type, ''), "+
			"COALESCE(project_category, ''), COALESCE(status, ''), COALESCE(source_updated_at, 0) "+
			"FROM sessions WHERE id IN ("+inClause+")", args...)
	if err != nil {
		return nil, fmt.Errorf("batch querying sessions: %w", err)
	}
	func() {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			var payload []byte
			var remoteURL, sessionType, projectCategory, status string
			var sourceUpdatedAt int64
			if scanErr := rows.Scan(&id, &payload, &remoteURL, &sessionType, &projectCategory, &status, &sourceUpdatedAt); scanErr != nil {
				log.Printf("[GetBatch] scan error: %v", scanErr)
				continue
			}

			decompressed, decErr := decompressPayload(payload)
			if decErr != nil {
				log.Printf("[GetBatch] decompress error for %s: %v", id, decErr)
				continue
			}

			var sess session.Session
			if umErr := json.Unmarshal(decompressed, &sess); umErr != nil {
				log.Printf("[GetBatch] unmarshal error for %s: %v", id, umErr)
				continue
			}

			// SourceUpdatedAt has json:"-" — restore from DB column.
			sess.SourceUpdatedAt = sourceUpdatedAt

			// Clear companion slices that may be embedded in the payload JSON.
			// The DB (session_links, file_changes) is the source of truth — we
			// will re-populate these below from queries 2 and 3. This matches
			// Get() semantics which assigns (not appends) loadLinks / loadFileChanges.
			sess.Links = nil
			sess.FileChanges = nil

			// Overlay mutable columns (same semantics as Get()).
			if remoteURL != "" && sess.RemoteURL == "" {
				sess.RemoteURL = remoteURL
			}
			if sessionType != "" && sess.SessionType == "" {
				sess.SessionType = sessionType
			}
			if projectCategory != "" && sess.ProjectCategory == "" {
				sess.ProjectCategory = projectCategory
			}
			if status != "" && sess.Status == "" {
				sess.Status = session.SessionStatus(status)
			}

			result[session.ID(id)] = &sess
		}
	}()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating batch sessions: %w", err)
	}

	// Early exit: no rows matched → skip the companion queries entirely.
	if len(result) == 0 {
		return result, nil
	}

	// ── Query 2: all links for matched sessions ──
	linkRows, err := s.db.Query(
		"SELECT session_id, link_type, link_ref FROM session_links WHERE session_id IN ("+inClause+")",
		args...)
	if err != nil {
		return nil, fmt.Errorf("batch loading links: %w", err)
	}
	func() {
		defer func() { _ = linkRows.Close() }()
		for linkRows.Next() {
			var sessionID string
			var link session.Link
			if scanErr := linkRows.Scan(&sessionID, &link.LinkType, &link.Ref); scanErr != nil {
				log.Printf("[GetBatch] scan link error: %v", scanErr)
				continue
			}
			if sess, ok := result[session.ID(sessionID)]; ok {
				sess.Links = append(sess.Links, link)
			}
		}
	}()
	if err := linkRows.Err(); err != nil {
		return nil, fmt.Errorf("iterating batch links: %w", err)
	}

	// ── Query 3: all file_changes for matched sessions ──
	fcRows, err := s.db.Query(
		"SELECT session_id, file_path, change_type FROM file_changes WHERE session_id IN ("+inClause+")",
		args...)
	if err != nil {
		return nil, fmt.Errorf("batch loading file changes: %w", err)
	}
	func() {
		defer func() { _ = fcRows.Close() }()
		for fcRows.Next() {
			var sessionID string
			var fc session.FileChange
			if scanErr := fcRows.Scan(&sessionID, &fc.FilePath, &fc.ChangeType); scanErr != nil {
				log.Printf("[GetBatch] scan file change error: %v", scanErr)
				continue
			}
			if sess, ok := result[session.ID(sessionID)]; ok {
				sess.FileChanges = append(sess.FileChanges, fc)
			}
		}
	}()
	if err := fcRows.Err(); err != nil {
		return nil, fmt.Errorf("iterating batch file changes: %w", err)
	}

	return result, nil
}

// GetLatestByBranch retrieves the most recent session for a project and branch.
func (s *Store) GetLatestByBranch(projectPath string, branch string) (*session.Session, error) {
	var id string
	err := s.db.QueryRow(
		"SELECT id FROM sessions WHERE project_path = ? AND branch = ? ORDER BY created_at DESC LIMIT 1",
		projectPath, branch,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, session.ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying by branch: %w", err)
	}

	return s.Get(session.ID(id))
}

// CountByBranch returns the number of sessions for a project and branch.
func (s *Store) CountByBranch(projectPath string, branch string) (int, error) {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM sessions WHERE project_path = ? AND branch = ?",
		projectPath, branch,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting sessions by branch: %w", err)
	}
	return count, nil
}

// List returns session summaries matching the given options.
func (s *Store) List(opts session.ListOptions) ([]session.Summary, error) {
	query := "SELECT id, provider, agent, branch, summary, message_count, total_tokens, tool_call_count, error_count, COALESCE(estimated_cost, 0), COALESCE(actual_cost, 0), created_at, COALESCE(source_updated_at, 0), COALESCE(owner_id, ''), COALESCE(parent_id, ''), COALESCE(project_path, ''), COALESCE(remote_url, ''), COALESCE(session_type, ''), COALESCE(project_category, ''), COALESCE(status, '') FROM sessions WHERE 1=1"
	args := []interface{}{}

	if opts.ProjectPath != "" {
		query += " AND project_path = ?"
		args = append(args, opts.ProjectPath)
	}
	if opts.RemoteURL != "" {
		query += " AND remote_url = ?"
		args = append(args, opts.RemoteURL)
	}
	if !opts.All && opts.Branch != "" {
		query += " AND branch = ?"
		args = append(args, opts.Branch)
	}
	if opts.Provider != "" {
		query += " AND provider = ?"
		args = append(args, opts.Provider)
	}
	if opts.SessionType != "" {
		query += " AND session_type = ?"
		args = append(args, opts.SessionType)
	}
	if opts.ProjectCategory != "" {
		query += " AND project_category = ?"
		args = append(args, opts.ProjectCategory)
	}
	if opts.OwnerID != "" {
		query += " AND owner_id = ?"
		args = append(args, opts.OwnerID)
	}
	if !opts.Since.IsZero() {
		query += " AND created_at >= ?"
		args = append(args, opts.Since.Format("2006-01-02T15:04:05Z"))
	}
	if !opts.Until.IsZero() {
		query += " AND created_at <= ?"
		args = append(args, opts.Until.Format("2006-01-02T15:04:05Z"))
	}
	query += " ORDER BY created_at DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var summaries []session.Summary
	for rows.Next() {
		var ss session.Summary
		var createdAt string
		var updatedAtMs int64
		if err := rows.Scan(&ss.ID, &ss.Provider, &ss.Agent, &ss.Branch, &ss.Summary, &ss.MessageCount, &ss.TotalTokens, &ss.ToolCallCount, &ss.ErrorCount, &ss.EstimatedCost, &ss.ActualCost, &createdAt, &updatedAtMs, &ss.OwnerID, &ss.ParentID, &ss.ProjectPath, &ss.RemoteURL, &ss.SessionType, &ss.ProjectCategory, &ss.Status); err != nil {
			return nil, fmt.Errorf("scanning session row: %w", err)
		}
		ss.CreatedAt, _ = time.Parse("2006-01-02T15:04:05Z", createdAt)
		if updatedAtMs > 0 {
			ss.UpdatedAt = time.UnixMilli(updatedAtMs)
		}
		summaries = append(summaries, ss)
	}

	return summaries, rows.Err()
}

// UpdateSummary updates only the summary column without re-writing the payload.
func (s *Store) UpdateSummary(id session.ID, summary string) error {
	result, err := s.db.Exec("UPDATE sessions SET summary = ? WHERE id = ?", summary, id)
	if err != nil {
		return fmt.Errorf("updating summary: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return session.ErrSessionNotFound
	}
	return nil
}

// UpdateSessionType sets the session_type classification tag.
func (s *Store) UpdateSessionType(id session.ID, sessionType string) error {
	result, err := s.db.Exec("UPDATE sessions SET session_type = ? WHERE id = ?", sessionType, id)
	if err != nil {
		return fmt.Errorf("updating session type: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return session.ErrSessionNotFound
	}
	return nil
}

// UpdateProjectCategory sets the project_category for all sessions matching the given project path.
func (s *Store) UpdateProjectCategory(projectPath, category string) (int, error) {
	result, err := s.db.Exec("UPDATE sessions SET project_category = ? WHERE project_path = ? AND (project_category = '' OR project_category IS NULL)", category, projectPath)
	if err != nil {
		return 0, fmt.Errorf("updating project category: %w", err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// SetProjectCategory sets the project_category for a single session by ID.
func (s *Store) SetProjectCategory(id session.ID, category string) error {
	result, err := s.db.Exec("UPDATE sessions SET project_category = ? WHERE id = ?", category, id)
	if err != nil {
		return fmt.Errorf("updating project category: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return session.ErrSessionNotFound
	}
	return nil
}

// UpdateRemoteURL sets the remote_url for a single session by ID.
func (s *Store) UpdateRemoteURL(id session.ID, remoteURL string) error {
	result, err := s.db.Exec("UPDATE sessions SET remote_url = ? WHERE id = ?", remoteURL, id)
	if err != nil {
		return fmt.Errorf("updating remote_url: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return session.ErrSessionNotFound
	}
	// Invalidate project/stats cache since project grouping may change.
	_ = s.InvalidateCache("stats:")
	return nil
}

// ListSessionsWithEmptyRemoteURL returns sessions that need their remote_url
// resolved. Only returns sessions with a non-empty project_path (candidates
// for git remote resolution). Results are ordered by created_at DESC (newest first).
func (s *Store) ListSessionsWithEmptyRemoteURL(limit int) ([]session.BackfillCandidate, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.Query(`
		SELECT id, project_path FROM sessions
		WHERE (remote_url = '' OR remote_url IS NULL)
		  AND project_path != ''
		ORDER BY created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing sessions with empty remote_url: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var candidates []session.BackfillCandidate
	for rows.Next() {
		var c session.BackfillCandidate
		if err := rows.Scan(&c.ID, &c.ProjectPath); err != nil {
			return nil, fmt.Errorf("scanning backfill candidate: %w", err)
		}
		candidates = append(candidates, c)
	}
	return candidates, rows.Err()
}

// Delete removes a session by its ID.
func (s *Store) Delete(id session.ID) error {
	result, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return session.ErrSessionNotFound
	}

	// Invalidate stats cache — session deleted.
	_ = s.InvalidateCache("stats:")
	_ = s.InvalidateCache("forecast:")
	return nil
}

// ── Fork Relations ──

// SaveForkRelation persists a fork relationship (upsert).
func (s *Store) SaveForkRelation(rel session.ForkRelation) error {
	_, err := s.db.Exec(`
		INSERT INTO session_forks (original_id, fork_id, fork_point, shared_messages, overlap_ratio, reason, fork_context, shared_input_tokens, shared_output_tokens, detected_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(original_id, fork_id) DO UPDATE SET
			fork_point=excluded.fork_point, shared_messages=excluded.shared_messages,
			overlap_ratio=excluded.overlap_ratio, reason=excluded.reason,
			fork_context=excluded.fork_context,
			shared_input_tokens=excluded.shared_input_tokens,
			shared_output_tokens=excluded.shared_output_tokens,
			detected_at=excluded.detected_at`,
		rel.OriginalID, rel.ForkID, rel.ForkPoint, rel.SharedMessages,
		rel.OverlapRatio, rel.Reason, rel.ForkContext,
		rel.SharedInputTokens, rel.SharedOutputTokens,
	)
	return err
}

// GetForkRelations retrieves all fork relations involving a session.
func (s *Store) GetForkRelations(sessionID session.ID) ([]session.ForkRelation, error) {
	rows, err := s.db.Query(`
		SELECT original_id, fork_id, fork_point, shared_messages, overlap_ratio,
		       COALESCE(reason, ''), COALESCE(fork_context, ''),
		       shared_input_tokens, shared_output_tokens
		FROM session_forks
		WHERE original_id = ? OR fork_id = ?
		ORDER BY fork_point ASC`, sessionID, sessionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var rels []session.ForkRelation
	for rows.Next() {
		var r session.ForkRelation
		if err := rows.Scan(&r.OriginalID, &r.ForkID, &r.ForkPoint, &r.SharedMessages,
			&r.OverlapRatio, &r.Reason, &r.ForkContext,
			&r.SharedInputTokens, &r.SharedOutputTokens); err != nil {
			return nil, err
		}
		rels = append(rels, r)
	}
	return rels, rows.Err()
}

// GetForkRelationsForSessions returns fork relations for multiple sessions in a single query.
func (s *Store) GetForkRelationsForSessions(ids []session.ID) (map[session.ID][]session.ForkRelation, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	// Build WHERE IN (?, ?, ...) clause.
	placeholders := make([]string, len(ids))
	args := make([]interface{}, 0, len(ids)*2)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, string(id))
	}
	inClause := strings.Join(placeholders, ",")
	// Duplicate args for the OR condition (original_id IN(...) OR fork_id IN(...)).
	args = append(args, args...)

	rows, err := s.db.Query(`
		SELECT original_id, fork_id, fork_point, shared_messages, overlap_ratio,
		       COALESCE(reason, ''), COALESCE(fork_context, ''),
		       shared_input_tokens, shared_output_tokens
		FROM session_forks
		WHERE original_id IN (`+inClause+`) OR fork_id IN (`+inClause+`)
		ORDER BY fork_point ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	// Build a lookup set for fast membership check.
	idSet := make(map[session.ID]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}

	result := make(map[session.ID][]session.ForkRelation)
	for rows.Next() {
		var r session.ForkRelation
		if scanErr := rows.Scan(&r.OriginalID, &r.ForkID, &r.ForkPoint, &r.SharedMessages,
			&r.OverlapRatio, &r.Reason, &r.ForkContext,
			&r.SharedInputTokens, &r.SharedOutputTokens); scanErr != nil {
			return nil, scanErr
		}
		// Associate this relation with the requested session(s).
		if idSet[r.OriginalID] {
			result[r.OriginalID] = append(result[r.OriginalID], r)
		}
		if idSet[r.ForkID] {
			result[r.ForkID] = append(result[r.ForkID], r)
		}
	}
	return result, rows.Err()
}

// ListAllForkRelations returns every fork relation in the database.
func (s *Store) ListAllForkRelations() ([]session.ForkRelation, error) {
	rows, err := s.db.Query(`
		SELECT original_id, fork_id, fork_point, shared_messages, overlap_ratio,
		       COALESCE(reason, ''), COALESCE(fork_context, ''),
		       shared_input_tokens, shared_output_tokens
		FROM session_forks
		ORDER BY original_id, fork_point ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var rels []session.ForkRelation
	for rows.Next() {
		var r session.ForkRelation
		if err := rows.Scan(&r.OriginalID, &r.ForkID, &r.ForkPoint, &r.SharedMessages,
			&r.OverlapRatio, &r.Reason, &r.ForkContext,
			&r.SharedInputTokens, &r.SharedOutputTokens); err != nil {
			return nil, err
		}
		rels = append(rels, r)
	}
	return rels, rows.Err()
}

// GetTotalDeduplication returns the total shared tokens across all forks.
func (s *Store) GetTotalDeduplication() (sharedInput, sharedOutput int, err error) {
	err = s.db.QueryRow(`
		SELECT COALESCE(SUM(shared_input_tokens), 0), COALESCE(SUM(shared_output_tokens), 0)
		FROM session_forks`).Scan(&sharedInput, &sharedOutput)
	return
}

// ── Session Objectives ──

// SaveObjective persists (upserts) a session objective.
func (s *Store) SaveObjective(obj session.SessionObjective) error {
	decisionsJSON, _ := json.Marshal(obj.Summary.Decisions)
	frictionJSON, _ := json.Marshal(obj.Summary.Friction)
	openItemsJSON, _ := json.Marshal(obj.Summary.OpenItems)

	_, err := s.db.Exec(`
		INSERT INTO session_objectives (session_id, intent, outcome, decisions, friction, open_items, explain_short, explain_full, computed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(session_id) DO UPDATE SET
			intent=excluded.intent, outcome=excluded.outcome,
			decisions=excluded.decisions, friction=excluded.friction,
			open_items=excluded.open_items,
			explain_short=excluded.explain_short, explain_full=excluded.explain_full,
			computed_at=excluded.computed_at`,
		obj.SessionID, obj.Summary.Intent, obj.Summary.Outcome,
		string(decisionsJSON), string(frictionJSON), string(openItemsJSON),
		obj.ExplainShort, obj.ExplainFull,
	)
	return err
}

// GetObjective retrieves the persisted objective for a session. Returns nil if not found.
func (s *Store) GetObjective(sessionID session.ID) (*session.SessionObjective, error) {
	var obj session.SessionObjective
	var decisionsJSON, frictionJSON, openItemsJSON, computedAt string

	err := s.db.QueryRow(`
		SELECT session_id, intent, outcome, decisions, friction, open_items,
		       explain_short, explain_full, computed_at
		FROM session_objectives WHERE session_id = ?`, sessionID).Scan(
		&obj.SessionID, &obj.Summary.Intent, &obj.Summary.Outcome,
		&decisionsJSON, &frictionJSON, &openItemsJSON,
		&obj.ExplainShort, &obj.ExplainFull, &computedAt,
	)
	if err != nil {
		return nil, err
	}

	_ = json.Unmarshal([]byte(decisionsJSON), &obj.Summary.Decisions)
	_ = json.Unmarshal([]byte(frictionJSON), &obj.Summary.Friction)
	_ = json.Unmarshal([]byte(openItemsJSON), &obj.Summary.OpenItems)

	obj.ComputedAt, _ = time.Parse("2006-01-02 15:04:05", computedAt)
	return &obj, nil
}

// ListObjectives retrieves objectives for multiple sessions at once.
func (s *Store) ListObjectives(sessionIDs []session.ID) (map[session.ID]*session.SessionObjective, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(sessionIDs))
	args := make([]interface{}, len(sessionIDs))
	for i, id := range sessionIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT session_id, intent, outcome, decisions, friction, open_items,
		       explain_short, explain_full, computed_at
		FROM session_objectives WHERE session_id IN (%s)`,
		strings.Join(placeholders, ","))

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make(map[session.ID]*session.SessionObjective)
	for rows.Next() {
		var obj session.SessionObjective
		var decisionsJSON, frictionJSON, openItemsJSON, computedAt string
		if err := rows.Scan(&obj.SessionID, &obj.Summary.Intent, &obj.Summary.Outcome,
			&decisionsJSON, &frictionJSON, &openItemsJSON,
			&obj.ExplainShort, &obj.ExplainFull, &computedAt); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(decisionsJSON), &obj.Summary.Decisions)
		_ = json.Unmarshal([]byte(frictionJSON), &obj.Summary.Friction)
		_ = json.Unmarshal([]byte(openItemsJSON), &obj.Summary.OpenItems)
		obj.ComputedAt, _ = time.Parse("2006-01-02 15:04:05", computedAt)
		result[obj.SessionID] = &obj
	}
	return result, rows.Err()
}

// ── Token Usage Buckets ──

// UpsertTokenBucket inserts or updates a token usage bucket.
func (s *Store) UpsertTokenBucket(b session.TokenUsageBucket) error {
	_, err := s.db.Exec(`
		INSERT INTO token_usage_buckets (bucket_start, granularity, project_path, provider, llm_backend,
			input_tokens, output_tokens, image_tokens, cache_read_tokens, cache_write_tokens,
			session_count, message_count,
			tool_call_count, tool_error_count, image_count, user_msg_count, assist_msg_count,
			estimated_cost, actual_cost, computed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(bucket_start, granularity, project_path, provider, llm_backend) DO UPDATE SET
			input_tokens=excluded.input_tokens, output_tokens=excluded.output_tokens,
			image_tokens=excluded.image_tokens,
			cache_read_tokens=excluded.cache_read_tokens, cache_write_tokens=excluded.cache_write_tokens,
			session_count=excluded.session_count,
			message_count=excluded.message_count,
			tool_call_count=excluded.tool_call_count, tool_error_count=excluded.tool_error_count,
			image_count=excluded.image_count, user_msg_count=excluded.user_msg_count,
			assist_msg_count=excluded.assist_msg_count,
			estimated_cost=excluded.estimated_cost, actual_cost=excluded.actual_cost,
			computed_at=excluded.computed_at`,
		b.BucketStart.Format(time.RFC3339), b.Granularity, b.ProjectPath, b.Provider, b.LLMBackend,
		b.InputTokens, b.OutputTokens, b.ImageTokens, b.CacheReadTokens, b.CacheWriteTokens,
		b.SessionCount, b.MessageCount,
		b.ToolCallCount, b.ToolErrorCount, b.ImageCount, b.UserMsgCount, b.AssistMsgCount,
		b.EstimatedCost, b.ActualCost,
	)
	return err
}

// QueryTokenBuckets retrieves token usage buckets for a time range and granularity.
func (s *Store) QueryTokenBuckets(granularity string, since, until time.Time, projectPath string) ([]session.TokenUsageBucket, error) {
	query := `SELECT bucket_start, granularity, project_path, provider,
		COALESCE(llm_backend,''), input_tokens, output_tokens, image_tokens,
		COALESCE(cache_read_tokens,0), COALESCE(cache_write_tokens,0),
		session_count, message_count,
		COALESCE(tool_call_count,0), COALESCE(tool_error_count,0), COALESCE(image_count,0),
		COALESCE(user_msg_count,0), COALESCE(assist_msg_count,0),
		COALESCE(estimated_cost,0), COALESCE(actual_cost,0)
		FROM token_usage_buckets WHERE granularity = ?`
	args := []interface{}{granularity}

	if !since.IsZero() {
		query += " AND bucket_start >= ?"
		args = append(args, since.Format(time.RFC3339))
	}
	if !until.IsZero() {
		query += " AND bucket_start < ?"
		args = append(args, until.Format(time.RFC3339))
	}
	if projectPath != "" {
		query += " AND project_path = ?"
		args = append(args, projectPath)
	}
	query += " ORDER BY bucket_start ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var buckets []session.TokenUsageBucket
	for rows.Next() {
		var b session.TokenUsageBucket
		var startStr string
		if err := rows.Scan(&startStr, &b.Granularity, &b.ProjectPath, &b.Provider,
			&b.LLMBackend, &b.InputTokens, &b.OutputTokens, &b.ImageTokens,
			&b.CacheReadTokens, &b.CacheWriteTokens,
			&b.SessionCount, &b.MessageCount,
			&b.ToolCallCount, &b.ToolErrorCount, &b.ImageCount, &b.UserMsgCount, &b.AssistMsgCount,
			&b.EstimatedCost, &b.ActualCost); err != nil {
			continue
		}
		b.BucketStart, _ = time.Parse(time.RFC3339, startStr)
		b.BucketEnd = b.BucketStart.Add(parseDuration(b.Granularity))
		buckets = append(buckets, b)
	}
	return buckets, rows.Err()
}

// GetLastBucketComputeTime returns the most recent computed_at for a granularity.
func (s *Store) GetLastBucketComputeTime(granularity string) (time.Time, error) {
	var ts string
	err := s.db.QueryRow("SELECT MAX(computed_at) FROM token_usage_buckets WHERE granularity = ?", granularity).Scan(&ts)
	if err != nil || ts == "" {
		return time.Time{}, nil
	}
	t, _ := time.Parse("2006-01-02 15:04:05", ts)
	return t, nil
}

func parseDuration(gran string) time.Duration {
	switch gran {
	case "1h":
		return time.Hour
	case "1d":
		return 24 * time.Hour
	default:
		return time.Hour
	}
}

// UpsertToolBucket inserts or updates a per-tool usage bucket.
func (s *Store) UpsertToolBucket(b session.ToolUsageBucket) error {
	_, err := s.db.Exec(`
		INSERT INTO tool_usage_buckets (bucket_start, granularity, project_path, tool_name, tool_category,
			call_count, input_tokens, output_tokens, error_count, total_duration_ms,
			estimated_cost, computed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(bucket_start, granularity, project_path, tool_name, tool_category) DO UPDATE SET
			call_count=excluded.call_count, input_tokens=excluded.input_tokens,
			output_tokens=excluded.output_tokens, error_count=excluded.error_count,
			total_duration_ms=excluded.total_duration_ms,
			estimated_cost=excluded.estimated_cost,
			computed_at=excluded.computed_at`,
		b.BucketStart.Format(time.RFC3339), b.Granularity, b.ProjectPath,
		b.ToolName, b.ToolCategory,
		b.CallCount, b.InputTokens, b.OutputTokens, b.ErrorCount, b.TotalDuration,
		b.EstimatedCost,
	)
	return err
}

// QueryToolBuckets retrieves per-tool usage buckets for a time range.
func (s *Store) QueryToolBuckets(granularity string, since, until time.Time, projectPath string) ([]session.ToolUsageBucket, error) {
	query := `SELECT bucket_start, granularity, project_path, tool_name, tool_category,
		call_count, input_tokens, output_tokens, error_count, total_duration_ms,
		COALESCE(estimated_cost, 0)
		FROM tool_usage_buckets WHERE granularity = ?`
	args := []interface{}{granularity}

	if !since.IsZero() {
		query += " AND bucket_start >= ?"
		args = append(args, since.Format(time.RFC3339))
	}
	if !until.IsZero() {
		query += " AND bucket_start < ?"
		args = append(args, until.Format(time.RFC3339))
	}
	if projectPath != "" {
		query += " AND project_path = ?"
		args = append(args, projectPath)
	}
	query += " ORDER BY bucket_start ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var buckets []session.ToolUsageBucket
	for rows.Next() {
		var b session.ToolUsageBucket
		var ts string
		if err := rows.Scan(&ts, &b.Granularity, &b.ProjectPath,
			&b.ToolName, &b.ToolCategory,
			&b.CallCount, &b.InputTokens, &b.OutputTokens,
			&b.ErrorCount, &b.TotalDuration,
			&b.EstimatedCost,
		); err != nil {
			return nil, err
		}
		b.BucketStart, _ = time.Parse(time.RFC3339, ts)
		b.BucketEnd = b.BucketStart.Add(parseDuration(b.Granularity))
		buckets = append(buckets, b)
	}
	return buckets, rows.Err()
}

// AddLink associates a session with a git object.
func (s *Store) AddLink(sessionID session.ID, link session.Link) error {
	// Verify session exists
	var exists int
	err := s.db.QueryRow("SELECT 1 FROM sessions WHERE id = ?", sessionID).Scan(&exists)
	if err == sql.ErrNoRows {
		return session.ErrSessionNotFound
	}
	if err != nil {
		return fmt.Errorf("checking session exists: %w", err)
	}

	_, err = s.db.Exec("INSERT INTO session_links (session_id, link_type, link_ref) VALUES (?, ?, ?)",
		sessionID, link.LinkType, link.Ref)
	if err != nil {
		return fmt.Errorf("inserting link: %w", err)
	}

	return nil
}

// GetByLink retrieves sessions linked to a specific ref (PR number, commit SHA, etc.).
func (s *Store) GetByLink(linkType session.LinkType, ref string) ([]session.Summary, error) {
	rows, err := s.db.Query(`
		SELECT s.id, s.provider, s.agent, s.branch, s.summary, s.message_count, s.total_tokens, s.tool_call_count, s.error_count, s.created_at, COALESCE(s.owner_id, ''), COALESCE(s.parent_id, ''), COALESCE(s.project_path, ''), COALESCE(s.remote_url, ''), COALESCE(s.session_type, '')
		FROM sessions s
		INNER JOIN session_links sl ON s.id = sl.session_id
		WHERE sl.link_type = ? AND sl.link_ref = ?
		ORDER BY s.created_at DESC`,
		linkType, ref,
	)
	if err != nil {
		return nil, fmt.Errorf("querying by link: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var summaries []session.Summary
	for rows.Next() {
		var ss session.Summary
		var createdAt string
		if scanErr := rows.Scan(&ss.ID, &ss.Provider, &ss.Agent, &ss.Branch, &ss.Summary, &ss.MessageCount, &ss.TotalTokens, &ss.ToolCallCount, &ss.ErrorCount, &createdAt, &ss.OwnerID, &ss.ParentID, &ss.ProjectPath, &ss.RemoteURL, &ss.SessionType); scanErr != nil {
			return nil, fmt.Errorf("scanning session row: %w", scanErr)
		}
		summaries = append(summaries, ss)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(summaries) == 0 {
		return nil, session.ErrSessionNotFound
	}

	return summaries, nil
}

func (s *Store) loadLinks(sessionID session.ID) ([]session.Link, error) {
	rows, err := s.db.Query("SELECT link_type, link_ref FROM session_links WHERE session_id = ?", sessionID)
	if err != nil {
		return nil, fmt.Errorf("loading links: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var links []session.Link
	for rows.Next() {
		var l session.Link
		if err := rows.Scan(&l.LinkType, &l.Ref); err != nil {
			return nil, fmt.Errorf("scanning link: %w", err)
		}
		links = append(links, l)
	}

	return links, rows.Err()
}

func (s *Store) loadFileChanges(sessionID session.ID) ([]session.FileChange, error) {
	rows, err := s.db.Query("SELECT file_path, change_type FROM file_changes WHERE session_id = ?", sessionID)
	if err != nil {
		return nil, fmt.Errorf("loading file changes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var changes []session.FileChange
	for rows.Next() {
		var fc session.FileChange
		if err := rows.Scan(&fc.FilePath, &fc.ChangeType); err != nil {
			return nil, fmt.Errorf("scanning file change: %w", err)
		}
		changes = append(changes, fc)
	}

	return changes, rows.Err()
}

// DeleteOlderThan removes sessions created before the given time.
// Returns the number of deleted sessions.
// Cascading deletes handle session_links and file_changes via foreign keys.
func (s *Store) DeleteOlderThan(before time.Time) (int, error) {
	result, err := s.db.Exec(
		"DELETE FROM sessions WHERE created_at < ?",
		before.Format("2006-01-02T15:04:05Z"),
	)
	if err != nil {
		return 0, fmt.Errorf("deleting old sessions: %w", err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("checking rows affected: %w", err)
	}

	if count > 0 {
		_ = s.InvalidateCache("stats:")
		_ = s.InvalidateCache("forecast:")
	}
	return int(count), nil
}

// ── Search ──

const defaultSearchLimit = 50
const maxSearchLimit = 200

// Search returns sessions matching the given query criteria.
func (s *Store) Search(query session.SearchQuery) (*session.SearchResult, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	// Build WHERE clause from filters
	where, args := buildSearchWhere(query)

	// Count total matches (before pagination)
	countQuery := "SELECT COUNT(*) FROM sessions" + where
	var totalCount int
	if err := s.db.QueryRow(countQuery, args...).Scan(&totalCount); err != nil {
		return nil, fmt.Errorf("counting search results: %w", err)
	}

	// Fetch paginated results
	selectCols := "SELECT id, provider, agent, branch, summary, message_count, total_tokens, tool_call_count, error_count, created_at, COALESCE(owner_id, ''), COALESCE(parent_id, ''), COALESCE(project_path, ''), COALESCE(remote_url, ''), COALESCE(session_type, ''), COALESCE(project_category, ''), COALESCE(status, '')"
	dataQuery := selectCols + " FROM sessions" + where + " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	dataArgs := make([]interface{}, len(args), len(args)+2)
	copy(dataArgs, args)
	dataArgs = append(dataArgs, limit, query.Offset)

	rows, err := s.db.Query(dataQuery, dataArgs...)
	if err != nil {
		return nil, fmt.Errorf("searching sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var summaries []session.Summary
	for rows.Next() {
		var ss session.Summary
		var createdAt, status string
		if err := rows.Scan(&ss.ID, &ss.Provider, &ss.Agent, &ss.Branch, &ss.Summary, &ss.MessageCount, &ss.TotalTokens, &ss.ToolCallCount, &ss.ErrorCount, &createdAt, &ss.OwnerID, &ss.ParentID, &ss.ProjectPath, &ss.RemoteURL, &ss.SessionType, &ss.ProjectCategory, &status); err != nil {
			return nil, fmt.Errorf("scanning search result: %w", err)
		}
		ss.CreatedAt, _ = time.Parse("2006-01-02T15:04:05Z", createdAt)
		ss.Status = session.SessionStatus(status)
		summaries = append(summaries, ss)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if summaries == nil {
		summaries = []session.Summary{}
	}

	return &session.SearchResult{
		Sessions:   summaries,
		TotalCount: totalCount,
		Limit:      limit,
		Offset:     query.Offset,
	}, nil
}

// buildSearchWhere constructs a WHERE clause and args from a SearchQuery.
// Returns the WHERE string (including leading " WHERE ") and the args slice.
// If no filters match, returns an empty string.
func buildSearchWhere(q session.SearchQuery) (string, []interface{}) {
	var conditions []string
	var args []interface{}

	if q.ProjectPath != "" {
		conditions = append(conditions, "project_path = ?")
		args = append(args, q.ProjectPath)
	}
	if q.RemoteURL != "" {
		conditions = append(conditions, "remote_url = ?")
		args = append(args, q.RemoteURL)
	}
	if q.Branch != "" {
		conditions = append(conditions, "branch = ?")
		args = append(args, q.Branch)
	}
	if q.Provider != "" {
		conditions = append(conditions, "provider = ?")
		args = append(args, string(q.Provider))
	}
	if q.OwnerID != "" {
		conditions = append(conditions, "owner_id = ?")
		args = append(args, string(q.OwnerID))
	}
	if q.SessionType != "" {
		conditions = append(conditions, "session_type = ?")
		args = append(args, q.SessionType)
	}
	if q.ProjectCategory != "" {
		conditions = append(conditions, "project_category = ?")
		args = append(args, q.ProjectCategory)
	}
	if !q.Since.IsZero() {
		conditions = append(conditions, "created_at >= ?")
		args = append(args, q.Since.Format("2006-01-02T15:04:05Z"))
	}
	if !q.Until.IsZero() {
		conditions = append(conditions, "created_at <= ?")
		args = append(args, q.Until.Format("2006-01-02T15:04:05Z"))
	}
	if q.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, string(q.Status))
	}
	if q.HasErrors != nil {
		if *q.HasErrors {
			conditions = append(conditions, "error_count > 0")
		} else {
			conditions = append(conditions, "(error_count = 0 OR error_count IS NULL)")
		}
	}
	if q.Keyword != "" {
		// Case-insensitive LIKE on summary (the indexed column).
		// Searching message content would require scanning the payload BLOB,
		// which is intentionally deferred to FTS5 mode.
		// Escape SQL LIKE wildcards (%, _) in user input to avoid unexpected matches.
		escaped := strings.NewReplacer("%", `\%`, "_", `\_`).Replace(q.Keyword)
		conditions = append(conditions, `summary LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escaped+"%")
	}

	if len(conditions) == 0 {
		return "", nil
	}

	return " WHERE " + strings.Join(conditions, " AND "), args
}

// SearchFacets returns aggregated counts for faceted navigation.
// Uses the same WHERE clause as Search so facet counts match the current context.
// All 7 facets are computed in a single UNION ALL query to minimize round trips.
func (s *Store) SearchFacets(q session.SearchQuery) (*session.SearchFacets, error) {
	where, baseArgs := buildSearchWhere(q)

	facets := &session.SearchFacets{}

	// Build a single UNION ALL query for all 7 facets.
	// Each sub-SELECT returns (facet_name, value, count).
	type facetSpec struct {
		name   string // tag for dispatching results
		column string
	}
	specs := []facetSpec{
		{"project", "COALESCE(NULLIF(remote_url,''), project_path)"},
		{"provider", "provider"},
		{"branch", "branch"},
		{"type", "session_type"},
		{"category", "project_category"},
		{"status", "status"},
		{"agent", "agent"},
	}

	// Build combined query: each facet is a ranked sub-query limited to top 30.
	var parts []string
	var allArgs []interface{}
	for _, spec := range specs {
		filterExpr := spec.column + " != ''"
		var sub string
		if where != "" {
			sub = "SELECT '" + spec.name + "' AS facet, " + spec.column + " AS val, COUNT(*) AS cnt FROM sessions" +
				where + " AND " + filterExpr +
				" GROUP BY val ORDER BY cnt DESC LIMIT 30"
		} else {
			sub = "SELECT '" + spec.name + "' AS facet, " + spec.column + " AS val, COUNT(*) AS cnt FROM sessions" +
				" WHERE " + filterExpr +
				" GROUP BY val ORDER BY cnt DESC LIMIT 30"
		}
		parts = append(parts, sub)
		allArgs = append(allArgs, baseArgs...)
	}

	query := strings.Join(parts, " UNION ALL ")

	rows, err := s.db.Query(query, allArgs...)
	if err != nil {
		return nil, fmt.Errorf("facets query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Dispatch rows into the correct facet slice.
	for rows.Next() {
		var facetName string
		var fv session.FacetValue
		if scanErr := rows.Scan(&facetName, &fv.Value, &fv.Count); scanErr != nil {
			return nil, fmt.Errorf("facets scan: %w", scanErr)
		}
		switch facetName {
		case "project":
			facets.Projects = append(facets.Projects, fv)
		case "provider":
			facets.Providers = append(facets.Providers, fv)
		case "branch":
			facets.Branches = append(facets.Branches, fv)
		case "type":
			facets.Types = append(facets.Types, fv)
		case "category":
			facets.Categories = append(facets.Categories, fv)
		case "status":
			facets.Statuses = append(facets.Statuses, fv)
		case "agent":
			facets.Agents = append(facets.Agents, fv)
		}
	}

	return facets, rows.Err()
}

// ── Cost denormalization methods ──

// UpdateCosts sets the denormalized estimated_cost and actual_cost columns.
func (s *Store) UpdateCosts(id session.ID, estimatedCost, actualCost float64) error {
	_, err := s.db.Exec("UPDATE sessions SET estimated_cost = ?, actual_cost = ? WHERE id = ?",
		estimatedCost, actualCost, id)
	if err != nil {
		return fmt.Errorf("updating costs for %s: %w", id, err)
	}
	return nil
}

// ListSessionsWithZeroCosts returns session IDs that have estimated_cost = 0.
// Used by the cost backfill task to find sessions needing cost computation.
func (s *Store) ListSessionsWithZeroCosts(limit int) ([]session.ID, error) {
	rows, err := s.db.Query(
		"SELECT id FROM sessions WHERE COALESCE(estimated_cost, 0) = 0 AND total_tokens > 0 ORDER BY created_at DESC LIMIT ?",
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing sessions with zero costs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []session.ID
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			continue
		}
		ids = append(ids, session.ID(id))
	}
	return ids, rows.Err()
}

// ── Materialized analytics (Phase 4 CQRS read model) ──
//
// The three methods below persist and query the session_analytics /
// session_agent_usage tables created by migration 031. They are the SQLite
// side of the CQRS materialized read model that replaces the old per-request
// message-array recompute in Forecast / CacheEfficiency / ContextSaturation /
// AgentROIAnalysis.

// marshalJSONBlob marshals v as JSON. Returns "" for nil pointers.
// json.Marshal encodes a typed nil pointer as the four-byte string "null";
// we normalize that to "" so the read path treats it the same as "never set".
func marshalJSONBlob(v interface{}) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(v)
	if err != nil || string(data) == "null" {
		return ""
	}
	return string(data)
}

// unmarshalJSONBlob deserializes a JSON TEXT column into dst.
// Empty/blank strings are treated as "no data" and leave dst unchanged.
func unmarshalJSONBlob(raw string, dst interface{}) {
	if raw == "" {
		return
	}
	_ = json.Unmarshal([]byte(raw), dst)
}

// UpsertSessionAnalytics stores or replaces the materialized analytics row
// for a session along with its sibling session_agent_usage rows.
// The entire operation runs in a single transaction: the parent row in
// session_analytics is upserted, all existing session_agent_usage rows for
// the session are deleted, and the AgentUsage slice is inserted fresh.
func (s *Store) UpsertSessionAnalytics(a session.Analytics) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx for UpsertSessionAnalytics: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Marshal the 6 JSON blob columns.
	wasteJSON := marshalJSONBlob(a.WasteBreakdown)
	freshnessJSON := marshalJSONBlob(a.Freshness)
	overloadJSON := marshalJSONBlob(a.Overload)
	syspromptJSON := marshalJSONBlob(a.PromptData)
	fitnessJSON := marshalJSONBlob(a.FitnessData)
	forecastJSON := marshalJSONBlob(a.ForecastInput)

	_, err = tx.Exec(`
		INSERT INTO session_analytics (
			session_id,
			peak_input_tokens, dominant_model, max_context_window, peak_saturation_pct,
			has_compaction, compaction_count, compaction_drop_pct, compaction_wasted_tokens,
			cache_read_tokens, cache_write_tokens, input_tokens,
			cache_miss_count, cache_wasted_tokens, longest_gap_mins, session_avg_gap_mins,
			backend, estimated_cost, actual_cost, fork_offset, deduplicated_cost,
			total_agent_invocations, unique_agents_used, agent_tokens, agent_cost, total_wasted_tokens,
			waste_breakdown_json, freshness_json, overload_json, sysprompt_json, fitness_json, forecast_input_json,
			schema_version, computed_at
		) VALUES (
			?,
			?,?,?,?,
			?,?,?,?,
			?,?,?,
			?,?,?,?,
			?,?,?,?,?,
			?,?,?,?,?,
			?,?,?,?,?,?,
			?,?
		)
		ON CONFLICT(session_id) DO UPDATE SET
			peak_input_tokens=excluded.peak_input_tokens, dominant_model=excluded.dominant_model,
			max_context_window=excluded.max_context_window, peak_saturation_pct=excluded.peak_saturation_pct,
			has_compaction=excluded.has_compaction, compaction_count=excluded.compaction_count,
			compaction_drop_pct=excluded.compaction_drop_pct, compaction_wasted_tokens=excluded.compaction_wasted_tokens,
			cache_read_tokens=excluded.cache_read_tokens, cache_write_tokens=excluded.cache_write_tokens,
			input_tokens=excluded.input_tokens,
			cache_miss_count=excluded.cache_miss_count, cache_wasted_tokens=excluded.cache_wasted_tokens,
			longest_gap_mins=excluded.longest_gap_mins, session_avg_gap_mins=excluded.session_avg_gap_mins,
			backend=excluded.backend, estimated_cost=excluded.estimated_cost,
			actual_cost=excluded.actual_cost, fork_offset=excluded.fork_offset,
			deduplicated_cost=excluded.deduplicated_cost,
			total_agent_invocations=excluded.total_agent_invocations, unique_agents_used=excluded.unique_agents_used,
			agent_tokens=excluded.agent_tokens, agent_cost=excluded.agent_cost,
			total_wasted_tokens=excluded.total_wasted_tokens,
			waste_breakdown_json=excluded.waste_breakdown_json, freshness_json=excluded.freshness_json,
			overload_json=excluded.overload_json, sysprompt_json=excluded.sysprompt_json,
			fitness_json=excluded.fitness_json, forecast_input_json=excluded.forecast_input_json,
			schema_version=excluded.schema_version, computed_at=excluded.computed_at`,
		a.SessionID,
		a.PeakInputTokens, a.DominantModel, a.MaxContextWindow, a.PeakSaturationPct,
		boolToInt(a.HasCompaction), a.CompactionCount, a.CompactionDropPct, a.CompactionWastedTokens,
		a.CacheReadTokens, a.CacheWriteTokens, a.InputTokens,
		a.CacheMissCount, a.CacheWastedTokens, a.LongestGapMins, a.SessionAvgGapMins,
		a.Backend, a.EstimatedCost, a.ActualCost, a.ForkOffset, a.DeduplicatedCost,
		a.TotalAgentInvocations, a.UniqueAgentsUsed, a.AgentTokens, a.AgentCost, a.TotalWastedTokens,
		wasteJSON, freshnessJSON, overloadJSON, syspromptJSON, fitnessJSON, forecastJSON,
		a.SchemaVersion, a.ComputedAt.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("upsert session_analytics for %s: %w", a.SessionID, err)
	}

	// Replace agent usage rows atomically: delete all, then insert fresh.
	if _, err := tx.Exec("DELETE FROM session_agent_usage WHERE session_id = ?", a.SessionID); err != nil {
		return fmt.Errorf("delete session_agent_usage for %s: %w", a.SessionID, err)
	}
	if len(a.AgentUsage) > 0 {
		stmt, err := tx.Prepare(`INSERT INTO session_agent_usage
			(session_id, agent_name, invocations, tokens, cost, errors)
			VALUES (?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("prepare session_agent_usage insert: %w", err)
		}
		defer func() { _ = stmt.Close() }()
		for _, au := range a.AgentUsage {
			if _, err := stmt.Exec(a.SessionID, au.AgentName, au.Invocations, au.Tokens, au.Cost, au.Errors); err != nil {
				return fmt.Errorf("insert session_agent_usage %s/%s: %w", a.SessionID, au.AgentName, err)
			}
		}
	}

	return tx.Commit()
}

// GetSessionAnalytics retrieves the materialized analytics for a single
// session, with its AgentUsage slice hydrated from session_agent_usage.
// Returns (nil, nil) — not an error — when no row exists.
func (s *Store) GetSessionAnalytics(id session.ID) (*session.Analytics, error) {
	var a session.Analytics
	var hasCompaction int
	var wasteJSON, freshnessJSON, overloadJSON, syspromptJSON, fitnessJSON, forecastJSON string
	var computedAtStr string

	err := s.db.QueryRow(`
		SELECT session_id,
			peak_input_tokens, dominant_model, max_context_window, peak_saturation_pct,
			has_compaction, compaction_count, compaction_drop_pct, compaction_wasted_tokens,
			cache_read_tokens, cache_write_tokens, input_tokens,
			cache_miss_count, cache_wasted_tokens, longest_gap_mins, session_avg_gap_mins,
			backend, estimated_cost, actual_cost, fork_offset, deduplicated_cost,
			total_agent_invocations, unique_agents_used, agent_tokens, agent_cost, total_wasted_tokens,
			COALESCE(waste_breakdown_json,''), COALESCE(freshness_json,''),
			COALESCE(overload_json,''), COALESCE(sysprompt_json,''),
			COALESCE(fitness_json,''), COALESCE(forecast_input_json,''),
			schema_version, computed_at
		FROM session_analytics WHERE session_id = ?`, id,
	).Scan(
		&a.SessionID,
		&a.PeakInputTokens, &a.DominantModel, &a.MaxContextWindow, &a.PeakSaturationPct,
		&hasCompaction, &a.CompactionCount, &a.CompactionDropPct, &a.CompactionWastedTokens,
		&a.CacheReadTokens, &a.CacheWriteTokens, &a.InputTokens,
		&a.CacheMissCount, &a.CacheWastedTokens, &a.LongestGapMins, &a.SessionAvgGapMins,
		&a.Backend, &a.EstimatedCost, &a.ActualCost, &a.ForkOffset, &a.DeduplicatedCost,
		&a.TotalAgentInvocations, &a.UniqueAgentsUsed, &a.AgentTokens, &a.AgentCost, &a.TotalWastedTokens,
		&wasteJSON, &freshnessJSON, &overloadJSON, &syspromptJSON, &fitnessJSON, &forecastJSON,
		&a.SchemaVersion, &computedAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session_analytics for %s: %w", id, err)
	}

	a.HasCompaction = hasCompaction != 0
	a.ComputedAt, _ = time.Parse(time.RFC3339, computedAtStr)

	// Unmarshal JSON blobs into pointer fields (empty strings → nil pointers).
	if wasteJSON != "" {
		a.WasteBreakdown = new(session.TokenWasteBreakdown)
		unmarshalJSONBlob(wasteJSON, a.WasteBreakdown)
	}
	if freshnessJSON != "" {
		a.Freshness = new(session.SessionFreshness)
		unmarshalJSONBlob(freshnessJSON, a.Freshness)
	}
	if overloadJSON != "" {
		a.Overload = new(session.OverloadAnalysis)
		unmarshalJSONBlob(overloadJSON, a.Overload)
	}
	if syspromptJSON != "" {
		a.PromptData = new(session.SessionPromptData)
		unmarshalJSONBlob(syspromptJSON, a.PromptData)
	}
	if fitnessJSON != "" {
		a.FitnessData = new(session.SessionFitnessData)
		unmarshalJSONBlob(fitnessJSON, a.FitnessData)
	}
	if forecastJSON != "" {
		a.ForecastInput = new(session.SessionForecastInput)
		unmarshalJSONBlob(forecastJSON, a.ForecastInput)
	}

	// Hydrate AgentUsage from sibling table.
	agentRows, err := s.db.Query(
		"SELECT agent_name, invocations, tokens, cost, errors FROM session_agent_usage WHERE session_id = ? ORDER BY agent_name",
		id,
	)
	if err != nil {
		return nil, fmt.Errorf("query session_agent_usage for %s: %w", id, err)
	}
	defer func() { _ = agentRows.Close() }()

	for agentRows.Next() {
		var au session.AgentUsage
		if scanErr := agentRows.Scan(&au.AgentName, &au.Invocations, &au.Tokens, &au.Cost, &au.Errors); scanErr != nil {
			continue
		}
		a.AgentUsage = append(a.AgentUsage, au)
	}
	if err := agentRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session_agent_usage for %s: %w", id, err)
	}

	return &a, nil
}

// QueryAnalytics returns analytics rows matching the filter, joined with
// the parent sessions table to populate AnalyticsRow.ProjectPath, CreatedAt,
// and Branch in a single query. AgentUsage is NOT hydrated (hot paths
// aggregate at parent level; per-agent reads go through GetSessionAnalytics).
func (s *Store) QueryAnalytics(filter session.AnalyticsFilter) ([]session.AnalyticsRow, error) {
	query := `
		SELECT
			sa.session_id,
			sa.peak_input_tokens, sa.dominant_model, sa.max_context_window, sa.peak_saturation_pct,
			sa.has_compaction, sa.compaction_count, sa.compaction_drop_pct, sa.compaction_wasted_tokens,
			sa.cache_read_tokens, sa.cache_write_tokens, sa.input_tokens,
			sa.cache_miss_count, sa.cache_wasted_tokens, sa.longest_gap_mins, sa.session_avg_gap_mins,
			sa.backend, sa.estimated_cost, sa.actual_cost, sa.fork_offset, sa.deduplicated_cost,
			sa.total_agent_invocations, sa.unique_agents_used, sa.agent_tokens, sa.agent_cost, sa.total_wasted_tokens,
			COALESCE(sa.waste_breakdown_json,''), COALESCE(sa.freshness_json,''),
			COALESCE(sa.overload_json,''), COALESCE(sa.sysprompt_json,''),
			COALESCE(sa.fitness_json,''), COALESCE(sa.forecast_input_json,''),
			sa.schema_version, sa.computed_at,
			s.project_path, s.created_at, COALESCE(s.branch,''),
			COALESCE(s.summary,''), COALESCE(s.agent,''),
			COALESCE(s.message_count,0), COALESCE(s.total_tokens,0),
			COALESCE(s.tool_call_count,0), COALESCE(s.error_count,0),
			COALESCE(s.session_type,''), COALESCE(s.status,'')
		FROM session_analytics sa
		JOIN sessions s ON sa.session_id = s.id
		WHERE 1=1`
	var args []interface{}

	if filter.ProjectPath != "" {
		query += " AND s.project_path = ?"
		args = append(args, filter.ProjectPath)
	}
	if !filter.Since.IsZero() {
		query += " AND s.created_at >= ?"
		args = append(args, filter.Since.Format(time.RFC3339))
	}
	if !filter.Until.IsZero() {
		query += " AND s.created_at <= ?"
		args = append(args, filter.Until.Format(time.RFC3339))
	}
	if filter.Backend != "" {
		query += " AND sa.backend = ?"
		args = append(args, filter.Backend)
	}
	if filter.MinSchemaVersion > 0 {
		query += " AND sa.schema_version >= ?"
		args = append(args, filter.MinSchemaVersion)
	}

	query += " ORDER BY s.created_at DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query session_analytics: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []session.AnalyticsRow
	for rows.Next() {
		var ar session.AnalyticsRow
		var hasCompaction int
		var wasteJSON, freshnessJSON, overloadJSON, syspromptJSON, fitnessJSON, forecastJSON string
		var computedAtStr, createdAtStr string

		var statusStr string
		if err := rows.Scan(
			&ar.SessionID,
			&ar.PeakInputTokens, &ar.DominantModel, &ar.MaxContextWindow, &ar.PeakSaturationPct,
			&hasCompaction, &ar.CompactionCount, &ar.CompactionDropPct, &ar.CompactionWastedTokens,
			&ar.CacheReadTokens, &ar.CacheWriteTokens, &ar.InputTokens,
			&ar.CacheMissCount, &ar.CacheWastedTokens, &ar.LongestGapMins, &ar.SessionAvgGapMins,
			&ar.Backend, &ar.EstimatedCost, &ar.ActualCost, &ar.ForkOffset, &ar.DeduplicatedCost,
			&ar.TotalAgentInvocations, &ar.UniqueAgentsUsed, &ar.AgentTokens, &ar.AgentCost, &ar.TotalWastedTokens,
			&wasteJSON, &freshnessJSON, &overloadJSON, &syspromptJSON, &fitnessJSON, &forecastJSON,
			&ar.SchemaVersion, &computedAtStr,
			&ar.ProjectPath, &createdAtStr, &ar.Branch,
			&ar.Summary, &ar.Agent,
			&ar.MessageCount, &ar.TotalTokens,
			&ar.ToolCallCount, &ar.ErrorCount,
			&ar.SessionType, &statusStr,
		); err != nil {
			continue
		}

		ar.HasCompaction = hasCompaction != 0
		ar.ComputedAt, _ = time.Parse(time.RFC3339, computedAtStr)
		ar.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		ar.Status = session.SessionStatus(statusStr)

		// Unmarshal JSON blobs.
		if wasteJSON != "" {
			ar.WasteBreakdown = new(session.TokenWasteBreakdown)
			unmarshalJSONBlob(wasteJSON, ar.WasteBreakdown)
		}
		if freshnessJSON != "" {
			ar.Freshness = new(session.SessionFreshness)
			unmarshalJSONBlob(freshnessJSON, ar.Freshness)
		}
		if overloadJSON != "" {
			ar.Overload = new(session.OverloadAnalysis)
			unmarshalJSONBlob(overloadJSON, ar.Overload)
		}
		if syspromptJSON != "" {
			ar.PromptData = new(session.SessionPromptData)
			unmarshalJSONBlob(syspromptJSON, ar.PromptData)
		}
		if fitnessJSON != "" {
			ar.FitnessData = new(session.SessionFitnessData)
			unmarshalJSONBlob(fitnessJSON, ar.FitnessData)
		}
		if forecastJSON != "" {
			ar.ForecastInput = new(session.SessionForecastInput)
			unmarshalJSONBlob(forecastJSON, ar.ForecastInput)
		}

		result = append(result, ar)
	}

	return result, rows.Err()
}

// ListSessionsNeedingAnalytics returns session IDs that either have no row
// in session_analytics at all, or have a row with schema_version < the
// provided minSchemaVersion. Results are ordered by created_at DESC so that
// the most recent sessions are backfilled first.
func (s *Store) ListSessionsNeedingAnalytics(minSchemaVersion int, limit int) ([]session.ID, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(`
		SELECT s.id FROM sessions s
		LEFT JOIN session_analytics sa ON s.id = sa.session_id
		WHERE sa.session_id IS NULL OR sa.schema_version < ?
		ORDER BY s.created_at DESC
		LIMIT ?
	`, minSchemaVersion, limit)
	if err != nil {
		return nil, fmt.Errorf("listing sessions needing analytics: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []session.ID
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			continue
		}
		ids = append(ids, session.ID(id))
	}
	return ids, rows.Err()
}

// ── Blame methods ──

// GetSessionsByFile returns sessions that touched the given file path.
// Results are ordered by created_at DESC. Optional filters narrow by branch/provider.
func (s *Store) GetSessionsByFile(query session.BlameQuery) ([]session.BlameEntry, error) {
	var conditions []string
	var args []interface{}

	conditions = append(conditions, "fc.file_path = ?")
	args = append(args, query.FilePath)

	if query.Branch != "" {
		conditions = append(conditions, "s.branch = ?")
		args = append(args, query.Branch)
	}
	if query.Provider != "" {
		conditions = append(conditions, "s.provider = ?")
		args = append(args, string(query.Provider))
	}
	if query.ExcludeReads {
		conditions = append(conditions, "fc.change_type IN ('created', 'modified', 'deleted')")
	}

	where := " WHERE " + strings.Join(conditions, " AND ")

	q := `SELECT s.id, s.provider, s.branch, s.summary, s.created_at,
	             COALESCE(s.owner_id, ''), fc.change_type
	      FROM sessions s
	      JOIN file_changes fc ON fc.session_id = s.id` + where + `
	      ORDER BY s.created_at DESC`

	if query.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", query.Limit)
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("querying blame: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []session.BlameEntry
	for rows.Next() {
		var e session.BlameEntry
		var createdAt string
		if err := rows.Scan(&e.SessionID, &e.Provider, &e.Branch, &e.Summary,
			&createdAt, &e.OwnerID, &e.ChangeType); err != nil {
			return nil, fmt.Errorf("scanning blame entry: %w", err)
		}
		e.CreatedAt, _ = time.Parse("2006-01-02T15:04:05Z", createdAt)
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if entries == nil {
		entries = []session.BlameEntry{}
	}

	return entries, nil
}

// ReplaceSessionFiles atomically replaces all file_changes for a session.
// Used by the file-blame extractor after parsing tool calls.
func (s *Store) ReplaceSessionFiles(sessionID session.ID, records []session.SessionFileRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec("DELETE FROM file_changes WHERE session_id = ?", sessionID); err != nil {
		return fmt.Errorf("deleting old file changes: %w", err)
	}

	stmt, err := tx.Prepare("INSERT INTO file_changes (session_id, file_path, change_type, tool_name) VALUES (?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("preparing file insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, r := range records {
		if _, err := stmt.Exec(r.SessionID, r.FilePath, r.ChangeType, r.ToolName); err != nil {
			return fmt.Errorf("inserting file record %q: %w", r.FilePath, err)
		}
	}

	return tx.Commit()
}

// GetSessionFileChanges returns all file changes for a given session.
func (s *Store) GetSessionFileChanges(sessionID session.ID) ([]session.SessionFileRecord, error) {
	rows, err := s.db.Query(
		`SELECT session_id, file_path, change_type, COALESCE(tool_name, '')
		 FROM file_changes WHERE session_id = ? ORDER BY file_path`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying session files: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var records []session.SessionFileRecord
	for rows.Next() {
		var r session.SessionFileRecord
		if err := rows.Scan(&r.SessionID, &r.FilePath, &r.ChangeType, &r.ToolName); err != nil {
			return nil, fmt.Errorf("scanning file record: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if records == nil {
		records = []session.SessionFileRecord{}
	}
	return records, nil
}

// CountSessionsWithFiles returns how many sessions have at least one file_changes row.
func (s *Store) CountSessionsWithFiles() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(DISTINCT session_id) FROM file_changes").Scan(&count)
	return count, err
}

// TopFilesForProject returns the most frequently touched files for a project,
// aggregated from file_changes joined with sessions. Results are sorted by
// session count descending.
func (s *Store) TopFilesForProject(projectPath string, limit int) ([]session.TopFileEntry, error) {
	if limit <= 0 {
		limit = 10
	}
	q := `SELECT fc.file_path,
	             COUNT(DISTINCT fc.session_id) AS sess_count,
	             COUNT(DISTINCT CASE WHEN fc.change_type IN ('created','modified','deleted') THEN fc.session_id END) AS write_count
	      FROM file_changes fc
	      JOIN sessions s ON s.id = fc.session_id
	      WHERE s.project_path = ?
	      GROUP BY fc.file_path
	      ORDER BY sess_count DESC
	      LIMIT ?`
	rows, err := s.db.Query(q, projectPath, limit)
	if err != nil {
		return nil, fmt.Errorf("querying top files: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []session.TopFileEntry
	for rows.Next() {
		var e session.TopFileEntry
		if err := rows.Scan(&e.FilePath, &e.SessionCount, &e.WriteCount); err != nil {
			return nil, fmt.Errorf("scanning top file: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []session.TopFileEntry{}
	}
	return entries, nil
}

// FilesForProject returns all files touched in a project with blame summary.
// Each entry includes session count, write count, and the most recent session info.
// If dirPrefix is non-empty, only files under that directory prefix are returned.
func (s *Store) FilesForProject(projectPath string, dirPrefix string, limit int) ([]session.ProjectFileEntry, error) {
	if limit <= 0 {
		limit = 500
	}

	// Only include files that were actually written (created/modified/deleted),
	// not merely read. Also filter to files under the project directory.
	where := "WHERE s.project_path = ? AND fc.file_path LIKE ? AND fc.change_type IN ('created','modified','deleted')"
	args := []any{projectPath, strings.TrimSuffix(projectPath, "/") + "/%"}
	if dirPrefix != "" {
		where += " AND fc.file_path LIKE ?"
		args = append(args, dirPrefix+"%")
	}
	args = append(args, limit)

	// Subquery: aggregate per file, then join back to get the last session info.
	q := `
	WITH file_agg AS (
		SELECT fc.file_path,
		       COUNT(DISTINCT fc.session_id) AS sess_count,
		       COUNT(DISTINCT fc.session_id) AS write_count,
		       MAX(s.created_at) AS last_time
		  FROM file_changes fc
		  JOIN sessions s ON s.id = fc.session_id
		  ` + where + `
		  GROUP BY fc.file_path
	),
	last_session AS (
		SELECT fa.file_path, fa.sess_count, fa.write_count, fa.last_time,
		       s.id AS last_session_id,
		       COALESCE(s.summary, '') AS last_summary,
		       COALESCE(s.branch, '') AS last_branch,
		       COALESCE(s.provider, '') AS last_provider,
		       COALESCE(s.commit_sha, '') AS last_commit_sha,
		       COALESCE(fc.change_type, '') AS last_change_type
		  FROM file_agg fa
		  JOIN sessions s ON s.project_path = ? AND s.created_at = fa.last_time
		  JOIN file_changes fc ON fc.session_id = s.id AND fc.file_path = fa.file_path
	)
	SELECT file_path, sess_count, write_count, last_change_type,
	       last_session_id, last_time, last_summary, last_branch, last_provider, last_commit_sha
	  FROM last_session
	  ORDER BY last_time DESC
	  LIMIT ?`

	// We need to add projectPath again for the second WHERE in last_session join.
	allArgs := make([]any, 0, len(args)+1)
	allArgs = append(allArgs, args[:len(args)-1]...) // where args without limit
	allArgs = append(allArgs, projectPath)           // for the second project_path match
	allArgs = append(allArgs, args[len(args)-1])     // limit

	rows, err := s.db.Query(q, allArgs...)
	if err != nil {
		return nil, fmt.Errorf("querying files for project: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []session.ProjectFileEntry
	for rows.Next() {
		var e session.ProjectFileEntry
		var provider, changeType, lastTime string
		if err := rows.Scan(
			&e.FilePath, &e.SessionCount, &e.WriteCount, &changeType,
			&e.LastSessionID, &lastTime, &e.LastSummary, &e.LastBranch, &provider, &e.LastCommitSHA,
		); err != nil {
			return nil, fmt.Errorf("scanning project file: %w", err)
		}
		e.LastSessionTime, _ = time.Parse("2006-01-02T15:04:05Z", lastTime)
		e.LastProvider = session.ProviderName(provider)
		e.LastChangeType = session.ChangeType(changeType)
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []session.ProjectFileEntry{}
	}
	return entries, nil
}

// ── User methods ──

// SaveUser creates or updates a user. If a user with the same email exists,
// the name, source, kind, and role are updated.
func (s *Store) SaveUser(user *session.User) error {
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now().UTC()
	}
	kind := string(user.Kind)
	if kind == "" {
		kind = string(session.UserKindUnknown)
	}
	role := string(user.Role)
	if role == "" {
		role = string(session.UserRoleMember)
	}

	_, err := s.db.Exec(`
		INSERT INTO users (id, name, email, source, created_at, kind, slack_id, slack_name, role)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(email) DO UPDATE SET
			name=excluded.name, source=excluded.source, kind=excluded.kind, role=excluded.role`,
		user.ID, user.Name, user.Email, user.Source,
		user.CreatedAt.Format("2006-01-02T15:04:05Z"),
		kind, user.SlackID, user.SlackName, role,
	)
	if err != nil {
		return fmt.Errorf("saving user: %w", err)
	}

	return nil
}

// scanUser scans a user row with all columns including the enriched fields.
func scanUser(scan func(dest ...any) error) (*session.User, error) {
	var u session.User
	var createdAt, kind, slackID, slackName, role string
	err := scan(&u.ID, &u.Name, &u.Email, &u.Source, &createdAt, &kind, &slackID, &slackName, &role)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.CreatedAt, _ = time.Parse("2006-01-02T15:04:05Z", createdAt)
	u.Kind = session.UserKind(kind)
	u.SlackID = slackID
	u.SlackName = slackName
	u.Role = session.UserRole(role)
	return &u, nil
}

const userColumns = "id, name, email, source, created_at, kind, slack_id, slack_name, role"

// GetUser retrieves a user by ID.
func (s *Store) GetUser(id session.ID) (*session.User, error) {
	row := s.db.QueryRow("SELECT "+userColumns+" FROM users WHERE id = ?", id)
	u, err := scanUser(row.Scan)
	if err != nil {
		return nil, fmt.Errorf("querying user: %w", err)
	}
	return u, nil
}

// GetUserByEmail retrieves a user by email address.
// Returns nil, nil if no user matches.
func (s *Store) GetUserByEmail(email string) (*session.User, error) {
	row := s.db.QueryRow("SELECT "+userColumns+" FROM users WHERE email = ?", email)
	u, err := scanUser(row.Scan)
	if err != nil {
		return nil, fmt.Errorf("querying user by email: %w", err)
	}
	return u, nil
}

// ListUsers returns all users ordered by name.
func (s *Store) ListUsers() ([]*session.User, error) {
	rows, err := s.db.Query("SELECT " + userColumns + " FROM users ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var users []*session.User
	for rows.Next() {
		u, scanErr := scanUser(rows.Scan)
		if scanErr != nil {
			return nil, fmt.Errorf("scanning user: %w", scanErr)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// ListUsersByKind returns users filtered by kind ("human", "machine", "unknown").
func (s *Store) ListUsersByKind(kind string) ([]*session.User, error) {
	rows, err := s.db.Query("SELECT "+userColumns+" FROM users WHERE kind = ? ORDER BY name", kind)
	if err != nil {
		return nil, fmt.Errorf("listing users by kind: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var users []*session.User
	for rows.Next() {
		u, scanErr := scanUser(rows.Scan)
		if scanErr != nil {
			return nil, fmt.Errorf("scanning user: %w", scanErr)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// UpdateUserSlack sets the Slack identity fields for a user.
func (s *Store) UpdateUserSlack(id session.ID, slackID, slackName string) error {
	res, err := s.db.Exec("UPDATE users SET slack_id = ?, slack_name = ? WHERE id = ?", slackID, slackName, id)
	if err != nil {
		return fmt.Errorf("updating user slack: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user %s not found", id)
	}
	return nil
}

// UpdateUserKind sets the kind classification for a user.
func (s *Store) UpdateUserKind(id session.ID, kind string) error {
	res, err := s.db.Exec("UPDATE users SET kind = ? WHERE id = ?", kind, id)
	if err != nil {
		return fmt.Errorf("updating user kind: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user %s not found", id)
	}
	return nil
}

// UpdateUserRole sets the notification role for a user.
func (s *Store) UpdateUserRole(id session.ID, role string) error {
	res, err := s.db.Exec("UPDATE users SET role = ? WHERE id = ?", role, id)
	if err != nil {
		return fmt.Errorf("updating user role: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user %s not found", id)
	}
	return nil
}

// OwnerStats returns aggregated session statistics grouped by owner_id
// for a given time range and optional project filter (empty = all projects).
func (s *Store) OwnerStats(projectPath string, since, until time.Time) ([]session.OwnerStat, error) {
	rows, err := s.db.Query(`
		SELECT
			s.owner_id,
			COALESCE(u.name, '') as owner_name,
			COALESCE(u.email, '') as owner_email,
			COALESCE(u.kind, 'unknown') as owner_kind,
			COUNT(*) as session_count,
			COALESCE(SUM(s.total_tokens), 0) as total_tokens,
			COALESCE(SUM(s.error_count), 0) as error_count
		FROM sessions s
		LEFT JOIN users u ON s.owner_id = u.id
		WHERE s.created_at >= ? AND s.created_at <= ?
			AND (? = '' OR s.project_path = ?)
		GROUP BY s.owner_id
		ORDER BY session_count DESC`,
		since.Format("2006-01-02T15:04:05Z"),
		until.Format("2006-01-02T15:04:05Z"),
		projectPath, projectPath,
	)
	if err != nil {
		return nil, fmt.Errorf("querying owner stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var stats []session.OwnerStat
	for rows.Next() {
		var os session.OwnerStat
		if err := rows.Scan(&os.OwnerID, &os.OwnerName, &os.OwnerEmail, &os.OwnerKind,
			&os.SessionCount, &os.TotalTokens, &os.ErrorCount); err != nil {
			return nil, fmt.Errorf("scanning owner stat: %w", err)
		}
		stats = append(stats, os)
	}
	return stats, rows.Err()
}

// ── Auth Users ──

const timeFormat = "2006-01-02T15:04:05Z"

// CreateAuthUser persists a new auth user.
func (s *Store) CreateAuthUser(user *auth.User) error {
	_, err := s.db.Exec(`
		INSERT INTO auth_users (id, username, password_hash, role, active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		user.ID, user.Username, user.PasswordHash, user.Role, boolToInt(user.Active),
		user.CreatedAt.Format(timeFormat), user.UpdatedAt.Format(timeFormat),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return auth.ErrUserExists
		}
		return fmt.Errorf("creating auth user: %w", err)
	}
	return nil
}

// GetAuthUser retrieves an auth user by ID.
func (s *Store) GetAuthUser(id string) (*auth.User, error) {
	return s.scanAuthUser(s.db.QueryRow(
		"SELECT id, username, password_hash, role, active, created_at, updated_at FROM auth_users WHERE id = ?", id))
}

// GetAuthUserByUsername retrieves an auth user by username.
func (s *Store) GetAuthUserByUsername(username string) (*auth.User, error) {
	return s.scanAuthUser(s.db.QueryRow(
		"SELECT id, username, password_hash, role, active, created_at, updated_at FROM auth_users WHERE username = ?", username))
}

// UpdateAuthUser updates mutable fields on an auth user.
func (s *Store) UpdateAuthUser(user *auth.User) error {
	result, err := s.db.Exec(`
		UPDATE auth_users SET password_hash = ?, role = ?, active = ?, updated_at = ?
		WHERE id = ?`,
		user.PasswordHash, user.Role, boolToInt(user.Active),
		user.UpdatedAt.Format(timeFormat), user.ID,
	)
	if err != nil {
		return fmt.Errorf("updating auth user: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return auth.ErrUserNotFound
	}
	return nil
}

// ListAuthUsers returns all auth users ordered by created_at ASC.
func (s *Store) ListAuthUsers() ([]*auth.User, error) {
	rows, err := s.db.Query(
		"SELECT id, username, password_hash, role, active, created_at, updated_at FROM auth_users ORDER BY created_at ASC")
	if err != nil {
		return nil, fmt.Errorf("listing auth users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var users []*auth.User
	for rows.Next() {
		u, scanErr := s.scanAuthUserRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// CountAuthUsers returns the total number of auth users.
func (s *Store) CountAuthUsers() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM auth_users").Scan(&count)
	return count, err
}

// scanAuthUser scans a single auth_users row.
func (s *Store) scanAuthUser(row *sql.Row) (*auth.User, error) {
	var u auth.User
	var active int
	var createdAt, updatedAt string

	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &active, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, auth.ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning auth user: %w", err)
	}

	u.Active = active != 0
	u.CreatedAt, _ = time.Parse(timeFormat, createdAt)
	u.UpdatedAt, _ = time.Parse(timeFormat, updatedAt)
	return &u, nil
}

// scanAuthUserRow scans from an sql.Rows cursor (for ListAuthUsers).
func (s *Store) scanAuthUserRow(rows *sql.Rows) (*auth.User, error) {
	var u auth.User
	var active int
	var createdAt, updatedAt string

	err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &active, &createdAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("scanning auth user row: %w", err)
	}

	u.Active = active != 0
	u.CreatedAt, _ = time.Parse(timeFormat, createdAt)
	u.UpdatedAt, _ = time.Parse(timeFormat, updatedAt)
	return &u, nil
}

// boolToInt converts a bool to SQLite integer (0/1).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ── Auth API Keys ──

// CreateAPIKey persists a new API key.
func (s *Store) CreateAPIKey(key *auth.APIKey) error {
	var expiresAt *string
	if key.ExpiresAt != nil {
		v := key.ExpiresAt.Format(timeFormat)
		expiresAt = &v
	}

	_, err := s.db.Exec(`
		INSERT INTO auth_api_keys (id, user_id, name, key_hash, key_prefix, active, expires_at, last_used_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		key.ID, key.UserID, key.Name, key.KeyHash, key.KeyPrefix,
		boolToInt(key.Active), expiresAt, nil, key.CreatedAt.Format(timeFormat),
	)
	if err != nil {
		return fmt.Errorf("creating API key: %w", err)
	}
	return nil
}

// GetAPIKeyByHash retrieves an API key by its SHA-256 hash.
func (s *Store) GetAPIKeyByHash(keyHash string) (*auth.APIKey, error) {
	return s.scanAPIKey(s.db.QueryRow(
		"SELECT id, user_id, name, key_hash, key_prefix, active, expires_at, last_used_at, created_at FROM auth_api_keys WHERE key_hash = ?",
		keyHash))
}

// ListAPIKeysByUser returns all API keys for a user, newest first.
func (s *Store) ListAPIKeysByUser(userID string) ([]*auth.APIKey, error) {
	rows, err := s.db.Query(
		"SELECT id, user_id, name, key_hash, key_prefix, active, expires_at, last_used_at, created_at FROM auth_api_keys WHERE user_id = ? ORDER BY created_at DESC",
		userID)
	if err != nil {
		return nil, fmt.Errorf("listing API keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var keys []*auth.APIKey
	for rows.Next() {
		k, scanErr := s.scanAPIKeyRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// UpdateAPIKey updates mutable fields on an API key.
func (s *Store) UpdateAPIKey(key *auth.APIKey) error {
	var lastUsedAt *string
	if key.LastUsedAt != nil {
		v := key.LastUsedAt.Format(timeFormat)
		lastUsedAt = &v
	}

	result, err := s.db.Exec(`UPDATE auth_api_keys SET active = ?, last_used_at = ? WHERE id = ?`,
		boolToInt(key.Active), lastUsedAt, key.ID)
	if err != nil {
		return fmt.Errorf("updating API key: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return auth.ErrAPIKeyNotFound
	}
	return nil
}

// DeleteAPIKey removes an API key by ID.
func (s *Store) DeleteAPIKey(id string) error {
	result, err := s.db.Exec("DELETE FROM auth_api_keys WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting API key: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return auth.ErrAPIKeyNotFound
	}
	return nil
}

// scanAPIKey scans a single auth_api_keys row.
func (s *Store) scanAPIKey(row *sql.Row) (*auth.APIKey, error) {
	var k auth.APIKey
	var active int
	var expiresAt, lastUsedAt sql.NullString
	var createdAt string

	err := row.Scan(&k.ID, &k.UserID, &k.Name, &k.KeyHash, &k.KeyPrefix,
		&active, &expiresAt, &lastUsedAt, &createdAt)
	if err == sql.ErrNoRows {
		return nil, auth.ErrAPIKeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning API key: %w", err)
	}

	k.Active = active != 0
	k.CreatedAt, _ = time.Parse(timeFormat, createdAt)
	if expiresAt.Valid {
		t, _ := time.Parse(timeFormat, expiresAt.String)
		k.ExpiresAt = &t
	}
	if lastUsedAt.Valid {
		t, _ := time.Parse(timeFormat, lastUsedAt.String)
		k.LastUsedAt = &t
	}
	return &k, nil
}

// scanAPIKeyRow scans from an sql.Rows cursor.
func (s *Store) scanAPIKeyRow(rows *sql.Rows) (*auth.APIKey, error) {
	var k auth.APIKey
	var active int
	var expiresAt, lastUsedAt sql.NullString
	var createdAt string

	err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.KeyHash, &k.KeyPrefix,
		&active, &expiresAt, &lastUsedAt, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("scanning API key row: %w", err)
	}

	k.Active = active != 0
	k.CreatedAt, _ = time.Parse(timeFormat, createdAt)
	if expiresAt.Valid {
		t, _ := time.Parse(timeFormat, expiresAt.String)
		k.ExpiresAt = &t
	}
	if lastUsedAt.Valid {
		t, _ := time.Parse(timeFormat, lastUsedAt.String)
		k.LastUsedAt = &t
	}
	return &k, nil
}

// ── Session Analysis ──

// SaveAnalysis persists a session analysis (upsert by ID).
func (s *Store) SaveAnalysis(a *analysis.SessionAnalysis) error {
	report, err := json.Marshal(a.Report)
	if err != nil {
		return fmt.Errorf("marshaling analysis report: %w", err)
	}

	report, err = compressPayload(report)
	if err != nil {
		return fmt.Errorf("compressing analysis report: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO session_analyses (id, session_id, created_at, trigger, adapter, model, tokens_used, duration_ms, error, report)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			session_id=excluded.session_id, created_at=excluded.created_at,
			trigger=excluded.trigger, adapter=excluded.adapter, model=excluded.model,
			tokens_used=excluded.tokens_used, duration_ms=excluded.duration_ms,
			error=excluded.error, report=excluded.report`,
		a.ID, a.SessionID, a.CreatedAt.Format("2006-01-02T15:04:05Z"),
		a.Trigger, a.Adapter, a.Model, a.TokensUsed, a.DurationMs, a.Error, report,
	)
	if err != nil {
		return fmt.Errorf("upserting analysis: %w", err)
	}
	return nil
}

// GetAnalysis retrieves a session analysis by its ID.
func (s *Store) GetAnalysis(id string) (*analysis.SessionAnalysis, error) {
	a, err := s.scanAnalysis(s.db.QueryRow(`
		SELECT id, session_id, created_at, trigger, adapter, model, tokens_used, duration_ms, error, report
		FROM session_analyses WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, storage.ErrAnalysisNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying analysis: %w", err)
	}
	return a, nil
}

// GetAnalysisBySession retrieves the most recent analysis for a session.
func (s *Store) GetAnalysisBySession(sessionID string) (*analysis.SessionAnalysis, error) {
	a, err := s.scanAnalysis(s.db.QueryRow(`
		SELECT id, session_id, created_at, trigger, adapter, model, tokens_used, duration_ms, error, report
		FROM session_analyses WHERE session_id = ? ORDER BY created_at DESC LIMIT 1`, sessionID))
	if err == sql.ErrNoRows {
		return nil, storage.ErrAnalysisNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying analysis by session: %w", err)
	}
	return a, nil
}

// ListAnalyses returns all analyses for a session, ordered by created_at DESC.
func (s *Store) ListAnalyses(sessionID string) ([]*analysis.SessionAnalysis, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, created_at, trigger, adapter, model, tokens_used, duration_ms, error, report
		FROM session_analyses WHERE session_id = ? ORDER BY created_at DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("listing analyses: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []*analysis.SessionAnalysis
	for rows.Next() {
		a, scanErr := s.scanAnalysisRow(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scanning analysis row: %w", scanErr)
		}
		results = append(results, a)
	}
	return results, rows.Err()
}

// scanAnalysis extracts a SessionAnalysis from a single-row query result.
func (s *Store) scanAnalysis(row *sql.Row) (*analysis.SessionAnalysis, error) {
	var a analysis.SessionAnalysis
	var createdAt, trigger, adapter string
	var model, errStr sql.NullString
	var report []byte

	err := row.Scan(&a.ID, &a.SessionID, &createdAt, &trigger, &adapter,
		&model, &a.TokensUsed, &a.DurationMs, &errStr, &report)
	if err != nil {
		return nil, err
	}

	a.CreatedAt, _ = time.Parse("2006-01-02T15:04:05Z", createdAt)
	a.Trigger = analysis.Trigger(trigger)
	a.Adapter = analysis.AdapterName(adapter)
	if model.Valid {
		a.Model = model.String
	}
	if errStr.Valid {
		a.Error = errStr.String
	}
	if len(report) > 0 {
		report, err = decompressPayload(report)
		if err != nil {
			return nil, fmt.Errorf("decompressing analysis report: %w", err)
		}
		if unmarshalErr := json.Unmarshal(report, &a.Report); unmarshalErr != nil {
			return nil, fmt.Errorf("unmarshaling analysis report: %w", unmarshalErr)
		}
	}

	return &a, nil
}

// scanAnalysisRow extracts a SessionAnalysis from a rows iterator.
func (s *Store) scanAnalysisRow(rows *sql.Rows) (*analysis.SessionAnalysis, error) {
	var a analysis.SessionAnalysis
	var createdAt, trigger, adapter string
	var model, errStr sql.NullString
	var report []byte

	err := rows.Scan(&a.ID, &a.SessionID, &createdAt, &trigger, &adapter,
		&model, &a.TokensUsed, &a.DurationMs, &errStr, &report)
	if err != nil {
		return nil, err
	}

	a.CreatedAt, _ = time.Parse("2006-01-02T15:04:05Z", createdAt)
	a.Trigger = analysis.Trigger(trigger)
	a.Adapter = analysis.AdapterName(adapter)
	if model.Valid {
		a.Model = model.String
	}
	if errStr.Valid {
		a.Error = errStr.String
	}
	if len(report) > 0 {
		report, err = decompressPayload(report)
		if err != nil {
			return nil, fmt.Errorf("decompressing analysis report: %w", err)
		}
		if unmarshalErr := json.Unmarshal(report, &a.Report); unmarshalErr != nil {
			return nil, fmt.Errorf("unmarshaling analysis report: %w", unmarshalErr)
		}
	}

	return &a, nil
}

// ── Cache ──

func (s *Store) GetCache(key string, maxAge time.Duration) ([]byte, error) {
	var value []byte
	var updatedAt string
	err := s.db.QueryRow(
		"SELECT value, updated_at FROM stats_cache WHERE key = ?", key,
	).Scan(&value, &updatedAt)
	if err != nil {
		return nil, nil // miss
	}
	t, tErr := time.Parse("2006-01-02T15:04:05Z", updatedAt)
	if tErr != nil {
		return nil, nil // corrupt timestamp → treat as miss
	}
	if time.Since(t) > maxAge {
		return nil, nil // expired
	}
	return value, nil
}

func (s *Store) SetCache(key string, value []byte) error {
	_, err := s.db.Exec(`
		INSERT INTO stats_cache (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	)
	return err
}

func (s *Store) InvalidateCache(prefix string) error {
	if prefix == "" {
		_, err := s.db.Exec("DELETE FROM stats_cache")
		return err
	}
	_, err := s.db.Exec("DELETE FROM stats_cache WHERE key LIKE ?", prefix+"%")
	return err
}

// ── User Preferences ──

func (s *Store) GetPreferences(userID session.ID) (*session.UserPreferences, error) {
	var prefsJSON string
	var updatedAt string
	err := s.db.QueryRow(
		"SELECT preferences, updated_at FROM user_preferences WHERE user_id = ?",
		string(userID),
	).Scan(&prefsJSON, &updatedAt)
	if err != nil {
		return nil, nil // not found → caller should use system defaults
	}

	var prefs session.UserPreferences
	if jsonErr := json.Unmarshal([]byte(prefsJSON), &prefs); jsonErr != nil {
		return nil, fmt.Errorf("unmarshaling preferences: %w", jsonErr)
	}
	prefs.UserID = userID
	if t, tErr := time.Parse("2006-01-02T15:04:05Z", updatedAt); tErr == nil {
		prefs.UpdatedAt = t
	}
	return &prefs, nil
}

func (s *Store) SavePreferences(prefs *session.UserPreferences) error {
	data, err := json.Marshal(prefs)
	if err != nil {
		return fmt.Errorf("marshaling preferences: %w", err)
	}
	_, err = s.db.Exec(`
		INSERT INTO user_preferences (user_id, preferences, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			preferences = excluded.preferences,
			updated_at = excluded.updated_at`,
		string(prefs.UserID),
		string(data),
		time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	)
	if err != nil {
		return fmt.Errorf("saving preferences: %w", err)
	}
	return nil
}

// ── Session-to-Session Links ──

// LinkSessions creates a bidirectional link between two sessions.
func (s *Store) LinkSessions(link session.SessionLink) error {
	// Validate sessions exist.
	for _, sid := range []session.ID{link.SourceSessionID, link.TargetSessionID} {
		var exists int
		err := s.db.QueryRow("SELECT 1 FROM sessions WHERE id = ?", sid).Scan(&exists)
		if err == sql.ErrNoRows {
			return fmt.Errorf("session %q not found", sid)
		}
		if err != nil {
			return fmt.Errorf("checking session %q: %w", sid, err)
		}
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	// Generate IDs if not provided.
	if link.ID == "" {
		link.ID = session.NewID()
	}
	inverseID := session.NewID()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Insert source → target.
	_, err = tx.Exec(`
		INSERT INTO session_session_links (id, source_session_id, target_session_id, link_type, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_session_id, target_session_id, link_type) DO NOTHING`,
		link.ID, link.SourceSessionID, link.TargetSessionID, link.LinkType, link.Description, now,
	)
	if err != nil {
		return fmt.Errorf("inserting session link: %w", err)
	}

	// Insert target → source (inverse direction).
	inverse := link.LinkType.Inverse()
	_, err = tx.Exec(`
		INSERT INTO session_session_links (id, source_session_id, target_session_id, link_type, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_session_id, target_session_id, link_type) DO NOTHING`,
		inverseID, link.TargetSessionID, link.SourceSessionID, inverse, link.Description, now,
	)
	if err != nil {
		return fmt.Errorf("inserting inverse session link: %w", err)
	}

	return tx.Commit()
}

// GetLinkedSessions retrieves all session links where the given session is the source.
func (s *Store) GetLinkedSessions(sessionID session.ID) ([]session.SessionLink, error) {
	rows, err := s.db.Query(`
		SELECT id, source_session_id, target_session_id, link_type, COALESCE(description, ''), created_at
		FROM session_session_links
		WHERE source_session_id = ?
		ORDER BY created_at DESC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying session links: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var links []session.SessionLink
	for rows.Next() {
		var l session.SessionLink
		var createdAt string
		if err := rows.Scan(&l.ID, &l.SourceSessionID, &l.TargetSessionID, &l.LinkType, &l.Description, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning session link: %w", err)
		}
		l.CreatedAt, _ = time.Parse("2006-01-02T15:04:05Z", createdAt)
		links = append(links, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating session links: %w", err)
	}

	return links, nil
}

// DeleteSessionLink removes a session-to-session link by its ID.
func (s *Store) DeleteSessionLink(id session.ID) error {
	result, err := s.db.Exec("DELETE FROM session_session_links WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting session link: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("session link %q not found", id)
	}
	return nil
}

// ── Migrations ──

// GetFreshness returns the stored message count and source-updated-at timestamp
// for a session. Returns (0, 0, nil) if the session doesn't exist.
func (s *Store) GetFreshness(id session.ID) (int, int64, error) {
	var messageCount int
	var sourceUpdatedAt int64
	err := s.db.QueryRow(
		`SELECT COALESCE(message_count, 0), COALESCE(source_updated_at, 0) FROM sessions WHERE id = ?`,
		id,
	).Scan(&messageCount, &sourceUpdatedAt)
	if err == sql.ErrNoRows {
		return 0, 0, nil // first capture — session not in store yet
	}
	if err != nil {
		return 0, 0, fmt.Errorf("querying session freshness: %w", err)
	}
	return messageCount, sourceUpdatedAt, nil
}

// ListProjects returns all distinct projects, grouped by remote_url when available,
// or by project_path for non-git projects. Sorted by session count descending.
func (s *Store) ListProjects() ([]session.ProjectGroup, error) {
	// Prefer non-worktree paths when grouping. The COALESCE+NULLIF trick
	// tries MIN() of non-worktree paths first, falling back to any path.
	rows, err := s.db.Query(`
		SELECT
			COALESCE(MAX(remote_url), '') AS remote_url,
			COALESCE(
				MIN(CASE WHEN project_path NOT LIKE '%/worktree/%'
				          AND project_path NOT LIKE '%/.opencode-worktrees/%'
				     THEN project_path END),
				MIN(project_path)
			) AS project_path,
			MAX(provider) AS provider,
			COALESCE(MAX(project_category), '') AS category,
			COUNT(*) AS session_count,
			COALESCE(SUM(total_tokens), 0) AS total_tokens
		FROM sessions
		GROUP BY
			CASE WHEN remote_url != '' THEN remote_url ELSE project_path END
		ORDER BY session_count DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var groups []session.ProjectGroup
	for rows.Next() {
		var pg session.ProjectGroup
		if err := rows.Scan(&pg.RemoteURL, &pg.ProjectPath, &pg.Provider, &pg.Category, &pg.SessionCount, &pg.TotalTokens); err != nil {
			return nil, fmt.Errorf("scanning project group: %w", err)
		}
		// Build human-friendly display name.
		if pg.RemoteURL != "" {
			// "github.com/org/repo" → "org/repo"
			parts := strings.SplitN(pg.RemoteURL, "/", 2)
			if len(parts) == 2 {
				pg.DisplayName = parts[1]
			} else {
				pg.DisplayName = pg.RemoteURL
			}
		} else {
			// Use the last folder component of project_path.
			idx := strings.LastIndex(pg.ProjectPath, "/")
			if idx >= 0 && idx < len(pg.ProjectPath)-1 {
				pg.DisplayName = pg.ProjectPath[idx+1:]
			} else {
				pg.DisplayName = pg.ProjectPath
			}
		}
		groups = append(groups, pg)
	}

	return groups, rows.Err()
}

// runMigrations applies schema migrations that cannot be expressed as IF NOT EXISTS in the base schema.
func runMigrations(db *sql.DB) error {
	// Migration 001: users table + owner_id column on sessions
	if _, err := db.Exec(migration001); err != nil {
		return fmt.Errorf("migration 001 (users table): %w", err)
	}

	// Add owner_id column to sessions if it doesn't exist.
	// SQLite doesn't support IF NOT EXISTS for ALTER TABLE, so we check first.
	if !columnExists(db, "sessions", "owner_id") {
		if _, err := db.Exec("ALTER TABLE sessions ADD COLUMN owner_id TEXT"); err != nil {
			return fmt.Errorf("migration 001 (owner_id column): %w", err)
		}
	}

	// Migration 002: session_analyses table
	if _, err := db.Exec(migration002); err != nil {
		return fmt.Errorf("migration 002 (session_analyses table): %w", err)
	}

	// Migration 003: tool_call_count and error_count columns on sessions
	if !columnExists(db, "sessions", "tool_call_count") {
		if _, err := db.Exec("ALTER TABLE sessions ADD COLUMN tool_call_count INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("migration 003 (tool_call_count column): %w", err)
		}
	}
	if !columnExists(db, "sessions", "error_count") {
		if _, err := db.Exec("ALTER TABLE sessions ADD COLUMN error_count INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("migration 003 (error_count column): %w", err)
		}
		// Backfill from payload JSON for existing sessions
		if err := backfillToolCounts(db); err != nil {
			return fmt.Errorf("migration 003 (backfill): %w", err)
		}
	}

	// Migration 004: user_preferences table
	if _, err := db.Exec(migration004); err != nil {
		return fmt.Errorf("migration 004 (user_preferences table): %w", err)
	}

	// Migration 005: source_updated_at column for skip-if-unchanged optimization.
	// Stores the source provider's last-updated timestamp (epoch ms) at capture time.
	if !columnExists(db, "sessions", "source_updated_at") {
		if _, err := db.Exec("ALTER TABLE sessions ADD COLUMN source_updated_at INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("migration 005 (source_updated_at column): %w", err)
		}
	}

	// Migration 006: stats_cache table
	if _, err := db.Exec(migration006StatsCache); err != nil {
		return fmt.Errorf("migration 006 (stats_cache table): %w", err)
	}

	// Migration 007: session_session_links table
	if _, err := db.Exec(migration007SessionLinks); err != nil {
		return fmt.Errorf("migration 007 (session_session_links table): %w", err)
	}

	// Migration 008: remote_url column on sessions
	if !columnExists(db, "sessions", "remote_url") {
		if _, err := db.Exec("ALTER TABLE sessions ADD COLUMN remote_url TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("migration 008 (remote_url column): %w", err)
		}
		if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_sessions_remote_url ON sessions(remote_url)"); err != nil {
			return fmt.Errorf("migration 008 (remote_url index): %w", err)
		}
	}

	// Migration 009: session_type column on sessions
	if !columnExists(db, "sessions", "session_type") {
		if _, err := db.Exec("ALTER TABLE sessions ADD COLUMN session_type TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("migration 009 (session_type column): %w", err)
		}
		if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_sessions_session_type ON sessions(session_type)"); err != nil {
			return fmt.Errorf("migration 009 (session_type index): %w", err)
		}
	}

	// Migration 010: auth_users and auth_api_keys tables
	if _, err := db.Exec(migration010Auth); err != nil {
		return fmt.Errorf("migration 010 (auth tables): %w", err)
	}

	// Migration 011: project_category column on sessions
	if !columnExists(db, "sessions", "project_category") {
		if _, err := db.Exec("ALTER TABLE sessions ADD COLUMN project_category TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("migration 011 (project_category): %w", err)
		}
		if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_sessions_project_category ON sessions(project_category)"); err != nil {
			return fmt.Errorf("migration 011 (project_category index): %w", err)
		}
	}

	// Migration 012: session_forks table for fork detection results.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS session_forks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		original_id TEXT NOT NULL,
		fork_id TEXT NOT NULL,
		fork_point INTEGER NOT NULL DEFAULT 0,
		shared_messages INTEGER NOT NULL DEFAULT 0,
		overlap_ratio REAL NOT NULL DEFAULT 0,
		reason TEXT NOT NULL DEFAULT '',
		fork_context TEXT NOT NULL DEFAULT '',
		shared_input_tokens INTEGER NOT NULL DEFAULT 0,
		shared_output_tokens INTEGER NOT NULL DEFAULT 0,
		detected_at TEXT NOT NULL DEFAULT '',
		UNIQUE(original_id, fork_id)
	)`); err != nil {
		return fmt.Errorf("migration 012 (session_forks): %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_session_forks_original ON session_forks(original_id)"); err != nil {
		return fmt.Errorf("migration 012 (session_forks index original): %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_session_forks_fork ON session_forks(fork_id)"); err != nil {
		return fmt.Errorf("migration 012 (session_forks index fork): %w", err)
	}

	// Migration 013: session_objectives table for persisted work descriptions.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS session_objectives (
		session_id TEXT PRIMARY KEY,
		intent TEXT NOT NULL DEFAULT '',
		outcome TEXT NOT NULL DEFAULT '',
		decisions TEXT NOT NULL DEFAULT '[]',
		friction TEXT NOT NULL DEFAULT '[]',
		open_items TEXT NOT NULL DEFAULT '[]',
		explain_short TEXT NOT NULL DEFAULT '',
		explain_full TEXT NOT NULL DEFAULT '',
		computed_at TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return fmt.Errorf("migration 013 (session_objectives): %w", err)
	}

	// Migration 014: token_usage_buckets table for pre-computed hourly/daily stats.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS token_usage_buckets (
		bucket_start TEXT NOT NULL,
		granularity TEXT NOT NULL DEFAULT '1h',
		project_path TEXT NOT NULL DEFAULT '',
		provider TEXT NOT NULL DEFAULT '',
		input_tokens INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		image_tokens INTEGER NOT NULL DEFAULT 0,
		session_count INTEGER NOT NULL DEFAULT 0,
		message_count INTEGER NOT NULL DEFAULT 0,
		computed_at TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (bucket_start, granularity, project_path, provider)
	)`); err != nil {
		return fmt.Errorf("migration 014 (token_usage_buckets): %w", err)
	}

	// Migration 015: add detail columns to token_usage_buckets.
	for _, col := range []string{"tool_call_count", "tool_error_count", "image_count", "user_msg_count", "assist_msg_count"} {
		if !columnExists(db, "token_usage_buckets", col) {
			if _, err := db.Exec(fmt.Sprintf("ALTER TABLE token_usage_buckets ADD COLUMN %s INTEGER NOT NULL DEFAULT 0", col)); err != nil {
				return fmt.Errorf("migration 015 (%s): %w", col, err)
			}
		}
	}

	// Migration 016: status column on sessions.
	if !columnExists(db, "sessions", "status") {
		if _, err := db.Exec("ALTER TABLE sessions ADD COLUMN status TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("migration 016 (status): %w", err)
		}
	}

	// Migration 017: session_errors table for structured error tracking.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS session_errors (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		category TEXT NOT NULL DEFAULT 'unknown',
		source TEXT NOT NULL DEFAULT 'client',
		message TEXT NOT NULL DEFAULT '',
		raw_error TEXT NOT NULL DEFAULT '',
		tool_name TEXT NOT NULL DEFAULT '',
		tool_call_id TEXT NOT NULL DEFAULT '',
		message_id TEXT NOT NULL DEFAULT '',
		message_index INTEGER NOT NULL DEFAULT 0,
		http_status INTEGER NOT NULL DEFAULT 0,
		provider_name TEXT NOT NULL DEFAULT '',
		request_id TEXT NOT NULL DEFAULT '',
		headers TEXT NOT NULL DEFAULT '{}',
		occurred_at TEXT NOT NULL DEFAULT '',
		duration_ms INTEGER NOT NULL DEFAULT 0,
		is_retryable INTEGER NOT NULL DEFAULT 0,
		confidence TEXT NOT NULL DEFAULT 'low',
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
	)`); err != nil {
		return fmt.Errorf("migration 017 (session_errors table): %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_session_errors_session ON session_errors(session_id)"); err != nil {
		return fmt.Errorf("migration 017 (session_errors index session): %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_session_errors_occurred ON session_errors(occurred_at)"); err != nil {
		return fmt.Errorf("migration 017 (session_errors index occurred): %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_session_errors_category ON session_errors(category)"); err != nil {
		return fmt.Errorf("migration 017 (session_errors index category): %w", err)
	}

	// Migration 018: session_events table for structured event tracking
	// and event_buckets table for pre-computed hourly/daily aggregations.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS session_events (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		event_type TEXT NOT NULL,
		message_index INTEGER NOT NULL DEFAULT 0,
		message_id TEXT NOT NULL DEFAULT '',
		occurred_at TEXT NOT NULL DEFAULT '',
		project_path TEXT NOT NULL DEFAULT '',
		remote_url TEXT NOT NULL DEFAULT '',
		provider TEXT NOT NULL DEFAULT '',
		agent TEXT NOT NULL DEFAULT '',
		payload BLOB,
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
	)`); err != nil {
		return fmt.Errorf("migration 018 (session_events table): %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_session_events_session ON session_events(session_id)"); err != nil {
		return fmt.Errorf("migration 018 (session_events index session): %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_session_events_type ON session_events(event_type)"); err != nil {
		return fmt.Errorf("migration 018 (session_events index type): %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_session_events_occurred ON session_events(occurred_at)"); err != nil {
		return fmt.Errorf("migration 018 (session_events index occurred): %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_session_events_project ON session_events(project_path)"); err != nil {
		return fmt.Errorf("migration 018 (session_events index project): %w", err)
	}

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS event_buckets (
		bucket_start TEXT NOT NULL,
		granularity TEXT NOT NULL DEFAULT '1h',
		project_path TEXT NOT NULL DEFAULT '',
		remote_url TEXT NOT NULL DEFAULT '',
		provider TEXT NOT NULL DEFAULT '',
		tool_call_count INTEGER NOT NULL DEFAULT 0,
		tool_error_count INTEGER NOT NULL DEFAULT 0,
		unique_tools INTEGER NOT NULL DEFAULT 0,
		top_tools TEXT NOT NULL DEFAULT '{}',
		skill_load_count INTEGER NOT NULL DEFAULT 0,
		unique_skills INTEGER NOT NULL DEFAULT 0,
		top_skills TEXT NOT NULL DEFAULT '{}',
		session_count INTEGER NOT NULL DEFAULT 0,
		agent_breakdown TEXT NOT NULL DEFAULT '{}',
		command_count INTEGER NOT NULL DEFAULT 0,
		command_error_count INTEGER NOT NULL DEFAULT 0,
		top_commands TEXT NOT NULL DEFAULT '{}',
		error_count INTEGER NOT NULL DEFAULT 0,
		error_by_category TEXT NOT NULL DEFAULT '{}',
		image_count INTEGER NOT NULL DEFAULT 0,
		image_tokens INTEGER NOT NULL DEFAULT 0,
		computed_at TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (bucket_start, granularity, project_path, provider)
	)`); err != nil {
		return fmt.Errorf("migration 018 (event_buckets table): %w", err)
	}

	// Migration 019: add llm_backend, estimated_cost, actual_cost to token_usage_buckets.
	for _, col := range []string{"llm_backend"} {
		if !columnExists(db, "token_usage_buckets", col) {
			if _, err := db.Exec("ALTER TABLE token_usage_buckets ADD COLUMN llm_backend TEXT NOT NULL DEFAULT ''"); err != nil {
				return fmt.Errorf("migration 019 (llm_backend): %w", err)
			}
		}
	}
	for _, col := range []string{"estimated_cost", "actual_cost"} {
		if !columnExists(db, "token_usage_buckets", col) {
			if _, err := db.Exec(fmt.Sprintf("ALTER TABLE token_usage_buckets ADD COLUMN %s REAL NOT NULL DEFAULT 0", col)); err != nil {
				return fmt.Errorf("migration 019 (%s): %w", col, err)
			}
		}
	}
	// Recreate unique index to include llm_backend in the key.
	// The old PRIMARY KEY was (bucket_start, granularity, project_path, provider).
	// We create a new unique index that includes llm_backend for the new bucket key.
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_token_buckets_key
		ON token_usage_buckets(bucket_start, granularity, project_path, provider, llm_backend)`); err != nil {
		return fmt.Errorf("migration 019 (token_buckets unique index): %w", err)
	}

	// Migration 020: recreate token_usage_buckets with llm_backend in the PRIMARY KEY.
	// The old PK was (bucket_start, granularity, project_path, provider) which rejected
	// inserts with different llm_backend values for the same time slot.
	// We recreate the table with the correct PK. Data is recomputed via `aisync usage compute`.
	{
		var hasBadPK bool
		// Check if the PK still lacks llm_backend by inspecting table_info.
		pkRows, pkErr := db.Query("PRAGMA table_info(token_usage_buckets)")
		if pkErr == nil {
			defer func() { _ = pkRows.Close() }()
			pkCols := make(map[string]bool)
			for pkRows.Next() {
				var cid int
				var name, typeName string
				var notNull, pkFlag int
				var dflt sql.NullString
				if scanErr := pkRows.Scan(&cid, &name, &typeName, &notNull, &dflt, &pkFlag); scanErr == nil && pkFlag > 0 {
					pkCols[name] = true
				}
			}
			_ = pkRows.Close()
			// If PK has 4 columns without llm_backend, we need to recreate.
			hasBadPK = len(pkCols) == 4 && !pkCols["llm_backend"]
		}

		if hasBadPK {
			// Drop and recreate with correct PK. Data will be recomputed.
			if _, err := db.Exec(`DROP TABLE IF EXISTS token_usage_buckets`); err != nil {
				return fmt.Errorf("migration 020 (drop old table): %w", err)
			}
			if _, err := db.Exec(`CREATE TABLE token_usage_buckets (
				bucket_start TEXT NOT NULL,
				granularity TEXT NOT NULL DEFAULT '1h',
				project_path TEXT NOT NULL DEFAULT '',
				provider TEXT NOT NULL DEFAULT '',
				llm_backend TEXT NOT NULL DEFAULT '',
				input_tokens INTEGER NOT NULL DEFAULT 0,
				output_tokens INTEGER NOT NULL DEFAULT 0,
				image_tokens INTEGER NOT NULL DEFAULT 0,
				session_count INTEGER NOT NULL DEFAULT 0,
				message_count INTEGER NOT NULL DEFAULT 0,
				tool_call_count INTEGER NOT NULL DEFAULT 0,
				tool_error_count INTEGER NOT NULL DEFAULT 0,
				image_count INTEGER NOT NULL DEFAULT 0,
				user_msg_count INTEGER NOT NULL DEFAULT 0,
				assist_msg_count INTEGER NOT NULL DEFAULT 0,
				estimated_cost REAL NOT NULL DEFAULT 0,
				actual_cost REAL NOT NULL DEFAULT 0,
				computed_at TEXT NOT NULL DEFAULT '',
				PRIMARY KEY (bucket_start, granularity, project_path, provider, llm_backend)
			)`); err != nil {
				return fmt.Errorf("migration 020 (create new table): %w", err)
			}
		}
	}

	// ── Migration 021: tool_usage_buckets table for per-tool cost tracking ──
	{
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS tool_usage_buckets (
			bucket_start TEXT NOT NULL,
			granularity TEXT NOT NULL DEFAULT '1d',
			project_path TEXT NOT NULL DEFAULT '',
			tool_name TEXT NOT NULL DEFAULT '',
			tool_category TEXT NOT NULL DEFAULT 'builtin',
			call_count INTEGER NOT NULL DEFAULT 0,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			error_count INTEGER NOT NULL DEFAULT 0,
			total_duration_ms INTEGER NOT NULL DEFAULT 0,
			estimated_cost REAL NOT NULL DEFAULT 0,
			computed_at TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (bucket_start, granularity, project_path, tool_name, tool_category)
		)`); err != nil {
			return fmt.Errorf("migration 021 (tool_usage_buckets): %w", err)
		}
	}

	// ── Migration 022: add cache token columns to token_usage_buckets ──
	{
		// Add columns if they don't exist (idempotent).
		for _, col := range []string{
			"ALTER TABLE token_usage_buckets ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0",
			"ALTER TABLE token_usage_buckets ADD COLUMN cache_write_tokens INTEGER NOT NULL DEFAULT 0",
		} {
			_, _ = db.Exec(col) // ignore "duplicate column" errors
		}
	}

	// ── Migration 023: project_snapshots table for registry persistence ──
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS project_snapshots (
		id TEXT PRIMARY KEY,
		project_path TEXT NOT NULL,
		scanned_at TEXT NOT NULL,
		change_type TEXT NOT NULL DEFAULT 'initial',
		capabilities_added INTEGER NOT NULL DEFAULT 0,
		capabilities_removed INTEGER NOT NULL DEFAULT 0,
		mcp_servers_added INTEGER NOT NULL DEFAULT 0,
		mcp_servers_removed INTEGER NOT NULL DEFAULT 0,
		payload BLOB NOT NULL
	)`); err != nil {
		return fmt.Errorf("migration 023 (project_snapshots): %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_project_snapshots_path_time ON project_snapshots(project_path, scanned_at DESC)"); err != nil {
		return fmt.Errorf("migration 023 (project_snapshots index): %w", err)
	}

	// ── Migration 024: add tool_name column to file_changes for richer blame data ──
	{
		if !columnExists(db, "file_changes", "tool_name") {
			if _, err := db.Exec("ALTER TABLE file_changes ADD COLUMN tool_name TEXT NOT NULL DEFAULT ''"); err != nil {
				return fmt.Errorf("migration 024 (file_changes tool_name): %w", err)
			}
		}
		// Add composite index for efficient blame queries.
		if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_file_changes_session ON file_changes(session_id)"); err != nil {
			return fmt.Errorf("migration 024 (file_changes session index): %w", err)
		}
	}

	// ── Migration 025: add top_mcp_servers, skill_tokens, compaction_count to event_buckets ──
	{
		for _, col := range []struct{ name, ddl string }{
			{"top_mcp_servers", "ALTER TABLE event_buckets ADD COLUMN top_mcp_servers TEXT NOT NULL DEFAULT '{}'"},
			{"skill_tokens", "ALTER TABLE event_buckets ADD COLUMN skill_tokens TEXT NOT NULL DEFAULT '{}'"},
			{"compaction_count", "ALTER TABLE event_buckets ADD COLUMN compaction_count INTEGER NOT NULL DEFAULT 0"},
		} {
			if !columnExists(db, "event_buckets", col.name) {
				if _, err := db.Exec(col.ddl); err != nil {
					return fmt.Errorf("migration 025 (%s): %w", col.name, err)
				}
			}
		}
	}

	// ── Migration 026: pull_requests + session_pull_requests tables ──
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS pull_requests (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		repo_owner TEXT NOT NULL,
		repo_name TEXT NOT NULL,
		number INTEGER NOT NULL,
		title TEXT NOT NULL DEFAULT '',
		branch TEXT NOT NULL DEFAULT '',
		base_branch TEXT NOT NULL DEFAULT '',
		state TEXT NOT NULL DEFAULT 'open',
		author TEXT NOT NULL DEFAULT '',
		url TEXT NOT NULL DEFAULT '',
		additions INTEGER NOT NULL DEFAULT 0,
		deletions INTEGER NOT NULL DEFAULT 0,
		comments INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL DEFAULT '',
		merged_at TEXT NOT NULL DEFAULT '',
		closed_at TEXT NOT NULL DEFAULT '',
		UNIQUE(repo_owner, repo_name, number)
	)`); err != nil {
		return fmt.Errorf("migration 026 (pull_requests): %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_pull_requests_branch ON pull_requests(branch)"); err != nil {
		return fmt.Errorf("migration 026 (pull_requests branch index): %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_pull_requests_state ON pull_requests(state)"); err != nil {
		return fmt.Errorf("migration 026 (pull_requests state index): %w", err)
	}

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS session_pull_requests (
		session_id TEXT NOT NULL,
		repo_owner TEXT NOT NULL,
		repo_name TEXT NOT NULL,
		pr_number INTEGER NOT NULL,
		linked_at TEXT NOT NULL DEFAULT '',
		UNIQUE(session_id, repo_owner, repo_name, pr_number),
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
	)`); err != nil {
		return fmt.Errorf("migration 026 (session_pull_requests): %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_session_prs_session ON session_pull_requests(session_id)"); err != nil {
		return fmt.Errorf("migration 026 (session_prs session index): %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_session_prs_pr ON session_pull_requests(repo_owner, repo_name, pr_number)"); err != nil {
		return fmt.Errorf("migration 026 (session_prs pr index): %w", err)
	}

	// ── Migration 027: Enrich users table for Slack integration ──
	// Add kind, slack_id, slack_name, role columns to users.
	// Add index on sessions.owner_id for GROUP BY performance.
	userCols027 := []struct {
		name string
		def  string
	}{
		{"kind", "TEXT NOT NULL DEFAULT 'unknown'"},
		{"slack_id", "TEXT NOT NULL DEFAULT ''"},
		{"slack_name", "TEXT NOT NULL DEFAULT ''"},
		{"role", "TEXT NOT NULL DEFAULT 'member'"},
	}
	for _, col := range userCols027 {
		if !columnExists(db, "users", col.name) {
			if _, err := db.Exec(fmt.Sprintf("ALTER TABLE users ADD COLUMN %s %s", col.name, col.def)); err != nil {
				return fmt.Errorf("migration 027 (users.%s): %w", col.name, err)
			}
		}
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_sessions_owner_id ON sessions(owner_id)"); err != nil {
		return fmt.Errorf("migration 027 (idx_sessions_owner_id): %w", err)
	}

	// ── Migration 028: recommendations table ──
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS recommendations (
		id TEXT PRIMARY KEY,
		project_path TEXT NOT NULL,
		type TEXT NOT NULL,
		priority TEXT NOT NULL DEFAULT 'medium',
		source TEXT NOT NULL DEFAULT 'deterministic',
		icon TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL,
		message TEXT NOT NULL,
		impact TEXT NOT NULL DEFAULT '',
		agent TEXT NOT NULL DEFAULT '',
		skill TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'active',
		fingerprint TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		dismissed_at DATETIME,
		snoozed_until DATETIME
	)`); err != nil {
		return fmt.Errorf("migration 028 (recommendations table): %w", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_recommendations_fingerprint ON recommendations(fingerprint)`); err != nil {
		return fmt.Errorf("migration 028 (fingerprint index): %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_recommendations_project_status ON recommendations(project_path, status)`); err != nil {
		return fmt.Errorf("migration 028 (project_status index): %w", err)
	}

	// ── Migration 029: denormalized cost columns on sessions ──
	// estimated_cost = API-equivalent cost (set by service layer via pricing calculator).
	// actual_cost = sum of provider-reported per-message costs (computed at Save time).
	// These columns eliminate the need to load full session payloads for cost aggregation
	// in Stats(), Forecast(), ContextSaturation(), and CacheEfficiency().
	for _, col := range []struct{ name, ddl string }{
		{"estimated_cost", "ALTER TABLE sessions ADD COLUMN estimated_cost REAL NOT NULL DEFAULT 0"},
		{"actual_cost", "ALTER TABLE sessions ADD COLUMN actual_cost REAL NOT NULL DEFAULT 0"},
	} {
		if !columnExists(db, "sessions", col.name) {
			if _, err := db.Exec(col.ddl); err != nil {
				return fmt.Errorf("migration 029 (%s): %w", col.name, err)
			}
		}
	}

	// ── Migration 030: flat project_capabilities table ──
	// One row per capability per project, enabling SQL queries like
	// "which projects have the sentry MCP server?" or "when was skill X first seen?"
	// Complements project_snapshots (JSON blob audit trail) with a queryable index.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS project_capabilities (
		id TEXT PRIMARY KEY,
		project_path TEXT NOT NULL,
		name TEXT NOT NULL,
		kind TEXT NOT NULL,
		scope TEXT NOT NULL DEFAULT 'global',
		is_active INTEGER NOT NULL DEFAULT 1,
		first_seen TEXT NOT NULL,
		last_seen TEXT NOT NULL,
		UNIQUE(project_path, name, kind)
	)`); err != nil {
		return fmt.Errorf("migration 030 (project_capabilities table): %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_project_capabilities_path
		ON project_capabilities(project_path, is_active)`); err != nil {
		return fmt.Errorf("migration 030 (project_capabilities index): %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_project_capabilities_kind
		ON project_capabilities(kind, is_active)`); err != nil {
		return fmt.Errorf("migration 030 (project_capabilities kind index): %w", err)
	}

	// ── Migration 031: session_analytics sidecar table (CQRS read model) ──
	//
	// One row per session, computed once in the same transaction as Save() via
	// the service-layer stampAnalytics() hook. Replaces the per-request recompute
	// inside Forecast(), CacheEfficiency(), ContextSaturation(), AgentROIAnalysis()
	// which used to load full session payloads and walk message arrays on every
	// dashboard hit (12-14s for 1764 sessions, 90-day window, cache disabled).
	//
	// Design decisions (validated 2026-04-05):
	//  - Inline in Save(): single-writer model, ~50ms overhead per write is invisible
	//  - schema_version column: force-rebuild on formula changes via backfill task
	//  - Backfill strategy: AnalyticsBackfillTask 200/run + handler fallback to live
	//    computation for any session not yet in session_analytics (zero-downtime)
	//
	// The columns below cover the four catastrophic hot paths:
	//  * ContextSaturation: peak_input_tokens, dominant_model, peak_saturation_pct,
	//    compaction_*, max_context_window
	//  * CacheEfficiency: cache_read_tokens, cache_write_tokens, input_tokens,
	//    cache_miss_count, cache_wasted_tokens, longest_gap_mins, session_avg_gap_mins
	//  * Forecast: backend, estimated_cost, actual_cost, fork_offset, deduplicated_cost
	//  * AgentROI: rolled up via the sibling session_agent_usage table below
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS session_analytics (
		session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,

		-- ContextSaturation fields
		peak_input_tokens        INTEGER NOT NULL DEFAULT 0,
		dominant_model           TEXT    NOT NULL DEFAULT '',
		max_context_window       INTEGER NOT NULL DEFAULT 0,
		peak_saturation_pct      REAL    NOT NULL DEFAULT 0,
		has_compaction           INTEGER NOT NULL DEFAULT 0,
		compaction_count         INTEGER NOT NULL DEFAULT 0,
		compaction_drop_pct      REAL    NOT NULL DEFAULT 0,
		compaction_wasted_tokens INTEGER NOT NULL DEFAULT 0,

		-- CacheEfficiency fields
		cache_read_tokens        INTEGER NOT NULL DEFAULT 0,
		cache_write_tokens       INTEGER NOT NULL DEFAULT 0,
		input_tokens             INTEGER NOT NULL DEFAULT 0,
		cache_miss_count         INTEGER NOT NULL DEFAULT 0,
		cache_wasted_tokens      INTEGER NOT NULL DEFAULT 0,
		longest_gap_mins         INTEGER NOT NULL DEFAULT 0,
		session_avg_gap_mins     REAL    NOT NULL DEFAULT 0,

		-- Forecast / cost breakdown fields
		backend                  TEXT    NOT NULL DEFAULT '',
		estimated_cost           REAL    NOT NULL DEFAULT 0,
		actual_cost              REAL    NOT NULL DEFAULT 0,
		fork_offset              INTEGER NOT NULL DEFAULT 0,
		deduplicated_cost        REAL    NOT NULL DEFAULT 0,

		-- Per-session agent rollups (see also session_agent_usage for per-agent breakdown)
		total_agent_invocations  INTEGER NOT NULL DEFAULT 0,
		unique_agents_used       INTEGER NOT NULL DEFAULT 0,
		agent_tokens             INTEGER NOT NULL DEFAULT 0,
		agent_cost               REAL    NOT NULL DEFAULT 0,
		total_wasted_tokens      INTEGER NOT NULL DEFAULT 0,

		-- JSON blob columns for complex per-session analyses that would
		-- otherwise require walking messages on every dashboard hit. Each blob
		-- is ~1-2 KB and deserializes in microseconds; the existing domain
		-- Aggregate* helpers operate on these structs and are already fast.
		--
		-- These cover:
		--  * waste_breakdown_json      → session.TokenWasteBreakdown     (ClassifyTokenWaste)
		--  * freshness_json            → session.SessionFreshness        (AnalyzeFreshness)
		--  * overload_json             → session.OverloadAnalysis        (DetectOverload)
		--  * sysprompt_json            → session.SessionPromptData       (SystemPromptEstimate + counters)
		--  * fitness_json              → session.SessionFitnessData      (per-task-type fitness)
		--  * forecast_input_json       → session.SessionForecastInput    (ForecastSaturation input)
		waste_breakdown_json     TEXT    NOT NULL DEFAULT '',
		freshness_json           TEXT    NOT NULL DEFAULT '',
		overload_json            TEXT    NOT NULL DEFAULT '',
		sysprompt_json           TEXT    NOT NULL DEFAULT '',
		fitness_json             TEXT    NOT NULL DEFAULT '',
		forecast_input_json      TEXT    NOT NULL DEFAULT '',

		-- Housekeeping
		schema_version           INTEGER NOT NULL DEFAULT 1,
		computed_at              TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("migration 031 (session_analytics table): %w", err)
	}
	// Index on schema_version to let the backfill task quickly find rows needing
	// rebuild after a formula change.
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_session_analytics_schema_version
		ON session_analytics(schema_version)`); err != nil {
		return fmt.Errorf("migration 031 (session_analytics schema_version index): %w", err)
	}

	// Sibling table for per-agent usage rollups. Normalized because the cardinality
	// of agents-per-session is small (~1-10) but unbounded, so exploding them into
	// columns on session_analytics would be wrong.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS session_agent_usage (
		session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		agent_name  TEXT NOT NULL,
		invocations INTEGER NOT NULL DEFAULT 0,
		tokens      INTEGER NOT NULL DEFAULT 0,
		cost        REAL    NOT NULL DEFAULT 0,
		errors      INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (session_id, agent_name)
	)`); err != nil {
		return fmt.Errorf("migration 031 (session_agent_usage table): %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_session_agent_usage_agent
		ON session_agent_usage(agent_name)`); err != nil {
		return fmt.Errorf("migration 031 (session_agent_usage agent index): %w", err)
	}

	// ── Migration 032: session_hotspots sidecar table ──
	//
	// Pre-computed investigation hot-spots for each session. Populated by the
	// nightly HotspotsTask and read by the session detail "Hot Spots" tab and
	// the investigation API.
	//
	// The payload column stores a compressed (gzip) JSON blob of
	// session.SessionHotspots. schema_version tracks the struct revision so
	// the backfill task can re-process stale rows when the schema evolves.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS session_hotspots (
		session_id     TEXT PRIMARY KEY NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		payload        BLOB NOT NULL,
		schema_version INTEGER NOT NULL DEFAULT 1,
		computed_at    DATETIME NOT NULL DEFAULT (datetime('now')),
		UNIQUE(session_id)
	)`); err != nil {
		return fmt.Errorf("migration 032 (session_hotspots table): %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_session_hotspots_schema_version
		ON session_hotspots(schema_version)`); err != nil {
		return fmt.Errorf("migration 032 (session_hotspots schema_version index): %w", err)
	}

	return nil
}

// backfillToolCounts reads each session's payload JSON and computes
// tool_call_count + error_count from the messages' tool_calls array.
func backfillToolCounts(db *sql.DB) error {
	rows, err := db.Query("SELECT id, payload FROM sessions WHERE payload IS NOT NULL")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	type row struct {
		id            string
		toolCallCount int
		errorCount    int
	}

	var updates []row
	for rows.Next() {
		var id string
		var payload []byte
		if scanErr := rows.Scan(&id, &payload); scanErr != nil {
			continue
		}
		var s session.Session
		if jsonErr := json.Unmarshal(payload, &s); jsonErr != nil {
			continue
		}
		tc, ec := countToolCalls(&s)
		updates = append(updates, row{id: id, toolCallCount: tc, errorCount: ec})
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, u := range updates {
		if _, err := db.Exec("UPDATE sessions SET tool_call_count = ?, error_count = ? WHERE id = ?",
			u.toolCallCount, u.errorCount, u.id); err != nil {
			return err
		}
	}
	return nil
}

// countToolCalls computes total tool calls and error count from a full session.
func countToolCalls(s *session.Session) (toolCallCount, errorCount int) {
	for _, msg := range s.Messages {
		toolCallCount += len(msg.ToolCalls)
		for _, tc := range msg.ToolCalls {
			if tc.State == session.ToolStateError {
				errorCount++
			}
		}
	}
	return
}

// computeActualCost sums the provider-reported costs from all assistant messages.
// This is the "what was actually charged" value; 0 for subscription users.
func computeActualCost(s *session.Session) float64 {
	var total float64
	for i := range s.Messages {
		if s.Messages[i].Role == session.RoleAssistant {
			total += s.Messages[i].ProviderCost
		}
	}
	return total
}

// columnExists checks if a column exists in a table using PRAGMA table_info.
func columnExists(db *sql.DB, table, column string) bool {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if scanErr := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); scanErr != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}
