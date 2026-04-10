package diagnostic

import (
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
)

func TestBuildReport_identity(t *testing.T) {
	now := time.Now()
	sess := &session.Session{
		ID:       "ses_test",
		Provider: session.ProviderOpenCode,
		Agent:    "opencode",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "hi", Timestamp: now},
			{ID: "m2", Role: session.RoleAssistant, Content: "hello", Timestamp: now, Model: "claude-opus-4-20250514"},
			{ID: "m3", Role: session.RoleUser, Content: "fix", Timestamp: now},
			{ID: "m4", Role: session.RoleAssistant, Content: "done", Timestamp: now, Model: "claude-opus-4-20250514"},
		},
	}

	r := BuildReport(sess, nil)

	if r.SessionID != "ses_test" {
		t.Errorf("SessionID = %q, want ses_test", r.SessionID)
	}
	if r.Provider != "opencode" {
		t.Errorf("Provider = %q, want opencode", r.Provider)
	}
	if r.Messages != 4 {
		t.Errorf("Messages = %d, want 4", r.Messages)
	}
	if r.UserMsgs != 2 {
		t.Errorf("UserMsgs = %d, want 2", r.UserMsgs)
	}
	if r.AsstMsgs != 2 {
		t.Errorf("AsstMsgs = %d, want 2", r.AsstMsgs)
	}
}

func TestBuildReport_allSectionsPopulated(t *testing.T) {
	sess := minimalSession()
	r := BuildReport(sess, nil)

	if r.Tokens == nil {
		t.Error("Tokens section is nil")
	}
	if r.Images == nil {
		t.Error("Images section is nil")
	}
	if r.Compaction == nil {
		t.Error("Compaction section is nil")
	}
	if r.Commands == nil {
		t.Error("Commands section is nil")
	}
	if r.ToolErrors == nil {
		t.Error("ToolErrors section is nil")
	}
	if r.Patterns == nil {
		t.Error("Patterns section is nil")
	}
	// Problems may be nil when no problems detected (append to nil returns nil)
	// This is fine — JSON marshals nil slice as null, which is acceptable.
}

// --- Token Section ---

func TestBuildTokenSection_basic(t *testing.T) {
	sess := &session.Session{
		TokenUsage: session.TokenUsage{
			InputTokens:  10000,
			OutputTokens: 500,
			TotalTokens:  10500,
			CacheRead:    8000,
			CacheWrite:   1000,
			ImageTokens:  2000,
		},
		EstimatedCost: 5.25,
		Messages: []session.Message{
			{Model: "claude-opus-4-20250514", InputTokens: 7000, OutputTokens: 300},
			{Model: "claude-sonnet-4-20250514", InputTokens: 3000, OutputTokens: 200},
		},
	}

	t_ := buildTokenSection(sess)

	if t_.Input != 10000 {
		t.Errorf("Input = %d, want 10000", t_.Input)
	}
	if t_.Output != 500 {
		t.Errorf("Output = %d, want 500", t_.Output)
	}
	if t_.Image != 2000 {
		t.Errorf("Image = %d, want 2000", t_.Image)
	}
	if t_.CacheRead != 8000 {
		t.Errorf("CacheRead = %d, want 8000", t_.CacheRead)
	}
	if t_.EstCost != 5.25 {
		t.Errorf("EstCost = %.2f, want 5.25", t_.EstCost)
	}

	// Cache percent: 8000/10000 = 80%
	if t_.CachePct < 79.9 || t_.CachePct > 80.1 {
		t.Errorf("CachePct = %.1f, want ~80.0", t_.CachePct)
	}

	// I/O ratio: 10000/500 = 20
	if t_.InputOutputRatio < 19.9 || t_.InputOutputRatio > 20.1 {
		t.Errorf("InputOutputRatio = %.1f, want ~20.0", t_.InputOutputRatio)
	}

	// Two models
	if len(t_.Models) != 2 {
		t.Fatalf("len(Models) = %d, want 2", len(t_.Models))
	}
	// Sorted by input desc: opus first
	if t_.Models[0].Input != 7000 {
		t.Errorf("Models[0].Input = %d, want 7000", t_.Models[0].Input)
	}
}

func TestBuildTokenSection_zeroOutput(t *testing.T) {
	sess := &session.Session{
		TokenUsage: session.TokenUsage{InputTokens: 100, OutputTokens: 0, TotalTokens: 100},
	}
	t_ := buildTokenSection(sess)
	if t_.InputOutputRatio != 0 {
		t.Errorf("InputOutputRatio should be 0 when Output=0, got %.1f", t_.InputOutputRatio)
	}
}

// --- Image Section ---

