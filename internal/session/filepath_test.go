package session

import (
	"testing"
)

func TestNormalizeFilePath(t *testing.T) {
	tests := []struct {
		name        string
		filePath    string
		projectRoot string
		want        string
	}{
		{
			name:        "in-project absolute becomes relative",
			filePath:    "/Users/x/repo/src/main.go",
			projectRoot: "/Users/x/repo",
			want:        "src/main.go",
		},
		{
			name:        "out-of-project absolute stays absolute",
			filePath:    "/tmp/cycloplan-ios.png",
			projectRoot: "/Users/x/repo",
			want:        "/tmp/cycloplan-ios.png",
		},
		{
			name:        "already relative stays unchanged (idempotent)",
			filePath:    "src/main.go",
			projectRoot: "/Users/x/repo",
			want:        "src/main.go",
		},
		{
			name:        "trailing slash on project root is handled",
			filePath:    "/Users/x/repo/src/main.go",
			projectRoot: "/Users/x/repo/",
			want:        "src/main.go",
		},
		{
			name:        "empty project root passes through",
			filePath:    "/Users/x/repo/src/main.go",
			projectRoot: "",
			want:        "/Users/x/repo/src/main.go",
		},
		{
			name:        "file equal to project root becomes dot",
			filePath:    "/Users/x/repo",
			projectRoot: "/Users/x/repo",
			want:        ".",
		},
		{
			name:        "case-insensitive match keeps original casing",
			filePath:    "/Users/X/Repo/src/Main.go",
			projectRoot: "/users/x/repo",
			want:        "src/Main.go",
		},
		{
			name:        "windows separators normalized to forward slash",
			filePath:    `C:\Users\x\repo\src\main.go`,
			projectRoot: `C:\Users\x\repo`,
			want:        "src/main.go",
		},
		{
			name:        "opencode worktree root strips to logical relative",
			filePath:    "/Users/g/.local/share/opencode/worktree/abc/crisp-harbor/internal/x.go",
			projectRoot: "/Users/g/.local/share/opencode/worktree/abc/crisp-harbor",
			want:        "internal/x.go",
		},
		{
			name:        "nested path under root",
			filePath:    "/Users/x/repo/internal/mcp/tools.go",
			projectRoot: "/Users/x/repo",
			want:        "internal/mcp/tools.go",
		},
		{
			name:        "sibling dir sharing root prefix is not stripped",
			filePath:    "/Users/x/repo-other/file.go",
			projectRoot: "/Users/x/repo",
			want:        "/Users/x/repo-other/file.go",
		},
		{
			name:        "empty file path stays empty",
			filePath:    "",
			projectRoot: "/Users/x/repo",
			want:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeFilePath(tt.filePath, tt.projectRoot)
			if got != tt.want {
				t.Errorf("NormalizeFilePath(%q, %q):\n  got  %q\n  want %q",
					tt.filePath, tt.projectRoot, got, tt.want)
			}
		})
	}
}

func TestIsAbsolutePath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/Users/x/repo/main.go", true},
		{"src/main.go", false},
		{"internal/mcp/tools.go", false},
		{`C:\Users\x\repo\main.go`, true},
		{"C:/Users/x/repo/main.go", true},
		{"d:/projects/app", true},
		{"", false},
		{"./relative.go", false},
		{"main.go", false},
	}
	for _, tt := range tests {
		if got := IsAbsolutePath(tt.path); got != tt.want {
			t.Errorf("IsAbsolutePath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestBlameMatchCandidates(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		projectRoot string
		want        []string
	}{
		{
			name:        "relative in-project yields relative and absolute",
			input:       "internal/mcp/tools.go",
			projectRoot: "/Users/x/repo",
			want:        []string{"internal/mcp/tools.go", "/Users/x/repo/internal/mcp/tools.go"},
		},
		{
			name:        "absolute in-project yields relative and absolute",
			input:       "/Users/x/repo/internal/mcp/tools.go",
			projectRoot: "/Users/x/repo",
			want:        []string{"internal/mcp/tools.go", "/Users/x/repo/internal/mcp/tools.go"},
		},
		{
			name:        "out-of-project absolute yields itself only",
			input:       "/tmp/cycloplan-ios.png",
			projectRoot: "/Users/x/repo",
			want:        []string{"/tmp/cycloplan-ios.png"},
		},
		{
			name:        "empty project root yields input only",
			input:       "internal/x.go",
			projectRoot: "",
			want:        []string{"internal/x.go"},
		},
		{
			name:        "windows backslash relative input is slash-normalized and anchored",
			input:       `internal\mcp\tools.go`,
			projectRoot: `C:\Users\x\repo`,
			want:        []string{"internal/mcp/tools.go", "C:/Users/x/repo/internal/mcp/tools.go"},
		},
		{
			name:        "empty input yields nil",
			input:       "",
			projectRoot: "/Users/x/repo",
			want:        nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BlameMatchCandidates(tt.input, tt.projectRoot)
			if !equalStrings(got, tt.want) {
				t.Errorf("BlameMatchCandidates(%q, %q):\n  got  %v\n  want %v",
					tt.input, tt.projectRoot, got, tt.want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestNormalizeFilePath_Idempotent verifies applying the function twice yields
// the same result as applying it once — required so capture, import, pull and
// the backfill migration can all run safely without double-stripping.
func TestNormalizeFilePath_Idempotent(t *testing.T) {
	cases := []struct {
		filePath    string
		projectRoot string
	}{
		{"/Users/x/repo/src/main.go", "/Users/x/repo"},
		{"/tmp/out.png", "/Users/x/repo"},
		{`C:\Users\x\repo\src\main.go`, `C:\Users\x\repo`},
		{"/Users/x/repo", "/Users/x/repo"},
	}
	for _, c := range cases {
		once := NormalizeFilePath(c.filePath, c.projectRoot)
		twice := NormalizeFilePath(once, c.projectRoot)
		if once != twice {
			t.Errorf("not idempotent for (%q, %q):\n  once  %q\n  twice %q",
				c.filePath, c.projectRoot, once, twice)
		}
	}
}
