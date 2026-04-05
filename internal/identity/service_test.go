package identity

import (
	"fmt"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// --- Mock SlackClient ---

type mockSlackClient struct {
	members []SlackMember
	err     error
}

func (m *mockSlackClient) ListMembers() ([]SlackMember, error) {
	return m.members, m.err
}

// --- Mock UserStore ---
// Minimal mock implementing only what Service needs.

type mockUserStore struct {
	users            []*session.User
	listErr          error
	updateSlackCalls []updateSlackCall
}

type updateSlackCall struct {
	ID        session.ID
	SlackID   string
	SlackName string
}

func (m *mockUserStore) SaveUser(_ *session.User) error                    { return nil }
func (m *mockUserStore) GetUser(_ session.ID) (*session.User, error)       { return nil, nil }
func (m *mockUserStore) GetUserByEmail(_ string) (*session.User, error)    { return nil, nil }
func (m *mockUserStore) ListUsersByKind(_ string) ([]*session.User, error) { return nil, nil }
func (m *mockUserStore) UpdateUserKind(_ session.ID, _ string) error       { return nil }
func (m *mockUserStore) UpdateUserRole(_ session.ID, _ string) error       { return nil }
func (m *mockUserStore) OwnerStats(_ string, _, _ time.Time) ([]session.OwnerStat, error) {
	return nil, nil
}

func (m *mockUserStore) ListUsers() ([]*session.User, error) {
	return m.users, m.listErr
}

func (m *mockUserStore) UpdateUserSlack(id session.ID, slackID, slackName string) error {
	m.updateSlackCalls = append(m.updateSlackCalls, updateSlackCall{id, slackID, slackName})
	return nil
}

// --- Tests ---

func TestNewService_NilWithoutClient(t *testing.T) {
	svc := NewService(nil, &mockUserStore{}, ServiceConfig{})
	if svc != nil {
		t.Error("expected nil service without slack client")
	}
}

func TestNewService_NilWithoutStore(t *testing.T) {
	svc := NewService(&mockSlackClient{}, nil, ServiceConfig{})
	if svc != nil {
		t.Error("expected nil service without store")
	}
}

func TestSyncSlackMembers_NoGitUsers(t *testing.T) {
	slack := &mockSlackClient{
		members: []SlackMember{
			{ID: "U001", RealName: "John Doe", Email: "john@company.com"},
		},
	}
	store := &mockUserStore{users: []*session.User{}}

	svc := NewService(slack, store, ServiceConfig{})
	result, err := svc.SyncSlackMembers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SlackMembersFound != 1 {
		t.Errorf("expected 1 slack member, got %d", result.SlackMembersFound)
	}
	if result.GitUsersTotal != 0 {
		t.Errorf("expected 0 git users, got %d", result.GitUsersTotal)
	}
	if len(result.NewSuggestions) != 0 {
		t.Errorf("expected 0 suggestions, got %d", len(result.NewSuggestions))
	}
}

func TestSyncSlackMembers_ExactEmailMatch(t *testing.T) {
	slack := &mockSlackClient{
		members: []SlackMember{
			{ID: "U001", RealName: "John Doe", Email: "john@company.com"},
		},
	}
	store := &mockUserStore{
		users: []*session.User{
			{ID: "git1", Name: "John", Email: "john@company.com", Kind: session.UserKindHuman},
		},
	}

	svc := NewService(slack, store, ServiceConfig{})
	result, err := svc.SyncSlackMembers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.NewSuggestions) != 1 {
		t.Fatalf("expected 1 suggestion, got %d", len(result.NewSuggestions))
	}

	s := result.NewSuggestions[0]
	if s.Score != 1.0 {
		t.Errorf("expected score 1.0, got %f", s.Score)
	}
	if s.Confidence != ConfidenceExact {
		t.Errorf("expected confidence exact, got %s", s.Confidence)
	}
	if s.Status != StatusPending {
		t.Errorf("expected status pending, got %s", s.Status)
	}
}

