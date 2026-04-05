package registry

import (
	"testing"
)

func TestCapabilityKind_Valid(t *testing.T) {
	tests := []struct {
		kind CapabilityKind
		want bool
	}{
		{KindAgent, true},
		{KindCommand, true},
		{KindSkill, true},
		{KindTool, true},
		{KindPlugin, true},
		{CapabilityKind("unknown"), false},
		{CapabilityKind(""), false},
	}
	for _, tt := range tests {
		if got := tt.kind.Valid(); got != tt.want {
			t.Errorf("CapabilityKind(%q).Valid() = %v, want %v", tt.kind, got, tt.want)
		}
	}
}

func TestProject_CapabilityStats(t *testing.T) {
	p := &Project{
		Capabilities: []Capability{
			{Name: "a1", Kind: KindAgent},
			{Name: "a2", Kind: KindAgent},
			{Name: "c1", Kind: KindCommand},
			{Name: "c2", Kind: KindCommand},
			{Name: "c3", Kind: KindCommand},
			{Name: "t1", Kind: KindTool},
		},
	}

	stats := p.CapabilityStats()

	// Should be sorted by kind order: agent, command, skill, tool, plugin
	expected := map[CapabilityKind]int{
		KindAgent:   2,
		KindCommand: 3,
		KindTool:    1,
	}

	if len(stats) != len(expected) {
		t.Fatalf("got %d stat entries, want %d", len(stats), len(expected))
	}

	for _, s := range stats {
		want, ok := expected[s.Kind]
		if !ok {
			t.Errorf("unexpected kind %q in stats", s.Kind)
			continue
		}
		if s.Count != want {
			t.Errorf("count for %q = %d, want %d", s.Kind, s.Count, want)
		}
	}
}

func TestProject_FindCapability(t *testing.T) {
	p := &Project{
		Capabilities: []Capability{
			{Name: "speckit.specify", Kind: KindCommand},
			{Name: "worktree", Kind: KindPlugin},
		},
	}

	// Found
	cap := p.FindCapability("speckit.specify")
	if cap == nil {
		t.Fatal("expected to find speckit.specify")
	}
	if cap.Kind != KindCommand {
		t.Errorf("kind = %q, want command", cap.Kind)
	}

	// Not found
	cap = p.FindCapability("nonexistent")
	if cap != nil {
		t.Errorf("expected nil for nonexistent capability, got %v", cap)
	}
}

func TestCapability_RelPath(t *testing.T) {
	cap := Capability{
		FilePath: "/home/user/project/.opencode/command/foo.md",
	}

	got := cap.RelPath("/home/user/project")
	want := ".opencode/command/foo.md"
	if got != want {
		t.Errorf("RelPath = %q, want %q", got, want)
	}
}

func TestProject_CapabilityStats_Empty(t *testing.T) {
	p := &Project{}
	stats := p.CapabilityStats()
	if len(stats) != 0 {
		t.Errorf("expected 0 stats for empty project, got %d", len(stats))
	}
}

// ── DiffSnapshots tests ──

func TestDiffSnapshots_Initial(t *testing.T) {
	curr := &Project{
		Capabilities: []Capability{
			{Name: "cmd-a", Kind: KindCommand},
			{Name: "tool-b", Kind: KindTool},
		},
		MCPServers: []MCPServer{
			{Name: "notion"},
			{Name: "sentry"},
		},
	}

	capsAdded, capsRemoved, mcpAdded, mcpRemoved := DiffSnapshots(nil, curr)

	if capsAdded != 2 {
		t.Errorf("capsAdded = %d, want 2", capsAdded)
	}
	if capsRemoved != 0 {
		t.Errorf("capsRemoved = %d, want 0", capsRemoved)
	}
	if mcpAdded != 2 {
		t.Errorf("mcpAdded = %d, want 2", mcpAdded)
	}
	if mcpRemoved != 0 {
		t.Errorf("mcpRemoved = %d, want 0", mcpRemoved)
	}
}

func TestDiffSnapshots_Unchanged(t *testing.T) {
	prev := &Project{
		Capabilities: []Capability{{Name: "a"}, {Name: "b"}},
		MCPServers:   []MCPServer{{Name: "notion"}},
	}
	curr := &Project{
		Capabilities: []Capability{{Name: "a"}, {Name: "b"}},
		MCPServers:   []MCPServer{{Name: "notion"}},
	}

	capsAdded, capsRemoved, mcpAdded, mcpRemoved := DiffSnapshots(prev, curr)

	if capsAdded != 0 || capsRemoved != 0 || mcpAdded != 0 || mcpRemoved != 0 {
		t.Errorf("expected all zeros for unchanged, got +%d/-%d caps, +%d/-%d mcp",
			capsAdded, capsRemoved, mcpAdded, mcpRemoved)
	}
}