func TestBuildImageSection_noImages(t *testing.T) {
	sess := &session.Session{
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "hello"},
			{Role: session.RoleAssistant, Content: "hi"},
		},
	}
	img := buildImageSection(sess)
	if img.ToolReadImages != 0 {
		t.Errorf("ToolReadImages = %d, want 0", img.ToolReadImages)
	}
	if img.InlineImages != 0 {
		t.Errorf("InlineImages = %d, want 0", img.InlineImages)
	}
	if img.SimctlCaptures != 0 {
		t.Errorf("SimctlCaptures = %d, want 0", img.SimctlCaptures)
	}
}

func TestBuildImageSection_withScreenshots(t *testing.T) {
	sess := &session.Session{
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "take screenshot"},
			{
				Role: session.RoleAssistant, Content: "capturing",
				ToolCalls: []session.ToolCall{
					{Name: "mcp_bash", Input: `{"command":"xcrun simctl io booted screenshot /tmp/s.png"}`, Output: "ok"},
					{Name: "mcp_bash", Input: `{"command":"sips -Z 500 /tmp/s.png --out /tmp/r.png"}`, Output: "ok"},
				},
			},
			{
				Role: session.RoleAssistant, Content: "reading",
				ToolCalls: []session.ToolCall{
					{Name: "mcp_read", Input: `{"filePath":"/tmp/r.png"}`, Output: "[image]"},
				},
			},
			{Role: session.RoleAssistant, Content: "looks good"},
			{Role: session.RoleAssistant, Content: "continuing"},
		},
	}

	img := buildImageSection(sess)

	if img.SimctlCaptures != 1 {
		t.Errorf("SimctlCaptures = %d, want 1", img.SimctlCaptures)
	}
	if img.SipsResizes != 1 {
		t.Errorf("SipsResizes = %d, want 1", img.SipsResizes)
	}
	if img.ToolReadImages != 1 {
		t.Errorf("ToolReadImages = %d, want 1", img.ToolReadImages)
	}
	// 1 image × 1500 est tokens × N assistant turns
	if img.TotalBilledTok == 0 {
		t.Error("TotalBilledTok should be > 0")
	}
	if img.EstImageCost == 0 {
		t.Error("EstImageCost should be > 0")
	}
}

func TestBuildImageSection_inlineImages(t *testing.T) {
	sess := &session.Session{
		Messages: []session.Message{
			{
				Role: session.RoleUser, Content: "look at this",
				Images: []session.ImageMeta{
					{TokensEstimate: 500},
					{TokensEstimate: 800},
				},
			},
		},
	}

	img := buildImageSection(sess)
	if img.InlineImages != 2 {
		t.Errorf("InlineImages = %d, want 2", img.InlineImages)
	}
	if img.InlineTokens != 1300 {
		t.Errorf("InlineTokens = %d, want 1300", img.InlineTokens)
	}
}

// --- Command Section ---

func TestBuildCommandSection_fromToolCalls(t *testing.T) {
	sess := &session.Session{
		Messages: []session.Message{
			{
				Role: session.RoleAssistant, Content: "running",
				ToolCalls: []session.ToolCall{
					{Name: "bash", Input: `{"command":"go test ./..."}`, Output: "PASS ok pkg 0.5s"},
					{Name: "bash", Input: `{"command":"go test ./..."}`, Output: "PASS ok pkg 0.5s"},
					{Name: "bash", Input: `{"command":"git status"}`, Output: "On branch main"},
					{Name: "mcp_read", Input: `{"filePath":"foo.go"}`, Output: "package main"}, // not a command
				},
			},
		},
	}

	cmd := buildCommandSection(sess, nil)

	if cmd.TotalCommands != 3 {
		t.Errorf("TotalCommands = %d, want 3", cmd.TotalCommands)
	}
	if cmd.UniqueCommands != 2 {
		t.Errorf("UniqueCommands = %d, want 2", cmd.UniqueCommands)
	}
	if cmd.TotalOutputBytes == 0 {
		t.Error("TotalOutputBytes should be > 0")
	}
}

func TestBuildCommandSection_fromEvents(t *testing.T) {
	events := []sessionevent.Event{
		{Type: sessionevent.EventCommand, Command: &sessionevent.CommandDetail{
			BaseCommand: "go", FullCommand: "go test ./...", OutputBytes: 5000, OutputTokens: 1250,
		}},
		{Type: sessionevent.EventCommand, Command: &sessionevent.CommandDetail{
			BaseCommand: "go", FullCommand: "go build ./...", OutputBytes: 200, OutputTokens: 50,
		}},
		{Type: sessionevent.EventCommand, Command: &sessionevent.CommandDetail{
			BaseCommand: "git", FullCommand: "git status", OutputBytes: 100, OutputTokens: 25,
		}},
	}

	sess := &session.Session{}
	cmd := buildCommandSection(sess, events)

	if cmd.TotalCommands != 3 {
		t.Errorf("TotalCommands = %d, want 3", cmd.TotalCommands)
	}
	if cmd.TotalOutputBytes != 5300 {
		t.Errorf("TotalOutputBytes = %d, want 5300", cmd.TotalOutputBytes)
	}
	if cmd.TotalOutputTok != 1325 {
		t.Errorf("TotalOutputTok = %d, want 1325", cmd.TotalOutputTok)
	}
	// go command should be first (most output)
	if len(cmd.TopByOutput) > 0 && cmd.TopByOutput[0].Command != "go" {
		t.Errorf("TopByOutput[0].Command = %q, want 'go'", cmd.TopByOutput[0].Command)
	}
}

