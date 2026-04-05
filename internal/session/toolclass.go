package session

import (
	"sort"
	"strings"
	"sync"
)

// MCPPrefix maps a tool name prefix to an MCP server name.
// Used by ClassifyTool to identify OpenCode-style MCP tool names
// (e.g. prefix "notionApi_" → server "notion").
type MCPPrefix struct {
	Prefix string `json:"prefix"`
	Server string `json:"server"`
}

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

	// Check extra (user-configured / auto-discovered) prefixes first —
	// they take priority over built-in defaults so users can override.
	extraMu.RLock()
	extras := extraMCPPrefixes
	extraMu.RUnlock()
	for _, prefix := range extras {
		if strings.HasPrefix(name, prefix.Prefix) {
			return "mcp:" + normalizeServerName(prefix.Server)
		}
	}

	// Check built-in MCP tool prefixes (OpenCode naming conventions).
	for _, prefix := range builtinMCPPrefixes {
		if strings.HasPrefix(name, prefix.Prefix) {
			return "mcp:" + prefix.Server
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

// SetExtraMCPPrefixes replaces the set of user-configured MCP prefixes.
// These are checked before the built-in defaults, allowing overrides.
// Thread-safe; typically called once at startup from config.
func SetExtraMCPPrefixes(prefixes []MCPPrefix) {
	// Sort by longest prefix first for accurate matching.
	sorted := make([]MCPPrefix, len(prefixes))
	copy(sorted, prefixes)
	sort.Slice(sorted, func(i, j int) bool {
		return len(sorted[i].Prefix) > len(sorted[j].Prefix)
	})
	extraMu.Lock()
	extraMCPPrefixes = sorted
	extraMu.Unlock()
}

// RegisterMCPServerPrefixes auto-generates prefix entries from MCP server names.
// For each server name (e.g. "my-mcp-server"), it registers the prefix
// "my-mcp-server_" → server "my-mcp-server". This handles the common OpenCode
// naming convention where tools are named "<server>_<tool>".
//
// Only servers not already covered by built-in or extra prefixes are added.
// Typically called at startup after registry scan.
func RegisterMCPServerPrefixes(serverNames []string) {
	extraMu.RLock()
	current := extraMCPPrefixes
	extraMu.RUnlock()

	// Build a set of already-known prefixes for dedup.
	known := make(map[string]bool, len(builtinMCPPrefixes)+len(current))
	for _, p := range builtinMCPPrefixes {
		known[p.Prefix] = true
	}
	for _, p := range current {
		known[p.Prefix] = true
	}

	var added []MCPPrefix
	for _, name := range serverNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		prefix := name + "_"
		if known[prefix] {
			continue
		}
		known[prefix] = true
		added = append(added, MCPPrefix{Prefix: prefix, Server: normalizeServerName(name)})
	}

	if len(added) == 0 {
		return
	}

	// Merge with existing extras and re-sort.
	merged := make([]MCPPrefix, 0, len(current)+len(added))
	merged = append(merged, current...)
	merged = append(merged, added...)
	sort.Slice(merged, func(i, j int) bool {
		return len(merged[i].Prefix) > len(merged[j].Prefix)
	})
	extraMu.Lock()
	extraMCPPrefixes = merged
	extraMu.Unlock()
}

// ExtraMCPPrefixes returns a copy of the currently registered extra prefixes.
// Useful for testing and diagnostics.
func ExtraMCPPrefixes() []MCPPrefix {
	extraMu.RLock()
	defer extraMu.RUnlock()
	out := make([]MCPPrefix, len(extraMCPPrefixes))
	copy(out, extraMCPPrefixes)
	return out
}

// ResetExtraMCPPrefixes clears all extra prefixes. Used in tests.
func ResetExtraMCPPrefixes() {
	extraMu.Lock()
	extraMCPPrefixes = nil
	extraMu.Unlock()
}

var (
	extraMu          sync.RWMutex
	extraMCPPrefixes []MCPPrefix
)

// builtinMCPPrefixes maps tool name prefixes to their MCP server.
// Sorted by longest prefix first for accurate matching.
var builtinMCPPrefixes = []MCPPrefix{
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
