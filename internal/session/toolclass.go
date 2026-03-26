package session

import "strings"

// ClassifyTool determines the category of a tool based on its name.
//
// Categories:
//   - "builtin"       — standard provider tools (bash, Read, Write, Edit, etc.)
//   - "mcp:<server>"  — MCP tools, where <server> is the MCP server name
//     (e.g. "mcp:notion", "mcp:sentry", "mcp:langfuse")
//
// MCP tool naming conventions:
//   - Claude Code:  "mcp__<server>__<tool>"  (double underscore)
//   - OpenCode:     "<server>_<tool>" or "<server>-<tool>" with known prefixes
//
// When an MCP server cannot be identified, the category is "mcp:unknown".
func ClassifyTool(name string) (category string) {
	// Claude Code convention: mcp__servername__toolname
	if strings.HasPrefix(name, "mcp__") {
		parts := strings.SplitN(name, "__", 3)
		if len(parts) >= 2 {
			server := normalizeServerName(parts[1])
			return "mcp:" + server
		}
		return "mcp:unknown"
	}

	// Check known MCP tool prefixes (OpenCode naming conventions).
	for _, prefix := range knownMCPPrefixes {
		if strings.HasPrefix(name, prefix.prefix) {
			return "mcp:" + prefix.server
		}
	}

	return "builtin"
}

// MCPServerName extracts the MCP server name from a tool category.
// Returns empty string if the category is not an MCP category.
func MCPServerName(category string) string {
	if strings.HasPrefix(category, "mcp:") {
		return category[4:]
	}
	return ""
}

// IsMCPTool returns true if the tool name belongs to an MCP server.
func IsMCPTool(name string) bool {
	return ClassifyTool(name) != "builtin"
}

type mcpPrefix struct {
	prefix string
	server string
}

// knownMCPPrefixes maps tool name prefixes to their MCP server.
// Sorted by longest prefix first for accurate matching.
var knownMCPPrefixes = []mcpPrefix{
	// Notion API tools
	{"notionApi_", "notion"},

	// Sentry tools
	{"sentry_", "sentry"},

	// Langfuse tools (local and prod)
	{"langfuse-local_", "langfuse"},
	{"langfuse-prod_", "langfuse"},
	{"langfuse_", "langfuse"},

	// Context7 documentation tools
	{"context7_", "context7"},

	// Anthropic auth tools
	{"anthropic-max-auth_", "anthropic-auth"},
}

// normalizeServerName normalizes MCP server names for consistent grouping.
func normalizeServerName(name string) string {
	name = strings.ToLower(name)

	// Common aliases
	switch name {
	case "notionapi", "notion-api", "notion_api":
		return "notion"
	case "langfuse-local", "langfuse-prod", "langfuse_local", "langfuse_prod":
		return "langfuse"
	default:
		return name
	}
}
