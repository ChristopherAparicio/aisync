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