func TestDiffSnapshots_AddedAndRemoved(t *testing.T) {
	prev := &Project{
		Capabilities: []Capability{{Name: "a"}, {Name: "b"}, {Name: "c"}},
		MCPServers:   []MCPServer{{Name: "notion"}, {Name: "old-server"}},
	}
	curr := &Project{
		Capabilities: []Capability{{Name: "b"}, {Name: "d"}},              // a,c removed; d added
		MCPServers:   []MCPServer{{Name: "notion"}, {Name: "new-server"}}, // old removed, new added
	}

	capsAdded, capsRemoved, mcpAdded, mcpRemoved := DiffSnapshots(prev, curr)

	if capsAdded != 1 {
		t.Errorf("capsAdded = %d, want 1 (d)", capsAdded)
	}
	if capsRemoved != 2 {
		t.Errorf("capsRemoved = %d, want 2 (a,c)", capsRemoved)
	}
	if mcpAdded != 1 {
		t.Errorf("mcpAdded = %d, want 1 (new-server)", mcpAdded)
	}
	if mcpRemoved != 1 {
		t.Errorf("mcpRemoved = %d, want 1 (old-server)", mcpRemoved)
	}
}

// ── AnalyzeConfiguredVsUsed tests ──

func TestAnalyzeConfiguredVsUsed_AllActive(t *testing.T) {
	configured := []MCPServer{
		{Name: "notion", Scope: ScopeGlobal, Enabled: true},
		{Name: "sentry", Scope: ScopeProject, Enabled: true},
	}
	usage := []MCPUsageData{
		{Server: "notion", CallCount: 50, ToolCount: 3, TotalCost: 1.25},
		{Server: "sentry", CallCount: 30, ToolCount: 2, TotalCost: 0.80},
	}

	result := AnalyzeConfiguredVsUsed(configured, usage)

	if result.ActiveCount != 2 {
		t.Errorf("ActiveCount = %d, want 2", result.ActiveCount)
	}
	if result.GhostCount != 0 {
		t.Errorf("GhostCount = %d, want 0", result.GhostCount)
	}
	if result.OrphanCount != 0 {
		t.Errorf("OrphanCount = %d, want 0", result.OrphanCount)
	}
	if result.TotalCalls != 80 {
		t.Errorf("TotalCalls = %d, want 80", result.TotalCalls)
	}
	if len(result.Servers) != 2 {
		t.Errorf("Servers count = %d, want 2", len(result.Servers))
	}
}

func TestAnalyzeConfiguredVsUsed_GhostServers(t *testing.T) {
	configured := []MCPServer{
		{Name: "notion", Scope: ScopeGlobal, Enabled: true},
		{Name: "unused-server", Scope: ScopeProject, Enabled: true},
		{Name: "disabled-server", Scope: ScopeGlobal, Enabled: false},
	}
	usage := []MCPUsageData{
		{Server: "notion", CallCount: 50, ToolCount: 3, TotalCost: 1.25},
	}

	result := AnalyzeConfiguredVsUsed(configured, usage)

	if result.ActiveCount != 1 {
		t.Errorf("ActiveCount = %d, want 1", result.ActiveCount)
	}
	if result.GhostCount != 2 {
		t.Errorf("GhostCount = %d, want 2 (unused-server + disabled-server)", result.GhostCount)
	}

	// Check individual statuses.
	for _, s := range result.Servers {
		switch s.Name {
		case "notion":
			if s.Status != MCPStatusActive {
				t.Errorf("notion: status = %q, want active", s.Status)
			}
			if s.CallCount != 50 {
				t.Errorf("notion: CallCount = %d, want 50", s.CallCount)
			}
		case "unused-server":
			if s.Status != MCPStatusGhost {
				t.Errorf("unused-server: status = %q, want ghost", s.Status)
			}
			if s.CallCount != 0 {
				t.Errorf("unused-server: CallCount = %d, want 0", s.CallCount)
			}
		case "disabled-server":
			if s.Status != MCPStatusGhost {
				t.Errorf("disabled-server: status = %q, want ghost", s.Status)
			}
		}
	}
}

