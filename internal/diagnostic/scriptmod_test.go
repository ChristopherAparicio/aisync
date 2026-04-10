package diagnostic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── ScriptModule unit tests ─────────────────────────────────────────────────

func TestNewScriptModule_Name(t *testing.T) {
	tests := []struct {
		path     string
		wantName string
	}{
		{"/path/to/detect-retry.py", "script:detect-retry"},
		{"/path/to/detect-retry.sh", "script:detect-retry"},
		{"/path/to/my-module", "script:my-module"},
		{"/path/to/module.rb", "script:module"},
	}

	for _, tt := range tests {
		m := NewScriptModule(tt.path, "project")
		if m.Name() != tt.wantName {
			t.Errorf("NewScriptModule(%q).Name() = %q, want %q", tt.path, m.Name(), tt.wantName)
		}
	}
}

func TestScriptModule_ShouldActivate_AlwaysTrue(t *testing.T) {
	m := NewScriptModule("/fake/script.py", "global")
	sess := &session.Session{}
	if !m.ShouldActivate(sess) {
		t.Error("ScriptModule.ShouldActivate should always return true")
	}
}

func TestScriptModule_Source(t *testing.T) {
	m := NewScriptModule("/fake/script.py", "project")
	if m.Source() != "project" {
		t.Errorf("Source() = %q, want %q", m.Source(), "project")
	}
}

func TestScriptModule_Detect_HappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	// Create a temp script that outputs valid JSON problems
	dir := t.TempDir()
	script := filepath.Join(dir, "detect-test.sh")
	content := `#!/bin/sh
cat <<'EOF'
[
  {
    "id": "test-problem-1",
    "severity": "high",
    "category": "commands",
    "title": "Test problem detected",
    "observation": "Observed something interesting",
    "impact": "Wastes 1000 tokens",
    "metric": 1000,
    "metric_unit": "tokens"
  }
]
EOF
`
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewScriptModule(script, "project")
	sess := &session.Session{ID: "ses_test123"}
	r := &InspectReport{}

	problems := m.Detect(r, sess)
	if len(problems) != 1 {
		t.Fatalf("got %d problems, want 1", len(problems))
	}

	p := problems[0]
	if p.ID != "test-problem-1" {
		t.Errorf("ID = %q, want %q", p.ID, "test-problem-1")
	}
	if p.Severity != SeverityHigh {
		t.Errorf("Severity = %q, want %q", p.Severity, SeverityHigh)
	}
	if p.Category != CategoryCommands {
		t.Errorf("Category = %q, want %q", p.Category, CategoryCommands)
	}
	if p.Title != "Test problem detected" {
		t.Errorf("Title = %q, want %q", p.Title, "Test problem detected")
	}
	if p.Metric != 1000 {
		t.Errorf("Metric = %f, want %f", p.Metric, 1000.0)
	}
}

func TestScriptModule_Detect_EmptyOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "empty.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho '[]'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewScriptModule(script, "project")
	problems := m.Detect(&InspectReport{}, &session.Session{})
	if len(problems) != 0 {
		t.Errorf("empty JSON array should produce 0 problems, got %d", len(problems))
	}
}

func TestScriptModule_Detect_NoOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "silent.sh")
	// Script outputs nothing at all
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewScriptModule(script, "project")
	problems := m.Detect(&InspectReport{}, &session.Session{})
	if len(problems) != 0 {
		t.Errorf("no output should produce 0 problems, got %d", len(problems))
	}
}

func TestScriptModule_Detect_BadJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "badjson.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'not json'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewScriptModule(script, "project")
	problems := m.Detect(&InspectReport{}, &session.Session{})
	if len(problems) != 0 {
		t.Errorf("bad JSON should produce 0 problems, got %d", len(problems))
	}
}

func TestScriptModule_Detect_NonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "fail.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewScriptModule(script, "project")
	problems := m.Detect(&InspectReport{}, &session.Session{})
	if len(problems) != 0 {
		t.Errorf("non-zero exit should produce 0 problems, got %d", len(problems))
	}
}

func TestScriptModule_Detect_MalformedEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	// Script returns a problem with missing required fields — should be skipped
	dir := t.TempDir()
	script := filepath.Join(dir, "malformed.sh")
	content := `#!/bin/sh
echo '[{"id": "", "title": ""}, {"id": "good", "severity": "low", "title": "Good problem", "observation": "ok", "impact": "none"}]'
`
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewScriptModule(script, "project")
	problems := m.Detect(&InspectReport{}, &session.Session{})
	if len(problems) != 1 {
		t.Fatalf("got %d problems, want 1 (malformed entry should be skipped)", len(problems))
	}
	if problems[0].ID != "good" {
		t.Errorf("ID = %q, want %q", problems[0].ID, "good")
	}
}