func TestSyncSlackMembers_FuzzyNameMatch(t *testing.T) {
	slack := &mockSlackClient{
		members: []SlackMember{
			{ID: "U001", RealName: "Christophe Aparicio", Email: "christophe@company.com"},
		},
	}
	store := &mockUserStore{
		users: []*session.User{
			{ID: "git1", Name: "christophe.aparicio", Email: "chris@local.dev", Kind: session.UserKindHuman},
		},
	}

	svc := NewService(slack, store, ServiceConfig{MinConfidence: ConfidenceLow})
	result, err := svc.SyncSlackMembers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.NewSuggestions) != 1 {
		t.Fatalf("expected 1 suggestion, got %d", len(result.NewSuggestions))
	}

	s := result.NewSuggestions[0]
	if s.Score < 0.5 {
		t.Errorf("expected score >= 0.5 for fuzzy match, got %f", s.Score)
	}
}

func TestSyncSlackMembers_SkipAlreadyLinked(t *testing.T) {
	slack := &mockSlackClient{
		members: []SlackMember{
			{ID: "U001", RealName: "John Doe", Email: "john@company.com"},
		},
	}
	store := &mockUserStore{
		users: []*session.User{
			{ID: "git1", Name: "John Doe", Email: "john@company.com", Kind: session.UserKindHuman, SlackID: "U001"},
		},
	}

	svc := NewService(slack, store, ServiceConfig{})
	result, err := svc.SyncSlackMembers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.AlreadyLinked != 1 {
		t.Errorf("expected 1 already linked, got %d", result.AlreadyLinked)
	}
	if len(result.NewSuggestions) != 0 {
		t.Errorf("expected 0 suggestions for already linked, got %d", len(result.NewSuggestions))
	}
}

func TestSyncSlackMembers_SkipMachineAccounts(t *testing.T) {
	slack := &mockSlackClient{
		members: []SlackMember{
			{ID: "U001", RealName: "John Doe", Email: "john@company.com"},
		},
	}
	store := &mockUserStore{
		users: []*session.User{
			{ID: "git1", Name: "github-actions", Email: "github-actions@noreply.github.com", Kind: session.UserKindMachine},
		},
	}

	svc := NewService(slack, store, ServiceConfig{})
	result, err := svc.SyncSlackMembers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Unmatched != 1 {
		t.Errorf("expected 1 unmatched (machine), got %d", result.Unmatched)
	}
	if len(result.NewSuggestions) != 0 {
		t.Errorf("expected 0 suggestions for machine account, got %d", len(result.NewSuggestions))
	}
}

func TestSyncSlackMembers_AutoLink(t *testing.T) {
	slack := &mockSlackClient{
		members: []SlackMember{
			{ID: "U001", RealName: "John Doe", Email: "john@company.com"},
		},
	}
	store := &mockUserStore{
		users: []*session.User{
			{ID: "git1", Name: "John Doe", Email: "john@company.com", Kind: session.UserKindHuman},
		},
	}

	svc := NewService(slack, store, ServiceConfig{AutoLinkExact: true})
	result, err := svc.SyncSlackMembers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.AutoLinked != 1 {
		t.Errorf("expected 1 auto-linked, got %d", result.AutoLinked)
	}
	if len(store.updateSlackCalls) != 1 {
		t.Fatalf("expected 1 UpdateUserSlack call, got %d", len(store.updateSlackCalls))
	}
	call := store.updateSlackCalls[0]
	if call.ID != "git1" || call.SlackID != "U001" || call.SlackName != "John Doe" {
		t.Errorf("unexpected UpdateUserSlack call: %+v", call)
	}
}

func TestSyncSlackMembers_FiltersBots(t *testing.T) {
	slack := &mockSlackClient{
		members: []SlackMember{
			{ID: "U001", RealName: "John Doe", Email: "john@company.com"},
			{ID: "U002", RealName: "Bot User", IsBot: true},
			{ID: "U003", RealName: "Left User", Deleted: true, Email: "left@company.com"},
		},
	}
	store := &mockUserStore{
		users: []*session.User{
			{ID: "git1", Name: "John Doe", Email: "john@company.com", Kind: session.UserKindHuman},
		},
	}

	svc := NewService(slack, store, ServiceConfig{})
	result, err := svc.SyncSlackMembers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only non-bot, non-deleted members count as active
	if result.SlackMembersFound != 1 {
		t.Errorf("expected 1 active slack member, got %d", result.SlackMembersFound)
	}
}