func TestAnalyzeConfiguredVsUsed_OrphanServers(t *testing.T) {
	configured := []MCPServer{
		{Name: "notion", Scope: ScopeGlobal, Enabled: true},
	}
	usage := []MCPUsageData{
		{Server: "notion", CallCount: 50, ToolCount: 3, TotalCost: 1.25},
		{Server: "langfuse", CallCount: 20, ToolCount: 5, TotalCost: 0.50},
		{Server: "context7", CallCount: 10, ToolCount: 1, TotalCost: 0.10},
	}

	result := AnalyzeConfiguredVsUsed(configured, usage)

	if result.ActiveCount != 1 {
		t.Errorf("ActiveCount = %d, want 1", result.ActiveCount)
	}
	if result.OrphanCount != 2 {
		t.Errorf("OrphanCount = %d, want 2 (langfuse + context7)", result.OrphanCount)
	}
	if result.TotalCalls != 80 {
		t.Errorf("TotalCalls = %d, want 80", result.TotalCalls)
	}

	// Check orphans have no scope.
	for _, s := range result.Servers {
		if s.Status == MCPStatusOrphan && s.Scope != "" {
			t.Errorf("orphan %s should have empty scope, got %q", s.Name, s.Scope)
		}
	}
}

func TestAnalyzeConfiguredVsUsed_Mixed(t *testing.T) {
	configured := []MCPServer{
		{Name: "notion", Scope: ScopeGlobal, Enabled: true},
		{Name: "ghost-api", Scope: ScopeProject, Enabled: true},
	}
	usage := []MCPUsageData{
		{Server: "notion", CallCount: 100, ToolCount: 5, TotalCost: 2.50},
		{Server: "orphan-tool", CallCount: 15, ToolCount: 2, TotalCost: 0.30},
	}

	result := AnalyzeConfiguredVsUsed(configured, usage)

	if result.ActiveCount != 1 {
		t.Errorf("ActiveCount = %d, want 1", result.ActiveCount)
	}
	if result.GhostCount != 1 {
		t.Errorf("GhostCount = %d, want 1", result.GhostCount)
	}
	if result.OrphanCount != 1 {
		t.Errorf("OrphanCount = %d, want 1", result.OrphanCount)
	}
}

func TestAnalyzeConfiguredVsUsed_Empty(t *testing.T) {
	result := AnalyzeConfiguredVsUsed(nil, nil)

	if result.ActiveCount != 0 || result.GhostCount != 0 || result.OrphanCount != 0 {
		t.Errorf("expected all zeros for empty input")
	}
	if len(result.Servers) != 0 {
		t.Errorf("expected no servers, got %d", len(result.Servers))
	}
}

// ── BuildCrossProjectSummary tests ──

func TestBuildCrossProjectSummary_Basic(t *testing.T) {
	projects := []Project{
		{
			Name:     "project-a",
			RootPath: "/home/user/project-a",
			Capabilities: []Capability{
				{Name: "cmd-1", Kind: KindCommand},
				{Name: "skill-1", Kind: KindSkill},
				{Name: "agent-1", Kind: KindAgent},
			},
			MCPServers: []MCPServer{
				{Name: "notion", Scope: ScopeGlobal},
				{Name: "sentry", Scope: ScopeProject},
			},
		},
		{
			Name:     "project-b",
			RootPath: "/home/user/project-b",
			Capabilities: []Capability{
				{Name: "cmd-2", Kind: KindCommand},
				{Name: "skill-2", Kind: KindSkill},
			},
			MCPServers: []MCPServer{
				{Name: "notion", Scope: ScopeGlobal}, // shared with project-a
				{Name: "langfuse", Scope: ScopeProject},
			},
		},
	}

	result := BuildCrossProjectSummary(projects)

	if result.ProjectCount != 2 {
		t.Errorf("ProjectCount = %d, want 2", result.ProjectCount)
	}
	if result.TotalCapabilities != 5 {
		t.Errorf("TotalCapabilities = %d, want 5", result.TotalCapabilities)
	}
	if result.TotalMCPServers != 3 {
		t.Errorf("TotalMCPServers = %d, want 3 (notion, sentry, langfuse)", result.TotalMCPServers)
	}

	// Check shared MCP servers.
	var notionShared *SharedMCPServer
	for i, s := range result.SharedMCPServers {
		if s.Name == "notion" {
			notionShared = &result.SharedMCPServers[i]
			break
		}
	}
	if notionShared == nil {
		t.Fatal("notion not found in SharedMCPServers")
	}
	if notionShared.ProjectCount != 2 {
		t.Errorf("notion.ProjectCount = %d, want 2", notionShared.ProjectCount)
	}

	// Check capability matrix.
	for _, row := range result.CapabilityMatrix {
		switch row.Kind {
		case KindCommand:
			if row.TotalCount != 2 || row.ProjectCount != 2 {
				t.Errorf("command: total=%d projects=%d, want 2/2", row.TotalCount, row.ProjectCount)
			}
		case KindSkill:
			if row.TotalCount != 2 || row.ProjectCount != 2 {
				t.Errorf("skill: total=%d projects=%d, want 2/2", row.TotalCount, row.ProjectCount)
			}
		case KindAgent:
			if row.TotalCount != 1 || row.ProjectCount != 1 {
				t.Errorf("agent: total=%d projects=%d, want 1/1", row.TotalCount, row.ProjectCount)
			}
		}
	}

	// Check project overviews.
	if len(result.ProjectOverviews) != 2 {
		t.Fatalf("ProjectOverviews = %d, want 2", len(result.ProjectOverviews))
	}
	for _, po := range result.ProjectOverviews {
		if po.Name == "project-a" {
			if po.CapabilityCount != 3 || po.MCPServerCount != 2 {
				t.Errorf("project-a: caps=%d mcp=%d, want 3/2", po.CapabilityCount, po.MCPServerCount)
			}
		}
	}
}

