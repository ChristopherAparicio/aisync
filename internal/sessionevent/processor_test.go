package sessionevent

import (
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Processor tests ──

func TestProcessor_ExtractAll_EmptySession(t *testing.T) {
	p := NewProcessor()
	sess := &session.Session{
		ID:       "test-empty",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
	}

	events, summary := p.ExtractAll(sess)

	// Should still get agent detection event even with no messages.
	if len(events) != 1 {
		t.Fatalf("expected 1 event (agent detection), got %d", len(events))
	}
	if events[0].Type != EventAgentDetection {
		t.Fatalf("expected agent_detection event, got %s", events[0].Type)
	}
	if summary.TotalEvents != 1 {
		t.Fatalf("expected total_events=1, got %d", summary.TotalEvents)
	}
}

func TestProcessor_ExtractAll_ToolCalls(t *testing.T) {
	p := NewProcessor()
	sess := &session.Session{
		ID:          "test-tools",
		Provider:    session.ProviderClaudeCode,
		Agent:       "claude",
		ProjectPath: "/home/user/project",
		RemoteURL:   "github.com/org/repo",
		Messages: []session.Message{
			{
				ID:        "msg-1",
				Timestamp: time.Now(),
				Role:      session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{
						ID:         "tc-1",
						Name:       "Read",
						State:      session.ToolStateCompleted,
						DurationMs: 100,
					},
					{
						ID:         "tc-2",
						Name:       "bash",
						Input:      `{"command": "git status"}`,
						State:      session.ToolStateCompleted,
						DurationMs: 500,
					},
					{
						ID:    "tc-3",
						Name:  "file_edit",
						State: session.ToolStateError,
					},
				},
			},
		},
	}

	events, summary := p.ExtractAll(sess)

	// Should have: 1 agent + 3 tool_call + 1 command (from bash) = 5
	if summary.ToolCallCount != 3 {
		t.Errorf("expected tool_call_count=3, got %d", summary.ToolCallCount)
	}
	if summary.CommandCount != 1 {
		t.Errorf("expected command_count=1, got %d", summary.CommandCount)
	}
	if summary.UniqueToolCount != 3 {
		t.Errorf("expected unique_tool_count=3, got %d", summary.UniqueToolCount)
	}

	// Check command details.
	var cmdEvents []Event
	for _, e := range events {
		if e.Type == EventCommand {
			cmdEvents = append(cmdEvents, e)
		}
	}
	if len(cmdEvents) != 1 {
		t.Fatalf("expected 1 command event, got %d", len(cmdEvents))
	}
	if cmdEvents[0].Command.BaseCommand != "git" {
		t.Errorf("expected base_command=git, got %s", cmdEvents[0].Command.BaseCommand)
	}

	// Check project context is propagated.
	for _, e := range events {
		if e.ProjectPath != "/home/user/project" {
			t.Errorf("event %s missing project_path", e.ID)
		}
		if e.RemoteURL != "github.com/org/repo" {
			t.Errorf("event %s missing remote_url", e.ID)
		}
	}
}

func TestProcessor_ExtractAll_SkillsViaToolCall(t *testing.T) {
	p := NewProcessor()
	sess := &session.Session{
		ID:       "test-skills-tc",
		Provider: session.ProviderOpenCode,
		Agent:    "opencode",
		Messages: []session.Message{
			{
				ID:        "msg-1",
				Timestamp: time.Now(),
				Role:      session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{
						ID:    "tc-1",
						Name:  "load_skill",
						Input: `{"name": "replay-tester"}`,
						State: session.ToolStateCompleted,
					},
					{
						ID:     "tc-2",
						Name:   "skill",
						Input:  `{"name": "opencode-sessions"}`,
						Output: "skill loaded",
						State:  session.ToolStateCompleted,
					},
				},
			},
		},
	}

	_, summary := p.ExtractAll(sess)

	if summary.SkillLoadCount != 2 {
		t.Errorf("expected skill_load_count=2, got %d", summary.SkillLoadCount)
	}
	if len(summary.SkillsLoaded) != 2 {
		t.Errorf("expected 2 unique skills, got %d", len(summary.SkillsLoaded))
	}

	// Verify skill names are correct.
	skillSet := make(map[string]bool)
	for _, s := range summary.SkillsLoaded {
		skillSet[s] = true
	}
	if !skillSet["replay-tester"] {
		t.Error("expected replay-tester in skills_loaded")
	}
	if !skillSet["opencode-sessions"] {
		t.Error("expected opencode-sessions in skills_loaded")
	}
}

func TestProcessor_ExtractAll_SkillsViaContentTag(t *testing.T) {
	p := NewProcessor()
	sess := &session.Session{
		ID:       "test-skills-tag",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Messages: []session.Message{
			{
				ID:        "msg-1",
				Timestamp: time.Now(),
				Role:      session.RoleAssistant,
				Content:   `Here is the skill content: <skill_content name="my-skill">some content</skill_content> and <skill_content name="other-skill">more</skill_content>`,
			},
		},
	}

	_, summary := p.ExtractAll(sess)

	if summary.SkillLoadCount != 2 {
		t.Errorf("expected skill_load_count=2, got %d", summary.SkillLoadCount)
	}

	skillSet := make(map[string]bool)
	for _, s := range summary.SkillsLoaded {
		skillSet[s] = true
	}
	if !skillSet["my-skill"] {
		t.Error("expected my-skill in skills_loaded")
	}
	if !skillSet["other-skill"] {
		t.Error("expected other-skill in skills_loaded")
	}
}

