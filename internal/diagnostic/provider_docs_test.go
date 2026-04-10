package diagnostic

import (
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestProviderDocLinks_AlwaysIncludesAisync(t *testing.T) {
	providers := []session.ProviderName{
		session.ProviderOpenCode,
		session.ProviderClaudeCode,
		session.ProviderCursor,
		"unknown-provider",
	}

	for _, p := range providers {
		links := ProviderDocLinks(p)
		if len(links) == 0 {
			t.Errorf("ProviderDocLinks(%q) returned 0 links", p)
			continue
		}
		// Last link should always be the aisync self-reference.
		last := links[len(links)-1]
		if last.Label != "aisync inspect documentation" {
			t.Errorf("ProviderDocLinks(%q): last link label = %q, want aisync self-reference", p, last.Label)
		}
	}
}

func TestProviderDocLinks_ProviderSpecific(t *testing.T) {
	tests := []struct {
		provider session.ProviderName
		minLinks int // minimum expected (provider-specific + aisync)
	}{
		{session.ProviderOpenCode, 3},   // 2 provider + 1 aisync
		{session.ProviderClaudeCode, 3}, // 2 provider + 1 aisync
		{session.ProviderCursor, 2},     // 1 provider + 1 aisync
	}

	for _, tc := range tests {
		links := ProviderDocLinks(tc.provider)
		if len(links) < tc.minLinks {
			t.Errorf("ProviderDocLinks(%q): got %d links, want >= %d", tc.provider, len(links), tc.minLinks)
		}
		// All links should have non-empty Label and URL.
		for i, l := range links {
			if l.Label == "" {
				t.Errorf("ProviderDocLinks(%q)[%d].Label is empty", tc.provider, i)
			}
			if l.URL == "" {
				t.Errorf("ProviderDocLinks(%q)[%d].URL is empty", tc.provider, i)
			}
		}
	}
}

func TestArtefactDocLinks(t *testing.T) {
	// OpenCode skill should have a link.
	links := ArtefactDocLinks(session.ProviderOpenCode, ArtefactSkill)
	if len(links) == 0 {
		t.Error("ArtefactDocLinks(opencode, skill) returned 0 links")
	}

	// Claude Code command should have a link.
	links = ArtefactDocLinks(session.ProviderClaudeCode, ArtefactCommand)
	if len(links) == 0 {
		t.Error("ArtefactDocLinks(claude-code, command) returned 0 links")
	}

	// Unknown combination should return nil.
	links = ArtefactDocLinks("unknown", ArtefactScript)
	if len(links) != 0 {
		t.Errorf("ArtefactDocLinks(unknown, script) returned %d links, want 0", len(links))
	}
}

func TestGenerateFixes_IncludesDocLinks(t *testing.T) {
	// Create a minimal report with a problem that has a fix generator.
	report := &InspectReport{
		SessionID: "test-001",
		Provider:  "opencode",
		Problems: []Problem{
			{
				ID:          ProblemToolErrorLoops,
				Severity:    SeverityMedium,
				Category:    CategoryToolErrors,
				Title:       "Tool error loops",
				Observation: "3 error loops detected",
				Impact:      "wasted 5000 tokens",
				Metric:      3,
				MetricUnit:  "count",
			},
		},
		ToolErrors: &ToolErrorSection{
			ErrorCount: 5,
			ErrorRate:  0.3,
			ErrorLoops: []ErrorLoop{
				{ToolName: "Bash", ErrorCount: 4, StartMsgIdx: 10, EndMsgIdx: 13},
			},
		},
	}

	fs := GenerateFixes(report, session.ProviderOpenCode)
	if len(fs.Fixes) == 0 {
		t.Fatal("expected at least 1 fix")
	}

	fix := fs.Fixes[0]
	if len(fix.DocLinks) == 0 {
		t.Fatal("expected doc links on fix")
	}

	// Should contain the aisync self-reference.
	found := false
	for _, dl := range fix.DocLinks {
		if dl.Label == "aisync inspect documentation" {
			found = true
			break
		}
	}
	if !found {
		t.Error("doc links missing aisync self-reference")
	}
}
