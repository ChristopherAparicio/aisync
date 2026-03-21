package cursor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/registry"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// Scanner implements provider.RegistryScanner for Cursor.
// It discovers capabilities from Cursor's config:
//   - Project:  .cursor/rules/*.md, .cursorrules
//   - MCP:      .cursor/mcp.json
type Scanner struct{}

// NewScanner creates a Cursor registry scanner.
func NewScanner() *Scanner {
	return &Scanner{}
}

// ProviderName returns the provider this scanner belongs to.
func (s *Scanner) ProviderName() session.ProviderName {
	return session.ProviderCursor
}

// ScanProject discovers capabilities in a project directory for Cursor.
func (s *Scanner) ScanProject(projectPath string) ([]registry.Capability, error) {
	var caps []registry.Capability

	// .cursorrules — legacy project rules
	cursorrules := filepath.Join(projectPath, ".cursorrules")
	if info, err := os.Stat(cursorrules); err == nil && !info.IsDir() {
		caps = append(caps, registry.Capability{
			Name:        ".cursorrules",
			Kind:        registry.KindAgent,
			Scope:       registry.ScopeProject,
			Description: "Legacy Cursor project rules",
			FilePath:    cursorrules,
		})
	}

	// .cursor/rules/*.md — modern Cursor rules
	rulesDir := filepath.Join(projectPath, ".cursor", "rules")
	ruleCaps, _ := scanCursorRules(rulesDir)
	caps = append(caps, ruleCaps...)

	// .cursor/mcp.json — project-level MCP servers (treated as tool capabilities)
	mcpFile := filepath.Join(projectPath, ".cursor", "mcp.json")
	mcpCaps, _ := scanCursorMCP(mcpFile, registry.ScopeProject)
	caps = append(caps, mcpCaps...)

	return caps, nil
}

// ScanGlobal discovers capabilities from the global Cursor config.
// Cursor stores most config in VS Code-style settings, not easily scannable.
// Returns empty results — Cursor's global config is minimal compared to OpenCode.
func (s *Scanner) ScanGlobal() ([]registry.Capability, []registry.MCPServer, error) {
	return nil, nil, nil
}

// ── Internal scanning ──

// scanCursorRules scans .cursor/rules/ for rule files.
func scanCursorRules(dir string) ([]registry.Capability, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var caps []registry.Capability
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		// Cursor rules can be .md or .mdc (MDX cursor format)
		name := entry.Name()
		if !strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".mdc") {
			continue
		}

		filePath := filepath.Join(dir, name)
		ext := filepath.Ext(name)
		baseName := strings.TrimSuffix(name, ext)

		caps = append(caps, registry.Capability{
			Name:        baseName,
			Kind:        registry.KindAgent,
			Scope:       registry.ScopeProject,
			Description: "Cursor rule",
			FilePath:    filePath,
		})
	}

	return caps, nil
}

// scanCursorMCP reads MCP server configuration from .cursor/mcp.json.
// Returns capabilities for each server (since Cursor MCP is project-scoped).
func scanCursorMCP(filePath string, scope registry.Scope) ([]registry.Capability, error) {
	data, err := os.ReadFile(filePath)
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

	var caps []registry.Capability
	for name := range config.MCPServers {
		caps = append(caps, registry.Capability{
			Name:        "mcp:" + name,
			Kind:        registry.KindTool,
			Scope:       scope,
			Description: "MCP server: " + name,
			FilePath:    filePath,
		})
	}

	return caps, nil
}
