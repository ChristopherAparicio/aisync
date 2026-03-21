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