func TestProcessor_ExtractAll_Errors(t *testing.T) {
	p := NewProcessor()
	sess := &session.Session{
		ID:       "test-errors",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Errors: []session.SessionError{
			{
				ID:         "err-1",
				SessionID:  "test-errors",
				Category:   session.ErrorCategoryRateLimit,
				Source:     session.ErrorSourceProvider,
				Message:    "rate limit exceeded",
				HTTPStatus: 429,
				OccurredAt: time.Now(),
			},
			{
				ID:         "err-2",
				SessionID:  "test-errors",
				Category:   session.ErrorCategoryToolError,
				Source:     session.ErrorSourceTool,
				Message:    "file not found",
				ToolName:   "file_edit",
				OccurredAt: time.Now(),
			},
		},
	}

	_, summary := p.ExtractAll(sess)

	if summary.ErrorCount != 2 {
		t.Errorf("expected error_count=2, got %d", summary.ErrorCount)
	}
	if summary.ErrorByCategory[session.ErrorCategoryRateLimit] != 1 {
		t.Error("expected 1 rate_limit error")
	}
	if summary.ErrorByCategory[session.ErrorCategoryToolError] != 1 {
		t.Error("expected 1 tool_error")
	}
	if summary.ErrorBySource[session.ErrorSourceProvider] != 1 {
		t.Error("expected 1 provider error")
	}
}

func TestProcessor_ExtractAll_Images(t *testing.T) {
	p := NewProcessor()
	sess := &session.Session{
		ID:       "test-images",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Messages: []session.Message{
			{
				ID:        "msg-1",
				Timestamp: time.Now(),
				Role:      session.RoleUser,
				Images: []session.ImageMeta{
					{MediaType: "image/png", SizeBytes: 1024, TokensEstimate: 100, Source: "base64"},
					{MediaType: "image/jpeg", SizeBytes: 2048, TokensEstimate: 200, Source: "file"},
				},
			},
		},
	}

	_, summary := p.ExtractAll(sess)

	if summary.ImageCount != 2 {
		t.Errorf("expected image_count=2, got %d", summary.ImageCount)
	}
}

func TestProcessor_ExtractAll_Models(t *testing.T) {
	p := NewProcessor()
	sess := &session.Session{
		ID:       "test-models",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Messages: []session.Message{
			{ID: "msg-1", Timestamp: time.Now(), Role: session.RoleAssistant, Model: "claude-sonnet-4-20250514"},
			{ID: "msg-2", Timestamp: time.Now(), Role: session.RoleAssistant, Model: "claude-sonnet-4-20250514"}, // duplicate
			{ID: "msg-3", Timestamp: time.Now(), Role: session.RoleAssistant, Model: "claude-opus-4-20250514"},
		},
	}

	_, summary := p.ExtractAll(sess)

	if len(summary.Models) != 2 {
		t.Errorf("expected 2 unique models, got %d: %v", len(summary.Models), summary.Models)
	}
}

func TestProcessor_ExtractAll_ComplexSession(t *testing.T) {
	p := NewProcessor()
	now := time.Now()
	sess := &session.Session{
		ID:          "test-complex",
		Provider:    session.ProviderOpenCode,
		Agent:       "jarvis",
		ProjectPath: "/home/user/aisync",
		RemoteURL:   "github.com/org/aisync",
		CreatedAt:   now,
		Messages: []session.Message{
			{
				ID: "msg-1", Timestamp: now, Role: session.RoleUser,
				Content: "Please help me fix the login bug",
				Images:  []session.ImageMeta{{MediaType: "image/png", SizeBytes: 1024}},
			},
			{
				ID: "msg-2", Timestamp: now.Add(time.Minute), Role: session.RoleAssistant,
				Model:   "claude-sonnet-4-20250514",
				Content: `Let me load a skill: <skill_content name="replay-tester">content here</skill_content>`,
				ToolCalls: []session.ToolCall{
					{ID: "tc-1", Name: "Read", Input: `{"path": "main.go"}`, State: session.ToolStateCompleted, DurationMs: 50},
					{ID: "tc-2", Name: "bash", Input: `{"command": "go test ./..."}`, State: session.ToolStateError, DurationMs: 3000},
					{ID: "tc-3", Name: "load_skill", Input: `{"name": "replay-tester"}`, State: session.ToolStateCompleted},
				},
			},
			{
				ID: "msg-3", Timestamp: now.Add(2 * time.Minute), Role: session.RoleAssistant,
				Model: "claude-sonnet-4-20250514",
				ToolCalls: []session.ToolCall{
					{ID: "tc-4", Name: "file_edit", State: session.ToolStateCompleted, DurationMs: 200},
					{ID: "tc-5", Name: "bash", Input: `{"command": "go test ./..."}`, State: session.ToolStateCompleted, DurationMs: 2000},
				},
			},
		},
		Errors: []session.SessionError{
			{
				ID: "err-1", SessionID: "test-complex",
				Category: session.ErrorCategoryToolError, Source: session.ErrorSourceTool,
				Message: "exit code 1", ToolName: "bash", OccurredAt: now.Add(time.Minute),
			},
		},
	}

	events, summary := p.ExtractAll(sess)

	// Verify comprehensive extraction.
	if summary.ToolCallCount != 5 {
		t.Errorf("tool_call_count: expected 5, got %d", summary.ToolCallCount)
	}
	if summary.UniqueToolCount != 4 {
		t.Errorf("unique_tool_count: expected 4 (Read, bash, file_edit, load_skill), got %d", summary.UniqueToolCount)
	}
	if summary.CommandCount != 2 {
		t.Errorf("command_count: expected 2, got %d", summary.CommandCount)
	}
	if summary.CommandErrorCount != 1 {
		t.Errorf("command_error_count: expected 1, got %d", summary.CommandErrorCount)
	}
	// Skills: 1 via tool call + 1 via content tag = 2, but same skill name → might be 1 unique
	// Actually the skill "replay-tester" appears both ways, but detector generates separate events
	if summary.SkillLoadCount < 1 {
		t.Errorf("skill_load_count: expected >= 1, got %d", summary.SkillLoadCount)
	}
	if summary.ErrorCount != 1 {
		t.Errorf("error_count: expected 1, got %d", summary.ErrorCount)
	}
	if summary.ImageCount != 1 {
		t.Errorf("image_count: expected 1, got %d", summary.ImageCount)
	}
	if summary.Provider != session.ProviderOpenCode {
		t.Errorf("provider: expected opencode, got %s", summary.Provider)
	}
	if summary.Agent != "jarvis" {
		t.Errorf("agent: expected jarvis, got %s", summary.Agent)
	}
	if len(summary.Models) != 1 {
		t.Errorf("models: expected 1, got %d: %v", len(summary.Models), summary.Models)
	}

	// Verify all events have session context.
	for _, e := range events {
		if e.SessionID != "test-complex" {
			t.Errorf("event %s has wrong session_id: %s", e.ID, e.SessionID)
		}
		if e.ID == "" {
			t.Error("event has empty ID")
		}
	}
}