func TestBuildCrossProjectSummary_Empty(t *testing.T) {
	result := BuildCrossProjectSummary(nil)
	if result.ProjectCount != 0 || result.TotalCapabilities != 0 {
		t.Errorf("expected zeros for empty input")
	}
}

// ── ProjectCapabilityKeys tests ──

func TestProjectCapabilityKeys_Basic(t *testing.T) {
	p := &Project{
		Capabilities: []Capability{
			{Name: "cmd-1", Kind: KindCommand, Scope: ScopeProject},
			{Name: "skill-1", Kind: KindSkill, Scope: ScopeGlobal},
		},
		MCPServers: []MCPServer{
			{Name: "notion", Scope: ScopeGlobal},
			{Name: "sentry", Scope: ScopeProject},
		},
	}

	keys := ProjectCapabilityKeys(p)

	if len(keys) != 4 {
		t.Fatalf("got %d keys, want 4", len(keys))
	}

	// Verify that capabilities come first, then MCP servers.
	if keys[0].Name != "cmd-1" || keys[0].Kind != KindCommand {
		t.Errorf("keys[0] = %s/%s, want cmd-1/command", keys[0].Name, keys[0].Kind)
	}
	if keys[1].Name != "skill-1" || keys[1].Kind != KindSkill {
		t.Errorf("keys[1] = %s/%s, want skill-1/skill", keys[1].Name, keys[1].Kind)
	}
	if keys[2].Name != "notion" || keys[2].Kind != KindMCPServer {
		t.Errorf("keys[2] = %s/%s, want notion/mcp_server", keys[2].Name, keys[2].Kind)
	}
	if keys[3].Name != "sentry" || keys[3].Kind != KindMCPServer {
		t.Errorf("keys[3] = %s/%s, want sentry/mcp_server", keys[3].Name, keys[3].Kind)
	}
}

func TestProjectCapabilityKeys_Dedup(t *testing.T) {
	p := &Project{
		Capabilities: []Capability{
			{Name: "skill-a", Kind: KindSkill},
			{Name: "skill-a", Kind: KindSkill}, // duplicate
		},
		MCPServers: []MCPServer{
			{Name: "notion"},
			{Name: "notion"}, // duplicate
		},
	}

	keys := ProjectCapabilityKeys(p)

	if len(keys) != 2 {
		t.Fatalf("got %d keys, want 2 (deduped)", len(keys))
	}
}

func TestProjectCapabilityKeys_Empty(t *testing.T) {
	p := &Project{}
	keys := ProjectCapabilityKeys(p)
	if len(keys) != 0 {
		t.Errorf("got %d keys for empty project, want 0", len(keys))
	}
}

