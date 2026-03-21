# Agent Registry — Architecture

> Last updated: 2026-03-11

## Overview

The Agent Registry is a **subdomain within aisync** that discovers and catalogs AI agent capabilities (agents, commands, skills, tools, plugins, MCP servers) across projects. It provides a **viewer** that reads directly from the filesystem — no persistence required — and enriches the view with session cost data from the existing Store.

## Motivation

aisync already captures **sessions** (who did what, when, at what cost). But it has no visibility into **what capabilities were available** to the agent during those sessions. The Agent Registry fills this gap by scanning provider-specific configuration files and building a unified model of all capabilities per project.

Use cases:
- "What agents/commands/skills are configured for this project?"
- "Show me the dependency graph between commands (handoffs)"
- "How much have sessions in this project cost, broken down by capability?"
- "Compare capability sets across projects"

## Domain Model

The Agent Registry introduces a new bounded context in `internal/registry/` with its own types. It is **linked** to the Session bounded context via `project_path`.

### Entities & Value Objects

```
Project (Aggregate Root)
├── ID            string           (OpenCode project hash, or derived)
├── Name          string           (human-readable: basename of RootPath)
├── RootPath      string           (absolute path to project root)
├── VCS           string           ("git" or "")
├── Capabilities  []Capability     (all discovered capabilities)
├── MCPServers    []MCPServer      (configured MCP servers)
└── Profiles      []string         (detected profiles)

Capability (Entity)
├── Name          string           ("speckit.specify", "worktree", "session-status")
├── Kind          CapabilityKind   (agent | command | skill | tool | plugin)
├── Scope         Scope            (global | profile | project)
├── Description   string           (from YAML frontmatter or tool description)
├── FilePath      string           (where it's defined on disk)
├── Handoffs      []Handoff        (agent-to-agent routing, for commands/agents)
├── MCPTools      []MCPToolRef     (required MCP tools, e.g. github/issue_write)
└── ExposedTools  []string         (tools provided by a plugin)

Handoff (Value Object)
├── Label         string           ("Build Technical Plan")
├── Target        string           ("speckit.plan")
├── Prompt        string           (initial prompt text)
└── Send          bool             (auto-send on handoff)

MCPServer (Value Object)
├── Name          string           ("langfuse-local", "sentry")
├── Type          string           ("local" | "remote")
├── Scope         Scope
└── Enabled       bool

MCPToolRef (Value Object)
├── Server        string           ("github/github-mcp-server")
└── Tool          string           ("issue_write")
```

### Enums

```go
type CapabilityKind string  // "agent", "command", "skill", "tool", "plugin"
type Scope          string  // "global", "profile", "project"
```

### Enriched View (computed, not stored)

```
ProjectView (read model, computed at query time)
├── Project                        (base entity)
├── SessionCount   int             (from Store)
├── TotalCost      float64         (from Store + pricing)
├── TotalTokens    int64           (from Store)
└── CapabilityStats []CapabilityStat

CapabilityStat
├── Kind           CapabilityKind
└── Count          int
```

## Port: RegistryScanner

Each AI provider knows how to discover capabilities from its own configuration format. The `RegistryScanner` interface is defined alongside the existing `Provider` interface in `internal/provider/`:

```go
// RegistryScanner discovers agent capabilities from a provider's config files.
// This is the second facet of a provider — Provider reads sessions,
// RegistryScanner reads capability definitions.
type RegistryScanner interface {
    // ScanProject discovers capabilities in a project directory.
    ScanProject(projectPath string) ([]registry.Capability, error)

    // ScanGlobal discovers capabilities from global/user-level config.
    ScanGlobal() ([]registry.Capability, []registry.MCPServer, error)
}
```

### Why it lives in `provider/`

The scanner is **the same concept as a Provider** — it reads provider-specific files from the filesystem. The existing `Provider` interface reads sessions; `RegistryScanner` reads capabilities. Both are driven adapters, both are indexed by provider name, and both are discovered the same way.

## Adapters

### OpenCode Scanner (`provider/opencode/registry.go`)

Scans:
- **Project-level**: `.opencode/command/*.md`, `.opencode/agent/*.md`, `.opencode/skill/*/SKILL.md`, `.opencode/tool/*.ts`
- **Global**: `~/.config/opencode/commands/*.md`, `~/.config/opencode/skills/*/SKILL.md`, `~/.config/opencode/tools/*.ts`, `~/.config/opencode/plugins/*.ts`
- **MCP servers**: `~/.config/opencode/opencode.json` → `mcp` section
- **Profiles**: `~/.config/opencode/profiles/*/`

