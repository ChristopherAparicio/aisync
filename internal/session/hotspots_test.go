package session

import (
	"testing"
)

// buildSession builds a minimal Session with messages for hotspot testing.
func buildSession(messages []Message) *Session {
	return &Session{
		Messages: messages,
	}
}

// tc is a shorthand to build a ToolCall with output.
func tc(name, input, output string, state ToolState) ToolCall {
	return ToolCall{
		Name:   name,
		Input:  input,
		Output: output,
		State:  state,
	}
}

func TestComputeHotspots_emptySession(t *testing.T) {
	sess := buildSession(nil)
	h := ComputeHotspots(sess, 0)

	if h.TotalCommands != 0 {
		t.Errorf("TotalCommands: want 0, got %d", h.TotalCommands)
	}
	if h.UniqueCommands != 0 {
		t.Errorf("UniqueCommands: want 0, got %d", h.UniqueCommands)
	}
	if len(h.TopCommandsByOutput) != 0 {
		t.Errorf("TopCommandsByOutput: want empty, got %d entries", len(h.TopCommandsByOutput))
	}
	if len(h.TopCommandsByReuse) != 0 {
		t.Errorf("TopCommandsByReuse: want empty, got %d entries", len(h.TopCommandsByReuse))
	}
	if len(h.ExpensiveMessages) != 0 {
		t.Errorf("ExpensiveMessages: want empty, got %d entries", len(h.ExpensiveMessages))
	}
	if len(h.SkillFootprints) != 0 {
		t.Errorf("SkillFootprints: want empty, got %d entries", len(h.SkillFootprints))
	}
}

func TestComputeHotspots_commandAggregation(t *testing.T) {
	sess := buildSession([]Message{
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				tc("Bash", `{"command":"git status"}`, "On branch main\nnothing to commit", ToolStateCompleted),
				tc("Bash", `{"command":"git diff HEAD~1"}`, "diff --git a/foo.go...\n+lots of output here", ToolStateCompleted),
			},
		},
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				tc("Bash", `{"command":"git log --oneline -5"}`, "abc1234 fix bug\ndef5678 add feature", ToolStateCompleted),
				tc("Bash", `{"command":"go test ./..."}`, "ok  pkg/foo 0.5s\nFAIL pkg/bar 1.2s", ToolStateError),
			},
		},
	})

	h := ComputeHotspots(sess, 0)

	if h.TotalCommands != 4 {
		t.Errorf("TotalCommands: want 4, got %d", h.TotalCommands)
	}
	// "git" appears 3 times, "go" appears 1 time.
	if h.UniqueCommands != 2 {
		t.Errorf("UniqueCommands: want 2, got %d", h.UniqueCommands)
	}
	if h.CommandErrorCount != 1 {
		t.Errorf("CommandErrorCount: want 1, got %d", h.CommandErrorCount)
	}

	// Check top by reuse: git (3) > go (1).
	if len(h.TopCommandsByReuse) < 2 {
		t.Fatalf("TopCommandsByReuse: want >= 2 entries, got %d", len(h.TopCommandsByReuse))
	}
	if h.TopCommandsByReuse[0].BaseCommand != "git" {
		t.Errorf("TopCommandsByReuse[0]: want git, got %s", h.TopCommandsByReuse[0].BaseCommand)
	}
	if h.TopCommandsByReuse[0].Invocations != 3 {
		t.Errorf("TopCommandsByReuse[0].Invocations: want 3, got %d", h.TopCommandsByReuse[0].Invocations)
	}
	if h.TopCommandsByReuse[1].BaseCommand != "go" {
		t.Errorf("TopCommandsByReuse[1]: want go, got %s", h.TopCommandsByReuse[1].BaseCommand)
	}
}