func TestScriptModule_Detect_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "slow.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 10\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewScriptModule(script, "project")
	m.timeout = 100 * time.Millisecond // very short timeout

	problems := m.Detect(&InspectReport{}, &session.Session{})
	if len(problems) != 0 {
		t.Errorf("timeout should produce 0 problems, got %d", len(problems))
	}
}

func TestScriptModule_Detect_ReceivesSessionJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	// This script reads the session JSON from stdin and checks for the session ID
	dir := t.TempDir()
	outFile := filepath.Join(dir, "received.json")
	script := filepath.Join(dir, "reader.sh")
	content := `#!/bin/sh
# Save stdin to a file so the test can verify what was received
cat > ` + outFile + `
echo '[]'
`
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewScriptModule(script, "project")
	sess := &session.Session{
		ID:       "ses_verify123",
		Provider: "claude",
		Agent:    "test-agent",
	}
	m.Detect(&InspectReport{}, sess)

	// Read back what the script received
	received, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("script did not write received JSON: %v", err)
	}

	var parsed session.Session
	if err := json.Unmarshal(received, &parsed); err != nil {
		t.Fatalf("script received invalid JSON: %v", err)
	}
	if parsed.ID != "ses_verify123" {
		t.Errorf("script received session ID = %q, want %q", parsed.ID, "ses_verify123")
	}
}

func TestScriptModule_Detect_DefaultCategory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	// Script returns a problem without category — should default to "patterns"
	dir := t.TempDir()
	script := filepath.Join(dir, "nocat.sh")
	content := `#!/bin/sh
echo '[{"id": "test", "severity": "low", "title": "Test", "observation": "obs", "impact": "none"}]'
`
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewScriptModule(script, "project")
	problems := m.Detect(&InspectReport{}, &session.Session{})
	if len(problems) != 1 {
		t.Fatalf("got %d problems, want 1", len(problems))
	}
	if problems[0].Category != CategoryPatterns {
		t.Errorf("Category = %q, want %q (default)", problems[0].Category, CategoryPatterns)
	}
}

// ── Discovery tests ─────────────────────────────────────────────────────────

func TestDiscoverScriptModules_EmptyDirs(t *testing.T) {
	mods := DiscoverScriptModules(ScriptDirs{
		ProjectDir: "/nonexistent/path",
		GlobalDir:  "/also/nonexistent",
	})
	if len(mods) != 0 {
		t.Errorf("nonexistent dirs should return 0 modules, got %d", len(mods))
	}
}

func TestDiscoverScriptModules_FindsExecutables(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable permissions not applicable on Windows")
	}

	dir := t.TempDir()

	// Create an executable script
	script := filepath.Join(dir, "detect-test.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho '[]'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	mods := DiscoverScriptModules(ScriptDirs{ProjectDir: dir})
	if len(mods) != 1 {
		t.Fatalf("got %d modules, want 1", len(mods))
	}
	if mods[0].Name() != "script:detect-test" {
		t.Errorf("Name = %q, want %q", mods[0].Name(), "script:detect-test")
	}
	if mods[0].Source() != "project" {
		t.Errorf("Source = %q, want %q", mods[0].Source(), "project")
	}
}

func TestDiscoverScriptModules_SkipsDisabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable permissions not applicable on Windows")
	}

	dir := t.TempDir()

	// Executable but disabled
	if err := os.WriteFile(filepath.Join(dir, "detect-test.sh.disabled"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	mods := DiscoverScriptModules(ScriptDirs{ProjectDir: dir})
	if len(mods) != 0 {
		t.Errorf("disabled scripts should be skipped, got %d", len(mods))
	}
}

func TestDiscoverScriptModules_SkipsNonExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable permissions not applicable on Windows")
	}

	dir := t.TempDir()

	// File without execute permission
	if err := os.WriteFile(filepath.Join(dir, "detect-test.py"), []byte("#!/usr/bin/env python3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mods := DiscoverScriptModules(ScriptDirs{ProjectDir: dir})
	if len(mods) != 0 {
		t.Errorf("non-executable files should be skipped, got %d", len(mods))
	}
}

func TestDiscoverScriptModules_SkipsHiddenFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable permissions not applicable on Windows")
	}

	dir := t.TempDir()

	// Hidden file (starts with .)
	if err := os.WriteFile(filepath.Join(dir, ".hidden-module.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	mods := DiscoverScriptModules(ScriptDirs{ProjectDir: dir})
	if len(mods) != 0 {
		t.Errorf("hidden files should be skipped, got %d", len(mods))
	}
}

func TestDiscoverScriptModules_SkipsREADME(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable permissions not applicable on Windows")
	}

	dir := t.TempDir()

	// README files
	for _, name := range []string{"README", "README.md", "readme.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("docs"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	mods := DiscoverScriptModules(ScriptDirs{ProjectDir: dir})
	if len(mods) != 0 {
		t.Errorf("README files should be skipped, got %d", len(mods))
	}
}

func TestDiscoverScriptModules_SkipsDirectories(t *testing.T) {
	dir := t.TempDir()

	// Subdirectory
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	mods := DiscoverScriptModules(ScriptDirs{ProjectDir: dir})
	if len(mods) != 0 {
		t.Errorf("directories should be skipped, got %d", len(mods))
	}
}

func TestDiscoverScriptModules_ProjectOverridesGlobal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable permissions not applicable on Windows")
	}

	projectDir := t.TempDir()
	globalDir := t.TempDir()

	// Same script name in both dirs
	content := "#!/bin/sh\necho '[]'\n"
	if err := os.WriteFile(filepath.Join(projectDir, "detect-test.sh"), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "detect-test.sh"), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	mods := DiscoverScriptModules(ScriptDirs{ProjectDir: projectDir, GlobalDir: globalDir})
	if len(mods) != 1 {
		t.Fatalf("duplicate names should be deduplicated, got %d", len(mods))
	}
	if mods[0].Source() != "project" {
		t.Errorf("project should take precedence, got source=%q", mods[0].Source())
	}
}

