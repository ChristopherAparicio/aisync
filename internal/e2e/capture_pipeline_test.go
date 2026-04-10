// Package e2e contains end-to-end tests for the aisync capture pipeline.
//
// These tests exercise the FULL pipeline:
//
//	provider.Export() → capture.Service → service.SessionService.Capture()
//	→ store.Save() (real SQLite) → store.Get() → verify all fields
//
// Two categories of tests:
//  1. Fixture-based: use test fixtures (always run, even in CI)
//  2. Real-data: use actual provider data on the host (~/.claude, opencode.db)
//     — automatically skipped if no provider data is found
package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/provider/claude"
	"github.com/ChristopherAparicio/aisync/internal/provider/opencode"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
)

// ---------------------------------------------------------------------------
// Fixture-based e2e: Claude Code
// Uses testdata fixtures copied into a temp dir — always runs.
// ---------------------------------------------------------------------------

func TestPipeline_ClaudeCode_FixtureBased(t *testing.T) {
	// Set up a fake Claude home with fixture data.
	claudeHome := setupClaudeFixtures(t)
	prov := claude.New(claudeHome)
	store := testutil.MustOpenStore(t)
	reg := provider.NewRegistry(prov)

	svc := service.NewSessionService(service.SessionServiceConfig{
		Store:    store,
		Registry: reg,
	})

	// Capture with explicit provider and session ID.
	// The fixture has session "a1b2c3d4-1111-2222-3333-444455556666".
	result, err := svc.CaptureByID(service.CaptureRequest{
		ProjectPath:  "/tmp/test/myproject",
		Branch:       "feat/hello-world",
		Mode:         session.StorageModeFull,
		ProviderName: session.ProviderClaudeCode,
	}, "a1b2c3d4-1111-2222-3333-444455556666")
	if err != nil {
		t.Fatalf("CaptureByID() error: %v", err)
	}

	if result.Skipped {
		t.Fatal("first capture should not be skipped")
	}

	// ── Verify the capture result ──
	sess := result.Session
	if sess.ID != "a1b2c3d4-1111-2222-3333-444455556666" {
		t.Errorf("Session.ID = %q, want fixture ID", sess.ID)
	}
	if sess.Provider != session.ProviderClaudeCode {
		t.Errorf("Provider = %q, want %q", sess.Provider, session.ProviderClaudeCode)
	}
	if len(sess.Messages) == 0 {
		t.Fatal("captured session has 0 messages")
	}
	if sess.TokenUsage.TotalTokens == 0 {
		t.Error("TotalTokens = 0 after capture")
	}

	// ── Read back from real SQLite and verify persistence ──
	got, err := store.Get("a1b2c3d4-1111-2222-3333-444455556666")
	if err != nil {
		t.Fatalf("store.Get() error: %v", err)
	}

	// Core identity.
	if got.ID != sess.ID {
		t.Errorf("stored ID = %q, want %q", got.ID, sess.ID)
	}
	if got.Provider != session.ProviderClaudeCode {
		t.Errorf("stored Provider = %q, want %q", got.Provider, session.ProviderClaudeCode)
	}
	if got.Branch != "feat/hello-world" {
		t.Errorf("stored Branch = %q, want %q", got.Branch, "feat/hello-world")
	}
	if got.ProjectPath != "/tmp/test/myproject" {
		t.Errorf("stored ProjectPath = %q, want %q", got.ProjectPath, "/tmp/test/myproject")
	}

	// Messages survived the round-trip.
	if len(got.Messages) != len(sess.Messages) {
		t.Errorf("stored Messages = %d, captured = %d", len(got.Messages), len(sess.Messages))
	}

	// Token usage survived.
	if got.TokenUsage.TotalTokens != sess.TokenUsage.TotalTokens {
		t.Errorf("stored TotalTokens = %d, captured = %d",
			got.TokenUsage.TotalTokens, sess.TokenUsage.TotalTokens)
	}

	// At least one user message with content.
	var foundUser bool
	for _, m := range got.Messages {
		if m.Role == session.RoleUser && m.Content != "" {
			foundUser = true
			break
		}
	}
	if !foundUser {
		t.Error("no user message with content found after round-trip")
	}

	t.Logf("Claude fixture e2e: %d messages, %d total tokens, %d file changes",
		len(got.Messages), got.TokenUsage.TotalTokens, len(got.FileChanges))
}

