package session

import (
	"testing"
)

func TestNormalizeCommand_paths(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"cat /usr/local/bin/foo.txt", "cat /PATH"},
		{"ls /tmp/abc/def", "ls /PATH"},
		{"git diff /Users/john/project/src/main.go", "git diff /PATH"},
	}
	for _, tt := range tests {
		got := NormalizeCommand(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeCommand(%q):\n  got  %q\n  want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeCommand_digits(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"lsof -i :8080", "lsof -i :N"},
		{"kill -9 12345", "kill -N N"},
		{"sleep 30", "sleep N"},
	}
	for _, tt := range tests {
		got := NormalizeCommand(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeCommand(%q):\n  got  %q\n  want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeCommand_quotedStrings(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`echo "hello world"`, `echo "..."`},
		{`grep 'pattern' file`, `grep '...' file`},
		{`curl -H "Authorization: Bearer abc123"`, `curl -H "..."`},
	}
	for _, tt := range tests {
		got := NormalizeCommand(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeCommand(%q):\n  got  %q\n  want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeCommand_UUIDs(t *testing.T) {
	got := NormalizeCommand("docker rm 7e07485f-12f9-416b-8b14-26260799b51f")
	want := "docker rm UUID"
	if got != want {
		t.Errorf("NormalizeCommand UUID:\n  got  %q\n  want %q", got, want)
	}
}

func TestNormalizeCommand_SHAs(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"git show abc1234", "git show SHA"},
		{"git diff abc1234def5678abc1234def5678abc1234d", "git diff SHA"},
	}
	for _, tt := range tests {
		got := NormalizeCommand(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeCommand(%q):\n  got  %q\n  want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeCommand_combined(t *testing.T) {
	input := `curl -X POST "http://localhost:3000/api/v1/sessions/7e07485f-12f9-416b-8b14-26260799b51f" -H "Authorization: Bearer token123"`
	got := NormalizeCommand(input)

	// Quoted strings replaced, UUID in URL also handled, path normalized.
	// The exact output depends on ordering, but key transformations should apply:
	// - "http://...UUID..." → "..."  (inside double quotes)
	// - "Authorization: Bearer token123" → "..."
	// The double-quoted strings get replaced FIRST, so the URLs inside them become "...".
	want := `curl -X POST "..." -H "..."`
	if got != want {
		t.Errorf("NormalizeCommand combined:\n  got  %q\n  want %q", got, want)
	}
}

func TestNormalizeCommand_whitespace(t *testing.T) {
	got := NormalizeCommand("  git   status    --short  ")
	want := "git status --short"
	if got != want {
		t.Errorf("NormalizeCommand whitespace:\n  got  %q\n  want %q", got, want)
	}
}

func TestFindCommandPatterns_basic(t *testing.T) {
	// Create 5 invocations of the same long command across 3 sessions.
	inputs := []CommandPatternInput{
		{FullCommand: "curl -X POST http://localhost:3000/api/v1/sessions -H 'Authorization: Bearer token123' -d '{\"key\":\"value\"}' --connect-timeout 30 --max-time 60", SessionID: "s1", ProjectPath: "/proj/a", OutputBytes: 500},
		{FullCommand: "curl -X POST http://localhost:3000/api/v1/sessions -H 'Authorization: Bearer token456' -d '{\"key\":\"value2\"}' --connect-timeout 30 --max-time 60", SessionID: "s1", ProjectPath: "/proj/a", OutputBytes: 600},
		{FullCommand: "curl -X POST http://localhost:3000/api/v1/sessions -H 'Authorization: Bearer token789' -d '{\"key\":\"value3\"}' --connect-timeout 30 --max-time 60", SessionID: "s2", ProjectPath: "/proj/a", OutputBytes: 450},
		{FullCommand: "curl -X POST http://localhost:3000/api/v1/sessions -H 'Authorization: Bearer tokenABC' -d '{\"key\":\"value4\"}' --connect-timeout 30 --max-time 60", SessionID: "s3", ProjectPath: "/proj/b", OutputBytes: 700},
		{FullCommand: "curl -X POST http://localhost:3000/api/v1/sessions -H 'Authorization: Bearer tokenDEF' -d '{\"key\":\"value5\"}' --connect-timeout 30 --max-time 60", SessionID: "s3", ProjectPath: "/proj/b", OutputBytes: 800},
		// A short command that should be filtered out (< 100 chars).
		{FullCommand: "git status", SessionID: "s1", ProjectPath: "/proj/a", OutputBytes: 100},
	}

	patterns := FindCommandPatterns(inputs, 100, 3)

	if len(patterns) != 1 {
		t.Fatalf("FindCommandPatterns: want 1 pattern, got %d", len(patterns))
	}

	p := patterns[0]
	if p.Occurrences != 5 {
		t.Errorf("Occurrences: want 5, got %d", p.Occurrences)
	}
	if p.SessionCount != 3 {
		t.Errorf("SessionCount: want 3, got %d", p.SessionCount)
	}
	if p.ProjectCount != 2 {
		t.Errorf("ProjectCount: want 2, got %d", p.ProjectCount)
	}
	if p.TotalOutput != 3050 {
		t.Errorf("TotalOutput: want 3050, got %d", p.TotalOutput)
	}
}

func TestFindCommandPatterns_minCountFilter(t *testing.T) {
	// 2 occurrences with minCount=3 → should be empty.
	inputs := []CommandPatternInput{
		{FullCommand: "npm run build --prefix=/Users/john/project --verbose --production --no-optional-dependencies 2>&1 | tee build.log", SessionID: "s1", ProjectPath: "/proj"},
		{FullCommand: "npm run build --prefix=/Users/jane/project --verbose --production --no-optional-dependencies 2>&1 | tee build.log", SessionID: "s2", ProjectPath: "/proj"},
	}

	patterns := FindCommandPatterns(inputs, 100, 3)
	if len(patterns) != 0 {
		t.Errorf("FindCommandPatterns: want 0 patterns (below minCount), got %d", len(patterns))
	}
}

func TestFindCommandPatterns_minLengthFilter(t *testing.T) {
	// Short commands with minLength=100 → all filtered out.
	inputs := []CommandPatternInput{
		{FullCommand: "git status", SessionID: "s1", ProjectPath: "/proj"},
		{FullCommand: "git status", SessionID: "s2", ProjectPath: "/proj"},
		{FullCommand: "git status", SessionID: "s3", ProjectPath: "/proj"},
	}

	patterns := FindCommandPatterns(inputs, 100, 3)
	if len(patterns) != 0 {
		t.Errorf("FindCommandPatterns: want 0 patterns (below minLength), got %d", len(patterns))
	}
}

func TestFindCommandPatterns_empty(t *testing.T) {
	patterns := FindCommandPatterns(nil, 0, 0)
	if len(patterns) != 0 {
		t.Errorf("FindCommandPatterns: want 0, got %d", len(patterns))
	}
}

func TestFindCommandPatterns_sortedByTotalChars(t *testing.T) {
	// Pattern A: 3x 150 chars = 450 total. Pattern B: 3x 200 chars = 600 total.
	shortCmd := "go test ./internal/session/ -v -run TestComputeHotspots -count=1 -timeout=60s 2>&1 | head -100 ; echo done with test"
	longCmd := "docker compose -f /path/to/docker-compose.yml up --build --force-recreate --no-deps --remove-orphans service-name 2>&1 | tee /tmp/docker-output.log ; echo done with docker compose rebuild"

	var inputs []CommandPatternInput
	for i := 0; i < 3; i++ {
		inputs = append(inputs, CommandPatternInput{FullCommand: shortCmd, SessionID: ID("s1"), ProjectPath: "/proj"})
		inputs = append(inputs, CommandPatternInput{FullCommand: longCmd, SessionID: ID("s1"), ProjectPath: "/proj"})
	}

	patterns := FindCommandPatterns(inputs, 100, 3)
	if len(patterns) != 2 {
		t.Fatalf("want 2 patterns, got %d", len(patterns))
	}

	// Pattern B (longCmd) should come first (higher TotalChars).
	if patterns[0].TotalChars <= patterns[1].TotalChars {
		t.Errorf("patterns should be sorted by TotalChars desc: %d <= %d",
			patterns[0].TotalChars, patterns[1].TotalChars)
	}
}
