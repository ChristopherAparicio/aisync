package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestName(t *testing.T) {
	p := New("")
	if p.Name() != session.ProviderOpenCode {
		t.Errorf("Name() = %q, want %q", p.Name(), session.ProviderOpenCode)
	}
}

func TestCanImport(t *testing.T) {
	p := New("")
	if !p.CanImport() {
		t.Error("CanImport() = false, want true")
	}
}

func TestDetect(t *testing.T) {
	dataHome := setupTestDataHome(t)
	p := New(dataHome)

	t.Run("finds sessions for matching project", func(t *testing.T) {
		summaries, err := p.Detect("/tmp/test/myproject", "")
		if err != nil {
			t.Fatalf("Detect() error: %v", err)
		}
		// Should find 1 session (parent only, not child)
		if len(summaries) != 1 {
			t.Fatalf("Detect() returned %d summaries, want 1", len(summaries))
		}
		if summaries[0].ID != "ses_test001" {
			t.Errorf("ID = %q, want %q", summaries[0].ID, "ses_test001")
		}
		if summaries[0].Summary != "Implement hello world" {
			t.Errorf("Summary = %q, want %q", summaries[0].Summary, "Implement hello world")
		}
	})

	t.Run("returns error for unknown project", func(t *testing.T) {
		_, err := p.Detect("/nonexistent/project", "")
		if err == nil {
			t.Error("Detect() should return error for unknown project")
		}
	})
}

func TestExport_fullMode(t *testing.T) {
	dataHome := setupTestDataHome(t)
	p := New(dataHome)

	sess, err := p.Export("ses_test001", session.StorageModeFull)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// Basic metadata
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

	// Should have 2 messages (user + assistant)
	if len(sess.Messages) != 2 {
		t.Fatalf("Messages count = %d, want 2", len(sess.Messages))
	}

	// First message: user
	if sess.Messages[0].Role != session.RoleUser {
		t.Errorf("Messages[0].Role = %q, want %q", sess.Messages[0].Role, session.RoleUser)
	}
	if sess.Messages[0].Content != "Add a hello world function to main.go" {
		t.Errorf("Messages[0].Content = %q, want %q", sess.Messages[0].Content, "Add a hello world function to main.go")
	}

	// Second message: assistant
	if sess.Messages[1].Role != session.RoleAssistant {
		t.Errorf("Messages[1].Role = %q, want %q", sess.Messages[1].Role, session.RoleAssistant)
	}

	// Tool calls on assistant message
	if len(sess.Messages[1].ToolCalls) != 2 {
		t.Fatalf("ToolCalls count = %d, want 2", len(sess.Messages[1].ToolCalls))
	}

	// First tool call: write (completed)
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

	// Second tool call: read (error)
	tc2 := sess.Messages[1].ToolCalls[1]
	if tc2.State != session.ToolStateError {
		t.Errorf("ToolCall[1].State = %q, want %q", tc2.State, session.ToolStateError)
	}

	// Token usage
	if sess.TokenUsage.TotalTokens == 0 {
		t.Error("TotalTokens should not be 0")
	}

	// File changes: write(main.go) + read(missing.go)
	if len(sess.FileChanges) != 2 {
		t.Fatalf("FileChanges count = %d, want 2", len(sess.FileChanges))
	}
	// Verify at least main.go is tracked
	var foundMainGo bool
	for _, fc := range sess.FileChanges {
		if fc.FilePath == "main.go" && fc.ChangeType == session.ChangeCreated {
			foundMainGo = true
		}
	}
	if !foundMainGo {
		t.Error("Expected main.go with ChangeCreated in FileChanges")
	}

	// Child sessions
	if len(sess.Children) != 1 {
		t.Fatalf("Children count = %d, want 1", len(sess.Children))
	}
	if sess.Children[0].ParentID != "ses_test001" {
		t.Errorf("Child.ParentID = %q, want %q", sess.Children[0].ParentID, "ses_test001")
	}
}

