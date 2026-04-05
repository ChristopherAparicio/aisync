package sqlite

import (
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/registry"
)

func TestUpsertCapabilities_Basic(t *testing.T) {
	store := mustOpenStore(t)

	caps := []registry.PersistedCapability{
		{Name: "skill-a", Kind: registry.KindSkill, Scope: registry.ScopeGlobal},
		{Name: "cmd-b", Kind: registry.KindCommand, Scope: registry.ScopeProject},
		{Name: "notion", Kind: registry.KindMCPServer, Scope: registry.ScopeGlobal},
	}

	if err := store.UpsertCapabilities("/project/x", caps); err != nil {
		t.Fatalf("UpsertCapabilities() error = %v", err)
	}

	// Read them back.
	got, err := store.ListCapabilities(registry.CapabilityFilter{ProjectPath: "/project/x"})
	if err != nil {
		t.Fatalf("ListCapabilities() error = %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("got %d capabilities, want 3", len(got))
	}

	// All should be active.
	for _, c := range got {
		if !c.IsActive {
			t.Errorf("capability %s is inactive, want active", c.Name)
		}
		if c.FirstSeen.IsZero() {
			t.Errorf("capability %s has zero FirstSeen", c.Name)
		}
		if c.LastSeen.IsZero() {
			t.Errorf("capability %s has zero LastSeen", c.Name)
		}
	}
}

func TestUpsertCapabilities_DeactivatesRemoved(t *testing.T) {
	store := mustOpenStore(t)

	// Initial upsert: 3 capabilities.
	caps1 := []registry.PersistedCapability{
		{Name: "skill-a", Kind: registry.KindSkill, Scope: registry.ScopeGlobal},
		{Name: "skill-b", Kind: registry.KindSkill, Scope: registry.ScopeGlobal},
		{Name: "notion", Kind: registry.KindMCPServer, Scope: registry.ScopeGlobal},
	}
	if err := store.UpsertCapabilities("/project/x", caps1); err != nil {
		t.Fatalf("first UpsertCapabilities() error = %v", err)
	}

	// Second upsert: only 2 capabilities (skill-b removed).
	caps2 := []registry.PersistedCapability{
		{Name: "skill-a", Kind: registry.KindSkill, Scope: registry.ScopeGlobal},
		{Name: "notion", Kind: registry.KindMCPServer, Scope: registry.ScopeGlobal},
	}
	if err := store.UpsertCapabilities("/project/x", caps2); err != nil {
		t.Fatalf("second UpsertCapabilities() error = %v", err)
	}

	// List all (including inactive).
	all, err := store.ListCapabilities(registry.CapabilityFilter{ProjectPath: "/project/x"})
	if err != nil {
		t.Fatalf("ListCapabilities() error = %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d capabilities, want 3 (2 active + 1 inactive)", len(all))
	}

	// List active only.
	active, err := store.ListCapabilities(registry.CapabilityFilter{
		ProjectPath: "/project/x",
		ActiveOnly:  true,
	})
	if err != nil {
		t.Fatalf("ListCapabilities(active) error = %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("got %d active capabilities, want 2", len(active))
	}

	// Check that skill-b is inactive.
	for _, c := range all {
		if c.Name == "skill-b" && c.IsActive {
			t.Error("skill-b should be inactive after removal")
		}
	}
}

func TestUpsertCapabilities_PreservesFirstSeen(t *testing.T) {
	store := mustOpenStore(t)

	caps := []registry.PersistedCapability{
		{Name: "skill-a", Kind: registry.KindSkill, Scope: registry.ScopeGlobal},
	}

	// First upsert.
	if err := store.UpsertCapabilities("/project/x", caps); err != nil {
		t.Fatalf("first UpsertCapabilities() error = %v", err)
	}

	// Read first_seen.
	got1, err := store.ListCapabilities(registry.CapabilityFilter{ProjectPath: "/project/x"})
	if err != nil || len(got1) != 1 {
		t.Fatalf("ListCapabilities() error = %v, len = %d", err, len(got1))
	}
	firstSeen1 := got1[0].FirstSeen

	// Second upsert (same capability).
	if err := store.UpsertCapabilities("/project/x", caps); err != nil {
		t.Fatalf("second UpsertCapabilities() error = %v", err)
	}

	// Read again — first_seen should be unchanged.
	got2, _ := store.ListCapabilities(registry.CapabilityFilter{ProjectPath: "/project/x"})
	if len(got2) != 1 {
		t.Fatalf("expected 1 capability, got %d", len(got2))
	}
	if !got2[0].FirstSeen.Equal(firstSeen1) {
		t.Errorf("first_seen changed: %v → %v", firstSeen1, got2[0].FirstSeen)
	}
}

func TestListCapabilities_FilterByKind(t *testing.T) {
	store := mustOpenStore(t)

	caps := []registry.PersistedCapability{
		{Name: "skill-a", Kind: registry.KindSkill, Scope: registry.ScopeGlobal},
		{Name: "cmd-b", Kind: registry.KindCommand, Scope: registry.ScopeProject},
		{Name: "notion", Kind: registry.KindMCPServer, Scope: registry.ScopeGlobal},
	}
	if err := store.UpsertCapabilities("/project/x", caps); err != nil {
		t.Fatalf("UpsertCapabilities() error = %v", err)
	}

	// Filter by mcp_server.
	mcps, err := store.ListCapabilities(registry.CapabilityFilter{Kind: registry.KindMCPServer})
	if err != nil {
		t.Fatalf("ListCapabilities(mcp_server) error = %v", err)
	}
	if len(mcps) != 1 || mcps[0].Name != "notion" {
		t.Errorf("expected 1 mcp_server 'notion', got %d: %v", len(mcps), mcps)
	}
}

func TestListCapabilities_CrossProject(t *testing.T) {
	store := mustOpenStore(t)

	capsA := []registry.PersistedCapability{
		{Name: "notion", Kind: registry.KindMCPServer, Scope: registry.ScopeGlobal},
		{Name: "sentry", Kind: registry.KindMCPServer, Scope: registry.ScopeProject},
	}
	capsB := []registry.PersistedCapability{
		{Name: "notion", Kind: registry.KindMCPServer, Scope: registry.ScopeGlobal},
		{Name: "langfuse", Kind: registry.KindMCPServer, Scope: registry.ScopeProject},
	}
	if err := store.UpsertCapabilities("/project/a", capsA); err != nil {
		t.Fatalf("UpsertCapabilities(a) error = %v", err)
	}
	if err := store.UpsertCapabilities("/project/b", capsB); err != nil {
		t.Fatalf("UpsertCapabilities(b) error = %v", err)
	}

	// Query all MCP servers across projects.
	all, err := store.ListCapabilities(registry.CapabilityFilter{Kind: registry.KindMCPServer})
	if err != nil {
		t.Fatalf("ListCapabilities() error = %v", err)
	}
	// project/a: notion, sentry; project/b: notion, langfuse → 4 rows
	if len(all) != 4 {
		t.Errorf("got %d capabilities, want 4", len(all))
	}
}

func TestListCapabilityProjects(t *testing.T) {
	store := mustOpenStore(t)

	capsA := []registry.PersistedCapability{
		{Name: "skill-a", Kind: registry.KindSkill, Scope: registry.ScopeGlobal},
	}
	capsB := []registry.PersistedCapability{
		{Name: "skill-b", Kind: registry.KindSkill, Scope: registry.ScopeGlobal},
	}
	if err := store.UpsertCapabilities("/project/a", capsA); err != nil {
		t.Fatalf("UpsertCapabilities(a) error = %v", err)
	}
	if err := store.UpsertCapabilities("/project/b", capsB); err != nil {
		t.Fatalf("UpsertCapabilities(b) error = %v", err)
	}

	paths, err := store.ListCapabilityProjects()
	if err != nil {
		t.Fatalf("ListCapabilityProjects() error = %v", err)
	}
	if len(paths) != 2 {
		t.Errorf("got %d projects, want 2", len(paths))
	}
}

func TestListCapabilityProjects_Empty(t *testing.T) {
	store := mustOpenStore(t)

	paths, err := store.ListCapabilityProjects()
	if err != nil {
		t.Fatalf("ListCapabilityProjects() error = %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("got %d projects, want 0", len(paths))
	}
}

func TestUpsertCapabilities_MultipleProjectsIndependent(t *testing.T) {
	store := mustOpenStore(t)

	// Project A has 3 capabilities.
	capsA := []registry.PersistedCapability{
		{Name: "skill-a", Kind: registry.KindSkill, Scope: registry.ScopeGlobal},
		{Name: "notion", Kind: registry.KindMCPServer, Scope: registry.ScopeGlobal},
		{Name: "sentry", Kind: registry.KindMCPServer, Scope: registry.ScopeProject},
	}
	if err := store.UpsertCapabilities("/project/a", capsA); err != nil {
		t.Fatal(err)
	}

	// Project B has 1 capability.
	capsB := []registry.PersistedCapability{
		{Name: "notion", Kind: registry.KindMCPServer, Scope: registry.ScopeGlobal},
	}
	if err := store.UpsertCapabilities("/project/b", capsB); err != nil {
		t.Fatal(err)
	}

	// Updating project B should NOT affect project A.
	capsB2 := []registry.PersistedCapability{} // empty — removes all
	if err := store.UpsertCapabilities("/project/b", capsB2); err != nil {
		t.Fatal(err)
	}

	activeA, err := store.ListCapabilities(registry.CapabilityFilter{
		ProjectPath: "/project/a",
		ActiveOnly:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(activeA) != 3 {
		t.Errorf("project A should still have 3 active capabilities, got %d", len(activeA))
	}

	activeB, err := store.ListCapabilities(registry.CapabilityFilter{
		ProjectPath: "/project/b",
		ActiveOnly:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(activeB) != 0 {
		t.Errorf("project B should have 0 active capabilities, got %d", len(activeB))
	}
}
