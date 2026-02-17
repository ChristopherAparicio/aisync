package commentcmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/git"
	"github.com/ChristopherAparicio/aisync/internal/domain"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// mockStore for comment tests.
type mockStore struct {
	sessions map[domain.SessionID]*domain.Session
	byBranch map[string]*domain.Session
	links    map[string][]domain.SessionSummary
}

func newMockStore() *mockStore {
	return &mockStore{
		sessions: make(map[domain.SessionID]*domain.Session),
		byBranch: make(map[string]*domain.Session),
		links:    make(map[string][]domain.SessionSummary),
	}
}

func (m *mockStore) Save(s *domain.Session) error {
	m.sessions[s.ID] = s
	key := s.ProjectPath + ":" + s.Branch
	m.byBranch[key] = s
	return nil
}

func (m *mockStore) Get(id domain.SessionID) (*domain.Session, error) {
	s, ok := m.sessions[id]
	if !ok {
		return nil, domain.ErrSessionNotFound
	}
	return s, nil
}

func (m *mockStore) GetByBranch(projectPath, branch string) (*domain.Session, error) {
	key := projectPath + ":" + branch
	s, ok := m.byBranch[key]
	if !ok {
		return nil, domain.ErrSessionNotFound
	}
	return s, nil
}

func (m *mockStore) List(_ domain.ListOptions) ([]domain.SessionSummary, error) { return nil, nil }
func (m *mockStore) Delete(_ domain.SessionID) error                            { return nil }
func (m *mockStore) Close() error                                               { return nil }

func (m *mockStore) AddLink(sessionID domain.SessionID, link domain.Link) error {
	key := string(link.LinkType) + ":" + link.Ref
	s, ok := m.sessions[sessionID]
	if !ok {
		return domain.ErrSessionNotFound
	}
	summary := domain.SessionSummary{
		ID:       s.ID,
		Provider: s.Provider,
		Branch:   s.Branch,
	}
	m.links[key] = append(m.links[key], summary)
	return nil
}

func (m *mockStore) GetByLink(linkType domain.LinkType, ref string) ([]domain.SessionSummary, error) {
	key := string(linkType) + ":" + ref
	summaries, ok := m.links[key]
	if !ok || len(summaries) == 0 {
		return nil, domain.ErrSessionNotFound
	}
	return summaries, nil
}

// mockPlatform for comment tests.
type mockPlatform struct {
	name       domain.PlatformName
	prByBranch map[string]*domain.PullRequest
	comments   map[int][]domain.PRComment
	added      []addedComment
	updated    []updatedComment
}

type addedComment struct {
	body     string
	prNumber int
}

type updatedComment struct {
	body      string
	commentID int64
}

func newMockPlatform() *mockPlatform {
	return &mockPlatform{
		name:       domain.PlatformGitHub,
		prByBranch: make(map[string]*domain.PullRequest),
		comments:   make(map[int][]domain.PRComment),
	}
}

func (m *mockPlatform) Name() domain.PlatformName { return m.name }

func (m *mockPlatform) GetPRForBranch(branch string) (*domain.PullRequest, error) {
	pr, ok := m.prByBranch[branch]
	if !ok {
		return nil, domain.ErrPRNotFound
	}
	return pr, nil
}

func (m *mockPlatform) GetPR(number int) (*domain.PullRequest, error) {
	for _, pr := range m.prByBranch {
		if pr.Number == number {
			return pr, nil
		}
	}
	return nil, domain.ErrPRNotFound
}

func (m *mockPlatform) ListPRsForBranch(_ string) ([]domain.PullRequest, error) {
	return nil, nil
}

func (m *mockPlatform) AddComment(prNumber int, body string) error {
	m.added = append(m.added, addedComment{prNumber: prNumber, body: body})
	return nil
}

func (m *mockPlatform) UpdateComment(commentID int64, body string) error {
	m.updated = append(m.updated, updatedComment{commentID: commentID, body: body})
	return nil
}

