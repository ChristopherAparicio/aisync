package sqlite

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// SaveErrors persists a batch of session errors (upsert by error ID).
func (s *Store) SaveErrors(errors []session.SessionError) error {
	if len(errors) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO session_errors
		(id, session_id, category, source, message, raw_error, tool_name, tool_call_id,
		 message_id, message_index, http_status, provider_name, request_id, headers,
		 occurred_at, duration_ms, is_retryable, confidence)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, e := range errors {
		headersJSON := "{}"
		if len(e.Headers) > 0 {
			if b, err := json.Marshal(e.Headers); err == nil {
				headersJSON = string(b)
			}
		}

		retryable := 0
		if e.IsRetryable {
			retryable = 1
		}

		occurredAt := ""
		if !e.OccurredAt.IsZero() {
			occurredAt = e.OccurredAt.Format(time.RFC3339Nano)
		}

		if _, err := stmt.Exec(
			e.ID,
			string(e.SessionID),
			string(e.Category),
			string(e.Source),
			e.Message,
			e.RawError,
			e.ToolName,
			e.ToolCallID,
			e.MessageID,
			e.MessageIndex,
			e.HTTPStatus,
			e.ProviderName,
			e.RequestID,
			headersJSON,
			occurredAt,
			e.DurationMs,
			retryable,
			e.Confidence,
		); err != nil {
			return fmt.Errorf("insert error %s: %w", e.ID, err)
		}
	}

	return tx.Commit()
}

// GetErrors retrieves all errors for a session, ordered by occurred_at ASC.
func (s *Store) GetErrors(sessionID session.ID) ([]session.SessionError, error) {
	rows, err := s.db.Query(
		`SELECT id, session_id, category, source, message, raw_error, tool_name, tool_call_id,
		        message_id, message_index, http_status, provider_name, request_id, headers,
		        occurred_at, duration_ms, is_retryable, confidence
		 FROM session_errors
		 WHERE session_id = ?
		 ORDER BY occurred_at ASC`,
		string(sessionID),
	)
	if err != nil {
		return nil, fmt.Errorf("querying errors: %w", err)
	}
	defer rows.Close()

	return scanErrors(rows)
}

// GetErrorSummary computes aggregated error statistics for a session.
func (s *Store) GetErrorSummary(sessionID session.ID) (*session.SessionErrorSummary, error) {
	errors, err := s.GetErrors(sessionID)
	if err != nil {
		return nil, err
	}
	summary := session.NewSessionErrorSummary(sessionID, errors)
	return &summary, nil
}

// ListRecentErrors returns recent errors across all sessions, ordered by occurred_at DESC.
func (s *Store) ListRecentErrors(limit int, category session.ErrorCategory) ([]session.SessionError, error) {
	if limit <= 0 {
		limit = 50
	}

	var query string
	var args []interface{}

	if category != "" && category.Valid() {
		query = `SELECT id, session_id, category, source, message, raw_error, tool_name, tool_call_id,
		                message_id, message_index, http_status, provider_name, request_id, headers,
		                occurred_at, duration_ms, is_retryable, confidence
		         FROM session_errors
		         WHERE category = ?
		         ORDER BY occurred_at DESC
		         LIMIT ?`
		args = []interface{}{string(category), limit}
	} else {
		query = `SELECT id, session_id, category, source, message, raw_error, tool_name, tool_call_id,
		                message_id, message_index, http_status, provider_name, request_id, headers,
		                occurred_at, duration_ms, is_retryable, confidence
		         FROM session_errors
		         ORDER BY occurred_at DESC
		         LIMIT ?`
		args = []interface{}{limit}
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying recent errors: %w", err)
	}
	defer rows.Close()

	return scanErrors(rows)
}

// scanErrors scans rows into SessionError entities.
func scanErrors(rows interface {
	Next() bool
	Scan(dest ...interface{}) error
}) ([]session.SessionError, error) {
	var errors []session.SessionError

	type scanner interface {
		Next() bool
		Scan(dest ...interface{}) error
	}
	r := rows.(scanner)

	for r.Next() {
		var (
			e           session.SessionError
			category    string
			source      string
			sessionID   string
			headersJSON string
			occurredAt  string
			retryable   int
		)

		if err := r.Scan(
			&e.ID, &sessionID, &category, &source, &e.Message, &e.RawError,
			&e.ToolName, &e.ToolCallID, &e.MessageID, &e.MessageIndex,
			&e.HTTPStatus, &e.ProviderName, &e.RequestID, &headersJSON,
			&occurredAt, &e.DurationMs, &retryable, &e.Confidence,
		); err != nil {
			continue
		}

		e.SessionID = session.ID(sessionID)
		e.Category = session.ErrorCategory(category)
		e.Source = session.ErrorSource(source)
		e.IsRetryable = retryable != 0

		if occurredAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, occurredAt); err == nil {
				e.OccurredAt = t
			}
		}

		if headersJSON != "" && headersJSON != "{}" {
			_ = json.Unmarshal([]byte(headersJSON), &e.Headers)
		}

		errors = append(errors, e)
	}

	return errors, nil
}
