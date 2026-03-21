package skillobs

import (
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/registry"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Detector Tests ──

func TestDetectLoadedSkills_ToolCall(t *testing.T) {
	messages := []session.Message{
		{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
			{Name: "load_skill", Input: `{"name":"django-migration-writer"}`},
		}},
	}
	loaded := DetectLoadedSkills(messages)
	if len(loaded) != 1 || loaded[0] != "django-migration-writer" {
		t.Errorf("loaded = %v, want [django-migration-writer]", loaded)
	}
}

func TestDetectLoadedSkills_MCPSkill(t *testing.T) {
	messages := []session.Message{
		{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
			{Name: "mcp_skill", Input: `{"name":"opencode-reference"}`},
		}},
	}
	loaded := DetectLoadedSkills(messages)
	if len(loaded) != 1 || loaded[0] != "opencode-reference" {
		t.Errorf("loaded = %v, want [opencode-reference]", loaded)
	}
}

func TestDetectLoadedSkills_PlainInput(t *testing.T) {
	messages := []session.Message{
		{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
			{Name: "skill", Input: "before-commit"},
		}},
	}
	loaded := DetectLoadedSkills(messages)
	if len(loaded) != 1 || loaded[0] != "before-commit" {
		t.Errorf("loaded = %v, want [before-commit]", loaded)
	}
}

func TestDetectLoadedSkills_SkillContentTag(t *testing.T) {
	messages := []session.Message{
		{Role: session.RoleAssistant, Content: `Here is the skill:
<skill_content name="testing-conventions">
Always write tests first...
</skill_content>`},
	}
	loaded := DetectLoadedSkills(messages)
	if len(loaded) != 1 || loaded[0] != "testing-conventions" {
		t.Errorf("loaded = %v, want [testing-conventions]", loaded)
	}
}

func TestDetectLoadedSkills_Deduplicate(t *testing.T) {
	messages := []session.Message{
		{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
			{Name: "load_skill", Input: `{"name":"my-skill"}`},
			{Name: "load_skill", Input: `{"name":"my-skill"}`}, // duplicate
		}},
	}
	loaded := DetectLoadedSkills(messages)
	if len(loaded) != 1 {
		t.Errorf("expected 1 unique skill, got %d: %v", len(loaded), loaded)
	}
}

func TestDetectLoadedSkills_NonSkillTool(t *testing.T) {
	messages := []session.Message{
		{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
			{Name: "Write", Input: `{"file_path":"main.go"}`},
			{Name: "bash", Input: `go test ./...`},
		}},
	}
	loaded := DetectLoadedSkills(messages)
	if len(loaded) != 0 {
		t.Errorf("expected 0 skills from non-skill tools, got %v", loaded)
	}
}

func TestDetectLoadedSkills_Empty(t *testing.T) {
	loaded := DetectLoadedSkills(nil)
	if len(loaded) != 0 {
		t.Errorf("expected 0, got %v", loaded)
	}
}

// ── Recommender Tests ──

var testSkills = []SkillEntry{
	{
		Name:        "django-migration-writer",
		Description: "Create DB migrations following blue-green safe patterns",
		Keywords:    extractKeywords("django-migration-writer", "Create DB migrations following blue-green safe patterns"),
	},
	{
		Name:        "testing-conventions",
		Description: "Write tests using project conventions and patterns",
		Keywords:    extractKeywords("testing-conventions", "Write tests using project conventions and patterns"),
	},
	{
		Name:        "before-commit",
		Description: "Run lint and format checks before committing",
		Keywords:    extractKeywords("before-commit", "Run lint and format checks before committing"),
	},
}

func TestRecommendSkills_Match(t *testing.T) {
	messages := []session.Message{
		{Role: session.RoleUser, Content: "add a new field to the User model and create a migration"},
	}
	recommended := RecommendSkills(messages, testSkills)
	if !contains(recommended, "django-migration-writer") {
		t.Errorf("expected django-migration-writer in %v", recommended)
	}
}

func TestRecommendSkills_MultipleMatches(t *testing.T) {
	messages := []session.Message{
		{Role: session.RoleUser, Content: "write tests for the migration code and run lint"},
	}
	recommended := RecommendSkills(messages, testSkills)
	if !contains(recommended, "testing-conventions") {
		t.Errorf("expected testing-conventions in %v", recommended)
	}
}

func TestRecommendSkills_NoMatch(t *testing.T) {
	messages := []session.Message{
		{Role: session.RoleUser, Content: "implement a REST API endpoint for user profiles"},
	}
	recommended := RecommendSkills(messages, testSkills)
	// None of our test skills should match this unrelated task.
	if contains(recommended, "django-migration-writer") {
		t.Errorf("did not expect django-migration-writer for unrelated task, got %v", recommended)
	}
}

