// Package sqlite implements domain.Store using a local SQLite database.
package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/ChristopherAparicio/aisync/internal/domain"

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

// Store implements domain.Store with SQLite.
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

	return &Store{db: db}, nil
}

// Close releases the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// Save stores a session. If a session with the same ID exists, it is replaced.
func (s *Store) Save(session *domain.Session) error {
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
		INSERT INTO sessions (id, provider, agent, branch, commit_sha, project_path, parent_id, storage_mode, summary, message_count, total_tokens, payload, created_at, exported_at, exported_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			provider=excluded.provider, agent=excluded.agent, branch=excluded.branch,
			commit_sha=excluded.commit_sha, project_path=excluded.project_path,
			parent_id=excluded.parent_id, storage_mode=excluded.storage_mode,
			summary=excluded.summary, message_count=excluded.message_count,
			total_tokens=excluded.total_tokens, payload=excluded.payload,
			created_at=excluded.created_at, exported_at=excluded.exported_at,
			exported_by=excluded.exported_by`,
		session.ID, session.Provider, session.Agent, session.Branch,
		session.CommitSHA, session.ProjectPath, session.ParentID,
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
func (s *Store) Get(id domain.SessionID) (*domain.Session, error) {
	var payload []byte
	err := s.db.QueryRow("SELECT payload FROM sessions WHERE id = ?", id).Scan(&payload)
	if err == sql.ErrNoRows {
		return nil, domain.ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying session: %w", err)
	}

	var session domain.Session
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

// GetByBranch retrieves the most recent session for a project and branch.
func (s *Store) GetByBranch(projectPath string, branch string) (*domain.Session, error) {
	var id string
	err := s.db.QueryRow(
		"SELECT id FROM sessions WHERE project_path = ? AND branch = ? ORDER BY created_at DESC LIMIT 1",
		projectPath, branch,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, domain.ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying by branch: %w", err)
	}

	return s.Get(domain.SessionID(id))
}

// List returns session summaries matching the given options.
func (s *Store) List(opts domain.ListOptions) ([]domain.SessionSummary, error) {
	query := "SELECT id, provider, agent, branch, summary, message_count, total_tokens, created_at FROM sessions WHERE 1=1"
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

	var summaries []domain.SessionSummary
	for rows.Next() {
		var ss domain.SessionSummary
		var createdAt string
		if err := rows.Scan(&ss.ID, &ss.Provider, &ss.Agent, &ss.Branch, &ss.Summary, &ss.MessageCount, &ss.TotalTokens, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning session row: %w", err)
		}
		summaries = append(summaries, ss)
	}

	return summaries, rows.Err()
}

// Delete removes a session by its ID.
func (s *Store) Delete(id domain.SessionID) error {
	result, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return domain.ErrSessionNotFound
	}

	return nil
}

// AddLink associates a session with a git object.
func (s *Store) AddLink(sessionID domain.SessionID, link domain.Link) error {
	// Verify session exists
	var exists int
	err := s.db.QueryRow("SELECT 1 FROM sessions WHERE id = ?", sessionID).Scan(&exists)
	if err == sql.ErrNoRows {
		return domain.ErrSessionNotFound
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
func (s *Store) GetByLink(linkType domain.LinkType, ref string) ([]domain.SessionSummary, error) {
	rows, err := s.db.Query(`
		SELECT s.id, s.provider, s.agent, s.branch, s.summary, s.message_count, s.total_tokens, s.created_at
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

	var summaries []domain.SessionSummary
	for rows.Next() {
		var ss domain.SessionSummary
		var createdAt string
		if scanErr := rows.Scan(&ss.ID, &ss.Provider, &ss.Agent, &ss.Branch, &ss.Summary, &ss.MessageCount, &ss.TotalTokens, &createdAt); scanErr != nil {
			return nil, fmt.Errorf("scanning session row: %w", scanErr)
		}
		summaries = append(summaries, ss)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(summaries) == 0 {
		return nil, domain.ErrSessionNotFound
	}

	return summaries, nil
}

func (s *Store) loadLinks(sessionID domain.SessionID) ([]domain.Link, error) {
	rows, err := s.db.Query("SELECT link_type, link_ref FROM session_links WHERE session_id = ?", sessionID)
	if err != nil {
		return nil, fmt.Errorf("loading links: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var links []domain.Link
	for rows.Next() {
		var l domain.Link
		if err := rows.Scan(&l.LinkType, &l.Ref); err != nil {
			return nil, fmt.Errorf("scanning link: %w", err)
		}
		links = append(links, l)
	}

	return links, rows.Err()
}

func (s *Store) loadFileChanges(sessionID domain.SessionID) ([]domain.FileChange, error) {
	rows, err := s.db.Query("SELECT file_path, change_type FROM file_changes WHERE session_id = ?", sessionID)
	if err != nil {
		return nil, fmt.Errorf("loading file changes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var changes []domain.FileChange
	for rows.Next() {
		var fc domain.FileChange
		if err := rows.Scan(&fc.FilePath, &fc.ChangeType); err != nil {
			return nil, fmt.Errorf("scanning file change: %w", err)
		}
		changes = append(changes, fc)
	}

	return changes, rows.Err()
}
