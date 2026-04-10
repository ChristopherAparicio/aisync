package diagnostic

import (
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── ProviderPathsFor tests ──────────────────────────────────────────────────

func TestProviderPathsFor_opencode(t *testing.T) {
	p := ProviderPathsFor(session.ProviderOpenCode)
	if p.InstructionsFile != "AGENTS.md" {
		t.Errorf("expected AGENTS.md, got %q", p.InstructionsFile)
	}
	if p.SkillsDir == "" {
		t.Error("expected non-empty skills dir for opencode")
	}
	if p.CommandsDir != "" {
		t.Error("expected empty commands dir for opencode")
	}
}

func TestProviderPathsFor_claudeCode(t *testing.T) {
	p := ProviderPathsFor(session.ProviderClaudeCode)
	if p.InstructionsFile != "CLAUDE.md" {
		t.Errorf("expected CLAUDE.md, got %q", p.InstructionsFile)
	}
	if p.CommandsDir == "" {
		t.Error("expected non-empty commands dir for claude-code")
	}
	if p.SkillsDir != "" {
		t.Error("expected empty skills dir for claude-code")
	}
}

func TestProviderPathsFor_cursor(t *testing.T) {
	p := ProviderPathsFor(session.ProviderCursor)
	if p.InstructionsFile != ".cursorrules" {
		t.Errorf("expected .cursorrules, got %q", p.InstructionsFile)
	}
}

func TestProviderPathsFor_unknown(t *testing.T) {
	p := ProviderPathsFor(session.ProviderName("unknown"))
	if p.InstructionsFile != "AGENTS.md" {
		t.Errorf("expected fallback AGENTS.md, got %q", p.InstructionsFile)
	}
}

// ── GenerateFixes tests ─────────────────────────────────────────────────────

func TestGenerateFixes_noProblems(t *testing.T) {
	report := &InspectReport{SessionID: "ses_test", Provider: "opencode"}
	fs := GenerateFixes(report, session.ProviderOpenCode)
	if len(fs.Fixes) != 0 {
		t.Errorf("expected 0 fixes, got %d", len(fs.Fixes))
	}
}

func TestGenerateFixes_expensiveScreenshots_opencode(t *testing.T) {
	report := &InspectReport{
		SessionID: "ses_test",
		Provider:  "opencode",
		Images: &ImageSection{
			ToolReadImages: 100,
			SimctlCaptures: 100,
			SipsResizes:    100,
			AvgTurnsInCtx:  50,
			TotalBilledTok: 27_000_000,
			EstImageCost:   81.0,
		},
		Problems: []Problem{{
			ID:       ProblemExpensiveScreenshots,
			Severity: SeverityHigh,
			Category: CategoryImages,
			Title:    "Expensive screenshots in context",
		}},
	}

	fs := GenerateFixes(report, session.ProviderOpenCode)
	if len(fs.Fixes) != 1 {
		t.Fatalf("expected 1 fix, got %d", len(fs.Fixes))
	}

	fix := fs.Fixes[0]
	if fix.ProblemID != ProblemExpensiveScreenshots {
		t.Errorf("expected problem ID expensive-screenshots, got %s", fix.ProblemID)
	}

	// Should produce: capture script, agent instructions, opencode skill
	if len(fix.Artefacts) < 3 {
		t.Errorf("expected at least 3 artefacts for opencode, got %d", len(fix.Artefacts))
	}

	// Check artefact types
	kinds := map[ArtefactKind]bool{}
	for _, a := range fix.Artefacts {
		kinds[a.Kind] = true
	}
	if !kinds[ArtefactScript] {
		t.Error("missing capture script artefact")
	}
	if !kinds[ArtefactAgentInstructions] {
		t.Error("missing agent instructions artefact")
	}
	if !kinds[ArtefactSkill] {
		t.Error("missing opencode skill artefact")
	}

	// Check capture script content
	for _, a := range fix.Artefacts {
		if a.Kind == ArtefactScript {
			if !strings.Contains(a.Content, "sips -Z 500") {
				t.Error("capture script should resize to 500px")
			}
			if !strings.Contains(a.Content, "format jpeg") {
				t.Error("capture script should convert to JPEG")
			}
			if !strings.Contains(a.RelPath, "capture-screen.sh") {
				t.Errorf("expected capture-screen.sh in path, got %q", a.RelPath)
			}
		}
	}
}

func TestGenerateFixes_expensiveScreenshots_claudeCode(t *testing.T) {
	report := &InspectReport{
		SessionID: "ses_test",
		Provider:  "claude-code",
		Images: &ImageSection{
			ToolReadImages: 50,
			SimctlCaptures: 50,
			SipsResizes:    50,
			AvgTurnsInCtx:  40,
			TotalBilledTok: 10_000_000,
			EstImageCost:   30.0,
		},
		Problems: []Problem{{
			ID:       ProblemExpensiveScreenshots,
			Severity: SeverityHigh,
			Category: CategoryImages,
		}},
	}

	fs := GenerateFixes(report, session.ProviderClaudeCode)
	if len(fs.Fixes) != 1 {
		t.Fatalf("expected 1 fix, got %d", len(fs.Fixes))
	}

	fix := fs.Fixes[0]
	kinds := map[ArtefactKind]bool{}
	for _, a := range fix.Artefacts {
		kinds[a.Kind] = true
		// Instructions should target CLAUDE.md
		if a.Kind == ArtefactAgentInstructions && !strings.Contains(a.RelPath, "CLAUDE.md") {
			t.Errorf("claude-code instructions should target CLAUDE.md, got %q", a.RelPath)
		}
		// Should produce a claude command, not a skill
		if a.Kind == ArtefactCommand && !strings.Contains(a.RelPath, ".claude/commands/") {
			t.Errorf("expected .claude/commands/ path, got %q", a.RelPath)
		}
	}
	if !kinds[ArtefactCommand] {
		t.Error("missing claude-code command artefact")
	}
	if kinds[ArtefactSkill] {
		t.Error("claude-code should not produce skill artefacts")
	}
}

func TestGenerateFixes_oversizedScreenshots(t *testing.T) {
	report := &InspectReport{
		SessionID: "ses_test",
		Provider:  "opencode",
		Images: &ImageSection{
			ToolReadImages: 50,
			SimctlCaptures: 50,
			SipsResizes:    50,
			AvgTurnsInCtx:  40,
			TotalBilledTok: 10_000_000,
			EstImageCost:   30.0,
		},
		Problems: []Problem{{
			ID:       ProblemOversizedScreenshots,
			Severity: SeverityMedium,
			Category: CategoryImages,
		}},
	}

	fs := GenerateFixes(report, session.ProviderOpenCode)
	if len(fs.Fixes) != 1 {
		t.Fatalf("expected 1 fix, got %d", len(fs.Fixes))
	}

	// Should include capture script and instructions about resolution
	for _, a := range fs.Fixes[0].Artefacts {
		if a.Kind == ArtefactAgentInstructions {
			if !strings.Contains(a.Content, "500px") {
				t.Error("instructions should mention 500px target")
			}
		}
	}
}

func TestGenerateFixes_unresizedScreenshots(t *testing.T) {
	report := &InspectReport{
		SessionID: "ses_test",
		Provider:  "opencode",
		Images: &ImageSection{
			ToolReadImages: 30,
			SimctlCaptures: 30,
			SipsResizes:    5, // most not resized
			AvgTurnsInCtx:  30,
			TotalBilledTok: 5_000_000,
			EstImageCost:   15.0,
		},
		Problems: []Problem{{
			ID:       ProblemUnresizedScreenshots,
			Severity: SeverityMedium,
			Category: CategoryImages,
		}},
	}

	fs := GenerateFixes(report, session.ProviderOpenCode)
	if len(fs.Fixes) != 1 {
		t.Fatalf("expected 1 fix, got %d", len(fs.Fixes))
	}

	for _, a := range fs.Fixes[0].Artefacts {
		if a.Kind == ArtefactAgentInstructions {
			if !strings.Contains(a.Content, "without resize") {
				t.Error("instructions should mention unresized screenshots")
			}
		}
	}
}

func TestGenerateFixes_frequentCompaction(t *testing.T) {
	report := &InspectReport{
		SessionID: "ses_test",
		Provider:  "opencode",
		Compaction: &CompactionSection{
			Count:           20,
			PerUserMsg:      0.3,
			TotalTokensLost: 500000,
		},
		Problems: []Problem{{
			ID:       ProblemFrequentCompaction,
			Severity: SeverityHigh,
			Category: CategoryCompaction,
		}},
	}

	fs := GenerateFixes(report, session.ProviderOpenCode)
	if len(fs.Fixes) != 1 {
		t.Fatalf("expected 1 fix, got %d", len(fs.Fixes))
	}

	a := fs.Fixes[0].Artefacts[0]
	if a.Kind != ArtefactAgentInstructions {
		t.Errorf("expected agent instructions, got %s", a.Kind)
	}
	if !a.AppendTo {
		t.Error("compaction fix should append to instructions file")
	}
	if !strings.Contains(a.Content, "Context Management") {
		t.Error("instructions should mention context management")
	}
}

func TestGenerateFixes_contextNearLimit(t *testing.T) {
	report := &InspectReport{
		SessionID: "ses_test",
		Provider:  "opencode",
		Compaction: &CompactionSection{
			Count:           10,
			AvgBeforeTokens: 175000,
		},
		Problems: []Problem{{
			ID:       ProblemContextNearLimit,
			Severity: SeverityHigh,
			Category: CategoryCompaction,
		}},
	}

	fs := GenerateFixes(report, session.ProviderOpenCode)
	if len(fs.Fixes) != 1 {
		t.Fatalf("expected 1 fix, got %d", len(fs.Fixes))
	}
	if !strings.Contains(fs.Fixes[0].Artefacts[0].Content, "Context Window") {
		t.Error("fix should mention context window")
	}
}

func TestGenerateFixes_contextThrashing(t *testing.T) {
	report := &InspectReport{
		SessionID: "ses_test",
		Provider:  "opencode",
		Compaction: &CompactionSection{
			Count:          15,
			IntervalMedian: 10,
		},
		Problems: []Problem{{
			ID:       ProblemContextThrashing,
			Severity: SeverityHigh,
			Category: CategoryTokens,
		}},
	}

	fs := GenerateFixes(report, session.ProviderOpenCode)
	if len(fs.Fixes) != 1 {
		t.Fatalf("expected 1 fix, got %d", len(fs.Fixes))
	}
	if !strings.Contains(fs.Fixes[0].Artefacts[0].Content, "Thrashing") {
		t.Error("fix should mention thrashing")
	}
}

func TestGenerateFixes_verboseCommands(t *testing.T) {
	report := &InspectReport{
		SessionID: "ses_test",
		Provider:  "opencode",
		Commands: &CommandSection{
			TotalCommands:    100,
			TotalOutputBytes: 2_000_000,
			TotalOutputTok:   500_000,
			TopByOutput: []CommandEntry{
				{Command: "pnpm test", TotalBytes: 800_000, Invocations: 20, EstTokens: 200_000},
				{Command: "rtk go test", TotalBytes: 500_000, Invocations: 15, EstTokens: 125_000},
			},
		},
		Problems: []Problem{{
			ID:       ProblemVerboseCommands,
			Severity: SeverityHigh,
			Category: CategoryCommands,
		}},
	}

	fs := GenerateFixes(report, session.ProviderOpenCode)
	if len(fs.Fixes) != 1 {
		t.Fatalf("expected 1 fix, got %d", len(fs.Fixes))
	}

	a := fs.Fixes[0].Artefacts[0]
	if !strings.Contains(a.Content, "pnpm test") {
		t.Error("fix should mention top verbose commands")
	}
	if !strings.Contains(a.Content, "Command Output Limits") {
		t.Error("fix should have Command Output Limits header")
	}
}

func TestGenerateFixes_repeatedCommands(t *testing.T) {
	report := &InspectReport{
		SessionID: "ses_test",
		Provider:  "opencode",
		Commands: &CommandSection{
			TotalCommands:  100,
			UniqueCommands: 40,
			RepeatedRatio:  0.6,
		},
		Problems: []Problem{{
			ID:       ProblemRepeatedCommands,
			Severity: SeverityMedium,
			Category: CategoryCommands,
		}},
	}

	fs := GenerateFixes(report, session.ProviderOpenCode)
	if len(fs.Fixes) != 1 {
		t.Fatalf("expected 1 fix, got %d", len(fs.Fixes))
	}
	if !strings.Contains(fs.Fixes[0].Artefacts[0].Content, "Repetition") {
		t.Error("fix should mention command repetition")
	}
}

func TestGenerateFixes_toolErrorLoops(t *testing.T) {
	report := &InspectReport{
		SessionID: "ses_test",
		Provider:  "opencode",
		ToolErrors: &ToolErrorSection{
			TotalToolCalls: 200,
			ErrorCount:     40,
			ErrorRate:      0.2,
			ConsecutiveMax: 8,
			ErrorLoops: []ErrorLoop{
				{ToolName: "bash", ErrorCount: 5, StartMsgIdx: 100, EndMsgIdx: 110, TotalTokens: 50000},
				{ToolName: "edit", ErrorCount: 4, StartMsgIdx: 200, EndMsgIdx: 208, TotalTokens: 30000},
			},
		},
		Problems: []Problem{{
			ID:       ProblemToolErrorLoops,
			Severity: SeverityHigh,
			Category: CategoryToolErrors,
		}},
	}

	fs := GenerateFixes(report, session.ProviderOpenCode)
	if len(fs.Fixes) != 1 {
		t.Fatalf("expected 1 fix, got %d", len(fs.Fixes))
	}

	a := fs.Fixes[0].Artefacts[0]
	if !strings.Contains(a.Content, "bash") {
		t.Error("fix should mention specific error loop tools")
	}
	if !strings.Contains(a.Content, "edit") {
		t.Error("fix should mention edit tool error loop")
	}
	if !strings.Contains(a.Content, "Tool Error Handling") {
		t.Error("fix should have Tool Error Handling header")
	}
}

func TestGenerateFixes_highInputRatio(t *testing.T) {
	report := &InspectReport{
		SessionID: "ses_test",
		Provider:  "opencode",
		Tokens: &TokenSection{
			Input:            500_000_000,
			Output:           2_000_000,
			InputOutputRatio: 250,
		},
		Problems: []Problem{{
			ID:       ProblemHighInputRatio,
			Severity: SeverityHigh,
			Category: CategoryTokens,
		}},
	}

	fs := GenerateFixes(report, session.ProviderOpenCode)
	if len(fs.Fixes) != 1 {
		t.Fatalf("expected 1 fix, got %d", len(fs.Fixes))
	}
	if !strings.Contains(fs.Fixes[0].Artefacts[0].Content, "Token Economy") {
		t.Error("fix should mention Token Economy")
	}
}

func TestGenerateFixes_yoloEditing(t *testing.T) {
	report := &InspectReport{
		SessionID: "ses_test",
		Provider:  "opencode",
		Patterns: &PatternSection{
			WriteWithoutReadCount: 25,
		},
		Problems: []Problem{{
			ID:       ProblemYoloEditing,
			Severity: SeverityMedium,
			Category: CategoryPatterns,
		}},
	}

	fs := GenerateFixes(report, session.ProviderOpenCode)
	if len(fs.Fixes) != 1 {
		t.Fatalf("expected 1 fix, got %d", len(fs.Fixes))
	}
	if !strings.Contains(fs.Fixes[0].Artefacts[0].Content, "File Editing Protocol") {
		t.Error("fix should mention File Editing Protocol")
	}
	if !strings.Contains(fs.Fixes[0].Artefacts[0].Content, "25 files") {
		t.Error("fix should include the actual count")
	}
}

func TestGenerateFixes_conversationDrift(t *testing.T) {
	report := &InspectReport{
		SessionID: "ses_test",
		Provider:  "opencode",
		Patterns: &PatternSection{
			UserCorrectionCount: 12,
			LongestRunLength:    45,
		},
		Problems: []Problem{{
			ID:       ProblemConversationDrift,
			Severity: SeverityMedium,
			Category: CategoryPatterns,
		}},
	}

	fs := GenerateFixes(report, session.ProviderOpenCode)
	if len(fs.Fixes) != 1 {
		t.Fatalf("expected 1 fix, got %d", len(fs.Fixes))
	}
	if !strings.Contains(fs.Fixes[0].Artefacts[0].Content, "Conversation Focus") {
		t.Error("fix should mention Conversation Focus")
	}
}

// ── Multiple problems test ──────────────────────────────────────────────────

func TestGenerateFixes_multipleProblems(t *testing.T) {
	report := &InspectReport{
		SessionID: "ses_test",
		Provider:  "opencode",
		Images: &ImageSection{
			ToolReadImages: 100,
			SimctlCaptures: 100,
			SipsResizes:    100,
			AvgTurnsInCtx:  50,
			TotalBilledTok: 27_000_000,
			EstImageCost:   81.0,
		},
		Compaction: &CompactionSection{
			Count:           20,
			PerUserMsg:      0.3,
			TotalTokensLost: 500000,
		},
		Patterns: &PatternSection{
			WriteWithoutReadCount: 15,
		},
		Problems: []Problem{
			{ID: ProblemExpensiveScreenshots, Severity: SeverityHigh, Category: CategoryImages},
			{ID: ProblemFrequentCompaction, Severity: SeverityHigh, Category: CategoryCompaction},
			{ID: ProblemYoloEditing, Severity: SeverityMedium, Category: CategoryPatterns},
		},
	}

	fs := GenerateFixes(report, session.ProviderOpenCode)
	if len(fs.Fixes) != 3 {
		t.Errorf("expected 3 fixes, got %d", len(fs.Fixes))
	}

	// Verify each problem got a fix
	fixIDs := map[ProblemID]bool{}
	for _, f := range fs.Fixes {
		fixIDs[f.ProblemID] = true
	}
	if !fixIDs[ProblemExpensiveScreenshots] {
		t.Error("missing fix for expensive-screenshots")
	}
	if !fixIDs[ProblemFrequentCompaction] {
		t.Error("missing fix for frequent-compaction")
	}
	if !fixIDs[ProblemYoloEditing] {
		t.Error("missing fix for yolo-editing")
	}
}

// ── Problem without generator test ──────────────────────────────────────────

func TestGenerateFixes_problemWithoutGenerator(t *testing.T) {
	report := &InspectReport{
		SessionID: "ses_test",
		Provider:  "opencode",
		Problems: []Problem{
			{ID: ProblemLowCacheUtilization, Severity: SeverityLow, Category: CategoryTokens},
			{ID: ProblemCompactionAccelerating, Severity: SeverityMedium, Category: CategoryCompaction},
			{ID: ProblemLongRunningCommands, Severity: SeverityLow, Category: CategoryCommands},
			{ID: ProblemExcessiveGlobbing, Severity: SeverityLow, Category: CategoryPatterns},
			{ID: ProblemAbandonedToolCalls, Severity: SeverityMedium, Category: CategoryToolErrors},
		},
	}

	fs := GenerateFixes(report, session.ProviderOpenCode)
	// These problems don't have generators yet
	if len(fs.Fixes) != 0 {
		t.Errorf("expected 0 fixes for problems without generators, got %d", len(fs.Fixes))
	}
}

// ── FixSet metadata test ────────────────────────────────────────────────────

func TestGenerateFixes_metadata(t *testing.T) {
	report := &InspectReport{
		SessionID: "ses_abc123",
		Provider:  "opencode",
	}

	fs := GenerateFixes(report, session.ProviderOpenCode)
	if fs.SessionID != "ses_abc123" {
		t.Errorf("expected session ID ses_abc123, got %q", fs.SessionID)
	}
	if fs.Provider != "opencode" {
		t.Errorf("expected provider opencode, got %q", fs.Provider)
	}
	if fs.Applied {
		t.Error("expected Applied to be false by default")
	}
}

// ── AppendTo flag test ──────────────────────────────────────────────────────

func TestGenerateFixes_agentInstructionsAppendTo(t *testing.T) {
	report := &InspectReport{
		SessionID: "ses_test",
		Provider:  "opencode",
		Patterns: &PatternSection{
			WriteWithoutReadCount: 10,
		},
		Problems: []Problem{{
			ID:       ProblemYoloEditing,
			Severity: SeverityLow,
			Category: CategoryPatterns,
		}},
	}

	fs := GenerateFixes(report, session.ProviderOpenCode)
	if len(fs.Fixes) != 1 {
		t.Fatalf("expected 1 fix, got %d", len(fs.Fixes))
	}

	for _, a := range fs.Fixes[0].Artefacts {
		if a.Kind == ArtefactAgentInstructions && !a.AppendTo {
			t.Error("agent instructions artefacts should have AppendTo=true")
		}
	}
}

// ── Cursor provider test (no skills/commands) ───────────────────────────────

func TestGenerateFixes_cursorProvider_noSkillsOrCommands(t *testing.T) {
	report := &InspectReport{
		SessionID: "ses_test",
		Provider:  "cursor",
		Images: &ImageSection{
			ToolReadImages: 100,
			SimctlCaptures: 100,
			SipsResizes:    100,
			AvgTurnsInCtx:  50,
			TotalBilledTok: 27_000_000,
			EstImageCost:   81.0,
		},
		Problems: []Problem{{
			ID:       ProblemExpensiveScreenshots,
			Severity: SeverityHigh,
			Category: CategoryImages,
		}},
	}

	fs := GenerateFixes(report, session.ProviderCursor)
	if len(fs.Fixes) != 1 {
		t.Fatalf("expected 1 fix, got %d", len(fs.Fixes))
	}

	// Cursor should get script + instructions, but no skill/command
	for _, a := range fs.Fixes[0].Artefacts {
		if a.Kind == ArtefactSkill || a.Kind == ArtefactCommand {
			t.Errorf("cursor should not get %s artefacts", a.Kind)
		}
		if a.Kind == ArtefactAgentInstructions && !strings.Contains(a.RelPath, ".cursorrules") {
			t.Errorf("cursor instructions should target .cursorrules, got %q", a.RelPath)
		}
	}
}

// ── Nil section guard tests ─────────────────────────────────────────────────

func TestGenerateFixes_nilSections(t *testing.T) {
	// Problems reference sections that are nil — generators should handle gracefully
	report := &InspectReport{
		SessionID: "ses_test",
		Provider:  "opencode",
		Problems: []Problem{
			{ID: ProblemFrequentCompaction, Severity: SeverityHigh, Category: CategoryCompaction},
			{ID: ProblemVerboseCommands, Severity: SeverityHigh, Category: CategoryCommands},
			{ID: ProblemToolErrorLoops, Severity: SeverityHigh, Category: CategoryToolErrors},
			{ID: ProblemHighInputRatio, Severity: SeverityHigh, Category: CategoryTokens},
			{ID: ProblemYoloEditing, Severity: SeverityMedium, Category: CategoryPatterns},
			{ID: ProblemConversationDrift, Severity: SeverityMedium, Category: CategoryPatterns},
			{ID: ProblemContextNearLimit, Severity: SeverityMedium, Category: CategoryCompaction},
			{ID: ProblemContextThrashing, Severity: SeverityHigh, Category: CategoryTokens},
		},
	}

	// Should not panic
	fs := GenerateFixes(report, session.ProviderOpenCode)
	if len(fs.Fixes) != 0 {
		t.Errorf("expected 0 fixes with nil sections, got %d", len(fs.Fixes))
	}
}