func TestBuildCrossProjectSummary_SingleProject(t *testing.T) {
	projects := []Project{
		{
			Name:     "solo",
			RootPath: "/solo",
			Capabilities: []Capability{
				{Name: "tool-1", Kind: KindTool},
			},
			MCPServers: []MCPServer{
				{Name: "sentry"},
			},
		},
	}

	result := BuildCrossProjectSummary(projects)

	if result.TotalMCPServers != 1 {
		t.Errorf("TotalMCPServers = %d, want 1", result.TotalMCPServers)
	}
	if result.SharedMCPServers[0].ProjectCount != 1 {
		t.Errorf("sentry.ProjectCount = %d, want 1", result.SharedMCPServers[0].ProjectCount)
	}
}

// ── BuildSkillReuseMap tests ──

func TestBuildSkillReuseMap_Empty(t *testing.T) {
	result := BuildSkillReuseMap(nil)
	if result.TotalSkills != 0 {
		t.Errorf("TotalSkills = %d, want 0", result.TotalSkills)
	}
}

func TestBuildSkillReuseMap_SharedSkills(t *testing.T) {
	inputs := []SkillUsageInput{
		{
			ProjectPath: "/proj-a",
			DisplayName: "proj-a",
			Configured:  []string{"replay-tester", "opencode-sessions"},
			Usage:       map[string]int{"replay-tester": 10, "opencode-sessions": 5},
			Tokens:      map[string]int{"replay-tester": 20000, "opencode-sessions": 10000},
		},
		{
			ProjectPath: "/proj-b",
			DisplayName: "proj-b",
			Configured:  []string{"replay-tester"},
			Usage:       map[string]int{"replay-tester": 8},
			Tokens:      map[string]int{"replay-tester": 16000},
		},
	}

	result := BuildSkillReuseMap(inputs)

	if result.SharedCount != 1 {
		t.Errorf("SharedCount = %d, want 1", result.SharedCount)
	}
	if result.MonoCount != 1 {
		t.Errorf("MonoCount = %d, want 1", result.MonoCount)
	}
	if result.TotalLoads != 23 {
		t.Errorf("TotalLoads = %d, want 23", result.TotalLoads)
	}
	if len(result.SharedSkills) != 1 || result.SharedSkills[0].Name != "replay-tester" {
		t.Errorf("SharedSkills = %v, want [replay-tester]", result.SharedSkills)
	}
	if result.SharedSkills[0].ProjectCount != 2 {
		t.Errorf("replay-tester ProjectCount = %d, want 2", result.SharedSkills[0].ProjectCount)
	}
	if result.SharedSkills[0].TotalLoads != 18 {
		t.Errorf("replay-tester TotalLoads = %d, want 18", result.SharedSkills[0].TotalLoads)
	}
	if result.SharedSkills[0].TotalTokens != 36000 {
		t.Errorf("replay-tester TotalTokens = %d, want 36000", result.SharedSkills[0].TotalTokens)
	}
}

func TestBuildSkillReuseMap_IdleSkills(t *testing.T) {
	inputs := []SkillUsageInput{
		{
			ProjectPath: "/proj-a",
			DisplayName: "proj-a",
			Configured:  []string{"replay-tester", "never-used"},
			Usage:       map[string]int{"replay-tester": 5},
		},
	}

	result := BuildSkillReuseMap(inputs)

	if result.IdleCount != 1 {
		t.Errorf("IdleCount = %d, want 1", result.IdleCount)
	}
	if len(result.IdleSkills) != 1 || result.IdleSkills[0].Name != "never-used" {
		t.Errorf("IdleSkills = %v, want [never-used]", result.IdleSkills)
	}
}

func TestBuildSkillReuseMap_SortByLoads(t *testing.T) {
	inputs := []SkillUsageInput{
		{
			ProjectPath: "/proj",
			DisplayName: "proj",
			Usage:       map[string]int{"low": 1, "high": 100, "mid": 50},
		},
	}

	result := BuildSkillReuseMap(inputs)

	if len(result.MonoSkills) != 3 {
		t.Fatalf("MonoSkills count = %d, want 3", len(result.MonoSkills))
	}
	if result.MonoSkills[0].Name != "high" || result.MonoSkills[1].Name != "mid" || result.MonoSkills[2].Name != "low" {
		t.Errorf("MonoSkills order = [%s, %s, %s], want [high, mid, low]",
			result.MonoSkills[0].Name, result.MonoSkills[1].Name, result.MonoSkills[2].Name)
	}
}

// ── BuildMCPGovernanceMatrix tests ──

func TestBuildMCPGovernanceMatrix_Empty(t *testing.T) {
	result := BuildMCPGovernanceMatrix(nil)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Projects) != 0 {
		t.Errorf("expected 0 projects, got %d", len(result.Projects))
	}
	if result.TotalServers != 0 {
		t.Errorf("expected 0 servers, got %d", result.TotalServers)
	}
}

