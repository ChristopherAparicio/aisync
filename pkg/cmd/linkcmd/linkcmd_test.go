package linkcmd

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

// mockStore for link tests.
type mockStore struct {
	sessions map[session.ID]*session.Session
	byBranch map[string]*session.Session
	links    []session.Link
}

func newMockStore() *mockStore {
	return &mockStore{
		sessions: make(map[session.ID]*session.Session),
		byBranch: make(map[string]*session.Session),
	}
}

func (m *mockStore) Save(s *session.Session) error {
	m.sessions[s.ID] = s
	key := s.ProjectPath + ":" + s.Branch
	m.byBranch[key] = s
	return nil
}

func (m *mockStore) Get(id session.ID) (*session.Session, error) {
	s, ok := m.sessions[id]
	if !ok {
		return nil, session.ErrSessionNotFound
	}
	return s, nil
}

func (m *mockStore) GetLatestByBranch(projectPath, branch string) (*session.Session, error) {
	key := projectPath + ":" + branch
	s, ok := m.byBranch[key]
	if !ok {
		return nil, session.ErrSessionNotFound
	}
	return s, nil
}
func (m *mockStore) CountByBranch(_, _ string) (int, error) { return 0, nil }

func (m *mockStore) List(_ session.ListOptions) ([]session.Summary, error) { return nil, nil }
func (m *mockStore) Delete(_ session.ID) error                             { return nil }

func (m *mockStore) AddLink(_ session.ID, link session.Link) error {
	m.links = append(m.links, link)
	return nil
}

func (m *mockStore) GetByLink(_ session.LinkType, _ string) ([]session.Summary, error) {
	return nil, session.ErrSessionNotFound
}

func (m *mockStore) DeleteOlderThan(_ time.Time) (int, error)       { return 0, nil }
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

func testFactory(t *testing.T, store *mockStore) (*cmdutil.Factory, *iostreams.IOStreams, string) {
	t.Helper()
	ios := iostreams.Test()
	repoDir := testutil.InitTestRepo(t)
	gitClient := git.NewClient(repoDir)

	if store == nil {
		store = newMockStore()
	}

	f := &cmdutil.Factory{
		IOStreams: ios,
		GitFunc:   func() (*git.Client, error) { return gitClient, nil },
		StoreFunc: func() (storage.Store, error) {
			return store, nil
		},
		SessionServiceFunc: func() (*service.SessionService, error) {
			return service.NewSessionService(service.SessionServiceConfig{
				Store: store,
				Git:   gitClient,
			}), nil
		},
	}

	return f, ios, repoDir
}

func TestLink_withPR(t *testing.T) {
	store := newMockStore()

	sess := testutil.NewSession("link-001")
	_ = store.Save(sess)

	f, ios, _ := testFactory(t, store)

	opts := &Options{
		IO:          ios,
		Factory:     f,
		SessionFlag: "link-001",
		PRFlag:      42,
	}

	err := runLink(opts)
	if err != nil {
		t.Fatalf("runLink() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "PR #42") {
		t.Error("expected 'PR #42' in output")
	}

	if len(store.links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(store.links))
	}
	if store.links[0].LinkType != session.LinkPR {
		t.Errorf("link type = %q, want %q", store.links[0].LinkType, session.LinkPR)
	}
	if store.links[0].Ref != "42" {
		t.Errorf("link ref = %q, want %q", store.links[0].Ref, "42")
	}
}

func TestLink_withCommit(t *testing.T) {
	store := newMockStore()

	sess := testutil.NewSession("link-002")
	_ = store.Save(sess)

	f, ios, _ := testFactory(t, store)

	opts := &Options{
		IO:          ios,
		Factory:     f,
		SessionFlag: "link-002",
		CommitFlag:  "abc1234",
	}

	err := runLink(opts)
	if err != nil {
		t.Fatalf("runLink() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "commit abc1234") {
		t.Error("expected 'commit abc1234' in output")
	}

	if len(store.links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(store.links))
	}
	if store.links[0].LinkType != session.LinkCommit {
		t.Errorf("link type = %q, want %q", store.links[0].LinkType, session.LinkCommit)
	}
}

func TestLink_byBranch(t *testing.T) {
	store := newMockStore()
	f, ios, repoDir := testFactory(t, store)

	// Store a session matching the repo's branch
	gitClient := git.NewClient(repoDir)
	topLevel, _ := gitClient.TopLevel()
	branch, _ := gitClient.CurrentBranch()

	sess := testutil.NewSession("link-branch")
	sess.ProjectPath = topLevel
	sess.Branch = branch
	_ = store.Save(sess)

	opts := &Options{
		IO:      ios,
		Factory: f,
		PRFlag:  99,
	}

	err := runLink(opts)
	if err != nil {
		t.Fatalf("runLink() error = %v", err)
	}

	output := ios.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "link-branch") {
		t.Error("expected session ID in output")
	}
}

func TestLink_noTarget(t *testing.T) {
	f, ios, _ := testFactory(t, nil)

	opts := &Options{
		IO:          ios,
		Factory:     f,
		SessionFlag: "some-id",
	}

	err := runLink(opts)
	if err == nil {
		t.Fatal("expected error when no --pr or --commit specified")
	}
}

func TestLink_sessionNotFound(t *testing.T) {
	store := newMockStore()
	f, ios, _ := testFactory(t, store)

	opts := &Options{
		IO:      ios,
		Factory: f,
		PRFlag:  42,
		// No --session flag, and no session for current branch
	}

	err := runLink(opts)
	if err == nil {
		t.Fatal("expected error when session not found")
	}
}

func TestNewCmdLink_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdLink(f)

	flags := []string{"session", "pr", "commit", "auto"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag", name)
		}
	}
}