// --- Tool Error Section ---

func TestBuildToolErrorSection_noErrors(t *testing.T) {
	sess := &session.Session{
		Messages: []session.Message{
			{
				Role: session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{Name: "mcp_read", Output: "ok"},
					{Name: "mcp_edit", Output: "ok"},
				},
			},
		},
	}

	te := buildToolErrorSection(sess)
	if te.TotalToolCalls != 2 {
		t.Errorf("TotalToolCalls = %d, want 2", te.TotalToolCalls)
	}
	if te.ErrorCount != 0 {
		t.Errorf("ErrorCount = %d, want 0", te.ErrorCount)
	}
	if te.ErrorRate != 0 {
		t.Errorf("ErrorRate = %f, want 0", te.ErrorRate)
	}
	if len(te.ErrorLoops) != 0 {
		t.Errorf("ErrorLoops len = %d, want 0", len(te.ErrorLoops))
	}
}

func TestBuildToolErrorSection_withErrorLoop(t *testing.T) {
	msgs := []session.Message{
		{Role: session.RoleUser, Content: "fix"},
	}
	// 5 consecutive errors on same tool
	for i := 0; i < 5; i++ {
		msgs = append(msgs, session.Message{
			Role: session.RoleAssistant, InputTokens: 100 * (i + 1),
			ToolCalls: []session.ToolCall{
				{Name: "mcp_edit", State: session.ToolStateError, Output: "error"},
			},
		})
	}
	// Then a success
	msgs = append(msgs, session.Message{
		Role: session.RoleAssistant,
		ToolCalls: []session.ToolCall{
			{Name: "mcp_edit", Output: "ok"},
		},
	})

	sess := &session.Session{Messages: msgs}
	te := buildToolErrorSection(sess)

	if te.ErrorCount != 5 {
		t.Errorf("ErrorCount = %d, want 5", te.ErrorCount)
	}
	if te.TotalToolCalls != 6 {
		t.Errorf("TotalToolCalls = %d, want 6", te.TotalToolCalls)
	}
	if len(te.ErrorLoops) != 1 {
		t.Fatalf("ErrorLoops len = %d, want 1", len(te.ErrorLoops))
	}
	if te.ErrorLoops[0].ErrorCount != 5 {
		t.Errorf("ErrorLoops[0].ErrorCount = %d, want 5", te.ErrorLoops[0].ErrorCount)
	}
	if te.ConsecutiveMax != 5 {
		t.Errorf("ConsecutiveMax = %d, want 5", te.ConsecutiveMax)
	}
}

func TestBuildToolErrorSection_multipleLoops(t *testing.T) {
	msgs := []session.Message{{Role: session.RoleUser, Content: "fix"}}
	// Loop 1: 3 errors on mcp_edit
	for i := 0; i < 3; i++ {
		msgs = append(msgs, session.Message{
			Role:      session.RoleAssistant,
			ToolCalls: []session.ToolCall{{Name: "mcp_edit", State: session.ToolStateError, Output: "err"}},
		})
	}
	// Success breaks it
	msgs = append(msgs, session.Message{
		Role:      session.RoleAssistant,
		ToolCalls: []session.ToolCall{{Name: "mcp_edit", Output: "ok"}},
	})
	// Loop 2: 4 errors on mcp_bash
	for i := 0; i < 4; i++ {
		msgs = append(msgs, session.Message{
			Role:      session.RoleAssistant,
			ToolCalls: []session.ToolCall{{Name: "mcp_bash", State: session.ToolStateError, Output: "err"}},
		})
	}

	sess := &session.Session{Messages: msgs}
	te := buildToolErrorSection(sess)

	if len(te.ErrorLoops) != 2 {
		t.Errorf("ErrorLoops len = %d, want 2", len(te.ErrorLoops))
	}
	if te.ConsecutiveMax != 4 {
		t.Errorf("ConsecutiveMax = %d, want 4", te.ConsecutiveMax)
	}
}

// --- Pattern Section ---