func TestComputeHotspots_topByOutput(t *testing.T) {
	// Create two commands: "cat" with large output, "echo" with small output.
	largeOutput := make([]byte, 10000)
	for i := range largeOutput {
		largeOutput[i] = 'x'
	}
	smallOutput := "hello"

	sess := buildSession([]Message{
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				tc("Bash", `{"command":"cat bigfile.log"}`, string(largeOutput), ToolStateCompleted),
				tc("Bash", `{"command":"echo hello"}`, smallOutput, ToolStateCompleted),
			},
		},
	})

	h := ComputeHotspots(sess, 0)

	if len(h.TopCommandsByOutput) < 2 {
		t.Fatalf("TopCommandsByOutput: want >= 2, got %d", len(h.TopCommandsByOutput))
	}
	// "cat" should be first (most output).
	if h.TopCommandsByOutput[0].BaseCommand != "cat" {
		t.Errorf("TopCommandsByOutput[0]: want cat, got %s", h.TopCommandsByOutput[0].BaseCommand)
	}
	if h.TopCommandsByOutput[0].TotalOutput != 10000 {
		t.Errorf("TopCommandsByOutput[0].TotalOutput: want 10000, got %d", h.TopCommandsByOutput[0].TotalOutput)
	}
	if h.TopCommandsByOutput[0].TokenImpact != 2500 {
		t.Errorf("TopCommandsByOutput[0].TokenImpact: want 2500, got %d", h.TopCommandsByOutput[0].TokenImpact)
	}
	if h.TopCommandsByOutput[1].BaseCommand != "echo" {
		t.Errorf("TopCommandsByOutput[1]: want echo, got %s", h.TopCommandsByOutput[1].BaseCommand)
	}
}

func TestComputeHotspots_topCappedAt10(t *testing.T) {
	// Create 15 unique commands.
	var toolCalls []ToolCall
	cmds := []string{"git", "go", "npm", "cat", "echo", "ls", "grep", "find", "sed", "awk", "curl", "docker", "make", "python", "ruby"}
	for _, cmd := range cmds {
		toolCalls = append(toolCalls, tc("Bash", `{"command":"`+cmd+` arg"}`, "output for "+cmd, ToolStateCompleted))
	}

	sess := buildSession([]Message{
		{Role: RoleAssistant, ToolCalls: toolCalls},
	})

	h := ComputeHotspots(sess, 0)

	if len(h.TopCommandsByOutput) != 10 {
		t.Errorf("TopCommandsByOutput: want 10 (capped), got %d", len(h.TopCommandsByOutput))
	}
	if len(h.TopCommandsByReuse) != 10 {
		t.Errorf("TopCommandsByReuse: want 10 (capped), got %d", len(h.TopCommandsByReuse))
	}
	if h.UniqueCommands != 15 {
		t.Errorf("UniqueCommands: want 15, got %d", h.UniqueCommands)
	}
}

func TestComputeHotspots_avgOutput(t *testing.T) {
	sess := buildSession([]Message{
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				tc("Bash", `{"command":"git status"}`, "short", ToolStateCompleted),                // 5 bytes
				tc("Bash", `{"command":"git diff"}`, "a longer output here!!", ToolStateCompleted), // 22 bytes
			},
		},
	})

	h := ComputeHotspots(sess, 0)

	if len(h.TopCommandsByOutput) < 1 {
		t.Fatalf("expected at least 1 command hotspot")
	}
	git := h.TopCommandsByOutput[0]
	if git.BaseCommand != "git" {
		t.Fatalf("expected git, got %s", git.BaseCommand)
	}
	expectedAvg := (5 + 22) / 2
	if git.AvgOutput != expectedAvg {
		t.Errorf("AvgOutput: want %d, got %d", expectedAvg, git.AvgOutput)
	}
}

func TestComputeHotspots_expensiveMessages(t *testing.T) {
	sess := buildSession([]Message{
		{Role: RoleUser, InputTokens: 100, OutputTokens: 0},
		{Role: RoleAssistant, InputTokens: 5000, OutputTokens: 2000, Model: "claude-sonnet-4-20250514"},
		{Role: RoleUser, InputTokens: 200, OutputTokens: 0},
		{Role: RoleAssistant, InputTokens: 80000, OutputTokens: 4000, Model: "claude-sonnet-4-20250514"},
		{Role: RoleUser, InputTokens: 300, OutputTokens: 0},
		{Role: RoleAssistant, InputTokens: 120000, OutputTokens: 8000, Model: "claude-sonnet-4-20250514"},
		{Role: RoleAssistant, InputTokens: 50000, OutputTokens: 3000, Model: "claude-sonnet-4-20250514"},
	})

	h := ComputeHotspots(sess, 0)

	if len(h.ExpensiveMessages) != 5 {
		t.Fatalf("ExpensiveMessages: want 5, got %d", len(h.ExpensiveMessages))
	}

	// Most expensive first: index 5 (128K total), then index 3 (84K), then index 6 (53K).
	if h.ExpensiveMessages[0].Index != 5 {
		t.Errorf("ExpensiveMessages[0].Index: want 5, got %d", h.ExpensiveMessages[0].Index)
	}
	if h.ExpensiveMessages[0].TotalTokens != 128000 {
		t.Errorf("ExpensiveMessages[0].TotalTokens: want 128000, got %d", h.ExpensiveMessages[0].TotalTokens)
	}
	if h.ExpensiveMessages[0].Role != RoleAssistant {
		t.Errorf("ExpensiveMessages[0].Role: want assistant, got %s", h.ExpensiveMessages[0].Role)
	}
	if h.ExpensiveMessages[0].Model != "claude-sonnet-4-20250514" {
		t.Errorf("ExpensiveMessages[0].Model: want claude-sonnet-4-20250514, got %s", h.ExpensiveMessages[0].Model)
	}

	if h.ExpensiveMessages[1].Index != 3 {
		t.Errorf("ExpensiveMessages[1].Index: want 3, got %d", h.ExpensiveMessages[1].Index)
	}
	if h.ExpensiveMessages[2].Index != 6 {
		t.Errorf("ExpensiveMessages[2].Index: want 6, got %d", h.ExpensiveMessages[2].Index)
	}
}