func TestDiscoverScriptModules_MixedSources(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable permissions not applicable on Windows")
	}

	projectDir := t.TempDir()
	globalDir := t.TempDir()

	content := "#!/bin/sh\necho '[]'\n"
	// Different scripts in each
	if err := os.WriteFile(filepath.Join(projectDir, "detect-project.sh"), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "detect-global.sh"), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	mods := DiscoverScriptModules(ScriptDirs{ProjectDir: projectDir, GlobalDir: globalDir})
	if len(mods) != 2 {
		t.Fatalf("got %d modules, want 2", len(mods))
	}

	sources := map[string]string{}
	for _, m := range mods {
		sources[m.Name()] = m.Source()
	}
	if sources["script:detect-project"] != "project" {
		t.Errorf("detect-project source = %q, want %q", sources["script:detect-project"], "project")
	}
	if sources["script:detect-global"] != "global" {
		t.Errorf("detect-global source = %q, want %q", sources["script:detect-global"], "global")
	}
}

// ── Helper tests ────────────────────────────────────────────────────────────

func TestParseSeverity(t *testing.T) {
	tests := []struct {
		input string
		want  Severity
	}{
		{"high", SeverityHigh},
		{"HIGH", SeverityHigh},
		{"High", SeverityHigh},
		{"medium", SeverityMedium},
		{"low", SeverityLow},
		{"unknown", SeverityLow},
		{"", SeverityLow},
	}
	for _, tt := range tests {
		got := parseSeverity(tt.input)
		if got != tt.want {
			t.Errorf("parseSeverity(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseCategory(t *testing.T) {
	tests := []struct {
		input string
		want  Category
	}{
		{"images", CategoryImages},
		{"commands", CategoryCommands},
		{"tokens", CategoryTokens},
		{"tool_errors", CategoryToolErrors},
		{"compaction", CategoryCompaction},
		{"patterns", CategoryPatterns},
		{"unknown", CategoryPatterns},
		{"", CategoryPatterns},
	}
	for _, tt := range tests {
		got := parseCategory(tt.input)
		if got != tt.want {
			t.Errorf("parseCategory(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestAsAnalysisModules(t *testing.T) {
	scripts := []*ScriptModule{
		NewScriptModule("/a/test.sh", "project"),
		NewScriptModule("/b/other.py", "global"),
	}
	modules := AsAnalysisModules(scripts)
	if len(modules) != 2 {
		t.Fatalf("got %d modules, want 2", len(modules))
	}
	// Verify they implement the interface properly
	for _, m := range modules {
		if m.Name() == "" {
			t.Error("module Name() should not be empty")
		}
	}
}

func TestScriptModulesInfo(t *testing.T) {
	scripts := []*ScriptModule{
		NewScriptModule("/path/to/test.sh", "project"),
	}
	infos := ScriptModulesInfo(scripts)
	if len(infos) != 1 {
		t.Fatalf("got %d infos, want 1", len(infos))
	}
	if infos[0].Source != "project" {
		t.Errorf("Source = %q, want %q", infos[0].Source, "project")
	}
}

// ── Integration: ScriptModule with RunModules ───────────────────────────────

func TestScriptModule_IntegrationWithRunModules(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "detect-integration.sh")
	content := `#!/bin/sh
echo '[{"id": "script-test", "severity": "medium", "title": "Script found something", "observation": "obs", "impact": "impact"}]'
`
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	sm := NewScriptModule(script, "project")
	modules := []AnalysisModule{sm}
	sess := &session.Session{ID: "ses_int_test"}
	r := &InspectReport{}

	problems, results := RunModules(modules, sess, r)
	if len(problems) != 1 {
		t.Fatalf("got %d problems, want 1", len(problems))
	}
	if problems[0].ID != "script-test" {
		t.Errorf("ID = %q, want %q", problems[0].ID, "script-test")
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if !results[0].Activated {
		t.Error("script module should always be activated")
	}
	if results[0].Problems != 1 {
		t.Errorf("result problems = %d, want 1", results[0].Problems)
	}
}
