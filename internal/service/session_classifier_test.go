package service

import (
	"regexp"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/config"
)

func TestExtractTickets(t *testing.T) {
	re := regexp.MustCompile(`(?i)OMO-\d+`)

	tests := []struct {
		name  string
		texts []string
		want  []string
	}{
		{"branch with ticket", []string{"feature/OMO-250-add-login"}, []string{"OMO-250"}},
		{"summary with ticket", []string{"", "Fix OMO-123 auth bug"}, []string{"OMO-123"}},
		{"multiple tickets", []string{"OMO-100 and OMO-200", "also OMO-300"}, []string{"OMO-100", "OMO-200", "OMO-300"}},
		{"duplicate dedup", []string{"OMO-250", "OMO-250"}, []string{"OMO-250"}},
		{"no match", []string{"feature/add-login", "fix bug"}, nil},
		{"case normalization", []string{"omo-42"}, []string{"OMO-42"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTickets(re, tt.texts...)
			if len(got) != len(tt.want) {
				t.Fatalf("extractTickets() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("extractTickets()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestMatchBranchRule(t *testing.T) {
	rules := map[string]string{
		"feature/*":  "feature",
		"fix/*":      "bug",
		"hotfix/*":   "bug",
		"refactor/*": "refactor",
	}

	tests := []struct {
		branch string
		want   string
	}{
		{"feature/add-login", "feature"},
		{"feature/OMO-250-deep-auth", "feature"},
		{"fix/slack-newlines", "bug"},
		{"hotfix/urgent-fix", "bug"},
		{"refactor/agent-reorganization", "refactor"},
		{"main", ""},
		{"master", ""},
		{"nimble-comet", ""},
	}

	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			got := matchBranchRule(rules, tt.branch)
			if got != tt.want {
				t.Errorf("matchBranchRule(%q) = %q, want %q", tt.branch, got, tt.want)
			}
		})
	}
}

func TestMatchConventionalCommit(t *testing.T) {
	rules := config.DefaultCommitRules

	tests := []struct {
		summary string
		want    string
	}{
		{"fix: auth bug", "bug"},
		{"feat: add login", "feature"},
		{"feat(auth): add OAuth", "feature"},
		{"refactor: cleanup code", "refactor"},
		{"chore: update deps", "devops"},
		{"docs: add README", "docs"},
		{"test: add unit tests", "review"},
		{"[COMMIT] fix: auth bug", "bug"},
		{"[COMMIT] 🔀 Fix auth tokens", "bug"},
		{"[PR] feat: new feature", "feature"},
		{"[WIP] refactor: agent reorg", "refactor"},
		{"Fix authentication flow", "bug"},
		{"Fixed the login issue", "bug"},
		{"Add new payment module", "feature"},
		{"Refactor agent architecture", "refactor"},
		{"Migrate database schema", "refactor"},
		{"random session title", ""},
		{"Explore codebase", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.summary, func(t *testing.T) {
			got := matchConventionalCommit(rules, tt.summary)
			if got != tt.want {
				t.Errorf("matchConventionalCommit(%q) = %q, want %q", tt.summary, got, tt.want)
			}
		})
	}
}

func TestMatchSummaryPrefix(t *testing.T) {
	rules := config.DefaultStatusRules

	tests := []struct {
		summary string
		want    string
	}{
		{"[WIP] working on feature", "active"},
		{"[DONE] finished task", "completed"},
		{"[PR] create pull request", "review"},
		{"[COMMIT] pushed code", "completed"},
		{"Normal session", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.summary, func(t *testing.T) {
			got := matchSummaryPrefix(rules, tt.summary)
			if got != tt.want {
				t.Errorf("matchSummaryPrefix(%q) = %q, want %q", tt.summary, got, tt.want)
			}
		})
	}
}
