package testdata

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func NewFixtureDB(t testing.TB) string {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("opening fixture db: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("closing fixture db: %v", err)
		}
	}()

	for _, stmt := range []string{
		`CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			source TEXT,
			user_id TEXT,
			model TEXT,
			parent_session_id TEXT,
			started_at REAL,
			ended_at REAL,
			end_reason TEXT,
			message_count INTEGER DEFAULT 0,
			tool_call_count INTEGER DEFAULT 0,
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			cache_read_tokens INTEGER DEFAULT 0,
			cache_write_tokens INTEGER DEFAULT 0,
			reasoning_tokens INTEGER DEFAULT 0,
			billing_provider TEXT,
			billing_base_url TEXT,
			billing_mode TEXT,
			estimated_cost_usd REAL,
			actual_cost_usd REAL,
			cost_status TEXT,
			title TEXT
		)`,
		`CREATE TABLE messages (
			id TEXT PRIMARY KEY,
			session_id TEXT,
			role TEXT,
			content TEXT,
			tool_call_id TEXT,
			tool_calls TEXT,
			tool_name TEXT,
			timestamp REAL,
			token_count INTEGER DEFAULT 0,
			finish_reason TEXT,
			reasoning TEXT,
			reasoning_content TEXT
		)`,
		`CREATE TABLE compression_locks (
			id TEXT PRIMARY KEY,
			locked_at REAL
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("creating fixture schema: %v", err)
		}
	}

	const (
		parentID      = "fixture-parent-001"
		childID       = "fixture-child-001"
		assistantMsg  = "fixture-msg-001"
		sentinelMsgID = "fixture-msg-002"
		cronID        = "cron_job123_1700000000"
	)

	if _, err := db.Exec(
		`INSERT INTO sessions (
			id, source, user_id, model, parent_session_id, started_at, ended_at, end_reason,
			message_count, tool_call_count, input_tokens, output_tokens, cache_read_tokens,
			cache_write_tokens, reasoning_tokens, billing_provider, billing_base_url,
			billing_mode, estimated_cost_usd, actual_cost_usd, cost_status, title
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		parentID, "manual", "", "claude-opus-4", nil, 1700000000.0, nil, nil,
		2, 1, 1000, 500, 0, 0, 0, nil, nil, nil, 0.05, 0.045, "final", "fixture parent",
	); err != nil {
		t.Fatalf("inserting parent session: %v", err)
	}

	if _, err := db.Exec(
		`INSERT INTO sessions (
			id, source, user_id, model, parent_session_id, started_at, ended_at, end_reason,
			message_count, tool_call_count, input_tokens, output_tokens, cache_read_tokens,
			cache_write_tokens, reasoning_tokens, billing_provider, billing_base_url,
			billing_mode, estimated_cost_usd, actual_cost_usd, cost_status, title
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		childID, "manual", "", "claude-opus-4", parentID, 1700000100.0, nil, nil,
		0, 0, 200, 100, 0, 0, 0, nil, nil, nil, 0.01, 0.009, "final", "fixture child",
	); err != nil {
		t.Fatalf("inserting child session: %v", err)
	}

	if _, err := db.Exec(
		`INSERT INTO sessions (
			id, source, user_id, model, parent_session_id, started_at, ended_at, end_reason,
			message_count, tool_call_count, input_tokens, output_tokens, cache_read_tokens,
			cache_write_tokens, reasoning_tokens, billing_provider, billing_base_url,
			billing_mode, estimated_cost_usd, actual_cost_usd, cost_status, title
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cronID, "cron", "", "claude-opus-4", nil, 1700000200.0, nil, nil,
		0, 0, 50, 0, 0, 0, 0, nil, nil, nil, 0.004, 0.003, "final", "fixture cron",
	); err != nil {
		t.Fatalf("inserting cron session: %v", err)
	}

	if _, err := db.Exec(
		`INSERT INTO messages (
			id, session_id, role, content, tool_call_id, tool_calls, tool_name,
			timestamp, token_count, finish_reason, reasoning, reasoning_content
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		assistantMsg, parentID, "assistant", "", nil,
		`[{"id":"tc1","name":"delegate_task","input":{"session_id":"fixture-child-001"}}]`,
		"delegate_task", 1700000001.0, 12, nil, "I need to delegate this task", nil,
	); err != nil {
		t.Fatalf("inserting assistant message: %v", err)
	}

	if _, err := db.Exec(
		`INSERT INTO messages (
			id, session_id, role, content, tool_call_id, tool_calls, tool_name,
			timestamp, token_count, finish_reason, reasoning, reasoning_content
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sentinelMsgID, parentID, "user", "\x00\x01{\"text\":\"hello from sentinel\"}", nil,
		nil, nil, 1700000002.0, 6, nil, nil, nil,
	); err != nil {
		t.Fatalf("inserting sentinel message: %v", err)
	}

	return dbPath
}
