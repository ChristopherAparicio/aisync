package session

import (
	"sort"
	"testing"
)

func TestExtractFileOperations_WriteOp(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "Write", Input: `{"filePath": "/Users/me/dev/project/main.go", "content": "package main"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].FilePath != "/Users/me/dev/project/main.go" {
		t.Errorf("FilePath = %q", ops[0].FilePath)
	}
	if ops[0].ChangeType != ChangeCreated {
		t.Errorf("ChangeType = %q, want created", ops[0].ChangeType)
	}
	if ops[0].ToolName != "Write" {
		t.Errorf("ToolName = %q, want Write", ops[0].ToolName)
	}
}

func TestExtractFileOperations_MCPWrite(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "mcp_write", Input: `{"filePath": "/tmp/test.txt", "content": "hello"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].ChangeType != ChangeCreated {
		t.Errorf("ChangeType = %q, want created", ops[0].ChangeType)
	}
}

func TestExtractFileOperations_EditOp(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "Edit", Input: `{"filePath": "/Users/me/dev/project/handler.go", "oldString": "foo", "newString": "bar"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].ChangeType != ChangeModified {
		t.Errorf("ChangeType = %q, want modified", ops[0].ChangeType)
	}
}

func TestExtractFileOperations_MCPEdit(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "mcp_edit", Input: `{"filePath": "/home/user/app.ts", "oldString": "x", "newString": "y"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].ChangeType != ChangeModified {
		t.Errorf("ChangeType = %q, want modified", ops[0].ChangeType)
	}
}

func TestExtractFileOperations_ReadOp(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "Read", Input: `{"filePath": "/Users/me/dev/project/config.yaml"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].ChangeType != ChangeRead {
		t.Errorf("ChangeType = %q, want read", ops[0].ChangeType)
	}
}

func TestExtractFileOperations_MCPRead(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "mcp_read", Input: `{"filePath": "/Users/me/project/README.md"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].ChangeType != ChangeRead {
		t.Errorf("ChangeType = %q, want read", ops[0].ChangeType)
	}
}

func TestExtractFileOperations_ReadWithFilePath(t *testing.T) {
	// Some providers use file_path instead of filePath.
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "Read", Input: `{"file_path": "/tmp/test.txt"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].FilePath != "/tmp/test.txt" {
		t.Errorf("FilePath = %q", ops[0].FilePath)
	}
}

func TestExtractFileOperations_BashRM(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "Bash", Input: `{"command": "rm /tmp/old.log"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].ChangeType != ChangeDeleted {
		t.Errorf("ChangeType = %q, want deleted", ops[0].ChangeType)
	}
	if ops[0].FilePath != "/tmp/old.log" {
		t.Errorf("FilePath = %q", ops[0].FilePath)
	}
}

func TestExtractFileOperations_BashTouch(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "bash", Input: `{"command": "touch /tmp/new.txt"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].ChangeType != ChangeCreated {
		t.Errorf("ChangeType = %q, want created", ops[0].ChangeType)
	}
}

func TestExtractFileOperations_BashCat(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "bash", Input: `{"command": "cat /etc/hosts"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].ChangeType != ChangeRead {
		t.Errorf("ChangeType = %q, want read", ops[0].ChangeType)
	}
}

func TestExtractFileOperations_BashCP(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "bash", Input: `{"command": "cp /tmp/src.txt /tmp/dst.txt"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].FilePath != "/tmp/dst.txt" {
		t.Errorf("FilePath = %q, want /tmp/dst.txt", ops[0].FilePath)
	}
	if ops[0].ChangeType != ChangeCreated {
		t.Errorf("ChangeType = %q, want created", ops[0].ChangeType)
	}
}

func TestExtractFileOperations_BashSedInPlace(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "bash", Input: `{"command": "sed -i 's/foo/bar/g' /tmp/config.txt"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].ChangeType != ChangeModified {
		t.Errorf("ChangeType = %q, want modified", ops[0].ChangeType)
	}
	if ops[0].FilePath != "/tmp/config.txt" {
		t.Errorf("FilePath = %q", ops[0].FilePath)
	}
}

