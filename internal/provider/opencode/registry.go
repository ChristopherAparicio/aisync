package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/registry"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// Scanner implements provider.RegistryScanner for OpenCode.
// It discovers capabilities from OpenCode's file-based config:
//   - Project:  .opencode/command/*.md, .opencode/agent/*.md,
//     .opencode/skill/*/SKILL.md, .opencode/tool/*.ts
//   - Global:   ~/.config/opencode/commands/*.md, skills/*/SKILL.md,
//     tools/*.ts, plugins/*.ts
//   - MCP:      ~/.config/opencode/opencode.json → mcp section
//   - Profiles: ~/.config/opencode/profiles/*/
type Scanner struct {
	// configHome overrides ~/.config/opencode for testing.
	configHome string
}

// NewScanner creates an OpenCode registry scanner.
// If configHome is empty, it defaults to ~/.config/opencode.
func NewScanner(configHome string) *Scanner {
	if configHome == "" {
		configHome = defaultConfigHome()
	}
	return &Scanner{configHome: configHome}
}

// ProviderName returns the provider this scanner belongs to.
func (s *Scanner) ProviderName() session.ProviderName {
	return session.ProviderOpenCode
}

// ScanProject discovers capabilities in a project's .opencode/ directory.
func (s *Scanner) ScanProject(projectPath string) ([]registry.Capability, error) {
	ocDir := filepath.Join(projectPath, ".opencode")
	if _, err := os.Stat(ocDir); os.IsNotExist(err) {
		return nil, nil // no .opencode directory — not an error
	}

	var caps []registry.Capability

	// Commands: .opencode/command/*.md
	cmdCaps, _ := s.scanMarkdownDir(filepath.Join(ocDir, "command"), registry.KindCommand, registry.ScopeProject)
	caps = append(caps, cmdCaps...)

	// Agents: .opencode/agent/*.md
	agentCaps, _ := s.scanMarkdownDir(filepath.Join(ocDir, "agent"), registry.KindAgent, registry.ScopeProject)
	caps = append(caps, agentCaps...)

	// Skills: .opencode/skill/*/SKILL.md
	skillCaps, _ := s.scanSkillDir(filepath.Join(ocDir, "skill"), registry.ScopeProject)
	caps = append(caps, skillCaps...)

	// Tools: .opencode/tool/*.ts
	toolCaps, _ := s.scanToolDir(filepath.Join(ocDir, "tool"), registry.KindTool, registry.ScopeProject)
	caps = append(caps, toolCaps...)

	return caps, nil
}

// ScanGlobal discovers capabilities from the global OpenCode config.
func (s *Scanner) ScanGlobal() ([]registry.Capability, []registry.MCPServer, error) {
	var caps []registry.Capability

	// Commands: ~/.config/opencode/commands/*.md
	cmdCaps, _ := s.scanMarkdownDir(filepath.Join(s.configHome, "commands"), registry.KindCommand, registry.ScopeGlobal)
	caps = append(caps, cmdCaps...)

	// Skills: ~/.config/opencode/skills/*/SKILL.md
	skillCaps, _ := s.scanSkillDir(filepath.Join(s.configHome, "skills"), registry.ScopeGlobal)
	caps = append(caps, skillCaps...)

	// Tools: ~/.config/opencode/tools/*.ts
	toolCaps, _ := s.scanToolDir(filepath.Join(s.configHome, "tools"), registry.KindTool, registry.ScopeGlobal)
	caps = append(caps, toolCaps...)

	// Plugins: ~/.config/opencode/plugins/*.ts
	pluginCaps, _ := s.scanToolDir(filepath.Join(s.configHome, "plugins"), registry.KindPlugin, registry.ScopeGlobal)
	caps = append(caps, pluginCaps...)

	// MCP servers: ~/.config/opencode/opencode.json
	servers, _ := s.scanMCPServers()

	// Profiles: scan each profile directory
	profileCaps, _ := s.scanProfiles()
	caps = append(caps, profileCaps...)

	return caps, servers, nil
}

// ── Internal scanning methods ──

// scanMarkdownDir scans a directory of Markdown files with YAML frontmatter.
// Used for commands (.md) and agents (.md).
func (s *Scanner) scanMarkdownDir(dir string, kind registry.CapabilityKind, scope registry.Scope) ([]registry.Capability, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var caps []registry.Capability
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		filePath := filepath.Join(dir, entry.Name())
		content, readErr := os.ReadFile(filePath)
		if readErr != nil {
			continue
		}

		fm, _ := parseFrontmatter(content)
		name := strings.TrimSuffix(entry.Name(), ".md")

		cap := registry.Capability{
			Name:        name,
			Kind:        kind,
			Scope:       scope,
			Description: fm.Description,
			FilePath:    filePath,
		}

		// Parse handoffs
		for _, h := range fm.Handoffs {
			cap.Handoffs = append(cap.Handoffs, registry.Handoff{
				Label:  h.Label,
				Target: h.Agent,
				Prompt: h.Prompt,
				Send:   h.Send,
			})
		}

		// Parse tool references
		allTools := append(fm.Tools, fm.AllowedTool...)
		for _, ref := range allTools {
			server, tool := parseToolRef(ref)
			cap.MCPTools = append(cap.MCPTools, registry.MCPToolRef{
				Server: server,
				Tool:   tool,
			})
		}

		caps = append(caps, cap)
	}

	return caps, nil
}

