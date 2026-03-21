package skillresolver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	content := `---
name: replay-tester
description: Test evolutions by replaying past sessions
---

# Replay Tester

Some body content here.`

	name, desc, body, err := parseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "replay-tester" {
		t.Errorf("name = %q, want %q", name, "replay-tester")
	}
	if desc != "Test evolutions by replaying past sessions" {
		t.Errorf("description = %q", desc)
	}
	if !strings.Contains(body, "# Replay Tester") {
		t.Errorf("body should contain header, got: %q", body)
	}
	if !strings.Contains(body, "Some body content here.") {
		t.Errorf("body should contain content, got: %q", body)
	}
}

func TestParseFrontmatter_QuotedValues(t *testing.T) {
	content := `---
name: "my-skill"
description: 'A skill with quoted description'
---

Body.`

	name, desc, _, err := parseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "my-skill" {
		t.Errorf("name = %q, want %q", name, "my-skill")
	}
	if desc != "A skill with quoted description" {
		t.Errorf("description = %q", desc)
	}
}

func TestParseFrontmatter_NoFrontmatter(t *testing.T) {
	_, _, _, err := parseFrontmatter("# Just markdown")
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestParseFrontmatter_Unclosed(t *testing.T) {
	_, _, _, err := parseFrontmatter("---\nname: test\n# No closing")
	if err == nil {
		t.Fatal("expected error for unclosed frontmatter")
	}
}

func TestBuildSkillFileContent(t *testing.T) {
	content := buildSkillFileContent("my-skill", "A test skill", "# Header\n\nBody text.\n")

	if !strings.Contains(content, "---\n") {
		t.Error("missing frontmatter delimiters")
	}
	if !strings.Contains(content, "name: my-skill") {
		t.Error("missing name field")
	}
	if !strings.Contains(content, "description: A test skill") {
		t.Error("missing description field")
	}
	if !strings.Contains(content, "# Header") {
		t.Error("missing body header")
	}
}

func TestBuildSkillFileContent_EmptyBody(t *testing.T) {
	content := buildSkillFileContent("s", "d", "")
	if !strings.Contains(content, "---\nname: s\n") {
		t.Errorf("content = %q", content)
	}
}

func TestReadWriteSkillFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")

	original := `---
name: test-skill
description: A test skill for reading and writing
---

# Test Skill

This is a test skill with body content.
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	// Read.
	sf, err := ReadSkillFile(path)
	if err != nil {
		t.Fatalf("ReadSkillFile: %v", err)
	}
	if sf.Name != "test-skill" {
		t.Errorf("Name = %q", sf.Name)
	}
	if sf.Description != "A test skill for reading and writing" {
		t.Errorf("Description = %q", sf.Description)
	}
	if !strings.Contains(sf.Body, "# Test Skill") {
		t.Errorf("Body = %q", sf.Body)
	}
	if sf.Path != path {
		t.Errorf("Path = %q, want %q", sf.Path, path)
	}

	// Modify and write back.
	sf.Description = "Updated description"
	if err := WriteSkillFile(sf); err != nil {
		t.Fatalf("WriteSkillFile: %v", err)
	}

	// Re-read and verify.
	sf2, err := ReadSkillFile(path)
	if err != nil {
		t.Fatalf("ReadSkillFile after write: %v", err)
	}
	if sf2.Description != "Updated description" {
		t.Errorf("Description after write = %q", sf2.Description)
	}
	if sf2.Name != "test-skill" {
		t.Errorf("Name changed: %q", sf2.Name)
	}
	if !strings.Contains(sf2.Body, "# Test Skill") {
		t.Errorf("Body lost after write: %q", sf2.Body)
	}
}

func TestReadSkillFile_NotFound(t *testing.T) {
	_, err := ReadSkillFile("/nonexistent/SKILL.md")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestApplyImprovement_Description(t *testing.T) {
	sf := &SkillFile{
		Name:        "s",
		Description: "Old description",
		Body:        "# Body",
	}

	changed := ApplyImprovement(sf, SkillImprovement{
		Kind:                KindDescription,
		ProposedDescription: "New and improved description",
	})

	if !changed {
		t.Error("expected change")
	}
	if sf.Description != "New and improved description" {
		t.Errorf("Description = %q", sf.Description)
	}
}

func TestApplyImprovement_Description_NoChange(t *testing.T) {
	sf := &SkillFile{
		Name:        "s",
		Description: "Same description",
		Body:        "# Body",
	}

	changed := ApplyImprovement(sf, SkillImprovement{
		Kind:                KindDescription,
		ProposedDescription: "Same description",
	})

	if changed {
		t.Error("expected no change for same description")
	}
}

func TestApplyImprovement_Keywords(t *testing.T) {
	sf := &SkillFile{
		Name:        "s",
		Description: "Test evolutions by replaying sessions",
		Body:        "# Body",
	}

	changed := ApplyImprovement(sf, SkillImprovement{
		Kind:        KindKeywords,
		AddKeywords: []string{"compare", "diff", "validate"},
	})

	if !changed {
		t.Error("expected change")
	}
	if !strings.Contains(sf.Description, "keywords:") {
		t.Errorf("Description should contain keywords section: %q", sf.Description)
	}
	if !strings.Contains(sf.Description, "compare") {
		t.Errorf("Description should contain 'compare': %q", sf.Description)
	}
}

func TestApplyImprovement_Keywords_AlreadyPresent(t *testing.T) {
	sf := &SkillFile{
		Name:        "s",
		Description: "Test evolutions by replaying sessions",
		Body:        "Compare results easily",
	}

	changed := ApplyImprovement(sf, SkillImprovement{
		Kind:        KindKeywords,
		AddKeywords: []string{"replaying", "sessions", "compare"},
	})

	if changed {
		t.Error("expected no change — all keywords already present")
	}
}

func TestApplyImprovement_TriggerPattern(t *testing.T) {
	sf := &SkillFile{
		Name:        "s",
		Description: "d",
		Body:        "# Body\n\nExisting content.",
	}

	changed := ApplyImprovement(sf, SkillImprovement{
		Kind:               KindTriggerPattern,
		AddTriggerPatterns: []string{"replay the session", "compare results"},
	})

	if !changed {
		t.Error("expected change")
	}
	if !strings.Contains(sf.Body, "## Trigger Patterns") {
		t.Errorf("Body should contain trigger patterns section: %q", sf.Body)
	}
	if !strings.Contains(sf.Body, "- replay the session") {
		t.Errorf("Body should contain pattern: %q", sf.Body)
	}
}

func TestApplyImprovement_Content(t *testing.T) {
	sf := &SkillFile{
		Name:        "s",
		Description: "d",
		Body:        "Old body",
	}

	changed := ApplyImprovement(sf, SkillImprovement{
		Kind:            KindContent,
		ProposedContent: "New improved body",
	})

	if !changed {
		t.Error("expected change")
	}
	if sf.Body != "New improved body" {
		t.Errorf("Body = %q", sf.Body)
	}
}

func TestApplyImprovement_Content_NoChange(t *testing.T) {
	sf := &SkillFile{
		Name:        "s",
		Description: "d",
		Body:        "Same body",
	}

	changed := ApplyImprovement(sf, SkillImprovement{
		Kind:            KindContent,
		ProposedContent: "Same body",
	})

	if changed {
		t.Error("expected no change for same content")
	}
}

func TestFilterNewKeywords(t *testing.T) {
	desc := "Test evolutions by replaying sessions"
	body := "Compare results and validate behavior"

	got := filterNewKeywords(desc, body, []string{"replay", "diff", "sessions", "validate", "benchmark"})
	// "replay" is in "replaying", "sessions" in desc, "validate" in body → filtered out
	// "diff" and "benchmark" are new
	want := map[string]bool{"diff": true, "benchmark": true}

	if len(got) != len(want) {
		t.Fatalf("got %v, want keys %v", got, want)
	}
	for _, kw := range got {
		if !want[kw] {
			t.Errorf("unexpected keyword %q", kw)
		}
	}
}

func TestAppendKeywordsToDescription(t *testing.T) {
	tests := []struct {
		name        string
		desc        string
		keywords    []string
		wantContain string
	}{
		{
			name:        "new keywords section",
			desc:        "A skill description",
			keywords:    []string{"foo", "bar"},
			wantContain: "(keywords: foo, bar)",
		},
		{
			name:        "extend existing keywords",
			desc:        "A skill description (keywords: existing)",
			keywords:    []string{"new"},
			wantContain: "(keywords: existing, new)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendKeywordsToDescription(tt.desc, tt.keywords)
			if !strings.Contains(got, tt.wantContain) {
				t.Errorf("got %q, want to contain %q", got, tt.wantContain)
			}
		})
	}
}

func TestAppendTriggerPatterns(t *testing.T) {
	body := "# Existing\n\nSome content."
	got := appendTriggerPatterns(body, []string{"pattern one", "pattern two"})

	if !strings.Contains(got, "## Trigger Patterns") {
		t.Error("missing trigger patterns header")
	}
	if !strings.Contains(got, "- pattern one") {
		t.Error("missing pattern one")
	}
	if !strings.Contains(got, "- pattern two") {
		t.Error("missing pattern two")
	}
}

func TestRoundTrip_RealSkillFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")

	original := `---
name: replay-tester
description: Test evolutions (skills, agents, prompts) by replaying past sessions and comparing results
---

# Replay Tester

Test any OpenCode evolution by replaying a past session.

## Use Cases

- Changed a skill
- Updated an agent prompt
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("writing: %v", err)
	}

	sf, err := ReadSkillFile(path)
	if err != nil {
		t.Fatalf("ReadSkillFile: %v", err)
	}

	// Apply a keyword improvement.
	ApplyImprovement(sf, SkillImprovement{
		Kind:        KindKeywords,
		AddKeywords: []string{"benchmark", "validate", "diff"},
	})

	if err := WriteSkillFile(sf); err != nil {
		t.Fatalf("WriteSkillFile: %v", err)
	}

	// Re-read and verify round-trip.
	sf2, err := ReadSkillFile(path)
	if err != nil {
		t.Fatalf("ReadSkillFile after write: %v", err)
	}

	if sf2.Name != "replay-tester" {
		t.Errorf("Name = %q", sf2.Name)
	}
	if !strings.Contains(sf2.Description, "keywords:") {
		t.Errorf("Description should contain keywords: %q", sf2.Description)
	}
	if !strings.Contains(sf2.Body, "# Replay Tester") {
		t.Errorf("Body should preserve header: %q", sf2.Body)
	}
	if !strings.Contains(sf2.Body, "## Use Cases") {
		t.Errorf("Body should preserve sections: %q", sf2.Body)
	}
}