func TestExtractFileOperations_BashChainedCommands(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "bash", Input: `{"command": "touch /tmp/a.txt && rm /tmp/b.txt"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 2 {
		t.Fatalf("got %d ops, want 2", len(ops))
	}

	sort.Slice(ops, func(i, j int) bool { return ops[i].FilePath < ops[j].FilePath })

	if ops[0].FilePath != "/tmp/a.txt" || ops[0].ChangeType != ChangeCreated {
		t.Errorf("ops[0] = %+v", ops[0])
	}
	if ops[1].FilePath != "/tmp/b.txt" || ops[1].ChangeType != ChangeDeleted {
		t.Errorf("ops[1] = %+v", ops[1])
	}
}

func TestExtractFileOperations_MergeUpgrade(t *testing.T) {
	// Read then Edit on same file → should be "modified" (stronger).
	messages := []Message{
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				{Name: "Read", Input: `{"filePath": "/project/main.go"}`, State: ToolStateCompleted},
			},
		},
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				{Name: "Edit", Input: `{"filePath": "/project/main.go", "oldString": "a", "newString": "b"}`, State: ToolStateCompleted},
			},
		},
	}

	ops := ExtractFileOperations(messages)
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1 (deduplicated)", len(ops))
	}
	if ops[0].ChangeType != ChangeModified {
		t.Errorf("ChangeType = %q, want modified (upgraded from read)", ops[0].ChangeType)
	}
}

func TestExtractFileOperations_MergeNoDowngrade(t *testing.T) {
	// Write then Read on same file → should stay "created" (stronger).
	messages := []Message{
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				{Name: "Write", Input: `{"filePath": "/project/new.go", "content": "pkg"}`, State: ToolStateCompleted},
			},
		},
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				{Name: "Read", Input: `{"filePath": "/project/new.go"}`, State: ToolStateCompleted},
			},
		},
	}

	ops := ExtractFileOperations(messages)
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].ChangeType != ChangeCreated {
		t.Errorf("ChangeType = %q, want created (not downgraded to read)", ops[0].ChangeType)
	}
}

func TestExtractFileOperations_SkipsGlob(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "Glob", Input: `{"pattern": "**/*.go", "path": "/project"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 0 {
		t.Errorf("got %d ops, want 0 (Glob should be skipped)", len(ops))
	}
}

func TestExtractFileOperations_SkipsGrep(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "Grep", Input: `{"pattern": "TODO", "include": "*.go"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 0 {
		t.Errorf("got %d ops, want 0 (Grep should be skipped)", len(ops))
	}
}

func TestExtractFileOperations_SkipsPlaywright(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "mcp_playwright_browser_click", Input: `{"ref": "btn1"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 0 {
		t.Errorf("got %d ops, want 0 (playwright should be skipped)", len(ops))
	}
}

func TestExtractFileOperations_SkipsNotion(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "mcp_notionApi_API-post-search", Input: `{"query": "test"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 0 {
		t.Errorf("got %d ops, want 0 (Notion should be skipped)", len(ops))
	}
}

func TestExtractFileOperations_MultipleMessages(t *testing.T) {
	messages := []Message{
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				{Name: "Read", Input: `{"filePath": "/project/config.go"}`, State: ToolStateCompleted},
				{Name: "Read", Input: `{"filePath": "/project/main.go"}`, State: ToolStateCompleted},
			},
		},
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				{Name: "Edit", Input: `{"filePath": "/project/main.go", "oldString": "a", "newString": "b"}`, State: ToolStateCompleted},
				{Name: "Write", Input: `{"filePath": "/project/new.go", "content": "pkg"}`, State: ToolStateCompleted},
			},
		},
	}

	ops := ExtractFileOperations(messages)
	// config.go → read, main.go → modified (upgraded), new.go → created
	if len(ops) != 3 {
		t.Fatalf("got %d ops, want 3", len(ops))
	}

	opMap := make(map[string]ChangeType)
	for _, op := range ops {
		opMap[op.FilePath] = op.ChangeType
	}

	if opMap["/project/config.go"] != ChangeRead {
		t.Errorf("config.go = %q, want read", opMap["/project/config.go"])
	}
	if opMap["/project/main.go"] != ChangeModified {
		t.Errorf("main.go = %q, want modified", opMap["/project/main.go"])
	}
	if opMap["/project/new.go"] != ChangeCreated {
		t.Errorf("new.go = %q, want created", opMap["/project/new.go"])
	}
}

func TestExtractFileOperations_EmptyInput(t *testing.T) {
	ops := ExtractFileOperations(nil)
	if len(ops) != 0 {
		t.Errorf("got %d ops, want 0", len(ops))
	}
}

func TestExtractFileOperations_EmptyToolInput(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "Write", Input: "", State: ToolStateCompleted},
			{Name: "Edit", Input: `{}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 0 {
		t.Errorf("got %d ops, want 0 (no valid file paths)", len(ops))
	}
}

func TestExtractFileOperations_BashNoFileArgs(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "bash", Input: `{"command": "git status"}`, State: ToolStateCompleted},
			{Name: "bash", Input: `{"command": "ls -la"}`, State: ToolStateCompleted},
			{Name: "bash", Input: `{"command": "echo hello"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 0 {
		t.Errorf("got %d ops, want 0 (no file-like args in these commands)", len(ops))
	}
}

func TestExtractFileOperations_BashWithFlags(t *testing.T) {
	messages := []Message{{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{Name: "bash", Input: `{"command": "rm -rf /tmp/builddir/old.txt"}`, State: ToolStateCompleted},
		},
	}}

	ops := ExtractFileOperations(messages)
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].FilePath != "/tmp/builddir/old.txt" {
		t.Errorf("FilePath = %q", ops[0].FilePath)
	}
}

func TestFileOperationsToChanges(t *testing.T) {
	ops := []FileOperation{
		{FilePath: "/a.go", ChangeType: ChangeCreated},
		{FilePath: "/b.go", ChangeType: ChangeRead},
	}
	changes := FileOperationsToChanges(ops)
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want 2", len(changes))
	}
	if changes[0].ChangeType != ChangeCreated {
		t.Errorf("changes[0] = %q", changes[0].ChangeType)
	}
}

func TestComputeFileStats(t *testing.T) {
	ops := []FileOperation{
		{FilePath: "/project/main.go", ChangeType: ChangeModified},
		{FilePath: "/project/handler.go", ChangeType: ChangeCreated},
		{FilePath: "/project/config.yaml", ChangeType: ChangeRead},
		{FilePath: "/project/old.go", ChangeType: ChangeDeleted},
		{FilePath: "/project/sub/util.go", ChangeType: ChangeModified},
	}

	stats := ComputeFileStats(ops)

	if stats.TotalFiles != 5 {
		t.Errorf("TotalFiles = %d, want 5", stats.TotalFiles)
	}
	if stats.Created != 1 {
		t.Errorf("Created = %d, want 1", stats.Created)
	}
	if stats.Modified != 2 {
		t.Errorf("Modified = %d, want 2", stats.Modified)
	}
	if stats.Read != 1 {
		t.Errorf("Read = %d, want 1", stats.Read)
	}
	if stats.Deleted != 1 {
		t.Errorf("Deleted = %d, want 1", stats.Deleted)
	}
	if stats.ByExtension[".go"] != 4 {
		t.Errorf("ByExtension[.go] = %d, want 4", stats.ByExtension[".go"])
	}
	if stats.ByExtension[".yaml"] != 1 {
		t.Errorf("ByExtension[.yaml] = %d, want 1", stats.ByExtension[".yaml"])
	}
	if len(stats.WriteFiles) != 4 { // modified + created + deleted
		t.Errorf("WriteFiles len = %d, want 4", len(stats.WriteFiles))
	}
	if len(stats.ReadOnlyFiles) != 1 {
		t.Errorf("ReadOnlyFiles len = %d, want 1", len(stats.ReadOnlyFiles))
	}
}

func TestComputeFileStats_Empty(t *testing.T) {
	stats := ComputeFileStats(nil)
	if stats.TotalFiles != 0 {
		t.Errorf("TotalFiles = %d, want 0", stats.TotalFiles)
	}
}

// ── helpers ──

func TestNormalizeToolName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Write", "write"},
		{"mcp_write", "write"},
		{"Edit", "edit"},
		{"mcp_edit", "edit"},
		{"Read", "read"},
		{"mcp_read", "read"},
		{"Bash", "bash"},
		{"mcp_bash", "bash"},
		{"shell", "bash"},
		{"terminal", "bash"},
		{"execute_command", "bash"},
		{"Glob", ""},
		{"mcp_glob", ""},
		{"Grep", ""},
		{"mcp_grep", ""},
		{"mcp_playwright_browser_click", ""},
		{"mcp_notionApi_API-post-search", ""},
		{"mcp_sentry_search_issues", ""},
		{"mcp_langfuse-local_list_datasets", ""},
		{"mcp_context7_resolve-library-id", ""},
		{"unknown_tool", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeToolName(tt.input)
			if got != tt.want {
				t.Errorf("normalizeToolName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLooksLikeFilePath(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"/tmp/test.txt", true},
		{"src/main.go", true},
		{"file.txt", true},
		{"~/Documents/notes.md", true},
		{"-rf", false},
		{"--force", false},
		{"", false},
		{".", false},
		{"..", false},
		{"/", false},
		{"http://example.com", false},
		{"https://github.com/foo", false},
		{"hello", false},  // no slash, no dot
		{"git", false},    // bare word
		{"status", false}, // bare word
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := looksLikeFilePath(tt.input)
			if got != tt.want {
				t.Errorf("looksLikeFilePath(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestCleanFilePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/Users/me/file.go", "/Users/me/file.go"},
		{"  /tmp/test.txt  ", "/tmp/test.txt"},
		{"/path/with/trailing/", "/path/with/trailing"},
		{"/", "/"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := cleanFilePath(tt.input)
			if got != tt.want {
				t.Errorf("cleanFilePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestChangePriority(t *testing.T) {
	if changePriority(ChangeRead) >= changePriority(ChangeModified) {
		t.Error("read should be lower priority than modified")
	}
	if changePriority(ChangeModified) >= changePriority(ChangeCreated) {
		t.Error("modified should be lower priority than created")
	}
	if changePriority(ChangeCreated) >= changePriority(ChangeDeleted) {
		t.Error("created should be lower priority than deleted")
	}
}

func TestSplitBashCommands(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"touch a.txt && rm b.txt", 2},
		{"ls; pwd", 2},
		{"touch a.txt && rm b.txt; echo done", 3},
		{"simple command", 1},
		{"", 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitBashCommands(tt.input)
			if len(got) != tt.want {
				t.Errorf("splitBashCommands(%q) = %d parts, want %d", tt.input, len(got), tt.want)
			}
		})
	}
}
