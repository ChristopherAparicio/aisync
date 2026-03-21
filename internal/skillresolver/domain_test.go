package skillresolver

import "testing"

func TestVerdictValid(t *testing.T) {
	tests := []struct {
		v    Verdict
		want bool
	}{
		{VerdictFixed, true},
		{VerdictPartial, true},
		{VerdictNoChange, true},
		{VerdictPending, true},
		{Verdict("unknown"), false},
		{Verdict(""), false},
	}
	for _, tt := range tests {
		if got := tt.v.Valid(); got != tt.want {
			t.Errorf("Verdict(%q).Valid() = %v, want %v", tt.v, got, tt.want)
		}
	}
}

func TestVerdictString(t *testing.T) {
	if got := VerdictFixed.String(); got != "fixed" {
		t.Errorf("VerdictFixed.String() = %q, want %q", got, "fixed")
	}
}

func TestImprovementKindValid(t *testing.T) {
	tests := []struct {
		k    ImprovementKind
		want bool
	}{
		{KindDescription, true},
		{KindKeywords, true},
		{KindTriggerPattern, true},
		{KindContent, true},
		{ImprovementKind("nope"), false},
		{ImprovementKind(""), false},
	}
	for _, tt := range tests {
		if got := tt.k.Valid(); got != tt.want {
			t.Errorf("ImprovementKind(%q).Valid() = %v, want %v", tt.k, got, tt.want)
		}
	}
}

func TestImprovementKindString(t *testing.T) {
	if got := KindKeywords.String(); got != "keywords" {
		t.Errorf("KindKeywords.String() = %q, want %q", got, "keywords")
	}
}

func TestResolveResultZeroValue(t *testing.T) {
	var r ResolveResult
	if r.Validated {
		t.Error("zero-value ResolveResult should not be validated")
	}
	if r.Applied != 0 {
		t.Errorf("zero-value Applied = %d, want 0", r.Applied)
	}
	if len(r.Improvements) != 0 {
		t.Error("zero-value Improvements should be empty")
	}
}

func TestSkillImprovementFields(t *testing.T) {
	imp := SkillImprovement{
		SkillName:           "replay-tester",
		SkillPath:           "/home/user/.config/opencode/skills/replay-tester/SKILL.md",
		Kind:                KindKeywords,
		CurrentDescription:  "Test evolutions by replaying sessions",
		ProposedDescription: "",
		AddKeywords:         []string{"replay", "session", "test", "compare"},
		Reasoning:           "User asked to 'replay the session' but skill was not triggered",
		Confidence:          0.85,
		SourceSessionID:     "sess_abc123",
	}

	if imp.SkillName != "replay-tester" {
		t.Errorf("SkillName = %q, want %q", imp.SkillName, "replay-tester")
	}
	if !imp.Kind.Valid() {
		t.Errorf("Kind %q should be valid", imp.Kind)
	}
	if len(imp.AddKeywords) != 4 {
		t.Errorf("AddKeywords len = %d, want 4", len(imp.AddKeywords))
	}
	if imp.Confidence < 0 || imp.Confidence > 1 {
		t.Errorf("Confidence = %f, should be 0.0-1.0", imp.Confidence)
	}
}

func TestAnalyzeInputFields(t *testing.T) {
	input := AnalyzeInput{
		SkillName:          "replay-tester",
		SkillPath:          "/path/to/SKILL.md",
		CurrentContent:     "# Replay Tester\nTest sessions...",
		CurrentDescription: "Test sessions by replaying",
		CurrentKeywords:    []string{"replay", "test"},
		UserMessages:       []string{"Can you replay the last session?", "Compare the results"},
		SessionSummary:     "User wanted to replay a session",
		SessionID:          "sess_xyz",
	}

	if input.SkillName != "replay-tester" {
		t.Errorf("SkillName = %q", input.SkillName)
	}
	if len(input.UserMessages) != 2 {
		t.Errorf("UserMessages len = %d, want 2", len(input.UserMessages))
	}
	if len(input.CurrentKeywords) != 2 {
		t.Errorf("CurrentKeywords len = %d, want 2", len(input.CurrentKeywords))
	}
}

func TestResolveRequest(t *testing.T) {
	req := ResolveRequest{
		SessionID:  "sess_123",
		SkillNames: []string{"replay-tester"},
		DryRun:     true,
	}
	if req.SessionID != "sess_123" {
		t.Errorf("SessionID = %q", req.SessionID)
	}
	if !req.DryRun {
		t.Error("DryRun should be true")
	}
	if len(req.SkillNames) != 1 {
		t.Errorf("SkillNames len = %d, want 1", len(req.SkillNames))
	}
}
