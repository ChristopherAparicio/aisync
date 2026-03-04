package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/llm"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Summarize ──

func TestSummarize_success(t *testing.T) {
	summary := session.StructuredSummary{
		Intent:    "Add authentication",
		Outcome:   "JWT middleware implemented",
		Decisions: []string{"Used JWT over sessions"},
		Friction:  []string{"CORS issues"},
		OpenItems: []string{"Add refresh tokens"},
	}
	summaryJSON, _ := json.Marshal(summary)

	mockLLM := &mockLLMClient{
		response: &llm.CompletionResponse{
			Content:      string(summaryJSON),
			Model:        "test-model",
			InputTokens:  100,
			OutputTokens: 50,
		},
	}

	store := &mockStore{
		sessions: make(map[session.ID]*session.Session),
	}

	svc := NewSessionService(SessionServiceConfig{
		Store: store,
		LLM:   mockLLM,
	})

	sess := &session.Session{
		ID: "test-session",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "Add JWT auth"},
			{Role: session.RoleAssistant, Content: "I'll implement JWT middleware..."},
		},
	}

	result, err := svc.Summarize(context.Background(), SummarizeRequest{Session: sess})
	if err != nil {
		t.Fatalf("Summarize() error: %v", err)
	}

	if result.Summary.Intent != "Add authentication" {
		t.Errorf("Intent = %q, want %q", result.Summary.Intent, "Add authentication")
	}
	if result.OneLine != "Add authentication: JWT middleware implemented" {
		t.Errorf("OneLine = %q, want %q", result.OneLine, "Add authentication: JWT middleware implemented")
	}
	if result.Model != "test-model" {
		t.Errorf("Model = %q, want %q", result.Model, "test-model")
	}
	if result.TokensUsed != 150 {
		t.Errorf("TokensUsed = %d, want 150", result.TokensUsed)
	}
}

func TestSummarize_noLLM(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{
		Store: &mockStore{sessions: make(map[session.ID]*session.Session)},
		// No LLM
	})

	sess := &session.Session{
		ID:       "test",
		Messages: []session.Message{{Role: session.RoleUser, Content: "hello"}},
	}

	_, err := svc.Summarize(context.Background(), SummarizeRequest{Session: sess})
	if err == nil {
		t.Fatal("expected error when LLM is nil")
	}
}

func TestSummarize_noMessages(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{
		Store: &mockStore{sessions: make(map[session.ID]*session.Session)},
		LLM:   &mockLLMClient{},
	})

	sess := &session.Session{ID: "test"}
	_, err := svc.Summarize(context.Background(), SummarizeRequest{Session: sess})
	if err == nil {
		t.Fatal("expected error for session with no messages")
	}
}

// ── Explain ──

func TestExplain_success(t *testing.T) {
	mockLLM := &mockLLMClient{
		response: &llm.CompletionResponse{
			Content:      "This session implemented JWT authentication...",
			Model:        "test-model",
			InputTokens:  200,
			OutputTokens: 100,
		},
	}

	sess := &session.Session{
		ID:       "explain-test",
		Messages: []session.Message{{Role: session.RoleUser, Content: "Add auth"}},
	}
	store := &mockStore{
		sessions: map[session.ID]*session.Session{
			sess.ID: sess,
		},
	}

	svc := NewSessionService(SessionServiceConfig{
		Store: store,
		LLM:   mockLLM,
	})

	result, err := svc.Explain(context.Background(), ExplainRequest{
		SessionID: sess.ID,
	})
	if err != nil {
		t.Fatalf("Explain() error: %v", err)
	}

	if result.Explanation != "This session implemented JWT authentication..." {
		t.Errorf("Explanation = %q, want expected text", result.Explanation)
	}
	if result.SessionID != "explain-test" {
		t.Errorf("SessionID = %q, want %q", result.SessionID, "explain-test")
	}
}

