package hermes

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // SQLite driver registration
)

const dbFileName = "state.db"

// dbReader reads Hermes sessions from the SQLite database at
// ~/.hermes/state.db (or $HERMES_HOME/state.db). The connection is opened
// read-only via query_only pragma — no writes are ever issued.
type dbReader struct {
	db *sql.DB
}

// newDBReader opens the Hermes SQLite database in read-only WAL mode.
// hermesHome should be the directory containing state.db (typically ~/.hermes).
// Returns an error if the file is missing or the required tables are absent.
func newDBReader(hermesHome string) (*dbReader, error) {
	dbPath := filepath.Join(hermesHome, dbFileName)

	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("hermes state.db not found at %s: %w", dbPath, err)
	}

	// Same DSN pattern as OpenCode: WAL mode + query_only to enforce read-only.
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=query_only(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening hermes state.db: %w", err)
	}

	// Verify required tables exist; the `compression_locks` table may also be
	// present but is never read here.
	var tableCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master
		 WHERE type = 'table' AND name IN ('sessions', 'messages')`,
	).Scan(&tableCount)
	if err != nil || tableCount < 2 {
		db.Close()
		return nil, fmt.Errorf("hermes state.db missing required tables (found %d/2)", tableCount)
	}

	return &dbReader{db: db}, nil
}

func (r *dbReader) close() error {
	if r.db != nil {
		return r.db.Close()
	}
	return nil
}

func (r *dbReader) listSessions() ([]hermesSession, error) {
	rows, err := r.db.Query(
		`SELECT id, source, user_id, model, parent_session_id,
		        started_at, ended_at, end_reason,
		        message_count, tool_call_count,
		        input_tokens, output_tokens, cache_read_tokens,
		        cache_write_tokens, reasoning_tokens,
		        billing_provider, billing_base_url, billing_mode,
		        estimated_cost_usd, actual_cost_usd, cost_status,
		        title
		 FROM sessions
		 WHERE end_reason IS NULL OR end_reason != 'deleted'
		 ORDER BY started_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("hermes: querying sessions: %w", err)
	}
	defer rows.Close()

	var sessions []hermesSession
	for rows.Next() {
		var s hermesSession
		if err := rows.Scan(
			&s.ID, &s.Source, &s.UserID, &s.Model, &s.ParentSessionID,
			&s.StartedAt, &s.EndedAt, &s.EndReason,
			&s.MessageCount, &s.ToolCallCount,
			&s.InputTokens, &s.OutputTokens, &s.CacheReadTokens,
			&s.CacheWriteTokens, &s.ReasoningTokens,
			&s.BillingProvider, &s.BillingBaseURL, &s.BillingMode,
			&s.EstimatedCostUSD, &s.ActualCostUSD, &s.CostStatus,
			&s.Title,
		); err != nil {
			log.Printf("hermes: dbreader: scan session row: %v", err)
			continue
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func (r *dbReader) readSession(id string) (*hermesSession, error) {
	var s hermesSession
	err := r.db.QueryRow(
		`SELECT id, source, user_id, model, parent_session_id,
		        started_at, ended_at, end_reason,
		        message_count, tool_call_count,
		        input_tokens, output_tokens, cache_read_tokens,
		        cache_write_tokens, reasoning_tokens,
		        billing_provider, billing_base_url, billing_mode,
		        estimated_cost_usd, actual_cost_usd, cost_status,
		        title
		 FROM sessions
		 WHERE id = ?`,
		id,
	).Scan(
		&s.ID, &s.Source, &s.UserID, &s.Model, &s.ParentSessionID,
		&s.StartedAt, &s.EndedAt, &s.EndReason,
		&s.MessageCount, &s.ToolCallCount,
		&s.InputTokens, &s.OutputTokens, &s.CacheReadTokens,
		&s.CacheWriteTokens, &s.ReasoningTokens,
		&s.BillingProvider, &s.BillingBaseURL, &s.BillingMode,
		&s.EstimatedCostUSD, &s.ActualCostUSD, &s.CostStatus,
		&s.Title,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("hermes: session not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("hermes: querying session %s: %w", id, err)
	}
	return &s, nil
}

func (r *dbReader) listMessages(sessionID string) ([]hermesMessage, error) {
	rows, err := r.db.Query(
		`SELECT id, session_id, role, content, tool_call_id,
		        tool_calls, tool_name, timestamp, token_count,
		        finish_reason, reasoning, reasoning_content
		 FROM messages
		 WHERE session_id = ?
		 ORDER BY rowid ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("hermes: querying messages for session %s: %w", sessionID, err)
	}
	defer rows.Close()

	var messages []hermesMessage
	for rows.Next() {
		var m hermesMessage
		if err := rows.Scan(
			&m.ID, &m.SessionID, &m.Role, &m.Content, &m.ToolCallID,
			&m.ToolCalls, &m.ToolName, &m.Timestamp, &m.TokenCount,
			&m.FinishReason, &m.Reasoning, &m.ReasoningContent,
		); err != nil {
			log.Printf("hermes: dbreader: scan message row: %v", err)
			continue
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

func (r *dbReader) findChildSessions(parentID string) ([]hermesSession, error) {
	rows, err := r.db.Query(
		`SELECT id, source, user_id, model, parent_session_id,
		        started_at, ended_at, end_reason,
		        message_count, tool_call_count,
		        input_tokens, output_tokens, cache_read_tokens,
		        cache_write_tokens, reasoning_tokens,
		        billing_provider, billing_base_url, billing_mode,
		        estimated_cost_usd, actual_cost_usd, cost_status,
		        title
		 FROM sessions
		 WHERE parent_session_id = ?
		 ORDER BY started_at ASC`,
		parentID,
	)
	if err != nil {
		return nil, fmt.Errorf("hermes: querying child sessions for %s: %w", parentID, err)
	}
	defer rows.Close()

	var sessions []hermesSession
	for rows.Next() {
		var s hermesSession
		if err := rows.Scan(
			&s.ID, &s.Source, &s.UserID, &s.Model, &s.ParentSessionID,
			&s.StartedAt, &s.EndedAt, &s.EndReason,
			&s.MessageCount, &s.ToolCallCount,
			&s.InputTokens, &s.OutputTokens, &s.CacheReadTokens,
			&s.CacheWriteTokens, &s.ReasoningTokens,
			&s.BillingProvider, &s.BillingBaseURL, &s.BillingMode,
			&s.EstimatedCostUSD, &s.ActualCostUSD, &s.CostStatus,
			&s.Title,
		); err != nil {
			log.Printf("hermes: dbreader: scan child session row: %v", err)
			continue
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}