// ── Bucket tests ──

func TestBucketAggregator_HourlyBuckets(t *testing.T) {
	agg := NewBucketAggregator()
	now := time.Date(2025, 3, 25, 14, 30, 0, 0, time.UTC) // 14:30 UTC

	events := []Event{
		{Type: EventToolCall, OccurredAt: now, ProjectPath: "/p", Provider: "opencode",
			ToolCall: &ToolCallDetail{ToolName: "bash", State: session.ToolStateCompleted}},
		{Type: EventToolCall, OccurredAt: now.Add(10 * time.Minute), ProjectPath: "/p", Provider: "opencode",
			ToolCall: &ToolCallDetail{ToolName: "Read", State: session.ToolStateCompleted}},
		{Type: EventToolCall, OccurredAt: now.Add(90 * time.Minute), ProjectPath: "/p", Provider: "opencode",
			ToolCall: &ToolCallDetail{ToolName: "bash", State: session.ToolStateError}},
		{Type: EventSkillLoad, OccurredAt: now, ProjectPath: "/p", Provider: "opencode",
			SkillLoad: &SkillLoadDetail{SkillName: "replay-tester"}},
		{Type: EventCommand, OccurredAt: now, ProjectPath: "/p", Provider: "opencode",
			Command: &CommandDetail{BaseCommand: "git", State: session.ToolStateCompleted}},
	}

	buckets := agg.Aggregate(events, "1h")

	// Should have 2 buckets: 14:00-15:00 and 16:00-17:00
	if len(buckets) != 2 {
		t.Fatalf("expected 2 hourly buckets, got %d", len(buckets))
	}

	// Find the 14:00 bucket.
	var bucket14 *EventBucket
	for i := range buckets {
		if buckets[i].BucketStart.Hour() == 14 {
			bucket14 = &buckets[i]
			break
		}
	}
	if bucket14 == nil {
		t.Fatal("14:00 bucket not found")
	}

	if bucket14.ToolCallCount != 2 {
		t.Errorf("14:00 bucket tool_call_count: expected 2, got %d", bucket14.ToolCallCount)
	}
	if bucket14.SkillLoadCount != 1 {
		t.Errorf("14:00 bucket skill_load_count: expected 1, got %d", bucket14.SkillLoadCount)
	}
	if bucket14.CommandCount != 1 {
		t.Errorf("14:00 bucket command_count: expected 1, got %d", bucket14.CommandCount)
	}
	if bucket14.UniqueTools != 2 {
		t.Errorf("14:00 bucket unique_tools: expected 2, got %d", bucket14.UniqueTools)
	}
}

func TestBucketAggregator_DailyBuckets(t *testing.T) {
	agg := NewBucketAggregator()
	day1 := time.Date(2025, 3, 24, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2025, 3, 25, 15, 0, 0, 0, time.UTC)

	events := []Event{
		{Type: EventToolCall, OccurredAt: day1, ProjectPath: "/p", Provider: "claude-code",
			ToolCall: &ToolCallDetail{ToolName: "bash"}},
		{Type: EventToolCall, OccurredAt: day2, ProjectPath: "/p", Provider: "claude-code",
			ToolCall: &ToolCallDetail{ToolName: "Read"}},
		{Type: EventToolCall, OccurredAt: day2, ProjectPath: "/p", Provider: "claude-code",
			ToolCall: &ToolCallDetail{ToolName: "bash"}},
	}

	buckets := agg.Aggregate(events, "1d")

	if len(buckets) != 2 {
		t.Fatalf("expected 2 daily buckets, got %d", len(buckets))
	}
}

