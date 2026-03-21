// Package registry contains the domain types for the Agent Registry subdomain.
// It discovers and catalogs AI agent capabilities (agents, commands, skills,
// tools, plugins, MCP servers) across projects. These types form a separate
// bounded context linked to the Session BC via project_path.
//
// No interfaces live here — they are defined by the packages that own
// the abstraction (provider.RegistryScanner, service.RegistryService).
package registry

import "path/filepath"

// ── Enums ──

// CapabilityKind identifies the type of agent capability.
type CapabilityKind string

// Known capability kinds.
const (
	KindAgent   CapabilityKind = "agent"
	KindCommand CapabilityKind = "command"
	KindSkill   CapabilityKind = "skill"
	KindTool    CapabilityKind = "tool"
	KindPlugin  CapabilityKind = "plugin"
)

var allKinds = []CapabilityKind{
	KindAgent,
	KindCommand,
	KindSkill,
	KindTool,
	KindPlugin,
}

// Valid reports whether k is a known capability kind.
func (k CapabilityKind) Valid() bool {
	for _, v := range allKinds {
		if k == v {
			return true
		}
	}
	return false
}

// String returns the string representation.
func (k CapabilityKind) String() string {
	return string(k)
}

// Scope identifies where a capability is defined.
type Scope string

// Known scopes, ordered by resolution priority (lowest to highest).
const (
	ScopeGlobal  Scope = "global"
	ScopeProfile Scope = "profile"
	ScopeProject Scope = "project"
)

// String returns the string representation.
func (s Scope) String() string {
	return string(s)
}

// ── Entities & Value Objects ──

// Project is the aggregate root for the Agent Registry.
// It represents a single codebase with its discovered capabilities.
type Project struct {
	// ID is a stable identifier for the project (provider-specific, e.g. OpenCode project hash).
	ID string `json:"id"`

	// Name is the human-readable project name (typically basename of RootPath).
	Name string `json:"name"`

	// RootPath is the absolute path to the project root directory.
	RootPath string `json:"root_path"`

	// VCS is the version control system ("git" or "").
	VCS string `json:"vcs,omitempty"`

	// Capabilities contains all discovered capabilities across all scopes.
	Capabilities []Capability `json:"capabilities,omitempty"`

	// MCPServers contains configured MCP servers.
	MCPServers []MCPServer `json:"mcp_servers,omitempty"`

	// Profiles lists detected configuration profiles.
	Profiles []string `json:"profiles,omitempty"`
}

// CapabilityStats returns counts grouped by CapabilityKind.
func (p *Project) CapabilityStats() []CapabilityStat {
	counts := make(map[CapabilityKind]int)
	for _, c := range p.Capabilities {
		counts[c.Kind]++
	}

	stats := make([]CapabilityStat, 0, len(counts))
	// Iterate in deterministic order
	for _, kind := range allKinds {
		if count, ok := counts[kind]; ok {
			stats = append(stats, CapabilityStat{Kind: kind, Count: count})
		}
	}
	return stats
}

// FindCapability returns the first capability matching the given name.
// Returns nil if not found.
func (p *Project) FindCapability(name string) *Capability {
	for i := range p.Capabilities {
		if p.Capabilities[i].Name == name {
			return &p.Capabilities[i]
		}
	}
	return nil
}

// Capability represents a single agent capability discovered from config files.
type Capability struct {
	// Name is the capability identifier (e.g. "speckit.specify", "worktree", "session-status").
	Name string `json:"name"`

	// Kind identifies the type: agent, command, skill, tool, or plugin.
	Kind CapabilityKind `json:"kind"`

	// Scope indicates where the capability is defined: global, profile, or project.
	Scope Scope `json:"scope"`

	// Description is a human-readable summary (from YAML frontmatter or tool metadata).
	Description string `json:"description,omitempty"`

	// FilePath is the absolute path to the file defining this capability.
	FilePath string `json:"file_path,omitempty"`

	// Handoffs defines agent-to-agent routing for commands and agents.
	Handoffs []Handoff `json:"handoffs,omitempty"`

	// MCPTools lists MCP tools required by this capability.
	MCPTools []MCPToolRef `json:"mcp_tools,omitempty"`

	// ExposedTools lists tools provided by a plugin.
	ExposedTools []string `json:"exposed_tools,omitempty"`
}

// RelPath returns the file path relative to the given base directory.
// If the path cannot be made relative, it returns the absolute path.
func (c *Capability) RelPath(base string) string {
	rel, err := filepath.Rel(base, c.FilePath)
	if err != nil {
		return c.FilePath
	}
	return rel
}

// Handoff is a value object describing agent-to-agent routing.
type Handoff struct {
	// Label is the human-readable name shown in the UI (e.g. "Build Technical Plan").
	Label string `json:"label"`

	// Target is the agent/command to hand off to (e.g. "speckit.plan").
	Target string `json:"target"`

	// Prompt is the initial prompt text sent on handoff.
	Prompt string `json:"prompt,omitempty"`

	// Send indicates whether the handoff auto-sends (true) or just populates the prompt (false).
	Send bool `json:"send,omitempty"`
}

// MCPServer is a value object describing a configured MCP server.
type MCPServer struct {
	// Name is the server identifier (e.g. "langfuse-local", "sentry").
	Name string `json:"name"`

	// Type is "local" (stdio) or "remote" (SSE/streamable-http).
	Type string `json:"type,omitempty"`

	// Scope indicates where the server is configured.
	Scope Scope `json:"scope"`

	// Enabled indicates whether the server is active.
	Enabled bool `json:"enabled"`
}

// MCPToolRef is a value object referencing an MCP tool by server and tool name.
type MCPToolRef struct {
	// Server is the MCP server providing the tool (e.g. "github/github-mcp-server").
	Server string `json:"server"`

	// Tool is the specific tool name (e.g. "issue_write").
	Tool string `json:"tool"`
}

// ── Read Models (computed, not stored) ──

// ProjectView is an enriched read model that combines capability data
// with session cost statistics from the Store.
type ProjectView struct {
	Project

	// SessionCount is the number of captured sessions for this project.
	SessionCount int `json:"session_count"`

	// TotalCost is the estimated total cost (USD) across all sessions.
	TotalCost float64 `json:"total_cost,omitempty"`

	// TotalTokens is the aggregate token count across all sessions.
	TotalTokens int64 `json:"total_tokens"`
}

// CapabilityStat groups capability counts by kind.
type CapabilityStat struct {
	Kind  CapabilityKind `json:"kind"`
	Count int            `json:"count"`
}
