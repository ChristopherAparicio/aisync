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
		 occurred_at, duration_ms, is_retryable, confidence, fingerprint)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
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
			e.Fingerprint,
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
		        occurred_at, duration_ms, is_retryable, confidence, fingerprint
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
		                occurred_at, duration_ms, is_retryable, confidence, fingerprint
		         FROM session_errors
		         WHERE category = ?
		         ORDER BY occurred_at DESC
		         LIMIT ?`
		args = []interface{}{string(category), limit}
	} else {
		query = `SELECT id, session_id, category, source, message, raw_error, tool_name, tool_call_id,
		                message_id, message_index, http_status, provider_name, request_id, headers,
		                occurred_at, duration_ms, is_retryable, confidence, fingerprint
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
			&e.Fingerprint,
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

// ── Fingerprint-based grouping ──

// UpsertFingerprint creates or updates an error fingerprint group.
func (s *Store) UpsertFingerprint(group session.ErrorFingerprintGroup) error {
	firstSeen := ""
	if !group.FirstSeen.IsZero() {
		firstSeen = group.FirstSeen.Format(time.RFC3339Nano)
	}
	lastSeen := ""
	if !group.LastSeen.IsZero() {
		lastSeen = group.LastSeen.Format(time.RFC3339Nano)
	}
	classifiedAt := ""
	if !group.ClassifiedAt.IsZero() {
		classifiedAt = group.ClassifiedAt.Format(time.RFC3339Nano)
	}

	_, err := s.db.Exec(`INSERT INTO error_fingerprints
		(fingerprint, sample_raw, category, message, classified_by, classified_at,
		 first_seen, last_seen, occurrence_count, project_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(fingerprint) DO UPDATE SET
			sample_raw = CASE WHEN excluded.sample_raw != '' THEN excluded.sample_raw ELSE error_fingerprints.sample_raw END,
			last_seen = CASE WHEN excluded.last_seen > error_fingerprints.last_seen THEN excluded.last_seen ELSE error_fingerprints.last_seen END,
			occurrence_count = error_fingerprints.occurrence_count + excluded.occurrence_count,
			project_count = MAX(error_fingerprints.project_count, excluded.project_count)`,
		group.Fingerprint,
		group.SampleRaw,
		string(group.Category),
		group.Message,
		group.ClassifiedBy,
		classifiedAt,
		firstSeen,
		lastSeen,
		group.OccurrenceCount,
		group.ProjectCount,
	)
	if err != nil {
		return fmt.Errorf("upsert fingerprint %s: %w", group.Fingerprint, err)
	}
	return nil
}

// ListFingerprintGroups returns fingerprint groups ordered by occurrence_count DESC.
func (s *Store) ListFingerprintGroups(onlyUnclassified bool, limit int) ([]session.ErrorFingerprintGroup, error) {
	if limit <= 0 {
		limit = 100
	}

	var query string
	var args []interface{}
	if onlyUnclassified {
		query = `SELECT fingerprint, sample_raw, category, message, classified_by,
		                classified_at, first_seen, last_seen, occurrence_count, project_count
		         FROM error_fingerprints
		         WHERE category = '' OR category = 'unknown'
		         ORDER BY occurrence_count DESC
		         LIMIT ?`
		args = []interface{}{limit}
	} else {
		query = `SELECT fingerprint, sample_raw, category, message, classified_by,
		                classified_at, first_seen, last_seen, occurrence_count, project_count
		         FROM error_fingerprints
		         ORDER BY occurrence_count DESC
		         LIMIT ?`
		args = []interface{}{limit}
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing fingerprint groups: %w", err)
	}
	defer rows.Close()

	return scanFingerprintGroups(rows)
}