// scanSkillDir scans a skill directory (skill/*/SKILL.md).
func (s *Scanner) scanSkillDir(dir string, scope registry.Scope) ([]registry.Capability, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var caps []registry.Capability
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillFile := filepath.Join(dir, entry.Name(), "SKILL.md")
		content, readErr := os.ReadFile(skillFile)
		if readErr != nil {
			continue
		}

		fm, _ := parseFrontmatter(content)

		cap := registry.Capability{
			Name:        entry.Name(),
			Kind:        registry.KindSkill,
			Scope:       scope,
			Description: fm.Description,
			FilePath:    skillFile,
		}

		caps = append(caps, cap)
	}

	return caps, nil
}

// scanToolDir scans a directory of TypeScript tool/plugin files.
// Extracts the name from the filename and attempts to find a description
// from the tool() or plugin() call pattern.
func (s *Scanner) scanToolDir(dir string, kind registry.CapabilityKind, scope registry.Scope) ([]registry.Capability, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var caps []registry.Capability
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ts") {
			continue
		}

		filePath := filepath.Join(dir, entry.Name())
		content, readErr := os.ReadFile(filePath)
		if readErr != nil {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".ts")
		desc := extractTSDescription(string(content))

		cap := registry.Capability{
			Name:        name,
			Kind:        kind,
			Scope:       scope,
			Description: desc,
			FilePath:    filePath,
		}

		// For plugins, try to extract exposed tools
		if kind == registry.KindPlugin {
			cap.ExposedTools = extractExposedTools(string(content))
		}

		caps = append(caps, cap)
	}

	return caps, nil
}

// scanMCPServers reads MCP server configuration from opencode.json.
func (s *Scanner) scanMCPServers() ([]registry.MCPServer, error) {
	configFile := filepath.Join(s.configHome, "opencode.json")
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}

	var config struct {
		MCP map[string]struct {
			Type    string `json:"type"`
			Enabled *bool  `json:"enabled"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	var servers []registry.MCPServer
	for name, cfg := range config.MCP {
		enabled := true
		if cfg.Enabled != nil {
			enabled = *cfg.Enabled
		}
		serverType := cfg.Type
		if serverType == "" {
			serverType = "local"
		}
		servers = append(servers, registry.MCPServer{
			Name:    name,
			Type:    serverType,
			Scope:   registry.ScopeGlobal,
			Enabled: enabled,
		})
	}

	return servers, nil
}

// scanProfiles discovers capabilities from profile directories.
func (s *Scanner) scanProfiles() ([]registry.Capability, error) {
	profilesDir := filepath.Join(s.configHome, "profiles")
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		return nil, err
	}

	var caps []registry.Capability
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		profileDir := filepath.Join(profilesDir, entry.Name())

		// Scan commands in profile
		cmdCaps, _ := s.scanMarkdownDir(filepath.Join(profileDir, "commands"), registry.KindCommand, registry.ScopeProfile)
		caps = append(caps, cmdCaps...)

		// Scan agents in profile (AGENTS.md is a single file, not a directory)
		agentsFile := filepath.Join(profileDir, "AGENTS.md")
		if _, statErr := os.Stat(agentsFile); statErr == nil {
			content, readErr := os.ReadFile(agentsFile)
			if readErr == nil {
				fm, _ := parseFrontmatter(content)
				caps = append(caps, registry.Capability{
					Name:        entry.Name() + "-agents",
					Kind:        registry.KindAgent,
					Scope:       registry.ScopeProfile,
					Description: fm.Description,
					FilePath:    agentsFile,
				})
			}
		}

		// Scan skills in profile
		skillCaps, _ := s.scanSkillDir(filepath.Join(profileDir, "skills"), registry.ScopeProfile)
		caps = append(caps, skillCaps...)
	}

	return caps, nil
}

// ── Helper functions ──

// extractTSDescription attempts to extract a description string from TypeScript
// tool/plugin source code. Looks for patterns like:
//
//	tool({ description: "..." })
//	description: "..."
func extractTSDescription(content string) string {
	// Simple heuristic: find `description:` or `description =` followed by a quoted string
	for _, pattern := range []string{`description: "`, `description: '`, `description = "`, `description = '`} {
		idx := strings.Index(content, pattern)
		if idx < 0 {
			continue
		}
		start := idx + len(pattern)
		quote := content[start-1] // the quote char used
		end := strings.IndexByte(content[start:], byte(quote))
		if end > 0 {
			return content[start : start+end]
		}
	}
	return ""
}

// extractExposedTools attempts to extract tool names exposed by a plugin.
// Looks for patterns like tool("name", ...) or definePlugin({ tools: [...] }).
func extractExposedTools(content string) []string {
	var tools []string

	// Look for tool("name" patterns — common in OpenCode plugins
	search := content
	for {
		idx := strings.Index(search, `tool("`)
		if idx < 0 {
			break
		}
		start := idx + 6
		end := strings.IndexByte(search[start:], '"')
		if end > 0 {
			tools = append(tools, search[start:start+end])
		}
		search = search[start+end+1:]
	}

	return tools
}

// defaultConfigHome returns the default OpenCode config directory.
func defaultConfigHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	// Prefer XDG_CONFIG_HOME if set
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode")
	}

	return filepath.Join(home, ".config", "opencode")
}