Parses:
- YAML frontmatter in Markdown files (description, handoffs, tools)
- JSON/JSONC config files (MCP servers, plugin declarations)
- TypeScript tool files (description from `tool({description: ...})`)

### Claude Code Scanner (`provider/claude/registry.go`)

Scans:
- **Project-level**: `.claude/` directory, `CLAUDE.md` (custom instructions)
- **Global**: `~/.claude/claude_desktop_config.json` (MCP servers)
- **Commands**: `.claude/commands/*.md`

### Cursor Scanner (`provider/cursor/registry.go`)

Scans:
- **Project-level**: `.cursor/rules/`, `.cursorrules`
- **MCP**: `.cursor/mcp.json`

## Service Layer

```go
// service/registry.go

type RegistryService struct {
    scanners []provider.RegistryScanner
    store    storage.Store        // for session cost enrichment
    pricing  *pricing.Calculator  // for cost computation
}

// ScanProject returns the full capability set for a project,
// merging global + project-level capabilities.
func (s *RegistryService) ScanProject(projectPath string) (*registry.Project, error)

// ListProjects discovers all registered projects and their capabilities.
func (s *RegistryService) ListProjects() ([]registry.Project, error)

// GetProjectView returns a project with session cost enrichment.
func (s *RegistryService) GetProjectView(projectPath string) (*registry.ProjectView, error)
```

The `RegistryService`:
1. Iterates over all `RegistryScanner` adapters
2. Merges capabilities with scope resolution (project overrides global)
3. Joins with `Store.Search()` to compute cost/token stats per project
4. Returns the enriched `ProjectView`

## CLI Commands

```
aisync agents                          List all projects with capability counts
aisync agents tree [--project <path>]  Capability tree (global + project)
aisync agents show <name>              Detail of a specific capability
aisync agents --json                   JSON output for automation
```

Located in `pkg/cmd/agentscmd/`.

## MCP Tools

```
aisync_agents_list     → RegistryService.ListProjects()
aisync_agents_tree     → RegistryService.ScanProject()
aisync_agents_show     → RegistryService.ScanProject() + filter by name
```

## Integration Points

### With existing Session BC

The link is `project_path`:
- `Store.Search({ProjectPath: project.RootPath})` → sessions for this project
- `pricing.SessionCost(session)` → cost per session
- Aggregated into `ProjectView.TotalCost`, `ProjectView.SessionCount`

### With existing Provider registry

The `RegistryScanner` adapters are registered alongside `Provider` adapters in the composition root (`pkg/cmd/factory/default.go`). A provider can implement both interfaces or just one.

## Design Decisions

| Decision | Rationale |
|----------|-----------|
| **Filesystem reader, not persisted** | Capabilities change with code. Scanning live ensures freshness. No migration needed. |
| **Same `provider/` package** | RegistryScanner is a second facet of a provider, not a new concept. Keeps adapters colocated. |
| **Separate domain package** | `internal/registry/` has its own types. Clean bounded context boundary. |
| **Service uses Store** | Cost enrichment reuses the existing session storage. No new tables. |
| **YAML frontmatter parser** | OpenCode commands/agents use YAML frontmatter. Simple regex + yaml.Unmarshal. |
| **Scope resolution** | Global < Profile < Project. Same model as OpenCode's own config resolution. |

## File Structure

```
internal/
  registry/
    domain.go              # Project, Capability, Handoff, MCPServer, enums

  provider/
    registry.go            # RegistryScanner interface + ScannerRegistry
    opencode/
      registry.go          # OpenCode RegistryScanner adapter
      frontmatter.go       # YAML frontmatter parser (shared)
    claude/
      registry.go          # Claude Code RegistryScanner adapter
    cursor/
      registry.go          # Cursor RegistryScanner adapter

  service/
    registry.go            # RegistryService (orchestration + cost enrichment)

pkg/cmd/
  agentscmd/
    agents.go              # Root 'aisync agents' command
    list.go                # 'aisync agents list' (default)
    tree.go                # 'aisync agents tree'
    show.go                # 'aisync agents show <name>'

internal/mcp/
  registry_tools.go        # MCP tools for agent registry
```

## Future Evolution

- **Snapshot to SQLite**: Persist capability snapshots over time to track how a project's agent setup evolves.
- **Web dashboard**: Add capability cards to the existing `aisync web` dashboard.
- **Team view**: When teams/permissions exist, show which team members have access to which capabilities.
- **Diff**: Compare capability sets between two projects or two points in time.
