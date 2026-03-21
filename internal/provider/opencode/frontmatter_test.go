package opencode

import (
	"testing"
)

func TestParseFrontmatter_WithHandoffs(t *testing.T) {
	content := `---
description: Create or update the feature specification.
handoffs:
  - label: Build Technical Plan
    agent: speckit.plan
    prompt: Create a plan for the spec.
  - label: Clarify Requirements
    agent: speckit.clarify
    prompt: Clarify spec
    send: true
---

## Content here
`

	fm, body := parseFrontmatter([]byte(content))

	if fm.Description != "Create or update the feature specification." {
		t.Errorf("description = %q", fm.Description)
	}

	if len(fm.Handoffs) != 2 {
		t.Fatalf("got %d handoffs, want 2", len(fm.Handoffs))
	}

	if fm.Handoffs[0].Label != "Build Technical Plan" {
		t.Errorf("handoff[0].Label = %q", fm.Handoffs[0].Label)
	}
	if fm.Handoffs[0].Agent != "speckit.plan" {
		t.Errorf("handoff[0].Agent = %q", fm.Handoffs[0].Agent)
	}
	if fm.Handoffs[0].Send != false {
		t.Errorf("handoff[0].Send = %v, want false", fm.Handoffs[0].Send)
	}

	if fm.Handoffs[1].Send != true {
		t.Errorf("handoff[1].Send = %v, want true", fm.Handoffs[1].Send)
	}

	if body != "## Content here" {
		t.Errorf("body = %q", body)
	}
}

func TestParseFrontmatter_WithTools(t *testing.T) {
	content := `---
description: Convert tasks to issues.
tools:
  - github/github-mcp-server:issue_write
  - simple_tool
---

Body text.
`

	fm, _ := parseFrontmatter([]byte(content))

	if len(fm.Tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(fm.Tools))
	}
	if fm.Tools[0] != "github/github-mcp-server:issue_write" {
		t.Errorf("tools[0] = %q", fm.Tools[0])
	}
}

func TestParseFrontmatter_NoFrontmatter(t *testing.T) {
	content := `# Just a Markdown file

No frontmatter here.
`

	fm, body := parseFrontmatter([]byte(content))

	if fm.Description != "" {
		t.Errorf("expected empty description, got %q", fm.Description)
	}
	if body != content {
		t.Errorf("body should be full content when no frontmatter")
	}
}

func TestParseFrontmatter_Empty(t *testing.T) {
	fm, body := parseFrontmatter([]byte(""))
	if fm.Description != "" {
		t.Errorf("expected empty description, got %q", fm.Description)
	}
	if body != "" {
		t.Errorf("expected empty body, got %q", body)
	}
}

func TestParseToolRef(t *testing.T) {
	tests := []struct {
		ref        string
		wantServer string
		wantTool   string
	}{
		{"github/github-mcp-server:issue_write", "github/github-mcp-server", "issue_write"},
		{"simple_tool", "", "simple_tool"},
		{"server:tool:extra", "server", "tool:extra"},
	}

	for _, tt := range tests {
		server, tool := parseToolRef(tt.ref)
		if server != tt.wantServer {
			t.Errorf("parseToolRef(%q) server = %q, want %q", tt.ref, server, tt.wantServer)
		}
		if tool != tt.wantTool {
			t.Errorf("parseToolRef(%q) tool = %q, want %q", tt.ref, tool, tt.wantTool)
		}
	}
}

func TestExtractTSDescription(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "double quoted",
			content: `export default tool({ description: "Pause execution for a specified duration", ...})`,
			want:    "Pause execution for a specified duration",
		},
		{
			name:    "single quoted",
			content: `description: 'Update the session title'`,
			want:    "Update the session title",
		},
		{
			name:    "no description",
			content: `export default function() {}`,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTSDescription(tt.content)
			if got != tt.want {
				t.Errorf("extractTSDescription() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractExposedTools(t *testing.T) {
	content := `
export default plugin({
	name: "worktree",
	tools: [
		tool("worktree_create", { ... }),
		tool("worktree_delete", { ... }),
	],
})
`

	tools := extractExposedTools(content)
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	if tools[0] != "worktree_create" {
		t.Errorf("tools[0] = %q, want worktree_create", tools[0])
	}
	if tools[1] != "worktree_delete" {
		t.Errorf("tools[1] = %q, want worktree_delete", tools[1])
	}
}
