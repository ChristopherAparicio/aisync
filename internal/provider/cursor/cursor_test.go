package cursor

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"

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
	if p.Name() != session.ProviderCursor {
		t.Errorf("Name() = %q, want %q", p.Name(), session.ProviderCursor)
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
	err := p.Import(&session.Session{})
	if err != session.ErrImportNotSupported {
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
	if s.Provider != session.ProviderCursor {
		t.Errorf("Provider = %q, want %q", s.Provider, session.ProviderCursor)
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

	sess, err := p.Export("comp-001", session.StorageModeCompact)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	if sess.ID != "comp-001" {
		t.Errorf("ID = %q, want comp-001", sess.ID)
	}
	if sess.Provider != session.ProviderCursor {
		t.Errorf("Provider = %q, want %q", sess.Provider, session.ProviderCursor)
	}
	if sess.Agent != "cursor-agent" {
		t.Errorf("Agent = %q, want cursor-agent", sess.Agent)
	}
	if sess.Branch != "feature/auth" {
		t.Errorf("Branch = %q, want feature/auth", sess.Branch)
	}
	if sess.Summary != "Implement OAuth2" {
		t.Errorf("Summary = %q, want 'Implement OAuth2'", sess.Summary)
	}
	if sess.TokenUsage.TotalTokens != 50000 {
		t.Errorf("TotalTokens = %d, want 50000", sess.TokenUsage.TotalTokens)
	}
}

func TestExport_messages(t *testing.T) {
	dir := setupTestEnv(t)
	p := New(dir)

	sess, err := p.Export("comp-001", session.StorageModeCompact)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	if len(sess.Messages) != 4 {
		t.Fatalf("len(Messages) = %d, want 4", len(sess.Messages))
	}

	// Check roles alternate: user, assistant, user, assistant
	expectedRoles := []session.MessageRole{
		session.RoleUser, session.RoleAssistant, session.RoleUser, session.RoleAssistant,
	}
	for i, msg := range sess.Messages {
		if msg.Role != expectedRoles[i] {
			t.Errorf("Messages[%d].Role = %q, want %q", i, msg.Role, expectedRoles[i])
		}
	}

	// Assistant messages should have model info
	if sess.Messages[1].Model != "claude-sonnet-4-20250514" {
		t.Errorf("Messages[1].Model = %q, want claude-sonnet-4-20250514", sess.Messages[1].Model)
	}
}

func TestExport_summaryMode(t *testing.T) {
	dir := setupTestEnv(t)
	p := New(dir)

	sess, err := p.Export("comp-001", session.StorageModeSummary)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	// Summary mode should not include messages
	if len(sess.Messages) != 0 {
		t.Errorf("len(Messages) = %d, want 0 in summary mode", len(sess.Messages))
	}
}

func TestExport_fileChanges(t *testing.T) {
	dir := setupTestEnv(t)
	p := New(dir)

	sess, err := p.Export("comp-001", session.StorageModeCompact)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	if len(sess.FileChanges) != 3 {
		t.Fatalf("len(FileChanges) = %d, want 3", len(sess.FileChanges))
	}

	expectedFiles := []string{"auth.go", "handler.go", "auth_test.go"}
	for i, fc := range sess.FileChanges {
		if fc.FilePath != expectedFiles[i] {
			t.Errorf("FileChanges[%d].FilePath = %q, want %q", i, fc.FilePath, expectedFiles[i])
		}
	}
}

func TestExport_notFound(t *testing.T) {
	dir := setupTestEnv(t)
	p := New(dir)

	_, err := p.Export("nonexistent", session.StorageModeCompact)
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

// ── New v13+ field tests ──

func TestTotalCostUSD(t *testing.T) {
	tests := []struct {
		name      string
		usageData map[string]*usageEntry
		want      float64
	}{
		{
			"single model cost",
			map[string]*usageEntry{
				"default": {CostInCents: 245, Amount: 8},
			},
			2.45,
		},
		{
			"multi model cost",
			map[string]*usageEntry{
				"default":                {CostInCents: 100, Amount: 5},
				"claude-opus-4-20250514": {CostInCents: 350, Amount: 2},
			},
			4.50,
		},
		{
			"nil usage data",
			nil,
			0,
		},
		{
			"empty usage data",
			map[string]*usageEntry{},
			0,
		},
		{
			"nil entry in map",
			map[string]*usageEntry{
				"default": nil,
				"other":   {CostInCents: 100, Amount: 1},
			},
			1.0,
		},
		{
			"zero cost",
			map[string]*usageEntry{
				"default": {CostInCents: 0, Amount: 3},
			},
			0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cd := &composerData{UsageData: tt.usageData}
			got := cd.totalCostUSD()
			if got != tt.want {
				t.Errorf("totalCostUSD() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTotalAPIRequests(t *testing.T) {
	tests := []struct {
		name      string
		usageData map[string]*usageEntry
		want      int
	}{
		{
			"single model",
			map[string]*usageEntry{
				"default": {CostInCents: 245, Amount: 8},
			},
			8,
		},
		{
			"multi model",
			map[string]*usageEntry{
				"default": {CostInCents: 100, Amount: 5},
				"premium": {CostInCents: 350, Amount: 2},
			},
			7,
		},
		{
			"nil usage data",
			nil,
			0,
		},
		{
			"nil entry ignored",
			map[string]*usageEntry{
				"default": nil,
				"other":   {CostInCents: 50, Amount: 3},
			},
			3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cd := &composerData{UsageData: tt.usageData}
			got := cd.totalAPIRequests()
			if got != tt.want {
				t.Errorf("totalAPIRequests() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMapCodeBlockStatus(t *testing.T) {
	tests := []struct {
		status string
		want   session.ToolState
	}{
		{"completed", session.ToolStateCompleted},
		{"accepted", session.ToolStateCompleted},
		{"aborted", session.ToolStateError},
		{"pending", session.ToolStatePending},
		{"in_progress", session.ToolStatePending},
		{"", session.ToolStatePending},
		{"unknown_status", session.ToolStatePending},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := mapCodeBlockStatus(tt.status)
			if got != tt.want {
				t.Errorf("mapCodeBlockStatus(%q) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestResolveAgent(t *testing.T) {
	tests := []struct {
		name        string
		isAgentic   bool
		isNAL       bool
		unifiedMode string
		want        string
	}{
		{"agentic flag", true, false, "chat", "cursor-agent"},
		{"NAL flag", false, true, "chat", "cursor-agent"},
		{"both flags", true, true, "chat", "cursor-agent"},
		{"no flags agent mode", false, false, "agent", "cursor-agent"},
		{"no flags chat mode", false, false, "chat", "cursor-chat"},
		{"no flags empty mode", false, false, "", defaultAgent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cd := &composerData{
				IsAgentic:   tt.isAgentic,
				IsNAL:       tt.isNAL,
				UnifiedMode: tt.unifiedMode,
			}
			got := cd.resolveAgent()
			if got != tt.want {
				t.Errorf("resolveAgent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildToolCallIndex(t *testing.T) {
	t.Run("empty codeBlockData", func(t *testing.T) {
		cd := &composerData{}
		got := cd.buildToolCallIndex()
		if got != nil {
			t.Errorf("buildToolCallIndex() = %v, want nil for empty codeBlockData", got)
		}
	})

	t.Run("single file single block", func(t *testing.T) {
		cd := &composerData{
			CodeBlockData: map[string]map[string]*codeBlock{
				"file:///src/main.go": {
					"block-1": {
						BubbleID:    "bubble-002",
						CodeBlockID: "block-1",
						Status:      "completed",
						LanguageID:  "go",
						URI:         &uriRef{FSPath: "/src/main.go"},
					},
				},
			},
		}

		got := cd.buildToolCallIndex()
		if got == nil {
			t.Fatal("buildToolCallIndex() returned nil, want non-nil")
		}

		tcs, ok := got["bubble-002"]
		if !ok {
			t.Fatal("no tool calls for bubble-002")
		}
		if len(tcs) != 1 {
			t.Fatalf("len(tool calls) = %d, want 1", len(tcs))
		}
		if tcs[0].ID != "block-1" {
			t.Errorf("ToolCall.ID = %q, want block-1", tcs[0].ID)
		}
		if tcs[0].Name != "edit" {
			t.Errorf("ToolCall.Name = %q, want edit", tcs[0].Name)
		}
		if tcs[0].Input != "/src/main.go" {
			t.Errorf("ToolCall.Input = %q, want /src/main.go", tcs[0].Input)
		}
		if tcs[0].State != session.ToolStateCompleted {
			t.Errorf("ToolCall.State = %q, want %q", tcs[0].State, session.ToolStateCompleted)
		}
	})

	t.Run("multiple blocks same bubble sorted by ID", func(t *testing.T) {
		cd := &composerData{
			CodeBlockData: map[string]map[string]*codeBlock{
				"file:///src/a.go": {
					"block-z": {
						BubbleID:    "bubble-002",
						CodeBlockID: "block-z",
						Status:      "completed",
						URI:         &uriRef{FSPath: "/src/a.go"},
					},
				},
				"file:///src/b.go": {
					"block-a": {
						BubbleID:    "bubble-002",
						CodeBlockID: "block-a",
						Status:      "accepted",
						URI:         &uriRef{FSPath: "/src/b.go"},
					},
				},
			},
		}

		got := cd.buildToolCallIndex()
		tcs := got["bubble-002"]
		if len(tcs) != 2 {
			t.Fatalf("len(tool calls) = %d, want 2", len(tcs))
		}
		// Should be sorted by ID: block-a before block-z
		if tcs[0].ID != "block-a" {
			t.Errorf("tcs[0].ID = %q, want block-a", tcs[0].ID)
		}
		if tcs[1].ID != "block-z" {
			t.Errorf("tcs[1].ID = %q, want block-z", tcs[1].ID)
		}
	})

	t.Run("nil block and empty bubbleID skipped", func(t *testing.T) {
		cd := &composerData{
			CodeBlockData: map[string]map[string]*codeBlock{
				"file:///src/a.go": {
					"block-1": nil,
					"block-2": {
						BubbleID:    "", // empty bubbleID → skipped
						CodeBlockID: "block-2",
						Status:      "completed",
					},
					"block-3": {
						BubbleID:    "bubble-005",
						CodeBlockID: "block-3",
						Status:      "completed",
						URI:         &uriRef{FSPath: "/src/a.go"},
					},
				},
			},
		}

		got := cd.buildToolCallIndex()
		if len(got) != 1 {
			t.Fatalf("len(index) = %d, want 1", len(got))
		}
		if _, ok := got["bubble-005"]; !ok {
			t.Error("expected entry for bubble-005")
		}
	})

	t.Run("falls back to fileURI when block has no URI", func(t *testing.T) {
		cd := &composerData{
			CodeBlockData: map[string]map[string]*codeBlock{
				"file:///fallback/path.go": {
					"block-1": {
						BubbleID:    "bubble-010",
						CodeBlockID: "block-1",
						Status:      "completed",
						// No URI → should use file URI key as Input
					},
				},
			},
		}

		got := cd.buildToolCallIndex()
		tcs := got["bubble-010"]
		if len(tcs) != 1 {
			t.Fatalf("len(tool calls) = %d, want 1", len(tcs))
		}
		if tcs[0].Input != "file:///fallback/path.go" {
			t.Errorf("ToolCall.Input = %q, want file:///fallback/path.go", tcs[0].Input)
		}
	})
}

func TestExtractFileChanges_structured(t *testing.T) {
	t.Run("newlyCreatedFiles", func(t *testing.T) {
		cd := &composerData{
			NewlyCreatedFiles: []fileRef{
				{URI: &uriRef{FSPath: "/src/new_file.go"}},
				{URI: &uriRef{FSPath: "/src/another.go"}},
			},
		}

		got := cd.extractFileChanges()
		if len(got) != 2 {
			t.Fatalf("len(changes) = %d, want 2", len(got))
		}
		// Sorted by path
		if got[0].FilePath != "/src/another.go" {
			t.Errorf("changes[0].FilePath = %q, want /src/another.go", got[0].FilePath)
		}
		if got[0].ChangeType != session.ChangeCreated {
			t.Errorf("changes[0].ChangeType = %q, want %q", got[0].ChangeType, session.ChangeCreated)
		}
		if got[1].FilePath != "/src/new_file.go" {
			t.Errorf("changes[1].FilePath = %q, want /src/new_file.go", got[1].FilePath)
		}
	})

	t.Run("originalFileStates as modified", func(t *testing.T) {
		cd := &composerData{
			OriginalFileStates: map[string]json.RawMessage{
				"file:///src/existing.go": json.RawMessage(`{}`),
				"file:///src/other.go":    json.RawMessage(`{}`),
			},
		}

		got := cd.extractFileChanges()
		if len(got) != 2 {
			t.Fatalf("len(changes) = %d, want 2", len(got))
		}
		for _, fc := range got {
			if fc.ChangeType != session.ChangeModified {
				t.Errorf("change %q type = %q, want %q", fc.FilePath, fc.ChangeType, session.ChangeModified)
			}
		}
	})

	t.Run("created takes priority over modified", func(t *testing.T) {
		cd := &composerData{
			NewlyCreatedFiles: []fileRef{
				{URI: &uriRef{FSPath: "/src/file.go"}},
			},
			OriginalFileStates: map[string]json.RawMessage{
				// Same file in both — should remain Created (not overwritten to Modified)
				"/src/file.go": json.RawMessage(`{}`),
			},
		}

		got := cd.extractFileChanges()
		if len(got) != 1 {
			t.Fatalf("len(changes) = %d, want 1", len(got))
		}
		if got[0].ChangeType != session.ChangeCreated {
			t.Errorf("ChangeType = %q, want %q (Created takes priority)", got[0].ChangeType, session.ChangeCreated)
		}
	})

	t.Run("codeBlockData adds modified files", func(t *testing.T) {
		cd := &composerData{
			CodeBlockData: map[string]map[string]*codeBlock{
				"file:///src/edited.go": {
					"block-1": {
						BubbleID:    "b1",
						CodeBlockID: "block-1",
						Status:      "completed",
						URI:         &uriRef{FSPath: "/src/edited.go"},
					},
				},
			},
		}

		got := cd.extractFileChanges()
		if len(got) != 1 {
			t.Fatalf("len(changes) = %d, want 1", len(got))
		}
		if got[0].FilePath != "/src/edited.go" {
			t.Errorf("FilePath = %q, want /src/edited.go", got[0].FilePath)
		}
		if got[0].ChangeType != session.ChangeModified {
			t.Errorf("ChangeType = %q, want %q", got[0].ChangeType, session.ChangeModified)
		}
	})

	t.Run("all three sources combined and deduped", func(t *testing.T) {
		cd := &composerData{
			NewlyCreatedFiles: []fileRef{
				{URI: &uriRef{FSPath: "/src/created.go"}},
			},
			OriginalFileStates: map[string]json.RawMessage{
				"file:///src/modified.go": json.RawMessage(`{}`),
			},
			CodeBlockData: map[string]map[string]*codeBlock{
				"file:///src/code_edit.go": {
					"block-1": {
						BubbleID:    "b1",
						CodeBlockID: "block-1",
						Status:      "completed",
						URI:         &uriRef{FSPath: "/src/code_edit.go"},
					},
				},
				// Also edits the created file — should not change it to Modified
				"file:///src/created.go": {
					"block-2": {
						BubbleID:    "b2",
						CodeBlockID: "block-2",
						Status:      "completed",
						URI:         &uriRef{FSPath: "/src/created.go"},
					},
				},
			},
		}

		got := cd.extractFileChanges()
		if len(got) != 3 {
			t.Fatalf("len(changes) = %d, want 3", len(got))
		}

		// Build a map for easy lookup (sorted output)
		byPath := make(map[string]session.ChangeType)
		for _, fc := range got {
			byPath[fc.FilePath] = fc.ChangeType
		}

		if byPath["/src/created.go"] != session.ChangeCreated {
			t.Errorf("created.go type = %q, want Created", byPath["/src/created.go"])
		}
		if ct, ok := byPath["/src/modified.go"]; !ok || ct != session.ChangeModified {
			t.Errorf("modified.go type = %q, want Modified", ct)
		}
		if ct, ok := byPath["/src/code_edit.go"]; !ok || ct != session.ChangeModified {
			t.Errorf("code_edit.go type = %q, want Modified", ct)
		}
	})

	t.Run("subtitle fallback when no structured data", func(t *testing.T) {
		cd := &composerData{
			FilesChangedCount: 2,
			Subtitle:          "Edited main.go, util.go",
		}

		got := cd.extractFileChanges()
		if len(got) != 2 {
			t.Fatalf("len(changes) = %d, want 2", len(got))
		}
		if got[0].FilePath != "main.go" {
			t.Errorf("changes[0].FilePath = %q, want main.go", got[0].FilePath)
		}
	})

	t.Run("nil URI in newlyCreatedFiles skipped", func(t *testing.T) {
		cd := &composerData{
			NewlyCreatedFiles: []fileRef{
				{URI: nil},
				{URI: &uriRef{FSPath: ""}},
				{URI: &uriRef{FSPath: "/valid.go"}},
			},
		}

		got := cd.extractFileChanges()
		if len(got) != 1 {
			t.Fatalf("len(changes) = %d, want 1", len(got))
		}
	})

	t.Run("URL-encoded originalFileStates path decoded", func(t *testing.T) {
		cd := &composerData{
			OriginalFileStates: map[string]json.RawMessage{
				"file:///Users/dev/My%20Project/file.go": json.RawMessage(`{}`),
			},
		}

		got := cd.extractFileChanges()
		if len(got) != 1 {
			t.Fatalf("len(changes) = %d, want 1", len(got))
		}
		if got[0].FilePath != "/Users/dev/My Project/file.go" {
			t.Errorf("FilePath = %q, want /Users/dev/My Project/file.go", got[0].FilePath)
		}
	})
}

func TestExtractMessages_withUsageData(t *testing.T) {
	cd := &composerData{
		ModelConfig: &modelConfig{ModelName: "claude-sonnet-4-20250514"},
		UsageData: map[string]*usageEntry{
			"default": {CostInCents: 200, Amount: 4},
		},
		FullConversationHeadersOnly: []bubble{
			{BubbleID: "b1", Type: bubbleTypeUser},
			{BubbleID: "b2", Type: bubbleTypeAssistant},
			{BubbleID: "b3", Type: bubbleTypeUser},
			{BubbleID: "b4", Type: bubbleTypeAssistant},
		},
	}

	msgs := cd.extractMessages()
	if len(msgs) != 4 {
		t.Fatalf("len(msgs) = %d, want 4", len(msgs))
	}

	// 4 total API requests, $2.00 total → $0.50 per request
	// 2 assistant messages should each get $0.50
	for _, msg := range msgs {
		if msg.Role == session.RoleAssistant {
			if msg.ProviderCost != 0.50 {
				t.Errorf("assistant msg %s ProviderCost = %v, want 0.50", msg.ID, msg.ProviderCost)
			}
		} else {
			if msg.ProviderCost != 0 {
				t.Errorf("user msg %s ProviderCost = %v, want 0", msg.ID, msg.ProviderCost)
			}
		}
	}
}

func TestExtractMessages_withToolCalls(t *testing.T) {
	cd := &composerData{
		ModelConfig: &modelConfig{ModelName: "gpt-4"},
		FullConversationHeadersOnly: []bubble{
			{BubbleID: "b1", Type: bubbleTypeUser},
			{BubbleID: "b2", Type: bubbleTypeAssistant},
		},
		CodeBlockData: map[string]map[string]*codeBlock{
			"file:///src/main.go": {
				"block-1": {
					BubbleID:    "b2",
					CodeBlockID: "block-1",
					Status:      "completed",
					URI:         &uriRef{FSPath: "/src/main.go"},
				},
			},
		},
	}

	msgs := cd.extractMessages()
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}

	// First message (user) should have no tool calls
	if len(msgs[0].ToolCalls) != 0 {
		t.Errorf("user message has %d tool calls, want 0", len(msgs[0].ToolCalls))
	}

	// Second message (assistant) should have 1 tool call
	if len(msgs[1].ToolCalls) != 1 {
		t.Fatalf("assistant message has %d tool calls, want 1", len(msgs[1].ToolCalls))
	}

	tc := msgs[1].ToolCalls[0]
	if tc.ID != "block-1" {
		t.Errorf("ToolCall.ID = %q, want block-1", tc.ID)
	}
	if tc.Name != "edit" {
		t.Errorf("ToolCall.Name = %q, want edit", tc.Name)
	}
	if tc.Input != "/src/main.go" {
		t.Errorf("ToolCall.Input = %q, want /src/main.go", tc.Input)
	}
}

func TestExtractMessages_noUsageData(t *testing.T) {
	cd := &composerData{
		ModelConfig: &modelConfig{ModelName: "claude-sonnet-4-20250514"},
		FullConversationHeadersOnly: []bubble{
			{BubbleID: "b1", Type: bubbleTypeUser},
			{BubbleID: "b2", Type: bubbleTypeAssistant},
		},
	}

	msgs := cd.extractMessages()
	for _, msg := range msgs {
		if msg.ProviderCost != 0 {
			t.Errorf("msg %s ProviderCost = %v, want 0 when no usage data", msg.ID, msg.ProviderCost)
		}
	}
}

// TestExport_v13Full tests a full Export with all v13+ fields populated.
func TestExport_v13Full(t *testing.T) {
	dir := t.TempDir()

	// Create workspace storage
	wsHash := "ws-v13"
	wsDir := filepath.Join(dir, workspaceStorageDir, wsHash)
	if mkErr := os.MkdirAll(wsDir, 0o755); mkErr != nil {
		t.Fatal(mkErr)
	}
	wsJSON := `{"folder":"file:///tmp/v13-project"}`
	if writeErr := os.WriteFile(filepath.Join(wsDir, "workspace.json"), []byte(wsJSON), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}

	// Create workspace DB
	wsDBPath := filepath.Join(wsDir, "state.vscdb")
	db, openErr := sql.Open("sqlite", wsDBPath)
	if openErr != nil {
		t.Fatal(openErr)
	}
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`)
	composers := workspaceComposers{
		AllComposers: []composerHead{
			{ComposerID: "v13-001", Name: "Full v13 session", UnifiedMode: "agent", CreatedAt: 1740000000000},
		},
	}
	wsData, _ := json.Marshal(composers)
	_, _ = db.Exec("INSERT INTO ItemTable (key, value) VALUES (?, ?)", composerDataKey, wsData)
	_ = db.Close()

	// Create global DB with full v13+ composer data
	globalDir := filepath.Join(dir, "globalStorage")
	if mkErr := os.MkdirAll(globalDir, 0o755); mkErr != nil {
		t.Fatal(mkErr)
	}
	globalDBPath := filepath.Join(globalDir, "state.vscdb")
	gdb, openErr := sql.Open("sqlite", globalDBPath)
	if openErr != nil {
		t.Fatal(openErr)
	}
	_, _ = gdb.Exec(`CREATE TABLE IF NOT EXISTS cursorDiskKV (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`)

	cd := composerData{
		ComposerID:          "v13-001",
		Name:                "Full v13 session",
		UnifiedMode:         "chat", // mode says chat, but isAgentic overrides
		IsAgentic:           true,
		IsNAL:               true,
		SchemaVersion:       14,
		CreatedAt:           1740000000000,
		LastUpdatedAt:       1740000100000,
		CommittedToBranch:   "feature/v13",
		CreatedOnBranch:     "feature/v13",
		ContextTokensUsed:   80000,
		ContextTokenLimit:   200000,
		ContextUsagePercent: 37.93,
		ModelConfig:         &modelConfig{ModelName: "claude-sonnet-4-20250514", MaxMode: true},
		UsageData: map[string]*usageEntry{
			"default": {CostInCents: 245, Amount: 8},
		},
		FullConversationHeadersOnly: []bubble{
			{BubbleID: "vb-001", Type: bubbleTypeUser},
			{BubbleID: "vb-002", Type: bubbleTypeAssistant},
			{BubbleID: "vb-003", Type: bubbleTypeUser},
			{BubbleID: "vb-004", Type: bubbleTypeAssistant},
		},
		CodeBlockData: map[string]map[string]*codeBlock{
			"file:///src/main.go": {
				"cb-001": {
					BubbleID:    "vb-002",
					CodeBlockID: "cb-001",
					Status:      "completed",
					LanguageID:  "go",
					URI:         &uriRef{FSPath: "/src/main.go"},
				},
			},
			"file:///src/util.go": {
				"cb-002": {
					BubbleID:    "vb-004",
					CodeBlockID: "cb-002",
					Status:      "accepted",
					LanguageID:  "go",
					URI:         &uriRef{FSPath: "/src/util.go"},
				},
			},
		},
		NewlyCreatedFiles: []fileRef{
			{URI: &uriRef{FSPath: "/src/new_handler.go"}},
		},
		OriginalFileStates: map[string]json.RawMessage{
			"file:///src/main.go": json.RawMessage(`{}`),
		},
		Todos: []todoItem{
			{ID: "t1", Content: "Implement handler", Status: "completed"},
			{ID: "t2", Content: "Write tests", Status: "pending"},
		},
		SubagentComposerIds: []string{"sub-001", "sub-002"},
	}

	cdData, _ := json.Marshal(cd)
	_, _ = gdb.Exec("INSERT INTO cursorDiskKV (key, value) VALUES (?, ?)",
		composerDataPrefix+"v13-001", cdData)
	_ = gdb.Close()

	// Export
	p := New(dir)
	sess, err := p.Export("v13-001", session.StorageModeCompact)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	// Verify agent resolved from isAgentic (not unifiedMode)
	if sess.Agent != "cursor-agent" {
		t.Errorf("Agent = %q, want cursor-agent (from isAgentic)", sess.Agent)
	}

	// Verify cost
	if sess.ActualCost != 2.45 {
		t.Errorf("ActualCost = %v, want 2.45", sess.ActualCost)
	}

	// Verify messages
	if len(sess.Messages) != 4 {
		t.Fatalf("len(Messages) = %d, want 4", len(sess.Messages))
	}

	// Message vb-002 (assistant) should have tool call cb-001
	msg2 := sess.Messages[1]
	if len(msg2.ToolCalls) != 1 {
		t.Fatalf("msg[1] tool calls = %d, want 1", len(msg2.ToolCalls))
	}
	if msg2.ToolCalls[0].ID != "cb-001" {
		t.Errorf("msg[1] tool call ID = %q, want cb-001", msg2.ToolCalls[0].ID)
	}

	// Message vb-004 (assistant) should have tool call cb-002
	msg4 := sess.Messages[3]
	if len(msg4.ToolCalls) != 1 {
		t.Fatalf("msg[3] tool calls = %d, want 1", len(msg4.ToolCalls))
	}
	if msg4.ToolCalls[0].ID != "cb-002" {
		t.Errorf("msg[3] tool call ID = %q, want cb-002", msg4.ToolCalls[0].ID)
	}

	// Verify file changes: 3 files (1 created + 1 modified from originalFileStates + 1 modified from codeBlockData)
	if len(sess.FileChanges) != 3 {
		t.Fatalf("len(FileChanges) = %d, want 3", len(sess.FileChanges))
	}
	byPath := make(map[string]session.ChangeType)
	for _, fc := range sess.FileChanges {
		byPath[fc.FilePath] = fc.ChangeType
	}
	if byPath["/src/new_handler.go"] != session.ChangeCreated {
		t.Errorf("new_handler.go = %q, want Created", byPath["/src/new_handler.go"])
	}
	if byPath["/src/main.go"] != session.ChangeModified {
		t.Errorf("main.go = %q, want Modified", byPath["/src/main.go"])
	}
	if byPath["/src/util.go"] != session.ChangeModified {
		t.Errorf("util.go = %q, want Modified", byPath["/src/util.go"])
	}

	// Verify assistant messages have cost distributed
	for _, msg := range sess.Messages {
		if msg.Role == session.RoleAssistant && msg.ProviderCost == 0 {
			t.Errorf("assistant msg %s has zero ProviderCost, expected distributed cost", msg.ID)
		}
	}
}

// TestExport_backwardCompat tests that sessions without v13+ fields still work correctly.
func TestExport_backwardCompat(t *testing.T) {
	dir := setupTestEnv(t)
	p := New(dir)

	// comp-001 in setupTestEnv has no v13+ fields (usageData, codeBlockData, etc.)
	sess, err := p.Export("comp-001", session.StorageModeCompact)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	// Cost should be zero (no usageData)
	if sess.ActualCost != 0 {
		t.Errorf("ActualCost = %v, want 0 for legacy session", sess.ActualCost)
	}

	// Messages should have no tool calls (no codeBlockData)
	for i, msg := range sess.Messages {
		if len(msg.ToolCalls) != 0 {
			t.Errorf("Messages[%d] has %d tool calls, want 0 for legacy session", i, len(msg.ToolCalls))
		}
	}

	// Messages should have no ProviderCost (no usageData)
	for i, msg := range sess.Messages {
		if msg.ProviderCost != 0 {
			t.Errorf("Messages[%d].ProviderCost = %v, want 0 for legacy session", i, msg.ProviderCost)
		}
	}

	// File changes should still come from subtitle parsing
	if len(sess.FileChanges) != 3 {
		t.Errorf("len(FileChanges) = %d, want 3 (from subtitle fallback)", len(sess.FileChanges))
	}
}