func TestBuildPatternSection_longRun(t *testing.T) {
	msgs := []session.Message{{Role: session.RoleUser, Content: "go"}}
	for i := 0; i < 15; i++ {
		msgs = append(msgs, session.Message{Role: session.RoleAssistant, Content: "step"})
	}

	sess := &session.Session{Messages: msgs}
	p := buildPatternSection(sess)

	if p.LongestRunLength != 15 {
		t.Errorf("LongestRunLength = %d, want 15", p.LongestRunLength)
	}
	if p.LongRunCount != 1 {
		t.Errorf("LongRunCount = %d, want 1", p.LongRunCount)
	}
}

func TestBuildPatternSection_userCorrections(t *testing.T) {
	msgs := []session.Message{
		{Role: session.RoleUser, Content: "fix A"},
		{Role: session.RoleUser, Content: "actually fix B"},
		{Role: session.RoleUser, Content: "no wait, fix C"},
		{Role: session.RoleAssistant, Content: "ok"},
	}

	sess := &session.Session{Messages: msgs}
	p := buildPatternSection(sess)

	// Two consecutive user→user transitions
	if p.UserCorrectionCount != 2 {
		t.Errorf("UserCorrectionCount = %d, want 2", p.UserCorrectionCount)
	}
}

func TestBuildPatternSection_globStorm(t *testing.T) {
	msgs := []session.Message{{Role: session.RoleUser, Content: "find the file"}}
	// 7 consecutive glob calls
	for i := 0; i < 7; i++ {
		msgs = append(msgs, session.Message{
			Role: session.RoleAssistant,
			ToolCalls: []session.ToolCall{
				{Name: "mcp_glob", Input: "*.go", Output: "foo.go"},
			},
		})
	}

	sess := &session.Session{Messages: msgs}
	p := buildPatternSection(sess)

	if p.GlobStormCount != 1 {
		t.Errorf("GlobStormCount = %d, want 1", p.GlobStormCount)
	}
}

func TestBuildPatternSection_writeWithoutRead(t *testing.T) {
	sess := &session.Session{
		Messages: []session.Message{
			{
				Role: session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					// Edit without prior read
					{Name: "mcp_edit", Input: `{"filePath":"/tmp/foo.go"}`, Output: "ok"},
				},
			},
			{
				Role: session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					// Read then edit — should not count
					{Name: "mcp_read", Input: `{"filePath":"/tmp/bar.go"}`, Output: "package main"},
					{Name: "mcp_edit", Input: `{"filePath":"/tmp/bar.go"}`, Output: "ok"},
				},
			},
		},
	}

	p := buildPatternSection(sess)
	if p.WriteWithoutReadCount != 1 {
		t.Errorf("WriteWithoutReadCount = %d, want 1", p.WriteWithoutReadCount)
	}
}

// --- Problem sorting ---

func TestBuildReport_problemsSortedBySeverity(t *testing.T) {
	// Craft a session that triggers multiple problems at different severities.
	// We'll create a large session with tool error loops + long runs.
	msgs := []session.Message{{Role: session.RoleUser, Content: "fix"}}
	// 8 consecutive errors → high severity tool-error-loops
	for i := 0; i < 8; i++ {
		msgs = append(msgs, session.Message{
			Role: session.RoleAssistant, InputTokens: 200,
			ToolCalls: []session.ToolCall{
				{Name: "mcp_edit", State: session.ToolStateError, Output: "err"},
			},
		})
	}

	sess := &session.Session{
		ID: "ses_sorted", Provider: session.ProviderClaudeCode, Agent: "claude",
		Messages:   msgs,
		TokenUsage: session.TokenUsage{InputTokens: 1600, OutputTokens: 50, TotalTokens: 1650},
	}

	r := BuildReport(sess, nil)

	// Verify sorted: high before medium before low
	for i := 1; i < len(r.Problems); i++ {
		prevRank := severityRank(r.Problems[i-1].Severity)
		currRank := severityRank(r.Problems[i].Severity)
		if currRank < prevRank {
			t.Errorf("problems not sorted by severity: %s(%s) before %s(%s)",
				r.Problems[i-1].ID, r.Problems[i-1].Severity,
				r.Problems[i].ID, r.Problems[i].Severity)
		}
	}
}

// --- Helpers ---

func minimalSession() *session.Session {
	now := time.Now()
	return &session.Session{
		ID:       "ses_min",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "hi", Timestamp: now},
			{ID: "m2", Role: session.RoleAssistant, Content: "hello", Timestamp: now, Model: "claude-sonnet-4-20250514", InputTokens: 50, OutputTokens: 20},
		},
		TokenUsage: session.TokenUsage{InputTokens: 50, OutputTokens: 20, TotalTokens: 70},
	}
}
