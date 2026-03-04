package secrets

import (
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestDefaultPatterns_loads(t *testing.T) {
	patterns := DefaultPatterns()
	if len(patterns) == 0 {
		t.Fatal("DefaultPatterns() returned empty — embedded patterns.txt broken")
	}
	// We have at least 15 patterns in patterns.txt
	if len(patterns) < 10 {
		t.Errorf("expected at least 10 patterns, got %d", len(patterns))
	}
}

func TestParsePatterns(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCount int
		wantErr   bool
	}{
		{
			name:      "simple pattern",
			input:     "API_KEY  sk-[A-Za-z0-9]{20,}",
			wantCount: 1,
		},
		{
			name:      "comments and blanks ignored",
			input:     "# comment\n\nAPI_KEY  sk-[A-Za-z0-9]{20,}\n# another\n",
			wantCount: 1,
		},
		{
			name:      "multiple patterns",
			input:     "AWS  AKIA[0-9A-Z]{16}\nGH  ghp_[A-Za-z0-9]{36}\n",
			wantCount: 2,
		},
		{
			name:    "invalid regex",
			input:   "BAD  [invalid(",
			wantErr: true,
		},
		{
			name:    "missing regex",
			input:   "LONELY",
			wantErr: true,
		},
		{
			name:      "tab separated",
			input:     "TOKEN\tghp_[A-Za-z0-9]{36}",
			wantCount: 1,
		},
		{
			name:      "empty input",
			input:     "",
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patterns, err := ParsePatterns(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(patterns) != tt.wantCount {
				t.Errorf("got %d patterns, want %d", len(patterns), tt.wantCount)
			}
		})
	}
}

func TestScanner_Scan(t *testing.T) {
	scanner := NewScanner(session.SecretModeMask, nil)

	tests := []struct {
		name      string
		input     string
		wantTypes []string
	}{
		{
			name:      "AWS access key",
			input:     "my key is AKIAIOSFODNN7EXAMPLE",
			wantTypes: []string{"AWS_ACCESS_KEY"},
		},
		{
			name:      "GitHub token",
			input:     "my ghp_ABCDEFghijklmnop1234567890abcdefghij here",
			wantTypes: []string{"GITHUB_TOKEN"},
		},
		{
			name:      "OpenAI API key",
			input:     "OPENAI_API_KEY=sk-proj1234567890abcdefghijklmnop",
			wantTypes: []string{"OPENAI_API_KEY"},
		},
		{
			name:      "private key header",
			input:     "-----BEGIN RSA PRIVATE KEY-----\nMIIE...",
			wantTypes: []string{"PRIVATE_KEY"},
		},
		{
			name:      "no secrets",
			input:     "just regular code with no secrets at all",
			wantTypes: nil,
		},
		{
			name:      "multiple secrets in one string",
			input:     "AKIAIOSFODNN7EXAMPLE and ghp_ABCDEFghijklmnop1234567890abcdefghij",
			wantTypes: []string{"AWS_ACCESS_KEY", "GITHUB_TOKEN"},
		},
		{
			name:      "Anthropic key",
			input:     "key: sk-ant-abc123def456ghi789jkl012",
			wantTypes: []string{"ANTHROPIC_API_KEY"},
		},
		{
			name:      "GitLab token",
			input:     "TOKEN=glpat-abcdefghijklmnopqrst",
			wantTypes: []string{"GITLAB_TOKEN"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := scanner.Scan(tt.input)

			if len(tt.wantTypes) == 0 && len(matches) > 0 {
				t.Errorf("expected no matches, got %d: %v", len(matches), matches)
				return
			}

			// Check each expected type is found
			foundTypes := make(map[string]bool)
			for _, m := range matches {
				foundTypes[m.Type] = true
			}
			for _, want := range tt.wantTypes {
				if !foundTypes[want] {
					t.Errorf("expected to find %s, but it was not detected (found: %v)", want, foundTypes)
				}
			}
		})
	}
}

func TestScanner_Mask(t *testing.T) {
	scanner := NewScanner(session.SecretModeMask, nil)

	tests := []struct {
		name        string
		input       string
		wantContain string
		wantExclude string
	}{
		{
			name:        "masks AWS key",
			input:       "my key is AKIAIOSFODNN7EXAMPLE",
			wantContain: "***REDACTED:AWS_ACCESS_KEY***",
			wantExclude: "AKIAIOSFODNN7EXAMPLE",
		},
		{
			name:        "masks GitHub token",
			input:       "my ghp_ABCDEFghijklmnop1234567890abcdefghij here",
			wantContain: "***REDACTED:GITHUB_TOKEN***",
			wantExclude: "ghp_ABCDEFghijklmnop1234567890abcdefghij",
		},
		{
			name:        "no secrets returns unchanged",
			input:       "just regular text",
			wantContain: "just regular text",
		},
		{
			name:        "masks private key header",
			input:       "-----BEGIN RSA PRIVATE KEY-----",
			wantContain: "***REDACTED:PRIVATE_KEY***",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scanner.Mask(tt.input)

			if !strings.Contains(got, tt.wantContain) {
				t.Errorf("Mask() = %q, want to contain %q", got, tt.wantContain)
			}
			if tt.wantExclude != "" && strings.Contains(got, tt.wantExclude) {
				t.Errorf("Mask() = %q, should NOT contain %q", got, tt.wantExclude)
			}
		})
	}
}

