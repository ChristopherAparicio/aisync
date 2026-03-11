package searchcmd

import (
	"bytes"
	"encoding/json"
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

// mockStore for search tests.
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
		Agent:        s.Agent,
		Branch:       s.Branch,
		Summary:      s.Summary,
		MessageCount: len(s.Messages),
		TotalTokens:  s.TokenUsage.TotalTokens,
		CreatedAt:    s.CreatedAt,
		OwnerID:      s.OwnerID,
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
func (m *mockStore) Search(q session.SearchQuery) (*session.SearchResult, error) {
	// Simple search simulation: filter summaries by keyword in Summary field
	var matched []session.Summary
	for _, s := range m.summaries {
		if q.Keyword != "" && !strings.Contains(strings.ToLower(s.Summary), strings.ToLower(q.Keyword)) {
			continue
		}
		if q.Branch != "" && s.Branch != q.Branch {
			continue
		}
		if q.Provider != "" && s.Provider != q.Provider {
			continue
		}
		matched = append(matched, s)
	}
	if matched == nil {
		matched = []session.Summary{}
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	return &session.SearchResult{
		Sessions:   matched,
		TotalCount: len(matched),
		Limit:      limit,
		Offset:     q.Offset,
	}, nil
}
func (m *mockStore) GetSessionsByFile(_ session.BlameQuery) ([]session.BlameEntry, error) {
	return nil, nil
}

func searchTestFactory(t *testing.T, store *mockStore) (*cmdutil.Factory, *iostreams.IOStreams) {
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

func TestNewCmdSearch_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdSearch(f)

	flags := []string{"branch", "provider", "owner-id", "since", "until", "limit", "json", "quiet"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestSearch_noResults(t *testing.T) {
	store := newMockStore()
	f, ios := searchTestFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
		Keyword: "nonexistent",
	}

	err := runSearch(opts)
	if err != nil {
		t.Fatalf("runSearch() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "No sessions found") {
		t.Errorf("expected 'No sessions found', got: %s", output)
	}
}

func TestSearch_tableOutput(t *testing.T) {
	store := newMockStore()

	s1 := &session.Session{
		ID:       "search-1",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Branch:   "feature/auth",
		Summary:  "Implement OAuth2",
		Messages: make([]session.Message, 5),
		TokenUsage: session.TokenUsage{
			TotalTokens: 1500,
		},
		CreatedAt: time.Now(),
	}
	_ = store.Save(s1)

	f, ios := searchTestFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
		Keyword: "OAuth2",
	}

	err := runSearch(opts)
	if err != nil {
		t.Fatalf("runSearch() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()

	// Check header is printed
	if !strings.Contains(output, "Found 1 session(s)") {
		t.Errorf("expected 'Found 1 session(s)' in output, got:\n%s", output)
	}
	// Check table columns header
	if !strings.Contains(output, "ID") || !strings.Contains(output, "PROVIDER") {
		t.Errorf("expected table headers in output, got:\n%s", output)
	}
	// Check data row
	if !strings.Contains(output, "search-1") {
		t.Errorf("expected session ID in output, got:\n%s", output)
	}
	if !strings.Contains(output, "claude-code") {
		t.Errorf("expected provider in output, got:\n%s", output)
	}
}

func TestSearch_jsonOutput(t *testing.T) {
	store := newMockStore()

	s1 := &session.Session{
		ID:       "json-1",
		Provider: session.ProviderClaudeCode,
		Agent:    "claude",
		Branch:   "main",
		Summary:  "Test session",
		Messages: make([]session.Message, 3),
		TokenUsage: session.TokenUsage{
			TotalTokens: 500,
		},
		CreatedAt: time.Now(),
	}
	_ = store.Save(s1)

	f, ios := searchTestFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
		JSON:    true,
	}

	err := runSearch(opts)
	if err != nil {
		t.Fatalf("runSearch() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()

	var result session.SearchResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("JSON output should be valid: %v\noutput: %s", err, output)
	}
	if result.TotalCount != 1 {
		t.Errorf("expected 1 result in JSON, got %d", result.TotalCount)
	}
}

func TestSearch_quietOutput(t *testing.T) {
	store := newMockStore()

	for _, id := range []string{"quiet-1", "quiet-2"} {
		_ = store.Save(&session.Session{
			ID:       session.ID(id),
			Provider: session.ProviderClaudeCode,
			Agent:    "claude",
			Summary:  "test",
		})
	}

	f, ios := searchTestFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
		Quiet:   true,
	}

	err := runSearch(opts)
	if err != nil {
		t.Fatalf("runSearch() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines (one per ID), got %d: %s", len(lines), output)
	}
	if !strings.Contains(output, "quiet-1") {
		t.Errorf("expected quiet-1 in output, got: %s", output)
	}
	if !strings.Contains(output, "quiet-2") {
		t.Errorf("expected quiet-2 in output, got: %s", output)
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
		{"long", "hello world!", "hello worl…", 11},
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

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		name  string
		want  string
		input int
	}{
		{"zero", "-", 0},
		{"small", "500", 500},
		{"thousands", "1k", 1500},
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

func TestTimeAgo(t *testing.T) {
	tests := []struct {
		name  string
		input time.Time
		want  string
	}{
		{"zero", time.Time{}, "-"},
		{"just now", time.Now(), "just now"},
		{"minutes", time.Now().Add(-5 * time.Minute), "5 min ago"},
		{"hours", time.Now().Add(-3 * time.Hour), "3 hours ago"},
		{"1 day", time.Now().Add(-25 * time.Hour), "1 day ago"},
		{"days", time.Now().Add(-72 * time.Hour), "3 days ago"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := timeAgo(tt.input)
			if got != tt.want {
				t.Errorf("timeAgo() = %q, want %q", got, tt.want)
			}
		})
	}
}
