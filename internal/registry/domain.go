// Package registry contains the domain types for the Agent Registry subdomain.
// It discovers and catalogs AI agent capabilities (agents, commands, skills,
// tools, plugins, MCP servers) across projects. These types form a separate
// bounded context linked to the Session BC via project_path.
//
// No interfaces live here — they are defined by the packages that own
// the abstraction (provider.RegistryScanner, service.RegistryService).
package registry

import (
	"path/filepath"
	"time"
)

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

// ProjectSnapshot is a point-in-time capture of a project's capabilities.
// Snapshots enable tracking capability evolution over time — detecting when
// MCP servers are added/removed, skills change, or configurations drift.
type ProjectSnapshot struct {
	// Identity
	ID          string `json:"id"`           // unique snapshot ID
	ProjectPath string `json:"project_path"` // absolute path

	// Captured data
	Project Project `json:"project"` // full project state at scan time

	// Metadata
	ScannedAt  string `json:"scanned_at"`  // RFC3339 timestamp
	ChangeType string `json:"change_type"` // "initial", "changed", "unchanged"

	// Delta summary (vs previous snapshot)
	CapabilitiesAdded   int `json:"capabilities_added,omitempty"`
	CapabilitiesRemoved int `json:"capabilities_removed,omitempty"`
	MCPServersAdded     int `json:"mcp_servers_added,omitempty"`
	MCPServersRemoved   int `json:"mcp_servers_removed,omitempty"`
}

// ── Persisted Capabilities (flat, queryable) ──

// PersistedCapability is a flat, per-row record of a capability discovered
// in a project. Unlike ProjectSnapshot (which stores the full project as a
// JSON blob), PersistedCapability stores one row per capability per project,
// enabling SQL queries like "which projects have the sentry MCP server?"
// or "when was skill X first seen?"
type PersistedCapability struct {
	// Identity
	ID          string `json:"id"`           // unique row ID
	ProjectPath string `json:"project_path"` // absolute path to project root

	// Capability details
	Name     string         `json:"name"`      // capability or MCP server name
	Kind     CapabilityKind `json:"kind"`      // agent, command, skill, tool, plugin, mcp_server
	Scope    Scope          `json:"scope"`     // global, profile, project
	IsActive bool           `json:"is_active"` // true if present in latest scan

	// Lifecycle timestamps
	FirstSeen time.Time `json:"first_seen"` // when first discovered
	LastSeen  time.Time `json:"last_seen"`  // when last confirmed present
}

// KindMCPServer is a pseudo-kind for MCP servers stored in the capabilities table.
const KindMCPServer CapabilityKind = "mcp_server"

// CapabilityFilter holds optional filters for querying persisted capabilities.
type CapabilityFilter struct {
	ProjectPath string         // filter by project (empty = all)
	Kind        CapabilityKind // filter by kind (empty = all)
	ActiveOnly  bool           // only return is_active == true
}

// ProjectCapabilityKeys extracts a deduplicated set of (name, kind, scope) tuples
// from a Project's capabilities and MCP servers. Used by the persistence layer
// to upsert flat capability rows.
func ProjectCapabilityKeys(p *Project) []PersistedCapability {
	seen := make(map[string]bool)
	var result []PersistedCapability

	for _, c := range p.Capabilities {
		key := string(c.Kind) + ":" + c.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, PersistedCapability{
			Name:  c.Name,
			Kind:  c.Kind,
			Scope: c.Scope,
		})
	}

	for _, m := range p.MCPServers {
		key := string(KindMCPServer) + ":" + m.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, PersistedCapability{
			Name:  m.Name,
			Kind:  KindMCPServer,
			Scope: m.Scope,
		})
	}

	return result
}

// ── Cross-Project MCP Governance Matrix (5.2) ──

// MCPGovernanceMatrix is a project × MCP server matrix showing the governance
// status of each server in each project (active, ghost, orphan, or absent).
type MCPGovernanceMatrix struct {
	Servers  []string                  `json:"servers"`  // unique MCP server names (column headers)
	Projects []MCPGovernanceProjectRow `json:"projects"` // one row per project
	// Summary counters.
	TotalActive  int `json:"total_active"`
	TotalGhost   int `json:"total_ghost"`
	TotalOrphan  int `json:"total_orphan"`
	TotalServers int `json:"total_servers"` // distinct MCP servers across all projects
}

