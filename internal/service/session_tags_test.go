package service

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// tagMockStore is a functional in-memory implementation of the tag-related
// store methods, embedded over mockStore to keep the rest of the Store
// interface satisfied by stubs.
type tagMockStore struct {
	mockStore
	tags          map[session.ID]map[string]struct{}
	listResult    []session.Summary // controls store.List() output for ResolveCurrentSessionID fallback
	listErr       error
	latestBranch  *session.Session
	getLatestErr  error
	deletedSessID session.ID // set when Delete is called, for assertion
}

func newTagMockStore() *tagMockStore {
	return &tagMockStore{
		mockStore: mockStore{sessions: make(map[session.ID]*session.Session)},
		tags:      make(map[session.ID]map[string]struct{}),
	}
}

// Override the relevant methods.

func (m *tagMockStore) AddTags(id session.ID, tags []string) (int, error) {
	if id == "" {
		return 0, errors.New("AddTags: empty id")
	}
	if _, ok := m.sessions[id]; !ok {
		return 0, session.ErrSessionNotFound
	}
	if m.tags[id] == nil {
		m.tags[id] = make(map[string]struct{})
	}
	inserted := 0
	seen := make(map[string]struct{})
	for _, raw := range tags {
		t := normalizeTagForMock(raw)
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		if _, exists := m.tags[id][t]; exists {
			continue
		}
		m.tags[id][t] = struct{}{}
		inserted++
	}
	return inserted, nil
}

func (m *tagMockStore) RemoveTags(id session.ID, tags []string) (int, error) {
	if id == "" {
		return 0, errors.New("RemoveTags: empty id")
	}
	removed := 0
	if m.tags[id] == nil {
		return 0, nil
	}
	seen := make(map[string]struct{})
	for _, raw := range tags {
		t := normalizeTagForMock(raw)
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		if _, exists := m.tags[id][t]; exists {
			delete(m.tags[id], t)
			removed++
		}
	}
	return removed, nil
}

func (m *tagMockStore) GetTags(id session.ID) ([]string, error) {
	if m.tags[id] == nil {
		return nil, nil
	}
	out := make([]string, 0, len(m.tags[id]))
	for t := range m.tags[id] {
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}

func (m *tagMockStore) GetTagsBatch(ids []session.ID) (map[session.ID][]string, error) {
	out := make(map[session.ID][]string)
	for _, id := range ids {
		if m.tags[id] == nil {
			continue
		}
		ts, _ := m.GetTags(id)
		if len(ts) > 0 {
			out[id] = ts
		}
	}
	return out, nil
}

func (m *tagMockStore) ListAllTags() ([]session.TagCount, error) {
	counts := make(map[string]int)
	for _, ts := range m.tags {
		for t := range ts {
			counts[t]++
		}
	}
	out := make([]session.TagCount, 0, len(counts))
	for t, c := range counts {
		out = append(out, session.TagCount{Tag: t, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Tag < out[j].Tag
	})
	return out, nil
}

func (m *tagMockStore) FilterSessionIDsByTags(ids []session.ID, tags []string) ([]session.ID, error) {
	if len(tags) == 0 {
		return ids, nil
	}
	want := make(map[string]struct{})
	for _, raw := range tags {
		if t := normalizeTagForMock(raw); t != "" {
			want[t] = struct{}{}
		}
	}
	if len(want) == 0 {
		return ids, nil
	}
	var out []session.ID
	for _, id := range ids {
		matched := 0
		for t := range want {
			if _, ok := m.tags[id][t]; ok {
				matched++
			}
		}
		if matched == len(want) {
			out = append(out, id)
		}
	}
	return out, nil
}

// Override List for ResolveCurrentSessionID fallback path.
func (m *tagMockStore) List(_ session.ListOptions) ([]session.Summary, error) {
	return m.listResult, m.listErr
}

func (m *tagMockStore) GetLatestByBranch(_, _ string) (*session.Session, error) {
	return m.latestBranch, m.getLatestErr
}

// normalizeTagForMock mirrors sqlite.NormalizeTag so the mock applies the
// same normalization the real store uses.
func normalizeTagForMock(tag string) string {
	t := strings.ToLower(strings.TrimSpace(tag))
	if t == "" {
		return ""
	}
	fields := strings.Fields(t)
	return strings.Join(fields, "-")
}

// ── AddTags ──

func TestSessionService_AddTags_Success(t *testing.T) {
	store := newTagMockStore()
	store.sessions["ses_1"] = &session.Session{ID: "ses_1"}

	svc := NewSessionService(SessionServiceConfig{Store: store})

	n, err := svc.AddTags(context.Background(), "ses_1", []string{"Bug", "URGENT", "bug"})
	if err != nil {
		t.Fatalf("AddTags: %v", err)
	}
	if n != 2 {
		t.Errorf("inserted = %d, want 2", n)
	}

	got, err := svc.GetSessionTags(context.Background(), "ses_1")
	if err != nil {
		t.Fatalf("GetSessionTags: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"bug", "urgent"}) {
		t.Errorf("tags = %v, want [bug urgent]", got)
	}
}

func TestSessionService_AddTags_SessionNotFound(t *testing.T) {
	store := newTagMockStore()
	svc := NewSessionService(SessionServiceConfig{Store: store})

	_, err := svc.AddTags(context.Background(), "ses_missing", []string{"foo"})
	if err == nil {
		t.Fatal("expected error for missing session")
	}
}

func TestSessionService_AddTags_EmptyID(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{Store: newTagMockStore()})
	_, err := svc.AddTags(context.Background(), "", []string{"foo"})
	if err == nil {
		t.Fatal("expected error for empty session id")
	}
}

// ── RemoveTags ──

func TestSessionService_RemoveTags(t *testing.T) {
	store := newTagMockStore()
	store.sessions["ses_1"] = &session.Session{ID: "ses_1"}
	if _, err := store.AddTags("ses_1", []string{"alpha", "beta"}); err != nil {
		t.Fatalf("seed AddTags: %v", err)
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})

	n, err := svc.RemoveTags(context.Background(), "ses_1", []string{"ALPHA", "ghost"})
	if err != nil {
		t.Fatalf("RemoveTags: %v", err)
	}
	if n != 1 {
		t.Errorf("removed = %d, want 1", n)
	}

	tags, _ := svc.GetSessionTags(context.Background(), "ses_1")
	if !reflect.DeepEqual(tags, []string{"beta"}) {
		t.Errorf("tags after remove = %v, want [beta]", tags)
	}
}

func TestSessionService_RemoveTags_EmptyID(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{Store: newTagMockStore()})
	_, err := svc.RemoveTags(context.Background(), "", []string{"foo"})
	if err == nil {
		t.Fatal("expected error for empty session id")
	}
}