func TestMergeBuckets(t *testing.T) {
	existing := &EventBucket{
		ToolCallCount:  5,
		SkillLoadCount: 1,
		SessionCount:   2,
		TopTools:       map[string]int{"bash": 3, "Read": 2},
		TopSkills:      map[string]int{"replay-tester": 1},
		AgentBreakdown: map[string]int{"claude": 2},
		TopCommands:    map[string]int{"git": 2, "npm": 1},
		ErrorByCategory: map[session.ErrorCategory]int{
			session.ErrorCategoryToolError: 1,
		},
	}

	incoming := &EventBucket{
		ToolCallCount:  3,
		SkillLoadCount: 2,
		SessionCount:   1,
		TopTools:       map[string]int{"bash": 1, "file_edit": 2},
		TopSkills:      map[string]int{"replay-tester": 1, "opencode-sessions": 1},
		AgentBreakdown: map[string]int{"jarvis": 1},
		TopCommands:    map[string]int{"git": 1},
		ErrorByCategory: map[session.ErrorCategory]int{
			session.ErrorCategoryRateLimit: 1,
		},
	}

	MergeBuckets(existing, incoming)

	if existing.ToolCallCount != 8 {
		t.Errorf("tool_call_count: expected 8, got %d", existing.ToolCallCount)
	}
	if existing.SkillLoadCount != 3 {
		t.Errorf("skill_load_count: expected 3, got %d", existing.SkillLoadCount)
	}
	if existing.SessionCount != 3 {
		t.Errorf("session_count: expected 3, got %d", existing.SessionCount)
	}
	if existing.TopTools["bash"] != 4 {
		t.Errorf("top_tools[bash]: expected 4, got %d", existing.TopTools["bash"])
	}
	if existing.UniqueTools != 3 {
		t.Errorf("unique_tools: expected 3, got %d", existing.UniqueTools)
	}
	if existing.UniqueSkills != 2 {
		t.Errorf("unique_skills: expected 2, got %d", existing.UniqueSkills)
	}
	if existing.AgentBreakdown["claude"] != 2 || existing.AgentBreakdown["jarvis"] != 1 {
		t.Errorf("agent_breakdown unexpected: %v", existing.AgentBreakdown)
	}
}

// ── Domain tests ──

func TestEventType_Valid(t *testing.T) {
	tests := []struct {
		t    EventType
		want bool
	}{
		{EventToolCall, true},
		{EventSkillLoad, true},
		{EventAgentDetection, true},
		{EventError, true},
		{EventCommand, true},
		{EventImageUsage, true},
		{"unknown_type", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := tt.t.Valid(); got != tt.want {
			t.Errorf("EventType(%q).Valid() = %v, want %v", tt.t, got, tt.want)
		}
	}
}

func TestNewSessionEventSummary_Empty(t *testing.T) {
	summary := NewSessionEventSummary("test-id", nil)

	if summary.SessionID != "test-id" {
		t.Errorf("expected session_id=test-id, got %s", summary.SessionID)
	}
	if summary.TotalEvents != 0 {
		t.Errorf("expected total_events=0, got %d", summary.TotalEvents)
	}
}

// ── Helper tests ──

func TestExtractSkillName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`{"name": "replay-tester"}`, "replay-tester"},
		{`{"skill_name": "my-skill"}`, "my-skill"},
		{`{"skill": "another"}`, "another"},
		{`"plain-name"`, "plain-name"},
		{`simple-name`, "simple-name"},
		{``, ""},
		{`{"other": "value"}`, ""},
		{`has spaces`, ""},
	}

	for _, tt := range tests {
		got := extractSkillName(tt.input)
		if got != tt.want {
			t.Errorf("extractSkillName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractSkillContentTags(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{`No skills here`, 0},
		{`<skill_content name="one">content</skill_content>`, 1},
		{`<skill_content name="a">x</skill_content> and <skill_content name="b">y</skill_content>`, 2},
		{`<skill_content name="">empty`, 0},
	}

	for _, tt := range tests {
		got := extractSkillContentTags(tt.input)
		if len(got) != tt.want {
			t.Errorf("extractSkillContentTags(%q) returned %d names, want %d", tt.input, len(got), tt.want)
		}
	}
}

// ── ToolCategory classification tests ──

func TestProcessor_ToolCategory_Builtin(t *testing.T) {
	p := NewProcessor()
	sess := &session.Session{
		ID:       "test-toolcat-builtin",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Messages: []session.Message{
			{
				ID:        "msg-1",
				Timestamp: time.Now(),
				Role:      session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{ID: "tc-1", Name: "Read", State: session.ToolStateCompleted},
					{ID: "tc-2", Name: "bash", State: session.ToolStateCompleted},
					{ID: "tc-3", Name: "file_edit", State: session.ToolStateCompleted},
				},
			},
		},
	}

	events, _ := p.ExtractAll(sess)

	for _, e := range events {
		if e.Type != EventToolCall {
			continue
		}
		if e.ToolCall.ToolCategory != "builtin" {
			t.Errorf("tool %q: expected category=builtin, got %q",
				e.ToolCall.ToolName, e.ToolCall.ToolCategory)
		}
	}
}

func TestProcessor_ToolCategory_MCP_ClaudeCode(t *testing.T) {
	p := NewProcessor()
	sess := &session.Session{
		ID:       "test-toolcat-mcp-cc",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Messages: []session.Message{
			{
				ID:        "msg-1",
				Timestamp: time.Now(),
				Role:      session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{ID: "tc-1", Name: "mcp__notion__search", State: session.ToolStateCompleted},
					{ID: "tc-2", Name: "mcp__sentry__get_issue", State: session.ToolStateCompleted},
				},
			},
		},
	}

	events, summary := p.ExtractAll(sess)

	// Verify tool categories.
	for _, e := range events {
		if e.Type != EventToolCall {
			continue
		}
		switch e.ToolCall.ToolName {
		case "mcp__notion__search":
			if e.ToolCall.ToolCategory != "mcp:notion" {
				t.Errorf("mcp__notion__search: expected category=mcp:notion, got %q", e.ToolCall.ToolCategory)
			}
		case "mcp__sentry__get_issue":
			if e.ToolCall.ToolCategory != "mcp:sentry" {
				t.Errorf("mcp__sentry__get_issue: expected category=mcp:sentry, got %q", e.ToolCall.ToolCategory)
			}
		}
	}

	// Verify MCP server breakdown in summary.
	if summary.MCPServerBreakdown["notion"] != 1 {
		t.Errorf("expected MCPServerBreakdown[notion]=1, got %d", summary.MCPServerBreakdown["notion"])
	}
	if summary.MCPServerBreakdown["sentry"] != 1 {
		t.Errorf("expected MCPServerBreakdown[sentry]=1, got %d", summary.MCPServerBreakdown["sentry"])
	}
}