func TestExport_summaryMode(t *testing.T) {
	dataHome := setupTestDataHome(t)
	p := New(dataHome)

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
}

func TestExport_sessionNotFound(t *testing.T) {
	dataHome := setupTestDataHome(t)
	p := New(dataHome)

	_, err := p.Export("nonexistent", session.StorageModeFull)
	if err != session.ErrSessionNotFound {
		t.Errorf("Export() error = %v, want ErrSessionNotFound", err)
	}
}

func TestImport_basicSession(t *testing.T) {
	dataHome := t.TempDir()
	p := New(dataHome)

	sess := &session.Session{
		ID:          "ses_import001",
		Provider:    session.ProviderOpenCode,
		Agent:       "coder",
		Summary:     "Test import session",
		ProjectPath: "/tmp/test/import-project",
		StorageMode: session.StorageModeFull,
		CreatedAt:   time.Date(2025, 2, 25, 10, 0, 0, 0, time.UTC),
		Messages: []session.Message{
			{
				ID:        "msg_user01",
				Role:      session.RoleUser,
				Content:   "Write a hello world function",
				Timestamp: time.Date(2025, 2, 25, 10, 0, 0, 0, time.UTC),
			},
			{
				ID:           "msg_asst01",
				Role:         session.RoleAssistant,
				Content:      "I'll create that for you.",
				Model:        "claude-sonnet-4-20250514",
				Timestamp:    time.Date(2025, 2, 25, 10, 0, 1, 0, time.UTC),
				OutputTokens: 200,
				ToolCalls: []session.ToolCall{
					{
						ID:         "tc_001",
						Name:       "write",
						Input:      `{"file_path":"main.go","content":"package main"}`,
						Output:     "File written.",
						State:      session.ToolStateCompleted,
						DurationMs: 300,
					},
				},
			},
		},
		TokenUsage: session.TokenUsage{
			InputTokens:  120,
			OutputTokens: 80,
			TotalTokens:  200,
		},
	}

	err := p.Import(sess)
	if err != nil {
		t.Fatalf("Import() error: %v", err)
	}

	// Verify project file was created
	projectsPath := filepath.Join(dataHome, storageDir, projectDir)
	entries, err := os.ReadDir(projectsPath)
	if err != nil {
		t.Fatalf("Reading projects dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("Project files = %d, want 1", len(entries))
	}

	// Verify project content
	projData, err := os.ReadFile(filepath.Join(projectsPath, entries[0].Name()))
	if err != nil {
		t.Fatalf("Reading project file: %v", err)
	}
	var proj ocProject
	if err := json.Unmarshal(projData, &proj); err != nil {
		t.Fatalf("Parsing project: %v", err)
	}
	if proj.Worktree != "/tmp/test/import-project" {
		t.Errorf("Worktree = %q, want %q", proj.Worktree, "/tmp/test/import-project")
	}

	// Verify session file was created
	sessDir := filepath.Join(dataHome, storageDir, sessionDir, proj.ID)
	sessPath := filepath.Join(sessDir, "ses_import001.json")
	sessData, err := os.ReadFile(sessPath)
	if err != nil {
		t.Fatalf("Reading session file: %v", err)
	}
	var ocSess ocSession
	if err := json.Unmarshal(sessData, &ocSess); err != nil {
		t.Fatalf("Parsing session: %v", err)
	}
	if ocSess.Title != "Test import session" {
		t.Errorf("Title = %q, want %q", ocSess.Title, "Test import session")
	}

	// Verify message files were created
	msgDir := filepath.Join(dataHome, storageDir, messageDir, "ses_import001")
	msgEntries, err := os.ReadDir(msgDir)
	if err != nil {
		t.Fatalf("Reading messages dir: %v", err)
	}
	if len(msgEntries) != 2 {
		t.Fatalf("Message files = %d, want 2", len(msgEntries))
	}

	// Verify parts for assistant message
	partDir := filepath.Join(dataHome, storageDir, partDir, "msg_asst01")
	partEntries, err := os.ReadDir(partDir)
	if err != nil {
		t.Fatalf("Reading parts dir: %v", err)
	}
	// Should have text part + tool part = 2
	if len(partEntries) != 2 {
		t.Fatalf("Part files = %d, want 2", len(partEntries))
	}
}

func TestImport_nilSession(t *testing.T) {
	p := New(t.TempDir())
	err := p.Import(nil)
	if err == nil {
		t.Error("Import(nil) should return error")
	}
}

func TestImport_noProjectPath(t *testing.T) {
	p := New(t.TempDir())
	sess := &session.Session{ID: "test-123"}
	err := p.Import(sess)
	if err == nil {
		t.Error("Import() with empty ProjectPath should return error")
	}
}

func TestImport_reusesExistingProject(t *testing.T) {
	dataHome := setupTestDataHome(t)
	p := New(dataHome)

	// Import a session for a project that already exists in fixtures
	sess := &session.Session{
		ID:          "ses_new001",
		Provider:    session.ProviderOpenCode,
		Agent:       "coder",
		Summary:     "New session for existing project",
		ProjectPath: "/tmp/test/myproject",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "Hello", Timestamp: time.Now()},
		},
	}

	err := p.Import(sess)
	if err != nil {
		t.Fatalf("Import() error: %v", err)
	}

	// The existing project ID should be reused (abc123def456 from fixtures)
	sessDir := filepath.Join(dataHome, storageDir, sessionDir, "abc123def456")
	sessPath := filepath.Join(sessDir, "ses_new001.json")
	if _, err := os.Stat(sessPath); os.IsNotExist(err) {
		t.Errorf("Session file not created under existing project ID")
	}
}

