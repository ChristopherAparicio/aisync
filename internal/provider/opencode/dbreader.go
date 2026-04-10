package opencode

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

func (r *dbReader) loadAllPartsForSession(sessionID string) (map[string][]ocPart, error) {
	rows, err := r.db.Query(
		`SELECT id, message_id, session_id, data FROM part
		 WHERE session_id = ?
		 ORDER BY message_id, time_created ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying all parts: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]ocPart)
	for rows.Next() {
		var id, msgID, sessID, dataStr string
		if err := rows.Scan(&id, &msgID, &sessID, &dataStr); err != nil {
			continue
		}
		var part ocPart
		if err := json.Unmarshal([]byte(dataStr), &part); err != nil {
			continue
		}
		part.ID = id
		part.SessionID = sessID
		part.MessageID = msgID
		result[msgID] = append(result[msgID], part)
	}
	return result, rows.Err()
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

// loadMessagesFrom returns messages starting at the given offset (0-based).
// This enables incremental captures by reading only new messages.
func (r *dbReader) loadMessagesFrom(sessionID string, offset int) ([]ocMessage, error) {
	rows, err := r.db.Query(
		`SELECT id, data FROM message
		 WHERE session_id = ?
		 ORDER BY time_created ASC
		 LIMIT -1 OFFSET ?`,
		sessionID, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("querying messages from offset %d: %w", offset, err)
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
		msg.ID = id
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}

// loadPartsForMessages loads parts for a specific set of message IDs.
// Used by incremental export to avoid loading parts for already-captured messages.
func (r *dbReader) loadPartsForMessages(messageIDs []string) (map[string][]ocPart, error) {
	if len(messageIDs) == 0 {
		return make(map[string][]ocPart), nil
	}

	// Build IN clause with placeholders.
	placeholders := make([]string, len(messageIDs))
	args := make([]interface{}, len(messageIDs))
	for i, id := range messageIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(
		`SELECT id, message_id, session_id, data FROM part
		 WHERE message_id IN (%s)
		 ORDER BY message_id, time_created ASC`,
		strings.Join(placeholders, ","),
	)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying parts for messages: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]ocPart)
	for rows.Next() {
		var id, msgID, sessID, dataStr string
		if err := rows.Scan(&id, &msgID, &sessID, &dataStr); err != nil {
			continue
		}
		var part ocPart
		if err := json.Unmarshal([]byte(dataStr), &part); err != nil {
			continue
		}
		part.ID = id
		part.SessionID = sessID
		part.MessageID = msgID
		result[msgID] = append(result[msgID], part)
	}
	return result, rows.Err()
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

func (r *dbReader) listAllProjects() ([]ocProjectInfo, error) {
	rows, err := r.db.Query(
		`SELECT p.id, p.worktree, COUNT(s.id) as session_count
		 FROM project p
		 LEFT JOIN session s ON s.project_id = p.id AND (s.parent_id IS NULL OR s.parent_id = '')
		 GROUP BY p.id, p.worktree
		 ORDER BY session_count DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying all projects: %w", err)
	}
	defer rows.Close()

	var projects []ocProjectInfo
	for rows.Next() {
		var p ocProjectInfo
		if err := rows.Scan(&p.ID, &p.Worktree, &p.SessionCount); err != nil {
			continue
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}