func TestProcessor_ToolCategory_MCP_OpenCode(t *testing.T) {
	p := NewProcessor()
	sess := &session.Session{
		ID:       "test-toolcat-mcp-oc",
		Provider: session.ProviderOpenCode,
		Agent:    "opencode",
		Messages: []session.Message{
			{
				ID:        "msg-1",
				Timestamp: time.Now(),
				Role:      session.RoleAssistant,
				ToolCalls: []session.ToolCall{
					{ID: "tc-1", Name: "notionApi_API-post-search", State: session.ToolStateCompleted},
					{ID: "tc-2", Name: "sentry_search_issues", State: session.ToolStateCompleted},
					{ID: "tc-3", Name: "langfuse-local_fetch_traces", State: session.ToolStateCompleted},
					{ID: "tc-4", Name: "context7_resolve-library-id", State: session.ToolStateCompleted},
				},
			},
		},
	}

	events, summary := p.ExtractAll(sess)

	expectedCategories := map[string]string{
		"notionApi_API-post-search":   "mcp:notion",
		"sentry_search_issues":        "mcp:sentry",
		"langfuse-local_fetch_traces": "mcp:langfuse",
		"context7_resolve-library-id": "mcp:context7",
	}

	for _, e := range events {
		if e.Type != EventToolCall {
			continue
		}
		expected, ok := expectedCategories[e.ToolCall.ToolName]
		if ok && e.ToolCall.ToolCategory != expected {
			t.Errorf("tool %q: expected category=%q, got %q",
				e.ToolCall.ToolName, expected, e.ToolCall.ToolCategory)
		}
	}

	// Verify MCP server breakdown.
	if summary.MCPServerBreakdown["notion"] != 1 {
		t.Errorf("expected MCPServerBreakdown[notion]=1, got %d", summary.MCPServerBreakdown["notion"])
	}
	if summary.MCPServerBreakdown["langfuse"] != 1 {
		t.Errorf("expected MCPServerBreakdown[langfuse]=1, got %d", summary.MCPServerBreakdown["langfuse"])
	}
}

// ── TopMCPServers in bucket aggregation ──

func TestBucketAggregator_TopMCPServers(t *testing.T) {
	agg := NewBucketAggregator()
	now := time.Date(2025, 3, 25, 14, 30, 0, 0, time.UTC)

	events := []Event{
		{Type: EventToolCall, OccurredAt: now, ProjectPath: "/p", Provider: "opencode",
			ToolCall: &ToolCallDetail{ToolName: "mcp__notion__search", ToolCategory: "mcp:notion", State: session.ToolStateCompleted}},
		{Type: EventToolCall, OccurredAt: now, ProjectPath: "/p", Provider: "opencode",
			ToolCall: &ToolCallDetail{ToolName: "mcp__notion__create_page", ToolCategory: "mcp:notion", State: session.ToolStateCompleted}},
		{Type: EventToolCall, OccurredAt: now, ProjectPath: "/p", Provider: "opencode",
			ToolCall: &ToolCallDetail{ToolName: "mcp__sentry__get_issue", ToolCategory: "mcp:sentry", State: session.ToolStateCompleted}},
		{Type: EventToolCall, OccurredAt: now, ProjectPath: "/p", Provider: "opencode",
			ToolCall: &ToolCallDetail{ToolName: "bash", ToolCategory: "builtin", State: session.ToolStateCompleted}},
	}

	buckets := agg.Aggregate(events, "1h")
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}

	b := buckets[0]
	if b.TopMCPServers["notion"] != 2 {
		t.Errorf("expected TopMCPServers[notion]=2, got %d", b.TopMCPServers["notion"])
	}
	if b.TopMCPServers["sentry"] != 1 {
		t.Errorf("expected TopMCPServers[sentry]=1, got %d", b.TopMCPServers["sentry"])
	}
	if _, exists := b.TopMCPServers["builtin"]; exists {
		t.Error("builtin tools should not appear in TopMCPServers")
	}
	if b.ToolCallCount != 4 {
		t.Errorf("expected ToolCallCount=4, got %d", b.ToolCallCount)
	}
}

// ── Compaction event type tests ──

func TestEventType_Compaction_Valid(t *testing.T) {
	if !EventCompaction.Valid() {
		t.Error("EventCompaction should be a valid event type")
	}
}

