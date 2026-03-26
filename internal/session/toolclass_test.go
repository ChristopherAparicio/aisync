package session_test

import (
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestClassifyTool(t *testing.T) {
	tests := []struct {
		name    string
		wantCat string
	}{
		// Built-in tools
		{"bash", "builtin"},
		{"Read", "builtin"},
		{"Write", "builtin"},
		{"Edit", "builtin"},
		{"Glob", "builtin"},
		{"Grep", "builtin"},
		{"task", "builtin"},
		{"webfetch", "builtin"},

		// Claude Code MCP convention: mcp__server__tool
		{"mcp__notion__search", "mcp:notion"},
		{"mcp__sentry__get_issue", "mcp:sentry"},
		{"mcp__langfuse__fetch_traces", "mcp:langfuse"},
		{"mcp__unknown-server__do_thing", "mcp:unknown-server"},

		// OpenCode / known prefix conventions
		{"notionApi_API-post-search", "mcp:notion"},
		{"notionApi_API-get-block-children", "mcp:notion"},
		{"sentry_search_issues", "mcp:sentry"},
		{"sentry_get_sentry_resource", "mcp:sentry"},
		{"langfuse-local_fetch_traces", "mcp:langfuse"},
		{"langfuse-prod_get_prompt", "mcp:langfuse"},
		{"langfuse_list_datasets", "mcp:langfuse"},
		{"context7_resolve-library-id", "mcp:context7"},
		{"context7_query-docs", "mcp:context7"},
		{"anthropic-max-auth_check_status", "mcp:anthropic-auth"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := session.ClassifyTool(tt.name)
			if got != tt.wantCat {
				t.Errorf("ClassifyTool(%q) = %q, want %q", tt.name, got, tt.wantCat)
			}
		})
	}
}

func TestIsMCPTool(t *testing.T) {
	if session.IsMCPTool("bash") {
		t.Error("bash should not be MCP")
	}
	if !session.IsMCPTool("notionApi_API-post-search") {
		t.Error("notionApi_API-post-search should be MCP")
	}
	if !session.IsMCPTool("mcp__sentry__get_issue") {
		t.Error("mcp__sentry__get_issue should be MCP")
	}
}

func TestMCPServerName(t *testing.T) {
	if got := session.MCPServerName("mcp:notion"); got != "notion" {
		t.Errorf("MCPServerName(mcp:notion) = %q, want notion", got)
	}
	if got := session.MCPServerName("builtin"); got != "" {
		t.Errorf("MCPServerName(builtin) = %q, want empty", got)
	}
}
