package service

import (
	"regexp"
	"testing"
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