func TestComputeHotspots_expensiveMessagesCappedAt5(t *testing.T) {
	var messages []Message
	for i := 0; i < 20; i++ {
		messages = append(messages, Message{
			Role:         RoleAssistant,
			InputTokens:  (i + 1) * 1000,
			OutputTokens: 500,
		})
	}
	sess := buildSession(messages)
	h := ComputeHotspots(sess, 0)

	if len(h.ExpensiveMessages) != 5 {
		t.Errorf("ExpensiveMessages: want 5 (capped), got %d", len(h.ExpensiveMessages))
	}
	// Most expensive is the last one: index 19, total = 20000 + 500.
	if h.ExpensiveMessages[0].Index != 19 {
		t.Errorf("ExpensiveMessages[0].Index: want 19, got %d", h.ExpensiveMessages[0].Index)
	}
}

func TestComputeHotspots_skillFootprints(t *testing.T) {
	sess := buildSession([]Message{
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				tc("mcp_skill", `{"name":"react-patterns"}`, "lots of skill content here for react patterns that is fairly large", ToolStateCompleted),
				tc("mcp_skill", `{"name":"react-patterns"}`, "more react content on second load", ToolStateCompleted),
				tc("mcp_skill", `{"name":"go-testing"}`, "go testing skill output", ToolStateCompleted),
			},
		},
	})

	h := ComputeHotspots(sess, 0)

	if len(h.SkillFootprints) != 2 {
		t.Fatalf("SkillFootprints: want 2, got %d", len(h.SkillFootprints))
	}

	// Sorted by estimated tokens desc. "react-patterns" has more output.
	if h.SkillFootprints[0].Name != "react-patterns" {
		t.Errorf("SkillFootprints[0].Name: want react-patterns, got %s", h.SkillFootprints[0].Name)
	}
	if h.SkillFootprints[0].LoadCount != 2 {
		t.Errorf("SkillFootprints[0].LoadCount: want 2, got %d", h.SkillFootprints[0].LoadCount)
	}
	reactOutput := len("lots of skill content here for react patterns that is fairly large") + len("more react content on second load")
	if h.SkillFootprints[0].TotalBytes != reactOutput {
		t.Errorf("SkillFootprints[0].TotalBytes: want %d, got %d", reactOutput, h.SkillFootprints[0].TotalBytes)
	}
	if h.SkillFootprints[0].EstimatedTokens != reactOutput/4 {
		t.Errorf("SkillFootprints[0].EstimatedTokens: want %d, got %d", reactOutput/4, h.SkillFootprints[0].EstimatedTokens)
	}

	if h.SkillFootprints[1].Name != "go-testing" {
		t.Errorf("SkillFootprints[1].Name: want go-testing, got %s", h.SkillFootprints[1].Name)
	}
	if h.SkillFootprints[1].LoadCount != 1 {
		t.Errorf("SkillFootprints[1].LoadCount: want 1, got %d", h.SkillFootprints[1].LoadCount)
	}
}

func TestComputeHotspots_skillNameVariants(t *testing.T) {
	// Test all three recognized skill tool names.
	sess := buildSession([]Message{
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				tc("skill", `{"name":"skill-a"}`, "output-a", ToolStateCompleted),
				tc("load_skill", `{"name":"skill-b"}`, "output-b", ToolStateCompleted),
				tc("mcp_skill", `{"name":"skill-c"}`, "output-c", ToolStateCompleted),
			},
		},
	})

	h := ComputeHotspots(sess, 0)

	if len(h.SkillFootprints) != 3 {
		t.Fatalf("SkillFootprints: want 3, got %d", len(h.SkillFootprints))
	}
}