func TestBucketAggregator_CompactionCount(t *testing.T) {
	agg := NewBucketAggregator()
	now := time.Date(2025, 3, 25, 14, 30, 0, 0, time.UTC)

	events := []Event{
		{Type: EventCompaction, OccurredAt: now, ProjectPath: "/p", Provider: "opencode",
			Compaction: &CompactionDetail{
				BeforeMessageIdx:  45,
				AfterMessageIdx:   46,
				BeforeInputTokens: 180000,
				AfterInputTokens:  50000,
				DropRatio:         0.278,
				CacheInvalidated:  true,
				Model:             "claude-opus-4-20250514",
			}},
		{Type: EventCompaction, OccurredAt: now.Add(10 * time.Minute), ProjectPath: "/p", Provider: "opencode",
			Compaction: &CompactionDetail{
				BeforeMessageIdx:  90,
				AfterMessageIdx:   91,
				BeforeInputTokens: 195000,
				AfterInputTokens:  60000,
				DropRatio:         0.308,
				CacheInvalidated:  true,
				Model:             "claude-opus-4-20250514",
			}},
		{Type: EventToolCall, OccurredAt: now, ProjectPath: "/p", Provider: "opencode",
			ToolCall: &ToolCallDetail{ToolName: "bash", ToolCategory: "builtin", State: session.ToolStateCompleted}},
	}

	buckets := agg.Aggregate(events, "1h")
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}

	b := buckets[0]
	if b.CompactionCount != 2 {
		t.Errorf("expected CompactionCount=2, got %d", b.CompactionCount)
	}
	if b.ToolCallCount != 1 {
		t.Errorf("expected ToolCallCount=1, got %d", b.ToolCallCount)
	}
}

func TestSessionEventSummary_CompactionCount(t *testing.T) {
	events := []Event{
		{Type: EventCompaction, OccurredAt: time.Now(), SessionID: "ses-1",
			Compaction: &CompactionDetail{BeforeInputTokens: 180000, AfterInputTokens: 50000}},
		{Type: EventToolCall, OccurredAt: time.Now(), SessionID: "ses-1",
			ToolCall: &ToolCallDetail{ToolName: "bash", ToolCategory: "builtin"}},
	}

	summary := NewSessionEventSummary("ses-1", events)
	if summary.CompactionCount != 1 {
		t.Errorf("expected CompactionCount=1, got %d", summary.CompactionCount)
	}
	if summary.ToolCallCount != 1 {
		t.Errorf("expected ToolCallCount=1, got %d", summary.ToolCallCount)
	}
	if summary.TotalEvents != 2 {
		t.Errorf("expected TotalEvents=2, got %d", summary.TotalEvents)
	}
}

// ── MergeBuckets with new fields ──

func TestMergeBuckets_WithMCPServersAndCompaction(t *testing.T) {
	existing := &EventBucket{
		ToolCallCount:   5,
		CompactionCount: 1,
		TopTools:        map[string]int{"bash": 3},
		TopMCPServers:   map[string]int{"notion": 2},
		TopSkills:       map[string]int{},
		AgentBreakdown:  map[string]int{},
		TopCommands:     map[string]int{},
		ErrorByCategory: map[session.ErrorCategory]int{},
	}

	incoming := &EventBucket{
		ToolCallCount:   3,
		CompactionCount: 2,
		TopTools:        map[string]int{"bash": 1},
		TopMCPServers:   map[string]int{"notion": 1, "sentry": 3},
		TopSkills:       map[string]int{},
		AgentBreakdown:  map[string]int{},
		TopCommands:     map[string]int{},
		ErrorByCategory: map[session.ErrorCategory]int{},
	}

	MergeBuckets(existing, incoming)

	if existing.CompactionCount != 3 {
		t.Errorf("expected CompactionCount=3, got %d", existing.CompactionCount)
	}
	if existing.TopMCPServers["notion"] != 3 {
		t.Errorf("expected TopMCPServers[notion]=3, got %d", existing.TopMCPServers["notion"])
	}
	if existing.TopMCPServers["sentry"] != 3 {
		t.Errorf("expected TopMCPServers[sentry]=3, got %d", existing.TopMCPServers["sentry"])
	}
}

// ── Compaction Detection Heuristic Tests ──

