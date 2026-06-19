package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func (s *Store) InsertNotificationLog(entry *session.NotificationLogEntry) error {
	if entry == nil {
		return fmt.Errorf("InsertNotificationLog: nil entry")
	}
	if entry.EventType == "" {
		return fmt.Errorf("InsertNotificationLog: empty event_type")
	}
	if entry.DispatchedAt.IsZero() {
		entry.DispatchedAt = time.Now().UTC()
	}
	severity := entry.Severity
	if severity == "" {
		severity = "info"
	}

	var ackAt sql.NullInt64
	if entry.AcknowledgedAt != nil {
		ackAt = sql.NullInt64{Int64: entry.AcknowledgedAt.UnixMilli(), Valid: true}
	}

	res, err := s.db.Exec(`
		INSERT INTO notification_log (
			event_type, severity, project, title, summary,
			payload_json, dispatched_at, acknowledged_at, acknowledged_by, dedup_key
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		entry.EventType,
		severity,
		entry.Project,
		entry.Title,
		entry.Summary,
		entry.PayloadJSON,
		entry.DispatchedAt.UnixMilli(),
		ackAt,
		entry.AcknowledgedBy,
		entry.DedupKey,
	)
	if err != nil {
		return fmt.Errorf("insert notification_log: %w", err)
	}
	if id, idErr := res.LastInsertId(); idErr == nil {
		entry.ID = id
	}
	return nil
}

func (s *Store) GetNotificationLog(id int64) (*session.NotificationLogEntry, error) {
	row := s.db.QueryRow(`
		SELECT id, event_type, severity, project, title, summary,
		       payload_json, dispatched_at, acknowledged_at, acknowledged_by, dedup_key
		FROM notification_log WHERE id = ?
	`, id)
	entry, err := scanNotificationLogRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get notification_log: %w", err)
	}
	return entry, nil
}

func (s *Store) ListNotificationLogs(filter session.NotificationLogFilter) ([]session.NotificationLogEntry, error) {
	var sb strings.Builder
	sb.WriteString(`SELECT id, event_type, severity, project, title, summary,
		payload_json, dispatched_at, acknowledged_at, acknowledged_by, dedup_key
		FROM notification_log`)

	var clauses []string
	var args []any
	if !filter.Since.IsZero() {
		clauses = append(clauses, "dispatched_at >= ?")
		args = append(args, filter.Since.UnixMilli())
	}
	if !filter.Until.IsZero() {
		clauses = append(clauses, "dispatched_at <= ?")
		args = append(args, filter.Until.UnixMilli())
	}
	if filter.OnlyUnack {
		clauses = append(clauses, "acknowledged_at IS NULL")
	}
	if len(filter.EventTypes) > 0 {
		clauses = append(clauses, inClause("event_type", len(filter.EventTypes)))
		for _, t := range filter.EventTypes {
			args = append(args, t)
		}
	}
	if len(filter.Severities) > 0 {
		clauses = append(clauses, inClause("severity", len(filter.Severities)))
		for _, sev := range filter.Severities {
			args = append(args, sev)
		}
	}
	if len(filter.Projects) > 0 {
		clauses = append(clauses, inClause("project", len(filter.Projects)))
		for _, p := range filter.Projects {
			args = append(args, p)
		}
	}
	if len(clauses) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(clauses, " AND "))
	}
	sb.WriteString(" ORDER BY dispatched_at DESC")
	if filter.Limit > 0 {
		sb.WriteString(fmt.Sprintf(" LIMIT %d", filter.Limit))
		if filter.Offset > 0 {
			sb.WriteString(fmt.Sprintf(" OFFSET %d", filter.Offset))
		}
	}

	rows, err := s.db.Query(sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list notification_log: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []session.NotificationLogEntry
	for rows.Next() {
		entry, scanErr := scanNotificationLogRow(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan notification_log row: %w", scanErr)
		}
		out = append(out, *entry)
	}
	return out, rows.Err()
}

func (s *Store) AcknowledgeNotification(id int64, actor string, at time.Time) error {
	if id <= 0 {
		return fmt.Errorf("AcknowledgeNotification: invalid id %d", id)
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := s.db.Exec(`
		UPDATE notification_log
		SET acknowledged_at = ?, acknowledged_by = ?
		WHERE id = ? AND acknowledged_at IS NULL
	`, at.UnixMilli(), actor, id)
	if err != nil {
		return fmt.Errorf("acknowledge notification: %w", err)
	}
	return nil
}

func (s *Store) AcknowledgeAllNotifications(actor string, at time.Time) (int, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	res, err := s.db.Exec(`
		UPDATE notification_log
		SET acknowledged_at = ?, acknowledged_by = ?
		WHERE acknowledged_at IS NULL
	`, at.UnixMilli(), actor)
	if err != nil {
		return 0, fmt.Errorf("acknowledge all notifications: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *Store) UnacknowledgedNotificationCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM notification_log WHERE acknowledged_at IS NULL`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count unacked notifications: %w", err)
	}
	return n, nil
}

func inClause(col string, count int) string {
	placeholders := strings.Repeat("?,", count)
	placeholders = placeholders[:len(placeholders)-1]
	return col + " IN (" + placeholders + ")"
}

func scanNotificationLogRow(r rowScanner) (*session.NotificationLogEntry, error) {
	var (
		entry           session.NotificationLogEntry
		dispatchedAtMs  int64
		acknowledgedAt  sql.NullInt64
	)
	err := r.Scan(
		&entry.ID,
		&entry.EventType,
		&entry.Severity,
		&entry.Project,
		&entry.Title,
		&entry.Summary,
		&entry.PayloadJSON,
		&dispatchedAtMs,
		&acknowledgedAt,
		&entry.AcknowledgedBy,
		&entry.DedupKey,
	)
	if err != nil {
		return nil, err
	}
	entry.DispatchedAt = time.UnixMilli(dispatchedAtMs).UTC()
	if acknowledgedAt.Valid {
		t := time.UnixMilli(acknowledgedAt.Int64).UTC()
		entry.AcknowledgedAt = &t
	}
	return &entry, nil
}