func TestPipeline_ClaudeCode_RecaptureStoresConsistently(t *testing.T) {
	claudeHome := setupClaudeFixtures(t)
	prov := claude.New(claudeHome)
	store := testutil.MustOpenStore(t)
	reg := provider.NewRegistry(prov)

	svc := service.NewSessionService(service.SessionServiceConfig{
		Store:    store,
		Registry: reg,
	})

	sessionID := session.ID("a1b2c3d4-1111-2222-3333-444455556666")

	// First capture.
	result1, err := svc.CaptureByID(service.CaptureRequest{
		ProjectPath:  "/tmp/test/myproject",
		Branch:       "feat/hello-world",
		Mode:         session.StorageModeFull,
		ProviderName: session.ProviderClaudeCode,
	}, sessionID)
	if err != nil {
		t.Fatalf("first CaptureByID() error: %v", err)
	}
	if result1.Skipped {
		t.Fatal("first capture should not be skipped")
	}

	// Second capture — re-export same data, save should overwrite cleanly.
	result2, err := svc.CaptureByID(service.CaptureRequest{
		ProjectPath:  "/tmp/test/myproject",
		Branch:       "feat/hello-world",
		Mode:         session.StorageModeFull,
		ProviderName: session.ProviderClaudeCode,
	}, sessionID)
	if err != nil {
		t.Fatalf("second CaptureByID() error: %v", err)
	}

	// Verify the session is still correctly stored after a re-capture.
	got, err := store.Get(sessionID)
	if err != nil {
		t.Fatalf("store.Get() after recapture error: %v", err)
	}
	if len(got.Messages) != len(result2.Session.Messages) {
		t.Errorf("stored Messages=%d after recapture, captured=%d",
			len(got.Messages), len(result2.Session.Messages))
	}
}

// ---------------------------------------------------------------------------
// Fixture-based e2e: OpenCode (in-memory SQLite as provider DB)
// ---------------------------------------------------------------------------

func TestPipeline_OpenCode_FixtureBased(t *testing.T) {
	// Set up OpenCode with file-based fixtures.
	dataHome := setupOpenCodeFixtures(t)
	prov := opencode.New(dataHome)
	store := testutil.MustOpenStore(t)
	reg := provider.NewRegistry(prov)

	svc := service.NewSessionService(service.SessionServiceConfig{
		Store:    store,
		Registry: reg,
	})

	result, err := svc.CaptureByID(service.CaptureRequest{
		ProjectPath:  "/tmp/test/myproject",
		Branch:       "main",
		Mode:         session.StorageModeFull,
		ProviderName: session.ProviderOpenCode,
	}, "ses_test001")
	if err != nil {
		t.Fatalf("CaptureByID() error: %v", err)
	}

	sess := result.Session
	if sess.ID != "ses_test001" {
		t.Errorf("Session.ID = %q, want %q", sess.ID, "ses_test001")
	}
	if sess.Provider != session.ProviderOpenCode {
		t.Errorf("Provider = %q, want %q", sess.Provider, session.ProviderOpenCode)
	}
	if len(sess.Messages) == 0 {
		t.Fatal("captured session has 0 messages")
	}

	// ── Read back from real SQLite ──
	got, err := store.Get("ses_test001")
	if err != nil {
		t.Fatalf("store.Get() error: %v", err)
	}

	if got.Provider != session.ProviderOpenCode {
		t.Errorf("stored Provider = %q, want %q", got.Provider, session.ProviderOpenCode)
	}
	if len(got.Messages) != len(sess.Messages) {
		t.Errorf("stored Messages = %d, captured = %d", len(got.Messages), len(sess.Messages))
	}
	if got.TokenUsage.TotalTokens != sess.TokenUsage.TotalTokens {
		t.Errorf("stored TotalTokens = %d, captured = %d",
			got.TokenUsage.TotalTokens, sess.TokenUsage.TotalTokens)
	}

	// Verify children were saved separately (if any exist).
	// Note: CaptureByID saves children in the service layer, not capture layer.
	// Children are stored as separate rows with ParentID set.
	if len(sess.Children) > 0 {
		childID := sess.Children[0].ID
		child, childErr := store.Get(childID)
		if childErr != nil {
			t.Logf("child %s not found in store (may not have been saved by CaptureByID): %v", childID, childErr)
		} else {
			if child.ParentID != "ses_test001" {
				t.Errorf("child.ParentID = %q, want %q", child.ParentID, "ses_test001")
			}
		}
	}

	t.Logf("OpenCode fixture e2e: %d messages, %d total tokens, %d children",
		len(got.Messages), got.TokenUsage.TotalTokens, len(sess.Children))
}