func TestProcessor_Compaction_ClearDrop(t *testing.T) {
	// Simulate a real compaction: input tokens grow steadily then drop sharply.
	p := NewProcessor()
	now := time.Now()
	sess := &session.Session{
		ID:          "test-compaction-clear",
		Provider:    session.ProviderOpenCode,
		Agent:       "opencode",
		ProjectPath: "/home/user/project",
		Messages: []session.Message{
			{ID: "m1", Timestamp: now, Role: session.RoleUser, Content: "start"},
			{ID: "m2", Timestamp: now.Add(1 * time.Minute), Role: session.RoleAssistant,
				Model: "claude-opus-4", InputTokens: 50000, OutputTokens: 2000, CacheReadTokens: 40000},
			{ID: "m3", Timestamp: now.Add(2 * time.Minute), Role: session.RoleUser, Content: "continue"},
			{ID: "m4", Timestamp: now.Add(3 * time.Minute), Role: session.RoleAssistant,
				Model: "claude-opus-4", InputTokens: 100000, OutputTokens: 3000, CacheReadTokens: 85000},
			{ID: "m5", Timestamp: now.Add(4 * time.Minute), Role: session.RoleUser, Content: "more"},
			{ID: "m6", Timestamp: now.Add(5 * time.Minute), Role: session.RoleAssistant,
				Model: "claude-opus-4", InputTokens: 180000, OutputTokens: 4000, CacheReadTokens: 160000},
			// === COMPACTION HERE === input drops from 180K to 50K, cache invalidated
			{ID: "m7", Timestamp: now.Add(6 * time.Minute), Role: session.RoleUser, Content: "after compaction"},
			{ID: "m8", Timestamp: now.Add(7 * time.Minute), Role: session.RoleAssistant,
				Model: "claude-opus-4", InputTokens: 50000, OutputTokens: 2000, CacheReadTokens: 0, CacheWriteTokens: 50000},
		},
	}

	events, summary := p.ExtractAll(sess)

	// Find compaction events.
	var compactions []Event
	for _, e := range events {
		if e.Type == EventCompaction {
			compactions = append(compactions, e)
		}
	}

	if len(compactions) != 1 {
		t.Fatalf("expected 1 compaction event, got %d", len(compactions))
	}

	c := compactions[0]
	if c.Compaction.BeforeInputTokens != 180000 {
		t.Errorf("BeforeInputTokens = %d, want 180000", c.Compaction.BeforeInputTokens)
	}
	if c.Compaction.AfterInputTokens != 50000 {
		t.Errorf("AfterInputTokens = %d, want 50000", c.Compaction.AfterInputTokens)
	}
	if !c.Compaction.CacheInvalidated {
		t.Error("expected CacheInvalidated=true")
	}
	if c.Compaction.Model != "claude-opus-4" {
		t.Errorf("Model = %q, want claude-opus-4", c.Compaction.Model)
	}
	if c.Compaction.BeforeMessageIdx != 5 { // m6 is index 5
		t.Errorf("BeforeMessageIdx = %d, want 5", c.Compaction.BeforeMessageIdx)
	}
	if c.Compaction.AfterMessageIdx != 7 { // m8 is index 7
		t.Errorf("AfterMessageIdx = %d, want 7", c.Compaction.AfterMessageIdx)
	}

	// Drop ratio should be 50000/180000 ≈ 0.278
	if c.Compaction.DropRatio < 0.27 || c.Compaction.DropRatio > 0.29 {
		t.Errorf("DropRatio = %f, want ~0.278", c.Compaction.DropRatio)
	}

	// Summary should count it.
	if summary.CompactionCount != 1 {
		t.Errorf("summary.CompactionCount = %d, want 1", summary.CompactionCount)
	}
}

func TestProcessor_Compaction_NormalGrowth(t *testing.T) {
	// Normal conversation: tokens grow monotonically → no compaction.
	p := NewProcessor()
	now := time.Now()
	sess := &session.Session{
		ID:       "test-no-compaction",
		Provider: session.ProviderOpenCode,
		Agent:    "opencode",
		Messages: []session.Message{
			{ID: "m1", Timestamp: now, Role: session.RoleAssistant,
				InputTokens: 20000, OutputTokens: 1000, CacheReadTokens: 15000},
			{ID: "m2", Timestamp: now.Add(time.Minute), Role: session.RoleAssistant,
				InputTokens: 40000, OutputTokens: 1500, CacheReadTokens: 30000},
			{ID: "m3", Timestamp: now.Add(2 * time.Minute), Role: session.RoleAssistant,
				InputTokens: 60000, OutputTokens: 2000, CacheReadTokens: 50000},
			{ID: "m4", Timestamp: now.Add(3 * time.Minute), Role: session.RoleAssistant,
				InputTokens: 80000, OutputTokens: 2500, CacheReadTokens: 65000},
		},
	}

	events, summary := p.ExtractAll(sess)

	for _, e := range events {
		if e.Type == EventCompaction {
			t.Error("expected no compaction events for normal growing context")
		}
	}
	if summary.CompactionCount != 0 {
		t.Errorf("summary.CompactionCount = %d, want 0", summary.CompactionCount)
	}
}

func TestProcessor_Compaction_SmallDropIgnored(t *testing.T) {
	// 30% drop — below the 50% threshold → no compaction.
	p := NewProcessor()
	now := time.Now()
	sess := &session.Session{
		ID:       "test-small-drop",
		Provider: session.ProviderOpenCode,
		Agent:    "opencode",
		Messages: []session.Message{
			{ID: "m1", Timestamp: now, Role: session.RoleAssistant,
				InputTokens: 100000, OutputTokens: 2000, CacheReadTokens: 80000},
			{ID: "m2", Timestamp: now.Add(time.Minute), Role: session.RoleAssistant,
				InputTokens: 70000, OutputTokens: 2000, CacheReadTokens: 50000}, // 30% drop
		},
	}

	events, _ := p.ExtractAll(sess)

	for _, e := range events {
		if e.Type == EventCompaction {
			t.Error("expected no compaction for 30% drop (below 50% threshold)")
		}
	}
}

func TestProcessor_Compaction_SmallContextIgnored(t *testing.T) {
	// Drop from 5K to 2K — below minBeforeTokens threshold (10K).
	p := NewProcessor()
	now := time.Now()
	sess := &session.Session{
		ID:       "test-small-context",
		Provider: session.ProviderOpenCode,
		Agent:    "opencode",
		Messages: []session.Message{
			{ID: "m1", Timestamp: now, Role: session.RoleAssistant,
				InputTokens: 5000, OutputTokens: 500},
			{ID: "m2", Timestamp: now.Add(time.Minute), Role: session.RoleAssistant,
				InputTokens: 2000, OutputTokens: 300}, // 60% drop but context too small
		},
	}

	events, _ := p.ExtractAll(sess)

	for _, e := range events {
		if e.Type == EventCompaction {
			t.Error("expected no compaction for small context (before < 10K)")
		}
	}
}