func TestImport_roundTrip(t *testing.T) {
	dataHome := setupTestDataHome(t)
	p := New(dataHome)

	// Export a session
	original, err := p.Export("ses_test001", session.StorageModeFull)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// Import it with a new ID and project path
	importHome := t.TempDir()
	importProvider := New(importHome)

	original.ID = "ses_roundtrip"
	original.ProjectPath = "/tmp/test/roundtrip"

	err = importProvider.Import(original)
	if err != nil {
		t.Fatalf("Import() error: %v", err)
	}

	// Verify we can detect it
	summaries, err := importProvider.Detect("/tmp/test/roundtrip", "")
	if err != nil {
		t.Fatalf("Detect() error: %v", err)
	}

	// Should find the main session (children have parentID, so they're filtered)
	found := false
	for _, s := range summaries {
		if s.ID == "ses_roundtrip" {
			found = true
			if s.Summary != original.Summary {
				t.Errorf("Summary = %q, want %q", s.Summary, original.Summary)
			}
		}
	}
	if !found {
		t.Error("Imported session not found via Detect()")
	}
}

// setupTestDataHome creates a temporary OpenCode data directory with test fixtures.
func setupTestDataHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Create directory structure
	dirs := []string{
		filepath.Join(dir, storageDir, projectDir),
		filepath.Join(dir, storageDir, sessionDir, "abc123def456"),
		filepath.Join(dir, storageDir, messageDir, "ses_test001"),
		filepath.Join(dir, storageDir, partDir, "msg_user01"),
		filepath.Join(dir, storageDir, partDir, "msg_asst01"),
		// Child session
		filepath.Join(dir, storageDir, messageDir, "ses_test002"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("MkdirAll error: %v", err)
		}
	}

	// Copy fixtures
	fixtures := []struct {
		src string
		dst string
	}{
		{"testdata/project.json", filepath.Join(dir, storageDir, projectDir, "abc123def456.json")},
		{"testdata/session.json", filepath.Join(dir, storageDir, sessionDir, "abc123def456", "ses_test001.json")},
		{"testdata/session_child.json", filepath.Join(dir, storageDir, sessionDir, "abc123def456", "ses_test002.json")},
		{"testdata/msg_user.json", filepath.Join(dir, storageDir, messageDir, "ses_test001", "msg_user01.json")},
		{"testdata/msg_assistant.json", filepath.Join(dir, storageDir, messageDir, "ses_test001", "msg_asst01.json")},
		{"testdata/prt_text_user.json", filepath.Join(dir, storageDir, partDir, "msg_user01", "prt_text01.json")},
		{"testdata/prt_text_asst.json", filepath.Join(dir, storageDir, partDir, "msg_asst01", "prt_text02.json")},
		{"testdata/prt_tool.json", filepath.Join(dir, storageDir, partDir, "msg_asst01", "prt_tool01.json")},
		{"testdata/prt_tool_error.json", filepath.Join(dir, storageDir, partDir, "msg_asst01", "prt_tool02.json")},
	}

	for _, f := range fixtures {
		data, err := os.ReadFile(f.src)
		if err != nil {
			t.Fatalf("ReadFile(%s) error: %v", f.src, err)
		}
		if err := os.WriteFile(f.dst, data, 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error: %v", f.dst, err)
		}
	}

	return dir
}

