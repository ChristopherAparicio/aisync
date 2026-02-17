package cursor

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/domain"

	_ "modernc.org/sqlite" // SQLite driver registration
)

// setupTestEnv creates a fake Cursor user directory with workspace and global state.
func setupTestEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Create workspace storage with workspace.json
	wsHash := "abc123test"
	wsDir := filepath.Join(dir, workspaceStorageDir, wsHash)
	if mkErr := os.MkdirAll(wsDir, 0o755); mkErr != nil {
		t.Fatal(mkErr)
	}

	// Write workspace.json pointing to /tmp/test-project
	wsJSON := `{"folder":"file:///tmp/test-project"}`
	if writeErr := os.WriteFile(filepath.Join(wsDir, "workspace.json"), []byte(wsJSON), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}

	// Create workspace state.vscdb with ItemTable
	createWorkspaceDB(t, filepath.Join(wsDir, "state.vscdb"))

	// Create global state.vscdb with cursorDiskKV
	globalDir := filepath.Join(dir, "globalStorage")
	if mkErr := os.MkdirAll(globalDir, 0o755); mkErr != nil {
		t.Fatal(mkErr)
	}
	createGlobalDB(t, filepath.Join(globalDir, "state.vscdb"))

	return dir
}

func createWorkspaceDB(t *testing.T, dbPath string) {
	t.Helper()
	db, openErr := sql.Open("sqlite", dbPath)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer func() { _ = db.Close() }()

	_, execErr := db.Exec(`CREATE TABLE IF NOT EXISTS ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`)
	if execErr != nil {
		t.Fatal(execErr)
	}

	// Insert composer data with 3 composers
	composers := workspaceComposers{
		AllComposers: []composerHead{
			{
				ComposerID:        "comp-001",
				Name:              "Implement OAuth2",
				UnifiedMode:       "agent",
				CreatedAt:         1740000000000,
				LastUpdatedAt:     1740000100000,
				CommittedToBranch: "feature/auth",
				CreatedOnBranch:   "feature/auth",
				FilesChangedCount: 3,
				TotalLinesAdded:   100,
				TotalLinesRemoved: 10,
				Subtitle:          "Edited auth.go, handler.go, auth_test.go",
			},
			{
				ComposerID:        "comp-002",
				Name:              "Fix linting issues",
				UnifiedMode:       "chat",
				CreatedAt:         1740000200000,
				LastUpdatedAt:     1740000300000,
				CommittedToBranch: "",
				CreatedOnBranch:   "main",
				FilesChangedCount: 0,
			},
			{
				ComposerID:        "comp-003",
				Name:              "Add dark mode",
				UnifiedMode:       "agent",
				CreatedAt:         1740000400000,
				LastUpdatedAt:     1740000500000,
				CommittedToBranch: "feature/auth",
				CreatedOnBranch:   "feature/auth",
				FilesChangedCount: 5,
				Subtitle:          "Edited theme.css, app.tsx, dark.css, light.css, colors.ts",
			},
		},
	}

	data, marshalErr := json.Marshal(composers)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}

	_, insertErr := db.Exec("INSERT INTO ItemTable (key, value) VALUES (?, ?)", composerDataKey, data)
	if insertErr != nil {
		t.Fatal(insertErr)
	}
}

func createGlobalDB(t *testing.T, dbPath string) {
	t.Helper()
	db, openErr := sql.Open("sqlite", dbPath)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer func() { _ = db.Close() }()

	_, execErr := db.Exec(`CREATE TABLE IF NOT EXISTS cursorDiskKV (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`)
	if execErr != nil {
		t.Fatal(execErr)
	}

	// Insert full composer data for comp-001
	cd := composerData{
		ComposerID:        "comp-001",
		Name:              "Implement OAuth2",
		UnifiedMode:       "agent",
		Status:            "completed",
		CreatedAt:         1740000000000,
		LastUpdatedAt:     1740000100000,
		CommittedToBranch: "feature/auth",
		CreatedOnBranch:   "feature/auth",
		ContextTokensUsed: 50000,
		ContextTokenLimit: 176000,
		FilesChangedCount: 3,
		TotalLinesAdded:   100,
		TotalLinesRemoved: 10,
		Subtitle:          "Edited auth.go, handler.go, auth_test.go",
		ModelConfig:       &modelConfig{ModelName: "claude-sonnet-4-20250514"},
		FullConversationHeadersOnly: []bubble{
			{BubbleID: "bubble-001", Type: bubbleTypeUser},
			{BubbleID: "bubble-002", Type: bubbleTypeAssistant},
			{BubbleID: "bubble-003", Type: bubbleTypeUser},
			{BubbleID: "bubble-004", Type: bubbleTypeAssistant},
		},
	}

	data, marshalErr := json.Marshal(cd)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}

	_, insertErr := db.Exec("INSERT INTO cursorDiskKV (key, value) VALUES (?, ?)",
		composerDataPrefix+"comp-001", data)
	if insertErr != nil {
		t.Fatal(insertErr)
	}
}

