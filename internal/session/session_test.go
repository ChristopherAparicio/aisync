package session

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSession_JSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 2, 16, 14, 30, 0, 0, time.UTC)

	session := Session{
		ID:          ID("a1b2c3d4"),
		Version:     1,
		Provider:    ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feature/auth",
		CommitSHA:   "abc1234",
		ProjectPath: "/home/chris/my-app",
		ExportedBy:  "Christopher",
		ExportedAt:  now,
		CreatedAt:   now,
		Summary:     "Implement OAuth2",
		StorageMode: StorageModeCompact,
		Messages: []Message{
			{
				ID:        "msg-001",
				Role:      RoleUser,
				Content:   "Implement OAuth2 with PKCE",
				Timestamp: now,
			},
			{
				ID:      "msg-002",
				Role:    RoleAssistant,
				Content: "I'll implement the OAuth2 PKCE flow",
				Model:   "claude-opus-4-5",
				ToolCalls: []ToolCall{
					{
						ID:         "tc-001",
						Name:       "Read",
						Input:      `{"path": "src/auth.py"}`,
						Output:     "file contents...",
						State:      ToolStateCompleted,
						DurationMs: 150,
					},
				},
				OutputTokens: 1500,
				Timestamp:    now,
			},
		},
		FileChanges: []FileChange{
			{FilePath: "src/auth/oauth.py", ChangeType: ChangeCreated},
			{FilePath: "src/auth/handler.py", ChangeType: ChangeModified},
		},
		TokenUsage: TokenUsage{
			InputTokens:  45000,
			OutputTokens: 12000,
			TotalTokens:  57000,
		},
		Links: []Link{
			{LinkType: LinkBranch, Ref: "feature/auth"},
			{LinkType: LinkCommit, Ref: "abc1234"},
		},
	}

	// Marshal to JSON
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	// Unmarshal back
	var got Session
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	// Verify key fields survived the round trip
	if got.ID != session.ID {
		t.Errorf("ID = %q, want %q", got.ID, session.ID)
	}
	if got.Provider != session.Provider {
		t.Errorf("Provider = %q, want %q", got.Provider, session.Provider)
	}
	if got.Agent != session.Agent {
		t.Errorf("Agent = %q, want %q", got.Agent, session.Agent)
	}
	if got.StorageMode != session.StorageMode {
		t.Errorf("StorageMode = %q, want %q", got.StorageMode, session.StorageMode)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("Messages count = %d, want 2", len(got.Messages))
	}
	if got.Messages[0].Role != RoleUser {
		t.Errorf("Messages[0].Role = %q, want %q", got.Messages[0].Role, RoleUser)
	}
	if got.Messages[1].ToolCalls[0].State != ToolStateCompleted {
		t.Errorf("ToolCalls[0].State = %q, want %q", got.Messages[1].ToolCalls[0].State, ToolStateCompleted)
	}
	if len(got.FileChanges) != 2 {
		t.Errorf("FileChanges count = %d, want 2", len(got.FileChanges))
	}
	if got.TokenUsage.TotalTokens != 57000 {
		t.Errorf("TotalTokens = %d, want 57000", got.TokenUsage.TotalTokens)
	}
	if len(got.Links) != 2 {
		t.Errorf("Links count = %d, want 2", len(got.Links))
	}
}

func TestSession_JSONOmitsEmpty(t *testing.T) {
	session := Session{
		ID:          ID("test"),
		Provider:    ProviderOpenCode,
		Agent:       "coder",
		ProjectPath: "/test",
		StorageMode: StorageModeFull,
	}

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	// Verify empty fields are omitted
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	for _, field := range []string{"branch", "commit_sha", "summary", "messages", "children", "parent_id", "file_changes", "links"} {
		if _, exists := raw[field]; exists {
			t.Errorf("field %q should be omitted when empty, but was present", field)
		}
	}
}

func TestSession_WithChildren(t *testing.T) {
	parent := Session{
		ID:          ID("parent-1"),
		Provider:    ProviderOpenCode,
		Agent:       "coder",
		ProjectPath: "/test",
		StorageMode: StorageModeCompact,
		Children: []Session{
			{
				ID:          ID("child-1"),
				Provider:    ProviderOpenCode,
				Agent:       "task",
				ParentID:    ID("parent-1"),
				ProjectPath: "/test",
				StorageMode: StorageModeCompact,
				Messages: []Message{
					{ID: "msg-c1", Role: RoleUser, Content: "sub-task"},
				},
			},
		},
	}

	data, err := json.Marshal(parent)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var got Session
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if len(got.Children) != 1 {
		t.Fatalf("Children count = %d, want 1", len(got.Children))
	}
	if got.Children[0].Agent != "task" {
		t.Errorf("Children[0].Agent = %q, want %q", got.Children[0].Agent, "task")
	}
	if got.Children[0].ParentID != ID("parent-1") {
		t.Errorf("Children[0].ParentID = %q, want %q", got.Children[0].ParentID, "parent-1")
	}
}