// ── GetSessionTags ──

func TestSessionService_GetSessionTags_NoTagsReturnsEmptySlice(t *testing.T) {
	store := newTagMockStore()
	store.sessions["ses_1"] = &session.Session{ID: "ses_1"}
	svc := NewSessionService(SessionServiceConfig{Store: store})

	got, err := svc.GetSessionTags(context.Background(), "ses_1")
	if err != nil {
		t.Fatalf("GetSessionTags: %v", err)
	}
	if got == nil {
		t.Fatal("expected empty slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestSessionService_GetSessionTags_EmptyID(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{Store: newTagMockStore()})
	_, err := svc.GetSessionTags(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty session id")
	}
}

// ── ListAllTags ──

func TestSessionService_ListAllTags(t *testing.T) {
	store := newTagMockStore()
	store.sessions["ses_a"] = &session.Session{ID: "ses_a"}
	store.sessions["ses_b"] = &session.Session{ID: "ses_b"}
	if _, err := store.AddTags("ses_a", []string{"shared", "rare"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := store.AddTags("ses_b", []string{"shared"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	svc := NewSessionService(SessionServiceConfig{Store: store})

	got, err := svc.ListAllTags(context.Background())
	if err != nil {
		t.Fatalf("ListAllTags: %v", err)
	}
	want := []session.TagCount{
		{Tag: "shared", Count: 2},
		{Tag: "rare", Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListAllTags = %v, want %v", got, want)
	}
}

func TestSessionService_ListAllTags_EmptyReturnsEmptySlice(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{Store: newTagMockStore()})
	got, err := svc.ListAllTags(context.Background())
	if err != nil {
		t.Fatalf("ListAllTags: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %d entries", len(got))
	}
}

// ── ResolveCurrentSessionID ──
//
// Note: branch-aware lookup goes through s.git.CurrentBranch() on a concrete
// *git.Client, which we can't easily mock here without invoking a real git
// repo. We only exercise the project fallback path (git nil → use store.List).

func TestSessionService_ResolveCurrentSessionID_FallbackToProject(t *testing.T) {
	store := newTagMockStore()
	now := time.Now()
	store.listResult = []session.Summary{
		{ID: "ses_recent", CreatedAt: now},
		{ID: "ses_older", CreatedAt: now.Add(-time.Hour)},
	}
	svc := NewSessionService(SessionServiceConfig{Store: store}) // git nil

	got, err := svc.ResolveCurrentSessionID(context.Background(), "/path/to/proj")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "ses_recent" {
		t.Errorf("Resolve = %q, want ses_recent", got)
	}
}

func TestSessionService_ResolveCurrentSessionID_EmptyPath(t *testing.T) {
	svc := NewSessionService(SessionServiceConfig{Store: newTagMockStore()})
	_, err := svc.ResolveCurrentSessionID(context.Background(), "")
	if !errors.Is(err, ErrNoCurrentSession) {
		t.Errorf("err = %v, want ErrNoCurrentSession", err)
	}
}

func TestSessionService_ResolveCurrentSessionID_NoSessions(t *testing.T) {
	store := newTagMockStore()
	store.listResult = nil
	svc := NewSessionService(SessionServiceConfig{Store: store})

	_, err := svc.ResolveCurrentSessionID(context.Background(), "/some/proj")
	if !errors.Is(err, ErrNoCurrentSession) {
		t.Errorf("err = %v, want ErrNoCurrentSession", err)
	}
}