func TestBuildMCPGovernanceMatrix_SingleProject(t *testing.T) {
	inputs := []MCPGovernanceInput{
		{
			ProjectPath: "/proj-a",
			DisplayName: "proj-a",
			Configured: []MCPServer{
				{Name: "sentry", Scope: ScopeGlobal, Enabled: true},
				{Name: "notion", Scope: ScopeProject, Enabled: true},
			},
			Usage: []MCPUsageData{
				{Server: "sentry", CallCount: 100, ToolCount: 5, TotalCost: 12.50},
				// notion is configured but has no usage → ghost
			},
		},
	}

	result := BuildMCPGovernanceMatrix(inputs)

	if len(result.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(result.Projects))
	}
	if result.TotalActive != 1 {
		t.Errorf("TotalActive = %d, want 1", result.TotalActive)
	}
	if result.TotalGhost != 1 {
		t.Errorf("TotalGhost = %d, want 1", result.TotalGhost)
	}
	if result.TotalServers != 2 {
		t.Errorf("TotalServers = %d, want 2", result.TotalServers)
	}

	row := result.Projects[0]
	sentryCell := row.Cells["sentry"]
	if sentryCell.Status != MCPStatusActive {
		t.Errorf("sentry status = %q, want %q", sentryCell.Status, MCPStatusActive)
	}
	if sentryCell.CallCount != 100 {
		t.Errorf("sentry calls = %d, want 100", sentryCell.CallCount)
	}

	notionCell := row.Cells["notion"]
	if notionCell.Status != MCPStatusGhost {
		t.Errorf("notion status = %q, want %q", notionCell.Status, MCPStatusGhost)
	}
}

func TestBuildMCPGovernanceMatrix_MultiProject(t *testing.T) {
	inputs := []MCPGovernanceInput{
		{
			ProjectPath: "/proj-a",
			DisplayName: "proj-a",
			Configured:  []MCPServer{{Name: "sentry", Enabled: true}},
			Usage:       []MCPUsageData{{Server: "sentry", CallCount: 50, TotalCost: 5.0}},
		},
		{
			ProjectPath: "/proj-b",
			DisplayName: "proj-b",
			Configured:  []MCPServer{{Name: "notion", Enabled: true}},
			Usage: []MCPUsageData{
				{Server: "notion", CallCount: 30, TotalCost: 3.0},
				{Server: "langfuse", CallCount: 10, TotalCost: 1.0}, // orphan — not configured
			},
		},
	}

	result := BuildMCPGovernanceMatrix(inputs)

	if len(result.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(result.Projects))
	}
	if result.TotalActive != 2 {
		t.Errorf("TotalActive = %d, want 2", result.TotalActive)
	}
	if result.TotalOrphan != 1 {
		t.Errorf("TotalOrphan = %d, want 1", result.TotalOrphan)
	}
	if result.TotalServers != 3 {
		t.Errorf("TotalServers = %d, want 3", result.TotalServers)
	}

	// Servers should be sorted alphabetically.
	if result.Servers[0] != "langfuse" || result.Servers[1] != "notion" || result.Servers[2] != "sentry" {
		t.Errorf("Servers = %v, want [langfuse notion sentry]", result.Servers)
	}

	// proj-b should have langfuse as orphan.
	projB := result.Projects[1]
	langfuseCell := projB.Cells["langfuse"]
	if langfuseCell.Status != MCPStatusOrphan {
		t.Errorf("langfuse status = %q, want %q", langfuseCell.Status, MCPStatusOrphan)
	}
}

func TestBuildMCPGovernanceMatrix_Costs(t *testing.T) {
	inputs := []MCPGovernanceInput{
		{
			ProjectPath: "/proj",
			DisplayName: "proj",
			Configured:  []MCPServer{{Name: "sentry", Enabled: true}, {Name: "notion", Enabled: true}},
			Usage: []MCPUsageData{
				{Server: "sentry", CallCount: 100, TotalCost: 10.0},
				{Server: "notion", CallCount: 50, TotalCost: 5.0},
			},
		},
	}

	result := BuildMCPGovernanceMatrix(inputs)

	row := result.Projects[0]
	if row.TotalCost != 15.0 {
		t.Errorf("TotalCost = %f, want 15.0", row.TotalCost)
	}
}
