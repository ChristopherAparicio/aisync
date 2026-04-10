package inspectcmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/diagnostic"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"

	"github.com/ChristopherAparicio/aisync/git"
)

// inspectTestFactory builds a Factory wired to a MockStore.
func inspectTestFactory(t *testing.T, store *testutil.MockStore) (*cmdutil.Factory, *iostreams.IOStreams) {
	t.Helper()
	ios := iostreams.Test()
	repoDir := testutil.InitTestRepo(t)

	if store == nil {
		store = testutil.NewMockStore()
	}
	gitClient := git.NewClient(repoDir)

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		StoreFunc: func() (storage.Store, error) { return store, nil },
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store: store,
				Git:   gitClient,
			}), nil
		},
	}
	return f, ios
}

// --- Flag tests ---

func TestNewCmdInspect_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdInspect(f)

	flags := []string{"json", "section"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag to be registered", name)
		}
	}
}

func TestNewCmdInspect_requiresArg(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdInspect(f)
	cmd.SetArgs([]string{}) // no args
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing argument")
	}
}

// --- Service init error ---

func TestInspect_serviceInitError(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: ios,
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return nil, fmt.Errorf("database connection failed")
		},
	}

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "any",
	}

	err := runInspect(opts)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "database connection failed") {
		t.Errorf("expected 'database connection failed' in error, got: %v", err)
	}
}

// --- Session not found ---

func TestInspect_sessionNotFound(t *testing.T) {
	store := testutil.NewMockStore()
	f, ios := inspectTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "nonexistent",
	}

	err := runInspect(opts)
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

// --- Minimal session text output ---

func TestInspect_minimalSession_textOutput(t *testing.T) {
	now := time.Now()
	sess := &session.Session{
		ID:       "ses_minimal",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "hello", Timestamp: now},
			{ID: "m2", Role: session.RoleAssistant, Content: "hi", Timestamp: now, Model: "claude-sonnet-4-20250514", InputTokens: 100, OutputTokens: 50},
		},
		TokenUsage: session.TokenUsage{
			InputTokens: 100, OutputTokens: 50, TotalTokens: 150,
		},
		CreatedAt: now,
	}

	store := testutil.NewMockStore(sess)
	f, ios := inspectTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "ses_minimal",
	}

	err := runInspect(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()

	// Check header
	if !strings.Contains(output, "SESSION INSPECTION") {
		t.Error("expected 'SESSION INSPECTION' header")
	}
	if !strings.Contains(output, "ses_minimal") {
		t.Error("expected session ID in output")
	}
	if !strings.Contains(output, "claude-code") {
		t.Error("expected provider in output")
	}
	if !strings.Contains(output, "Messages: 2") {
		t.Error("expected message count")
	}

	// Check token section printed
	if !strings.Contains(output, "TOKENS") {
		t.Error("expected TOKENS section header")
	}
	if !strings.Contains(output, "100") {
		t.Error("expected input token count")
	}

	// Images section should show "No images detected"
	if !strings.Contains(output, "No images detected") {
		t.Error("expected 'No images detected' for session without images")
	}
}

// --- JSON output ---

func TestInspect_jsonOutput(t *testing.T) {
	now := time.Now()
	sess := &session.Session{
		ID:       "ses_json",
		Provider: session.ProviderOpenCode,
		Agent:    "opencode",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "test", Timestamp: now},
			{ID: "m2", Role: session.RoleAssistant, Content: "ok", Timestamp: now, Model: "claude-opus-4-20250514", InputTokens: 500, OutputTokens: 100},
		},
		TokenUsage: session.TokenUsage{
			InputTokens: 500, OutputTokens: 100, TotalTokens: 600,
			CacheRead: 300,
		},
		EstimatedCost: 1.50,
		CreatedAt:     now,
	}

	store := testutil.NewMockStore(sess)
	f, ios := inspectTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "ses_json",
		JSON:      true,
	}

	err := runInspect(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).Bytes()

	var report diagnostic.InspectReport
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("failed to parse JSON output: %v\nraw: %s", err, string(output))
	}

	if report.SessionID != "ses_json" {
		t.Errorf("expected session_id 'ses_json', got %q", report.SessionID)
	}
	if report.Provider != "opencode" {
		t.Errorf("expected provider 'opencode', got %q", report.Provider)
	}
	if report.Messages != 2 {
		t.Errorf("expected 2 messages, got %d", report.Messages)
	}
	if report.UserMsgs != 1 {
		t.Errorf("expected 1 user message, got %d", report.UserMsgs)
	}
	if report.Tokens == nil {
		t.Fatal("expected tokens section in JSON")
	}
	if report.Tokens.Input != 500 {
		t.Errorf("expected 500 input tokens, got %d", report.Tokens.Input)
	}
	if report.Tokens.CacheRead != 300 {
		t.Errorf("expected 300 cache read tokens, got %d", report.Tokens.CacheRead)
	}
	if report.Tokens.EstCost != 1.50 {
		t.Errorf("expected 1.50 est cost, got %.2f", report.Tokens.EstCost)
	}
}

