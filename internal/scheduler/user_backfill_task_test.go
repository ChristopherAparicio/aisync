package scheduler

import (
	"context"
	"log"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/config"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
)

func TestUserKindBackfillTask_Name(t *testing.T) {
	task := NewUserKindBackfillTask(UserKindBackfillConfig{})
	if got := task.Name(); got != "user_kind_backfill" {
		t.Errorf("Name() = %q, want %q", got, "user_kind_backfill")
	}
}

func TestUserKindBackfillTask_Run(t *testing.T) {
	store := &mockUserStore{
		users: []*session.User{
			{ID: "u1", Name: "Human", Email: "john@company.com", Kind: session.UserKindUnknown},
			{ID: "u2", Name: "Bot", Email: "ci@deploy.io", Kind: session.UserKindUnknown},
			{ID: "u3", Name: "Already", Email: "jane@company.com", Kind: session.UserKindHuman},
		},
	}

	cfg, _ := config.New("", "")

	task := NewUserKindBackfillTask(UserKindBackfillConfig{
		Store:  store,
		Config: cfg,
		Logger: log.Default(),
	})

	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// u1: unknown → human (john@company.com doesn't match machine patterns)
	// u2: unknown → machine (ci@* matches)
	// u3: human → human (no change)
	if len(store.kindUpdates) != 2 {
		t.Errorf("expected 2 kind updates, got %d", len(store.kindUpdates))
	}
}

func TestUserKindBackfillTask_NoConfig(t *testing.T) {
	store := &mockUserStore{
		users: []*session.User{
			{ID: "u1", Name: "Test", Email: "test@example.com", Kind: session.UserKindUnknown},
		},
	}

	task := NewUserKindBackfillTask(UserKindBackfillConfig{
		Store:  store,
		Config: nil,
		Logger: log.Default(),
	})

	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// With nil config, no machine patterns → unknown email becomes human
	if len(store.kindUpdates) != 1 {
		t.Errorf("expected 1 kind update, got %d", len(store.kindUpdates))
	}
}

func TestUserKindBackfillTask_EmptyUsers(t *testing.T) {
	store := &mockUserStore{users: nil}

	task := NewUserKindBackfillTask(UserKindBackfillConfig{
		Store:  store,
		Logger: log.Default(),
	})

	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(store.kindUpdates) != 0 {
		t.Errorf("expected 0 updates, got %d", len(store.kindUpdates))
	}
}

// mockUserStore wraps testutil.MockStore with user-specific tracking.
type mockUserStore struct {
	testutil.MockStore
	users       []*session.User
	kindUpdates []kindUpdate
}

type kindUpdate struct {
	id   session.ID
	kind string
}

func (m *mockUserStore) ListUsers() ([]*session.User, error) {
	return m.users, nil
}

func (m *mockUserStore) UpdateUserKind(id session.ID, kind string) error {
	m.kindUpdates = append(m.kindUpdates, kindUpdate{id: id, kind: kind})
	return nil
}