func TestProcessor_Compaction_DoubleCompaction(t *testing.T) {
	// Long session with two compaction events.
	p := NewProcessor()
	now := time.Now()
	sess := &session.Session{
		ID:       "test-double-compaction",
		Provider: session.ProviderOpenCode,
		Agent:    "opencode",
		Messages: []session.Message{
			// Phase 1: grow to 180K
			{ID: "m1", Timestamp: now, Role: session.RoleAssistant,
				Model: "claude-opus-4", InputTokens: 50000, CacheReadTokens: 40000},
			{ID: "m2", Timestamp: now.Add(time.Minute), Role: session.RoleAssistant,
				Model: "claude-opus-4", InputTokens: 120000, CacheReadTokens: 100000},
			{ID: "m3", Timestamp: now.Add(2 * time.Minute), Role: session.RoleAssistant,
				Model: "claude-opus-4", InputTokens: 180000, CacheReadTokens: 160000},
			// Compaction #1: 180K → 45K
			{ID: "m4", Timestamp: now.Add(3 * time.Minute), Role: session.RoleAssistant,
				Model: "claude-opus-4", InputTokens: 45000, CacheReadTokens: 0, CacheWriteTokens: 45000},
			// Phase 2: grow again to 190K
			{ID: "m5", Timestamp: now.Add(4 * time.Minute), Role: session.RoleAssistant,
				Model: "claude-opus-4", InputTokens: 100000, CacheReadTokens: 40000},
			{ID: "m6", Timestamp: now.Add(5 * time.Minute), Role: session.RoleAssistant,
				Model: "claude-opus-4", InputTokens: 190000, CacheReadTokens: 170000},
			// Compaction #2: 190K → 55K
			{ID: "m7", Timestamp: now.Add(6 * time.Minute), Role: session.RoleAssistant,
				Model: "claude-opus-4", InputTokens: 55000, CacheReadTokens: 0, CacheWriteTokens: 55000},
		},
	}

	events, summary := p.ExtractAll(sess)

	var compactions []Event
	for _, e := range events {
		if e.Type == EventCompaction {
			compactions = append(compactions, e)
		}
	}

	if len(compactions) != 2 {
		t.Fatalf("expected 2 compaction events, got %d", len(compactions))
	}

	// First compaction: 180K → 45K
	if compactions[0].Compaction.BeforeInputTokens != 180000 {
		t.Errorf("compaction[0].Before = %d, want 180000", compactions[0].Compaction.BeforeInputTokens)
	}
	if compactions[0].Compaction.AfterInputTokens != 45000 {
		t.Errorf("compaction[0].After = %d, want 45000", compactions[0].Compaction.AfterInputTokens)
	}

	// Second compaction: 190K → 55K
	if compactions[1].Compaction.BeforeInputTokens != 190000 {
		t.Errorf("compaction[1].Before = %d, want 190000", compactions[1].Compaction.BeforeInputTokens)
	}
	if compactions[1].Compaction.AfterInputTokens != 55000 {
		t.Errorf("compaction[1].After = %d, want 55000", compactions[1].Compaction.AfterInputTokens)
	}

	// Both should have cache invalidated.
	for i, c := range compactions {
		if !c.Compaction.CacheInvalidated {
			t.Errorf("compaction[%d]: expected CacheInvalidated=true", i)
		}
	}

	if summary.CompactionCount != 2 {
		t.Errorf("summary.CompactionCount = %d, want 2", summary.CompactionCount)
	}
}

func TestProcessor_Compaction_UserMessagesIgnored(t *testing.T) {
	// User messages have InputTokens but should not be considered for compaction.
	p := NewProcessor()
	now := time.Now()
	sess := &session.Session{
		ID:       "test-user-ignored",
		Provider: session.ProviderOpenCode,
		Agent:    "opencode",
		Messages: []session.Message{
			{ID: "m1", Timestamp: now, Role: session.RoleAssistant,
				InputTokens: 100000, CacheReadTokens: 80000},
			// User message with low InputTokens — should NOT trigger compaction.
			{ID: "m2", Timestamp: now.Add(time.Minute), Role: session.RoleUser,
				InputTokens: 500},
			{ID: "m3", Timestamp: now.Add(2 * time.Minute), Role: session.RoleAssistant,
				InputTokens: 120000, CacheReadTokens: 100000}, // normal growth
		},
	}

	events, _ := p.ExtractAll(sess)

	for _, e := range events {
		if e.Type == EventCompaction {
			t.Error("expected no compaction — user messages should be filtered out")
		}
	}
}

func TestProcessor_Compaction_CacheNotInvalidated(t *testing.T) {
	// Drop with cache still active — compaction detected but cache not invalidated.
	// This could happen with model switching or unusual provider behavior.
	p := NewProcessor()
	now := time.Now()
	sess := &session.Session{
		ID:       "test-cache-not-invalidated",
		Provider: session.ProviderOpenCode,
		Agent:    "opencode",
		Messages: []session.Message{
			{ID: "m1", Timestamp: now, Role: session.RoleAssistant,
				InputTokens: 150000, CacheReadTokens: 120000},
			{ID: "m2", Timestamp: now.Add(time.Minute), Role: session.RoleAssistant,
				InputTokens: 60000, CacheReadTokens: 30000}, // 60% drop but cache_read=50% of input
		},
	}

	events, _ := p.ExtractAll(sess)

	var compactions []Event
	for _, e := range events {
		if e.Type == EventCompaction {
			compactions = append(compactions, e)
		}
	}

	if len(compactions) != 1 {
		t.Fatalf("expected 1 compaction (drop is >50%%), got %d", len(compactions))
	}

	// Should detect compaction but CacheInvalidated should be false.
	if compactions[0].Compaction.CacheInvalidated {
		t.Error("expected CacheInvalidated=false (cache_read is 50% of input)")
	}
}
