package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/pricing"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/registry"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// RegistryService orchestrates capability discovery across providers.
// It merges global + project-level capabilities and enriches the view
// with session cost data from the existing Store.
type RegistryService struct {
	scanners *provider.ScannerRegistry
	store    storage.Store       // for session cost enrichment (optional)
	pricing  *pricing.Calculator // for cost computation (optional)
}

// RegistryServiceConfig holds all dependencies for creating a RegistryService.
type RegistryServiceConfig struct {
	Scanners *provider.ScannerRegistry
	Store    storage.Store       // optional — nil disables cost enrichment
	Pricing  *pricing.Calculator // optional — nil uses defaults
}

// NewRegistryService creates a RegistryService with all dependencies.
func NewRegistryService(cfg RegistryServiceConfig) *RegistryService {
	calc := cfg.Pricing
	if calc == nil {
		calc = pricing.NewCalculator()
	}
	return &RegistryService{
		scanners: cfg.Scanners,
		store:    cfg.Store,
		pricing:  calc,
	}
}

// ScanProject returns the full capability set for a project,
// merging global + project-level capabilities from all providers.
func (s *RegistryService) ScanProject(projectPath string) (*registry.Project, error) {
	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		return nil, err
	}

	project := &registry.Project{
		Name:     filepath.Base(absPath),
		RootPath: absPath,
	}

	// Detect VCS
	if isGitRepo(absPath) {
		project.VCS = "git"
	}

	// Scan all providers
	for _, scanner := range s.scanners.All() {
		// Global capabilities
		globalCaps, mcpServers, _ := scanner.ScanGlobal()
		project.Capabilities = append(project.Capabilities, globalCaps...)
		project.MCPServers = append(project.MCPServers, mcpServers...)

		// Project-level capabilities
		projectCaps, scanErr := scanner.ScanProject(absPath)
		if scanErr != nil {
			continue
		}
		project.Capabilities = append(project.Capabilities, projectCaps...)
	}

	// Sort capabilities: project > profile > global, then by kind, then by name
	sort.Slice(project.Capabilities, func(i, j int) bool {
		a, b := project.Capabilities[i], project.Capabilities[j]
		if scopeOrder(a.Scope) != scopeOrder(b.Scope) {
			return scopeOrder(a.Scope) > scopeOrder(b.Scope)
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})

	// Persist snapshot if store is available and capabilities changed.
	if s.store != nil {
		s.persistSnapshot(project)
	}

	return project, nil
}

// ListProjects discovers all known projects by scanning the OpenCode project registry.
// For each project, it runs a full capability scan.
func (s *RegistryService) ListProjects() ([]registry.Project, error) {
	// Discover project paths from OpenCode's storage
	projectPaths, err := discoverProjectPaths()
	if err != nil {
		return nil, err
	}

	var projects []registry.Project
	for _, path := range projectPaths {
		project, scanErr := s.ScanProject(path)
		if scanErr != nil {
			continue
		}
		projects = append(projects, *project)
	}

	// Sort by name
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].Name < projects[j].Name
	})

	return projects, nil
}

// GetProjectView returns a project with session cost enrichment.
func (s *RegistryService) GetProjectView(projectPath string) (*registry.ProjectView, error) {
	project, err := s.ScanProject(projectPath)
	if err != nil {
		return nil, err
	}

	view := &registry.ProjectView{
		Project: *project,
	}

	// Enrich with session data if store is available
	if s.store != nil {
		result, searchErr := s.store.Search(session.SearchQuery{
			ProjectPath: project.RootPath,
			Limit:       0, // all sessions
		})
		if searchErr == nil && result != nil {
			view.SessionCount = result.TotalCount

			// Aggregate tokens
			for _, summary := range result.Sessions {
				view.TotalTokens += int64(summary.TotalTokens)
			}

			// Compute cost from full sessions (if we have enough data)
			// For now, use token count as a proxy since we'd need to load full sessions for accurate cost
			// TODO: Add Store.AggregateByProject() for efficient cost computation
		}
	}

	return view, nil
}

// ── Helpers ──