// MCPGovernanceProjectRow is one row of the governance matrix (one project).
type MCPGovernanceProjectRow struct {
	ProjectPath string                       `json:"project_path"`
	DisplayName string                       `json:"display_name"`
	Cells       map[string]MCPGovernanceCell `json:"cells"` // server name → cell
	ActiveCount int                          `json:"active_count"`
	GhostCount  int                          `json:"ghost_count"`
	OrphanCount int                          `json:"orphan_count"`
	TotalCost   float64                      `json:"total_cost"`
}

// MCPGovernanceCell describes one MCP server's status within a single project.
type MCPGovernanceCell struct {
	Status    MCPUsageStatus `json:"status"`     // active, ghost, orphan
	CallCount int            `json:"call_count"` // 0 for ghosts
	TotalCost float64        `json:"total_cost"` // 0 for ghosts
}

// MCPGovernanceInput packages the inputs needed to build the governance matrix
// for a single project. This is a parameter object for BuildMCPGovernanceMatrix.
type MCPGovernanceInput struct {
	ProjectPath string
	DisplayName string
	Configured  []MCPServer
	Usage       []MCPUsageData
}

// BuildMCPGovernanceMatrix creates a cross-project governance matrix from
// per-project configured servers and usage data. This is a pure function — no I/O.
func BuildMCPGovernanceMatrix(inputs []MCPGovernanceInput) *MCPGovernanceMatrix {
	result := &MCPGovernanceMatrix{}
	serverSet := make(map[string]bool)

	for _, input := range inputs {
		cvuResult := AnalyzeConfiguredVsUsed(input.Configured, input.Usage)
		if cvuResult == nil {
			continue
		}

		row := MCPGovernanceProjectRow{
			ProjectPath: input.ProjectPath,
			DisplayName: input.DisplayName,
			Cells:       make(map[string]MCPGovernanceCell),
			ActiveCount: cvuResult.ActiveCount,
			GhostCount:  cvuResult.GhostCount,
			OrphanCount: cvuResult.OrphanCount,
		}

		for _, srv := range cvuResult.Servers {
			serverSet[srv.Name] = true
			row.Cells[srv.Name] = MCPGovernanceCell{
				Status:    srv.Status,
				CallCount: srv.CallCount,
				TotalCost: srv.TotalCost,
			}
			row.TotalCost += srv.TotalCost
		}

		result.TotalActive += cvuResult.ActiveCount
		result.TotalGhost += cvuResult.GhostCount
		result.TotalOrphan += cvuResult.OrphanCount
		result.Projects = append(result.Projects, row)
	}

	// Collect and sort server names.
	for name := range serverSet {
		result.Servers = append(result.Servers, name)
	}
	sortStrings(result.Servers)
	result.TotalServers = len(result.Servers)

	return result
}

// sortStrings is a minimal insertion sort for small string slices.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// ── Skill Reuse Map (1.5) ──

// SkillReuseMap classifies skills by their cross-project sharing pattern.
type SkillReuseMap struct {
	SharedSkills []SkillReuseEntry `json:"shared_skills"` // used in 2+ projects
	MonoSkills   []SkillReuseEntry `json:"mono_skills"`   // used in exactly 1 project
	IdleSkills   []SkillReuseEntry `json:"idle_skills"`   // configured but never loaded
	TotalSkills  int               `json:"total_skills"`  // total distinct skills
	TotalLoads   int               `json:"total_loads"`   // total skill loads across all projects
	SharedCount  int               `json:"shared_count"`
	MonoCount    int               `json:"mono_count"`
	IdleCount    int               `json:"idle_count"`
}

// SkillReuseEntry describes a single skill's reuse status across projects.
type SkillReuseEntry struct {
	Name         string   `json:"name"`
	Scope        Scope    `json:"scope"`         // global or project
	ProjectCount int      `json:"project_count"` // number of projects using it
	Projects     []string `json:"projects"`      // display names of projects
	TotalLoads   int      `json:"total_loads"`   // total load count across all projects
	TotalTokens  int      `json:"total_tokens"`  // total estimated context tokens consumed
}

