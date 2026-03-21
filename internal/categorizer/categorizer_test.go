package categorizer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func touch(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestDetect_Backend_GoMod(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod")
	touch(t, dir, "main.go")
	if got := DetectProjectCategory(dir); got != "backend" {
		t.Errorf("got %q, want backend", got)
	}
}

func TestDetect_Backend_CargoToml(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Cargo.toml")
	if got := DetectProjectCategory(dir); got != "backend" {
		t.Errorf("got %q, want backend", got)
	}
}

func TestDetect_Frontend_PackageJson_Vite(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "package.json")
	touch(t, dir, "vite.config.ts")
	if got := DetectProjectCategory(dir); got != "frontend" {
		t.Errorf("got %q, want frontend", got)
	}
}

func TestDetect_Frontend_PackageJson_Next(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "package.json")
	touch(t, dir, "next.config.js")
	if got := DetectProjectCategory(dir); got != "frontend" {
		t.Errorf("got %q, want frontend", got)
	}
}

func TestDetect_Fullstack(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod")
	touch(t, dir, "package.json")
	touch(t, dir, "tsconfig.json")
	if got := DetectProjectCategory(dir); got != "fullstack" {
		t.Errorf("got %q, want fullstack", got)
	}
}

func TestDetect_Ops_Dockerfile(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Dockerfile")
	touch(t, dir, "docker-compose.yml")
	if got := DetectProjectCategory(dir); got != "ops" {
		t.Errorf("got %q, want ops", got)
	}
}

func TestDetect_Ops_Terraform(t *testing.T) {
	dir := t.TempDir()
	mkdir(t, dir, "terraform")
	if got := DetectProjectCategory(dir); got != "ops" {
		t.Errorf("got %q, want ops", got)
	}
}

func TestDetect_Library(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "go.mod")
	mkdir(t, dir, "pkg")
	// No cmd/ or main.go → library
	if got := DetectProjectCategory(dir); got != "library" {
		t.Errorf("got %q, want library", got)
	}
}

func TestDetect_Data(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "pyproject.toml")
	mkdir(t, dir, "notebooks")
	if got := DetectProjectCategory(dir); got != "data" {
		t.Errorf("got %q, want data", got)
	}
}

func TestDetect_Mobile(t *testing.T) {
	dir := t.TempDir()
	mkdir(t, dir, "ios")
	if got := DetectProjectCategory(dir); got != "mobile" {
		t.Errorf("got %q, want mobile", got)
	}
}

func TestDetect_Unknown(t *testing.T) {
	dir := t.TempDir()
	// Empty directory → unknown
	if got := DetectProjectCategory(dir); got != "" {
		t.Errorf("got %q, want empty (unknown)", got)
	}
}

func TestDetect_Docs(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "mkdocs.yml")
	if got := DetectProjectCategory(dir); got != "docs" {
		t.Errorf("got %q, want docs", got)
	}
}

// ── LLM Classifier Tests ──

func TestParseProjectResult_ValidJSON(t *testing.T) {
	raw := `{"category": "backend", "confidence": 0.9, "reasoning": "Go project with API endpoints"}`
	result := parseProjectResult(raw, session.DefaultProjectCategories)
	if result.Category != "backend" {
		t.Errorf("Category = %q, want backend", result.Category)
	}
	if result.Confidence != 0.9 {
		t.Errorf("Confidence = %f, want 0.9", result.Confidence)
	}
}

func TestParseProjectResult_JSONInText(t *testing.T) {
	raw := `Based on the files, I classify this as: {"category": "frontend", "confidence": 0.85, "reasoning": "React project"}`
	result := parseProjectResult(raw, session.DefaultProjectCategories)
	if result.Category != "frontend" {
		t.Errorf("Category = %q, want frontend", result.Category)
	}
}

func TestParseProjectResult_FallbackWordMatch(t *testing.T) {
	raw := "This is clearly an ops/infrastructure project."
	result := parseProjectResult(raw, session.DefaultProjectCategories)
	if result.Category != "ops" {
		t.Errorf("Category = %q, want ops", result.Category)
	}
}

func TestParseProjectResult_InvalidJSON(t *testing.T) {
	raw := "I have no idea what this project is about."
	result := parseProjectResult(raw, session.DefaultProjectCategories)
	if result.Category != "" {
		t.Errorf("Category = %q, want empty", result.Category)
	}
}

func TestParseProjectResult_InvalidCategory(t *testing.T) {
	raw := `{"category": "blockchain", "confidence": 0.9}`
	result := parseProjectResult(raw, session.DefaultProjectCategories)
	// "blockchain" is not in session.DefaultProjectCategories → should fallback
	if result.Category == "blockchain" {
		t.Error("should not accept invalid category")
	}
}

func TestBuildProjectPrompt_ContainsCategories(t *testing.T) {
	sess := &session.Session{
		ID:       "test-1",
		Summary:  "Add OAuth2 authentication",
		Branch:   "feature/auth",
		Provider: session.ProviderClaudeCode,
		FileChanges: []session.FileChange{
			{FilePath: "auth.go", ChangeType: session.ChangeCreated},
			{FilePath: "main.go", ChangeType: session.ChangeModified},
		},
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "Add OAuth2 support to the API"},
		},
	}

	prompt := buildProjectPrompt(sess, session.DefaultProjectCategories)

	// Check prompt contains all default categories.
	for _, cat := range session.DefaultProjectCategories {
		if !strings.Contains(prompt, cat) {
			t.Errorf("prompt missing category %q", cat)
		}
	}
	// Check prompt contains session context.
	if !strings.Contains(prompt, "OAuth2") {
		t.Error("prompt should contain session summary")
	}
	if !strings.Contains(prompt, "auth.go") {
		t.Error("prompt should contain file changes")
	}
}
