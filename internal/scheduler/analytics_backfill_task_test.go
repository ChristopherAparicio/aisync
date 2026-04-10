package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// analyticsStore is a minimal mock that embeds storage.Store (nil) and
// implements only the four methods AnalyticsBackfillTask actually calls:
//   - ListSessionsNeedingAnalytics
//   - Get
//   - GetForkRelations
//   - UpsertSessionAnalytics
type analyticsStore struct {
	storage.Store // nil fallthrough — panics if unexpected methods are called

	sessions  map[session.ID]*session.Session
	forks     map[session.ID][]session.ForkRelation
	analytics map[session.ID]session.Analytics

	listErr   error
	getErr    error
	upsertErr error
}

func newAnalyticsStore() *analyticsStore {
	return &analyticsStore{
		sessions:  make(map[session.ID]*session.Session),
		forks:     make(map[session.ID][]session.ForkRelation),
		analytics: make(map[session.ID]session.Analytics),
	}
}

func (s *analyticsStore) ListSessionsNeedingAnalytics(minSchemaVersion int, limit int) ([]session.ID, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	var ids []session.ID
	for id := range s.sessions {
		a, ok := s.analytics[id]
		if !ok || a.SchemaVersion < minSchemaVersion {
			ids = append(ids, id)
			if limit > 0 && len(ids) >= limit {
				break
			}
		}
	}
	return ids, nil
}

func (s *analyticsStore) Get(id session.ID) (*session.Session, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	sess, ok := s.sessions[id]
	if !ok {
		return nil, session.ErrSessionNotFound
	}
	return sess, nil
}

func (s *analyticsStore) GetForkRelations(id session.ID) ([]session.ForkRelation, error) {
	rels, ok := s.forks[id]
	if !ok {
		return nil, nil
	}
	return rels, nil
}

func (s *analyticsStore) UpsertSessionAnalytics(a session.Analytics) error {
	if s.upsertErr != nil {
		return s.upsertErr
	}
	s.analytics[a.SessionID] = a
	return nil
}

// ── Helpers ──

func makeSession(id session.ID, msgCount int) *session.Session {
	msgs := make([]session.Message, msgCount)
	for i := range msgs {
		role := session.RoleUser
		if i%2 == 1 {
			role = session.RoleAssistant
		}
		msgs[i] = session.Message{
			Role:      role,
			Content:   "test message",
			Timestamp: time.Now(),
		}
	}
	return &session.Session{
		ID:        id,
		Summary:   "Test Session " + string(id),
		Messages:  msgs,
		CreatedAt: time.Now(),
	}
}

func newBackfillTask(store storage.Store) *AnalyticsBackfillTask {
	return NewAnalyticsBackfillTask(AnalyticsBackfillConfig{
		Store:   store,
		Pricing: nil, // nil pricing is fine — ComputeAnalytics handles it
		Logger:  log.Default(),
	})
}

// ── Tests ──

func TestAnalyticsBackfillTask_Name(t *testing.T) {
	task := newBackfillTask(newAnalyticsStore())
	if got := task.Name(); got != "analytics_backfill" {
		t.Fatalf("Name() = %q, want %q", got, "analytics_backfill")
	}
}

func TestAnalyticsBackfillTask_NoWork(t *testing.T) {
	store := newAnalyticsStore()
	task := newBackfillTask(store)

	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
}

func TestAnalyticsBackfillTask_BackfillsSessions(t *testing.T) {
	store := newAnalyticsStore()
	store.sessions["s1"] = makeSession("s1", 4)
	store.sessions["s2"] = makeSession("s2", 6)

	task := newBackfillTask(store)

	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	// Both sessions should now have analytics.
	if len(store.analytics) != 2 {
		t.Fatalf("expected 2 analytics rows, got %d", len(store.analytics))
	}
	for _, id := range []session.ID{"s1", "s2"} {
		a, ok := store.analytics[id]
		if !ok {
			t.Errorf("missing analytics for %s", id)
			continue
		}
		if a.SessionID != id {
			t.Errorf("analytics.SessionID = %s, want %s", a.SessionID, id)
		}
		if a.SchemaVersion != session.AnalyticsSchemaVersion {
			t.Errorf("analytics.SchemaVersion = %d, want %d", a.SchemaVersion, session.AnalyticsSchemaVersion)
		}
	}
}

