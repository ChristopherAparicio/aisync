package statscmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// mockStore for stats tests.
type mockStore struct {
	sessions  map[session.ID]*session.Session
	summaries []session.Summary
}

func newMockStore() *mockStore {
	return &mockStore{
		sessions: make(map[session.ID]*session.Session),
	}
}

func (m *mockStore) Save(s *session.Session) error {
	m.sessions[s.ID] = s
	m.summaries = append(m.summaries, session.Summary{
		ID:           s.ID,
		Provider:     s.Provider,
		Branch:       s.Branch,
		MessageCount: len(s.Messages),
		TotalTokens:  s.TokenUsage.TotalTokens,
		CreatedAt:    s.CreatedAt,
	})
	return nil
}

func (m *mockStore) Get(id session.ID) (*session.Session, error) {
	s, ok := m.sessions[id]
	if !ok {
		return nil, session.ErrSessionNotFound
	}
	return s, nil
}

func (m *mockStore) GetLatestByBranch(_, _ string) (*session.Session, error) {
	return nil, session.ErrSessionNotFound
}
func (m *mockStore) CountByBranch(_, _ string) (int, error) { return 0, nil }

func (m *mockStore) List(_ session.ListOptions) ([]session.Summary, error) {
	return m.summaries, nil
}

func (m *mockStore) Delete(_ session.ID) error                  { return nil }
func (m *mockStore) AddLink(_ session.ID, _ session.Link) error { return nil }
func (m *mockStore) DeleteOlderThan(_ time.Time) (int, error)   { return 0, nil }
func (m *mockStore) Close() error                               { return nil }

func (m *mockStore) GetByLink(_ session.LinkType, _ string) ([]session.Summary, error) {
	return nil, session.ErrSessionNotFound
}
func (m *mockStore) SaveUser(_ *session.User) error                 { return nil }
func (m *mockStore) GetUser(_ session.ID) (*session.User, error)    { return nil, nil }
func (m *mockStore) GetUserByEmail(_ string) (*session.User, error) { return nil, nil }
func (m *mockStore) Search(_ session.SearchQuery) (*session.SearchResult, error) {
	return &session.SearchResult{}, nil
}
func (m *mockStore) GetSessionsByFile(_ session.BlameQuery) ([]session.BlameEntry, error) {
	return nil, nil
}

func statsTestFactory(t *testing.T, store *mockStore) (*cmdutil.Factory, *iostreams.IOStreams) {
	t.Helper()
	ios := iostreams.Test()
	repoDir := testutil.InitTestRepo(t)

	if store == nil {
		store = newMockStore()
	}
	gitClient := git.NewClient(repoDir)

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		StoreFunc: func() (storage.Store, error) { return store, nil },
		SessionServiceFunc: func() (*service.SessionService, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store: store,
				Git:   gitClient,
			}), nil
		},
	}

	return f, ios
}

func TestNewCmdStats_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdStats(f)

	flags := []string{"branch", "provider", "all"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestStats_noSessions(t *testing.T) {
	store := newMockStore()
	f, ios := statsTestFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
	}

	err := runStats(opts)
	if err != nil {
		t.Fatalf("runStats() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "No sessions found") {
		t.Errorf("expected 'No sessions found', got: %s", output)
	}
}

func TestStats_withSessions(t *testing.T) {
	store := newMockStore()

	// Save sessions with file changes
	s1 := &session.Session{
		ID:       "stats-1",
		Provider: session.ProviderClaudeCode,
		Branch:   "feature-auth",
		Messages: make([]session.Message, 5),
		TokenUsage: session.TokenUsage{
			InputTokens:  1000,
			OutputTokens: 500,
			TotalTokens:  1500,
		},
		FileChanges: []session.FileChange{
			{FilePath: "auth.go", ChangeType: session.ChangeCreated},
			{FilePath: "main.go", ChangeType: session.ChangeModified},
		},
		CreatedAt: time.Now(),
	}
	s2 := &session.Session{
		ID:       "stats-2",
		Provider: session.ProviderOpenCode,
		Branch:   "feature-api",
		Messages: make([]session.Message, 10),
		TokenUsage: session.TokenUsage{
			InputTokens:  3000,
			OutputTokens: 2000,
			TotalTokens:  5000,
		},
		FileChanges: []session.FileChange{
			{FilePath: "api.go", ChangeType: session.ChangeCreated},
			{FilePath: "main.go", ChangeType: session.ChangeModified},
		},
		CreatedAt: time.Now(),
	}
	_ = store.Save(s1)
	_ = store.Save(s2)

	f, ios := statsTestFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
	}

	err := runStats(opts)
	if err != nil {
		t.Fatalf("runStats() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()

	// Check overall stats
	if !strings.Contains(output, "Sessions:  2") {
		t.Errorf("expected 2 sessions in output, got:\n%s", output)
	}
	if !strings.Contains(output, "Messages:  15") {
		t.Errorf("expected 15 messages in output, got:\n%s", output)
	}
	if !strings.Contains(output, "6.5k") {
		t.Errorf("expected 6.5k total tokens in output, got:\n%s", output)
	}

	// Check by provider
	if !strings.Contains(output, "claude-code") {
		t.Error("expected claude-code in provider stats")
	}
	if !strings.Contains(output, "opencode") {
		t.Error("expected opencode in provider stats")
	}

	// Check by branch
	if !strings.Contains(output, "feature-auth") {
		t.Error("expected feature-auth in branch stats")
	}
	if !strings.Contains(output, "feature-api") {
		t.Error("expected feature-api in branch stats")
	}

	// Check most touched files
	if !strings.Contains(output, "main.go") {
		t.Error("expected main.go in top files")
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		name  string
		want  string
		input int
	}{
		{"zero", "0", 0},
		{"small", "500", 500},
		{"thousands", "57.0k", 57000},
		{"millions", "1.5M", 1500000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTokens(tt.input)
			if got != tt.want {
				t.Errorf("formatTokens(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   string
		maxLen int
	}{
		{"short", "hello", "hello", 10},
		{"exact", "hello", "hello", 5},
		{"long", "hello world", "hello w…", 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}
