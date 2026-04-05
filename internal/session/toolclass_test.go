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

// ── Extra MCP prefix tests ──

func TestSetExtraMCPPrefixes(t *testing.T) {
	defer session.ResetExtraMCPPrefixes()

	// Before: unknown tool is builtin.
	if got := session.ClassifyTool("myserver_do_thing"); got != "builtin" {
		t.Fatalf("before SetExtra: got %q, want builtin", got)
	}

	// Register extra prefix.
	session.SetExtraMCPPrefixes([]session.MCPPrefix{
		{Prefix: "myserver_", Server: "myserver"},
	})

	// After: recognized as MCP.
	if got := session.ClassifyTool("myserver_do_thing"); got != "mcp:myserver" {
		t.Errorf("after SetExtra: got %q, want mcp:myserver", got)
	}

	// Extras returned correctly.
	extras := session.ExtraMCPPrefixes()
	if len(extras) != 1 || extras[0].Prefix != "myserver_" {
		t.Errorf("ExtraMCPPrefixes() = %v, want [{myserver_ myserver}]", extras)
	}
}

func TestSetExtraMCPPrefixes_OverridesBuiltin(t *testing.T) {
	defer session.ResetExtraMCPPrefixes()

	// Extra prefixes take priority: re-map sentry_ to custom-sentry.
	session.SetExtraMCPPrefixes([]session.MCPPrefix{
		{Prefix: "sentry_", Server: "custom-sentry"},
	})

	if got := session.ClassifyTool("sentry_search_issues"); got != "mcp:custom-sentry" {
		t.Errorf("extra override: got %q, want mcp:custom-sentry", got)
	}
}

func TestSetExtraMCPPrefixes_SortedLongestFirst(t *testing.T) {
	defer session.ResetExtraMCPPrefixes()

	// Register two prefixes where one is a substring of the other.
	session.SetExtraMCPPrefixes([]session.MCPPrefix{
		{Prefix: "ab_", Server: "short"},
		{Prefix: "abc_", Server: "long"},
	})

	// The longer prefix should match first.
	if got := session.ClassifyTool("abc_tool"); got != "mcp:long" {
		t.Errorf("longest-first: got %q, want mcp:long", got)
	}
	if got := session.ClassifyTool("ab_tool"); got != "mcp:short" {
		t.Errorf("short match: got %q, want mcp:short", got)
	}
}

func TestRegisterMCPServerPrefixes(t *testing.T) {
	defer session.ResetExtraMCPPrefixes()

	session.RegisterMCPServerPrefixes([]string{"github-mcp", "linear"})

	// Auto-generated prefixes: "github-mcp_" and "linear_".
	if got := session.ClassifyTool("github-mcp_list_repos"); got != "mcp:github-mcp" {
		t.Errorf("auto-register: got %q, want mcp:github-mcp", got)
	}
	if got := session.ClassifyTool("linear_create_issue"); got != "mcp:linear" {
		t.Errorf("auto-register: got %q, want mcp:linear", got)
	}
}

func TestRegisterMCPServerPrefixes_SkipsExisting(t *testing.T) {
	defer session.ResetExtraMCPPrefixes()

	// "sentry" already has a built-in prefix "sentry_".
	session.RegisterMCPServerPrefixes([]string{"sentry", "newserver"})

	extras := session.ExtraMCPPrefixes()
	// Only "newserver_" should be added (sentry_ is built-in).
	if len(extras) != 1 {
		t.Fatalf("expected 1 extra prefix (newserver), got %d: %v", len(extras), extras)
	}
	if extras[0].Prefix != "newserver_" {
		t.Errorf("expected newserver_ prefix, got %q", extras[0].Prefix)
	}
}

func TestRegisterMCPServerPrefixes_MergesWithExtra(t *testing.T) {
	defer session.ResetExtraMCPPrefixes()

	// First set some extras.
	session.SetExtraMCPPrefixes([]session.MCPPrefix{
		{Prefix: "custom_", Server: "custom"},
	})

	// Then register from server names.
	session.RegisterMCPServerPrefixes([]string{"newmcp"})

	extras := session.ExtraMCPPrefixes()
	if len(extras) != 2 {
		t.Fatalf("expected 2 extras, got %d: %v", len(extras), extras)
	}

	// Both should work.
	if got := session.ClassifyTool("custom_tool"); got != "mcp:custom" {
		t.Errorf("custom_tool: got %q, want mcp:custom", got)
	}
	if got := session.ClassifyTool("newmcp_tool"); got != "mcp:newmcp" {
		t.Errorf("newmcp_tool: got %q, want mcp:newmcp", got)
	}
}

func TestResetExtraMCPPrefixes(t *testing.T) {
	session.SetExtraMCPPrefixes([]session.MCPPrefix{
		{Prefix: "temp_", Server: "temp"},
	})
	session.ResetExtraMCPPrefixes()

	if got := session.ClassifyTool("temp_tool"); got != "builtin" {
		t.Errorf("after reset: got %q, want builtin", got)
	}
	if extras := session.ExtraMCPPrefixes(); len(extras) != 0 {
		t.Errorf("after reset: extras = %v, want empty", extras)
	}
}
