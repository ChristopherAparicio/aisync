package scheduler

import (
	"context"
	"errors"
	"log"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/notification"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// ── PersonalDigestTask Tests ──

func TestPersonalDigestTask_Name(t *testing.T) {
	task := NewPersonalDigestTask(PersonalDigestConfig{})
	if task.Name() != "personal_digest" {
		t.Errorf("Name() = %q, want %q", task.Name(), "personal_digest")
	}
}

func TestPersonalDigestTask_NilNotifService(t *testing.T) {
	task := NewPersonalDigestTask(PersonalDigestConfig{
		Store:  &mockPersonalDigestStore{},
		Logger: log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPersonalDigestTask_NilStore(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestPersonalDigestNotifService(ch)

	task := NewPersonalDigestTask(PersonalDigestConfig{
		NotifService: notifSvc,
		Logger:       log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPersonalDigestTask_NoHumanUsers(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestPersonalDigestNotifService(ch)
	store := &mockPersonalDigestStore{
		users: []*session.User{},
	}

	task := NewPersonalDigestTask(PersonalDigestConfig{
		Store:        store,
		NotifService: notifSvc,
		Logger:       log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) != 0 {
		t.Errorf("expected no notifications, got %d", len(ch.sent))
	}
}

func TestPersonalDigestTask_UsersWithoutSlackID_Skipped(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestPersonalDigestNotifService(ch)
	store := &mockPersonalDigestStore{
		users: []*session.User{
			{ID: "u1", Name: "Alice", Kind: "human", SlackID: ""}, // no SlackID
			{ID: "u2", Name: "Bob", Kind: "human", SlackID: ""},   // no SlackID
		},
	}

	task := NewPersonalDigestTask(PersonalDigestConfig{
		Store:        store,
		NotifService: notifSvc,
		Logger:       log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) != 0 {
		t.Errorf("expected no notifications for users without SlackID, got %d", len(ch.sent))
	}
}

func TestPersonalDigestTask_SendsDMs(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestPersonalDigestNotifService(ch)

	store := &mockPersonalDigestStore{
		users: []*session.User{
			{ID: "u1", Name: "Alice", Kind: "human", SlackID: "U001"},
			{ID: "u2", Name: "Bob", Kind: "human", SlackID: "U002"},
		},
		ownerStats: []session.OwnerStat{
			{OwnerID: "u1", OwnerName: "Alice", OwnerKind: "human", SessionCount: 5, TotalTokens: 100000, ErrorCount: 1},
			{OwnerID: "u2", OwnerName: "Bob", OwnerKind: "human", SessionCount: 3, TotalTokens: 50000, ErrorCount: 0},
		},
	}

	task := NewPersonalDigestTask(PersonalDigestConfig{
		Store:        store,
		NotifService: notifSvc,
		DashboardURL: "http://localhost:8371",
		Logger:       log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) != 2 {
		t.Errorf("expected 2 DMs (Alice + Bob), got %d", len(ch.sent))
	}
}

func TestPersonalDigestTask_SkipsUsersWithNoActivity(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestPersonalDigestNotifService(ch)

	store := &mockPersonalDigestStore{
		users: []*session.User{
			{ID: "u1", Name: "Alice", Kind: "human", SlackID: "U001"},
			{ID: "u2", Name: "Bob", Kind: "human", SlackID: "U002"},
		},
		ownerStats: []session.OwnerStat{
			// Only Alice has activity; Bob is absent from ownerStats.
			{OwnerID: "u1", OwnerName: "Alice", OwnerKind: "human", SessionCount: 5, TotalTokens: 100000, ErrorCount: 1},
		},
	}

	task := NewPersonalDigestTask(PersonalDigestConfig{
		Store:        store,
		NotifService: notifSvc,
		Logger:       log.Default(),
	})

	err := task.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) != 1 {
		t.Errorf("expected 1 DM (Alice only), got %d", len(ch.sent))
	}
}

func TestPersonalDigestTask_ListUsersError(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestPersonalDigestNotifService(ch)

	store := &mockPersonalDigestStore{
		usersErr: errors.New("db down"),
	}

	task := NewPersonalDigestTask(PersonalDigestConfig{
		Store:        store,
		NotifService: notifSvc,
		Logger:       log.Default(),
	})

	err := task.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when ListUsersByKind fails")
	}
}

func TestPersonalDigestTask_OwnerStatsError(t *testing.T) {
	ch := &mockNotifChannel{nameLit: "mock"}
	notifSvc := newTestPersonalDigestNotifService(ch)

	store := &mockPersonalDigestStore{
		users: []*session.User{
			{ID: "u1", Name: "Alice", Kind: "human", SlackID: "U001"},
		},
		ownerStatsErr: errors.New("stats broken"),
	}

	task := NewPersonalDigestTask(PersonalDigestConfig{
		Store:        store,
		NotifService: notifSvc,
		Logger:       log.Default(),
	})

	err := task.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when OwnerStats fails")
	}
}

func TestEstimateCostFromTokens(t *testing.T) {
	tests := []struct {
		tokens int
		want   float64
	}{
		{0, 0.0},
		{1_000_000, 3.0},
		{500_000, 1.5},
		{100_000, 0.3},
	}

	const epsilon = 1e-9
	for _, tt := range tests {
		got := estimateCostFromTokens(tt.tokens)
		diff := got - tt.want
		if diff < -epsilon || diff > epsilon {
			t.Errorf("estimateCostFromTokens(%d) = %.10f, want %.10f", tt.tokens, got, tt.want)
		}
	}
}

// ── Mock store for personal digest tests ──

// mockPersonalDigestStore implements the subset of storage.Store used by PersonalDigestTask.
type mockPersonalDigestStore struct {
	storage.Store // embed to satisfy interface; only used methods are implemented

	users         []*session.User
	usersErr      error
	ownerStats    []session.OwnerStat
	ownerStatsErr error
}

func (m *mockPersonalDigestStore) ListUsersByKind(_ string) ([]*session.User, error) {
	return m.users, m.usersErr
}

func (m *mockPersonalDigestStore) OwnerStats(_ string, _, _ time.Time) ([]session.OwnerStat, error) {
	return m.ownerStats, m.ownerStatsErr
}

// ── Helpers ──

// newTestPersonalDigestNotifService creates a notification service configured for personal DMs.
func newTestPersonalDigestNotifService(ch *mockNotifChannel) *notification.Service {
	return notification.NewService(notification.ServiceConfig{
		Channels: []notification.ChannelWithFormatter{
			{
				Channel:   ch,
				Formatter: &mockNotifFormatter{},
			},
		},
		Router: notification.NewDefaultRouter(notification.RoutingConfig{
			DefaultChannel: "#test",
			Digest: notification.DigestConfig{
				Personal: true, // enable personal DM routing
			},
		}),
	})
}
