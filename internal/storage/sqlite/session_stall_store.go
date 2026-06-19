package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func (s *Store) UpsertStall(stall *session.SessionStall) error {
	if stall == nil {
		return fmt.Errorf("UpsertStall: nil stall")
	}
	if stall.ProviderSessionID == "" {
		return fmt.Errorf("UpsertStall: empty provider_session_id")
	}
	if stall.RootCause == "" {
		return fmt.Errorf("UpsertStall: empty root_cause")
	}

	now := time.Now().UTC()
	if stall.CreatedAt.IsZero() {
		stall.CreatedAt = now
	}
	stall.UpdatedAt = now

	var endedAtMs sql.NullInt64
	if stall.EndedAt != nil {
		endedAtMs = sql.NullInt64{Int64: stall.EndedAt.UnixMilli(), Valid: true}
		if stall.DurationMs == 0 {
			stall.DurationMs = stall.EndedAt.Sub(stall.StartedAt).Milliseconds()
		}
	}
	var durationMs sql.NullInt64
	if stall.DurationMs > 0 {
		durationMs = sql.NullInt64{Int64: stall.DurationMs, Valid: true}
	}

	res, err := s.db.Exec(`
		INSERT INTO session_stalls (
			session_id, provider_session_id, detected_at, started_at,
			ended_at, duration_ms, root_cause, provider, model, agent,
			parent_session_id, tool_name, tokens_lost, cost_lost_usd,
			error_message, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider_session_id, started_at, root_cause) DO UPDATE SET
			session_id    = excluded.session_id,
			detected_at   = MIN(session_stalls.detected_at, excluded.detected_at),
			ended_at      = COALESCE(excluded.ended_at, session_stalls.ended_at),
			duration_ms   = COALESCE(excluded.duration_ms, session_stalls.duration_ms),
			provider      = excluded.provider,
			model         = excluded.model,
			agent         = excluded.agent,
			parent_session_id = excluded.parent_session_id,
			tool_name     = excluded.tool_name,
			tokens_lost   = MAX(session_stalls.tokens_lost, excluded.tokens_lost),
			cost_lost_usd = MAX(session_stalls.cost_lost_usd, excluded.cost_lost_usd),
			error_message = CASE
				WHEN excluded.error_message != '' THEN excluded.error_message
				ELSE session_stalls.error_message
			END,
			updated_at    = excluded.updated_at
	`,
		string(stall.SessionID),
		stall.ProviderSessionID,
		stall.DetectedAt.UnixMilli(),
		stall.StartedAt.UnixMilli(),
		endedAtMs,
		durationMs,
		string(stall.RootCause),
		stall.Provider,
		stall.Model,
		stall.Agent,
		stall.ParentSessionID,
		stall.ToolName,
		stall.TokensLost,
		stall.CostLostUSD,
		stall.ErrorMessage,
		stall.CreatedAt.UnixMilli(),
		stall.UpdatedAt.UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("upsert stall: %w", err)
	}

	if stall.ID == 0 {
		if id, idErr := res.LastInsertId(); idErr == nil && id > 0 {
			stall.ID = id
		} else {
			row := s.db.QueryRow(
				`SELECT id FROM session_stalls WHERE provider_session_id = ? AND started_at = ? AND root_cause = ?`,
				stall.ProviderSessionID,
				stall.StartedAt.UnixMilli(),
				string(stall.RootCause),
			)
			if scanErr := row.Scan(&stall.ID); scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
				return fmt.Errorf("lookup stall id: %w", scanErr)
			}
		}
	}
	return nil
}

func (s *Store) SealStall(id int64, endedAt time.Time) error {
	if id <= 0 {
		return fmt.Errorf("SealStall: invalid id %d", id)
	}
	endedMs := endedAt.UTC().UnixMilli()
	_, err := s.db.Exec(`
		UPDATE session_stalls
		SET ended_at    = ?,
		    duration_ms = ? - started_at,
		    updated_at  = ?
		WHERE id = ? AND ended_at IS NULL
	`, endedMs, endedMs, time.Now().UTC().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("seal stall: %w", err)
	}
	return nil
}

func (s *Store) GetStall(id int64) (*session.SessionStall, error) {
	row := s.db.QueryRow(`
		SELECT id, session_id, provider_session_id, detected_at, started_at,
		       ended_at, duration_ms, root_cause, provider, model, agent,
		       parent_session_id, tool_name, tokens_lost, cost_lost_usd,
		       error_message, created_at, updated_at
		FROM session_stalls WHERE id = ?
	`, id)
	stall, err := scanStallRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get stall: %w", err)
	}
	return stall, nil
}

