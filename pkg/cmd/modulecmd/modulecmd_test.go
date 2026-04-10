package modulecmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

func TestModuleInit_Python(t *testing.T) {
	dir := t.TempDir()

	out := &bytes.Buffer{}
	io := &iostreams.IOStreams{Out: out, ErrOut: &bytes.Buffer{}}
	cmd := NewCmdModuleForTest(io)

	// Override working directory by changing the project dir
	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	cmd.SetArgs([]string{"init", "detect-test"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// Verify file was created
	path := filepath.Join(dir, ".aisync", "modules", "detect-test.py")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("module not created: %v", err)
	}

	// Check executable
	if info.Mode()&0o111 == 0 {
		t.Error("module should be executable")
	}

	// Check output
	if !bytes.Contains(out.Bytes(), []byte("Created module:")) {
		t.Error("output should contain 'Created module:'")
	}
}

func TestModuleInit_Shell(t *testing.T) {
	dir := t.TempDir()

	out := &bytes.Buffer{}
	io := &iostreams.IOStreams{Out: out, ErrOut: &bytes.Buffer{}}
	cmd := NewCmdModuleForTest(io)

	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	cmd.SetArgs([]string{"init", "detect-test", "--sh"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --sh failed: %v", err)
	}

	path := filepath.Join(dir, ".aisync", "modules", "detect-test.sh")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("shell module not created: %v", err)
	}
}

func TestModuleInit_AlreadyExists(t *testing.T) {
	dir := t.TempDir()

	out := &bytes.Buffer{}
	io := &iostreams.IOStreams{Out: out, ErrOut: &bytes.Buffer{}}

	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	// Create first
	cmd1 := NewCmdModuleForTest(io)
	cmd1.SetArgs([]string{"init", "detect-dup"})
	if err := cmd1.Execute(); err != nil {
		t.Fatalf("first init failed: %v", err)
	}

	// Try to create again — should error
	cmd2 := NewCmdModuleForTest(io)
	cmd2.SetArgs([]string{"init", "detect-dup"})
	if err := cmd2.Execute(); err == nil {
		t.Error("second init should fail (file already exists)")
	}
}

func TestModuleList_Empty(t *testing.T) {
	dir := t.TempDir()

	out := &bytes.Buffer{}
	io := &iostreams.IOStreams{Out: out, ErrOut: &bytes.Buffer{}}
	cmd := NewCmdModuleForTest(io)

	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list failed: %v", err)
	}

	if !bytes.Contains(out.Bytes(), []byte("No script modules found")) {
		t.Error("empty list should say 'No script modules found'")
	}
}

// ── extractScript tests ─────────────────────────────────────────────────────

func TestExtractScript_PythonFence(t *testing.T) {
	input := "Here is the script:\n\n```python\n#!/usr/bin/env python3\nimport json\nimport sys\n\ndef main():\n    print('hello')\n\nif __name__ == '__main__':\n    main()\n```\n\nThat's it!"

	got := extractScript(input)

	if !strings.HasPrefix(got, "#!/usr/bin/env python3\n") {
		t.Errorf("should start with shebang, got: %q", got[:min(40, len(got))])
	}
	if !strings.Contains(got, "import json") {
		t.Error("should contain script body")
	}
	if !strings.HasSuffix(got, "\n") {
		t.Error("should end with newline")
	}
	// Should not contain the surrounding prose
	if strings.Contains(got, "Here is the script") {
		t.Error("should strip surrounding prose")
	}
	if strings.Contains(got, "That's it") {
		t.Error("should strip trailing prose")
	}
}

func TestExtractScript_PythonFence_NoShebang(t *testing.T) {
	input := "```python\nimport json\nimport sys\n\nprint('hello')\n```"

	got := extractScript(input)

	if !strings.HasPrefix(got, "#!/usr/bin/env python3\n") {
		t.Errorf("should prepend shebang when missing, got: %q", got)
	}
	if !strings.Contains(got, "import json") {
		t.Error("should contain script body")
	}
}