// --- Section filter ---

func TestInspect_sectionFilter(t *testing.T) {
	now := time.Now()
	sess := &session.Session{
		ID:       "ses_section",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "hello", Timestamp: now},
			{ID: "m2", Role: session.RoleAssistant, Content: "hi", Timestamp: now, Model: "claude-sonnet-4-20250514", InputTokens: 200, OutputTokens: 50},
		},
		TokenUsage: session.TokenUsage{
			InputTokens: 200, OutputTokens: 50, TotalTokens: 250,
		},
		CreatedAt: now,
	}

	store := testutil.NewMockStore(sess)

	tests := []struct {
		section     string
		wantPresent string
		wantAbsent  string
	}{
		{"tokens", "TOKENS", "COMPACTIONS"},
		{"images", "IMAGES", "TOKENS"},
		{"compactions", "COMPACTIONS", "TOKENS"},
		{"commands", "COMMANDS", "TOKENS"},
		{"errors", "TOOL ERRORS", "TOKENS"},
		{"patterns", "BEHAVIORAL PATTERNS", "TOKENS"},
		{"problems", "DETECTED PROBLEMS", "TOKENS"},
	}

	for _, tt := range tests {
		t.Run(tt.section, func(t *testing.T) {
			f, ios := inspectTestFactory(t, store)
			opts := &Options{
				IO:        ios,
				Factory:   f,
				SessionID: "ses_section",
				Section:   tt.section,
			}

			err := runInspect(opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			output := ios.Out.(*bytes.Buffer).String()
			if !strings.Contains(output, tt.wantPresent) {
				t.Errorf("section=%q: expected %q in output", tt.section, tt.wantPresent)
			}
			// The header is always present; section headers are the sectioned part
			if strings.Contains(output, "─── "+tt.wantAbsent) {
				t.Errorf("section=%q: did not expect %q section header in output", tt.section, tt.wantAbsent)
			}
		})
	}
}

// --- Session with images ---

func TestInspect_sessionWithImages(t *testing.T) {
	now := time.Now()
	sess := &session.Session{
		ID:       "ses_images",
		Provider: session.ProviderOpenCode,
		Agent:    "opencode",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "take a screenshot", Timestamp: now},
			{
				ID: "m2", Role: session.RoleAssistant, Content: "taking screenshot", Timestamp: now,
				Model: "claude-opus-4-20250514", InputTokens: 500, OutputTokens: 100,
				ToolCalls: []session.ToolCall{
					{Name: "mcp_bash", Input: `{"command":"xcrun simctl io booted screenshot /tmp/screen.png"}`, Output: "ok"},
					{Name: "mcp_bash", Input: `{"command":"sips -Z 500 /tmp/screen.png --out /tmp/resized.png"}`, Output: "ok"},
				},
			},
			{
				ID: "m3", Role: session.RoleAssistant, Content: "reading image", Timestamp: now,
				Model: "claude-opus-4-20250514", InputTokens: 2000, OutputTokens: 200,
				ToolCalls: []session.ToolCall{
					{Name: "mcp_read", Input: `{"filePath":"/tmp/resized.png"}`, Output: "[image data]"},
				},
			},
			{ID: "m4", Role: session.RoleAssistant, Content: "the UI looks good", Timestamp: now, Model: "claude-opus-4-20250514", InputTokens: 3000, OutputTokens: 100},
		},
		TokenUsage: session.TokenUsage{
			InputTokens: 5500, OutputTokens: 400, TotalTokens: 5900,
		},
		CreatedAt: now,
	}

	store := testutil.NewMockStore(sess)
	f, ios := inspectTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "ses_images",
		Section:   "images",
	}

	err := runInspect(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Tool-read images:  1") {
		t.Error("expected 1 tool-read image")
	}
	if !strings.Contains(output, "simctl captures:   1") {
		t.Error("expected 1 simctl capture")
	}
	if !strings.Contains(output, "sips resizes:      1") {
		t.Error("expected 1 sips resize")
	}
}

