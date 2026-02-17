package opencode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/domain"
)

func TestName(t *testing.T) {
	p := New("")
	if p.Name() != domain.ProviderOpenCode {
		t.Errorf("Name() = %q, want %q", p.Name(), domain.ProviderOpenCode)
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

	session, err := p.Export("ses_test001", domain.StorageModeFull)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// Basic metadata
	if session.Provider != domain.ProviderOpenCode {
		t.Errorf("Provider = %q, want %q", session.Provider, domain.ProviderOpenCode)
	}
	if session.Agent != "build" {
		t.Errorf("Agent = %q, want %q", session.Agent, "build")
	}
	if session.Summary != "Implement hello world" {
		t.Errorf("Summary = %q, want %q", session.Summary, "Implement hello world")
	}
	if session.ProjectPath != "/tmp/test/myproject" {
		t.Errorf("ProjectPath = %q, want %q", session.ProjectPath, "/tmp/test/myproject")
	}

	// Should have 2 messages (user + assistant)
	if len(session.Messages) != 2 {
		t.Fatalf("Messages count = %d, want 2", len(session.Messages))
	}

	// First message: user
	if session.Messages[0].Role != domain.RoleUser {
		t.Errorf("Messages[0].Role = %q, want %q", session.Messages[0].Role, domain.RoleUser)
	}
	if session.Messages[0].Content != "Add a hello world function to main.go" {
		t.Errorf("Messages[0].Content = %q, want %q", session.Messages[0].Content, "Add a hello world function to main.go")
	}

	// Second message: assistant
	if session.Messages[1].Role != domain.RoleAssistant {
		t.Errorf("Messages[1].Role = %q, want %q", session.Messages[1].Role, domain.RoleAssistant)
	}

	// Tool calls on assistant message
	if len(session.Messages[1].ToolCalls) != 2 {
		t.Fatalf("ToolCalls count = %d, want 2", len(session.Messages[1].ToolCalls))
	}

	// First tool call: write (completed)
	tc := session.Messages[1].ToolCalls[0]
	if tc.Name != "write" {
		t.Errorf("ToolCall[0].Name = %q, want %q", tc.Name, "write")
	}
	if tc.State != domain.ToolStateCompleted {
		t.Errorf("ToolCall[0].State = %q, want %q", tc.State, domain.ToolStateCompleted)
	}
	if tc.DurationMs != 500 {
		t.Errorf("ToolCall[0].DurationMs = %d, want 500", tc.DurationMs)
	}

	// Second tool call: read (error)
	tc2 := session.Messages[1].ToolCalls[1]
	if tc2.State != domain.ToolStateError {
		t.Errorf("ToolCall[1].State = %q, want %q", tc2.State, domain.ToolStateError)
	}

	// Token usage
	if session.TokenUsage.TotalTokens == 0 {
		t.Error("TotalTokens should not be 0")
	}

	// File changes: write(main.go) + read(missing.go)
	if len(session.FileChanges) != 2 {
		t.Fatalf("FileChanges count = %d, want 2", len(session.FileChanges))
	}
	// Verify at least main.go is tracked
	var foundMainGo bool
	for _, fc := range session.FileChanges {
		if fc.FilePath == "main.go" && fc.ChangeType == domain.ChangeCreated {
			foundMainGo = true
		}
	}
	if !foundMainGo {
		t.Error("Expected main.go with ChangeCreated in FileChanges")
	}

	// Child sessions
	if len(session.Children) != 1 {
		t.Fatalf("Children count = %d, want 1", len(session.Children))
	}
	if session.Children[0].ParentID != "ses_test001" {
		t.Errorf("Child.ParentID = %q, want %q", session.Children[0].ParentID, "ses_test001")
	}
}

func TestExport_summaryMode(t *testing.T) {
	dataHome := setupTestDataHome(t)
	p := New(dataHome)

	session, err := p.Export("ses_test001", domain.StorageModeSummary)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	if session.Summary != "Implement hello world" {
		t.Errorf("Summary = %q, want %q", session.Summary, "Implement hello world")
	}
	if len(session.Messages) != 0 {
		t.Errorf("Messages count = %d, want 0 in summary mode", len(session.Messages))
	}
}

func TestExport_sessionNotFound(t *testing.T) {
	dataHome := setupTestDataHome(t)
	p := New(dataHome)

	_, err := p.Export("nonexistent", domain.StorageModeFull)
	if err != domain.ErrSessionNotFound {
		t.Errorf("Export() error = %v, want ErrSessionNotFound", err)
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