func (s *Store) ListStalls(filter session.StallFilter) ([]session.SessionStall, error) {
	q, args := buildStallSelect(filter, false)
	q += " ORDER BY detected_at DESC"
	if filter.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("list stalls: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanStallRows(rows)
}

func (s *Store) ListLiveStalls() ([]session.SessionStall, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, provider_session_id, detected_at, started_at,
		       ended_at, duration_ms, root_cause, provider, model, agent,
		       parent_session_id, tool_name, tokens_lost, cost_lost_usd,
		       error_message, created_at, updated_at
		FROM session_stalls
		WHERE ended_at IS NULL
		ORDER BY started_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list live stalls: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanStallRows(rows)
}

func (s *Store) StallStats(filter session.StallFilter) (*session.StallStats, error) {
	filter.OnlyLive = false
	q, args := buildStallSelect(filter, true)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("stall stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	stats := &session.StallStats{
		ByRootCause: make(map[session.StallRootCause]session.StallStatsRow),
		ByProvider:  make(map[string]session.StallStatsRow),
	}
	for rows.Next() {
		var (
			endedAt    sql.NullInt64
			durationMs sql.NullInt64
			rootCause  string
			provider   string
			tokens     int64
			cost       float64
		)
		if scanErr := rows.Scan(&endedAt, &durationMs, &rootCause, &provider, &tokens, &cost); scanErr != nil {
			return nil, fmt.Errorf("scan stall stats: %w", scanErr)
		}
		stats.TotalCount++
		stats.TokensLost += tokens
		stats.CostLostUSD += cost
		if durationMs.Valid {
			stats.TotalDurationMs += durationMs.Int64
		}
		if !endedAt.Valid {
			stats.LiveCount++
		}

		rc := session.StallRootCause(rootCause)
		r := stats.ByRootCause[rc]
		r.Count++
		r.TokensLost += tokens
		r.CostLostUSD += cost
		stats.ByRootCause[rc] = r

		p := stats.ByProvider[provider]
		p.Count++
		p.TokensLost += tokens
		p.CostLostUSD += cost
		stats.ByProvider[provider] = p
	}
	return stats, rows.Err()
}

func buildStallSelect(filter session.StallFilter, statsOnly bool) (string, []any) {
	var sb strings.Builder
	if statsOnly {
		sb.WriteString(`SELECT ended_at, duration_ms, root_cause, provider, tokens_lost, cost_lost_usd FROM session_stalls`)
	} else {
		sb.WriteString(`SELECT id, session_id, provider_session_id, detected_at, started_at,
			ended_at, duration_ms, root_cause, provider, model, agent,
			parent_session_id, tool_name, tokens_lost, cost_lost_usd,
			error_message, created_at, updated_at FROM session_stalls`)
	}

	var clauses []string
	var args []any
	if !filter.Since.IsZero() {
		clauses = append(clauses, "detected_at >= ?")
		args = append(args, filter.Since.UnixMilli())
	}
	if !filter.Until.IsZero() {
		clauses = append(clauses, "detected_at <= ?")
		args = append(args, filter.Until.UnixMilli())
	}
	if filter.OnlyLive {
		clauses = append(clauses, "ended_at IS NULL")
	}
	if len(filter.RootCauses) > 0 {
		placeholders := strings.Repeat("?,", len(filter.RootCauses))
		placeholders = placeholders[:len(placeholders)-1]
		clauses = append(clauses, "root_cause IN ("+placeholders+")")
		for _, rc := range filter.RootCauses {
			args = append(args, string(rc))
		}
	}
	if len(filter.Providers) > 0 {
		placeholders := strings.Repeat("?,", len(filter.Providers))
		placeholders = placeholders[:len(placeholders)-1]
		clauses = append(clauses, "provider IN ("+placeholders+")")
		for _, p := range filter.Providers {
			args = append(args, p)
		}
	}
	if len(clauses) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(clauses, " AND "))
	}
	return sb.String(), args
}

func scanStallRow(r rowScanner) (*session.SessionStall, error) {
	var (
		s             session.SessionStall
		sessionID     string
		detectedAtMs  int64
		startedAtMs   int64
		endedAtMs     sql.NullInt64
		durationMs    sql.NullInt64
		rootCause     string
		createdAtMs   int64
		updatedAtMs   int64
	)
	err := r.Scan(
		&s.ID,
		&sessionID,
		&s.ProviderSessionID,
		&detectedAtMs,
		&startedAtMs,
		&endedAtMs,
		&durationMs,
		&rootCause,
		&s.Provider,
		&s.Model,
		&s.Agent,
		&s.ParentSessionID,
		&s.ToolName,
		&s.TokensLost,
		&s.CostLostUSD,
		&s.ErrorMessage,
		&createdAtMs,
		&updatedAtMs,
	)
	if err != nil {
		return nil, err
	}
	s.SessionID = session.ID(sessionID)
	s.DetectedAt = time.UnixMilli(detectedAtMs).UTC()
	s.StartedAt = time.UnixMilli(startedAtMs).UTC()
	if endedAtMs.Valid {
		t := time.UnixMilli(endedAtMs.Int64).UTC()
		s.EndedAt = &t
	}
	if durationMs.Valid {
		s.DurationMs = durationMs.Int64
	}
	s.RootCause = session.StallRootCause(rootCause)
	s.CreatedAt = time.UnixMilli(createdAtMs).UTC()
	s.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
	return &s, nil
}

func scanStallRows(rows *sql.Rows) ([]session.SessionStall, error) {
	var out []session.SessionStall
	for rows.Next() {
		stall, err := scanStallRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan stall row: %w", err)
		}
		out = append(out, *stall)
	}
	return out, rows.Err()
}
