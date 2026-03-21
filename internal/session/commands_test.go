package session

import (
	"testing"
)

func TestExtractBaseCommand_JSONInput(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		input    string
		expected string
	}{
		{"git status", "bash", `{"command": "git status --short"}`, "git"},
		{"ls", "Bash", `{"command": "ls -la /tmp"}`, "ls"},
		{"cd", "bash", `{"command": "cd /Users/me/dev"}`, "cd"},
		{"go test", "bash", `{"command": "go test ./..."}`, "go"},
		{"npm install", "bash", `{"command": "npm install express"}`, "npm"},
		{"docker build", "bash", `{"command": "docker build -t myapp ."}`, "docker"},
		{"gh pr", "bash", `{"command": "gh pr create --title hello"}`, "gh"},
		{"sudo apt", "bash", `{"command": "sudo apt install vim"}`, "apt"},
		{"env prefix", "bash", `{"command": "ENV_VAR=1 python script.py"}`, "python"},
		{"full path", "bash", `{"command": "/usr/bin/git status"}`, "git"},
		{"not bash", "read", `{"file_path": "main.go"}`, ""},
		{"empty command", "bash", `{"command": ""}`, ""},
		{"empty input", "bash", "", ""},
		{"shell tool", "shell", `{"command": "echo hello"}`, "echo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractBaseCommand(tt.tool, tt.input)
			if got != tt.expected {
				t.Errorf("ExtractBaseCommand(%q, %q) = %q, want %q", tt.tool, tt.input, got, tt.expected)
			}
		})
	}
}

func TestExtractBaseCommand_PlainInput(t *testing.T) {
	got := ExtractBaseCommand("bash", "ls -la")
	if got != "ls" {
		t.Errorf("got %q, want %q", got, "ls")
	}
}

func TestComputeCommandStats(t *testing.T) {
	messages := []Message{
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				{Name: "bash", Input: `{"command": "git status"}`, State: ToolStateCompleted},
				{Name: "bash", Input: `{"command": "git add ."}`, State: ToolStateCompleted},
				{Name: "bash", Input: `{"command": "go test ./..."}`, State: ToolStateError},
				{Name: "write", Input: `{"file_path": "main.go"}`, State: ToolStateCompleted},
			},
		},
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				{Name: "Bash", Input: `{"command": "ls -la"}`, State: ToolStateCompleted},
				{Name: "bash", Input: `{"command": "git push"}`, State: ToolStateCompleted},
			},
		},
	}

	stats := ComputeCommandStats(messages)

	if stats.TotalCommands != 5 {
		t.Errorf("TotalCommands = %d, want 5", stats.TotalCommands)
	}
	if stats.ByCommand["git"] != 3 {
		t.Errorf("git commands = %d, want 3", stats.ByCommand["git"])
	}
	if stats.ByCommand["go"] != 1 {
		t.Errorf("go commands = %d, want 1", stats.ByCommand["go"])
	}
	if stats.ByCommand["ls"] != 1 {
		t.Errorf("ls commands = %d, want 1", stats.ByCommand["ls"])
	}
	if stats.ErrorCommands != 1 {
		t.Errorf("ErrorCommands = %d, want 1", stats.ErrorCommands)
	}
}

func TestComputeImageStats(t *testing.T) {
	messages := []Message{
		{
			Role: RoleUser,
			Images: []ImageMeta{
				{MediaType: "image/png", SizeBytes: 50000, TokensEstimate: 200},
				{MediaType: "image/jpeg", SizeBytes: 30000, TokensEstimate: 150},
			},
		},
		{
			Role: RoleUser,
			Images: []ImageMeta{
				{MediaType: "image/png", SizeBytes: 80000, TokensEstimate: 400},
			},
		},
	}

	stats := ComputeImageStats(messages)

	if stats.Count != 3 {
		t.Errorf("Count = %d, want 3", stats.Count)
	}
	if stats.TotalBytes != 160000 {
		t.Errorf("TotalBytes = %d, want 160000", stats.TotalBytes)
	}
	if stats.TotalTokens != 750 {
		t.Errorf("TotalTokens = %d, want 750", stats.TotalTokens)
	}
}

func TestComputeCommandStats_Empty(t *testing.T) {
	stats := ComputeCommandStats(nil)
	if stats.TotalCommands != 0 {
		t.Errorf("TotalCommands = %d, want 0", stats.TotalCommands)
	}
}

func TestComputeImageStats_Empty(t *testing.T) {
	stats := ComputeImageStats(nil)
	if stats.Count != 0 {
		t.Errorf("Count = %d, want 0", stats.Count)
	}
}
