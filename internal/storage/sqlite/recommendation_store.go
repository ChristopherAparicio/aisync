package sqlite

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/google/uuid"
)

// ── RecommendationStore implementation ──

// UpsertRecommendation inserts a new recommendation or updates an existing one
// matched by fingerprint. Only updates if the existing status is 'active' —
// dismissed/snoozed recommendations are not overwritten (user intent is preserved).
func (s *Store) UpsertRecommendation(rec *session.RecommendationRecord) error {
	if rec.ID == "" {
		rec.ID = uuid.New().String()
	}
	if rec.Status == "" {
		rec.Status = session.RecStatusActive
	}
	if rec.Source == "" {
		rec.Source = session.RecSourceDeterministic
	}
	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = now
	}

	_, err := s.db.Exec(`
		INSERT INTO recommendations
			(id, project_path, type, priority, source, icon, title, message, impact,
			 agent, skill, status, fingerprint, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(fingerprint) DO UPDATE SET
			priority   = excluded.priority,
			icon       = excluded.icon,
			title      = excluded.title,
			message    = excluded.message,
			impact     = excluded.impact,
			agent      = excluded.agent,
			skill      = excluded.skill,
			source     = excluded.source,
			updated_at = CURRENT_TIMESTAMP
		WHERE recommendations.status = 'active'`,
		rec.ID, rec.ProjectPath, rec.Type, rec.Priority,
		string(rec.Source), rec.Icon, rec.Title, rec.Message, rec.Impact,
		rec.Agent, rec.Skill, string(rec.Status), rec.Fingerprint,
		rec.CreatedAt.Format(time.RFC3339),
		rec.UpdatedAt.Format(time.RFC3339),
	)
	return err
}

// ListRecommendations returns recommendations matching the given filter.
// Results are ordered by priority (high > medium > low), then created_at DESC.
func (s *Store) ListRecommendations(filter session.RecommendationFilter) ([]session.RecommendationRecord, error) {
	query := `SELECT id, project_path, type, priority, source, icon, title, message,
		impact, agent, skill, status, fingerprint,
		created_at, updated_at, dismissed_at, snoozed_until
		FROM recommendations WHERE 1=1`
	var args []interface{}

	if filter.ProjectPath != "" {
		query += " AND project_path = ?"
		args = append(args, filter.ProjectPath)
	}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, string(filter.Status))
	}
	if filter.Priority != "" {
		query += " AND priority = ?"
		args = append(args, filter.Priority)
	}
	if filter.Source != "" {
		query += " AND source = ?"
		args = append(args, string(filter.Source))
	}

	query += ` ORDER BY
		CASE priority WHEN 'high' THEN 0 WHEN 'medium' THEN 1 WHEN 'low' THEN 2 ELSE 3 END,
		created_at DESC`

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list recommendations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []session.RecommendationRecord
	for rows.Next() {
		rec, scanErr := scanRecommendation(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan recommendation: %w", scanErr)
		}
		results = append(results, *rec)
	}
	return results, rows.Err()
}

// DismissRecommendation marks a recommendation as dismissed.
func (s *Store) DismissRecommendation(id string) error {
	_, err := s.db.Exec(`
		UPDATE recommendations
		SET status = 'dismissed', dismissed_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, id)
	return err
}

// SnoozeRecommendation marks a recommendation as snoozed until the given time.
func (s *Store) SnoozeRecommendation(id string, until time.Time) error {
	_, err := s.db.Exec(`
		UPDATE recommendations
		SET status = 'snoozed', snoozed_until = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		until.UTC().Format(time.RFC3339), id)
	return err
}

// ExpireRecommendations marks active recommendations older than maxAge as expired.
// Returns the number of expired recommendations.
func (s *Store) ExpireRecommendations(maxAge time.Duration) (int, error) {
	cutoff := time.Now().Add(-maxAge).UTC().Format(time.RFC3339)

	result, err := s.db.Exec(`
		UPDATE recommendations
		SET status = 'expired', updated_at = CURRENT_TIMESTAMP
		WHERE status = 'active' AND updated_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("expire recommendations: %w", err)
	}

	n, err := result.RowsAffected()
	return int(n), err
}

// ReactivateSnoozed reactivates snoozed recommendations whose snooze period has elapsed.
// Returns the number of reactivated recommendations.
func (s *Store) ReactivateSnoozed() (int, error) {
	result, err := s.db.Exec(`
		UPDATE recommendations
		SET status = 'active', snoozed_until = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE status = 'snoozed' AND snoozed_until <= CURRENT_TIMESTAMP`)
	if err != nil {
		return 0, fmt.Errorf("reactivate snoozed: %w", err)
	}

	n, err := result.RowsAffected()
	return int(n), err
}

// RecommendationStats returns aggregate counts by status for a project.
// Pass empty projectPath for global stats.
func (s *Store) RecommendationStats(projectPath string) (session.RecommendationStats, error) {
	query := "SELECT status, COUNT(*) FROM recommendations"
	var args []interface{}
	if projectPath != "" {
		query += " WHERE project_path = ?"
		args = append(args, projectPath)
	}
	query += " GROUP BY status"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return session.RecommendationStats{}, fmt.Errorf("recommendation stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var stats session.RecommendationStats
	for rows.Next() {
		var status string
		var count int
		if scanErr := rows.Scan(&status, &count); scanErr != nil {
			return stats, fmt.Errorf("scan recommendation stats: %w", scanErr)
		}
		stats.Total += count
		switch session.RecommendationStatus(status) {
		case session.RecStatusActive:
			stats.Active = count
		case session.RecStatusDismissed:
			stats.Dismissed = count
		case session.RecStatusSnoozed:
			stats.Snoozed = count
		}
	}
	return stats, rows.Err()
}

// DeleteRecommendationsByProject removes all recommendations for a project.
// Returns the number of deleted recommendations.
func (s *Store) DeleteRecommendationsByProject(projectPath string) (int, error) {
	result, err := s.db.Exec("DELETE FROM recommendations WHERE project_path = ?", projectPath)
	if err != nil {
		return 0, fmt.Errorf("delete recommendations by project: %w", err)
	}

	n, err := result.RowsAffected()
	return int(n), err
}

// ── helpers ──

// scanRecommendation reads a single recommendation from a Rows cursor.
func scanRecommendation(rows *sql.Rows) (*session.RecommendationRecord, error) {
	var rec session.RecommendationRecord
	var (
		source       string
		status       string
		createdAt    string
		updatedAt    string
		dismissedAt  sql.NullString
		snoozedUntil sql.NullString
	)

	if err := rows.Scan(
		&rec.ID, &rec.ProjectPath, &rec.Type, &rec.Priority,
		&source, &rec.Icon, &rec.Title, &rec.Message,
		&rec.Impact, &rec.Agent, &rec.Skill,
		&status, &rec.Fingerprint,
		&createdAt, &updatedAt, &dismissedAt, &snoozedUntil,
	); err != nil {
		return nil, err
	}

	rec.Source = session.RecommendationSource(source)
	rec.Status = session.RecommendationStatus(status)
	rec.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	rec.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	if dismissedAt.Valid && dismissedAt.String != "" {
		if t, err := time.Parse(time.RFC3339, dismissedAt.String); err == nil {
			rec.DismissedAt = &t
		}
	}
	if snoozedUntil.Valid && snoozedUntil.String != "" {
		if t, err := time.Parse(time.RFC3339, snoozedUntil.String); err == nil {
			rec.SnoozedUntil = &t
		}
	}

	return &rec, nil
}