// --- Session with commands ---

func TestInspect_sessionWithCommands(t *testing.T) {
	now := time.Now()
	sess := &session.Session{
		ID:       "ses_cmds",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "run tests", Timestamp: now},
			{
				ID: "m2", Role: session.RoleAssistant, Content: "running", Timestamp: now,
				Model: "claude-sonnet-4-20250514", InputTokens: 200, OutputTokens: 50,
				ToolCalls: []session.ToolCall{
					{Name: "bash", Input: `{"command":"go test ./..."}`, Output: strings.Repeat("PASS\n", 100)},
					{Name: "bash", Input: `{"command":"go test ./..."}`, Output: strings.Repeat("PASS\n", 100)},
					{Name: "bash", Input: `{"command":"git status"}`, Output: "On branch main"},
				},
			},
		},
		TokenUsage: session.TokenUsage{
			InputTokens: 200, OutputTokens: 50, TotalTokens: 250,
		},
		CreatedAt: now,
	}

	store := testutil.NewMockStore(sess)
	f, ios := inspectTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "ses_cmds",
		Section:   "commands",
	}

	err := runInspect(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Total:    3") {
		t.Error("expected 3 total commands")
	}
	if !strings.Contains(output, "repeated") {
		t.Error("expected repeated ratio info")
	}
}

// --- Session with tool errors ---

func TestInspect_sessionWithToolErrors(t *testing.T) {
	now := time.Now()
	sess := &session.Session{
		ID:       "ses_errors",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "fix the file", Timestamp: now},
			{
				ID: "m2", Role: session.RoleAssistant, Content: "editing", Timestamp: now,
				Model: "claude-sonnet-4-20250514", InputTokens: 200, OutputTokens: 50,
				ToolCalls: []session.ToolCall{
					{Name: "mcp_edit", Input: `{"filePath":"foo.go"}`, Output: "error: oldString not found", State: session.ToolStateError},
				},
			},
			{
				ID: "m3", Role: session.RoleAssistant, Content: "retry", Timestamp: now,
				Model: "claude-sonnet-4-20250514", InputTokens: 300, OutputTokens: 50,
				ToolCalls: []session.ToolCall{
					{Name: "mcp_edit", Input: `{"filePath":"foo.go"}`, Output: "error: oldString not found", State: session.ToolStateError},
				},
			},
			{
				ID: "m4", Role: session.RoleAssistant, Content: "retry again", Timestamp: now,
				Model: "claude-sonnet-4-20250514", InputTokens: 400, OutputTokens: 50,
				ToolCalls: []session.ToolCall{
					{Name: "mcp_edit", Input: `{"filePath":"foo.go"}`, Output: "error: oldString not found", State: session.ToolStateError},
				},
			},
			{
				ID: "m5", Role: session.RoleAssistant, Content: "reading first", Timestamp: now,
				Model: "claude-sonnet-4-20250514", InputTokens: 500, OutputTokens: 50,
				ToolCalls: []session.ToolCall{
					{Name: "mcp_read", Input: `{"filePath":"foo.go"}`, Output: "package main"},
					{Name: "mcp_edit", Input: `{"filePath":"foo.go"}`, Output: "ok"},
				},
			},
		},
		TokenUsage: session.TokenUsage{
			InputTokens: 1400, OutputTokens: 200, TotalTokens: 1600,
		},
		CreatedAt: now,
	}

	store := testutil.NewMockStore(sess)
	f, ios := inspectTestFactory(t, store)

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "ses_errors",
		Section:   "errors",
	}

	err := runInspect(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "TOOL ERRORS") {
		t.Error("expected TOOL ERRORS section")
	}
	// 5 tool calls total (3 errors + 1 read + 1 edit success)
	if !strings.Contains(output, "Errors:") {
		t.Error("expected error count line")
	}
}