// SkillUsageInput is the per-project skill usage data needed to build the reuse map.
type SkillUsageInput struct {
	ProjectPath string
	DisplayName string
	Configured  []string       // skill names from registry
	Usage       map[string]int // skill name → load count (from EventBucket.TopSkills)
	Tokens      map[string]int // skill name → total tokens (from EventBucket.SkillTokens)
}

// BuildSkillReuseMap creates a cross-project skill reuse classification.
// This is a pure function — no I/O.
func BuildSkillReuseMap(inputs []SkillUsageInput) *SkillReuseMap {
	result := &SkillReuseMap{}

	// Track per-skill data across projects.
	type skillData struct {
		scope       Scope
		projects    []string
		totalLoads  int
		totalTokens int
		configured  bool // appears in at least one registry
		loaded      bool // loaded in at least one session
	}
	skillMap := make(map[string]*skillData)

	for _, input := range inputs {
		// Mark configured skills.
		for _, name := range input.Configured {
			sd, ok := skillMap[name]
			if !ok {
				sd = &skillData{scope: ScopeGlobal}
				skillMap[name] = sd
			}
			sd.configured = true
		}

		// Mark used skills.
		for name, count := range input.Usage {
			sd, ok := skillMap[name]
			if !ok {
				sd = &skillData{scope: ScopeProject}
				skillMap[name] = sd
			}
			sd.loaded = true
			sd.projects = append(sd.projects, input.DisplayName)
			sd.totalLoads += count
			result.TotalLoads += count

			if tokens, ok := input.Tokens[name]; ok {
				sd.totalTokens += tokens
			}
		}
	}

	// Classify each skill.
	for name, sd := range skillMap {
		entry := SkillReuseEntry{
			Name:         name,
			Scope:        sd.scope,
			ProjectCount: len(sd.projects),
			Projects:     sd.projects,
			TotalLoads:   sd.totalLoads,
			TotalTokens:  sd.totalTokens,
		}

		if !sd.loaded {
			// Configured but never loaded anywhere.
			result.IdleSkills = append(result.IdleSkills, entry)
			result.IdleCount++
		} else if len(sd.projects) > 1 {
			result.SharedSkills = append(result.SharedSkills, entry)
			result.SharedCount++
		} else {
			result.MonoSkills = append(result.MonoSkills, entry)
			result.MonoCount++
		}
		result.TotalSkills++
	}

	// Sort by load count (descending) within each category.
	sortSkillEntries(result.SharedSkills)
	sortSkillEntries(result.MonoSkills)
	sortSkillEntries(result.IdleSkills)

	return result
}

func sortSkillEntries(entries []SkillReuseEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].TotalLoads > entries[j-1].TotalLoads; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}

// DiffSnapshots compares two project states and returns the number of capabilities
// and MCP servers that were added or removed. A nil prev means the project is new.
func DiffSnapshots(prev *Project, curr *Project) (capsAdded, capsRemoved, mcpAdded, mcpRemoved int) {
	if prev == nil {
		return len(curr.Capabilities), 0, len(curr.MCPServers), 0
	}

	// Capabilities diff.
	prevCaps := make(map[string]bool)
	for _, c := range prev.Capabilities {
		prevCaps[c.Name] = true
	}
	currCaps := make(map[string]bool)
	for _, c := range curr.Capabilities {
		currCaps[c.Name] = true
		if !prevCaps[c.Name] {
			capsAdded++
		}
	}
	for name := range prevCaps {
		if !currCaps[name] {
			capsRemoved++
		}
	}

	// MCP servers diff.
	prevMCP := make(map[string]bool)
	for _, s := range prev.MCPServers {
		prevMCP[s.Name] = true
	}
	currMCP := make(map[string]bool)
	for _, s := range curr.MCPServers {
		currMCP[s.Name] = true
		if !prevMCP[s.Name] {
			mcpAdded++
		}
	}
	for name := range prevMCP {
		if !currMCP[name] {
			mcpRemoved++
		}
	}

	return
}

// ── Configured vs Used Analysis ──

