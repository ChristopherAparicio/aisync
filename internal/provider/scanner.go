package provider

import (
	"github.com/ChristopherAparicio/aisync/internal/registry"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// RegistryScanner discovers agent capabilities from a provider's config files.
// This is the second facet of a provider — Provider reads sessions,
// RegistryScanner reads capability definitions (agents, commands, skills,
// tools, plugins, MCP servers).
//
// Implementations live alongside the existing Provider adapters:
// opencode/registry.go, claude/registry.go, cursor/registry.go.
type RegistryScanner interface {
	// ProviderName returns the provider this scanner belongs to.
	ProviderName() session.ProviderName

	// ScanProject discovers capabilities in a project directory.
	// Returns only project-scoped capabilities (not global).
	ScanProject(projectPath string) ([]registry.Capability, error)

	// ScanGlobal discovers capabilities from global/user-level config.
	// Returns global capabilities and configured MCP servers.
	ScanGlobal() ([]registry.Capability, []registry.MCPServer, error)
}

// ScannerRegistry manages available RegistryScanner adapters.
type ScannerRegistry struct {
	scanners map[session.ProviderName]RegistryScanner
}

// NewScannerRegistry creates a ScannerRegistry with the given scanners.
func NewScannerRegistry(scanners ...RegistryScanner) *ScannerRegistry {
	r := &ScannerRegistry{
		scanners: make(map[session.ProviderName]RegistryScanner, len(scanners)),
	}
	for _, s := range scanners {
		r.scanners[s.ProviderName()] = s
	}
	return r
}

// All returns all registered scanners.
func (r *ScannerRegistry) All() []RegistryScanner {
	scanners := make([]RegistryScanner, 0, len(r.scanners))
	for _, s := range r.scanners {
		scanners = append(scanners, s)
	}
	return scanners
}

// Get returns a specific scanner by provider name.
func (r *ScannerRegistry) Get(name session.ProviderName) (RegistryScanner, bool) {
	s, ok := r.scanners[name]
	return s, ok
}