func TestExplain_noLLM(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{
		Store: &mockStore{sessions: make(map[session.ID]*session.Session)},
	})

	_, err := svc.Explain(context.Background(), ExplainRequest{SessionID: "any"})
	if err == nil {
		t.Fatal("expected error when LLM is nil")
	}
}

func TestExplain_sessionNotFound(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{
		Store: &mockStore{sessions: make(map[session.ID]*session.Session)},
		LLM:   &mockLLMClient{},
	})

	_, err := svc.Explain(context.Background(), ExplainRequest{SessionID: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

// ── Rewind ──

func TestRewind_success(t *testing.T) {
	original := &session.Session{
		ID:       "original",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Branch:   "feat/auth",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "Step 1", Tokens: 10},
			{ID: "m2", Role: session.RoleAssistant, Content: "Done 1", Tokens: 20},
			{ID: "m3", Role: session.RoleUser, Content: "Step 2", Tokens: 15},
			{ID: "m4", Role: session.RoleAssistant, Content: "Done 2", Tokens: 25},
			{ID: "m5", Role: session.RoleUser, Content: "Step 3", Tokens: 12},
		},
		FileChanges: []session.FileChange{
			{FilePath: "auth.go", ChangeType: session.ChangeModified},
		},
	}

	store := &mockStore{
		sessions: map[session.ID]*session.Session{
			original.ID: original,
		},
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})

	result, err := svc.Rewind(context.Background(), RewindRequest{
		SessionID: original.ID,
		AtMessage: 3,
	})
	if err != nil {
		t.Fatalf("Rewind() error: %v", err)
	}

	if result.OriginalID != "original" {
		t.Errorf("OriginalID = %q, want %q", result.OriginalID, "original")
	}
	if result.TruncatedAt != 3 {
		t.Errorf("TruncatedAt = %d, want 3", result.TruncatedAt)
	}
	if result.MessagesRemoved != 2 {
		t.Errorf("MessagesRemoved = %d, want 2", result.MessagesRemoved)
	}
	if len(result.NewSession.Messages) != 3 {
		t.Errorf("new session has %d messages, want 3", len(result.NewSession.Messages))
	}
	if result.NewSession.Messages[2].ID != "m3" {
		t.Errorf("last message ID = %q, want %q", result.NewSession.Messages[2].ID, "m3")
	}
	if result.NewSession.ParentID != "original" {
		t.Errorf("ParentID = %q, want %q", result.NewSession.ParentID, "original")
	}
	if result.NewSession.Branch != "feat/auth" {
		t.Errorf("Branch = %q, want %q", result.NewSession.Branch, "feat/auth")
	}
	// Verify it was saved
	if _, ok := store.sessions[result.NewSession.ID]; !ok {
		t.Error("new session was not saved to store")
	}
}

func TestRewind_outOfRange(t *testing.T) {
	sess := &session.Session{
		ID: "test",
		Messages: []session.Message{
			{ID: "m1", Role: session.RoleUser, Content: "hello"},
		},
	}
	store := &mockStore{
		sessions: map[session.ID]*session.Session{sess.ID: sess},
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})

	_, err := svc.Rewind(context.Background(), RewindRequest{
		SessionID: sess.ID,
		AtMessage: 5, // only 1 message exists
	})
	if err == nil {
		t.Fatal("expected error for out-of-range message index")
	}
}

func TestRewind_noMessages(t *testing.T) {
	sess := &session.Session{ID: "empty"}
	store := &mockStore{
		sessions: map[session.ID]*session.Session{sess.ID: sess},
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})

	_, err := svc.Rewind(context.Background(), RewindRequest{
		SessionID: sess.ID,
		AtMessage: 1,
	})
	if err == nil {
		t.Fatal("expected error for session with no messages")
	}
}

// ── StructuredSummary.OneLine ──

