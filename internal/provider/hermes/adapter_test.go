package hermes

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/provider/hermes/testdata"
	"github.com/ChristopherAparicio/aisync/internal/session"
	_ "modernc.org/sqlite"
)

// TestAdapter_Sessions verifies Detect() on the fixture DB returns all root
// sessions with correct provider, token totals, and cost values.
func TestAdapter_Sessions(t *testing.T) {
	dbPath := testdata.NewFixtureDB(t)
	provider := New(filepath.Dir(dbPath))

	summaries, err := provider.Detect("", "")
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	// Fixture has 2 root sessions: fixture-parent-001 and cron_job123_1700000000
	// (fixture-child-001 is filtered out because it has a parent_session_id).
	if len(summaries) < 2 {
		t.Fatalf("Detect() returned %d sessions, want >= 2", len(summaries))
	}
	for _, s := range summaries {
		if s.Provider != session.ProviderHermes {
			t.Errorf("session %q provider = %q, want %q", s.ID, s.Provider, session.ProviderHermes)
		}
	}

	var parent *session.Summary
	for i := range summaries {
		if summaries[i].ID == "fixture-parent-001" {
			parent = &summaries[i]
			break
		}
	}
	if parent == nil {
		t.Fatalf("fixture-parent-001 not found in Detect() results; got: %v", summaryIDs(summaries))
	}
	// input=1000 + output=500 + cache_read=0 + cache_write=0 + reasoning=0
	if parent.TotalTokens != 1500 {
		t.Errorf("parent TotalTokens = %d, want 1500", parent.TotalTokens)
	}
	if parent.EstimatedCost != 0.05 {
		t.Errorf("parent EstimatedCost = %f, want 0.05", parent.EstimatedCost)
	}
	if parent.ActualCost != 0.045 {
		t.Errorf("parent ActualCost = %f, want 0.045", parent.ActualCost)
	}
}

// TestAdapter_Messages verifies Export() loads messages and correctly applies
// tool-call mapping (Name == "delegate_task") and sentinel prefix stripping.
func TestAdapter_Messages(t *testing.T) {
	dbPath := testdata.NewFixtureDB(t)
	provider := New(filepath.Dir(dbPath))

	sess, err := provider.Export(session.ID("fixture-parent-001"), session.StorageModeCompact)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if len(sess.Messages) < 2 {
		t.Fatalf("messages count = %d, want >= 2", len(sess.Messages))
	}

	// Locate the delegate_task tool-call message.
	var delegateMsg *session.Message
	for i := range sess.Messages {
		if len(sess.Messages[i].ToolCalls) > 0 && sess.Messages[i].ToolCalls[0].Name == "delegate_task" {
			delegateMsg = &sess.Messages[i]
			break
		}
	}
	if delegateMsg == nil {
		t.Fatalf("no message with ToolCalls[0].Name == \"delegate_task\" found")
	}
	if delegateMsg.ToolCalls[0].Name != "delegate_task" {
		t.Errorf("tool call name = %q, want \"delegate_task\"", delegateMsg.ToolCalls[0].Name)
	}

	// Locate the sentinel message and verify the \x00\x01 prefix is stripped.
	var sentinelMsg *session.Message
	for i := range sess.Messages {
		if strings.Contains(sess.Messages[i].Content, "hello from sentinel") {
			sentinelMsg = &sess.Messages[i]
			break
		}
	}
	if sentinelMsg == nil {
		t.Fatalf("sentinel message not found in %d messages", len(sess.Messages))
	}
	if len(sentinelMsg.Content) >= 2 && sentinelMsg.Content[0] == '\x00' && sentinelMsg.Content[1] == '\x01' {
		t.Errorf("sentinel prefix still present in mapped content: %q", sentinelMsg.Content[:4])
	}
}

// TestAdapter_ChildLineage verifies Export() on a parent session populates
// Children with the correct count and ParentID back-reference.
func TestAdapter_ChildLineage(t *testing.T) {
	dbPath := testdata.NewFixtureDB(t)
	provider := New(filepath.Dir(dbPath))

	sess, err := provider.Export(session.ID("fixture-parent-001"), session.StorageModeCompact)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if len(sess.Children) != 1 {
		t.Fatalf("children count = %d, want 1", len(sess.Children))
	}
	if sess.Children[0].ID != "fixture-child-001" {
		t.Errorf("child ID = %q, want \"fixture-child-001\"", sess.Children[0].ID)
	}
	if sess.Children[0].ParentID != "fixture-parent-001" {
		t.Errorf("child ParentID = %q, want \"fixture-parent-001\"", sess.Children[0].ParentID)
	}
}

// TestAdapter_NullTolerant verifies Export() handles rows with NULL
// estimated_cost_usd and actual_cost_usd without panicking and maps them to 0.
func TestAdapter_NullTolerant(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("opening null-tolerant fixture db: %v", err)
	}
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
		`CREATE TABLE compression_locks (id TEXT PRIMARY KEY, locked_at REAL)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("creating null-tolerant schema: %v", err)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO sessions
		 (id, source, user_id, model, started_at, message_count, tool_call_count,
		  input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"null-session-001", "manual", "", "claude-opus-4",
		1700000000.0, 0, 0, 0, 0, 0, 0, 0,
	); err != nil {
		t.Fatalf("inserting null-cost session: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("closing null-tolerant fixture db: %v", err)
	}

	provider := New(dir)

	sess, err := provider.Export(session.ID("null-session-001"), session.StorageModeCompact)
	if err != nil {
		t.Fatalf("Export() on null-cost session error = %v", err)
	}
	if sess.EstimatedCost != 0 {
		t.Errorf("EstimatedCost = %f, want 0 for NULL column", sess.EstimatedCost)
	}
	if sess.ActualCost != 0 {
		t.Errorf("ActualCost = %f, want 0 for NULL column", sess.ActualCost)
	}
}

func summaryIDs(summaries []session.Summary) []session.ID {
	ids := make([]session.ID, len(summaries))
	for i, s := range summaries {
		ids[i] = s.ID
	}
	return ids
}