func TestScanner_Mode(t *testing.T) {
	tests := []struct {
		mode session.SecretMode
	}{
		{session.SecretModeMask},
		{session.SecretModeWarn},
		{session.SecretModeBlock},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			scanner := NewScanner(tt.mode, nil)
			if scanner.Mode() != tt.mode {
				t.Errorf("Mode() = %q, want %q", scanner.Mode(), tt.mode)
			}
		})
	}
}

func TestScanner_ScanSession(t *testing.T) {
	scanner := NewScanner(session.SecretModeMask, nil)

	sess := &session.Session{
		Messages: []session.Message{
			{
				Content: "Here is my key: AKIAIOSFODNN7EXAMPLE",
				Role:    session.RoleUser,
			},
			{
				Content: "I see your AWS key",
				Role:    session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{
						Output: "Found ghp_ABCDEFghijklmnop1234567890abcdefghij in code",
					},
				},
			},
			{
				Content: "No secrets here",
				Role:    session.RoleUser,
			},
		},
	}

	matches := scanner.ScanSession(sess)
	if len(matches) < 2 {
		t.Fatalf("expected at least 2 matches, got %d", len(matches))
	}

	foundTypes := make(map[string]bool)
	for _, m := range matches {
		foundTypes[m.Type] = true
	}
	if !foundTypes["AWS_ACCESS_KEY"] {
		t.Error("AWS_ACCESS_KEY not detected in session")
	}
	if !foundTypes["GITHUB_TOKEN"] {
		t.Error("GITHUB_TOKEN not detected in session")
	}
}

func TestScanner_MaskSession(t *testing.T) {
	scanner := NewScanner(session.SecretModeMask, nil)

	sess := &session.Session{
		Messages: []session.Message{
			{
				Content: "key=AKIAIOSFODNN7EXAMPLE",
				Role:    session.RoleUser,
			},
			{
				Content: "checking...",
				Role:    session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{Output: "found ghp_ABCDEFghijklmnop1234567890abcdefghij in code"},
				},
			},
		},
	}

	scanner.MaskSession(sess)

	if strings.Contains(sess.Messages[0].Content, "AKIA") {
		t.Error("message content should be masked")
	}
	if !strings.Contains(sess.Messages[0].Content, "***REDACTED:") {
		t.Error("message content should contain redacted placeholder")
	}

	toolOutput := sess.Messages[1].ToolCalls[0].Output
	if strings.Contains(toolOutput, "ghp_") {
		t.Error("tool call output should be masked")
	}
	if !strings.Contains(toolOutput, "***REDACTED:") {
		t.Error("tool call output should contain redacted placeholder")
	}
}

func TestScanner_AddPatterns(t *testing.T) {
	scanner := NewScanner(session.SecretModeMask, []Pattern{})
	if scanner.PatternCount() != 0 {
		t.Fatalf("expected 0 patterns, got %d", scanner.PatternCount())
	}

	custom, err := ParsePatterns("CUSTOM_KEY  custom_[a-z]{10}")
	if err != nil {
		t.Fatal(err)
	}

	scanner.AddPatterns(custom)
	if scanner.PatternCount() != 1 {
		t.Errorf("expected 1 pattern after add, got %d", scanner.PatternCount())
	}

	matches := scanner.Scan("found custom_abcdefghij here")
	if len(matches) != 1 {
		t.Errorf("expected 1 match, got %d", len(matches))
	}
	if len(matches) > 0 && matches[0].Type != "CUSTOM_KEY" {
		t.Errorf("match type = %q, want CUSTOM_KEY", matches[0].Type)
	}
}

func TestFormatMatches(t *testing.T) {
	tests := []struct {
		name    string
		want    string
		matches []session.SecretMatch
	}{
		{
			name:    "no matches",
			matches: nil,
			want:    "no secrets found",
		},
		{
			name: "single match",
			matches: []session.SecretMatch{
				{Type: "AWS_ACCESS_KEY"},
			},
			want: "AWS_ACCESS_KEY",
		},
		{
			name: "multiple same type",
			matches: []session.SecretMatch{
				{Type: "AWS_ACCESS_KEY"},
				{Type: "AWS_ACCESS_KEY"},
			},
			want: "AWS_ACCESS_KEY (2)",
		},
		{
			name: "different types",
			matches: []session.SecretMatch{
				{Type: "AWS_ACCESS_KEY"},
				{Type: "GITHUB_TOKEN"},
			},
			want: "AWS_ACCESS_KEY, GITHUB_TOKEN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatMatches(tt.matches)
			if got != tt.want {
				t.Errorf("FormatMatches() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScanner_withCustomPatterns(t *testing.T) {
	custom, err := ParsePatterns("MY_SECRET  mysecret_[0-9]{8}")
	if err != nil {
		t.Fatal(err)
	}

	scanner := NewScanner(session.SecretModeMask, custom)

	matches := scanner.Scan("found mysecret_12345678 here")
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Type != "MY_SECRET" {
		t.Errorf("Type = %q, want MY_SECRET", matches[0].Type)
	}

	// Default patterns should NOT match since we only loaded custom
	awsMatches := scanner.Scan("AKIAIOSFODNN7EXAMPLE")
	if len(awsMatches) != 0 {
		t.Error("default patterns should not be loaded when custom provided")
	}
}