func TestName(t *testing.T) {
	p := New("")
	if p.Name() != domain.ProviderCursor {
		t.Errorf("Name() = %q, want %q", p.Name(), domain.ProviderCursor)
	}
}

func TestCanImport(t *testing.T) {
	p := New("")
	if p.CanImport() {
		t.Error("CanImport() = true, want false")
	}
}

func TestImport_returnsError(t *testing.T) {
	p := New("")
	err := p.Import(&domain.Session{})
	if err != domain.ErrImportNotSupported {
		t.Errorf("Import() error = %v, want ErrImportNotSupported", err)
	}
}

func TestDetect_allSessions(t *testing.T) {
	dir := setupTestEnv(t)
	p := New(dir)

	summaries, err := p.Detect("/tmp/test-project", "")
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	if len(summaries) != 3 {
		t.Fatalf("len(summaries) = %d, want 3", len(summaries))
	}

	// Should be sorted by created_at descending
	if summaries[0].ID != "comp-003" {
		t.Errorf("summaries[0].ID = %q, want comp-003 (most recent)", summaries[0].ID)
	}
}

func TestDetect_filterByBranch(t *testing.T) {
	dir := setupTestEnv(t)
	p := New(dir)

	summaries, err := p.Detect("/tmp/test-project", "feature/auth")
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	if len(summaries) != 2 {
		t.Fatalf("len(summaries) = %d, want 2 (comp-001 and comp-003)", len(summaries))
	}

	for _, s := range summaries {
		if s.Branch != "feature/auth" {
			t.Errorf("summary %s Branch = %q, want feature/auth", s.ID, s.Branch)
		}
	}
}

func TestDetect_noMatchingProject(t *testing.T) {
	dir := setupTestEnv(t)
	p := New(dir)

	_, err := p.Detect("/tmp/nonexistent", "")
	if err == nil {
		t.Fatal("Detect() expected error for non-matching project")
	}
}

func TestDetect_providerInfo(t *testing.T) {
	dir := setupTestEnv(t)
	p := New(dir)

	summaries, err := p.Detect("/tmp/test-project", "main")
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	if len(summaries) != 1 {
		t.Fatalf("len(summaries) = %d, want 1", len(summaries))
	}

	s := summaries[0]
	if s.Provider != domain.ProviderCursor {
		t.Errorf("Provider = %q, want %q", s.Provider, domain.ProviderCursor)
	}
	if s.Agent != "cursor-chat" {
		t.Errorf("Agent = %q, want cursor-chat", s.Agent)
	}
	if s.Summary != "Fix linting issues" {
		t.Errorf("Summary = %q, want 'Fix linting issues'", s.Summary)
	}
}

func TestExport(t *testing.T) {
	dir := setupTestEnv(t)
	p := New(dir)

	session, err := p.Export("comp-001", domain.StorageModeCompact)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	if session.ID != "comp-001" {
		t.Errorf("ID = %q, want comp-001", session.ID)
	}
	if session.Provider != domain.ProviderCursor {
		t.Errorf("Provider = %q, want %q", session.Provider, domain.ProviderCursor)
	}
	if session.Agent != "cursor-agent" {
		t.Errorf("Agent = %q, want cursor-agent", session.Agent)
	}
	if session.Branch != "feature/auth" {
		t.Errorf("Branch = %q, want feature/auth", session.Branch)
	}
	if session.Summary != "Implement OAuth2" {
		t.Errorf("Summary = %q, want 'Implement OAuth2'", session.Summary)
	}
	if session.TokenUsage.TotalTokens != 50000 {
		t.Errorf("TotalTokens = %d, want 50000", session.TokenUsage.TotalTokens)
	}
}