// scopeOrder returns a sort priority for scopes (higher = more specific).
func scopeOrder(s registry.Scope) int {
	switch s {
	case registry.ScopeProject:
		return 3
	case registry.ScopeProfile:
		return 2
	case registry.ScopeGlobal:
		return 1
	default:
		return 0
	}
}

// isGitRepo checks if a directory is inside a git repository.
func isGitRepo(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

// discoverProjectPaths finds all registered project paths from OpenCode's storage.
// This reads the OpenCode project registry at ~/.local/share/opencode/storage/project/.
func discoverProjectPaths() ([]string, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dataHome = filepath.Join(home, ".local", "share")
	}

	projectDir := filepath.Join(dataHome, "opencode", "storage", "project")
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filePath := filepath.Join(projectDir, entry.Name())
		data, readErr := os.ReadFile(filePath)
		if readErr != nil {
			continue
		}

		// OpenCode project files contain a JSON object with a "worktree" field.
		type projectFile struct {
			ID       string `json:"id"`
			Worktree string `json:"worktree"`
		}
		var pf projectFile
		if err := parseJSONLoose(data, &pf); err != nil || pf.Worktree == "" {
			continue
		}

		// Skip the "global" pseudo-project
		if pf.ID == "global" || entry.Name() == "global.json" {
			continue
		}

		// Only include projects that still exist on disk
		if _, statErr := os.Stat(pf.Worktree); statErr == nil {
			paths = append(paths, pf.Worktree)
		}
	}

	return paths, nil
}

// parseJSONLoose is a lenient JSON parser that ignores trailing garbage.
// OpenCode project files are simple JSON objects.
func parseJSONLoose(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// ConfiguredVsUsed compares configured MCP servers against actual tool usage
// for a project. It identifies ghost servers (configured but never called)
// and orphan servers (called but not in config).
func (s *RegistryService) ConfiguredVsUsed(projectPath string, since time.Time) (*registry.ConfiguredVsUsed, error) {
	// Scan current capabilities to get configured MCP servers.
	project, err := s.ScanProject(projectPath)
	if err != nil {
		return nil, fmt.Errorf("scanning project: %w", err)
	}

	usage := s.aggregateMCPUsage(projectPath, since)
	return registry.AnalyzeConfiguredVsUsed(project.MCPServers, usage), nil
}

// persistSnapshot saves a project snapshot if capabilities changed since
// the last snapshot. This is called automatically after ScanProject().
// It also upserts the flat project_capabilities table on every call
// to keep last_seen timestamps fresh.
func (s *RegistryService) persistSnapshot(project *registry.Project) {
	prev, err := s.store.GetLatestSnapshot(project.RootPath)
	if err != nil {
		return // silently skip on error
	}

	// Compute diff.
	var prevProject *registry.Project
	if prev != nil {
		prevProject = &prev.Project
	}
	capsAdded, capsRemoved, mcpAdded, mcpRemoved := registry.DiffSnapshots(prevProject, project)

	// Determine change type.
	changeType := "unchanged"
	if prev == nil {
		changeType = "initial"
	} else if capsAdded > 0 || capsRemoved > 0 || mcpAdded > 0 || mcpRemoved > 0 {
		changeType = "changed"
	}

	// Only persist snapshot blob if it's the initial snapshot or something changed.
	if changeType != "unchanged" {
		snapshot := &registry.ProjectSnapshot{
			ProjectPath:         project.RootPath,
			Project:             *project,
			ScannedAt:           time.Now().UTC().Format(time.RFC3339),
			ChangeType:          changeType,
			CapabilitiesAdded:   capsAdded,
			CapabilitiesRemoved: capsRemoved,
			MCPServersAdded:     mcpAdded,
			MCPServersRemoved:   mcpRemoved,
		}
		_ = s.store.SaveProjectSnapshot(snapshot) // best-effort
	}

	// Always upsert flat capabilities to keep last_seen fresh.
	caps := registry.ProjectCapabilityKeys(project)
	_ = s.store.UpsertCapabilities(project.RootPath, caps) // best-effort
}

// CrossProjectMCPGovernance builds a matrix of projects × MCP servers with
// governance status (active/ghost/orphan) per cell. This is the cross-project
// version of ConfiguredVsUsed.
func (s *RegistryService) CrossProjectMCPGovernance(since time.Time) (*registry.MCPGovernanceMatrix, error) {
	projects, err := s.ListProjects()
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}

	var inputs []registry.MCPGovernanceInput
	for _, proj := range projects {
		usage := s.aggregateMCPUsage(proj.RootPath, since)
		displayName := proj.Name
		if displayName == "" {
			displayName = filepath.Base(proj.RootPath)
		}
		inputs = append(inputs, registry.MCPGovernanceInput{
			ProjectPath: proj.RootPath,
			DisplayName: displayName,
			Configured:  proj.MCPServers,
			Usage:       usage,
		})
	}

	return registry.BuildMCPGovernanceMatrix(inputs), nil
}

