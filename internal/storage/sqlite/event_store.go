package sqlite

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
)

// ── SaveEvents ──

// SaveEvents persists a batch of session events (upsert by event ID).
func (s *Store) SaveEvents(events []sessionevent.Event) error {
	if len(events) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO session_events
		(id, session_id, event_type, message_index, message_id,
		 occurred_at, project_path, remote_url, provider, agent, payload)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, e := range events {
		payload, err := marshalEventPayload(&e)
		if err != nil {
			return fmt.Errorf("marshal event %s payload: %w", e.ID, err)
		}

		occurredAt := ""
		if !e.OccurredAt.IsZero() {
			occurredAt = e.OccurredAt.Format(time.RFC3339Nano)
		}

		if _, err := stmt.Exec(
			e.ID,
			string(e.SessionID),
			string(e.Type),
			e.MessageIndex,
			e.MessageID,
			occurredAt,
			e.ProjectPath,
			e.RemoteURL,
			string(e.Provider),
			e.Agent,
			payload,
		); err != nil {
			return fmt.Errorf("insert event %s: %w", e.ID, err)
		}
	}

	return tx.Commit()
}

// ── GetSessionEvents ──

// GetSessionEvents returns all events for a given session, ordered by occurred_at.
func (s *Store) GetSessionEvents(sessionID session.ID) ([]sessionevent.Event, error) {
	rows, err := s.db.Query(
		`SELECT id, session_id, event_type, message_index, message_id,
		        occurred_at, project_path, remote_url, provider, agent, payload
		 FROM session_events
		 WHERE session_id = ?
		 ORDER BY occurred_at ASC, message_index ASC`,
		string(sessionID),
	)
	if err != nil {
		return nil, fmt.Errorf("querying session events: %w", err)
	}
	defer rows.Close()

	return scanEvents(rows)
}

// ── QueryEvents ──

// QueryEvents returns events matching the given filters.
func (s *Store) QueryEvents(query sessionevent.EventQuery) ([]sessionevent.Event, error) {
	var conditions []string
	var args []interface{}

	if query.SessionID != "" {
		conditions = append(conditions, "session_id = ?")
		args = append(args, string(query.SessionID))
	}
	if query.ProjectPath != "" {
		conditions = append(conditions, "project_path = ?")
		args = append(args, query.ProjectPath)
	}
	if query.RemoteURL != "" {
		conditions = append(conditions, "remote_url = ?")
		args = append(args, query.RemoteURL)
	}
	if query.Type != "" {
		conditions = append(conditions, "event_type = ?")
		args = append(args, string(query.Type))
	}
	if query.Provider != "" {
		conditions = append(conditions, "provider = ?")
		args = append(args, string(query.Provider))
	}
	if !query.Since.IsZero() {
		conditions = append(conditions, "occurred_at >= ?")
		args = append(args, query.Since.Format(time.RFC3339Nano))
	}
	if !query.Until.IsZero() {
		conditions = append(conditions, "occurred_at < ?")
		args = append(args, query.Until.Format(time.RFC3339Nano))
	}

	sql := `SELECT id, session_id, event_type, message_index, message_id,
	               occurred_at, project_path, remote_url, provider, agent, payload
	        FROM session_events`

	if len(conditions) > 0 {
		sql += " WHERE " + strings.Join(conditions, " AND ")
	}
	sql += " ORDER BY occurred_at ASC, message_index ASC"

	if query.Limit > 0 {
		sql += fmt.Sprintf(" LIMIT %d", query.Limit)
		if query.Offset > 0 {
			sql += fmt.Sprintf(" OFFSET %d", query.Offset)
		}
	}

	rows, err := s.db.Query(sql, args...)
	if err != nil {
		return nil, fmt.Errorf("querying events: %w", err)
	}
	defer rows.Close()

	return scanEvents(rows)
}

// ── DeleteSessionEvents ──

// DeleteSessionEvents removes all events for a session (used during re-capture).
func (s *Store) DeleteSessionEvents(sessionID session.ID) error {
	_, err := s.db.Exec("DELETE FROM session_events WHERE session_id = ?", string(sessionID))
	if err != nil {
		return fmt.Errorf("deleting session events: %w", err)
	}
	return nil
}

// ── UpsertEventBucket ──

// UpsertEventBucket inserts or merges a single event bucket.
// For batch operations, prefer UpsertEventBuckets which wraps all upserts in a transaction.
func (s *Store) UpsertEventBucket(b sessionevent.EventBucket) error {
	return s.UpsertEventBuckets([]sessionevent.EventBucket{b})
}