func TestExport_messages(t *testing.T) {
	dir := setupTestEnv(t)
	p := New(dir)

	session, err := p.Export("comp-001", domain.StorageModeCompact)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	if len(session.Messages) != 4 {
		t.Fatalf("len(Messages) = %d, want 4", len(session.Messages))
	}

	// Check roles alternate: user, assistant, user, assistant
	expectedRoles := []domain.MessageRole{
		domain.RoleUser, domain.RoleAssistant, domain.RoleUser, domain.RoleAssistant,
	}
	for i, msg := range session.Messages {
		if msg.Role != expectedRoles[i] {
			t.Errorf("Messages[%d].Role = %q, want %q", i, msg.Role, expectedRoles[i])
		}
	}

	// Assistant messages should have model info
	if session.Messages[1].Model != "claude-sonnet-4-20250514" {
		t.Errorf("Messages[1].Model = %q, want claude-sonnet-4-20250514", session.Messages[1].Model)
	}
}

func TestExport_summaryMode(t *testing.T) {
	dir := setupTestEnv(t)
	p := New(dir)

	session, err := p.Export("comp-001", domain.StorageModeSummary)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	// Summary mode should not include messages
	if len(session.Messages) != 0 {
		t.Errorf("len(Messages) = %d, want 0 in summary mode", len(session.Messages))
	}
}

func TestExport_fileChanges(t *testing.T) {
	dir := setupTestEnv(t)
	p := New(dir)

	session, err := p.Export("comp-001", domain.StorageModeCompact)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	if len(session.FileChanges) != 3 {
		t.Fatalf("len(FileChanges) = %d, want 3", len(session.FileChanges))
	}

	expectedFiles := []string{"auth.go", "handler.go", "auth_test.go"}
	for i, fc := range session.FileChanges {
		if fc.FilePath != expectedFiles[i] {
			t.Errorf("FileChanges[%d].FilePath = %q, want %q", i, fc.FilePath, expectedFiles[i])
		}
	}
}

func TestExport_notFound(t *testing.T) {
	dir := setupTestEnv(t)
	p := New(dir)

	_, err := p.Export("nonexistent", domain.StorageModeCompact)
	if err == nil {
		t.Fatal("Export() expected error for nonexistent composer")
	}
}

func TestAgentFromMode(t *testing.T) {
	tests := []struct {
		mode string
		want string
	}{
		{"agent", "cursor-agent"},
		{"chat", "cursor-chat"},
		{"unknown", defaultAgent},
		{"", defaultAgent},
	}
	for _, tt := range tests {
		got := agentFromMode(tt.mode)
		if got != tt.want {
			t.Errorf("agentFromMode(%q) = %q, want %q", tt.mode, got, tt.want)
		}
	}
}

func TestExtractFileChanges(t *testing.T) {
	tests := []struct {
		name     string
		subtitle string
		want     int
	}{
		{"edited files", "Edited auth.go, handler.go, test.go", 3},
		{"created files", "Created new_file.go", 1},
		{"empty", "", 0},
		{"just prefix", "Edited ", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFileChanges(tt.subtitle)
			if len(got) != tt.want {
				t.Errorf("extractFileChanges(%q) returned %d changes, want %d", tt.subtitle, len(got), tt.want)
			}
		})
	}
}

func TestWorkspaceJSON_extractPath(t *testing.T) {
	tests := []struct {
		name string
		ws   workspaceJSON
		want string
	}{
		{"folder", workspaceJSON{Folder: "file:///Users/test/project"}, "/Users/test/project"},
		{"workspace", workspaceJSON{Workspace: "file:///Users/test/ws.code-workspace"}, "/Users/test/ws.code-workspace"},
		{"encoded", workspaceJSON{Folder: "file:///Users/test/My%20Project"}, "/Users/test/My Project"},
		{"empty", workspaceJSON{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ws.extractPath()
			if got != tt.want {
				t.Errorf("extractPath() = %q, want %q", got, tt.want)
			}
		})
	}
}