func TestStructuredSummary_OneLine(t *testing.T) {
	tests := []struct {
		name     string
		summary  session.StructuredSummary
		expected string
	}{
		{
			name:     "both fields",
			summary:  session.StructuredSummary{Intent: "Add auth", Outcome: "JWT implemented"},
			expected: "Add auth: JWT implemented",
		},
		{
			name:     "intent only",
			summary:  session.StructuredSummary{Intent: "Add auth"},
			expected: "Add auth",
		},
		{
			name:     "outcome only",
			summary:  session.StructuredSummary{Outcome: "Done"},
			expected: "Done",
		},
		{
			name:     "both empty",
			summary:  session.StructuredSummary{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.summary.OneLine(); got != tt.expected {
				t.Errorf("OneLine() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// ── buildSessionTranscript ──

func TestBuildSessionTranscript_emptySession(t *testing.T) {
	sess := &session.Session{ID: "empty"}
	if got := buildSessionTranscript(sess); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestBuildSessionTranscript_withMessages(t *testing.T) {
	sess := &session.Session{
		ID:       "test",
		Provider: session.ProviderClaudeCode,
		Branch:   "main",
		Summary:  "Test session",
		Messages: []session.Message{
			{Role: session.RoleUser, Content: "hello"},
			{Role: session.RoleAssistant, Content: "hi there"},
		},
		FileChanges: []session.FileChange{
			{FilePath: "main.go", ChangeType: session.ChangeModified},
		},
	}

	result := buildSessionTranscript(sess)
	if result == "" {
		t.Fatal("expected non-empty transcript")
	}
	// Should contain key info
	for _, want := range []string{"test", "claude-code", "main", "hello", "hi there", "main.go"} {
		if !contains(result, want) {
			t.Errorf("transcript missing %q", want)
		}
	}
}

func TestBuildSessionTranscript_truncatesLargeSessions(t *testing.T) {
	messages := make([]session.Message, 30)
	for i := range messages {
		messages[i] = session.Message{
			Role:    session.RoleUser,
			Content: "message content",
		}
	}

	sess := &session.Session{
		ID:       "large",
		Messages: messages,
	}

	result := buildSessionTranscript(sess)
	// Should mention truncation
	if !contains(result, "showing") {
		t.Error("expected truncation notice for large session")
	}
}

// ── Helpers ──

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ── Mocks ──

type mockLLMClient struct {
	response *llm.CompletionResponse
	err      error
	lastReq  llm.CompletionRequest
}

func (m *mockLLMClient) Complete(_ context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	m.lastReq = req
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

type mockStore struct {
	sessions map[session.ID]*session.Session
}

func (m *mockStore) Save(sess *session.Session) error {
	m.sessions[sess.ID] = sess
	return nil
}

func (m *mockStore) Get(id session.ID) (*session.Session, error) {
	s, ok := m.sessions[id]
	if !ok {
		return nil, session.ErrSessionNotFound
	}
	return s, nil
}

func (m *mockStore) GetByBranch(_, _ string) (*session.Session, error) {
	return nil, session.ErrSessionNotFound
}
func (m *mockStore) List(_ session.ListOptions) ([]session.Summary, error) { return nil, nil }
func (m *mockStore) Delete(_ session.ID) error                             { return nil }
func (m *mockStore) AddLink(_ session.ID, _ session.Link) error            { return nil }
func (m *mockStore) GetByLink(_ session.LinkType, _ string) ([]session.Summary, error) {
	return nil, session.ErrSessionNotFound
}
func (m *mockStore) Close() error                                   { return nil }
func (m *mockStore) SaveUser(_ *session.User) error                 { return nil }
func (m *mockStore) GetUser(_ session.ID) (*session.User, error)    { return nil, nil }
func (m *mockStore) GetUserByEmail(_ string) (*session.User, error) { return nil, nil }
func (m *mockStore) Search(_ session.SearchQuery) (*session.SearchResult, error) {
	return &session.SearchResult{}, nil
}
func (m *mockStore) GetSessionsByFile(_ session.BlameQuery) ([]session.BlameEntry, error) {
	return nil, nil
}