func TestSyncSlackMembers_SortsByScore(t *testing.T) {
	slack := &mockSlackClient{
		members: []SlackMember{
			{ID: "U001", RealName: "Alice Smith", Email: "alice@company.com"},
			{ID: "U002", RealName: "John Doe", Email: "john@company.com"},
		},
	}
	store := &mockUserStore{
		users: []*session.User{
			{ID: "git1", Name: "alice", Email: "alice@local.dev", Kind: session.UserKindHuman},
			{ID: "git2", Name: "John Doe", Email: "john@company.com", Kind: session.UserKindHuman},
		},
	}

	svc := NewService(slack, store, ServiceConfig{})
	result, err := svc.SyncSlackMembers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.NewSuggestions) < 2 {
		t.Fatalf("expected at least 2 suggestions, got %d", len(result.NewSuggestions))
	}

	// The exact email match (John) should be first (score 1.0)
	if result.NewSuggestions[0].Score < result.NewSuggestions[1].Score {
		t.Error("expected suggestions sorted by score descending")
	}
}

func TestSyncSlackMembers_SlackError(t *testing.T) {
	slack := &mockSlackClient{
		err: fmt.Errorf("connection refused"),
	}
	store := &mockUserStore{}

	svc := NewService(slack, store, ServiceConfig{})
	_, err := svc.SyncSlackMembers()
	if err == nil {
		t.Error("expected error when slack client fails")
	}
}

func TestSyncSlackMembers_StoreError(t *testing.T) {
	slack := &mockSlackClient{
		members: []SlackMember{
			{ID: "U001", RealName: "John Doe"},
		},
	}
	store := &mockUserStore{
		listErr: fmt.Errorf("database locked"),
	}

	svc := NewService(slack, store, ServiceConfig{})
	_, err := svc.SyncSlackMembers()
	if err == nil {
		t.Error("expected error when store fails")
	}
}

func TestSyncSlackMembers_NoDoubleLink(t *testing.T) {
	// Two git users that could match the same Slack member — only the better match should get it
	slack := &mockSlackClient{
		members: []SlackMember{
			{ID: "U001", RealName: "John Doe", Email: "john@company.com"},
		},
	}
	store := &mockUserStore{
		users: []*session.User{
			{ID: "git1", Name: "John Doe", Email: "john@company.com", Kind: session.UserKindHuman},
			{ID: "git2", Name: "John D", Email: "johnd@local.dev", Kind: session.UserKindHuman},
		},
	}

	svc := NewService(slack, store, ServiceConfig{AutoLinkExact: true})
	result, err := svc.SyncSlackMembers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// git1 gets the exact match and auto-link
	if result.AutoLinked != 1 {
		t.Errorf("expected 1 auto-linked, got %d", result.AutoLinked)
	}

	// git2 should NOT get U001 since it's already taken
	for _, s := range result.NewSuggestions {
		if s.GitUserID == "git2" && s.SlackID == "U001" && s.Status == StatusApproved {
			t.Error("git2 should not be linked to U001 (already taken by git1)")
		}
	}
}

func TestLinkUser(t *testing.T) {
	store := &mockUserStore{}
	slack := &mockSlackClient{}
	svc := NewService(slack, store, ServiceConfig{})

	err := svc.LinkUser("git1", "U001", "John Doe")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(store.updateSlackCalls) != 1 {
		t.Fatalf("expected 1 UpdateUserSlack call, got %d", len(store.updateSlackCalls))
	}
	call := store.updateSlackCalls[0]
	if call.ID != "git1" || call.SlackID != "U001" || call.SlackName != "John Doe" {
		t.Errorf("unexpected call: %+v", call)
	}
}

func TestNilService(t *testing.T) {
	var svc *Service
	_, err := svc.SyncSlackMembers()
	if err == nil {
		t.Error("expected error from nil service")
	}
	err = svc.LinkUser("x", "y", "z")
	if err == nil {
		t.Error("expected error from nil service")
	}
}