func TestExtractScript_GenericFence(t *testing.T) {
	input := "```\n#!/usr/bin/env python3\nimport sys\nprint('ok')\n```"

	got := extractScript(input)

	if !strings.HasPrefix(got, "#!/usr/bin/env python3\n") {
		t.Errorf("should extract from generic fence, got: %q", got)
	}
	if !strings.Contains(got, "import sys") {
		t.Error("should contain script body")
	}
}

func TestExtractScript_NoFence_Shebang(t *testing.T) {
	input := "#!/usr/bin/env python3\nimport json\nprint('raw script')"

	got := extractScript(input)

	if !strings.HasPrefix(got, "#!/usr/bin/env python3") {
		t.Error("should recognize raw script starting with shebang")
	}
	if !strings.Contains(got, "import json") {
		t.Error("should contain script body")
	}
}

func TestExtractScript_NoFence_Import(t *testing.T) {
	input := "import json\nimport sys\n\ndef main():\n    pass"

	got := extractScript(input)

	if !strings.HasPrefix(got, "import json") {
		t.Errorf("should recognize script starting with 'import', got: %q", got)
	}
}

func TestExtractScript_Garbage(t *testing.T) {
	input := "I'm sorry, I can't help with that. Here's a poem instead."

	got := extractScript(input)

	if got != "" {
		t.Errorf("should return empty for non-script content, got: %q", got)
	}
}

func TestExtractScript_EmptyInput(t *testing.T) {
	got := extractScript("")

	if got != "" {
		t.Errorf("should return empty for empty input, got: %q", got)
	}
}

func TestExtractScript_MultipleFences(t *testing.T) {
	// Should extract the python fence (first match), not any later one
	input := "Here's the JSON schema:\n```json\n{\"id\": \"test\"}\n```\n\nAnd the script:\n```python\n#!/usr/bin/env python3\nimport json\nprint('hello')\n```"

	got := extractScript(input)

	if !strings.HasPrefix(got, "#!/usr/bin/env python3\n") {
		t.Errorf("should prefer ```python fence, got: %q", got[:min(60, len(got))])
	}
	if strings.Contains(got, `"id": "test"`) {
		t.Error("should not contain JSON fence content")
	}
	if !strings.Contains(got, "import json") {
		t.Error("should contain python script body")
	}
}

// ── suggestName tests ───────────────────────────────────────────────────────

func TestSuggestName(t *testing.T) {
	tests := []struct {
		description string
		want        string
	}{
		{
			description: "Detect when pytest is run more than 5 times",
			want:        "detect-when-pytest",
		},
		{
			description: "Flag sessions where npm is used instead of pnpm",
			want:        "detect-sessions-where",
		},
		{
			description: "Alert when git push happens without tests",
			want:        "detect-when-git",
		},
		{
			description: "Find repeated curl commands",
			want:        "detect-repeated-curl",
		},
		{
			description: "Check for missing error handling",
			want:        "detect-for-missing",
		},
		{
			description: "Something that doesn't match any prefix",
			want:        "detect-custom",
		},
		{
			description: "",
			want:        "detect-custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			got := suggestName(tt.description)
			if got != tt.want {
				t.Errorf("suggestName(%q) = %q, want %q", tt.description, got, tt.want)
			}
		})
	}
}

func TestSuggestName_SpecialChars(t *testing.T) {
	got := suggestName("Detect when 'npm install' fails repeatedly!")
	// Should only contain lowercase letters, digits, hyphens
	for _, r := range got {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			t.Errorf("suggestName result contains invalid char %q in %q", string(r), got)
		}
	}
	// Should still start with "detect-"
	if !strings.HasPrefix(got, "detect-") {
		t.Errorf("expected 'detect-' prefix, got: %q", got)
	}
}

func TestSuggestName_CaseInsensitive(t *testing.T) {
	// suggestName should normalize case
	got := suggestName("DETECT INFINITE LOOPS")
	want := "detect-infinite-loops"
	if got != want {
		t.Errorf("suggestName uppercase = %q, want %q", got, want)
	}
}

// min is a helper used in tests (Go 1.21+ has it as a builtin but
// we keep this for clarity and backward compatibility with older toolchains).
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
