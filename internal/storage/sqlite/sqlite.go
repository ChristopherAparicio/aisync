// Package sqlite implements session.Store using a local SQLite database.
package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"

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

// Store implements session.Store with SQLite.
type Store struct {
	db *sql.DB
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

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Upsert session
	_, err = tx.Exec(`
		INSERT INTO sessions (id, provider, agent, branch, commit_sha, project_path, parent_id, owner_id, storage_mode, summary, message_count, total_tokens, payload, created_at, exported_at, exported_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			provider=excluded.provider, agent=excluded.agent, branch=excluded.branch,
			commit_sha=excluded.commit_sha, project_path=excluded.project_path,
			parent_id=excluded.parent_id, owner_id=excluded.owner_id,
			storage_mode=excluded.storage_mode,
			summary=excluded.summary, message_count=excluded.message_count,
			total_tokens=excluded.total_tokens, payload=excluded.payload,
			created_at=excluded.created_at, exported_at=excluded.exported_at,
			exported_by=excluded.exported_by`,
		session.ID, session.Provider, session.Agent, session.Branch,
		session.CommitSHA, session.ProjectPath, session.ParentID, session.OwnerID,
		session.StorageMode, session.Summary, len(session.Messages),
		session.TokenUsage.TotalTokens, payload,
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
		if _, err := tx.Exec("INSERT INTO file_changes (session_id, file_path, change_type) VALUES (?, ?, ?)",
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

	return tx.Commit()
}

// Get retrieves a session by its ID.
func (s *Store) Get(id session.ID) (*session.Session, error) {
	var payload []byte
	err := s.db.QueryRow("SELECT payload FROM sessions WHERE id = ?", id).Scan(&payload)
	if err == sql.ErrNoRows {
		return nil, session.ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying session: %w", err)
	}

	var session session.Session
	if unmarshalErr := json.Unmarshal(payload, &session); unmarshalErr != nil {
		return nil, fmt.Errorf("unmarshaling session: %w", unmarshalErr)
	}

	// Load links from DB (they may have been added after save)
	links, err := s.loadLinks(id)
	if err != nil {
		return nil, err
	}
	session.Links = links

	// Load file changes from DB
	fileChanges, err := s.loadFileChanges(id)
	if err != nil {
		return nil, err
	}
	session.FileChanges = fileChanges

	return &session, nil
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
	query := "SELECT id, provider, agent, branch, summary, message_count, total_tokens, created_at, COALESCE(owner_id, '') FROM sessions WHERE 1=1"
	args := []interface{}{}

	if opts.ProjectPath != "" {
		query += " AND project_path = ?"
		args = append(args, opts.ProjectPath)
	}
	if !opts.All && opts.Branch != "" {
		query += " AND branch = ?"
		args = append(args, opts.Branch)
	}
	if opts.Provider != "" {
		query += " AND provider = ?"
		args = append(args, opts.Provider)
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
		if err := rows.Scan(&ss.ID, &ss.Provider, &ss.Agent, &ss.Branch, &ss.Summary, &ss.MessageCount, &ss.TotalTokens, &createdAt, &ss.OwnerID); err != nil {
			return nil, fmt.Errorf("scanning session row: %w", err)
		}
		summaries = append(summaries, ss)
	}

	return summaries, rows.Err()
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

	return nil
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
		SELECT s.id, s.provider, s.agent, s.branch, s.summary, s.message_count, s.total_tokens, s.created_at, COALESCE(s.owner_id, '')
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
		if scanErr := rows.Scan(&ss.ID, &ss.Provider, &ss.Agent, &ss.Branch, &ss.Summary, &ss.MessageCount, &ss.TotalTokens, &createdAt, &ss.OwnerID); scanErr != nil {
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
	selectCols := "SELECT id, provider, agent, branch, summary, message_count, total_tokens, created_at, COALESCE(owner_id, '')"
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
		var createdAt string
		if err := rows.Scan(&ss.ID, &ss.Provider, &ss.Agent, &ss.Branch, &ss.Summary, &ss.MessageCount, &ss.TotalTokens, &createdAt, &ss.OwnerID); err != nil {
			return nil, fmt.Errorf("scanning search result: %w", err)
		}
		ss.CreatedAt, _ = time.Parse("2006-01-02T15:04:05Z", createdAt)
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
	if !q.Since.IsZero() {
		conditions = append(conditions, "created_at >= ?")
		args = append(args, q.Since.Format("2006-01-02T15:04:05Z"))
	}
	if !q.Until.IsZero() {
		conditions = append(conditions, "created_at <= ?")
		args = append(args, q.Until.Format("2006-01-02T15:04:05Z"))
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

// ── User methods ──

// SaveUser creates or updates a user. If a user with the same email exists,
// the name and source are updated and the existing user is returned.
func (s *Store) SaveUser(user *session.User) error {
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now().UTC()
	}

	_, err := s.db.Exec(`
		INSERT INTO users (id, name, email, source, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(email) DO UPDATE SET
			name=excluded.name, source=excluded.source`,
		user.ID, user.Name, user.Email, user.Source,
		user.CreatedAt.Format("2006-01-02T15:04:05Z"),
	)
	if err != nil {
		return fmt.Errorf("saving user: %w", err)
	}

	return nil
}

// GetUser retrieves a user by ID.
func (s *Store) GetUser(id session.ID) (*session.User, error) {
	var u session.User
	var createdAt string
	err := s.db.QueryRow("SELECT id, name, email, source, created_at FROM users WHERE id = ?", id).
		Scan(&u.ID, &u.Name, &u.Email, &u.Source, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying user: %w", err)
	}
	u.CreatedAt, _ = time.Parse("2006-01-02T15:04:05Z", createdAt)
	return &u, nil
}

// GetUserByEmail retrieves a user by email address.
// Returns nil, nil if no user matches.
func (s *Store) GetUserByEmail(email string) (*session.User, error) {
	var u session.User
	var createdAt string
	err := s.db.QueryRow("SELECT id, name, email, source, created_at FROM users WHERE email = ?", email).
		Scan(&u.ID, &u.Name, &u.Email, &u.Source, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying user by email: %w", err)
	}
	u.CreatedAt, _ = time.Parse("2006-01-02T15:04:05Z", createdAt)
	return &u, nil
}

// ── Migrations ──

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

	return nil
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