func (m *mockPlatform) ListComments(prNumber int) ([]domain.PRComment, error) {
	return m.comments[prNumber], nil
}

func commentTestFactory(t *testing.T, store *mockStore, plat *mockPlatform) (*cmdutil.Factory, *iostreams.IOStreams, string) {
	t.Helper()
	ios := iostreams.Test()
	repoDir := testutil.InitTestRepo(t)

	if store == nil {
		store = newMockStore()
	}
	gitClient := git.NewClient(repoDir)

	f := &cmdutil.Factory{
		IOStreams:    ios,
		GitFunc:      func() (*git.Client, error) { return gitClient, nil },
		StoreFunc:    func() (domain.Store, error) { return store, nil },
		PlatformFunc: func() (domain.Platform, error) { return plat, nil },
	}

	return f, ios, repoDir
}

func TestNewCmdComment_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdComment(f)

	flags := []string{"session", "pr"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}

func TestComment_newComment(t *testing.T) {
	store := newMockStore()
	plat := newMockPlatform()

	f, ios, repoDir := commentTestFactory(t, store, plat)

	// Create session for branch
	gitClient := git.NewClient(repoDir)
	topLevel, _ := gitClient.TopLevel()
	branchName, _ := gitClient.CurrentBranch()

	// Set up PR matching actual branch name
	plat.prByBranch[branchName] = &domain.PullRequest{
		Number: 10,
		Title:  "Test PR",
		Branch: branchName,
	}

	session := testutil.NewSession("comment-test-1")
	session.Provider = domain.ProviderClaudeCode
	session.ProjectPath = topLevel
	session.Branch = branchName
	session.Summary = "Added new feature"
	session.FileChanges = []domain.FileChange{
		{FilePath: "main.go", ChangeType: domain.ChangeModified},
	}
	_ = store.Save(session)

	opts := &Options{
		IO:      ios,
		Factory: f,
	}

	err := runComment(opts)
	if err != nil {
		t.Fatalf("runComment() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Posted aisync comment on PR #10") {
		t.Errorf("expected 'Posted' message, got: %s", output)
	}

	// Verify comment was added
	if len(plat.added) != 1 {
		t.Fatalf("expected 1 added comment, got %d", len(plat.added))
	}
	if plat.added[0].prNumber != 10 {
		t.Errorf("expected PR #10, got #%d", plat.added[0].prNumber)
	}
	if !strings.Contains(plat.added[0].body, aisyncMarker) {
		t.Error("expected aisync marker in comment body")
	}
	if !strings.Contains(plat.added[0].body, "Added new feature") {
		t.Error("expected summary in comment body")
	}
}

func TestComment_updateExisting(t *testing.T) {
	store := newMockStore()
	plat := newMockPlatform()

	f, ios, repoDir := commentTestFactory(t, store, plat)

	gitClient := git.NewClient(repoDir)
	topLevel, _ := gitClient.TopLevel()
	branchName, _ := gitClient.CurrentBranch()

	session := testutil.NewSession("comment-update-1")
	session.Provider = domain.ProviderClaudeCode
	session.ProjectPath = topLevel
	session.Branch = branchName
	_ = store.Save(session)

	// Pre-existing aisync comment
	plat.comments[5] = []domain.PRComment{
		{
			ID:   123,
			Body: aisyncMarker + "\nOld comment",
		},
	}

	opts := &Options{
		IO:      ios,
		Factory: f,
		PRFlag:  5,
	}

	err := runComment(opts)
	if err != nil {
		t.Fatalf("runComment() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Updated aisync comment on PR #5") {
		t.Errorf("expected 'Updated' message, got: %s", output)
	}

	// Verify update was called, not add
	if len(plat.added) != 0 {
		t.Error("expected no new comments to be added")
	}
	if len(plat.updated) != 1 {
		t.Fatalf("expected 1 updated comment, got %d", len(plat.updated))
	}
	if plat.updated[0].commentID != 123 {
		t.Errorf("expected comment ID 123, got %d", plat.updated[0].commentID)
	}
}

func TestComment_withSessionFlag(t *testing.T) {
	store := newMockStore()
	plat := newMockPlatform()

	session := testutil.NewSession("explicit-session")
	session.Provider = domain.ProviderOpenCode
	session.Summary = "Explicit session"
	_ = store.Save(session)

	f, ios, _ := commentTestFactory(t, store, plat)

	opts := &Options{
		IO:          ios,
		Factory:     f,
		SessionFlag: "explicit-session",
		PRFlag:      7,
	}

	err := runComment(opts)
	if err != nil {
		t.Fatalf("runComment() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Posted aisync comment on PR #7") {
		t.Errorf("expected 'Posted' message, got: %s", output)
	}

	if len(plat.added) != 1 {
		t.Fatalf("expected 1 added comment, got %d", len(plat.added))
	}
	if !strings.Contains(plat.added[0].body, "explicit-session") {
		t.Error("expected session ID in comment body")
	}
}

func TestComment_noPR(t *testing.T) {
	store := newMockStore()
	plat := newMockPlatform()
	// No PRs set up

	f, ios, _ := commentTestFactory(t, store, plat)

	opts := &Options{
		IO:      ios,
		Factory: f,
	}

	err := runComment(opts)
	if err == nil {
		t.Fatal("expected error when no PR found")
	}
	if !strings.Contains(err.Error(), "no open PR found") {
		t.Errorf("expected 'no open PR found' error, got: %v", err)
	}
}

func TestBuildCommentBody(t *testing.T) {
	session := &domain.Session{
		ID:       "test-session-123",
		Provider: domain.ProviderClaudeCode,
		Branch:   "feature-branch",
		Summary:  "Implemented auth module",
		TokenUsage: domain.TokenUsage{
			InputTokens:  1000,
			OutputTokens: 500,
			TotalTokens:  1500,
		},
		Messages: make([]domain.Message, 5),
		FileChanges: []domain.FileChange{
			{FilePath: "auth.go", ChangeType: domain.ChangeCreated},
			{FilePath: "main.go", ChangeType: domain.ChangeModified},
		},
	}

	body := buildCommentBody(session)

	checks := []struct {
		name     string
		expected string
	}{
		{"marker", aisyncMarker},
		{"session ID", "test-session-123"},
		{"provider", "claude-code"},
		{"branch", "feature-branch"},
		{"summary", "Implemented auth module"},
		{"input tokens", "| Input  | 1000 |"},
		{"output tokens", "| Output | 500 |"},
		{"total tokens", "| Total  | 1500 |"},
		{"messages count", "**Messages:** 5"},
		{"file auth.go", "`auth.go` (created)"},
		{"file main.go", "`main.go` (modified)"},
		{"attribution", "aisync"},
	}

	for _, check := range checks {
		if !strings.Contains(body, check.expected) {
			t.Errorf("expected %s (%q) in body, got:\n%s", check.name, check.expected, body)
		}
	}
}

func TestBuildCommentBody_minimal(t *testing.T) {
	session := &domain.Session{
		ID:       "minimal-session",
		Provider: domain.ProviderOpenCode,
		Branch:   "main",
	}

	body := buildCommentBody(session)

	if !strings.Contains(body, aisyncMarker) {
		t.Error("expected aisync marker")
	}
	if !strings.Contains(body, "minimal-session") {
		t.Error("expected session ID")
	}
	// No token table for zero tokens
	if strings.Contains(body, "Token Usage") {
		t.Error("should not show token usage for zero tokens")
	}
	// No file changes section
	if strings.Contains(body, "Files Changed") {
		t.Error("should not show files section when none changed")
	}
}