// UpsertEventBuckets inserts or merges multiple event buckets in a single transaction.
// On conflict, counters are added (merged) to existing values.
// This is the preferred method for batch imports — it avoids per-bucket transaction overhead.
func (s *Store) UpsertEventBuckets(buckets []sessionevent.EventBucket) error {
	if len(buckets) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`
		INSERT INTO event_buckets (
			bucket_start, granularity, project_path, remote_url, provider,
			tool_call_count, tool_error_count, unique_tools, top_tools,
			skill_load_count, unique_skills, top_skills,
			session_count, agent_breakdown,
			command_count, command_error_count, top_commands,
			error_count, error_by_category,
			image_count, image_tokens,
			computed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(bucket_start, granularity, project_path, provider) DO UPDATE SET
			tool_call_count   = event_buckets.tool_call_count   + excluded.tool_call_count,
			tool_error_count  = event_buckets.tool_error_count  + excluded.tool_error_count,
			skill_load_count  = event_buckets.skill_load_count  + excluded.skill_load_count,
			session_count     = event_buckets.session_count     + excluded.session_count,
			command_count     = event_buckets.command_count     + excluded.command_count,
			command_error_count = event_buckets.command_error_count + excluded.command_error_count,
			error_count       = event_buckets.error_count       + excluded.error_count,
			image_count       = event_buckets.image_count       + excluded.image_count,
			image_tokens      = event_buckets.image_tokens      + excluded.image_tokens,
			top_tools         = excluded.top_tools,
			unique_tools      = excluded.unique_tools,
			top_skills        = excluded.top_skills,
			unique_skills     = excluded.unique_skills,
			agent_breakdown   = excluded.agent_breakdown,
			top_commands      = excluded.top_commands,
			error_by_category = excluded.error_by_category,
			computed_at       = excluded.computed_at`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, b := range buckets {
		topToolsJSON, _ := json.Marshal(b.TopTools)
		topSkillsJSON, _ := json.Marshal(b.TopSkills)
		agentBreakdownJSON, _ := json.Marshal(b.AgentBreakdown)
		topCommandsJSON, _ := json.Marshal(b.TopCommands)
		errorByCategoryJSON, _ := json.Marshal(b.ErrorByCategory)

		if _, err := stmt.Exec(
			b.BucketStart.Format(time.RFC3339), b.Granularity, b.ProjectPath, b.RemoteURL, string(b.Provider),
			b.ToolCallCount, b.ToolErrorCount, b.UniqueTools, string(topToolsJSON),
			b.SkillLoadCount, b.UniqueSkills, string(topSkillsJSON),
			b.SessionCount, string(agentBreakdownJSON),
			b.CommandCount, b.CommandErrorCount, string(topCommandsJSON),
			b.ErrorCount, string(errorByCategoryJSON),
			b.ImageCount, b.ImageTokens,
		); err != nil {
			return fmt.Errorf("upsert bucket %s: %w", b.BucketStart.Format(time.RFC3339), err)
		}
	}

	return tx.Commit()
}

