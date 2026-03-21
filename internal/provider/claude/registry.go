package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/registry"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// Scanner implements provider.RegistryScanner for Claude Code.
// It discovers capabilities from Claude's config:
//   - Project:  .claude/commands/*.md, CLAUDE.md
//   - Global:   ~/.claude/claude_desktop_config.json (MCP servers)
type Scanner struct {
	// claudeHome overrides ~/.claude for testing.
	claudeHome string
}

// NewScanner creates a Claude Code registry scanner.
// If claudeHome is empty, it defaults to ~/.claude.
func NewScanner(claudeHome string) *Scanner {
	if claudeHome == "" {
		claudeHome = defaultClaudeHome()
	}
	return &Scanner{claudeHome: claudeHome}
}

// ProviderName returns the provider this scanner belongs to.
func (s *Scanner) ProviderName() session.ProviderName {
	return session.ProviderClaudeCode
}

// ScanProject discovers capabilities in a project directory for Claude Code.
func (s *Scanner) ScanProject(projectPath string) ([]registry.Capability, error) {
	var caps []registry.Capability

	// CLAUDE.md — project instructions (treated as an agent capability)
	claudeMD := filepath.Join(projectPath, "CLAUDE.md")
	if info, err := os.Stat(claudeMD); err == nil && !info.IsDir() {
		caps = append(caps, registry.Capability{
			Name:        "CLAUDE.md",
			Kind:        registry.KindAgent,
			Scope:       registry.ScopeProject,
			Description: "Project-level instructions for Claude Code",
			FilePath:    claudeMD,
		})
	}

	// .claude/commands/*.md
	cmdDir := filepath.Join(projectPath, ".claude", "commands")
	cmdCaps, _ := scanClaudeCommands(cmdDir, registry.ScopeProject)
	caps = append(caps, cmdCaps...)

	// .claude/settings.json — may contain MCP or custom instructions
	settingsFile := filepath.Join(projectPath, ".claude", "settings.json")
	if _, err := os.Stat(settingsFile); err == nil {
		caps = append(caps, registry.Capability{
			Name:        "settings",
			Kind:        registry.KindAgent,
			Scope:       registry.ScopeProject,
			Description: "Claude Code project settings",
			FilePath:    settingsFile,
		})
	}

	return caps, nil
}

// ScanGlobal discovers capabilities from the global Claude config.
func (s *Scanner) ScanGlobal() ([]registry.Capability, []registry.MCPServer, error) {
	var caps []registry.Capability

	// Global CLAUDE.md
	globalClaudeMD := filepath.Join(s.claudeHome, "CLAUDE.md")
	if info, err := os.Stat(globalClaudeMD); err == nil && !info.IsDir() {
		caps = append(caps, registry.Capability{
			Name:        "CLAUDE.md",
			Kind:        registry.KindAgent,
			Scope:       registry.ScopeGlobal,
			Description: "Global instructions for Claude Code",
			FilePath:    globalClaudeMD,
		})
	}

	// Global commands: ~/.claude/commands/*.md
	cmdDir := filepath.Join(s.claudeHome, "commands")
	cmdCaps, _ := scanClaudeCommands(cmdDir, registry.ScopeGlobal)
	caps = append(caps, cmdCaps...)

	// MCP servers from claude_desktop_config.json
	servers, _ := scanClaudeMCPServers(s.claudeHome)

	return caps, servers, nil
}

// ── Internal scanning ──

// scanClaudeCommands scans a directory of Claude command files.
// Claude commands are plain Markdown without YAML frontmatter.
func scanClaudeCommands(dir string, scope registry.Scope) ([]registry.Capability, error) {
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
		name := strings.TrimSuffix(entry.Name(), ".md")

		// Claude commands don't use YAML frontmatter — extract first line as description
		desc := extractFirstLine(filePath)

		caps = append(caps, registry.Capability{
			Name:        name,
			Kind:        registry.KindCommand,
			Scope:       scope,
			Description: desc,
			FilePath:    filePath,
		})
	}

	return caps, nil
}

// scanClaudeMCPServers reads MCP server config from claude_desktop_config.json.
func scanClaudeMCPServers(claudeHome string) ([]registry.MCPServer, error) {
	configFile := filepath.Join(claudeHome, "claude_desktop_config.json")
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}

	var config struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	var servers []registry.MCPServer
	for name := range config.MCPServers {
		servers = append(servers, registry.MCPServer{
			Name:    name,
			Type:    "local",
			Scope:   registry.ScopeGlobal,
			Enabled: true,
		})
	}

	return servers, nil
}

// extractFirstLine reads the first non-empty line of a file as a description.
func extractFirstLine(filePath string) string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return line
		}
	}
	return ""
}

// defaultClaudeHome returns the default Claude config directory.
func defaultClaudeHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}