// --- JSON with tool errors produces error loops ---

func TestInspect_jsonToolErrorLoops(t *testing.T) {
	now := time.Now()
	sess := &session.Session{
		ID:       "ses_loops",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "fix", Timestamp: now},
			{ID: "m2", Role: session.RoleAssistant, Content: "try1", Timestamp: now, Model: "claude-sonnet-4-20250514", InputTokens: 100, OutputTokens: 10,
				ToolCalls: []session.ToolCall{{Name: "mcp_edit", State: session.ToolStateError, Input: `{"filePath":"x.go"}`, Output: "error"}}},
			{ID: "m3", Role: session.RoleAssistant, Content: "try2", Timestamp: now, Model: "claude-sonnet-4-20250514", InputTokens: 200, OutputTokens: 10,
				ToolCalls: []session.ToolCall{{Name: "mcp_edit", State: session.ToolStateError, Input: `{"filePath":"x.go"}`, Output: "error"}}},
			{ID: "m4", Role: session.RoleAssistant, Content: "try3", Timestamp: now, Model: "claude-sonnet-4-20250514", InputTokens: 300, OutputTokens: 10,
				ToolCalls: []session.ToolCall{{Name: "mcp_edit", State: session.ToolStateError, Input: `{"filePath":"x.go"}`, Output: "error"}}},
			{ID: "m5", Role: session.RoleAssistant, Content: "try4", Timestamp: now, Model: "claude-sonnet-4-20250514", InputTokens: 400, OutputTokens: 10,
				ToolCalls: []session.ToolCall{{Name: "mcp_edit", State: session.ToolStateError, Input: `{"filePath":"x.go"}`, Output: "error"}}},
		},
		TokenUsage: session.TokenUsage{InputTokens: 1000, OutputTokens: 40, TotalTokens: 1040},
		CreatedAt:  now,
	}

	store := testutil.NewMockStore(sess)
	f, ios := inspectTestFactory(t, store)

	opts := &Options{IO: ios, Factory: f, SessionID: "ses_loops", JSON: true}
	if err := runInspect(opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report diagnostic.InspectReport
	if err := json.Unmarshal(ios.Out.(*bytes.Buffer).Bytes(), &report); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if report.ToolErrors == nil {
		t.Fatal("expected tool_errors section")
	}
	if report.ToolErrors.ErrorCount != 4 {
		t.Errorf("expected 4 errors, got %d", report.ToolErrors.ErrorCount)
	}
	if len(report.ToolErrors.ErrorLoops) == 0 {
		t.Error("expected at least one error loop")
	}
	if report.ToolErrors.ConsecutiveMax < 4 {
		t.Errorf("expected consecutive max >= 4, got %d", report.ToolErrors.ConsecutiveMax)
	}
}

// --- Session with behavioral patterns ---