// ReplaceEventBuckets deletes existing buckets matching each bucket's key,
// then inserts the new values. This replaces additive merge with full replacement,
// solving the double-count problem on re-capture.
func (s *Store) ReplaceEventBuckets(buckets []sessionevent.EventBucket) error {
	if len(buckets) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Delete matching buckets first.
	delStmt, err := tx.Prepare(`DELETE FROM event_buckets
		WHERE bucket_start = ? AND granularity = ? AND project_path = ? AND provider = ?`)
	if err != nil {
		return fmt.Errorf("prepare delete: %w", err)
	}
	defer delStmt.Close()

	// Insert fresh buckets.
	insStmt, err := tx.Prepare(`INSERT INTO event_buckets (
		bucket_start, granularity, project_path, remote_url, provider,
		tool_call_count, tool_error_count, unique_tools, top_tools,
		skill_load_count, unique_skills, top_skills,
		session_count, agent_breakdown,
		command_count, command_error_count, top_commands,
		error_count, error_by_category,
		image_count, image_tokens,
		computed_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer insStmt.Close()

	for _, b := range buckets {
		startStr := b.BucketStart.Format(time.RFC3339)

		// Delete old.
		if _, err := delStmt.Exec(startStr, b.Granularity, b.ProjectPath, string(b.Provider)); err != nil {
			return fmt.Errorf("delete bucket %s: %w", startStr, err)
		}

		// Insert new.
		topToolsJSON, _ := json.Marshal(b.TopTools)
		topSkillsJSON, _ := json.Marshal(b.TopSkills)
		agentBreakdownJSON, _ := json.Marshal(b.AgentBreakdown)
		topCommandsJSON, _ := json.Marshal(b.TopCommands)
		errorByCategoryJSON, _ := json.Marshal(b.ErrorByCategory)

		if _, err := insStmt.Exec(
			startStr, b.Granularity, b.ProjectPath, b.RemoteURL, string(b.Provider),
			b.ToolCallCount, b.ToolErrorCount, b.UniqueTools, string(topToolsJSON),
			b.SkillLoadCount, b.UniqueSkills, string(topSkillsJSON),
			b.SessionCount, string(agentBreakdownJSON),
			b.CommandCount, b.CommandErrorCount, string(topCommandsJSON),
			b.ErrorCount, string(errorByCategoryJSON),
			b.ImageCount, b.ImageTokens,
		); err != nil {
			return fmt.Errorf("insert bucket %s: %w", startStr, err)
		}
	}

	return tx.Commit()
}

// DeleteEventBuckets removes buckets matching the query criteria.
func (s *Store) DeleteEventBuckets(query sessionevent.BucketQuery) error {
	var conditions []string
	var args []interface{}

	conditions = append(conditions, "granularity = ?")
	args = append(args, query.Granularity)

	if !query.Since.IsZero() {
		conditions = append(conditions, "bucket_start >= ?")
		args = append(args, query.Since.Format(time.RFC3339))
	}
	if !query.Until.IsZero() {
		conditions = append(conditions, "bucket_start < ?")
		args = append(args, query.Until.Format(time.RFC3339))
	}
	if query.ProjectPath != "" {
		conditions = append(conditions, "project_path = ?")
		args = append(args, query.ProjectPath)
	}
	if query.Provider != "" {
		conditions = append(conditions, "provider = ?")
		args = append(args, string(query.Provider))
	}

	sql := "DELETE FROM event_buckets"
	if len(conditions) > 0 {
		sql += " WHERE " + strings.Join(conditions, " AND ")
	}

	_, err := s.db.Exec(sql, args...)
	return err
}

// ── QueryEventBuckets ──

// QueryEventBuckets returns buckets matching the given filters.
func (s *Store) QueryEventBuckets(query sessionevent.BucketQuery) ([]sessionevent.EventBucket, error) {
	var conditions []string
	var args []interface{}

	conditions = append(conditions, "granularity = ?")
	args = append(args, query.Granularity)

	if !query.Since.IsZero() {
		conditions = append(conditions, "bucket_start >= ?")
		args = append(args, query.Since.Format(time.RFC3339))
	}
	if !query.Until.IsZero() {
		conditions = append(conditions, "bucket_start < ?")
		args = append(args, query.Until.Format(time.RFC3339))
	}
	if query.ProjectPath != "" {
		conditions = append(conditions, "project_path = ?")
		args = append(args, query.ProjectPath)
	}
	if query.RemoteURL != "" {
		conditions = append(conditions, "remote_url = ?")
		args = append(args, query.RemoteURL)
	}
	if query.Provider != "" {
		conditions = append(conditions, "provider = ?")
		args = append(args, string(query.Provider))
	}

	sql := `SELECT bucket_start, granularity, project_path, remote_url, provider,
	               tool_call_count, tool_error_count, unique_tools, COALESCE(top_tools, '{}'),
	               skill_load_count, unique_skills, COALESCE(top_skills, '{}'),
	               session_count, COALESCE(agent_breakdown, '{}'),
	               command_count, command_error_count, COALESCE(top_commands, '{}'),
	               error_count, COALESCE(error_by_category, '{}'),
	               image_count, image_tokens
	        FROM event_buckets`

	if len(conditions) > 0 {
		sql += " WHERE " + strings.Join(conditions, " AND ")
	}
	sql += " ORDER BY bucket_start ASC"

	rows, err := s.db.Query(sql, args...)
	if err != nil {
		return nil, fmt.Errorf("querying event buckets: %w", err)
	}
	defer rows.Close()

	var buckets []sessionevent.EventBucket
	for rows.Next() {
		var b sessionevent.EventBucket
		var startStr, provStr string
		var topToolsJSON, topSkillsJSON, agentJSON, topCmdsJSON, errCatJSON string

		if err := rows.Scan(
			&startStr, &b.Granularity, &b.ProjectPath, &b.RemoteURL, &provStr,
			&b.ToolCallCount, &b.ToolErrorCount, &b.UniqueTools, &topToolsJSON,
			&b.SkillLoadCount, &b.UniqueSkills, &topSkillsJSON,
			&b.SessionCount, &agentJSON,
			&b.CommandCount, &b.CommandErrorCount, &topCmdsJSON,
			&b.ErrorCount, &errCatJSON,
			&b.ImageCount, &b.ImageTokens,
		); err != nil {
			continue
		}

		b.BucketStart, _ = time.Parse(time.RFC3339, startStr)
		b.BucketEnd = b.BucketStart.Add(parseDuration(b.Granularity))
		b.Provider = session.ProviderName(provStr)

		// Unmarshal JSON maps.
		b.TopTools = make(map[string]int)
		_ = json.Unmarshal([]byte(topToolsJSON), &b.TopTools)
		b.TopSkills = make(map[string]int)
		_ = json.Unmarshal([]byte(topSkillsJSON), &b.TopSkills)
		b.AgentBreakdown = make(map[string]int)
		_ = json.Unmarshal([]byte(agentJSON), &b.AgentBreakdown)
		b.TopCommands = make(map[string]int)
		_ = json.Unmarshal([]byte(topCmdsJSON), &b.TopCommands)
		b.ErrorByCategory = make(map[session.ErrorCategory]int)
		_ = json.Unmarshal([]byte(errCatJSON), &b.ErrorByCategory)

		buckets = append(buckets, b)
	}
	return buckets, rows.Err()
}

// ── Internal helpers ──

// marshalEventPayload serializes the type-specific payload of an event.
func marshalEventPayload(e *sessionevent.Event) ([]byte, error) {
	switch e.Type {
	case sessionevent.EventToolCall:
		return json.Marshal(e.ToolCall)
	case sessionevent.EventSkillLoad:
		return json.Marshal(e.SkillLoad)
	case sessionevent.EventAgentDetection:
		return json.Marshal(e.AgentInfo)
	case sessionevent.EventError:
		return json.Marshal(e.Error)
	case sessionevent.EventCommand:
		return json.Marshal(e.Command)
	case sessionevent.EventImageUsage:
		return json.Marshal(e.Image)
	default:
		return []byte("{}"), nil
	}
}

// unmarshalEventPayload deserializes the type-specific payload into an event.
func unmarshalEventPayload(e *sessionevent.Event, data []byte) {
	if len(data) == 0 || string(data) == "{}" {
		return
	}

	switch e.Type {
	case sessionevent.EventToolCall:
		var d sessionevent.ToolCallDetail
		if err := json.Unmarshal(data, &d); err == nil {
			e.ToolCall = &d
		}
	case sessionevent.EventSkillLoad:
		var d sessionevent.SkillLoadDetail
		if err := json.Unmarshal(data, &d); err == nil {
			e.SkillLoad = &d
		}
	case sessionevent.EventAgentDetection:
		var d sessionevent.AgentDetail
		if err := json.Unmarshal(data, &d); err == nil {
			e.AgentInfo = &d
		}
	case sessionevent.EventError:
		var d sessionevent.ErrorDetail
		if err := json.Unmarshal(data, &d); err == nil {
			e.Error = &d
		}
	case sessionevent.EventCommand:
		var d sessionevent.CommandDetail
		if err := json.Unmarshal(data, &d); err == nil {
			e.Command = &d
		}
	case sessionevent.EventImageUsage:
		var d sessionevent.ImageDetail
		if err := json.Unmarshal(data, &d); err == nil {
			e.Image = &d
		}
	}
}

// scanEvents scans rows into Event entities.
func scanEvents(rows interface {
	Next() bool
	Scan(dest ...interface{}) error
}) ([]sessionevent.Event, error) {
	var events []sessionevent.Event

	type scanner interface {
		Next() bool
		Scan(dest ...interface{}) error
	}
	r := rows.(scanner)

	for r.Next() {
		var (
			e          sessionevent.Event
			sessionID  string
			eventType  string
			occurredAt string
			provider   string
			payload    []byte
		)

		if err := r.Scan(
			&e.ID, &sessionID, &eventType, &e.MessageIndex, &e.MessageID,
			&occurredAt, &e.ProjectPath, &e.RemoteURL, &provider, &e.Agent,
			&payload,
		); err != nil {
			continue
		}

		e.SessionID = session.ID(sessionID)
		e.Type = sessionevent.EventType(eventType)
		e.Provider = session.ProviderName(provider)

		if occurredAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, occurredAt); err == nil {
				e.OccurredAt = t
			}
		}

		unmarshalEventPayload(&e, payload)

		events = append(events, e)
	}

	return events, nil
}
