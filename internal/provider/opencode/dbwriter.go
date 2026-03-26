package opencode

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/google/uuid"

	_ "modernc.org/sqlite" // SQLite driver registration
)

// dbWriter writes sessions directly into OpenCode's SQLite database.
// This is the counterpart to dbReader — while the reader opens the DB
// in read-only mode, the writer opens it for read-write.
type dbWriter struct {
	db *sql.DB
}

// newDBWriter opens the OpenCode SQLite database for writing.
// Returns nil, err if the database doesn't exist or can't be opened.
func newDBWriter(dataHome string) (*dbWriter, error) {
	dbPath := filepath.Join(dataHome, dbFileName)

	// The DB must already exist — we don't create it.
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("opencode.db not found: %w", err)
	}

	// Open in read-write mode with WAL (no query_only this time).
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening opencode.db for write: %w", err)
	}

	// Verify required tables exist.
	var tableCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master
		 WHERE type = 'table' AND name IN ('session', 'message', 'part', 'project')`,
	).Scan(&tableCount)
	if err != nil || tableCount < 4 {
		db.Close()
		return nil, fmt.Errorf("opencode.db missing required tables (found %d/4)", tableCount)
	}

	return &dbWriter{db: db}, nil
}

func (w *dbWriter) close() error {
	if w.db != nil {
		return w.db.Close()
	}
	return nil
}

// importSession writes a full session (session + messages + parts) into the DB
// using a single transaction for consistency.
func (w *dbWriter) importSession(sess *session.Session, agent string) error {
	projectPath := sess.ProjectPath
	if projectPath == "" {
		return fmt.Errorf("session has no project path")
	}

	// Generate session ID if missing.
	sessionID := string(sess.ID)
	if sessionID == "" {
		sessionID = "ses_" + uuid.New().String()[:8]
		sess.ID = session.ID(sessionID)
	}

	tx, err := w.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Step 1: Ensure project exists.
	projectID, err := w.ensureProject(tx, projectPath)
	if err != nil {
		return fmt.Errorf("ensuring project: %w", err)
	}

	// Step 2: Insert session.
	if err := w.insertSession(tx, sess, projectID); err != nil {
		return fmt.Errorf("inserting session: %w", err)
	}

	// Step 3: Insert messages + parts.
	if err := w.insertMessages(tx, sess, agent); err != nil {
		return fmt.Errorf("inserting messages: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	return nil
}

// ensureProject finds or creates the project entry in the DB.
func (w *dbWriter) ensureProject(tx *sql.Tx, worktreePath string) (string, error) {
	// Check if project already exists.
	var id string
	err := tx.QueryRow(`SELECT id FROM project WHERE worktree = ?`, worktreePath).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("querying project: %w", err)
	}

	// Create new project.
	id = uuid.New().String()[:12]
	now := time.Now().UnixMilli()
	_, err = tx.Exec(
		`INSERT INTO project (id, worktree, time_created, time_updated, sandboxes) VALUES (?, ?, ?, ?, '[]')`,
		id, worktreePath, now, now,
	)
	if err != nil {
		return "", fmt.Errorf("inserting project: %w", err)
	}
	return id, nil
}

// insertSession writes the session row.
func (w *dbWriter) insertSession(tx *sql.Tx, sess *session.Session, projectID string) error {
	created := sess.CreatedAt.UnixMilli()
	if sess.CreatedAt.IsZero() {
		created = time.Now().UnixMilli()
	}
	updated := time.Now().UnixMilli()

	title := sess.Summary
	if title == "" {
		title = "Restored from aisync"
	}

	// Use INSERT OR REPLACE so re-running is safe.
	_, err := tx.Exec(
		`INSERT OR REPLACE INTO session
		 (id, project_id, parent_id, slug, directory, title, version, time_created, time_updated)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(sess.ID), projectID, string(sess.ParentID),
		"aisync-restored", sess.ProjectPath, title, "1.0.0",
		created, updated,
	)
	return err
}