// ---------------------------------------------------------------------------
// Real-data e2e: Claude Code
// Skipped if no real ~/.claude/ exists.
// ---------------------------------------------------------------------------

func TestPipeline_ClaudeCode_RealData(t *testing.T) {
	claudeHome, sessionID, projectPath := findRealClaudeSession(t)

	prov := claude.New(claudeHome)
	store := testutil.MustOpenStore(t)
	reg := provider.NewRegistry(prov)

	svc := service.NewSessionService(service.SessionServiceConfig{
		Store:    store,
		Registry: reg,
	})

	result, err := svc.CaptureByID(service.CaptureRequest{
		ProjectPath:  projectPath,
		Branch:       "", // don't filter by branch
		Mode:         session.StorageModeFull,
		ProviderName: session.ProviderClaudeCode,
	}, session.ID(sessionID))
	if err != nil {
		t.Fatalf("CaptureByID(%s) error: %v", sessionID, err)
	}

	sess := result.Session
	if len(sess.Messages) == 0 {
		t.Fatal("real Claude session captured 0 messages")
	}

	// Read back from SQLite.
	got, err := store.Get(session.ID(sessionID))
	if err != nil {
		t.Fatalf("store.Get(%s) error: %v", sessionID, err)
	}

	if len(got.Messages) != len(sess.Messages) {
		t.Errorf("round-trip message count: stored=%d, captured=%d",
			len(got.Messages), len(sess.Messages))
	}
	if got.TokenUsage.TotalTokens != sess.TokenUsage.TotalTokens {
		t.Errorf("round-trip TotalTokens: stored=%d, captured=%d",
			got.TokenUsage.TotalTokens, sess.TokenUsage.TotalTokens)
	}

	// Verify message content survived compression + decompression.
	for i, m := range got.Messages {
		if m.Role == "" {
			t.Errorf("stored Messages[%d].Role is empty", i)
		}
		if m.Content == "" && len(m.ToolCalls) == 0 && m.Thinking == "" {
			t.Errorf("stored Messages[%d] has no content, tool calls, or thinking", i)
		}
	}

	t.Logf("Real Claude e2e: session=%s, %d msgs, %d tokens, %d files",
		sessionID, len(got.Messages), got.TokenUsage.TotalTokens, len(got.FileChanges))
}

// ---------------------------------------------------------------------------
// Real-data e2e: OpenCode
// Skipped if no real opencode.db exists.
// ---------------------------------------------------------------------------