func TestInspect_behavioralPatterns(t *testing.T) {
	now := time.Now()
	msgs := []session.Message{
		{ID: "m0", Role: session.RoleUser, Content: "fix everything", Timestamp: now},
	}
	// Add 12 consecutive assistant messages (long run)
	for i := 1; i <= 12; i++ {
		msgs = append(msgs, session.Message{
			ID: fmt.Sprintf("m%d", i), Role: session.RoleAssistant,
			Content: fmt.Sprintf("step %d", i), Timestamp: now,
			Model: "claude-sonnet-4-20250514", InputTokens: 100, OutputTokens: 20,
		})
	}
	// Add user corrections (2 consecutive user messages)
	msgs = append(msgs,
		session.Message{ID: "u1", Role: session.RoleUser, Content: "no wait", Timestamp: now},
		session.Message{ID: "u2", Role: session.RoleUser, Content: "actually do this", Timestamp: now},
	)

	sess := &session.Session{
		ID: "ses_patterns", Provider: session.ProviderClaudeCode, Agent: "claude",
		Messages:   msgs,
		TokenUsage: session.TokenUsage{InputTokens: 1200, OutputTokens: 240, TotalTokens: 1440},
		CreatedAt:  now,
	}

	store := testutil.NewMockStore(sess)
	f, ios := inspectTestFactory(t, store)

	opts := &Options{IO: ios, Factory: f, SessionID: "ses_patterns", JSON: true}
	if err := runInspect(opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report diagnostic.InspectReport
	if err := json.Unmarshal(ios.Out.(*bytes.Buffer).Bytes(), &report); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if report.Patterns == nil {
		t.Fatal("expected patterns section")
	}
	if report.Patterns.LongestRunLength < 12 {
		t.Errorf("expected longest run >= 12, got %d", report.Patterns.LongestRunLength)
	}
	if report.Patterns.LongRunCount == 0 {
		t.Error("expected at least 1 long run detected")
	}
	if report.Patterns.UserCorrectionCount == 0 {
		t.Error("expected at least 1 user correction")
	}
}

// --- Compaction detection ---

func TestInspect_compactionDetection(t *testing.T) {
	now := time.Now()
	// Build messages that simulate compaction: token drop >30% between consecutive messages
	msgs := []session.Message{
		{ID: "m1", Role: session.RoleUser, Content: "start", Timestamp: now},
		{ID: "m2", Role: session.RoleAssistant, Content: "working", Timestamp: now, Model: "claude-sonnet-4-20250514", InputTokens: 100000, OutputTokens: 500},
		// Compaction happens: next message has much lower input tokens
		{ID: "m3", Role: session.RoleAssistant, Content: "compacted", Timestamp: now, Model: "claude-sonnet-4-20250514", InputTokens: 30000, OutputTokens: 500},
		{ID: "m4", Role: session.RoleUser, Content: "continue", Timestamp: now},
		{ID: "m5", Role: session.RoleAssistant, Content: "more work", Timestamp: now, Model: "claude-sonnet-4-20250514", InputTokens: 35000, OutputTokens: 500},
	}

	sess := &session.Session{
		ID: "ses_compact", Provider: session.ProviderOpenCode, Agent: "opencode",
		Messages:   msgs,
		TokenUsage: session.TokenUsage{InputTokens: 165000, OutputTokens: 1500, TotalTokens: 166500},
		CreatedAt:  now,
	}

	store := testutil.NewMockStore(sess)
	f, ios := inspectTestFactory(t, store)

	opts := &Options{IO: ios, Factory: f, SessionID: "ses_compact", Section: "compactions"}
	if err := runInspect(opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "COMPACTIONS") {
		t.Error("expected COMPACTIONS section header")
	}
}

// --- Format helpers ---

func TestFmtNum(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1000, "1,000"},
		{1234567, "1,234,567"},
		{100000000, "100,000,000"},
	}
	for _, tt := range tests {
		got := fmtNum(tt.input)
		if got != tt.want {
			t.Errorf("fmtNum(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFmtTok(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0"},
		{500, "500"},
		{1500, "1.5K"},
		{27100000, "27.1M"},
	}
	for _, tt := range tests {
		got := fmtTok(tt.input)
		if got != tt.want {
			t.Errorf("fmtTok(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFmtBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1500, "1.5 KB"},
		{2500000, "2.5 MB"},
	}
	for _, tt := range tests {
		got := fmtBytes(tt.input)
		if got != tt.want {
			t.Errorf("fmtBytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTruncStr(t *testing.T) {
	tests := []struct {
		input string
		max   int
		want  string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is longer", 10, "this is l…"},
	}
	for _, tt := range tests {
		got := truncStr(tt.input, tt.max)
		if got != tt.want {
			t.Errorf("truncStr(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
		}
	}
}

// --- Problem detection in report ---

func TestInspect_problemsDetected(t *testing.T) {
	now := time.Now()
	// Build a session with enough tool errors to trigger "tool-error-loops" problem
	msgs := []session.Message{
		{ID: "m0", Role: session.RoleUser, Content: "fix", Timestamp: now},
	}
	for i := 1; i <= 6; i++ {
		msgs = append(msgs, session.Message{
			ID: fmt.Sprintf("m%d", i), Role: session.RoleAssistant,
			Content: fmt.Sprintf("attempt %d", i), Timestamp: now,
			Model: "claude-sonnet-4-20250514", InputTokens: 200 * i, OutputTokens: 20,
			ToolCalls: []session.ToolCall{
				{Name: "mcp_edit", State: session.ToolStateError, Input: `{"filePath":"x.go"}`, Output: "error"},
			},
		})
	}

	sess := &session.Session{
		ID: "ses_problems", Provider: session.ProviderClaudeCode, Agent: "claude",
		Messages:   msgs,
		TokenUsage: session.TokenUsage{InputTokens: 4200, OutputTokens: 120, TotalTokens: 4320},
		CreatedAt:  now,
	}

	store := testutil.NewMockStore(sess)
	f, ios := inspectTestFactory(t, store)

	// JSON to check problems array
	opts := &Options{IO: ios, Factory: f, SessionID: "ses_problems", JSON: true}
	if err := runInspect(opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report diagnostic.InspectReport
	if err := json.Unmarshal(ios.Out.(*bytes.Buffer).Bytes(), &report); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Should have detected tool-error-loops
	found := false
	for _, p := range report.Problems {
		if p.ID == diagnostic.ProblemToolErrorLoops {
			found = true
			if p.Severity != diagnostic.SeverityHigh {
				t.Errorf("expected high severity for 6 error loops, got %s", p.Severity)
			}
			break
		}
	}
	if !found {
		t.Error("expected 'tool-error-loops' problem to be detected")
	}

	// Text output should also show problems
	f2, ios2 := inspectTestFactory(t, store)
	opts2 := &Options{IO: ios2, Factory: f2, SessionID: "ses_problems", Section: "problems"}
	if err := runInspect(opts2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	textOutput := ios2.Out.(*bytes.Buffer).String()
	if !strings.Contains(textOutput, "problem(s) detected") {
		t.Error("expected problems count in text output")
	}
}

// --- Multi-model session ---

func TestInspect_multiModelSession(t *testing.T) {
	now := time.Now()
	sess := &session.Session{
		ID:       "ses_multi",
		Provider: session.ProviderOpenCode,
		Agent:    "opencode",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "hello", Timestamp: now},
			{ID: "m2", Role: session.RoleAssistant, Content: "hi", Timestamp: now, Model: "claude-opus-4-20250514", InputTokens: 1000, OutputTokens: 200},
			{ID: "m3", Role: session.RoleUser, Content: "fix", Timestamp: now},
			{ID: "m4", Role: session.RoleAssistant, Content: "done", Timestamp: now, Model: "claude-sonnet-4-20250514", InputTokens: 500, OutputTokens: 100},
			{ID: "m5", Role: session.RoleUser, Content: "more", Timestamp: now},
			{ID: "m6", Role: session.RoleAssistant, Content: "ok", Timestamp: now, Model: "claude-opus-4-20250514", InputTokens: 2000, OutputTokens: 300},
		},
		TokenUsage: session.TokenUsage{InputTokens: 3500, OutputTokens: 600, TotalTokens: 4100},
		CreatedAt:  now,
	}

	store := testutil.NewMockStore(sess)
	f, ios := inspectTestFactory(t, store)

	opts := &Options{IO: ios, Factory: f, SessionID: "ses_multi", JSON: true}
	if err := runInspect(opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report diagnostic.InspectReport
	if err := json.Unmarshal(ios.Out.(*bytes.Buffer).Bytes(), &report); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(report.Tokens.Models) != 2 {
		t.Errorf("expected 2 models, got %d", len(report.Tokens.Models))
	}
	// Models sorted by input descending: opus should be first
	if len(report.Tokens.Models) >= 2 {
		if !strings.Contains(report.Tokens.Models[0].Model, "opus") {
			t.Errorf("expected opus model first (highest input), got %q", report.Tokens.Models[0].Model)
		}
	}
}

// --- Generate-fix flag tests ---

func TestNewCmdInspect_generateFixFlags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdInspect(f)

	for _, name := range []string{"generate-fix", "apply"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag to be registered", name)
		}
	}
}

func TestInspect_applyWithoutGenerateFix(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdInspect(f)
	cmd.SetArgs([]string{"--apply", "ses_test"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for --apply without --generate-fix")
	}
	if !strings.Contains(err.Error(), "--apply requires --generate-fix") {
		t.Errorf("expected '--apply requires --generate-fix' in error, got: %v", err)
	}
}

func TestInspect_generateFix_textOutput(t *testing.T) {
	now := time.Now()
	sess := &session.Session{
		ID:       "ses_fix_text",
		Provider: session.ProviderOpenCode,
		Agent:    "opencode",
		Messages: buildYoloEditMessages(now, 10),
		TokenUsage: session.TokenUsage{
			InputTokens: 500000, OutputTokens: 5000, TotalTokens: 505000,
		},
		CreatedAt: now,
	}

	store := testutil.NewMockStore(sess)
	f, ios := inspectTestFactory(t, store)

	opts := &Options{
		IO:          ios,
		Factory:     f,
		SessionID:   "ses_fix_text",
		GenerateFix: true,
	}

	err := runInspect(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()

	if !strings.Contains(output, "GENERATED FIXES") {
		t.Error("expected 'GENERATED FIXES' header")
	}
	if !strings.Contains(output, "opencode") {
		t.Error("expected provider in fix output")
	}
	// Should contain re-run hint since not applied
	if !strings.Contains(output, "re-run with --apply") {
		t.Error("expected 're-run with --apply' hint")
	}
}

func TestInspect_generateFix_jsonOutput(t *testing.T) {
	now := time.Now()
	sess := &session.Session{
		ID:       "ses_fix_json",
		Provider: session.ProviderOpenCode,
		Agent:    "opencode",
		Messages: buildYoloEditMessages(now, 10),
		TokenUsage: session.TokenUsage{
			InputTokens: 500000, OutputTokens: 5000, TotalTokens: 505000,
		},
		CreatedAt: now,
	}

	store := testutil.NewMockStore(sess)
	f, ios := inspectTestFactory(t, store)

	opts := &Options{
		IO:          ios,
		Factory:     f,
		SessionID:   "ses_fix_json",
		GenerateFix: true,
		JSON:        true,
	}

	err := runInspect(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fixSet diagnostic.FixSet
	if err := json.Unmarshal(ios.Out.(*bytes.Buffer).Bytes(), &fixSet); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if fixSet.SessionID != "ses_fix_json" {
		t.Errorf("expected session ID ses_fix_json, got %q", fixSet.SessionID)
	}
	if fixSet.Provider != "opencode" {
		t.Errorf("expected provider opencode, got %q", fixSet.Provider)
	}
}

func TestInspect_generateFix_noProblems(t *testing.T) {
	now := time.Now()
	sess := &session.Session{
		ID:       "ses_no_problems",
		Provider: session.ProviderOpenCode,
		Agent:    "opencode",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "hello", Timestamp: now},
			{ID: "m2", Role: session.RoleAssistant, Content: "hi", Timestamp: now, Model: "claude-sonnet-4-20250514", InputTokens: 100, OutputTokens: 50},
		},
		TokenUsage: session.TokenUsage{
			InputTokens: 100, OutputTokens: 50, TotalTokens: 150,
		},
		CreatedAt: now,
	}

	store := testutil.NewMockStore(sess)
	f, ios := inspectTestFactory(t, store)

	opts := &Options{
		IO:          ios,
		Factory:     f,
		SessionID:   "ses_no_problems",
		GenerateFix: true,
	}

	err := runInspect(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "No fixes generated") {
		t.Error("expected 'No fixes generated' for session without problems")
	}
}

func TestInspect_generateFix_withScreenshots(t *testing.T) {
	now := time.Now()
	msgs := buildScreenshotMessages(now, 50)
	sess := &session.Session{
		ID:       "ses_fix_screenshots",
		Provider: session.ProviderOpenCode,
		Agent:    "opencode",
		Messages: msgs,
		TokenUsage: session.TokenUsage{
			InputTokens: 50_000_000, OutputTokens: 500_000, TotalTokens: 50_500_000,
		},
		CreatedAt: now,
	}

	store := testutil.NewMockStore(sess)
	f, ios := inspectTestFactory(t, store)

	opts := &Options{
		IO:          ios,
		Factory:     f,
		SessionID:   "ses_fix_screenshots",
		GenerateFix: true,
	}

	err := runInspect(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()

	// Should have screenshot-related fixes
	if !strings.Contains(output, "capture-screen.sh") {
		t.Error("expected capture-screen.sh in fix output")
	}
	if !strings.Contains(output, "Screenshot Protocol") {
		t.Error("expected Screenshot Protocol in fix output")
	}
}

func TestInspect_generateFix_claudeCodeProvider(t *testing.T) {
	now := time.Now()
	msgs := buildScreenshotMessages(now, 30)
	sess := &session.Session{
		ID:       "ses_fix_claude",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Messages: msgs,
		TokenUsage: session.TokenUsage{
			InputTokens: 30_000_000, OutputTokens: 300_000, TotalTokens: 30_300_000,
		},
		CreatedAt: now,
	}

	store := testutil.NewMockStore(sess)
	f, ios := inspectTestFactory(t, store)

	opts := &Options{
		IO:          ios,
		Factory:     f,
		SessionID:   "ses_fix_claude",
		GenerateFix: true,
	}

	err := runInspect(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "CLAUDE.md") {
		t.Error("expected CLAUDE.md in claude-code fix output")
	}
}

// --- Helper: build messages with yolo editing pattern ---

func buildYoloEditMessages(now time.Time, count int) []session.Message {
	var msgs []session.Message
	idx := 0
	for i := 0; i < count; i++ {
		idx++
		msgs = append(msgs, session.Message{
			ID: fmt.Sprintf("m%d", idx), Role: session.RoleUser,
			Content: "fix it", Timestamp: now,
		})
		idx++
		// Write without read
		msgs = append(msgs, session.Message{
			ID: fmt.Sprintf("m%d", idx), Role: session.RoleAssistant,
			Content: "done", Timestamp: now,
			Model: "claude-sonnet-4-20250514", InputTokens: 50000, OutputTokens: 500,
			ToolCalls: []session.ToolCall{
				{
					Name:  "mcp_write",
					Input: fmt.Sprintf(`{"filePath":"file%d.go"}`, i),
					State: session.ToolStateCompleted,
				},
			},
		})
	}
	return msgs
}

// --- Helper: build messages with screenshot pattern ---

func buildScreenshotMessages(now time.Time, count int) []session.Message {
	var msgs []session.Message
	idx := 0
	for i := 0; i < count; i++ {
		idx++
		msgs = append(msgs, session.Message{
			ID: fmt.Sprintf("m%d", idx), Role: session.RoleUser,
			Content: "check", Timestamp: now,
		})
		// Simulate simctl capture + sips resize + image read
		idx++
		msgs = append(msgs, session.Message{
			ID: fmt.Sprintf("m%d", idx), Role: session.RoleAssistant,
			Content: "capturing", Timestamp: now,
			Model: "claude-sonnet-4-20250514", InputTokens: 500000, OutputTokens: 1000,
			ToolCalls: []session.ToolCall{
				{
					Name:  "mcp_bash",
					Input: `{"command":"xcrun simctl io booted screenshot /tmp/raw.png"}`,
					State: session.ToolStateCompleted,
				},
				{
					Name:  "mcp_bash",
					Input: `{"command":"sips -Z 1000 /tmp/raw.png --out /tmp/resized.png"}`,
					State: session.ToolStateCompleted,
				},
				{
					Name:  "mcp_read",
					Input: fmt.Sprintf(`{"filePath":"/tmp/screenshot_%d.png"}`, i),
					State: session.ToolStateCompleted,
				},
			},
		})
		// Several assistant turns without new captures (image stays in context)
		for j := 0; j < 5; j++ {
			idx++
			msgs = append(msgs, session.Message{
				ID: fmt.Sprintf("m%d", idx), Role: session.RoleAssistant,
				Content: "working", Timestamp: now,
				Model: "claude-sonnet-4-20250514", InputTokens: 500000, OutputTokens: 500,
			})
		}
	}
	return msgs
}
