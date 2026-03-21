package opencode

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// setupTestDB creates an in-memory SQLite database with the OpenCode schema
// and populates it with test fixtures matching the file-based test data.
func setupTestDB(t *testing.T) *dbReader {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening in-memory db: %v", err)
	}

	// Create the schema matching OpenCode's production database.
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

	// Insert test project.
	if _, err := db.Exec(
		`INSERT INTO project (id, worktree, time_created, time_updated, sandboxes)
		 VALUES (?, ?, ?, ?, ?)`,
		"abc123def456", "/tmp/test/myproject", 1771245757000, 1771245757000, "[]",
	); err != nil {
		t.Fatalf("inserting project: %v", err)
	}

	// Insert parent session.
	if _, err := db.Exec(
		`INSERT INTO session (id, project_id, parent_id, slug, directory, title, version, time_created, time_updated)
		 VALUES (?, ?, NULL, ?, ?, ?, ?, ?, ?)`,
		"ses_test001", "abc123def456", "clever-knight", "/tmp/test/myproject",
		"Implement hello world", "1.1.36", 1771245757992, 1771255877946,
	); err != nil {
		t.Fatalf("inserting session: %v", err)
	}

	// Insert child session.
	if _, err := db.Exec(
		`INSERT INTO session (id, project_id, parent_id, slug, directory, title, version, time_created, time_updated)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"ses_test002", "abc123def456", "ses_test001", "child-slug", "/tmp/test/myproject",
		"Sub-agent task", "1.0.0", 1771246000000, 1771246100000,
	); err != nil {
		t.Fatalf("inserting child session: %v", err)
	}

	// Insert user message (data JSON matches OpenCode's format — no ID in JSON).
	userMsgData := mustJSON(t, map[string]interface{}{
		"role":  "user",
		"agent": "build",
		"time": map[string]interface{}{
			"created": 1771245758000,
		},
		"model": map[string]interface{}{
			"providerID": "anthropic",
			"modelID":    "claude-opus-4-6",
		},
		"tokens": map[string]interface{}{
			"input":     0,
			"output":    0,
			"reasoning": 0,
			"cache":     map[string]interface{}{"read": 0, "write": 0},
		},
	})
	if _, err := db.Exec(
		`INSERT INTO message (id, session_id, time_created, time_updated, data)
		 VALUES (?, ?, ?, ?, ?)`,
		"msg_user01", "ses_test001", 1771245758000, 1771245758000, userMsgData,
	); err != nil {
		t.Fatalf("inserting user message: %v", err)
	}

	// Insert assistant message with tokens, cost, and providerID.
	asstMsgData := mustJSON(t, map[string]interface{}{
		"role":       "assistant",
		"agent":      "build",
		"modelID":    "claude-opus-4-6",
		"providerID": "anthropic",
		"cost":       0.05,
		"time": map[string]interface{}{
			"created":   1771245758049,
			"completed": 1771247994872,
		},
		"model": map[string]interface{}{
			"providerID": "anthropic",
			"modelID":    "claude-opus-4-6",
		},
		"tokens": map[string]interface{}{
			"input":     100,
			"output":    250,
			"reasoning": 0,
			"cache": map[string]interface{}{
				"read":  500,
				"write": 200,
			},
		},
	})
	if _, err := db.Exec(
		`INSERT INTO message (id, session_id, time_created, time_updated, data)
		 VALUES (?, ?, ?, ?, ?)`,
		"msg_asst01", "ses_test001", 1771245758049, 1771247994872, asstMsgData,
	); err != nil {
		t.Fatalf("inserting assistant message: %v", err)
	}

	// Insert parts for user message.
	userTextPartData := mustJSON(t, map[string]interface{}{
		"type": "text",
		"text": "Add a hello world function to main.go",
	})
	if _, err := db.Exec(
		`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"prt_text01", "msg_user01", "ses_test001", 1771245758000, 1771245758000, userTextPartData,
	); err != nil {
		t.Fatalf("inserting user text part: %v", err)
	}

	// Insert parts for assistant message: text + tool (write) + tool (read/error).
	asstTextPartData := mustJSON(t, map[string]interface{}{
		"type": "text",
		"text": "I'll create a hello world function for you.",
	})
	if _, err := db.Exec(
		`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"prt_text02", "msg_asst01", "ses_test001", 1771245758100, 1771245758100, asstTextPartData,
	); err != nil {
		t.Fatalf("inserting assistant text part: %v", err)
	}

	toolWritePartData := mustJSON(t, map[string]interface{}{
		"type":   "tool",
		"callID": "call_write01",
		"tool":   "write",
		"state": map[string]interface{}{
			"status": "completed",
			"input":  map[string]interface{}{"file_path": "main.go", "content": "package main\nfunc HelloWorld() {}"},
			"output": "File written successfully",
			"time": map[string]interface{}{
				"start": 1771245759000,
				"end":   1771245759500,
			},
		},
	})
	if _, err := db.Exec(
		`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"prt_tool01", "msg_asst01", "ses_test001", 1771245759000, 1771245759500, toolWritePartData,
	); err != nil {
		t.Fatalf("inserting write tool part: %v", err)
	}

	toolReadPartData := mustJSON(t, map[string]interface{}{
		"type":   "tool",
		"callID": "call_read01",
		"tool":   "read",
		"state": map[string]interface{}{
			"status": "error",
			"input":  map[string]interface{}{"file_path": "missing.go"},
			"output": "file not found",
			"time": map[string]interface{}{
				"start": 1771245760000,
				"end":   1771245760100,
			},
		},
	})
	if _, err := db.Exec(
		`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"prt_tool02", "msg_asst01", "ses_test001", 1771245760000, 1771245760100, toolReadPartData,
	); err != nil {
		t.Fatalf("inserting read tool part: %v", err)
	}

	t.Cleanup(func() { db.Close() })
	return &dbReader{db: db}
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshalling JSON: %v", err)
	}
	return string(data)
}

func TestDBReader_findProjectID(t *testing.T) {
	r := setupTestDB(t)

	t.Run("finds existing project", func(t *testing.T) {
		id, err := r.findProjectID("/tmp/test/myproject")
		if err != nil {
			t.Fatalf("findProjectID() error: %v", err)
		}
		if id != "abc123def456" {
			t.Errorf("findProjectID() = %q, want %q", id, "abc123def456")
		}
	})

	t.Run("returns error for unknown worktree", func(t *testing.T) {
		_, err := r.findProjectID("/nonexistent/path")
		if err != session.ErrSessionNotFound {
			t.Errorf("findProjectID() error = %v, want ErrSessionNotFound", err)
		}
	})
}

func TestDBReader_listSessions(t *testing.T) {
	r := setupTestDB(t)

	sessions, err := r.listSessions("abc123def456")
	if err != nil {
		t.Fatalf("listSessions() error: %v", err)
	}

	// Should return both parent and child sessions (filtering is done by Provider).
	if len(sessions) != 2 {
		t.Fatalf("listSessions() returned %d sessions, want 2", len(sessions))
	}

	// Verify parent session metadata.
	var parent *ocSession
	for i := range sessions {
		if sessions[i].ID == "ses_test001" {
			parent = &sessions[i]
			break
		}
	}
	if parent == nil {
		t.Fatal("parent session ses_test001 not found")
	}
	if parent.Title != "Implement hello world" {
		t.Errorf("Title = %q, want %q", parent.Title, "Implement hello world")
	}
	if parent.Directory != "/tmp/test/myproject" {
		t.Errorf("Directory = %q, want %q", parent.Directory, "/tmp/test/myproject")
	}
	if parent.ParentID != "" {
		t.Errorf("ParentID = %q, want empty", parent.ParentID)
	}
	if parent.Time.Created != 1771245757992 {
		t.Errorf("Time.Created = %d, want %d", parent.Time.Created, 1771245757992)
	}
}

func TestDBReader_listSessions_unknownProject(t *testing.T) {
	r := setupTestDB(t)

	sessions, err := r.listSessions("nonexistent")
	if err != nil {
		t.Fatalf("listSessions() error: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("listSessions() returned %d sessions, want 0", len(sessions))
	}
}

func TestDBReader_readSession(t *testing.T) {
	r := setupTestDB(t)

	t.Run("reads existing session", func(t *testing.T) {
		sess, err := r.readSession("ses_test001")
		if err != nil {
			t.Fatalf("readSession() error: %v", err)
		}
		if sess.ID != "ses_test001" {
			t.Errorf("ID = %q, want %q", sess.ID, "ses_test001")
		}
		if sess.Title != "Implement hello world" {
			t.Errorf("Title = %q, want %q", sess.Title, "Implement hello world")
		}
		if sess.ProjectID != "abc123def456" {
			t.Errorf("ProjectID = %q, want %q", sess.ProjectID, "abc123def456")
		}
	})

	t.Run("returns error for nonexistent session", func(t *testing.T) {
		_, err := r.readSession("nonexistent")
		if err != session.ErrSessionNotFound {
			t.Errorf("readSession() error = %v, want ErrSessionNotFound", err)
		}
	})
}

func TestDBReader_loadMessages(t *testing.T) {
	r := setupTestDB(t)

	messages, err := r.loadMessages("ses_test001")
	if err != nil {
		t.Fatalf("loadMessages() error: %v", err)
	}

	if len(messages) != 2 {
		t.Fatalf("loadMessages() returned %d messages, want 2", len(messages))
	}

	// Messages should be ordered by time_created ASC.
	if messages[0].ID != "msg_user01" {
		t.Errorf("messages[0].ID = %q, want %q", messages[0].ID, "msg_user01")
	}
	if messages[1].ID != "msg_asst01" {
		t.Errorf("messages[1].ID = %q, want %q", messages[1].ID, "msg_asst01")
	}

	// Verify user message fields.
	userMsg := messages[0]
	if userMsg.Role != "user" {
		t.Errorf("userMsg.Role = %q, want %q", userMsg.Role, "user")
	}
	if userMsg.Agent != "build" {
		t.Errorf("userMsg.Agent = %q, want %q", userMsg.Agent, "build")
	}

	// Verify assistant message fields (from JSON data).
	asstMsg := messages[1]
	if asstMsg.Role != "assistant" {
		t.Errorf("asstMsg.Role = %q, want %q", asstMsg.Role, "assistant")
	}
	if asstMsg.ModelID != "claude-opus-4-6" {
		t.Errorf("asstMsg.ModelID = %q, want %q", asstMsg.ModelID, "claude-opus-4-6")
	}
	if asstMsg.ProviderID != "anthropic" {
		t.Errorf("asstMsg.ProviderID = %q, want %q", asstMsg.ProviderID, "anthropic")
	}
	if asstMsg.Cost != 0.05 {
		t.Errorf("asstMsg.Cost = %f, want %f", asstMsg.Cost, 0.05)
	}
	if asstMsg.Tokens.Input != 100 {
		t.Errorf("asstMsg.Tokens.Input = %d, want %d", asstMsg.Tokens.Input, 100)
	}
	if asstMsg.Tokens.Output != 250 {
		t.Errorf("asstMsg.Tokens.Output = %d, want %d", asstMsg.Tokens.Output, 250)
	}
	if asstMsg.Tokens.Cache.Read != 500 {
		t.Errorf("asstMsg.Tokens.Cache.Read = %d, want %d", asstMsg.Tokens.Cache.Read, 500)
	}
	if asstMsg.Tokens.Cache.Write != 200 {
		t.Errorf("asstMsg.Tokens.Cache.Write = %d, want %d", asstMsg.Tokens.Cache.Write, 200)
	}
}

func TestDBReader_loadMessages_emptySession(t *testing.T) {
	r := setupTestDB(t)

	messages, err := r.loadMessages("ses_test002")
	if err != nil {
		t.Fatalf("loadMessages() error: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("loadMessages() returned %d messages, want 0", len(messages))
	}
}

func TestDBReader_loadParts(t *testing.T) {
	r := setupTestDB(t)

	t.Run("user message has 1 text part", func(t *testing.T) {
		parts, err := r.loadParts("msg_user01")
		if err != nil {
			t.Fatalf("loadParts() error: %v", err)
		}
		if len(parts) != 1 {
			t.Fatalf("loadParts() returned %d parts, want 1", len(parts))
		}
		if parts[0].Type != "text" {
			t.Errorf("parts[0].Type = %q, want %q", parts[0].Type, "text")
		}
		if parts[0].Text != "Add a hello world function to main.go" {
			t.Errorf("parts[0].Text = %q, want %q", parts[0].Text, "Add a hello world function to main.go")
		}
		// Verify table columns are set.
		if parts[0].ID != "prt_text01" {
			t.Errorf("parts[0].ID = %q, want %q", parts[0].ID, "prt_text01")
		}
		if parts[0].MessageID != "msg_user01" {
			t.Errorf("parts[0].MessageID = %q, want %q", parts[0].MessageID, "msg_user01")
		}
		if parts[0].SessionID != "ses_test001" {
			t.Errorf("parts[0].SessionID = %q, want %q", parts[0].SessionID, "ses_test001")
		}
	})

	t.Run("assistant message has text + 2 tool parts", func(t *testing.T) {
		parts, err := r.loadParts("msg_asst01")
		if err != nil {
			t.Fatalf("loadParts() error: %v", err)
		}
		if len(parts) != 3 {
			t.Fatalf("loadParts() returned %d parts, want 3", len(parts))
		}

		// First part: text.
		if parts[0].Type != "text" {
			t.Errorf("parts[0].Type = %q, want %q", parts[0].Type, "text")
		}

		// Second part: write tool (completed).
		if parts[1].Type != "tool" {
			t.Errorf("parts[1].Type = %q, want %q", parts[1].Type, "tool")
		}
		if parts[1].Tool != "write" {
			t.Errorf("parts[1].Tool = %q, want %q", parts[1].Tool, "write")
		}
		if parts[1].State.Status != "completed" {
			t.Errorf("parts[1].State.Status = %q, want %q", parts[1].State.Status, "completed")
		}
		if parts[1].CallID != "call_write01" {
			t.Errorf("parts[1].CallID = %q, want %q", parts[1].CallID, "call_write01")
		}

		// Third part: read tool (error).
		if parts[2].Type != "tool" {
			t.Errorf("parts[2].Type = %q, want %q", parts[2].Type, "tool")
		}
		if parts[2].Tool != "read" {
			t.Errorf("parts[2].Tool = %q, want %q", parts[2].Tool, "read")
		}
		if parts[2].State.Status != "error" {
			t.Errorf("parts[2].State.Status = %q, want %q", parts[2].State.Status, "error")
		}
	})

	t.Run("returns empty for unknown message", func(t *testing.T) {
		parts, err := r.loadParts("nonexistent")
		if err != nil {
			t.Fatalf("loadParts() error: %v", err)
		}
		if len(parts) != 0 {
			t.Errorf("loadParts() returned %d parts, want 0", len(parts))
		}
	})
}

func TestDBReader_countMessages(t *testing.T) {
	r := setupTestDB(t)

	count := r.countMessages("ses_test001")
	if count != 2 {
		t.Errorf("countMessages() = %d, want 2", count)
	}

	count = r.countMessages("ses_test002")
	if count != 0 {
		t.Errorf("countMessages() for child = %d, want 0", count)
	}

	count = r.countMessages("nonexistent")
	if count != 0 {
		t.Errorf("countMessages() for nonexistent = %d, want 0", count)
	}
}

func TestDBReader_findChildSessions(t *testing.T) {
	r := setupTestDB(t)

	t.Run("finds child sessions", func(t *testing.T) {
		children, err := r.findChildSessions("ses_test001")
		if err != nil {
			t.Fatalf("findChildSessions() error: %v", err)
		}
		if len(children) != 1 {
			t.Fatalf("findChildSessions() returned %d children, want 1", len(children))
		}
		if children[0].ID != "ses_test002" {
			t.Errorf("child.ID = %q, want %q", children[0].ID, "ses_test002")
		}
		if children[0].ParentID != "ses_test001" {
			t.Errorf("child.ParentID = %q, want %q", children[0].ParentID, "ses_test001")
		}
		if children[0].Title != "Sub-agent task" {
			t.Errorf("child.Title = %q, want %q", children[0].Title, "Sub-agent task")
		}
	})

	t.Run("returns empty for session without children", func(t *testing.T) {
		children, err := r.findChildSessions("ses_test002")
		if err != nil {
			t.Fatalf("findChildSessions() error: %v", err)
		}
		if len(children) != 0 {
			t.Errorf("findChildSessions() returned %d children, want 0", len(children))
		}
	})
}

// TestDBReader_fullExportIntegration tests the full Provider pipeline
// using the DB reader backend.
func TestDBReader_fullExportIntegration(t *testing.T) {
	r := setupTestDB(t)

	// Create a Provider that uses the DB reader.
	p := &Provider{
		dataHome: t.TempDir(),
		reader:   r,
	}

	sess, err := p.Export("ses_test001", session.StorageModeFull)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// Basic metadata.
	if sess.Provider != session.ProviderOpenCode {
		t.Errorf("Provider = %q, want %q", sess.Provider, session.ProviderOpenCode)
	}
	if sess.Agent != "build" {
		t.Errorf("Agent = %q, want %q", sess.Agent, "build")
	}
	if sess.Summary != "Implement hello world" {
		t.Errorf("Summary = %q, want %q", sess.Summary, "Implement hello world")
	}
	if sess.ProjectPath != "/tmp/test/myproject" {
		t.Errorf("ProjectPath = %q, want %q", sess.ProjectPath, "/tmp/test/myproject")
	}

	// Should have 2 messages.
	if len(sess.Messages) != 2 {
		t.Fatalf("Messages count = %d, want 2", len(sess.Messages))
	}

	// User message.
	if sess.Messages[0].Role != session.RoleUser {
		t.Errorf("Messages[0].Role = %q, want %q", sess.Messages[0].Role, session.RoleUser)
	}
	if sess.Messages[0].Content != "Add a hello world function to main.go" {
		t.Errorf("Messages[0].Content = %q, want %q", sess.Messages[0].Content, "Add a hello world function to main.go")
	}

	// Assistant message.
	if sess.Messages[1].Role != session.RoleAssistant {
		t.Errorf("Messages[1].Role = %q, want %q", sess.Messages[1].Role, session.RoleAssistant)
	}
	if sess.Messages[1].ProviderID != "anthropic" {
		t.Errorf("Messages[1].ProviderID = %q, want %q", sess.Messages[1].ProviderID, "anthropic")
	}
	if sess.Messages[1].ProviderCost != 0.05 {
		t.Errorf("Messages[1].ProviderCost = %f, want %f", sess.Messages[1].ProviderCost, 0.05)
	}

	// Tool calls on assistant message.
	if len(sess.Messages[1].ToolCalls) != 2 {
		t.Fatalf("ToolCalls count = %d, want 2", len(sess.Messages[1].ToolCalls))
	}

	tc := sess.Messages[1].ToolCalls[0]
	if tc.Name != "write" {
		t.Errorf("ToolCall[0].Name = %q, want %q", tc.Name, "write")
	}
	if tc.State != session.ToolStateCompleted {
		t.Errorf("ToolCall[0].State = %q, want %q", tc.State, session.ToolStateCompleted)
	}
	if tc.DurationMs != 500 {
		t.Errorf("ToolCall[0].DurationMs = %d, want 500", tc.DurationMs)
	}

	tc2 := sess.Messages[1].ToolCalls[1]
	if tc2.Name != "read" {
		t.Errorf("ToolCall[1].Name = %q, want %q", tc2.Name, "read")
	}
	if tc2.State != session.ToolStateError {
		t.Errorf("ToolCall[1].State = %q, want %q", tc2.State, session.ToolStateError)
	}

	// Token usage.
	if sess.TokenUsage.InputTokens != 800 { // 100 + 500 (cache.read) + 200 (cache.write)
		t.Errorf("InputTokens = %d, want 800", sess.TokenUsage.InputTokens)
	}
	if sess.TokenUsage.OutputTokens != 250 {
		t.Errorf("OutputTokens = %d, want 250", sess.TokenUsage.OutputTokens)
	}

	// File changes.
	if len(sess.FileChanges) != 2 {
		t.Fatalf("FileChanges count = %d, want 2", len(sess.FileChanges))
	}
	var foundMainGo bool
	for _, fc := range sess.FileChanges {
		if fc.FilePath == "main.go" && fc.ChangeType == session.ChangeCreated {
			foundMainGo = true
		}
	}
	if !foundMainGo {
		t.Error("Expected main.go with ChangeCreated in FileChanges")
	}

	// Children via DB reader.
	if len(sess.Children) != 1 {
		t.Fatalf("Children count = %d, want 1", len(sess.Children))
	}
	if sess.Children[0].ParentID != "ses_test001" {
		t.Errorf("Child.ParentID = %q, want %q", sess.Children[0].ParentID, "ses_test001")
	}
}

func TestDBReader_fullDetectIntegration(t *testing.T) {
	r := setupTestDB(t)

	p := &Provider{
		dataHome: t.TempDir(),
		reader:   r,
	}

	summaries, err := p.Detect("/tmp/test/myproject", "")
	if err != nil {
		t.Fatalf("Detect() error: %v", err)
	}

	// Should find 1 session (parent only, child is filtered out).
	if len(summaries) != 1 {
		t.Fatalf("Detect() returned %d summaries, want 1", len(summaries))
	}
	if summaries[0].ID != "ses_test001" {
		t.Errorf("ID = %q, want %q", summaries[0].ID, "ses_test001")
	}
	if summaries[0].MessageCount != 2 {
		t.Errorf("MessageCount = %d, want 2", summaries[0].MessageCount)
	}
}

func TestDBReader_summaryModeIntegration(t *testing.T) {
	r := setupTestDB(t)

	p := &Provider{
		dataHome: t.TempDir(),
		reader:   r,
	}

	sess, err := p.Export("ses_test001", session.StorageModeSummary)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	if sess.Summary != "Implement hello world" {
		t.Errorf("Summary = %q, want %q", sess.Summary, "Implement hello world")
	}
	if len(sess.Messages) != 0 {
		t.Errorf("Messages count = %d, want 0 in summary mode", len(sess.Messages))
	}
	// Token usage should still be computed.
	if sess.TokenUsage.TotalTokens == 0 {
		t.Error("TotalTokens should not be 0 in summary mode")
	}
}