func TestAnalyticsBackfillTask_SkipsEmptySessions(t *testing.T) {
	store := newAnalyticsStore()
	store.sessions["empty"] = &session.Session{
		ID:        "empty",
		Summary:   "Empty session",
		Messages:  nil, // no messages
		CreatedAt: time.Now(),
	}
	store.sessions["full"] = makeSession("full", 4)

	task := newBackfillTask(store)

	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	// Only "full" should have analytics — "empty" is skipped.
	if _, ok := store.analytics["empty"]; ok {
		t.Error("expected no analytics for empty session")
	}
	if _, ok := store.analytics["full"]; !ok {
		t.Error("expected analytics for full session")
	}
}

func TestAnalyticsBackfillTask_SkipsAlreadyComputed(t *testing.T) {
	store := newAnalyticsStore()
	store.sessions["s1"] = makeSession("s1", 4)

	// Pre-populate analytics at current schema version with a sentinel value.
	store.analytics["s1"] = session.Analytics{
		SessionID:             "s1",
		SchemaVersion:         session.AnalyticsSchemaVersion,
		TotalAgentInvocations: 999, // sentinel — real value would be 4
	}

	task := newBackfillTask(store)

	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	// Analytics should be untouched (sentinel value preserved).
	a := store.analytics["s1"]
	if a.TotalAgentInvocations != 999 {
		t.Errorf("expected sentinel TotalAgentInvocations=999, got %d — row was unexpectedly recomputed", a.TotalAgentInvocations)
	}
}

func TestAnalyticsBackfillTask_RecomputesStaleSchema(t *testing.T) {
	store := newAnalyticsStore()
	store.sessions["s1"] = makeSession("s1", 4)

	// Pre-populate analytics at a stale schema version.
	store.analytics["s1"] = session.Analytics{
		SessionID:             "s1",
		SchemaVersion:         session.AnalyticsSchemaVersion - 1,
		TotalAgentInvocations: 999,
	}

	task := newBackfillTask(store)

	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	// Analytics should have been recomputed (sentinel overwritten).
	a := store.analytics["s1"]
	if a.SchemaVersion != session.AnalyticsSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", a.SchemaVersion, session.AnalyticsSchemaVersion)
	}
	// ComputeAnalytics sets TotalAgentInvocations = len(Messages) = 4
	if a.TotalAgentInvocations != 4 {
		t.Errorf("expected recomputed TotalAgentInvocations=4, got %d", a.TotalAgentInvocations)
	}
}

func TestAnalyticsBackfillTask_UsesForkOffset(t *testing.T) {
	store := newAnalyticsStore()
	store.sessions["fork1"] = makeSession("fork1", 10)
	store.forks["fork1"] = []session.ForkRelation{
		{
			OriginalID:     "parent",
			ForkID:         "fork1",
			SharedMessages: 4,
		},
	}

	task := newBackfillTask(store)

	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	a, ok := store.analytics["fork1"]
	if !ok {
		t.Fatal("missing analytics for fork1")
	}
	// The ForkOffset should be set to 4 (SharedMessages from the relation).
	if a.ForkOffset != 4 {
		t.Errorf("ForkOffset = %d, want 4", a.ForkOffset)
	}
}

func TestAnalyticsBackfillTask_ListError(t *testing.T) {
	store := newAnalyticsStore()
	store.listErr = errors.New("db locked")

	task := newBackfillTask(store)

	err := task.Run(context.Background())
	if err == nil || err.Error() != "db locked" {
		t.Fatalf("expected 'db locked' error, got %v", err)
	}
}

func TestAnalyticsBackfillTask_GetErrorContinues(t *testing.T) {
	store := newAnalyticsStore()
	store.sessions["s1"] = makeSession("s1", 4)
	store.getErr = errors.New("corrupt")

	task := newBackfillTask(store)

	// Should not return error — Get failures are non-fatal per session.
	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	// No analytics should have been written.
	if len(store.analytics) != 0 {
		t.Errorf("expected 0 analytics rows, got %d", len(store.analytics))
	}
}

func TestAnalyticsBackfillTask_UpsertErrorContinues(t *testing.T) {
	store := newAnalyticsStore()
	store.sessions["s1"] = makeSession("s1", 4)
	store.sessions["s2"] = makeSession("s2", 4)
	store.upsertErr = errors.New("disk full")

	task := newBackfillTask(store)

	// Should not return error — Upsert failures are non-fatal per session.
	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
}

func TestAnalyticsBackfillTask_ContextCancelled(t *testing.T) {
	store := newAnalyticsStore()
	// Add enough sessions that we'd iterate.
	for i := 0; i < 5; i++ {
		id := session.ID(fmt.Sprintf("s%d", i))
		store.sessions[id] = makeSession(id, 4)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	task := newBackfillTask(store)

	err := task.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