// GetFingerprintGroup returns a single fingerprint group.
func (s *Store) GetFingerprintGroup(fingerprint string) (*session.ErrorFingerprintGroup, error) {
	row := s.db.QueryRow(
		`SELECT fingerprint, sample_raw, category, message, classified_by,
		        classified_at, first_seen, last_seen, occurrence_count, project_count
		 FROM error_fingerprints
		 WHERE fingerprint = ?`,
		fingerprint,
	)

	var (
		g            session.ErrorFingerprintGroup
		category     string
		classifiedAt string
		firstSeen    string
		lastSeen     string
	)
	if err := row.Scan(
		&g.Fingerprint, &g.SampleRaw, &category, &g.Message, &g.ClassifiedBy,
		&classifiedAt, &firstSeen, &lastSeen, &g.OccurrenceCount, &g.ProjectCount,
	); err != nil {
		return nil, fmt.Errorf("get fingerprint group %s: %w", fingerprint, err)
	}

	g.Category = session.ErrorCategory(category)
	if classifiedAt != "" {
		if t, parseErr := time.Parse(time.RFC3339Nano, classifiedAt); parseErr == nil {
			g.ClassifiedAt = t
		}
	}
	if firstSeen != "" {
		if t, parseErr := time.Parse(time.RFC3339Nano, firstSeen); parseErr == nil {
			g.FirstSeen = t
		}
	}
	if lastSeen != "" {
		if t, parseErr := time.Parse(time.RFC3339Nano, lastSeen); parseErr == nil {
			g.LastSeen = t
		}
	}

	return &g, nil
}

// ClassifyFingerprintGroup sets the category + message on a fingerprint group
// and bulk-updates all session_errors sharing that fingerprint.
func (s *Store) ClassifyFingerprintGroup(fingerprint string, category session.ErrorCategory, message string, classifiedBy string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().Format(time.RFC3339Nano)

	// Update the fingerprint group.
	if _, err := tx.Exec(
		`UPDATE error_fingerprints SET category = ?, message = ?, classified_by = ?, classified_at = ? WHERE fingerprint = ?`,
		string(category), message, classifiedBy, now, fingerprint,
	); err != nil {
		return fmt.Errorf("update fingerprint %s: %w", fingerprint, err)
	}

	// Bulk-update all session_errors with this fingerprint.
	if _, err := tx.Exec(
		`UPDATE session_errors SET category = ?, message = ?, confidence = 'high' WHERE fingerprint = ?`,
		string(category), message, fingerprint,
	); err != nil {
		return fmt.Errorf("bulk-update errors for fingerprint %s: %w", fingerprint, err)
	}

	return tx.Commit()
}

// GetFingerprintMatch looks up a classified fingerprint for auto-classification.
// Returns nil if not found or not yet classified.
func (s *Store) GetFingerprintMatch(fingerprint string) (*session.ErrorFingerprintGroup, error) {
	g, err := s.GetFingerprintGroup(fingerprint)
	if err != nil {
		return nil, nil // not found — not an error, just no match
	}
	// Only return classified groups.
	if g.Category == "" || g.Category == session.ErrorCategoryUnknown {
		return nil, nil
	}
	return g, nil
}

// scanFingerprintGroups scans rows into ErrorFingerprintGroup entities.
func scanFingerprintGroups(rows interface {
	Next() bool
	Scan(dest ...interface{}) error
}) ([]session.ErrorFingerprintGroup, error) {
	var groups []session.ErrorFingerprintGroup

	type scanner interface {
		Next() bool
		Scan(dest ...interface{}) error
	}
	r := rows.(scanner)

	for r.Next() {
		var (
			g            session.ErrorFingerprintGroup
			category     string
			classifiedAt string
			firstSeen    string
			lastSeen     string
		)

		if err := r.Scan(
			&g.Fingerprint, &g.SampleRaw, &category, &g.Message, &g.ClassifiedBy,
			&classifiedAt, &firstSeen, &lastSeen, &g.OccurrenceCount, &g.ProjectCount,
		); err != nil {
			continue
		}

		g.Category = session.ErrorCategory(category)
		if classifiedAt != "" {
			if t, parseErr := time.Parse(time.RFC3339Nano, classifiedAt); parseErr == nil {
				g.ClassifiedAt = t
			}
		}
		if firstSeen != "" {
			if t, parseErr := time.Parse(time.RFC3339Nano, firstSeen); parseErr == nil {
				g.FirstSeen = t
			}
		}
		if lastSeen != "" {
			if t, parseErr := time.Parse(time.RFC3339Nano, lastSeen); parseErr == nil {
				g.LastSeen = t
			}
		}

		groups = append(groups, g)
	}

	return groups, nil
}