func TestRecommendSkills_AssistantMessagesIgnored(t *testing.T) {
	messages := []session.Message{
		{Role: session.RoleAssistant, Content: "I'll create a migration for you"}, // should be ignored
	}
	recommended := RecommendSkills(messages, testSkills)
	if len(recommended) != 0 {
		t.Errorf("expected 0 recommendations from assistant messages, got %v", recommended)
	}
}

func TestRecommendSkills_Empty(t *testing.T) {
	recommended := RecommendSkills(nil, testSkills)
	if len(recommended) != 0 {
		t.Errorf("expected 0, got %v", recommended)
	}
}

func TestBuildSkillEntries_FiltersByKind(t *testing.T) {
	caps := []registry.Capability{
		{Name: "my-skill", Kind: registry.KindSkill, Description: "A skill"},
		{Name: "my-command", Kind: registry.KindCommand, Description: "A command"},
		{Name: "my-agent", Kind: registry.KindAgent, Description: "An agent"},
	}
	entries := BuildSkillEntries(caps)
	if len(entries) != 1 {
		t.Fatalf("expected 1 skill entry, got %d", len(entries))
	}
	if entries[0].Name != "my-skill" {
		t.Errorf("name = %q, want %q", entries[0].Name, "my-skill")
	}
}

func TestExtractKeywords(t *testing.T) {
	kws := extractKeywords("django-migration-writer", "Create DB migrations")
	if !contains(kws, "django-migration-writer") {
		t.Errorf("expected full name in keywords: %v", kws)
	}
	if !contains(kws, "django") {
		t.Errorf("expected 'django' in keywords: %v", kws)
	}
	if !contains(kws, "migration") {
		t.Errorf("expected 'migration' in keywords: %v", kws)
	}
	if !contains(kws, "migrations") {
		t.Errorf("expected 'migrations' (from description) in keywords: %v", kws)
	}
}

// ── Observer Tests ──

func TestObserve_FullScenario(t *testing.T) {
	caps := []registry.Capability{
		{Name: "django-migration-writer", Kind: registry.KindSkill, Description: "Create DB migrations"},
		{Name: "testing-conventions", Kind: registry.KindSkill, Description: "Write tests following conventions"},
		{Name: "before-commit", Kind: registry.KindSkill, Description: "Run lint and format before commit"},
	}

	messages := []session.Message{
		// User asks about migration — should recommend django-migration-writer.
		{Role: session.RoleUser, Content: "add a migration for the new field"},
		// Agent loads testing-conventions (not recommended — discovered).
		{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
			{Name: "mcp_skill", Input: `{"name":"testing-conventions"}`},
		}},
		// Agent does NOT load django-migration-writer (recommended — missed).
		{Role: session.RoleAssistant, Content: "I'll create the migration now."},
	}

	obs := Observe(messages, caps)
	if obs == nil {
		t.Fatal("expected non-nil observation")
	}

	if len(obs.Available) != 3 {
		t.Errorf("available = %d, want 3", len(obs.Available))
	}
	if !contains(obs.Recommended, "django-migration-writer") {
		t.Errorf("expected django-migration-writer in recommended: %v", obs.Recommended)
	}
	if !contains(obs.Loaded, "testing-conventions") {
		t.Errorf("expected testing-conventions in loaded: %v", obs.Loaded)
	}
	if !contains(obs.Missed, "django-migration-writer") {
		t.Errorf("expected django-migration-writer in missed: %v", obs.Missed)
	}
	if !contains(obs.Discovered, "testing-conventions") {
		t.Errorf("expected testing-conventions in discovered: %v", obs.Discovered)
	}
}

func TestObserve_NoSkills(t *testing.T) {
	obs := Observe([]session.Message{{Role: session.RoleUser, Content: "hello"}}, nil)
	if obs != nil {
		t.Errorf("expected nil when no skills, got %+v", obs)
	}
}

func TestObserve_AllLoaded(t *testing.T) {
	caps := []registry.Capability{
		{Name: "my-skill", Kind: registry.KindSkill, Description: "Does things"},
	}
	messages := []session.Message{
		{Role: session.RoleUser, Content: "use my-skill to do things"},
		{Role: session.RoleAssistant, ToolCalls: []session.ToolCall{
			{Name: "load_skill", Input: `{"name":"my-skill"}`},
		}},
	}

	obs := Observe(messages, caps)
	if obs == nil {
		t.Fatal("expected non-nil observation")
	}
	if len(obs.Missed) != 0 {
		t.Errorf("expected no missed skills, got %v", obs.Missed)
	}
	if len(obs.Discovered) != 0 {
		t.Errorf("expected no discovered skills, got %v", obs.Discovered)
	}
}

// ── Helpers ──

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
