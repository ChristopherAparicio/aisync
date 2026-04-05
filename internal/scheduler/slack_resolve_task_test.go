package scheduler

import (
	"context"
	"errors"
	"log"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// ── SlackResolveTask Tests ──

func TestSlackResolveTask_Name(t *testing.T) {
	task := NewSlackResolveTask(SlackResolveConfig{})
	if task.Name() != "slack_resolve" {
		t.Errorf("Name() = %q, want %q", task.Name(), "slack_resolve")
	}
}

func TestSlackResolveTask_NilStore(t *testing.T) {
	task := NewSlackResolveTask(SlackResolveConfig{
		Resolver: &mockSlackResolver{},
		Logger:   log.Default(),
	})
	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSlackResolveTask_NilResolver(t *testing.T) {
	task := NewSlackResolveTask(SlackResolveConfig{
		Store:  &mockSlackResolveStore{},
		Logger: log.Default(),
	})
	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSlackResolveTask_ResolvesUsers(t *testing.T) {
	store := &mockSlackResolveStore{
		users: []*session.User{
			{ID: "u1", Name: "Alice", Email: "alice@example.com", Kind: "human"},
			{ID: "u2", Name: "Bob", Email: "bob@example.com", Kind: "human"},
		},
	}
	resolver := &mockSlackResolver{
		results: map[string]*SlackUserInfo{
			"alice@example.com": {ID: "U001", Name: "alice", RealName: "Alice Smith"},
			"bob@example.com":   {ID: "U002", Name: "bob", RealName: "Bob Jones"},
		},
	}

	task := NewSlackResolveTask(SlackResolveConfig{
		Store:    store,
		Resolver: resolver,
		Logger:   log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(store.slackUpdates) != 2 {
		t.Fatalf("expected 2 slack updates, got %d", len(store.slackUpdates))
	}

	// Verify alice update.
	if upd, ok := store.slackUpdates["u1"]; ok {
		if upd.slackID != "U001" {
			t.Errorf("alice slackID = %q, want U001", upd.slackID)
		}
		if upd.slackName != "alice" {
			t.Errorf("alice slackName = %q, want alice", upd.slackName)
		}
	} else {
		t.Error("missing slack update for alice (u1)")
	}
}

func TestSlackResolveTask_SkipsUsersWithSlackID(t *testing.T) {
	store := &mockSlackResolveStore{
		users: []*session.User{
			{ID: "u1", Name: "Alice", Email: "alice@example.com", Kind: "human", SlackID: "U001"}, // already has slack_id
		},
	}
	resolver := &mockSlackResolver{
		results: map[string]*SlackUserInfo{
			"alice@example.com": {ID: "U001", Name: "alice"},
		},
	}

	task := NewSlackResolveTask(SlackResolveConfig{
		Store:    store,
		Resolver: resolver,
		Logger:   log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(store.slackUpdates) != 0 {
		t.Errorf("expected 0 updates (user already has slack_id), got %d", len(store.slackUpdates))
	}
	if resolver.lookupCount != 0 {
		t.Errorf("expected 0 lookups, got %d", resolver.lookupCount)
	}
}

func TestSlackResolveTask_SkipsMachineAccounts(t *testing.T) {
	store := &mockSlackResolveStore{
		users: []*session.User{
			{ID: "u1", Name: "ci-bot", Email: "ci@example.com", Kind: "machine"},
		},
	}
	resolver := &mockSlackResolver{}

	task := NewSlackResolveTask(SlackResolveConfig{
		Store:    store,
		Resolver: resolver,
		Logger:   log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resolver.lookupCount != 0 {
		t.Errorf("expected 0 lookups for machine account, got %d", resolver.lookupCount)
	}
}

func TestSlackResolveTask_SkipsUsersWithoutEmail(t *testing.T) {
	store := &mockSlackResolveStore{
		users: []*session.User{
			{ID: "u1", Name: "NoEmail", Kind: "human"}, // no email
		},
	}
	resolver := &mockSlackResolver{}

	task := NewSlackResolveTask(SlackResolveConfig{
		Store:    store,
		Resolver: resolver,
		Logger:   log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resolver.lookupCount != 0 {
		t.Errorf("expected 0 lookups for user without email, got %d", resolver.lookupCount)
	}
}

func TestSlackResolveTask_UserNotFoundInSlack(t *testing.T) {
	store := &mockSlackResolveStore{
		users: []*session.User{
			{ID: "u1", Name: "Alice", Email: "alice@example.com", Kind: "human"},
		},
	}
	resolver := &mockSlackResolver{
		results: map[string]*SlackUserInfo{}, // empty — no Slack match
	}

	task := NewSlackResolveTask(SlackResolveConfig{
		Store:    store,
		Resolver: resolver,
		Logger:   log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(store.slackUpdates) != 0 {
		t.Errorf("expected 0 updates (user not in Slack), got %d", len(store.slackUpdates))
	}
}

func TestSlackResolveTask_LookupError_NonFatal(t *testing.T) {
	store := &mockSlackResolveStore{
		users: []*session.User{
			{ID: "u1", Name: "Alice", Email: "alice@example.com", Kind: "human"},
			{ID: "u2", Name: "Bob", Email: "bob@example.com", Kind: "human"},
		},
	}
	resolver := &mockSlackResolver{
		results: map[string]*SlackUserInfo{
			"bob@example.com": {ID: "U002", Name: "bob"},
		},
		errorForEmail: map[string]error{
			"alice@example.com": errors.New("rate limited"),
		},
	}

	task := NewSlackResolveTask(SlackResolveConfig{
		Store:    store,
		Resolver: resolver,
		Logger:   log.Default(),
	})

	// Should not return error — individual lookup failures are non-fatal.
	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Bob should still be resolved.
	if len(store.slackUpdates) != 1 {
		t.Fatalf("expected 1 update (bob only), got %d", len(store.slackUpdates))
	}
	if _, ok := store.slackUpdates["u2"]; !ok {
		t.Error("expected bob (u2) to be updated")
	}
}

func TestSlackResolveTask_ListUsersError(t *testing.T) {
	store := &mockSlackResolveStore{
		usersErr: errors.New("db down"),
	}
	resolver := &mockSlackResolver{}

	task := NewSlackResolveTask(SlackResolveConfig{
		Store:    store,
		Resolver: resolver,
		Logger:   log.Default(),
	})

	err := task.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when ListUsers fails")
	}
}

func TestSlackResolveTask_ContextCancelled(t *testing.T) {
	store := &mockSlackResolveStore{
		users: []*session.User{
			{ID: "u1", Name: "Alice", Email: "alice@example.com", Kind: "human"},
		},
	}
	resolver := &mockSlackResolver{}

	task := NewSlackResolveTask(SlackResolveConfig{
		Store:    store,
		Resolver: resolver,
		Logger:   log.Default(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := task.Run(ctx)
	if err == nil {
		t.Error("expected context cancellation error")
	}
}

func TestSlackResolveTask_FallbackToRealName(t *testing.T) {
	store := &mockSlackResolveStore{
		users: []*session.User{
			{ID: "u1", Name: "Alice", Email: "alice@example.com", Kind: "human"},
		},
	}
	resolver := &mockSlackResolver{
		results: map[string]*SlackUserInfo{
			"alice@example.com": {ID: "U001", Name: "", RealName: "Alice Smith"}, // no display_name
		},
	}

	task := NewSlackResolveTask(SlackResolveConfig{
		Store:    store,
		Resolver: resolver,
		Logger:   log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if upd, ok := store.slackUpdates["u1"]; ok {
		if upd.slackName != "Alice Smith" {
			t.Errorf("slackName = %q, want 'Alice Smith' (fallback to RealName)", upd.slackName)
		}
	} else {
		t.Error("expected slack update for u1")
	}
}

// ── Mock types ──

type mockSlackResolver struct {
	results       map[string]*SlackUserInfo
	errorForEmail map[string]error
	lookupCount   int
}

func (m *mockSlackResolver) LookupByEmail(email string) (*SlackUserInfo, error) {
	m.lookupCount++
	if m.errorForEmail != nil {
		if err, ok := m.errorForEmail[email]; ok {
			return nil, err
		}
	}
	if m.results != nil {
		if info, ok := m.results[email]; ok {
			return info, nil
		}
	}
	return nil, nil // not found
}

type slackUpdate struct {
	slackID   string
	slackName string
}

type mockSlackResolveStore struct {
	storage.Store // embed to satisfy interface

	users        []*session.User
	usersErr     error
	slackUpdates map[string]slackUpdate // keyed by user ID
}

func (m *mockSlackResolveStore) ListUsers() ([]*session.User, error) {
	return m.users, m.usersErr
}

func (m *mockSlackResolveStore) UpdateUserSlack(id session.ID, slackID, slackName string) error {
	if m.slackUpdates == nil {
		m.slackUpdates = make(map[string]slackUpdate)
	}
	m.slackUpdates[string(id)] = slackUpdate{slackID: slackID, slackName: slackName}
	return nil
}