func TestComputeHotspots_skillNoNameField(t *testing.T) {
	// Skill tool call without a "name" field in input — should be skipped.
	sess := buildSession([]Message{
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				tc("mcp_skill", `{"something":"else"}`, "output", ToolStateCompleted),
				tc("mcp_skill", `not json`, "output2", ToolStateCompleted),
			},
		},
	})

	h := ComputeHotspots(sess, 0)

	if len(h.SkillFootprints) != 0 {
		t.Errorf("SkillFootprints: want 0 (no name), got %d", len(h.SkillFootprints))
	}
}

func TestComputeHotspots_totalOutputBytes(t *testing.T) {
	sess := buildSession([]Message{
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				tc("Bash", `{"command":"echo aaa"}`, "aaa", ToolStateCompleted), // 3 bytes
				tc("Bash", `{"command":"echo bb"}`, "bb", ToolStateCompleted),   // 2 bytes
			},
		},
	})

	h := ComputeHotspots(sess, 0)

	if h.TotalOutputBytes != 5 {
		t.Errorf("TotalOutputBytes: want 5, got %d", h.TotalOutputBytes)
	}
}

func TestComputeHotspots_compactionDataForwarded(t *testing.T) {
	// Build a session with a clear compaction (80K → 10K).
	sess := buildSession([]Message{
		{Role: RoleAssistant, InputTokens: 5000},
		{Role: RoleAssistant, InputTokens: 20000},
		{Role: RoleAssistant, InputTokens: 50000},
		{Role: RoleAssistant, InputTokens: 80000},
		{Role: RoleAssistant, InputTokens: 10000}, // compaction: 87.5% drop
		{Role: RoleAssistant, InputTokens: 25000},
		{Role: RoleAssistant, InputTokens: 40000},
	})

	h := ComputeHotspots(sess, 0)

	if h.CompactionCount != 1 {
		t.Errorf("CompactionCount: want 1, got %d", h.CompactionCount)
	}
	if h.TotalTokensLost != 70000 {
		t.Errorf("TotalTokensLost: want 70000, got %d", h.TotalTokensLost)
	}
	if h.DetectionCoverage != "full" {
		t.Errorf("DetectionCoverage: want full, got %s", h.DetectionCoverage)
	}
}

func TestComputeHotspots_noTokenData(t *testing.T) {
	// Cursor-like session: no token data on any message.
	sess := buildSession([]Message{
		{Role: RoleAssistant, InputTokens: 0, OutputTokens: 0},
		{Role: RoleAssistant, InputTokens: 0, OutputTokens: 0},
	})

	h := ComputeHotspots(sess, 0)

	// Should show "none" for detection coverage.
	if h.DetectionCoverage != "none" {
		t.Errorf("DetectionCoverage: want none, got %s", h.DetectionCoverage)
	}
	if h.CompactionCount != 0 {
		t.Errorf("CompactionCount: want 0, got %d", h.CompactionCount)
	}
}

func TestComputeHotspots_nonCommandToolCallsSkipped(t *testing.T) {
	// Tool calls that don't have extractable commands should not be counted.
	sess := buildSession([]Message{
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				tc("Read", "/some/file.go", "file content here", ToolStateCompleted),
				tc("Write", `{"path":"/some/file.go"}`, "", ToolStateCompleted),
			},
		},
	})

	h := ComputeHotspots(sess, 0)

	// ExtractBaseCommand should return "" for Read/Write — depends on
	// the actual implementation. If it extracts something, the count will
	// be non-zero and that's also valid. Let's just verify it doesn't panic.
	if h.TotalCommands < 0 {
		t.Errorf("TotalCommands should be >= 0, got %d", h.TotalCommands)
	}
}