// ---------------------------------------------------------------------------
// E2E smoke tests — run against real OpenCode data on the host machine.
// Skipped if no real OpenCode DB exists (CI, containers).
// These validate the full pipeline against the production DB format.
// ---------------------------------------------------------------------------

func findRealOpenCodeSession(t *testing.T) (provider *Provider, sessionID string, projectPath string) {
	t.Helper()

	// Try default data home.
	p := New("")
	if p.reader == nil {
		t.Skip("no OpenCode installation found")
	}

	// Check if using DB reader (real DB).
	if _, ok := p.reader.(*dbReader); !ok {
		t.Skip("OpenCode DB not available, only file reader")
	}

	// Discover all projects and find one with sessions.
	projects, err := p.ListAllProjects()
	if err != nil || len(projects) == 0 {
		t.Skip("no OpenCode projects found")
	}

	// Find a project with sessions.
	for _, proj := range projects {
		if proj.SessionCount == 0 {
			continue
		}
		summaries, err := p.Detect(proj.Path, "")
		if err != nil || len(summaries) == 0 {
			continue
		}
		return p, string(summaries[0].ID), proj.Path
	}

	t.Skip("no OpenCode sessions found")
	return nil, "", ""
}

func TestE2E_realOpenCodeSession_Export(t *testing.T) {
	p, sid, _ := findRealOpenCodeSession(t)

	sess, err := p.Export(session.ID(sid), session.StorageModeFull)
	if err != nil {
		t.Fatalf("Export(%s) error: %v", sid, err)
	}

	// Basic sanity.
	if sess.Provider != session.ProviderOpenCode {
		t.Errorf("Provider = %q, want %q", sess.Provider, session.ProviderOpenCode)
	}
	if len(sess.Messages) == 0 {
		t.Fatal("Export returned 0 messages from a real DB session")
	}

	// Every message should have a role.
	for i, m := range sess.Messages {
		if m.Role == "" {
			t.Errorf("Messages[%d].Role is empty", i)
		}
	}

	// At least one user message.
	var hasUser bool
	for _, m := range sess.Messages {
		if m.Role == session.RoleUser {
			hasUser = true
			break
		}
	}
	if !hasUser {
		t.Error("no user message found in real session")
	}

	t.Logf("session %s: %d messages, %d input tokens, %d output tokens, %d file changes, %d children",
		sid, len(sess.Messages), sess.TokenUsage.InputTokens,
		sess.TokenUsage.OutputTokens, len(sess.FileChanges), len(sess.Children))
}

