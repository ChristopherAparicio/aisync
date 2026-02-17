package statscmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// mockStore for stats tests.
type mockStore struct {
	sessions  map[domain.SessionID]*domain.Session
	summaries []domain.SessionSummary
}

func newMockStore() *mockStore {
	return &mockStore{
		sessions: make(map[domain.SessionID]*domain.Session),
	}
}

func (m *mockStore) Save(s *domain.Session) error {
	m.sessions[s.ID] = s
	m.summaries = append(m.summaries, domain.SessionSummary{
		ID:           s.ID,
		Provider:     s.Provider,
		Branch:       s.Branch,
		MessageCount: len(s.Messages),
		TotalTokens:  s.TokenUsage.TotalTokens,
		CreatedAt:    s.CreatedAt,
	})
	return nil
}

func (m *mockStore) Get(id domain.SessionID) (*domain.Session, error) {
	s, ok := m.sessions[id]
	if !ok {
		return nil, domain.ErrSessionNotFound
	}
	return s, nil
}

func (m *mockStore) GetByBranch(_, _ string) (*domain.Session, error) {
	return nil, domain.ErrSessionNotFound
}

func (m *mockStore) List(_ domain.ListOptions) ([]domain.SessionSummary, error) {
	return m.summaries, nil
}

func (m *mockStore) Delete(_ domain.SessionID) error                 { return nil }
func (m *mockStore) AddLink(_ domain.SessionID, _ domain.Link) error { return nil }
func (m *mockStore) Close() error                                    { return nil }

func (m *mockStore) GetByLink(_ domain.LinkType, _ string) ([]domain.SessionSummary, error) {
	return nil, domain.ErrSessionNotFound
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
		StoreFunc: func() (domain.Store, error) { return store, nil },
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
	s1 := &domain.Session{
		ID:       "stats-1",
		Provider: domain.ProviderClaudeCode,
		Branch:   "feature-auth",
		Messages: make([]domain.Message, 5),
		TokenUsage: domain.TokenUsage{
			InputTokens:  1000,
			OutputTokens: 500,
			TotalTokens:  1500,
		},
		FileChanges: []domain.FileChange{
			{FilePath: "auth.go", ChangeType: domain.ChangeCreated},
			{FilePath: "main.go", ChangeType: domain.ChangeModified},
		},
		CreatedAt: time.Now(),
	}
	s2 := &domain.Session{
		ID:       "stats-2",
		Provider: domain.ProviderOpenCode,
		Branch:   "feature-api",
		Messages: make([]domain.Message, 10),
		TokenUsage: domain.TokenUsage{
			InputTokens:  3000,
			OutputTokens: 2000,
			TotalTokens:  5000,
		},
		FileChanges: []domain.FileChange{
			{FilePath: "api.go", ChangeType: domain.ChangeCreated},
			{FilePath: "main.go", ChangeType: domain.ChangeModified},
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