func TestPipeline_OpenCode_RealData(t *testing.T) {
	ocProvider, sessionID, projectPath := findRealOpenCodeSession(t)

	store := testutil.MustOpenStore(t)
	reg := provider.NewRegistry(ocProvider)

	svc := service.NewSessionService(service.SessionServiceConfig{
		Store:    store,
		Registry: reg,
	})

	result, err := svc.CaptureByID(service.CaptureRequest{
		ProjectPath:  projectPath,
		Branch:       "",
		Mode:         session.StorageModeFull,
		ProviderName: session.ProviderOpenCode,
	}, session.ID(sessionID))
	if err != nil {
		t.Fatalf("CaptureByID(%s) error: %v", sessionID, err)
	}

	sess := result.Session
	if len(sess.Messages) == 0 {
		t.Fatal("real OpenCode session captured 0 messages")
	}

	// Read back from SQLite.
	got, err := store.Get(session.ID(sessionID))
	if err != nil {
		t.Fatalf("store.Get(%s) error: %v", sessionID, err)
	}

	if len(got.Messages) != len(sess.Messages) {
		t.Errorf("round-trip message count: stored=%d, captured=%d",
			len(got.Messages), len(sess.Messages))
	}
	if got.TokenUsage.TotalTokens != sess.TokenUsage.TotalTokens {
		t.Errorf("round-trip TotalTokens: stored=%d, captured=%d",
			got.TokenUsage.TotalTokens, sess.TokenUsage.TotalTokens)
	}

	// Verify freshness is stored and matches.
	storedMsgCount, _, err := store.GetFreshness(session.ID(sessionID))
	if err != nil {
		t.Fatalf("store.GetFreshness() error: %v", err)
	}
	if storedMsgCount != len(got.Messages) {
		t.Errorf("stored freshness MessageCount=%d, Messages=%d",
			storedMsgCount, len(got.Messages))
	}

	t.Logf("Real OpenCode e2e: session=%s, %d msgs, %d tokens, %d children",
		sessionID, len(got.Messages), got.TokenUsage.TotalTokens, len(sess.Children))
}

// ---------------------------------------------------------------------------
// Real-data e2e: OpenCode incremental capture
// Captures a session, then recaptures — incremental path should be used.
// ---------------------------------------------------------------------------

func TestPipeline_OpenCode_RealData_IncrementalRecapture(t *testing.T) {
	ocProvider, sessionID, projectPath := findRealOpenCodeSession(t)

	store := testutil.MustOpenStore(t)
	reg := provider.NewRegistry(ocProvider)

	svc := service.NewSessionService(service.SessionServiceConfig{
		Store:    store,
		Registry: reg,
	})

	req := service.CaptureRequest{
		ProjectPath:  projectPath,
		Branch:       "",
		Mode:         session.StorageModeFull,
		ProviderName: session.ProviderOpenCode,
	}

	// First capture — full export.
	result1, err := svc.CaptureByID(req, session.ID(sessionID))
	if err != nil {
		t.Fatalf("first CaptureByID() error: %v", err)
	}
	if result1.Skipped {
		t.Fatal("first capture should not be skipped")
	}

	// Second capture — should be skipped (freshness unchanged).
	result2, err := svc.CaptureByID(req, session.ID(sessionID))
	if err != nil {
		t.Fatalf("second CaptureByID() error: %v", err)
	}
	if !result2.Skipped {
		t.Error("second capture of unchanged session should be skipped")
	}

	// Verify the stored session is intact after the skip.
	got, err := store.Get(session.ID(sessionID))
	if err != nil {
		t.Fatalf("store.Get() error: %v", err)
	}
	if len(got.Messages) != len(result1.Session.Messages) {
		t.Errorf("stored Messages=%d after skip, want %d",
			len(got.Messages), len(result1.Session.Messages))
	}

	t.Logf("OpenCode incremental e2e: session=%s, first=%d msgs, second skipped=%v",
		sessionID, len(result1.Session.Messages), result2.Skipped)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// setupClaudeFixtures creates a temp dir mimicking ~/.claude with fixture data.
func setupClaudeFixtures(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Mirror the Claude Code directory structure.
	projDir := filepath.Join(dir, "projects",
		"-tmp-test-myproject") // encodeProjectPath("/tmp/test/myproject")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("MkdirAll error: %v", err)
	}

	// Copy fixture files from the Claude provider's testdata.
	srcDir := filepath.Join("..", "provider", "claude", "testdata")
	fixtures := []struct{ src, dst string }{
		{"sessions-index.json", filepath.Join(projDir, "sessions-index.json")},
		{"session_simple.jsonl", filepath.Join(projDir, "a1b2c3d4-1111-2222-3333-444455556666.jsonl")},
		{"session_with_error.jsonl", filepath.Join(projDir, "c3d4e5f6-3333-4444-5555-666677778888.jsonl")},
	}
	for _, f := range fixtures {
		data, err := os.ReadFile(filepath.Join(srcDir, f.src))
		if err != nil {
			t.Fatalf("ReadFile(%s) error: %v", f.src, err)
		}
		if err := os.WriteFile(f.dst, data, 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error: %v", f.dst, err)
		}
	}

	return dir
}

