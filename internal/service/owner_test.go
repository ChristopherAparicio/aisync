package service

import (
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestClassifyUserKind(t *testing.T) {
	defaultPatterns := []string{
		"*[bot]@*",
		"ci@*",
		"bot@*",
		"automation@*",
		"dependabot*",
		"renovate*",
		"github-actions*",
	}

	tests := []struct {
		name     string
		email    string
		patterns []string
		want     session.UserKind
	}{
		{
			name:     "empty email",
			email:    "",
			patterns: defaultPatterns,
			want:     session.UserKindUnknown,
		},
		{
			name:     "regular human email",
			email:    "john@company.com",
			patterns: defaultPatterns,
			want:     session.UserKindHuman,
		},
		{
			name:     "bot email with [bot] suffix",
			email:    "claude-reviewer[bot]@users.noreply.github.com",
			patterns: defaultPatterns,
			want:     session.UserKindMachine,
		},
		{
			name:     "ci email prefix",
			email:    "ci@company.com",
			patterns: defaultPatterns,
			want:     session.UserKindMachine,
		},
		{
			name:     "bot email prefix",
			email:    "bot@deploy.io",
			patterns: defaultPatterns,
			want:     session.UserKindMachine,
		},
		{
			name:     "automation email prefix",
			email:    "automation@pipeline.dev",
			patterns: defaultPatterns,
			want:     session.UserKindMachine,
		},
		{
			name:     "dependabot",
			email:    "dependabot[bot]@github.com",
			patterns: defaultPatterns,
			want:     session.UserKindMachine,
		},
		{
			name:     "renovate",
			email:    "renovate-bot@whitesourcesoftware.com",
			patterns: defaultPatterns,
			want:     session.UserKindMachine,
		},
		{
			name:     "github-actions",
			email:    "github-actions[bot]@users.noreply.github.com",
			patterns: defaultPatterns,
			want:     session.UserKindMachine,
		},
		{
			name:     "noreply without bot — unknown",
			email:    "12345+user@users.noreply.github.com",
			patterns: defaultPatterns,
			want:     session.UserKindUnknown,
		},
		{
			name:     "nil patterns — noreply still unknown",
			email:    "12345+user@users.noreply.github.com",
			patterns: nil,
			want:     session.UserKindUnknown,
		},
		{
			name:     "nil patterns — regular email is human",
			email:    "john@company.com",
			patterns: nil,
			want:     session.UserKindHuman,
		},
		{
			name:     "case insensitive matching",
			email:    "CI@Company.COM",
			patterns: defaultPatterns,
			want:     session.UserKindMachine,
		},
		{
			name:     "custom pattern",
			email:    "deploy-bot@internal.dev",
			patterns: []string{"deploy-bot@*"},
			want:     session.UserKindMachine,
		},
		{
			name:     "email with plus addressing — human",
			email:    "john+test@company.com",
			patterns: defaultPatterns,
			want:     session.UserKindHuman,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyUserKind(tt.email, tt.patterns)
			if got != tt.want {
				t.Errorf("ClassifyUserKind(%q, ...) = %q, want %q", tt.email, got, tt.want)
			}
		})
	}
}

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern string
		text    string
		want    bool
	}{
		{"*", "anything", true},
		{"*", "", true},
		{"", "", true},
		{"", "x", false},
		{"abc", "abc", true},
		{"abc", "def", false},
		{"*@*", "user@host.com", true},
		{"ci@*", "ci@company.com", true},
		{"ci@*", "notci@company.com", false},
		{"*[bot]@*", "claude[bot]@github.com", true},
		{"*[bot]@*", "user@github.com", false},
		{"dependabot*", "dependabot[bot]@github.com", true},
		{"dependabot*", "xdependabot@github.com", false},
		{"*.test", "foo.test", true},
		{"*.test", "foo.tests", false},
		{"a*b*c", "abc", true},
		{"a*b*c", "aXXbYYc", true},
		{"a*b*c", "aXXc", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.text, func(t *testing.T) {
			got := globMatch(tt.pattern, tt.text)
			if got != tt.want {
				t.Errorf("globMatch(%q, %q) = %v, want %v", tt.pattern, tt.text, got, tt.want)
			}
		})
	}
}
