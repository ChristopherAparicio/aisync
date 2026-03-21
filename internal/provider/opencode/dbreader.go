package opencode

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ChristopherAparicio/aisync/internal/session"

	_ "modernc.org/sqlite" // SQLite driver registration
)

const dbFileName = "opencode.db"

// dbReader reads OpenCode sessions from the SQLite database at
// ~/.local/share/opencode/opencode.db. This is the primary storage
// used by modern OpenCode versions and contains ALL sessions (928+)
// compared to the legacy file-based storage which holds only a subset.
//
// Schema overview:
//   - session: id, project_id, parent_id, directory, title, time_created, time_updated (columns, no JSON)
//   - message: id, session_id, data (JSON with role, tokens, cost, providerID, etc.)
//   - part:    id, message_id, session_id, data (JSON with type, text, tool, state, etc.)
//   - project: id, worktree
type dbReader struct {
	db *sql.DB
}

// newDBReader opens the OpenCode SQLite database in read-only mode.
// Returns nil, err if the database doesn't exist, can't be opened, or
// lacks the required schema (session, message, part, project tables).
func newDBReader(dataHome string) (*dbReader, error) {
	dbPath := filepath.Join(dataHome, dbFileName)

	// Bail early if the file doesn't exist — avoid sqlite creating an empty file.
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("opencode.db not found: %w", err)
	}

	// modernc.org/sqlite uses a different DSN format from mattn/go-sqlite3.
	// Use _pragma for read-only behavior and WAL mode.
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=query_only(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening opencode.db: %w", err)
	}

	// Verify the database has the required tables.
	var tableCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master
		 WHERE type = 'table' AND name IN ('session', 'message', 'part', 'project')`,
	).Scan(&tableCount)
	if err != nil || tableCount < 4 {
		db.Close()
		return nil, fmt.Errorf("opencode.db missing required tables (found %d/4)", tableCount)
	}

	return &dbReader{db: db}, nil
}

func (r *dbReader) close() error {
	if r.db != nil {
		return r.db.Close()
	}
	return nil
}

func (r *dbReader) findProjectID(worktreePath string) (string, error) {
	var id string
	err := r.db.QueryRow(
		`SELECT id FROM project WHERE worktree = ?`,
		worktreePath,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return "", session.ErrSessionNotFound
	}
	if err != nil {
		return "", fmt.Errorf("querying project by worktree: %w", err)
	}
	return id, nil
}

func (r *dbReader) listSessions(projectID string) ([]ocSession, error) {
	rows, err := r.db.Query(
		`SELECT id, project_id, COALESCE(parent_id, ''), directory, title,
		        time_created, time_updated
		 FROM session
		 WHERE project_id = ?
		 ORDER BY time_created DESC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying sessions: %w", err)
	}
	defer rows.Close()

	var sessions []ocSession
	for rows.Next() {
		var s ocSession
		if err := rows.Scan(
			&s.ID, &s.ProjectID, &s.ParentID, &s.Directory, &s.Title,
			&s.Time.Created, &s.Time.Updated,
		); err != nil {
			continue
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func (r *dbReader) readSession(sessionID string) (*ocSession, error) {
	var s ocSession
	err := r.db.QueryRow(
		`SELECT id, project_id, COALESCE(parent_id, ''), directory, title,
		        time_created, time_updated
		 FROM session
		 WHERE id = ?`,
		sessionID,
	).Scan(
		&s.ID, &s.ProjectID, &s.ParentID, &s.Directory, &s.Title,
		&s.Time.Created, &s.Time.Updated,
	)
	if err == sql.ErrNoRows {
		return nil, session.ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying session %s: %w", sessionID, err)
	}
	return &s, nil
}

func (r *dbReader) loadMessages(sessionID string) ([]ocMessage, error) {
	rows, err := r.db.Query(
		`SELECT id, data FROM message
		 WHERE session_id = ?
		 ORDER BY time_created ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying messages: %w", err)
	}
	defer rows.Close()

	var messages []ocMessage
	for rows.Next() {
		var (
			id      string
			dataStr string
		)
		if err := rows.Scan(&id, &dataStr); err != nil {
			continue
		}

		var msg ocMessage
		if err := json.Unmarshal([]byte(dataStr), &msg); err != nil {
			continue
		}
		// The id is a table column, not in the JSON data.
		msg.ID = id
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}

func (r *dbReader) loadParts(messageID string) ([]ocPart, error) {
	rows, err := r.db.Query(
		`SELECT id, message_id, session_id, data FROM part
		 WHERE message_id = ?
		 ORDER BY time_created ASC`,
		messageID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying parts: %w", err)
	}
	defer rows.Close()

	var parts []ocPart
	for rows.Next() {
		var (
			id, msgID, sessID string
			dataStr           string
		)
		if err := rows.Scan(&id, &msgID, &sessID, &dataStr); err != nil {
			continue
		}

		var part ocPart
		if err := json.Unmarshal([]byte(dataStr), &part); err != nil {
			continue
		}
		// Table columns take precedence — they're authoritative.
		part.ID = id
		part.MessageID = msgID
		part.SessionID = sessID
		parts = append(parts, part)
	}
	return parts, rows.Err()
}

func (r *dbReader) countMessages(sessionID string) int {
	var count int
	err := r.db.QueryRow(
		`SELECT COUNT(*) FROM message WHERE session_id = ?`,
		sessionID,
	).Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

func (r *dbReader) findChildSessions(parentID string) ([]ocSession, error) {
	rows, err := r.db.Query(
		`SELECT id, project_id, COALESCE(parent_id, ''), directory, title,
		        time_created, time_updated
		 FROM session
		 WHERE parent_id = ?
		 ORDER BY time_created ASC`,
		parentID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying child sessions: %w", err)
	}
	defer rows.Close()

	var children []ocSession
	for rows.Next() {
		var s ocSession
		if err := rows.Scan(
			&s.ID, &s.ProjectID, &s.ParentID, &s.Directory, &s.Title,
			&s.Time.Created, &s.Time.Updated,
		); err != nil {
			continue
		}
		children = append(children, s)
	}
	return children, rows.Err()
}

func (r *dbReader) sessionUpdatedAt(sessionID string) int64 {
	var updated int64
	err := r.db.QueryRow(
		`SELECT time_updated FROM session WHERE id = ?`,
		sessionID,
	).Scan(&updated)
	if err != nil {
		return 0
	}
	return updated
}