// MCPUsageStatus classifies an MCP server's relationship between configuration and usage.
type MCPUsageStatus string

const (
	// MCPStatusActive means the server is configured AND actively used.
	MCPStatusActive MCPUsageStatus = "active"

	// MCPStatusGhost means the server is configured but never used — wasted config.
	MCPStatusGhost MCPUsageStatus = "ghost"

	// MCPStatusOrphan means the server is used but not in the current config —
	// possibly removed after sessions were recorded, or from a different provider.
	MCPStatusOrphan MCPUsageStatus = "orphan"
)

// ConfiguredVsUsed is the result of comparing configured MCP servers against
// actual tool usage data. It identifies ghost servers (configured but unused)
// and orphan servers (used but not configured).
type ConfiguredVsUsed struct {
	// All MCP server entries with their status.
	Servers []MCPServerStatus `json:"servers"`

	// Summary counts.
	ActiveCount  int `json:"active_count"`  // configured + used
	GhostCount   int `json:"ghost_count"`   // configured but never used
	OrphanCount  int `json:"orphan_count"`  // used but not configured
	TotalCalls   int `json:"total_calls"`   // total MCP tool invocations
	GhostServers int `json:"ghost_servers"` // number of ghost MCP server configs (wasted)
}

// MCPServerStatus describes a single MCP server's configuration and usage state.
type MCPServerStatus struct {
	Name      string         `json:"name"`       // server name (e.g. "notion", "sentry")
	Status    MCPUsageStatus `json:"status"`     // active, ghost, orphan
	Scope     Scope          `json:"scope"`      // where configured (global, project) — empty for orphans
	Enabled   bool           `json:"enabled"`    // config enabled flag — false for orphans
	CallCount int            `json:"call_count"` // total tool invocations (0 for ghosts)
	ToolCount int            `json:"tool_count"` // distinct tools used (0 for ghosts)
	TotalCost float64        `json:"total_cost"` // estimated cost (0 for ghosts)
}

// MCPUsageData holds aggregated usage data for a single MCP server.
// This is an input to AnalyzeConfiguredVsUsed — computed elsewhere from tool buckets.
type MCPUsageData struct {
	Server    string  `json:"server"`
	CallCount int     `json:"call_count"`
	ToolCount int     `json:"tool_count"`
	TotalCost float64 `json:"total_cost"`
}

// AnalyzeConfiguredVsUsed produces a ConfiguredVsUsed report from configured
// MCP servers and actual usage data. This is a pure function — no I/O.
func AnalyzeConfiguredVsUsed(configured []MCPServer, usage []MCPUsageData) *ConfiguredVsUsed {
	result := &ConfiguredVsUsed{}

	// Index usage by server name.
	usageMap := make(map[string]*MCPUsageData, len(usage))
	for i := range usage {
		usageMap[usage[i].Server] = &usage[i]
	}

	// Track which usage entries were matched.
	matched := make(map[string]bool)

	// Check each configured server against usage.
	for _, cfg := range configured {
		entry := MCPServerStatus{
			Name:    cfg.Name,
			Scope:   cfg.Scope,
			Enabled: cfg.Enabled,
		}

		if ud, ok := usageMap[cfg.Name]; ok {
			entry.Status = MCPStatusActive
			entry.CallCount = ud.CallCount
			entry.ToolCount = ud.ToolCount
			entry.TotalCost = ud.TotalCost
			result.ActiveCount++
			result.TotalCalls += ud.CallCount
			matched[cfg.Name] = true
		} else {
			entry.Status = MCPStatusGhost
			result.GhostCount++
		}

		result.Servers = append(result.Servers, entry)
	}

	// Find orphan servers (used but not configured).
	for _, ud := range usage {
		if !matched[ud.Server] {
			result.Servers = append(result.Servers, MCPServerStatus{
				Name:      ud.Server,
				Status:    MCPStatusOrphan,
				CallCount: ud.CallCount,
				ToolCount: ud.ToolCount,
				TotalCost: ud.TotalCost,
			})
			result.OrphanCount++
			result.TotalCalls += ud.CallCount
		}
	}

	result.GhostServers = result.GhostCount
	return result
}