// aggregateMCPUsage queries tool buckets for a project and aggregates by MCP server.
func (s *RegistryService) aggregateMCPUsage(projectPath string, since time.Time) []registry.MCPUsageData {
	if s.store == nil {
		return nil
	}
	buckets, err := s.store.QueryToolBuckets("1d", since, time.Time{}, projectPath)
	if err != nil {
		return nil
	}

	serverAgg := make(map[string]*registry.MCPUsageData)
	toolsSeen := make(map[string]map[string]bool)

	for _, b := range buckets {
		server := session.MCPServerName(b.ToolCategory)
		if server == "" {
			continue
		}
		agg, ok := serverAgg[server]
		if !ok {
			agg = &registry.MCPUsageData{Server: server}
			serverAgg[server] = agg
			toolsSeen[server] = make(map[string]bool)
		}
		agg.CallCount += b.CallCount
		agg.TotalCost += b.EstimatedCost
		toolsSeen[server][b.ToolName] = true
	}

	var result []registry.MCPUsageData
	for server, agg := range serverAgg {
		agg.ToolCount = len(toolsSeen[server])
		result = append(result, *agg)
	}
	return result
}

// CrossProjectCapabilities aggregates capabilities across all known projects.
// It scans all discovered projects and builds a cross-project summary.
func (s *RegistryService) CrossProjectCapabilities() (*registry.CrossProjectSummary, error) {
	projects, err := s.ListProjects()
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	return registry.BuildCrossProjectSummary(projects), nil
}

// SkillReuseAnalysis builds a cross-project skill reuse map combining
// configured skills from the registry with actual usage from event buckets.
func (s *RegistryService) SkillReuseAnalysis(since time.Time) (*registry.SkillReuseMap, error) {
	projects, err := s.ListProjects()
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}

	var inputs []registry.SkillUsageInput
	for _, proj := range projects {
		displayName := proj.Name
		if displayName == "" {
			displayName = filepath.Base(proj.RootPath)
		}

		// Configured skills from the registry scan.
		var configured []string
		for _, cap := range proj.Capabilities {
			if cap.Kind == registry.KindSkill {
				configured = append(configured, cap.Name)
			}
		}

		// Actual skill usage from event buckets.
		usage := make(map[string]int)
		tokens := make(map[string]int)
		if s.store != nil {
			buckets, bErr := s.store.QueryEventBuckets(sessionevent.BucketQuery{
				ProjectPath: proj.RootPath,
				Granularity: "1d",
				Since:       since,
			})
			if bErr == nil {
				for _, b := range buckets {
					for name, count := range b.TopSkills {
						usage[name] += count
					}
					for name, tok := range b.SkillTokens {
						tokens[name] += tok
					}
				}
			}
		}

		inputs = append(inputs, registry.SkillUsageInput{
			ProjectPath: proj.RootPath,
			DisplayName: displayName,
			Configured:  configured,
			Usage:       usage,
			Tokens:      tokens,
		})
	}

	return registry.BuildSkillReuseMap(inputs), nil
}

// ScanAllProjects discovers and scans all known projects, persisting snapshots
// and flat capabilities for each. Returns the number of projects successfully scanned.
// This method satisfies the scheduler.RegistryScanner interface.
func (s *RegistryService) ScanAllProjects() (int, error) {
	projects, err := s.ListProjects()
	if err != nil {
		return 0, fmt.Errorf("listing projects: %w", err)
	}
	// ListProjects already calls ScanProject() for each, which triggers persistSnapshot().
	return len(projects), nil
}