// insertMessages writes all messages and their parts.
func (w *dbWriter) insertMessages(tx *sql.Tx, sess *session.Session, agent string) error {
	sessionID := string(sess.ID)

	for _, msg := range sess.Messages {
		msgID := msg.ID
		if msgID == "" {
			msgID = "msg_" + uuid.New().String()[:16]
		}

		// If this message ID already exists under a different session
		// (e.g. this is a rewind/fork sharing the original IDs),
		// generate a fresh ID to avoid conflicts.
		var existingSession string
		err := tx.QueryRow(`SELECT session_id FROM message WHERE id = ?`, msgID).Scan(&existingSession)
		if err == nil && existingSession != sessionID {
			msgID = "msg_" + uuid.New().String()[:16]
		}

		created := msg.Timestamp.UnixMilli()
		if msg.Timestamp.IsZero() {
			created = time.Now().UnixMilli()
		}

		agentName := agent
		if agentName == "" {
			agentName = sess.Agent
		}
		if agentName == "" {
			agentName = "coder"
		}

		// Build the data JSON blob (everything except id).
		ocMsg := ocMessage{
			Role:    string(msg.Role),
			Agent:   agentName,
			ModelID: msg.Model,
			Model: ocModel{
				ModelID: msg.Model,
			},
			Tokens: ocTokens{
				Input:  msg.InputTokens,
				Output: msg.OutputTokens,
			},
			Time: ocMsgTime{
				Created:   created,
				Completed: created,
			},
		}

		dataBytes, err := json.Marshal(ocMsg)
		if err != nil {
			return fmt.Errorf("marshalling message data: %w", err)
		}

		_, err = tx.Exec(
			`INSERT OR IGNORE INTO message (id, session_id, time_created, time_updated, data)
			 VALUES (?, ?, ?, ?, ?)`,
			msgID, sessionID, created, created, string(dataBytes),
		)
		if err != nil {
			return fmt.Errorf("inserting message %s: %w", msgID, err)
		}

		// Insert parts for this message.
		if err := w.insertParts(tx, msgID, sessionID, msg); err != nil {
			return fmt.Errorf("inserting parts for %s: %w", msgID, err)
		}
	}
	return nil
}

// insertParts writes part rows for a message's content and tool calls.
func (w *dbWriter) insertParts(tx *sql.Tx, msgID, sessionID string, msg session.Message) error {
	partIndex := 0

	// Text part.
	if msg.Content != "" {
		if err := w.insertPart(tx, msgID, sessionID, partIndex, ocPart{
			Type: "text",
			Text: msg.Content,
		}); err != nil {
			return err
		}
		partIndex++
	}

	// Thinking/reasoning part.
	if msg.Thinking != "" {
		if err := w.insertPart(tx, msgID, sessionID, partIndex, ocPart{
			Type: "reasoning",
			Text: msg.Thinking,
		}); err != nil {
			return err
		}
		partIndex++
	}

	// Tool call parts.
	for _, tc := range msg.ToolCalls {
		var input interface{}
		if tc.Input != "" && json.Valid([]byte(tc.Input)) {
			_ = json.Unmarshal([]byte(tc.Input), &input)
		}

		status := string(tc.State)
		if status == "" {
			status = "completed"
		}

		if err := w.insertPart(tx, msgID, sessionID, partIndex, ocPart{
			CallID: tc.ID,
			Tool:   tc.Name,
			Type:   "tool",
			State: ocToolState{
				Input:  input,
				Output: tc.Output,
				Status: status,
				Time: ocPartTime{
					Start: msg.Timestamp.UnixMilli(),
					End:   msg.Timestamp.UnixMilli() + int64(tc.DurationMs),
				},
			},
		}); err != nil {
			return err
		}
		partIndex++
	}

	return nil
}

// insertPart writes a single part row.
func (w *dbWriter) insertPart(tx *sql.Tx, msgID, sessionID string, index int, part ocPart) error {
	partID := fmt.Sprintf("prt_%s_%03d", msgID, index)
	now := time.Now().UnixMilli()

	// Build the data JSON — strip id/messageID/sessionID since they're columns.
	dataObj := struct {
		Type      string      `json:"type"`
		Text      string      `json:"text,omitempty"`
		CallID    string      `json:"callID,omitempty"`
		Tool      string      `json:"tool,omitempty"`
		State     ocToolState `json:"state,omitempty"`
		MediaType string      `json:"mediaType,omitempty"`
		FileName  string      `json:"filename,omitempty"`
	}{
		Type:   part.Type,
		Text:   part.Text,
		CallID: part.CallID,
		Tool:   part.Tool,
		State:  part.State,
	}

	dataBytes, err := json.Marshal(dataObj)
	if err != nil {
		return fmt.Errorf("marshalling part data: %w", err)
	}

	_, err = tx.Exec(
		`INSERT OR IGNORE INTO part (id, message_id, session_id, time_created, time_updated, data)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		partID, msgID, sessionID, now, now, string(dataBytes),
	)
	return err
}