func TestE2E_realOpenCodeSession_Freshness(t *testing.T) {
	p, sid, _ := findRealOpenCodeSession(t)

	freshness, err := p.SessionFreshness(session.ID(sid))
	if err != nil {
		t.Fatalf("SessionFreshness(%s) error: %v", sid, err)
	}

	if freshness.MessageCount <= 0 {
		t.Errorf("MessageCount = %d, want > 0", freshness.MessageCount)
	}
	if freshness.UpdatedAt <= 0 {
		t.Errorf("UpdatedAt = %d, want > 0", freshness.UpdatedAt)
	}

	// Stable across calls.
	freshness2, err := p.SessionFreshness(session.ID(sid))
	if err != nil {
		t.Fatalf("second SessionFreshness() error: %v", err)
	}
	if freshness.MessageCount != freshness2.MessageCount {
		t.Errorf("MessageCount not stable: %d vs %d",
			freshness.MessageCount, freshness2.MessageCount)
	}

	t.Logf("session %s: freshness MessageCount=%d, UpdatedAt=%d",
		sid, freshness.MessageCount, freshness.UpdatedAt)
}

func TestE2E_realOpenCodeSession_ExportIncremental(t *testing.T) {
	p, sid, _ := findRealOpenCodeSession(t)

	// First: full export to get message count.
	full, err := p.Export(session.ID(sid), session.StorageModeFull)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}
	if len(full.Messages) < 2 {
		t.Skip("session has fewer than 2 messages, can't test incremental")
	}

	// Incremental from offset 0 should match full.
	incr0, err := p.ExportIncremental(session.ID(sid), 0, session.StorageModeFull)
	if err != nil {
		t.Fatalf("ExportIncremental(offset=0) error: %v", err)
	}
	if len(incr0.NewMessages) != len(full.Messages) {
		t.Errorf("offset=0: got %d messages, full has %d",
			len(incr0.NewMessages), len(full.Messages))
	}

	// Incremental from half should return the other half.
	half := len(full.Messages) / 2
	incrHalf, err := p.ExportIncremental(session.ID(sid), half, session.StorageModeFull)
	if err != nil {
		t.Fatalf("ExportIncremental(offset=%d) error: %v", half, err)
	}
	expectedNew := len(full.Messages) - half
	if len(incrHalf.NewMessages) != expectedNew {
		t.Errorf("offset=%d: got %d messages, want %d",
			half, len(incrHalf.NewMessages), expectedNew)
	}

	// Token totals should be the same regardless of offset (full recomputation).
	if incr0.TokenUsage.InputTokens != incrHalf.TokenUsage.InputTokens {
		t.Errorf("InputTokens differ: offset0=%d, offsetHalf=%d",
			incr0.TokenUsage.InputTokens, incrHalf.TokenUsage.InputTokens)
	}

	// offset == message count → no new messages.
	_, err = p.ExportIncremental(session.ID(sid), len(full.Messages), session.StorageModeFull)
	if err == nil {
		t.Error("ExportIncremental(offset=all) should return error")
	}

	t.Logf("session %s: full=%d, incr(0)=%d, incr(%d)=%d",
		sid, len(full.Messages), len(incr0.NewMessages),
		half, len(incrHalf.NewMessages))
}

func TestE2E_realOpenCodeSession_FreshnessVsExport(t *testing.T) {
	p, sid, _ := findRealOpenCodeSession(t)

	freshness, err := p.SessionFreshness(session.ID(sid))
	if err != nil {
		t.Fatalf("SessionFreshness() error: %v", err)
	}

	sess, err := p.Export(session.ID(sid), session.StorageModeFull)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// For OpenCode, freshness MessageCount should exactly match Export messages
	// (unlike Claude where JSONL lines may merge).
	if freshness.MessageCount != len(sess.Messages) {
		t.Errorf("freshness MessageCount=%d, export Messages=%d — should match for OpenCode",
			freshness.MessageCount, len(sess.Messages))
	}

	t.Logf("session %s: freshness=%d, export=%d",
		sid, freshness.MessageCount, len(sess.Messages))
}
