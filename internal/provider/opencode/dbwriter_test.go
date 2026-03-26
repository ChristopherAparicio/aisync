package opencode

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// setupTestDBForWriter creates an in-memory SQLite with the OpenCode schema.
func setupTestDBForWriter(t *testing.T) *dbWriter {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening in-memory db: %v", err)
	}

	schema := `
		CREATE TABLE project (
			id TEXT PRIMARY KEY,
			worktree TEXT NOT NULL,
			vcs TEXT,
			name TEXT,
			time_created INTEGER NOT NULL,
			time_updated INTEGER NOT NULL,
			sandboxes TEXT NOT NULL DEFAULT '[]'
		);
		CREATE TABLE session (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			parent_id TEXT,
			slug TEXT NOT NULL DEFAULT '',
			directory TEXT NOT NULL,
			title TEXT NOT NULL,
			version TEXT NOT NULL DEFAULT '1.0.0',
			time_created INTEGER NOT NULL,
			time_updated INTEGER NOT NULL,
			FOREIGN KEY (project_id) REFERENCES project(id) ON DELETE CASCADE
		);
		CREATE INDEX session_project_idx ON session (project_id);
		CREATE INDEX session_parent_idx ON session (parent_id);
		CREATE TABLE message (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL,
			time_updated INTEGER NOT NULL,
			data TEXT NOT NULL,
			FOREIGN KEY (session_id) REFERENCES session(id) ON DELETE CASCADE
		);
		CREATE INDEX message_session_idx ON message (session_id);
		CREATE TABLE part (
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL,
			time_updated INTEGER NOT NULL,
			data TEXT NOT NULL,
			FOREIGN KEY (message_id) REFERENCES message(id) ON DELETE CASCADE
		);
		CREATE INDEX part_message_idx ON part (message_id);
		CREATE INDEX part_session_idx ON part (session_id);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}

	return &dbWriter{db: db}
}

func TestDBWriter_ImportSession_Basic(t *testing.T) {
	dw := setupTestDBForWriter(t)
	defer dw.close()

	now := time.Now()
	sess := &session.Session{
		ID:          "ses_test_001",
		ProjectPath: "/tmp/test/myproject",
		Summary:     "Test session from aisync",
		Agent:       "coder",
		CreatedAt:   now,
		Messages: []session.Message{
			{
				ID:        "msg_001",
				Role:      session.RoleUser,
				Content:   "Hello, can you help me?",
				Timestamp: now,
			},
			{
				ID:           "msg_002",
				Role:         session.RoleAssistant,
				Content:      "Sure, I'd be happy to help!",
				Model:        "claude-opus-4",
				InputTokens:  100,
				OutputTokens: 50,
				Timestamp:    now,
			},
		},
	}

	if err := dw.importSession(sess, "coder"); err != nil {
		t.Fatalf("importSession: %v", err)
	}

	// Verify project was created.
	var projID string
	err := dw.db.QueryRow(`SELECT id FROM project WHERE worktree = ?`, "/tmp/test/myproject").Scan(&projID)
	if err != nil {
		t.Fatalf("project not found: %v", err)
	}

	// Verify session was created.
	var title, directory string
	err = dw.db.QueryRow(`SELECT title, directory FROM session WHERE id = ?`, "ses_test_001").Scan(&title, &directory)
	if err != nil {
		t.Fatalf("session not found: %v", err)
	}
	if title != "Test session from aisync" {
		t.Errorf("title = %q, want %q", title, "Test session from aisync")
	}
	if directory != "/tmp/test/myproject" {
		t.Errorf("directory = %q, want %q", directory, "/tmp/test/myproject")
	}

	// Verify messages were created.
	var msgCount int
	dw.db.QueryRow(`SELECT COUNT(*) FROM message WHERE session_id = ?`, "ses_test_001").Scan(&msgCount)
	if msgCount != 2 {
		t.Errorf("message count = %d, want 2", msgCount)
	}

	// Verify message data JSON has correct role.
	var dataStr string
	dw.db.QueryRow(`SELECT data FROM message WHERE id = ?`, "msg_002").Scan(&dataStr)
	var msgData ocMessage
	if err := json.Unmarshal([]byte(dataStr), &msgData); err != nil {
		t.Fatalf("unmarshalling message data: %v", err)
	}
	if msgData.Role != "assistant" {
		t.Errorf("msg role = %q, want %q", msgData.Role, "assistant")
	}
	if msgData.ModelID != "claude-opus-4" {
		t.Errorf("msg modelID = %q, want %q", msgData.ModelID, "claude-opus-4")
	}

	// Verify parts were created (2 text parts for 2 messages with content).
	var partCount int
	dw.db.QueryRow(`SELECT COUNT(*) FROM part WHERE session_id = ?`, "ses_test_001").Scan(&partCount)
	if partCount != 2 {
		t.Errorf("part count = %d, want 2", partCount)
	}
}

func TestDBWriter_ImportSession_WithToolCalls(t *testing.T) {
	dw := setupTestDBForWriter(t)
	defer dw.close()

	now := time.Now()
	sess := &session.Session{
		ID:          "ses_test_tools",
		ProjectPath: "/tmp/test/tools",
		Summary:     "Session with tool calls",
		Agent:       "coder",
		CreatedAt:   now,
		Messages: []session.Message{
			{
				ID:        "msg_t01",
				Role:      session.RoleUser,
				Content:   "Read file.txt",
				Timestamp: now,
			},
			{
				ID:        "msg_t02",
				Role:      session.RoleAssistant,
				Content:   "Let me read that file.",
				Timestamp: now,
				ToolCalls: []session.ToolCall{
					{
						ID:         "tc_001",
						Name:       "Read",
						Input:      `{"path":"file.txt"}`,
						Output:     "file contents here",
						State:      "completed",
						DurationMs: 150,
					},
				},
			},
		},
	}

	if err := dw.importSession(sess, "coder"); err != nil {
		t.Fatalf("importSession: %v", err)
	}

	// The assistant message has 1 text + 1 tool part = 2.
	// The user message has 1 text part = 1.
	// Total = 3 parts.
	var partCount int
	dw.db.QueryRow(`SELECT COUNT(*) FROM part WHERE session_id = ?`, "ses_test_tools").Scan(&partCount)
	if partCount != 3 {
		t.Errorf("part count = %d, want 3", partCount)
	}

	// Verify tool part data.
	var toolDataStr string
	dw.db.QueryRow(
		`SELECT data FROM part WHERE session_id = ? AND data LIKE '%"type":"tool"%'`,
		"ses_test_tools",
	).Scan(&toolDataStr)

	if toolDataStr == "" {
		t.Fatal("tool part not found")
	}

	var toolPart struct {
		Type  string      `json:"type"`
		Tool  string      `json:"tool"`
		State ocToolState `json:"state"`
	}
	if err := json.Unmarshal([]byte(toolDataStr), &toolPart); err != nil {
		t.Fatalf("unmarshalling tool part: %v", err)
	}
	if toolPart.Tool != "Read" {
		t.Errorf("tool = %q, want %q", toolPart.Tool, "Read")
	}
	if toolPart.State.Status != "completed" {
		t.Errorf("status = %q, want %q", toolPart.State.Status, "completed")
	}
}

func TestDBWriter_ImportSession_Idempotent(t *testing.T) {
	dw := setupTestDBForWriter(t)
	defer dw.close()

	now := time.Now()
	sess := &session.Session{
		ID:          "ses_test_idem",
		ProjectPath: "/tmp/test/idem",
		Summary:     "Idempotent test",
		CreatedAt:   now,
		Messages: []session.Message{
			{
				ID:        "msg_i01",
				Role:      session.RoleUser,
				Content:   "Hello",
				Timestamp: now,
			},
		},
	}

	// Import twice — should not fail.
	if err := dw.importSession(sess, "coder"); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if err := dw.importSession(sess, "coder"); err != nil {
		t.Fatalf("second import: %v", err)
	}

	// Still just one session.
	var sessCount int
	dw.db.QueryRow(`SELECT COUNT(*) FROM session WHERE id = ?`, "ses_test_idem").Scan(&sessCount)
	if sessCount != 1 {
		t.Errorf("session count = %d, want 1", sessCount)
	}
}

func TestDBWriter_ImportSession_ReuseExistingProject(t *testing.T) {
	dw := setupTestDBForWriter(t)
	defer dw.close()

	// Pre-create a project.
	dw.db.Exec(
		`INSERT INTO project (id, worktree, time_created, time_updated, sandboxes)
		 VALUES (?, ?, ?, ?, ?)`,
		"existing_proj", "/tmp/test/existing", time.Now().UnixMilli(), time.Now().UnixMilli(), "[]",
	)

	sess := &session.Session{
		ID:          "ses_test_reuse",
		ProjectPath: "/tmp/test/existing",
		Summary:     "Reuse project",
		CreatedAt:   time.Now(),
		Messages:    []session.Message{},
	}

	if err := dw.importSession(sess, "coder"); err != nil {
		t.Fatalf("importSession: %v", err)
	}

	// Verify session uses the existing project.
	var projID string
	dw.db.QueryRow(`SELECT project_id FROM session WHERE id = ?`, "ses_test_reuse").Scan(&projID)
	if projID != "existing_proj" {
		t.Errorf("project_id = %q, want %q", projID, "existing_proj")
	}

	// No new project should have been created.
	var projCount int
	dw.db.QueryRow(`SELECT COUNT(*) FROM project WHERE worktree = ?`, "/tmp/test/existing").Scan(&projCount)
	if projCount != 1 {
		t.Errorf("project count = %d, want 1", projCount)
	}
}

func TestDBWriter_EnsureProject_CreatesNew(t *testing.T) {
	dw := setupTestDBForWriter(t)
	defer dw.close()

	tx, err := dw.db.Begin()
	if err != nil {
		t.Fatal(err)
	}

	id, err := dw.ensureProject(tx, "/tmp/brand/new")
	if err != nil {
		t.Fatalf("ensureProject: %v", err)
	}
	tx.Commit()

	if id == "" {
		t.Fatal("expected non-empty project ID")
	}

	var worktree string
	dw.db.QueryRow(`SELECT worktree FROM project WHERE id = ?`, id).Scan(&worktree)
	if worktree != "/tmp/brand/new" {
		t.Errorf("worktree = %q, want %q", worktree, "/tmp/brand/new")
	}
}

func TestDBWriter_ImportSession_ConflictingMessageIDs(t *testing.T) {
	dw := setupTestDBForWriter(t)
	defer dw.close()

	now := time.Now()

	// Import original session.
	original := &session.Session{
		ID:          "ses_original",
		ProjectPath: "/tmp/test/conflict",
		Summary:     "Original session",
		CreatedAt:   now,
		Messages: []session.Message{
			{ID: "msg_shared_001", Role: session.RoleUser, Content: "Hello", Timestamp: now},
			{ID: "msg_shared_002", Role: session.RoleAssistant, Content: "Hi", Timestamp: now},
		},
	}
	if err := dw.importSession(original, "coder"); err != nil {
		t.Fatalf("importing original: %v", err)
	}

	// Import a rewind/fork that reuses the same message IDs.
	fork := &session.Session{
		ID:          "ses_fork",
		ProjectPath: "/tmp/test/conflict",
		Summary:     "Forked session (rewind)",
		ParentID:    "ses_original",
		CreatedAt:   now,
		Messages: []session.Message{
			{ID: "msg_shared_001", Role: session.RoleUser, Content: "Hello", Timestamp: now},
			{ID: "msg_shared_002", Role: session.RoleAssistant, Content: "Hi", Timestamp: now},
		},
	}
	if err := dw.importSession(fork, "coder"); err != nil {
		t.Fatalf("importing fork: %v", err)
	}

	// Both sessions should exist.
	var sessCount int
	dw.db.QueryRow(`SELECT COUNT(*) FROM session`).Scan(&sessCount)
	if sessCount != 2 {
		t.Errorf("session count = %d, want 2", sessCount)
	}

	// Original should still have 2 messages.
	var origMsgCount int
	dw.db.QueryRow(`SELECT COUNT(*) FROM message WHERE session_id = ?`, "ses_original").Scan(&origMsgCount)
	if origMsgCount != 2 {
		t.Errorf("original message count = %d, want 2", origMsgCount)
	}

	// Fork should also have 2 messages (with new IDs).
	var forkMsgCount int
	dw.db.QueryRow(`SELECT COUNT(*) FROM message WHERE session_id = ?`, "ses_fork").Scan(&forkMsgCount)
	if forkMsgCount != 2 {
		t.Errorf("fork message count = %d, want 2", forkMsgCount)
	}

	// Total: 4 messages (2 original + 2 fork with new IDs).
	var totalMsgs int
	dw.db.QueryRow(`SELECT COUNT(*) FROM message`).Scan(&totalMsgs)
	if totalMsgs != 4 {
		t.Errorf("total message count = %d, want 4", totalMsgs)
	}
}