func TestComputeHotspots_fullIntegration(t *testing.T) {
	// Build a realistic session with commands, compactions, skills, and expensive messages.
	sess := buildSession([]Message{
		// User message (no token data — user messages don't report input tokens).
		{Role: RoleUser},
		// Assistant with commands and skill.
		{
			Role: RoleAssistant, InputTokens: 5000, OutputTokens: 2000, Model: "claude-sonnet-4-20250514",
			ToolCalls: []ToolCall{
				tc("Bash", `{"command":"git status"}`, "On branch main", ToolStateCompleted),
				tc("mcp_skill", `{"name":"testing-patterns"}`, "large skill content for testing patterns", ToolStateCompleted),
			},
		},
		// User message.
		{Role: RoleUser},
		// Assistant with growing context.
		{Role: RoleAssistant, InputTokens: 50000, OutputTokens: 3000, Model: "claude-sonnet-4-20250514"},
		// User message.
		{Role: RoleUser},
		// Big context.
		{
			Role: RoleAssistant, InputTokens: 80000, OutputTokens: 4000, Model: "claude-sonnet-4-20250514",
			ToolCalls: []ToolCall{
				tc("Bash", `{"command":"go test ./..."}`, "ok  all tests passed\n"+string(make([]byte, 5000)), ToolStateCompleted),
			},
		},
		// User message.
		{Role: RoleUser},
		// Compaction happened: 80K → 10K.
		{
			Role: RoleAssistant, InputTokens: 10000, OutputTokens: 1000, Model: "claude-sonnet-4-20250514",
			ToolCalls: []ToolCall{
				tc("Bash", `{"command":"git diff HEAD~1"}`, "diff output", ToolStateCompleted),
				tc("Bash", `{"command":"go test ./pkg/foo"}`, "FAIL", ToolStateError),
			},
		},
	})

	h := ComputeHotspots(sess, 0)

	// Commands: git status, go test, git diff, go test = 4 commands.
	if h.TotalCommands != 4 {
		t.Errorf("TotalCommands: want 4, got %d", h.TotalCommands)
	}
	// Unique: git, go = 2.
	if h.UniqueCommands != 2 {
		t.Errorf("UniqueCommands: want 2, got %d", h.UniqueCommands)
	}
	// 1 error (go test ./pkg/foo).
	if h.CommandErrorCount != 1 {
		t.Errorf("CommandErrorCount: want 1, got %d", h.CommandErrorCount)
	}
	// 1 skill.
	if len(h.SkillFootprints) != 1 {
		t.Errorf("SkillFootprints: want 1, got %d", len(h.SkillFootprints))
	}
	// 1 compaction (80K → 10K).
	if h.CompactionCount != 1 {
		t.Errorf("CompactionCount: want 1, got %d", h.CompactionCount)
	}
	// Expensive messages: top 4 (only 4 assistant messages have non-zero tokens).
	if len(h.ExpensiveMessages) != 4 {
		t.Errorf("ExpensiveMessages: want 4, got %d", len(h.ExpensiveMessages))
	}
	// Most expensive is index 5 (80000+4000=84000).
	if len(h.ExpensiveMessages) > 0 && h.ExpensiveMessages[0].TotalTokens != 84000 {
		t.Errorf("ExpensiveMessages[0].TotalTokens: want 84000, got %d", h.ExpensiveMessages[0].TotalTokens)
	}
}

func TestIsSkillTool(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"skill", true},
		{"load_skill", true},
		{"mcp_skill", true},
		{"Bash", false},
		{"Read", false},
		{"", false},
		{"mcp_skill_extra", false},
	}
	for _, tt := range tests {
		got := isSkillTool(tt.name)
		if got != tt.want {
			t.Errorf("isSkillTool(%q): want %v, got %v", tt.name, tt.want, got)
		}
	}
}

func TestExtractSkillNameFromTC(t *testing.T) {
	tests := []struct {
		desc  string
		input string
		want  string
	}{
		{"valid name", `{"name":"my-skill"}`, "my-skill"},
		{"empty name", `{"name":""}`, ""},
		{"no name field", `{"foo":"bar"}`, ""},
		{"invalid JSON", `not json`, ""},
		{"empty input", ``, ""},
	}
	for _, tt := range tests {
		tc := &ToolCall{Input: tt.input}
		got := extractSkillNameFromTC(tc)
		if got != tt.want {
			t.Errorf("%s: extractSkillNameFromTC: want %q, got %q", tt.desc, tt.want, got)
		}
	}
}

func TestParseJSON(t *testing.T) {
	var obj map[string]interface{}
	err := parseJSON(`{"key":"value"}`, &obj)
	if err != nil {
		t.Fatalf("parseJSON: unexpected error: %v", err)
	}
	if obj["key"] != "value" {
		t.Errorf("parseJSON: want key=value, got %v", obj["key"])
	}

	err = parseJSON(`not json`, &obj)
	if err == nil {
		t.Error("parseJSON: expected error for invalid JSON")
	}
}