// setupOpenCodeFixtures creates a temp dir mimicking OpenCode's file-based storage.
func setupOpenCodeFixtures(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	srcDir := filepath.Join("..", "provider", "opencode", "testdata")

	// OpenCode file layout: storage/{type}/{id}.json
	type fixture struct {
		src    string
		subdir string
		name   string
	}

	dirs := []string{
		"storage/project",
		"storage/session/abc123def456",
		"storage/message/ses_test001",
		"storage/message/ses_test002",
		"storage/part/msg_user01",
		"storage/part/msg_asst01",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatalf("MkdirAll error: %v", err)
		}
	}

	files := []fixture{
		{"project.json", "storage/project", "abc123def456.json"},
		{"session.json", "storage/session/abc123def456", "ses_test001.json"},
		{"session_child.json", "storage/session/abc123def456", "ses_test002.json"},
		{"msg_user.json", "storage/message/ses_test001", "msg_user01.json"},
		{"msg_assistant.json", "storage/message/ses_test001", "msg_asst01.json"},
		{"prt_text_user.json", "storage/part/msg_user01", "prt_text01.json"},
		{"prt_text_asst.json", "storage/part/msg_asst01", "prt_text02.json"},
		{"prt_tool.json", "storage/part/msg_asst01", "prt_tool01.json"},
		{"prt_tool_error.json", "storage/part/msg_asst01", "prt_tool02.json"},
	}

	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(srcDir, f.src))
		if err != nil {
			t.Fatalf("ReadFile(%s) error: %v", f.src, err)
		}
		dst := filepath.Join(dir, f.subdir, f.name)
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error: %v", dst, err)
		}
	}

	return dir
}

// findRealClaudeSession finds a real Claude Code session on the host machine.
// Returns the claude home, a session ID, and the project path that Detect() expects.
// Skips the test if none are found.
func findRealClaudeSession(t *testing.T) (claudeHome string, sessionID string, projectPath string) {
	t.Helper()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}

	claudeHome = filepath.Join(home, ".claude")
	projectsPath := filepath.Join(claudeHome, "projects")

	if _, err := os.Stat(projectsPath); os.IsNotExist(err) {
		t.Skip("no Claude Code installation found")
	}

	entries, err := os.ReadDir(projectsPath)
	if err != nil {
		t.Skip("cannot read Claude projects directory")
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		projDir := filepath.Join(projectsPath, entry.Name())

		// Check for sessions-index.json — needed by Detect().
		indexPath := filepath.Join(projDir, "sessions-index.json")
		if _, err := os.Stat(indexPath); os.IsNotExist(err) {
			continue
		}

		// Read the index to find a session with an existing JSONL file.
		indexData, err := os.ReadFile(indexPath)
		if err != nil {
			continue
		}
		var index struct {
			Entries []struct {
				SessionID   string `json:"sessionId"`
				ProjectPath string `json:"projectPath"`
			} `json:"entries"`
		}
		if err := json.Unmarshal(indexData, &index); err != nil {
			continue
		}

		for _, e := range index.Entries {
			jsonlFile := filepath.Join(projDir, e.SessionID+".jsonl")
			if _, err := os.Stat(jsonlFile); err == nil {
				return claudeHome, e.SessionID, e.ProjectPath
			}
		}
	}

	t.Skip("no Claude Code sessions with valid index + JSONL found on this machine")
	return "", "", ""
}

// findRealOpenCodeSession finds a real OpenCode session on the host machine.
// Returns the provider, a session ID, and the real project path that Detect() expects.
// Skips the test if none are found.
func findRealOpenCodeSession(t *testing.T) (prov *opencode.Provider, sessionID string, projectPath string) {
	t.Helper()

	p := opencode.New("")

	projects, err := p.ListAllProjects()
	if err != nil || len(projects) == 0 {
		t.Skip("no OpenCode projects found")
	}

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