// ── Cross-Project Capabilities ──

// CrossProjectSummary aggregates capabilities across all known projects.
type CrossProjectSummary struct {
	ProjectCount      int                 `json:"project_count"`
	TotalCapabilities int                 `json:"total_capabilities"`
	TotalMCPServers   int                 `json:"total_mcp_servers"`  // distinct MCP servers across all projects
	SharedMCPServers  []SharedMCPServer   `json:"shared_mcp_servers"` // MCP servers used by multiple projects
	CapabilityMatrix  []CapabilityKindRow `json:"capability_matrix"`  // per-kind counts across projects
	ProjectOverviews  []ProjectOverview   `json:"project_overviews"`  // summary per project
}

// SharedMCPServer shows an MCP server used across multiple projects.
type SharedMCPServer struct {
	Name         string   `json:"name"`
	ProjectCount int      `json:"project_count"` // number of projects using this server
	Projects     []string `json:"projects"`      // display names of projects
	Scope        Scope    `json:"scope"`         // most common scope
}

// CapabilityKindRow aggregates a single capability kind across projects.
type CapabilityKindRow struct {
	Kind         CapabilityKind `json:"kind"`
	TotalCount   int            `json:"total_count"`   // total capabilities of this kind
	ProjectCount int            `json:"project_count"` // projects that have at least one
}

// ProjectOverview is a lightweight per-project summary for the cross-project view.
type ProjectOverview struct {
	Name            string `json:"name"`
	Path            string `json:"path"`
	CapabilityCount int    `json:"capability_count"`
	MCPServerCount  int    `json:"mcp_server_count"`
	SkillCount      int    `json:"skill_count"`
	AgentCount      int    `json:"agent_count"`
	CommandCount    int    `json:"command_count"`
}

// BuildCrossProjectSummary creates an aggregate view from multiple projects.
// This is a pure function — no I/O.
func BuildCrossProjectSummary(projects []Project) *CrossProjectSummary {
	result := &CrossProjectSummary{
		ProjectCount: len(projects),
	}

	mcpByName := make(map[string]*SharedMCPServer)
	kindCounts := make(map[CapabilityKind]struct{ total, projects int })
	kindProjectSeen := make(map[CapabilityKind]map[string]bool)

	for _, proj := range projects {
		overview := ProjectOverview{
			Name:            proj.Name,
			Path:            proj.RootPath,
			CapabilityCount: len(proj.Capabilities),
			MCPServerCount:  len(proj.MCPServers),
		}

		// Count capabilities by kind.
		for _, cap := range proj.Capabilities {
			result.TotalCapabilities++
			kc := kindCounts[cap.Kind]
			kc.total++
			kindCounts[cap.Kind] = kc

			if kindProjectSeen[cap.Kind] == nil {
				kindProjectSeen[cap.Kind] = make(map[string]bool)
			}
			kindProjectSeen[cap.Kind][proj.Name] = true

			switch cap.Kind {
			case KindSkill:
				overview.SkillCount++
			case KindAgent:
				overview.AgentCount++
			case KindCommand:
				overview.CommandCount++
			}
		}

		// Track MCP servers across projects.
		for _, mcp := range proj.MCPServers {
			shared, ok := mcpByName[mcp.Name]
			if !ok {
				shared = &SharedMCPServer{Name: mcp.Name, Scope: mcp.Scope}
				mcpByName[mcp.Name] = shared
			}
			shared.ProjectCount++
			shared.Projects = append(shared.Projects, proj.Name)
		}

		result.ProjectOverviews = append(result.ProjectOverviews, overview)
	}

	// Build capability kind rows.
	for _, kind := range allKinds {
		kc, ok := kindCounts[kind]
		if !ok {
			continue
		}
		result.CapabilityMatrix = append(result.CapabilityMatrix, CapabilityKindRow{
			Kind:         kind,
			TotalCount:   kc.total,
			ProjectCount: len(kindProjectSeen[kind]),
		})
	}

	// Build shared MCP servers list.
	result.TotalMCPServers = len(mcpByName)
	for _, shared := range mcpByName {
		result.SharedMCPServers = append(result.SharedMCPServers, *shared)
	}

	return result
}
