package sqlite

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/analysis"
	"github.com/ChristopherAparicio/aisync/internal/auth"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/sessionevent"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

func mustOpenStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New(%q) error = %v", dbPath, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testSession(id string) *session.Session {
	now := time.Date(2026, 2, 16, 14, 0, 0, 0, time.UTC)
	return &session.Session{
		ID:          session.ID(id),
		Version:     1,
		Provider:    session.ProviderClaudeCode,
		Agent:       "claude",
		Branch:      "feature/auth",
		CommitSHA:   "abc1234",
		ProjectPath: "/home/chris/my-app",
		ExportedBy:  "Christopher",
		ExportedAt:  now,
		CreatedAt:   now,
		Summary:     "Implement OAuth2",
		StorageMode: session.StorageModeCompact,
		Messages: []session.Message{
			{
				ID:        "msg-001",
				Role:      session.RoleUser,
				Content:   "Implement OAuth2",
				Timestamp: now,
			},
		},
		FileChanges: []session.FileChange{
			{FilePath: "src/auth.py", ChangeType: session.ChangeCreated},
		},
		TokenUsage: session.TokenUsage{
			InputTokens:  1000,
			OutputTokens: 500,
			TotalTokens:  1500,
		},
		Links: []session.Link{
			{LinkType: session.LinkBranch, Ref: "feature/auth"},
		},
	}
}

func TestSaveAndGet(t *testing.T) {
	store := mustOpenStore(t)
	sess := testSession("sess-1")

	// Save
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Get
	got, err := store.Get(session.ID("sess-1"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if got.ID != sess.ID {
		t.Errorf("ID = %q, want %q", got.ID, sess.ID)
	}
	if got.Provider != sess.Provider {
		t.Errorf("Provider = %q, want %q", got.Provider, sess.Provider)
	}
	if got.Agent != "claude" {
		t.Errorf("Agent = %q, want %q", got.Agent, "claude")
	}
	if got.Branch != "feature/auth" {
		t.Errorf("Branch = %q, want %q", got.Branch, "feature/auth")
	}
	if got.Summary != "Implement OAuth2" {
		t.Errorf("Summary = %q, want %q", got.Summary, "Implement OAuth2")
	}
	if len(got.Messages) != 1 {
		t.Fatalf("Messages count = %d, want 1", len(got.Messages))
	}
	if got.Messages[0].Role != session.RoleUser {
		t.Errorf("Messages[0].Role = %q, want %q", got.Messages[0].Role, session.RoleUser)
	}
	if len(got.FileChanges) != 1 {
		t.Fatalf("FileChanges count = %d, want 1", len(got.FileChanges))
	}
	if got.TokenUsage.TotalTokens != 1500 {
		t.Errorf("TotalTokens = %d, want 1500", got.TokenUsage.TotalTokens)
	}
}

func TestGet_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	_, err := store.Get(session.ID("nonexistent"))
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("Get(nonexistent) error = %v, want ErrSessionNotFound", err)
	}
}

func TestGetBatch(t *testing.T) {
	store := mustOpenStore(t)

	// Seed three sessions with distinct IDs and heterogeneous companion data:
	//   sess-a: has both links and file changes
	//   sess-b: has file changes, no links
	//   sess-c: has no companion rows at all
	a := testSession("sess-a")
	b := testSession("sess-b")
	b.Links = nil
	c := testSession("sess-c")
	c.Links = nil
	c.FileChanges = nil

	for _, s := range []*session.Session{a, b, c} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	t.Run("returns all requested sessions", func(t *testing.T) {
		got, err := store.GetBatch([]session.ID{"sess-a", "sess-b", "sess-c"})
		if err != nil {
			t.Fatalf("GetBatch() error = %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("len(got) = %d, want 3", len(got))
		}
		for _, id := range []session.ID{"sess-a", "sess-b", "sess-c"} {
			if _, ok := got[id]; !ok {
				t.Errorf("missing session %q in result", id)
			}
		}
	})

	t.Run("hydrates links and file_changes from companion tables", func(t *testing.T) {
		got, err := store.GetBatch([]session.ID{"sess-a", "sess-b", "sess-c"})
		if err != nil {
			t.Fatalf("GetBatch() error = %v", err)
		}

		gotA := got[session.ID("sess-a")]
		if len(gotA.Links) != 1 || gotA.Links[0].Ref != "feature/auth" {
			t.Errorf("sess-a.Links = %+v, want one feature/auth link", gotA.Links)
		}
		if len(gotA.FileChanges) != 1 || gotA.FileChanges[0].FilePath != "src/auth.py" {
			t.Errorf("sess-a.FileChanges = %+v, want one src/auth.py change", gotA.FileChanges)
		}

		gotB := got[session.ID("sess-b")]
		if len(gotB.Links) != 0 {
			t.Errorf("sess-b.Links = %+v, want empty", gotB.Links)
		}
		if len(gotB.FileChanges) != 1 {
			t.Errorf("sess-b.FileChanges len = %d, want 1", len(gotB.FileChanges))
		}

		gotC := got[session.ID("sess-c")]
		if len(gotC.Links) != 0 || len(gotC.FileChanges) != 0 {
			t.Errorf("sess-c should have no companion rows, got links=%d fc=%d",
				len(gotC.Links), len(gotC.FileChanges))
		}
	})

	t.Run("missing IDs are silently omitted", func(t *testing.T) {
		got, err := store.GetBatch([]session.ID{"sess-a", "does-not-exist"})
		if err != nil {
			t.Fatalf("GetBatch() error = %v", err)
		}
		if len(got) != 1 {
			t.Errorf("len(got) = %d, want 1 (missing ID should be dropped silently)", len(got))
		}
		if _, ok := got[session.ID("does-not-exist")]; ok {
			t.Errorf("missing ID should not appear in result map")
		}
	})

	t.Run("empty input returns empty map without error", func(t *testing.T) {
		got, err := store.GetBatch(nil)
		if err != nil {
			t.Fatalf("GetBatch(nil) error = %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("GetBatch(nil) = %v, want empty map", got)
		}
	})

	t.Run("duplicate IDs are de-duplicated", func(t *testing.T) {
		got, err := store.GetBatch([]session.ID{"sess-a", "sess-a", "sess-b"})
		if err != nil {
			t.Fatalf("GetBatch() error = %v", err)
		}
		if len(got) != 2 {
			t.Errorf("len(got) = %d, want 2 (dedupe)", len(got))
		}
	})

	t.Run("matches Get() semantics for a single ID", func(t *testing.T) {
		single, err := store.Get(session.ID("sess-a"))
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		batch, err := store.GetBatch([]session.ID{"sess-a"})
		if err != nil {
			t.Fatalf("GetBatch() error = %v", err)
		}
		got := batch[session.ID("sess-a")]
		if got.ID != single.ID || got.Summary != single.Summary {
			t.Errorf("GetBatch result differs from Get: batch=%+v single=%+v", got, single)
		}
		if len(got.Links) != len(single.Links) || len(got.FileChanges) != len(single.FileChanges) {
			t.Errorf("companion row counts differ: batch links=%d fc=%d, single links=%d fc=%d",
				len(got.Links), len(got.FileChanges), len(single.Links), len(single.FileChanges))
		}
	})
}

// TestGetSessionEventsBatch covers the batch event-load path introduced by Fix #8
// to eliminate the N+1 pattern in SkillROIAnalysis. It exercises:
//   - basic multi-session retrieval with correct per-session bucketing
//   - SQL-level event_type filter (variadic types parameter)
//   - silent omission of missing IDs
//   - empty input → empty map
//   - duplicate ID de-duplication
//   - parity with GetSessionEvents for a single session (ordering + payload unmarshal)
func TestGetSessionEventsBatch(t *testing.T) {
	store := mustOpenStore(t)

	// session_events has a FK to sessions(id) ON DELETE CASCADE, so parent rows
	// must exist before SaveEvents is called.
	for _, id := range []string{"sess-a", "sess-b", "sess-c"} {
		if err := store.Save(testSession(id)); err != nil {
			t.Fatalf("Save(%s) error = %v", id, err)
		}
	}

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	// Seed a heterogeneous event mix across three sessions so we can assert both
	// session bucketing and the type filter path:
	//   sess-a: 1 skill_load + 1 tool_call
	//   sess-b: 2 skill_load
	//   sess-c: 1 tool_call only (no skill_load → should be absent from filtered result)
	events := []sessionevent.Event{
		{
			ID: "evt-a-1", SessionID: "sess-a", Type: sessionevent.EventSkillLoad,
			MessageIndex: 0, OccurredAt: base,
			SkillLoad: &sessionevent.SkillLoadDetail{SkillName: "replay-tester", EstimatedTokens: 500},
		},
		{
			ID: "evt-a-2", SessionID: "sess-a", Type: sessionevent.EventToolCall,
			MessageIndex: 1, OccurredAt: base.Add(1 * time.Second),
			ToolCall: &sessionevent.ToolCallDetail{ToolName: "bash", ToolCategory: "builtin"},
		},
		{
			ID: "evt-b-1", SessionID: "sess-b", Type: sessionevent.EventSkillLoad,
			MessageIndex: 0, OccurredAt: base,
			SkillLoad: &sessionevent.SkillLoadDetail{SkillName: "replay-tester", EstimatedTokens: 450},
		},
		{
			ID: "evt-b-2", SessionID: "sess-b", Type: sessionevent.EventSkillLoad,
			MessageIndex: 1, OccurredAt: base.Add(2 * time.Second),
			SkillLoad: &sessionevent.SkillLoadDetail{SkillName: "opencode-sessions", EstimatedTokens: 300},
		},
		{
			ID: "evt-c-1", SessionID: "sess-c", Type: sessionevent.EventToolCall,
			MessageIndex: 0, OccurredAt: base,
			ToolCall: &sessionevent.ToolCallDetail{ToolName: "Read", ToolCategory: "builtin"},
		},
	}
	if err := store.SaveEvents(events); err != nil {
		t.Fatalf("SaveEvents() error = %v", err)
	}

	t.Run("returns all events bucketed per session", func(t *testing.T) {
		got, err := store.GetSessionEventsBatch([]session.ID{"sess-a", "sess-b", "sess-c"})
		if err != nil {
			t.Fatalf("GetSessionEventsBatch() error = %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("len(got) = %d, want 3 sessions", len(got))
		}
		if len(got["sess-a"]) != 2 {
			t.Errorf("sess-a event count = %d, want 2", len(got["sess-a"]))
		}
		if len(got["sess-b"]) != 2 {
			t.Errorf("sess-b event count = %d, want 2", len(got["sess-b"]))
		}
		if len(got["sess-c"]) != 1 {
			t.Errorf("sess-c event count = %d, want 1", len(got["sess-c"]))
		}
	})

	t.Run("event_type filter narrows result at SQL level", func(t *testing.T) {
		got, err := store.GetSessionEventsBatch(
			[]session.ID{"sess-a", "sess-b", "sess-c"},
			sessionevent.EventSkillLoad,
		)
		if err != nil {
			t.Fatalf("GetSessionEventsBatch(types=skill_load) error = %v", err)
		}
		// sess-c has no skill_load events → should be absent from the map.
		if _, ok := got["sess-c"]; ok {
			t.Errorf("sess-c should be absent from filtered result (no skill_load events)")
		}
		if len(got["sess-a"]) != 1 {
			t.Errorf("sess-a skill_load count = %d, want 1", len(got["sess-a"]))
		}
		if len(got["sess-b"]) != 2 {
			t.Errorf("sess-b skill_load count = %d, want 2", len(got["sess-b"]))
		}
		// Verify every returned event is actually a skill_load with its payload unmarshalled.
		for sid, evs := range got {
			for _, e := range evs {
				if e.Type != sessionevent.EventSkillLoad {
					t.Errorf("%s returned non-skill_load event type %q", sid, e.Type)
				}
				if e.SkillLoad == nil || e.SkillLoad.SkillName == "" {
					t.Errorf("%s event %s has empty SkillLoad payload", sid, e.ID)
				}
			}
		}
	})

	t.Run("multiple event types via variadic", func(t *testing.T) {
		got, err := store.GetSessionEventsBatch(
			[]session.ID{"sess-a"},
			sessionevent.EventSkillLoad, sessionevent.EventToolCall,
		)
		if err != nil {
			t.Fatalf("GetSessionEventsBatch(types=skill_load,tool_call) error = %v", err)
		}
		if len(got["sess-a"]) != 2 {
			t.Errorf("sess-a multi-type count = %d, want 2", len(got["sess-a"]))
		}
	})

	t.Run("missing IDs are silently omitted", func(t *testing.T) {
		got, err := store.GetSessionEventsBatch([]session.ID{"sess-a", "does-not-exist"})
		if err != nil {
			t.Fatalf("GetSessionEventsBatch() error = %v", err)
		}
		if _, ok := got["does-not-exist"]; ok {
			t.Errorf("missing ID should not appear in result map")
		}
		if len(got["sess-a"]) != 2 {
			t.Errorf("sess-a count = %d, want 2", len(got["sess-a"]))
		}
	})

	t.Run("empty input returns empty map without error", func(t *testing.T) {
		got, err := store.GetSessionEventsBatch(nil)
		if err != nil {
			t.Fatalf("GetSessionEventsBatch(nil) error = %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("GetSessionEventsBatch(nil) = %v, want empty map", got)
		}
	})

	t.Run("duplicate IDs are de-duplicated", func(t *testing.T) {
		got, err := store.GetSessionEventsBatch([]session.ID{"sess-a", "sess-a", "sess-b"})
		if err != nil {
			t.Fatalf("GetSessionEventsBatch() error = %v", err)
		}
		// Dedup: sess-a should only appear once and still hold its 2 events
		// (not 4 — if dedupe broke, SQL would return duplicate rows).
		if len(got["sess-a"]) != 2 {
			t.Errorf("sess-a count = %d, want 2 (dedup check)", len(got["sess-a"]))
		}
		if len(got["sess-b"]) != 2 {
			t.Errorf("sess-b count = %d, want 2", len(got["sess-b"]))
		}
	})

	t.Run("matches GetSessionEvents() semantics for a single ID", func(t *testing.T) {
		single, err := store.GetSessionEvents(session.ID("sess-b"))
		if err != nil {
			t.Fatalf("GetSessionEvents() error = %v", err)
		}
		batch, err := store.GetSessionEventsBatch([]session.ID{"sess-b"})
		if err != nil {
			t.Fatalf("GetSessionEventsBatch() error = %v", err)
		}
		got := batch[session.ID("sess-b")]
		if len(got) != len(single) {
			t.Fatalf("batch len = %d, single len = %d", len(got), len(single))
		}
		for i := range got {
			if got[i].ID != single[i].ID {
				t.Errorf("event[%d] ID: batch=%q single=%q", i, got[i].ID, single[i].ID)
			}
			if got[i].Type != single[i].Type {
				t.Errorf("event[%d] Type: batch=%q single=%q", i, got[i].Type, single[i].Type)
			}
			// Payload unmarshal parity check.
			if (got[i].SkillLoad == nil) != (single[i].SkillLoad == nil) {
				t.Errorf("event[%d] SkillLoad payload parity mismatch", i)
			}
			if got[i].SkillLoad != nil && single[i].SkillLoad != nil {
				if got[i].SkillLoad.SkillName != single[i].SkillLoad.SkillName {
					t.Errorf("event[%d] SkillName: batch=%q single=%q",
						i, got[i].SkillLoad.SkillName, single[i].SkillLoad.SkillName)
				}
			}
		}
	})
}

func TestSave_Upsert(t *testing.T) {
	store := mustOpenStore(t)
	sess := testSession("sess-1")

	// Save initial
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Update summary and save again
	sess.Summary = "Updated summary"
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() upsert error = %v", err)
	}

	got, err := store.Get(session.ID("sess-1"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Summary != "Updated summary" {
		t.Errorf("Summary = %q, want %q", got.Summary, "Updated summary")
	}
}

func TestGetLatestByBranch(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("sess-1")
	s1.Branch = "feature/auth"
	s1.ProjectPath = "/project"
	s1.CreatedAt = time.Date(2026, 2, 16, 10, 0, 0, 0, time.UTC)

	s2 := testSession("sess-2")
	s2.Branch = "feature/other"
	s2.ProjectPath = "/project"

	s3 := testSession("sess-3")
	s3.Branch = "feature/auth"
	s3.ProjectPath = "/project"
	s3.CreatedAt = time.Date(2026, 2, 17, 10, 0, 0, 0, time.UTC)

	for _, s := range []*session.Session{s1, s2, s3} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	got, err := store.GetLatestByBranch("/project", "feature/auth")
	if err != nil {
		t.Fatalf("GetLatestByBranch() error = %v", err)
	}
	// Should return the most recent session (sess-3, created Feb 17)
	if got.ID != session.ID("sess-3") {
		t.Errorf("ID = %q, want %q (most recent)", got.ID, "sess-3")
	}
}

func TestGetLatestByBranch_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	_, err := store.GetLatestByBranch("/project", "nonexistent")
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("GetLatestByBranch(nonexistent) error = %v, want ErrSessionNotFound", err)
	}
}

func TestCountByBranch(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("count-1")
	s1.Branch = "feature/auth"
	s1.ProjectPath = "/project"

	s2 := testSession("count-2")
	s2.Branch = "feature/auth"
	s2.ProjectPath = "/project"

	s3 := testSession("count-3")
	s3.Branch = "main"
	s3.ProjectPath = "/project"

	for _, s := range []*session.Session{s1, s2, s3} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	count, err := store.CountByBranch("/project", "feature/auth")
	if err != nil {
		t.Fatalf("CountByBranch() error = %v", err)
	}
	if count != 2 {
		t.Errorf("CountByBranch(feature/auth) = %d, want 2", count)
	}

	count, err = store.CountByBranch("/project", "main")
	if err != nil {
		t.Fatalf("CountByBranch() error = %v", err)
	}
	if count != 1 {
		t.Errorf("CountByBranch(main) = %d, want 1", count)
	}

	count, err = store.CountByBranch("/project", "nonexistent")
	if err != nil {
		t.Fatalf("CountByBranch() error = %v", err)
	}
	if count != 0 {
		t.Errorf("CountByBranch(nonexistent) = %d, want 0", count)
	}
}

func TestList(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("sess-1")
	s1.Branch = "feature/auth"
	s1.ProjectPath = "/project"

	s2 := testSession("sess-2")
	s2.Branch = "feature/auth"
	s2.ProjectPath = "/project"
	s2.Provider = session.ProviderOpenCode
	s2.Agent = "coder"

	s3 := testSession("sess-3")
	s3.Branch = "main"
	s3.ProjectPath = "/project"

	for _, s := range []*session.Session{s1, s2, s3} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	t.Run("list by branch", func(t *testing.T) {
		summaries, err := store.List(session.ListOptions{
			ProjectPath: "/project",
			Branch:      "feature/auth",
		})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(summaries) != 2 {
			t.Errorf("List(branch=feature/auth) count = %d, want 2", len(summaries))
		}
	})

	t.Run("list all", func(t *testing.T) {
		summaries, err := store.List(session.ListOptions{
			ProjectPath: "/project",
			All:         true,
		})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(summaries) != 3 {
			t.Errorf("List(all) count = %d, want 3", len(summaries))
		}
	})
}

func TestDelete(t *testing.T) {
	store := mustOpenStore(t)
	sess := testSession("sess-1")

	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Delete(session.ID("sess-1")); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, err := store.Get(session.ID("sess-1"))
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("Get after Delete: error = %v, want ErrSessionNotFound", err)
	}
}

func TestDelete_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	err := store.Delete(session.ID("nonexistent"))
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("Delete(nonexistent) error = %v, want ErrSessionNotFound", err)
	}
}

func TestAddLink(t *testing.T) {
	store := mustOpenStore(t)
	sess := testSession("sess-1")

	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	link := session.Link{LinkType: session.LinkCommit, Ref: "def5678"}
	if err := store.AddLink(session.ID("sess-1"), link); err != nil {
		t.Fatalf("AddLink() error = %v", err)
	}

	got, err := store.Get(session.ID("sess-1"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	// Should have original link + new one
	if len(got.Links) != 2 {
		t.Fatalf("Links count = %d, want 2", len(got.Links))
	}

	found := false
	for _, l := range got.Links {
		if l.LinkType == session.LinkCommit && l.Ref == "def5678" {
			found = true
			break
		}
	}
	if !found {
		t.Error("AddLink: commit link not found after adding")
	}
}

// ── User tests ──

func TestSaveAndGetUser(t *testing.T) {
	store := mustOpenStore(t)

	user := &session.User{
		ID:     session.ID("user-1"),
		Name:   "Test User",
		Email:  "test@example.com",
		Source: "git",
	}

	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser() error = %v", err)
	}

	got, err := store.GetUser(session.ID("user-1"))
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetUser() returned nil")
	}
	if got.Name != "Test User" {
		t.Errorf("Name = %q, want %q", got.Name, "Test User")
	}
	if got.Email != "test@example.com" {
		t.Errorf("Email = %q, want %q", got.Email, "test@example.com")
	}
	if got.Source != "git" {
		t.Errorf("Source = %q, want %q", got.Source, "git")
	}
}

func TestGetUserByEmail(t *testing.T) {
	store := mustOpenStore(t)

	user := &session.User{
		ID:     session.ID("user-2"),
		Name:   "Email User",
		Email:  "email@example.com",
		Source: "git",
	}

	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser() error = %v", err)
	}

	got, err := store.GetUserByEmail("email@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetUserByEmail() returned nil")
	}
	if got.ID != session.ID("user-2") {
		t.Errorf("ID = %q, want %q", got.ID, "user-2")
	}
}

func TestGetUserByEmail_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	got, err := store.GetUserByEmail("nobody@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail() error = %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for unknown email, got %+v", got)
	}
}

func TestSaveUser_UpsertByEmail(t *testing.T) {
	store := mustOpenStore(t)

	user1 := &session.User{
		ID:     session.ID("user-a"),
		Name:   "Original Name",
		Email:  "same@example.com",
		Source: "git",
	}
	if err := store.SaveUser(user1); err != nil {
		t.Fatalf("SaveUser(1) error = %v", err)
	}

	// Save again with different ID but same email — should update name
	user2 := &session.User{
		ID:     session.ID("user-b"),
		Name:   "Updated Name",
		Email:  "same@example.com",
		Source: "config",
	}
	if err := store.SaveUser(user2); err != nil {
		t.Fatalf("SaveUser(2) error = %v", err)
	}

	// The original ID should still exist with updated name
	got, err := store.GetUserByEmail("same@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetUserByEmail() returned nil")
	}
	if got.Name != "Updated Name" {
		t.Errorf("Name = %q, want %q", got.Name, "Updated Name")
	}
}

// ── Search tests ──

func TestSearch_EmptyQuery(t *testing.T) {
	store := mustOpenStore(t)

	// Seed 3 sessions
	for _, id := range []string{"s-1", "s-2", "s-3"} {
		s := testSession(id)
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", id, err)
		}
	}

	result, err := store.Search(session.SearchQuery{})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.TotalCount != 3 {
		t.Errorf("TotalCount = %d, want 3", result.TotalCount)
	}
	if len(result.Sessions) != 3 {
		t.Errorf("Sessions count = %d, want 3", len(result.Sessions))
	}
	if result.Limit != 50 {
		t.Errorf("Limit = %d, want 50 (default)", result.Limit)
	}
	if result.Offset != 0 {
		t.Errorf("Offset = %d, want 0", result.Offset)
	}
}

func TestSearch_KeywordMatch(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("keyword-1")
	s1.Summary = "Implement OAuth2 login"
	s2 := testSession("keyword-2")
	s2.Summary = "Fix database migration"
	s3 := testSession("keyword-3")
	s3.Summary = "Refactor OAuth2 token handling"

	for _, s := range []*session.Session{s1, s2, s3} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	result, err := store.Search(session.SearchQuery{Keyword: "OAuth2"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.TotalCount != 2 {
		t.Errorf("TotalCount = %d, want 2 (matching OAuth2)", result.TotalCount)
	}
}

func TestSearch_KeywordCaseInsensitive(t *testing.T) {
	store := mustOpenStore(t)

	s := testSession("case-test")
	s.Summary = "Implement OAUTH2 Login"
	if err := store.Save(s); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// SQLite LIKE is case-insensitive for ASCII by default
	result, err := store.Search(session.SearchQuery{Keyword: "oauth2"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.TotalCount != 1 {
		t.Errorf("TotalCount = %d, want 1 (case-insensitive match)", result.TotalCount)
	}
}

func TestSearch_FilterByBranch(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("branch-1")
	s1.Branch = "feature/auth"
	s2 := testSession("branch-2")
	s2.Branch = "feature/api"
	s3 := testSession("branch-3")
	s3.Branch = "feature/auth"

	for _, s := range []*session.Session{s1, s2, s3} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	result, err := store.Search(session.SearchQuery{Branch: "feature/auth"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.TotalCount != 2 {
		t.Errorf("TotalCount = %d, want 2", result.TotalCount)
	}
}

func TestSearch_FilterByProvider(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("prov-1")
	s1.Provider = session.ProviderClaudeCode
	s2 := testSession("prov-2")
	s2.Provider = session.ProviderOpenCode
	s3 := testSession("prov-3")
	s3.Provider = session.ProviderClaudeCode

	for _, s := range []*session.Session{s1, s2, s3} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	result, err := store.Search(session.SearchQuery{Provider: session.ProviderOpenCode})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.TotalCount != 1 {
		t.Errorf("TotalCount = %d, want 1", result.TotalCount)
	}
	if len(result.Sessions) == 1 && result.Sessions[0].ID != "prov-2" {
		t.Errorf("Session ID = %s, want prov-2", result.Sessions[0].ID)
	}
}

func TestSearch_FilterByOwnerID(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("owner-1")
	s1.OwnerID = session.ID("user-alice")
	s2 := testSession("owner-2")
	s2.OwnerID = session.ID("user-bob")

	for _, s := range []*session.Session{s1, s2} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	result, err := store.Search(session.SearchQuery{OwnerID: session.ID("user-alice")})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.TotalCount != 1 {
		t.Errorf("TotalCount = %d, want 1", result.TotalCount)
	}
}

func TestSearch_FilterByProjectPath(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("proj-1")
	s1.ProjectPath = "/home/alice/project-a"
	s2 := testSession("proj-2")
	s2.ProjectPath = "/home/alice/project-b"

	for _, s := range []*session.Session{s1, s2} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	result, err := store.Search(session.SearchQuery{ProjectPath: "/home/alice/project-a"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.TotalCount != 1 {
		t.Errorf("TotalCount = %d, want 1", result.TotalCount)
	}
}

func TestSearch_FilterByTimeRange(t *testing.T) {
	store := mustOpenStore(t)

	jan := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	feb := time.Date(2026, 2, 15, 10, 0, 0, 0, time.UTC)
	mar := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)

	for i, ts := range []time.Time{jan, feb, mar} {
		s := testSession(fmt.Sprintf("time-%d", i+1))
		s.CreatedAt = ts
		s.ExportedAt = ts
		if err := store.Save(s); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	// Since Feb 1st => should get Feb + Mar sessions
	since := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	result, err := store.Search(session.SearchQuery{Since: since})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.TotalCount != 2 {
		t.Errorf("TotalCount (since Feb) = %d, want 2", result.TotalCount)
	}

	// Until Feb 28th => should get Jan + Feb sessions
	until := time.Date(2026, 2, 28, 23, 59, 59, 0, time.UTC)
	result, err = store.Search(session.SearchQuery{Until: until})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.TotalCount != 2 {
		t.Errorf("TotalCount (until Feb) = %d, want 2", result.TotalCount)
	}

	// Both since + until => only Feb
	result, err = store.Search(session.SearchQuery{Since: since, Until: until})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.TotalCount != 1 {
		t.Errorf("TotalCount (Feb only) = %d, want 1", result.TotalCount)
	}
}

func TestSearch_CombinedFilters(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("combo-1")
	s1.Branch = "feature/auth"
	s1.Provider = session.ProviderClaudeCode
	s1.Summary = "OAuth2 implementation"

	s2 := testSession("combo-2")
	s2.Branch = "feature/auth"
	s2.Provider = session.ProviderOpenCode
	s2.Summary = "OAuth2 refactor"

	s3 := testSession("combo-3")
	s3.Branch = "feature/api"
	s3.Provider = session.ProviderClaudeCode
	s3.Summary = "REST API endpoints"

	for _, s := range []*session.Session{s1, s2, s3} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	// Branch + Provider + Keyword — should match only s1
	result, err := store.Search(session.SearchQuery{
		Branch:   "feature/auth",
		Provider: session.ProviderClaudeCode,
		Keyword:  "OAuth2",
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.TotalCount != 1 {
		t.Errorf("TotalCount = %d, want 1", result.TotalCount)
	}
	if len(result.Sessions) == 1 && result.Sessions[0].ID != "combo-1" {
		t.Errorf("Session ID = %s, want combo-1", result.Sessions[0].ID)
	}
}

func TestSearch_Pagination(t *testing.T) {
	store := mustOpenStore(t)

	// Seed 5 sessions
	for i := 1; i <= 5; i++ {
		s := testSession(fmt.Sprintf("page-%d", i))
		if err := store.Save(s); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	// Get first 2
	result, err := store.Search(session.SearchQuery{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.TotalCount != 5 {
		t.Errorf("TotalCount = %d, want 5", result.TotalCount)
	}
	if len(result.Sessions) != 2 {
		t.Errorf("Sessions count = %d, want 2", len(result.Sessions))
	}
	if result.Limit != 2 {
		t.Errorf("Limit = %d, want 2", result.Limit)
	}

	// Get next 2
	result, err = store.Search(session.SearchQuery{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Sessions) != 2 {
		t.Errorf("Sessions count = %d, want 2", len(result.Sessions))
	}
	if result.Offset != 2 {
		t.Errorf("Offset = %d, want 2", result.Offset)
	}

	// Get last 1
	result, err = store.Search(session.SearchQuery{Limit: 2, Offset: 4})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Sessions) != 1 {
		t.Errorf("Sessions count = %d, want 1 (last page)", len(result.Sessions))
	}
}

func TestSearch_LimitClamping(t *testing.T) {
	store := mustOpenStore(t)

	s := testSession("clamp-1")
	if err := store.Save(s); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Limit exceeding max should be clamped to 200
	result, err := store.Search(session.SearchQuery{Limit: 999})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Limit != 200 {
		t.Errorf("Limit = %d, want 200 (clamped)", result.Limit)
	}

	// Zero limit should use default 50
	result, err = store.Search(session.SearchQuery{Limit: 0})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Limit != 50 {
		t.Errorf("Limit = %d, want 50 (default)", result.Limit)
	}
}

func TestSearch_NoResults(t *testing.T) {
	store := mustOpenStore(t)

	result, err := store.Search(session.SearchQuery{Keyword: "nonexistent"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.TotalCount != 0 {
		t.Errorf("TotalCount = %d, want 0", result.TotalCount)
	}
	if result.Sessions == nil {
		t.Error("Sessions should be empty slice, not nil")
	}
	if len(result.Sessions) != 0 {
		t.Errorf("Sessions count = %d, want 0", len(result.Sessions))
	}
}

func TestSearch_KeywordEscapesWildcards(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("wild-1")
	s1.Summary = "100% complete feature"
	s2 := testSession("wild-2")
	s2.Summary = "user_name validation"
	s3 := testSession("wild-3")
	s3.Summary = "normal summary"

	for _, s := range []*session.Session{s1, s2, s3} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	// Searching for literal "%" should only match the one with "%" in summary
	result, err := store.Search(session.SearchQuery{Keyword: "100%"})
	if err != nil {
		t.Fatalf("Search(100%%) error = %v", err)
	}
	if result.TotalCount != 1 {
		t.Errorf("TotalCount for '100%%' = %d, want 1 (only literal match)", result.TotalCount)
	}

	// Searching for literal "_" should only match the one with "_" in summary
	result, err = store.Search(session.SearchQuery{Keyword: "user_name"})
	if err != nil {
		t.Fatalf("Search(user_name) error = %v", err)
	}
	if result.TotalCount != 1 {
		t.Errorf("TotalCount for 'user_name' = %d, want 1 (only literal match)", result.TotalCount)
	}
}

func TestSearch_ResultFields(t *testing.T) {
	store := mustOpenStore(t)

	s := testSession("fields-1")
	s.Provider = session.ProviderOpenCode
	s.Agent = "coder"
	s.Branch = "feature/search"
	s.Summary = "Search feature test"
	s.OwnerID = session.ID("user-test")
	if err := store.Save(s); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	result, err := store.Search(session.SearchQuery{})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("Sessions count = %d, want 1", len(result.Sessions))
	}

	got := result.Sessions[0]
	if got.ID != "fields-1" {
		t.Errorf("ID = %s, want fields-1", got.ID)
	}
	if got.Provider != session.ProviderOpenCode {
		t.Errorf("Provider = %s, want opencode", got.Provider)
	}
	if got.Agent != "coder" {
		t.Errorf("Agent = %s, want coder", got.Agent)
	}
	if got.Branch != "feature/search" {
		t.Errorf("Branch = %s, want feature/search", got.Branch)
	}
	if got.Summary != "Search feature test" {
		t.Errorf("Summary = %s, want 'Search feature test'", got.Summary)
	}
	if got.OwnerID != session.ID("user-test") {
		t.Errorf("OwnerID = %s, want user-test", got.OwnerID)
	}
}

func TestSearch_FilterByStatus(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("active-sess")
	s1.Status = session.StatusActive
	s1.Summary = "Active session"
	if err := store.Save(s1); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	s2 := testSession("idle-sess")
	s2.Status = session.StatusIdle
	s2.Summary = "Idle session"
	if err := store.Save(s2); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Filter by active
	result, err := store.Search(session.SearchQuery{Status: session.StatusActive})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("expected 1 active session, got %d", len(result.Sessions))
	}
	if result.Sessions[0].ID != "active-sess" {
		t.Errorf("ID = %s, want active-sess", result.Sessions[0].ID)
	}
	if result.Sessions[0].Status != session.StatusActive {
		t.Errorf("Status = %s, want active", result.Sessions[0].Status)
	}

	// Filter by idle
	result, err = store.Search(session.SearchQuery{Status: session.StatusIdle})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("expected 1 idle session, got %d", len(result.Sessions))
	}
	if result.Sessions[0].ID != "idle-sess" {
		t.Errorf("ID = %s, want idle-sess", result.Sessions[0].ID)
	}
}

func TestSearch_FilterByHasErrors(t *testing.T) {
	store := mustOpenStore(t)

	// Session with errors (tool call with error state)
	s1 := testSession("error-sess")
	s1.Summary = "Session with errors"
	s1.Messages = append(s1.Messages, session.Message{
		ID:   "msg-err",
		Role: session.RoleAssistant,
		ToolCalls: []session.ToolCall{
			{Name: "bash", State: session.ToolStateError, Output: "exit code 1"},
		},
	})
	if err := store.Save(s1); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Session without errors
	s2 := testSession("clean-sess")
	s2.Summary = "Clean session"
	if err := store.Save(s2); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Filter: has errors
	hasErrors := true
	result, err := store.Search(session.SearchQuery{HasErrors: &hasErrors})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("expected 1 session with errors, got %d", len(result.Sessions))
	}
	if result.Sessions[0].ID != "error-sess" {
		t.Errorf("ID = %s, want error-sess", result.Sessions[0].ID)
	}

	// Filter: no errors
	noErrors := false
	result, err = store.Search(session.SearchQuery{HasErrors: &noErrors})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("expected 1 session without errors, got %d", len(result.Sessions))
	}
	if result.Sessions[0].ID != "clean-sess" {
		t.Errorf("ID = %s, want clean-sess", result.Sessions[0].ID)
	}

	// No filter: should return both
	result, err = store.Search(session.SearchQuery{})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Sessions) != 2 {
		t.Errorf("expected 2 sessions with no filter, got %d", len(result.Sessions))
	}
}

func TestSearch_StatusAndHasErrorsCombined(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("active-error")
	s1.Status = session.StatusActive
	s1.Summary = "Active with errors"
	s1.Messages = append(s1.Messages, session.Message{
		ID: "msg-ae", Role: session.RoleAssistant,
		ToolCalls: []session.ToolCall{{Name: "bash", State: session.ToolStateError}},
	})
	if err := store.Save(s1); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	s2 := testSession("active-clean")
	s2.Status = session.StatusActive
	s2.Summary = "Active no errors"
	if err := store.Save(s2); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	s3 := testSession("idle-error")
	s3.Status = session.StatusIdle
	s3.Summary = "Idle with errors"
	s3.Messages = append(s3.Messages, session.Message{
		ID: "msg-ie", Role: session.RoleAssistant,
		ToolCalls: []session.ToolCall{{Name: "bash", State: session.ToolStateError}},
	})
	if err := store.Save(s3); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Active + has errors = only s1
	hasErrors := true
	result, err := store.Search(session.SearchQuery{
		Status:    session.StatusActive,
		HasErrors: &hasErrors,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("expected 1, got %d", len(result.Sessions))
	}
	if result.Sessions[0].ID != "active-error" {
		t.Errorf("ID = %s, want active-error", result.Sessions[0].ID)
	}
}

func TestSearch_ProjectCategoryAndStatusInResults(t *testing.T) {
	store := mustOpenStore(t)

	s := testSession("full-fields")
	s.ProjectCategory = "backend"
	s.Status = session.StatusActive
	s.Summary = "Full field test"
	if err := store.Save(s); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	result, err := store.Search(session.SearchQuery{})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("expected 1, got %d", len(result.Sessions))
	}
	if result.Sessions[0].ProjectCategory != "backend" {
		t.Errorf("ProjectCategory = %q, want %q", result.Sessions[0].ProjectCategory, "backend")
	}
	if result.Sessions[0].Status != session.StatusActive {
		t.Errorf("Status = %q, want %q", result.Sessions[0].Status, session.StatusActive)
	}
}

func TestSessionWithOwnerID(t *testing.T) {
	store := mustOpenStore(t)

	// Create a user
	user := &session.User{
		ID:     session.ID("owner-1"),
		Name:   "Owner",
		Email:  "owner@example.com",
		Source: "git",
	}
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser() error = %v", err)
	}

	// Create a session with owner_id
	sess := testSession("owned-session")
	sess.OwnerID = user.ID
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Get and verify owner_id persists through JSON payload
	got, err := store.Get(session.ID("owned-session"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.OwnerID != user.ID {
		t.Errorf("OwnerID = %q, want %q", got.OwnerID, user.ID)
	}

	// Verify owner_id appears in List too
	summaries, err := store.List(session.ListOptions{All: true})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(summaries) == 0 {
		t.Fatal("List() returned empty")
	}
	if summaries[0].OwnerID != user.ID {
		t.Errorf("Summary.OwnerID = %q, want %q", summaries[0].OwnerID, user.ID)
	}
}

// ── Blame (GetSessionsByFile) ──

func TestGetSessionsByFile_Basic(t *testing.T) {
	store := mustOpenStore(t)

	sess := testSession("blame-1")
	sess.FileChanges = []session.FileChange{
		{FilePath: "src/handler.go", ChangeType: session.ChangeModified},
		{FilePath: "src/main.go", ChangeType: session.ChangeCreated},
	}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	entries, err := store.GetSessionsByFile(session.BlameQuery{
		FilePath: "src/handler.go",
	})
	if err != nil {
		t.Fatalf("GetSessionsByFile() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].SessionID != "blame-1" {
		t.Errorf("SessionID = %q, want blame-1", entries[0].SessionID)
	}
	if entries[0].ChangeType != session.ChangeModified {
		t.Errorf("ChangeType = %q, want modified", entries[0].ChangeType)
	}
	if entries[0].Provider != session.ProviderClaudeCode {
		t.Errorf("Provider = %q, want claude-code", entries[0].Provider)
	}
	if entries[0].Branch != "feature/auth" {
		t.Errorf("Branch = %q, want feature/auth", entries[0].Branch)
	}
}

func TestGetSessionsByFile_NoMatch(t *testing.T) {
	store := mustOpenStore(t)

	sess := testSession("blame-nomatch")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	entries, err := store.GetSessionsByFile(session.BlameQuery{
		FilePath: "nonexistent.go",
	})
	if err != nil {
		t.Fatalf("GetSessionsByFile() error = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestGetSessionsByFile_MultipleSessions(t *testing.T) {
	store := mustOpenStore(t)

	// Two sessions touching the same file
	sess1 := testSession("blame-multi-1")
	sess1.CreatedAt = time.Date(2026, 2, 16, 10, 0, 0, 0, time.UTC)
	sess1.FileChanges = []session.FileChange{
		{FilePath: "shared.go", ChangeType: session.ChangeModified},
	}
	sess2 := testSession("blame-multi-2")
	sess2.CreatedAt = time.Date(2026, 2, 17, 10, 0, 0, 0, time.UTC)
	sess2.FileChanges = []session.FileChange{
		{FilePath: "shared.go", ChangeType: session.ChangeCreated},
	}

	if err := store.Save(sess1); err != nil {
		t.Fatalf("Save(1) error = %v", err)
	}
	if err := store.Save(sess2); err != nil {
		t.Fatalf("Save(2) error = %v", err)
	}

	entries, err := store.GetSessionsByFile(session.BlameQuery{
		FilePath: "shared.go",
	})
	if err != nil {
		t.Fatalf("GetSessionsByFile() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// Most recent first
	if entries[0].SessionID != "blame-multi-2" {
		t.Errorf("first entry SessionID = %q, want blame-multi-2", entries[0].SessionID)
	}
}

func TestGetSessionsByFile_Limit(t *testing.T) {
	store := mustOpenStore(t)

	for i := 0; i < 5; i++ {
		sess := testSession(fmt.Sprintf("blame-limit-%d", i))
		sess.CreatedAt = time.Date(2026, 2, 16+i, 10, 0, 0, 0, time.UTC)
		sess.FileChanges = []session.FileChange{
			{FilePath: "limited.go", ChangeType: session.ChangeModified},
		}
		if err := store.Save(sess); err != nil {
			t.Fatalf("Save(%d) error = %v", i, err)
		}
	}

	entries, err := store.GetSessionsByFile(session.BlameQuery{
		FilePath: "limited.go",
		Limit:    2,
	})
	if err != nil {
		t.Fatalf("GetSessionsByFile() error = %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestGetSessionsByFile_FilterByBranch(t *testing.T) {
	store := mustOpenStore(t)

	sess1 := testSession("blame-branch-1")
	sess1.Branch = "feat/a"
	sess1.FileChanges = []session.FileChange{
		{FilePath: "branched.go", ChangeType: session.ChangeModified},
	}
	sess2 := testSession("blame-branch-2")
	sess2.Branch = "feat/b"
	sess2.FileChanges = []session.FileChange{
		{FilePath: "branched.go", ChangeType: session.ChangeCreated},
	}

	if err := store.Save(sess1); err != nil {
		t.Fatalf("Save(1) error = %v", err)
	}
	if err := store.Save(sess2); err != nil {
		t.Fatalf("Save(2) error = %v", err)
	}

	entries, err := store.GetSessionsByFile(session.BlameQuery{
		FilePath: "branched.go",
		Branch:   "feat/a",
	})
	if err != nil {
		t.Fatalf("GetSessionsByFile() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].SessionID != "blame-branch-1" {
		t.Errorf("SessionID = %q, want blame-branch-1", entries[0].SessionID)
	}
}

func TestGetSessionsByFile_FilterByProvider(t *testing.T) {
	store := mustOpenStore(t)

	sess1 := testSession("blame-prov-1")
	sess1.Provider = session.ProviderClaudeCode
	sess1.FileChanges = []session.FileChange{
		{FilePath: "prov.go", ChangeType: session.ChangeModified},
	}
	sess2 := testSession("blame-prov-2")
	sess2.Provider = session.ProviderOpenCode
	sess2.FileChanges = []session.FileChange{
		{FilePath: "prov.go", ChangeType: session.ChangeCreated},
	}

	if err := store.Save(sess1); err != nil {
		t.Fatalf("Save(1) error = %v", err)
	}
	if err := store.Save(sess2); err != nil {
		t.Fatalf("Save(2) error = %v", err)
	}

	entries, err := store.GetSessionsByFile(session.BlameQuery{
		FilePath: "prov.go",
		Provider: session.ProviderOpenCode,
	})
	if err != nil {
		t.Fatalf("GetSessionsByFile() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].SessionID != "blame-prov-2" {
		t.Errorf("SessionID = %q, want blame-prov-2", entries[0].SessionID)
	}
}

// ── FilesForProject ──

func TestFilesForProject_Basic(t *testing.T) {
	store := mustOpenStore(t)

	sess := testSession("fp-basic-1")
	sess.ProjectPath = "/home/user/myproj"
	sess.FileChanges = []session.FileChange{
		{FilePath: "/home/user/myproj/src/handler.go", ChangeType: session.ChangeModified},
		{FilePath: "/home/user/myproj/src/main.go", ChangeType: session.ChangeCreated},
		{FilePath: "/home/user/myproj/README.md", ChangeType: session.ChangeModified},
	}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	entries, err := store.FilesForProject("/home/user/myproj", "", 100)
	if err != nil {
		t.Fatalf("FilesForProject() error = %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Verify one of the entries.
	found := false
	for _, e := range entries {
		if e.FilePath == "/home/user/myproj/src/handler.go" {
			found = true
			if e.SessionCount != 1 {
				t.Errorf("SessionCount = %d, want 1", e.SessionCount)
			}
			if e.LastSessionID != "fp-basic-1" {
				t.Errorf("LastSessionID = %q, want fp-basic-1", e.LastSessionID)
			}
			if e.LastChangeType != session.ChangeModified {
				t.Errorf("LastChangeType = %q, want modified", e.LastChangeType)
			}
		}
	}
	if !found {
		t.Error("expected to find src/handler.go in results")
	}
}

func TestFilesForProject_MultipleSessions(t *testing.T) {
	store := mustOpenStore(t)

	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	sess1 := testSession("fp-multi-1")
	sess1.ProjectPath = "/home/user/proj2"
	sess1.CreatedAt = now
	sess1.FileChanges = []session.FileChange{
		{FilePath: "/home/user/proj2/app.go", ChangeType: session.ChangeModified},
	}

	sess2 := testSession("fp-multi-2")
	sess2.ProjectPath = "/home/user/proj2"
	sess2.CreatedAt = now.Add(1 * time.Hour)
	sess2.Summary = "Second session"
	sess2.FileChanges = []session.FileChange{
		{FilePath: "/home/user/proj2/app.go", ChangeType: session.ChangeCreated},
	}

	if err := store.Save(sess1); err != nil {
		t.Fatalf("Save(1) error = %v", err)
	}
	if err := store.Save(sess2); err != nil {
		t.Fatalf("Save(2) error = %v", err)
	}

	entries, err := store.FilesForProject("/home/user/proj2", "", 100)
	if err != nil {
		t.Fatalf("FilesForProject() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (aggregated), got %d", len(entries))
	}
	e := entries[0]
	if e.SessionCount != 2 {
		t.Errorf("SessionCount = %d, want 2", e.SessionCount)
	}
	// Last session should be the more recent one.
	if e.LastSessionID != "fp-multi-2" {
		t.Errorf("LastSessionID = %q, want fp-multi-2", e.LastSessionID)
	}
}

func TestFilesForProject_DirPrefix(t *testing.T) {
	store := mustOpenStore(t)

	sess := testSession("fp-dir-1")
	sess.ProjectPath = "/home/user/proj3"
	sess.FileChanges = []session.FileChange{
		{FilePath: "/home/user/proj3/src/handler.go", ChangeType: session.ChangeModified},
		{FilePath: "/home/user/proj3/src/main.go", ChangeType: session.ChangeCreated},
		{FilePath: "/home/user/proj3/docs/README.md", ChangeType: session.ChangeModified},
	}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Filter by "src" prefix.
	entries, err := store.FilesForProject("/home/user/proj3", "/home/user/proj3/src", 100)
	if err != nil {
		t.Fatalf("FilesForProject() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries under src/, got %d", len(entries))
	}
	for _, e := range entries {
		if e.FilePath != "/home/user/proj3/src/handler.go" && e.FilePath != "/home/user/proj3/src/main.go" {
			t.Errorf("unexpected file: %s", e.FilePath)
		}
	}
}

func TestFilesForProject_ExcludesOutsidePaths(t *testing.T) {
	store := mustOpenStore(t)

	sess := testSession("fp-outside-1")
	sess.ProjectPath = "/home/user/proj4"
	sess.FileChanges = []session.FileChange{
		{FilePath: "/home/user/proj4/app.go", ChangeType: session.ChangeModified},
		{FilePath: "/home/user/.config/tool.json", ChangeType: session.ChangeRead},
		{FilePath: "2>/dev/null", ChangeType: session.ChangeRead},
	}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	entries, err := store.FilesForProject("/home/user/proj4", "", 100)
	if err != nil {
		t.Fatalf("FilesForProject() error = %v", err)
	}
	// Only app.go should be returned (paths outside project are excluded).
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (project files only), got %d", len(entries))
	}
	if entries[0].FilePath != "/home/user/proj4/app.go" {
		t.Errorf("FilePath = %q, want /home/user/proj4/app.go", entries[0].FilePath)
	}
}

func TestFilesForProject_Empty(t *testing.T) {
	store := mustOpenStore(t)

	entries, err := store.FilesForProject("/nonexistent/project", "", 100)
	if err != nil {
		t.Fatalf("FilesForProject() error = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

// ── DeleteOlderThan ──

func TestDeleteOlderThan(t *testing.T) {
	store := mustOpenStore(t)
	now := time.Now().UTC()

	// Create 3 sessions: 2 old, 1 recent
	old1 := testSession("gc-old-1")
	old1.CreatedAt = now.Add(-40 * 24 * time.Hour)
	old2 := testSession("gc-old-2")
	old2.CreatedAt = now.Add(-35 * 24 * time.Hour)
	recent := testSession("gc-recent")
	recent.CreatedAt = now.Add(-24 * time.Hour)

	for _, s := range []*session.Session{old1, old2, recent} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	// Delete sessions older than 30 days
	cutoff := now.Add(-30 * 24 * time.Hour)
	deleted, err := store.DeleteOlderThan(cutoff)
	if err != nil {
		t.Fatalf("DeleteOlderThan() error = %v", err)
	}
	if deleted != 2 {
		t.Errorf("expected 2 deleted, got %d", deleted)
	}

	// Verify old sessions are gone
	_, err = store.Get("gc-old-1")
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound for gc-old-1, got %v", err)
	}
	_, err = store.Get("gc-old-2")
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound for gc-old-2, got %v", err)
	}

	// Verify recent session still exists
	got, err := store.Get("gc-recent")
	if err != nil {
		t.Fatalf("Get(gc-recent) error = %v", err)
	}
	if got.ID != "gc-recent" {
		t.Errorf("ID = %q, want gc-recent", got.ID)
	}
}

func TestDeleteOlderThan_noneMatch(t *testing.T) {
	store := mustOpenStore(t)

	recent := testSession("gc-none")
	recent.CreatedAt = time.Now().UTC().Add(-24 * time.Hour)
	if err := store.Save(recent); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	deleted, err := store.DeleteOlderThan(cutoff)
	if err != nil {
		t.Fatalf("DeleteOlderThan() error = %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted, got %d", deleted)
	}
}

func TestDeleteOlderThan_cascadesLinksAndFiles(t *testing.T) {
	store := mustOpenStore(t)

	old := testSession("gc-cascade")
	old.CreatedAt = time.Now().UTC().Add(-40 * 24 * time.Hour)
	old.FileChanges = []session.FileChange{
		{FilePath: "main.go", ChangeType: session.ChangeModified},
	}
	if err := store.Save(old); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	if err := store.AddLink("gc-cascade", session.Link{LinkType: session.LinkPR, Ref: "42"}); err != nil {
		t.Fatalf("AddLink error = %v", err)
	}

	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	deleted, err := store.DeleteOlderThan(cutoff)
	if err != nil {
		t.Fatalf("DeleteOlderThan() error = %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	// File changes and links should be cascade-deleted
	entries, err := store.GetSessionsByFile(session.BlameQuery{FilePath: "main.go"})
	if err != nil {
		t.Fatalf("GetSessionsByFile error = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 file change entries after cascade, got %d", len(entries))
	}

	_, err = store.GetByLink(session.LinkPR, "42")
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound for link after cascade, got %v", err)
	}
}

// ── User Preferences Tests ──

func TestPreferences_SaveAndGet(t *testing.T) {
	store := mustOpenStore(t)

	// Initially no preferences — should return nil.
	prefs, err := store.GetPreferences("")
	if err != nil {
		t.Fatalf("GetPreferences() error = %v", err)
	}
	if prefs != nil {
		t.Fatalf("expected nil prefs for new store, got %+v", prefs)
	}

	// Save global defaults (empty user ID).
	err = store.SavePreferences(&session.UserPreferences{
		Dashboard: session.DashboardPreferences{
			PageSize:  50,
			Columns:   []string{"id", "provider", "cost", "when"},
			SortBy:    "tokens",
			SortOrder: "asc",
		},
	})
	if err != nil {
		t.Fatalf("SavePreferences() error = %v", err)
	}

	// Read back.
	prefs, err = store.GetPreferences("")
	if err != nil {
		t.Fatalf("GetPreferences() error = %v", err)
	}
	if prefs == nil {
		t.Fatal("expected non-nil prefs after save")
	}
	if prefs.Dashboard.PageSize != 50 {
		t.Errorf("PageSize = %d, want 50", prefs.Dashboard.PageSize)
	}
	if len(prefs.Dashboard.Columns) != 4 {
		t.Errorf("Columns count = %d, want 4", len(prefs.Dashboard.Columns))
	}
	if prefs.Dashboard.SortBy != "tokens" {
		t.Errorf("SortBy = %q, want %q", prefs.Dashboard.SortBy, "tokens")
	}
	if prefs.Dashboard.SortOrder != "asc" {
		t.Errorf("SortOrder = %q, want %q", prefs.Dashboard.SortOrder, "asc")
	}
}

func TestPreferences_UserOverride(t *testing.T) {
	store := mustOpenStore(t)

	// Save global defaults.
	_ = store.SavePreferences(&session.UserPreferences{
		Dashboard: session.DashboardPreferences{PageSize: 25},
	})

	// Save user-specific preferences.
	_ = store.SavePreferences(&session.UserPreferences{
		UserID:    "user-alice",
		Dashboard: session.DashboardPreferences{PageSize: 100},
	})

	// Global should still be 25.
	global, _ := store.GetPreferences("")
	if global.Dashboard.PageSize != 25 {
		t.Errorf("global PageSize = %d, want 25", global.Dashboard.PageSize)
	}

	// Alice should be 100.
	alice, _ := store.GetPreferences("user-alice")
	if alice.Dashboard.PageSize != 100 {
		t.Errorf("alice PageSize = %d, want 100", alice.Dashboard.PageSize)
	}

	// Unknown user should return nil (fall back to defaults).
	unknown, _ := store.GetPreferences("user-bob")
	if unknown != nil {
		t.Errorf("expected nil for unknown user, got %+v", unknown)
	}
}

func TestPreferences_Upsert(t *testing.T) {
	store := mustOpenStore(t)

	// Save once.
	_ = store.SavePreferences(&session.UserPreferences{
		Dashboard: session.DashboardPreferences{PageSize: 25},
	})

	// Update (upsert).
	_ = store.SavePreferences(&session.UserPreferences{
		Dashboard: session.DashboardPreferences{PageSize: 75, SortBy: "cost"},
	})

	prefs, _ := store.GetPreferences("")
	if prefs.Dashboard.PageSize != 75 {
		t.Errorf("PageSize after upsert = %d, want 75", prefs.Dashboard.PageSize)
	}
	if prefs.Dashboard.SortBy != "cost" {
		t.Errorf("SortBy after upsert = %q, want %q", prefs.Dashboard.SortBy, "cost")
	}
}

// ── Cache Tests ──

func TestCache_MissAndPopulate(t *testing.T) {
	store := mustOpenStore(t)

	// Miss — returns nil.
	data, err := store.GetCache("stats:all", 30*time.Second)
	if err != nil {
		t.Fatalf("GetCache error = %v", err)
	}
	if data != nil {
		t.Fatalf("expected nil on miss, got %d bytes", len(data))
	}

	// Set.
	payload := []byte(`{"total_sessions":42}`)
	if err := store.SetCache("stats:all", payload); err != nil {
		t.Fatalf("SetCache error = %v", err)
	}

	// Hit.
	data, err = store.GetCache("stats:all", 30*time.Second)
	if err != nil {
		t.Fatalf("GetCache error = %v", err)
	}
	if string(data) != string(payload) {
		t.Errorf("got %q, want %q", string(data), string(payload))
	}
}

func TestCache_TTLExpiry(t *testing.T) {
	store := mustOpenStore(t)

	_ = store.SetCache("stats:all", []byte(`{}`))

	// With very short TTL, it should be treated as expired immediately.
	// We use 0 duration to simulate "already expired".
	data, _ := store.GetCache("stats:all", 0)
	if data != nil {
		t.Error("expected nil for expired cache entry")
	}

	// With long TTL, it should still be valid.
	data, _ = store.GetCache("stats:all", 1*time.Hour)
	if data == nil {
		t.Error("expected non-nil for valid cache entry")
	}
}

func TestCache_InvalidateByPrefix(t *testing.T) {
	store := mustOpenStore(t)

	_ = store.SetCache("stats:projectA", []byte(`{"a":1}`))
	_ = store.SetCache("stats:projectB", []byte(`{"b":2}`))
	_ = store.SetCache("forecast:projectA", []byte(`{"f":3}`))

	// Invalidate only stats:*
	if err := store.InvalidateCache("stats:"); err != nil {
		t.Fatalf("InvalidateCache error = %v", err)
	}

	// stats entries should be gone.
	data, _ := store.GetCache("stats:projectA", 1*time.Hour)
	if data != nil {
		t.Error("expected nil for invalidated stats:projectA")
	}
	data, _ = store.GetCache("stats:projectB", 1*time.Hour)
	if data != nil {
		t.Error("expected nil for invalidated stats:projectB")
	}

	// forecast should still be there.
	data, _ = store.GetCache("forecast:projectA", 1*time.Hour)
	if data == nil {
		t.Error("expected non-nil for forecast:projectA (not invalidated)")
	}
}

func TestCache_InvalidateAll(t *testing.T) {
	store := mustOpenStore(t)

	_ = store.SetCache("stats:all", []byte(`{}`))
	_ = store.SetCache("forecast:all", []byte(`{}`))

	// Invalidate everything.
	if err := store.InvalidateCache(""); err != nil {
		t.Fatalf("InvalidateCache error = %v", err)
	}

	data, _ := store.GetCache("stats:all", 1*time.Hour)
	if data != nil {
		t.Error("expected nil after full invalidation")
	}
	data, _ = store.GetCache("forecast:all", 1*time.Hour)
	if data != nil {
		t.Error("expected nil after full invalidation")
	}
}

func TestCache_InvalidatedOnSave(t *testing.T) {
	store := mustOpenStore(t)

	// Populate cache.
	_ = store.SetCache("stats:all", []byte(`{"cached":true}`))

	// Save a session — should auto-invalidate cache.
	sess := &session.Session{
		ID:       "cache-test-1",
		Provider: "opencode",
		Messages: []session.Message{
			{ID: "m1", Role: "user", Content: "hello"},
		},
	}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	// Cache should be invalidated.
	data, _ := store.GetCache("stats:all", 1*time.Hour)
	if data != nil {
		t.Error("expected cache invalidated after Save()")
	}
}

func TestCache_Upsert(t *testing.T) {
	store := mustOpenStore(t)

	_ = store.SetCache("stats:all", []byte(`{"v":1}`))
	_ = store.SetCache("stats:all", []byte(`{"v":2}`))

	data, _ := store.GetCache("stats:all", 1*time.Hour)
	if string(data) != `{"v":2}` {
		t.Errorf("expected upserted value, got %q", string(data))
	}
}

// ── Session Analysis Tests ──

func testAnalysis(id, sessionID string) *analysis.SessionAnalysis {
	return &analysis.SessionAnalysis{
		ID:         id,
		SessionID:  sessionID,
		CreatedAt:  time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
		Trigger:    analysis.TriggerManual,
		Adapter:    analysis.AdapterLLM,
		Model:      "gpt-4o",
		TokensUsed: 500,
		DurationMs: 1200,
		Report: analysis.AnalysisReport{
			Score:   85,
			Summary: "Good session overall",
			Problems: []analysis.Problem{
				{Severity: analysis.SeverityLow, Description: "Minor style issue"},
			},
			Recommendations: []analysis.Recommendation{
				{Category: analysis.CategorySkill, Title: "Use structured output", Description: "Consider using JSON mode", Priority: 3},
			},
		},
	}
}

func TestSaveAndGetAnalysis(t *testing.T) {
	store := mustOpenStore(t)

	// Save a session first (required for foreign key)
	sess := testSession("analysis-sess-1")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save session error = %v", err)
	}

	a := testAnalysis("analysis-1", "analysis-sess-1")
	if err := store.SaveAnalysis(a); err != nil {
		t.Fatalf("SaveAnalysis() error = %v", err)
	}

	got, err := store.GetAnalysis("analysis-1")
	if err != nil {
		t.Fatalf("GetAnalysis() error = %v", err)
	}

	if got.ID != "analysis-1" {
		t.Errorf("ID = %q, want %q", got.ID, "analysis-1")
	}
	if got.SessionID != "analysis-sess-1" {
		t.Errorf("SessionID = %q, want %q", got.SessionID, "analysis-sess-1")
	}
	if got.Trigger != analysis.TriggerManual {
		t.Errorf("Trigger = %q, want %q", got.Trigger, analysis.TriggerManual)
	}
	if got.Adapter != analysis.AdapterLLM {
		t.Errorf("Adapter = %q, want %q", got.Adapter, analysis.AdapterLLM)
	}
	if got.Model != "gpt-4o" {
		t.Errorf("Model = %q, want %q", got.Model, "gpt-4o")
	}
	if got.TokensUsed != 500 {
		t.Errorf("TokensUsed = %d, want 500", got.TokensUsed)
	}
	if got.DurationMs != 1200 {
		t.Errorf("DurationMs = %d, want 1200", got.DurationMs)
	}
	if got.Report.Score != 85 {
		t.Errorf("Report.Score = %d, want 85", got.Report.Score)
	}
	if got.Report.Summary != "Good session overall" {
		t.Errorf("Report.Summary = %q, want %q", got.Report.Summary, "Good session overall")
	}
	if len(got.Report.Problems) != 1 {
		t.Errorf("Report.Problems count = %d, want 1", len(got.Report.Problems))
	}
	if len(got.Report.Recommendations) != 1 {
		t.Errorf("Report.Recommendations count = %d, want 1", len(got.Report.Recommendations))
	}
}

func TestGetAnalysis_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	_, err := store.GetAnalysis("nonexistent")
	if !errors.Is(err, storage.ErrAnalysisNotFound) {
		t.Errorf("GetAnalysis(nonexistent) error = %v, want ErrAnalysisNotFound", err)
	}
}

func TestSaveAnalysis_Upsert(t *testing.T) {
	store := mustOpenStore(t)

	sess := testSession("upsert-analysis-sess")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save session error = %v", err)
	}

	a := testAnalysis("upsert-analysis", "upsert-analysis-sess")
	if err := store.SaveAnalysis(a); err != nil {
		t.Fatalf("SaveAnalysis(1) error = %v", err)
	}

	// Update the analysis
	a.Report.Score = 95
	a.Report.Summary = "Updated analysis"
	if err := store.SaveAnalysis(a); err != nil {
		t.Fatalf("SaveAnalysis(2) error = %v", err)
	}

	got, err := store.GetAnalysis("upsert-analysis")
	if err != nil {
		t.Fatalf("GetAnalysis() error = %v", err)
	}
	if got.Report.Score != 95 {
		t.Errorf("Report.Score = %d, want 95 (upserted)", got.Report.Score)
	}
	if got.Report.Summary != "Updated analysis" {
		t.Errorf("Report.Summary = %q, want %q", got.Report.Summary, "Updated analysis")
	}
}

func TestSaveAnalysis_WithError(t *testing.T) {
	store := mustOpenStore(t)

	sess := testSession("error-analysis-sess")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save session error = %v", err)
	}

	a := &analysis.SessionAnalysis{
		ID:        "error-analysis",
		SessionID: "error-analysis-sess",
		CreatedAt: time.Now().UTC(),
		Trigger:   analysis.TriggerAuto,
		Adapter:   analysis.AdapterLLM,
		Error:     "LLM request failed: rate limited",
	}
	if err := store.SaveAnalysis(a); err != nil {
		t.Fatalf("SaveAnalysis() error = %v", err)
	}

	got, err := store.GetAnalysis("error-analysis")
	if err != nil {
		t.Fatalf("GetAnalysis() error = %v", err)
	}
	if got.Error != "LLM request failed: rate limited" {
		t.Errorf("Error = %q, want error string", got.Error)
	}
}

func TestGetAnalysisBySession(t *testing.T) {
	store := mustOpenStore(t)

	sess := testSession("multi-analysis-sess")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save session error = %v", err)
	}

	// Save two analyses for the same session (different times)
	a1 := testAnalysis("multi-a1", "multi-analysis-sess")
	a1.CreatedAt = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	a1.Report.Summary = "First analysis"
	if err := store.SaveAnalysis(a1); err != nil {
		t.Fatalf("SaveAnalysis(1) error = %v", err)
	}

	a2 := testAnalysis("multi-a2", "multi-analysis-sess")
	a2.CreatedAt = time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	a2.Report.Summary = "Second analysis"
	if err := store.SaveAnalysis(a2); err != nil {
		t.Fatalf("SaveAnalysis(2) error = %v", err)
	}

	// GetAnalysisBySession returns the most recent
	got, err := store.GetAnalysisBySession("multi-analysis-sess")
	if err != nil {
		t.Fatalf("GetAnalysisBySession() error = %v", err)
	}
	if got.ID != "multi-a2" {
		t.Errorf("ID = %q, want %q (most recent)", got.ID, "multi-a2")
	}
	if got.Report.Summary != "Second analysis" {
		t.Errorf("Summary = %q, want %q", got.Report.Summary, "Second analysis")
	}
}

func TestGetAnalysisBySession_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	_, err := store.GetAnalysisBySession("nonexistent-session")
	if !errors.Is(err, storage.ErrAnalysisNotFound) {
		t.Errorf("GetAnalysisBySession(nonexistent) error = %v, want ErrAnalysisNotFound", err)
	}
}

func TestListAnalyses(t *testing.T) {
	store := mustOpenStore(t)

	sess := testSession("list-analysis-sess")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save session error = %v", err)
	}

	// Save 3 analyses
	for i := 1; i <= 3; i++ {
		a := testAnalysis(fmt.Sprintf("list-a%d", i), "list-analysis-sess")
		a.CreatedAt = time.Date(2026, 3, i, 10, 0, 0, 0, time.UTC)
		a.Report.Summary = fmt.Sprintf("Analysis #%d", i)
		if err := store.SaveAnalysis(a); err != nil {
			t.Fatalf("SaveAnalysis(%d) error = %v", i, err)
		}
	}

	results, err := store.ListAnalyses("list-analysis-sess")
	if err != nil {
		t.Fatalf("ListAnalyses() error = %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("ListAnalyses() count = %d, want 3", len(results))
	}
	// Ordered by created_at DESC
	if results[0].ID != "list-a3" {
		t.Errorf("results[0].ID = %q, want list-a3 (most recent)", results[0].ID)
	}
	if results[2].ID != "list-a1" {
		t.Errorf("results[2].ID = %q, want list-a1 (oldest)", results[2].ID)
	}
}

func TestListAnalyses_Empty(t *testing.T) {
	store := mustOpenStore(t)

	results, err := store.ListAnalyses("nonexistent-session")
	if err != nil {
		t.Fatalf("ListAnalyses() error = %v", err)
	}
	if len(results) != 0 {
		t.Errorf("ListAnalyses() count = %d, want 0", len(results))
	}
}

// ── Session-to-Session Links Tests ──

func TestLinkSessions(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("link-source")
	s2 := testSession("link-target")
	for _, s := range []*session.Session{s1, s2} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	link := session.SessionLink{
		SourceSessionID: "link-source",
		TargetSessionID: "link-target",
		LinkType:        session.SessionLinkDelegatedTo,
		Description:     "Delegated auth work",
	}
	if err := store.LinkSessions(link); err != nil {
		t.Fatalf("LinkSessions() error = %v", err)
	}

	// Source should have forward link
	links, err := store.GetLinkedSessions("link-source")
	if err != nil {
		t.Fatalf("GetLinkedSessions(source) error = %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("source links count = %d, want 1", len(links))
	}
	if links[0].LinkType != session.SessionLinkDelegatedTo {
		t.Errorf("source link type = %q, want %q", links[0].LinkType, session.SessionLinkDelegatedTo)
	}
	if links[0].TargetSessionID != "link-target" {
		t.Errorf("source link target = %q, want %q", links[0].TargetSessionID, "link-target")
	}
	if links[0].Description != "Delegated auth work" {
		t.Errorf("description = %q, want %q", links[0].Description, "Delegated auth work")
	}

	// Target should have inverse link
	inverseLinks, err := store.GetLinkedSessions("link-target")
	if err != nil {
		t.Fatalf("GetLinkedSessions(target) error = %v", err)
	}
	if len(inverseLinks) != 1 {
		t.Fatalf("target links count = %d, want 1", len(inverseLinks))
	}
	if inverseLinks[0].LinkType != session.SessionLinkDelegatedFrom {
		t.Errorf("inverse link type = %q, want %q", inverseLinks[0].LinkType, session.SessionLinkDelegatedFrom)
	}
}

func TestLinkSessions_SessionNotFound(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("link-exists")
	if err := store.Save(s1); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	// Target doesn't exist
	link := session.SessionLink{
		SourceSessionID: "link-exists",
		TargetSessionID: "nonexistent",
		LinkType:        session.SessionLinkRelated,
	}
	if err := store.LinkSessions(link); err == nil {
		t.Error("LinkSessions() should fail when target doesn't exist")
	}

	// Source doesn't exist
	link2 := session.SessionLink{
		SourceSessionID: "nonexistent",
		TargetSessionID: "link-exists",
		LinkType:        session.SessionLinkRelated,
	}
	if err := store.LinkSessions(link2); err == nil {
		t.Error("LinkSessions() should fail when source doesn't exist")
	}
}

func TestLinkSessions_DuplicateIgnored(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("dup-source")
	s2 := testSession("dup-target")
	for _, s := range []*session.Session{s1, s2} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	link := session.SessionLink{
		SourceSessionID: "dup-source",
		TargetSessionID: "dup-target",
		LinkType:        session.SessionLinkRelated,
	}
	// First call should succeed
	if err := store.LinkSessions(link); err != nil {
		t.Fatalf("LinkSessions(1) error = %v", err)
	}
	// Second call with same link should not error (ON CONFLICT DO NOTHING)
	if err := store.LinkSessions(link); err != nil {
		t.Fatalf("LinkSessions(2) error = %v (should be idempotent)", err)
	}
}

func TestGetLinkedSessions_Empty(t *testing.T) {
	store := mustOpenStore(t)

	links, err := store.GetLinkedSessions("nonexistent")
	if err != nil {
		t.Fatalf("GetLinkedSessions() error = %v", err)
	}
	if len(links) != 0 {
		t.Errorf("expected 0 links, got %d", len(links))
	}
}

func TestDeleteSessionLink(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("del-link-source")
	s2 := testSession("del-link-target")
	for _, s := range []*session.Session{s1, s2} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	link := session.SessionLink{
		SourceSessionID: "del-link-source",
		TargetSessionID: "del-link-target",
		LinkType:        session.SessionLinkContinuation,
	}
	if err := store.LinkSessions(link); err != nil {
		t.Fatalf("LinkSessions() error = %v", err)
	}

	// Get the link ID
	links, err := store.GetLinkedSessions("del-link-source")
	if err != nil || len(links) == 0 {
		t.Fatalf("GetLinkedSessions() error = %v, count = %d", err, len(links))
	}
	linkID := links[0].ID

	// Delete it
	if err := store.DeleteSessionLink(linkID); err != nil {
		t.Fatalf("DeleteSessionLink() error = %v", err)
	}

	// Verify it's gone
	afterLinks, err := store.GetLinkedSessions("del-link-source")
	if err != nil {
		t.Fatalf("GetLinkedSessions() after delete error = %v", err)
	}
	if len(afterLinks) != 0 {
		t.Errorf("expected 0 links after delete, got %d", len(afterLinks))
	}
}

func TestDeleteSessionLink_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	err := store.DeleteSessionLink("nonexistent-link-id")
	if err == nil {
		t.Error("DeleteSessionLink(nonexistent) should return error")
	}
}

// ── GetFreshness Tests ──

func TestGetFreshness(t *testing.T) {
	store := mustOpenStore(t)

	sess := testSession("freshness-1")
	sess.SourceUpdatedAt = 1700000000
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	msgCount, sourceUpdatedAt, err := store.GetFreshness("freshness-1")
	if err != nil {
		t.Fatalf("GetFreshness() error = %v", err)
	}
	if msgCount != 1 { // testSession has 1 message
		t.Errorf("messageCount = %d, want 1", msgCount)
	}
	if sourceUpdatedAt != 1700000000 {
		t.Errorf("sourceUpdatedAt = %d, want 1700000000", sourceUpdatedAt)
	}
}

func TestGetFreshness_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	msgCount, sourceUpdatedAt, err := store.GetFreshness("nonexistent")
	if err != nil {
		t.Fatalf("GetFreshness(nonexistent) error = %v", err)
	}
	if msgCount != 0 || sourceUpdatedAt != 0 {
		t.Errorf("expected (0, 0) for nonexistent, got (%d, %d)", msgCount, sourceUpdatedAt)
	}
}

// ── GetByLink additional tests ──

func TestGetByLink_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	_, err := store.GetByLink(session.LinkPR, "999")
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("GetByLink(nonexistent) error = %v, want ErrSessionNotFound", err)
	}
}

func TestGetByLink_MultipleResults(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("bylink-1")
	s1.Links = []session.Link{{LinkType: session.LinkPR, Ref: "42"}}
	s2 := testSession("bylink-2")
	s2.Links = []session.Link{{LinkType: session.LinkPR, Ref: "42"}}

	for _, s := range []*session.Session{s1, s2} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	summaries, err := store.GetByLink(session.LinkPR, "42")
	if err != nil {
		t.Fatalf("GetByLink() error = %v", err)
	}
	if len(summaries) != 2 {
		t.Errorf("GetByLink() count = %d, want 2", len(summaries))
	}
}

// ── AddLink edge case ──

func TestAddLink_SessionNotFound(t *testing.T) {
	store := mustOpenStore(t)

	err := store.AddLink("nonexistent", session.Link{LinkType: session.LinkPR, Ref: "42"})
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("AddLink(nonexistent) error = %v, want ErrSessionNotFound", err)
	}
}

// ── GetUser not found ──

func TestGetUser_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	got, err := store.GetUser("nonexistent-user")
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for nonexistent user, got %+v", got)
	}
}

// ── List by provider filter ──

func TestList_FilterByProvider(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("prov-list-1")
	s1.Provider = session.ProviderClaudeCode
	s2 := testSession("prov-list-2")
	s2.Provider = session.ProviderOpenCode
	s3 := testSession("prov-list-3")
	s3.Provider = session.ProviderClaudeCode

	for _, s := range []*session.Session{s1, s2, s3} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	summaries, err := store.List(session.ListOptions{
		All:      true,
		Provider: session.ProviderOpenCode,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(summaries) != 1 {
		t.Errorf("List(provider=opencode) count = %d, want 1", len(summaries))
	}
}

// ── Tool call counting ──

func TestSave_ToolCallCounting(t *testing.T) {
	store := mustOpenStore(t)

	sess := testSession("tool-count")
	sess.Messages = []session.Message{
		{
			ID:   "m1",
			Role: session.RoleAssistant,
			ToolCalls: []session.ToolCall{
				{ID: "tc1", Name: "read_file", State: session.ToolStateCompleted},
				{ID: "tc2", Name: "write_file", State: session.ToolStateError},
			},
		},
		{
			ID:   "m2",
			Role: session.RoleAssistant,
			ToolCalls: []session.ToolCall{
				{ID: "tc3", Name: "bash", State: session.ToolStateCompleted},
			},
		},
	}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Verify via List (which reads tool_call_count, error_count columns)
	summaries, err := store.List(session.ListOptions{All: true})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("List() count = %d, want 1", len(summaries))
	}
	if summaries[0].ToolCallCount != 3 {
		t.Errorf("ToolCallCount = %d, want 3", summaries[0].ToolCallCount)
	}
	if summaries[0].ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", summaries[0].ErrorCount)
	}
}

// ── Auth Users ──

func TestCreateAndGetAuthUser(t *testing.T) {
	store := mustOpenStore(t)

	user, err := auth.NewUser("alice", "secure-password-123", auth.RoleUser)
	if err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}

	if err := store.CreateAuthUser(user); err != nil {
		t.Fatalf("CreateAuthUser() error = %v", err)
	}

	// Get by ID.
	got, err := store.GetAuthUser(user.ID)
	if err != nil {
		t.Fatalf("GetAuthUser() error = %v", err)
	}
	if got.Username != "alice" {
		t.Errorf("Username = %q, want %q", got.Username, "alice")
	}
	if got.Role != auth.RoleUser {
		t.Errorf("Role = %q, want %q", got.Role, auth.RoleUser)
	}
	if !got.Active {
		t.Error("Active should be true")
	}
	if got.PasswordHash == "" {
		t.Error("PasswordHash should be preserved")
	}

	// Get by username.
	got2, err := store.GetAuthUserByUsername("alice")
	if err != nil {
		t.Fatalf("GetAuthUserByUsername() error = %v", err)
	}
	if got2.ID != user.ID {
		t.Errorf("ID = %q, want %q", got2.ID, user.ID)
	}
}

func TestCreateAuthUser_Duplicate(t *testing.T) {
	store := mustOpenStore(t)

	user1, _ := auth.NewUser("bob", "password-123456", auth.RoleUser)
	user2, _ := auth.NewUser("bob", "different-pass-123", auth.RoleAdmin)

	if err := store.CreateAuthUser(user1); err != nil {
		t.Fatalf("first CreateAuthUser() error = %v", err)
	}

	err := store.CreateAuthUser(user2)
	if !errors.Is(err, auth.ErrUserExists) {
		t.Fatalf("second CreateAuthUser() error = %v, want ErrUserExists", err)
	}
}

func TestGetAuthUser_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	_, err := store.GetAuthUser("nonexistent")
	if !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("GetAuthUser() error = %v, want ErrUserNotFound", err)
	}
}

func TestGetAuthUserByUsername_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	_, err := store.GetAuthUserByUsername("nobody")
	if !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("GetAuthUserByUsername() error = %v, want ErrUserNotFound", err)
	}
}

func TestUpdateAuthUser(t *testing.T) {
	store := mustOpenStore(t)

	user, _ := auth.NewUser("charlie", "password-123456", auth.RoleUser)
	if err := store.CreateAuthUser(user); err != nil {
		t.Fatalf("CreateAuthUser() error = %v", err)
	}

	// Promote to admin and deactivate.
	user.Role = auth.RoleAdmin
	user.Active = false
	user.UpdatedAt = time.Now().UTC()

	if err := store.UpdateAuthUser(user); err != nil {
		t.Fatalf("UpdateAuthUser() error = %v", err)
	}

	got, _ := store.GetAuthUser(user.ID)
	if got.Role != auth.RoleAdmin {
		t.Errorf("Role = %q, want admin", got.Role)
	}
	if got.Active {
		t.Error("Active should be false after update")
	}
}

func TestListAuthUsers(t *testing.T) {
	store := mustOpenStore(t)

	user1, _ := auth.NewUser("alpha", "password-123456", auth.RoleAdmin)
	user2, _ := auth.NewUser("beta", "password-123456", auth.RoleUser)
	_ = store.CreateAuthUser(user1)
	_ = store.CreateAuthUser(user2)

	users, err := store.ListAuthUsers()
	if err != nil {
		t.Fatalf("ListAuthUsers() error = %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
}

func TestCountAuthUsers(t *testing.T) {
	store := mustOpenStore(t)

	count, _ := store.CountAuthUsers()
	if count != 0 {
		t.Fatalf("expected 0 users, got %d", count)
	}

	user, _ := auth.NewUser("delta", "password-123456", auth.RoleUser)
	_ = store.CreateAuthUser(user)

	count, _ = store.CountAuthUsers()
	if count != 1 {
		t.Fatalf("expected 1 user, got %d", count)
	}
}

// ── Auth API Keys ──

func TestCreateAndGetAPIKey(t *testing.T) {
	store := mustOpenStore(t)

	// Need a user first.
	user, _ := auth.NewUser("keyowner", "password-123456", auth.RoleUser)
	_ = store.CreateAuthUser(user)

	key, rawKey, err := auth.NewAPIKey(user.ID, "CI key")
	if err != nil {
		t.Fatalf("NewAPIKey() error = %v", err)
	}

	if err := store.CreateAPIKey(key); err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}

	// Look up by hash (the way the middleware would).
	got, err := store.GetAPIKeyByHash(key.KeyHash)
	if err != nil {
		t.Fatalf("GetAPIKeyByHash() error = %v", err)
	}
	if got.ID != key.ID {
		t.Errorf("ID = %q, want %q", got.ID, key.ID)
	}
	if got.UserID != user.ID {
		t.Errorf("UserID = %q, want %q", got.UserID, user.ID)
	}
	if got.Name != "CI key" {
		t.Errorf("Name = %q, want %q", got.Name, "CI key")
	}
	if !got.Active {
		t.Error("Active should be true")
	}

	// Verify the raw key matches.
	if !got.MatchesKey(rawKey) {
		t.Error("stored key should match the raw key")
	}
}

func TestGetAPIKeyByHash_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	_, err := store.GetAPIKeyByHash("nonexistent-hash")
	if !errors.Is(err, auth.ErrAPIKeyNotFound) {
		t.Fatalf("GetAPIKeyByHash() error = %v, want ErrAPIKeyNotFound", err)
	}
}

func TestListAPIKeysByUser(t *testing.T) {
	store := mustOpenStore(t)

	user, _ := auth.NewUser("keylister", "password-123456", auth.RoleUser)
	_ = store.CreateAuthUser(user)

	key1, _, _ := auth.NewAPIKey(user.ID, "key 1")
	key2, _, _ := auth.NewAPIKey(user.ID, "key 2")
	_ = store.CreateAPIKey(key1)
	_ = store.CreateAPIKey(key2)

	keys, err := store.ListAPIKeysByUser(user.ID)
	if err != nil {
		t.Fatalf("ListAPIKeysByUser() error = %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
}

func TestUpdateAPIKey_Revoke(t *testing.T) {
	store := mustOpenStore(t)

	user, _ := auth.NewUser("revoker", "password-123456", auth.RoleUser)
	_ = store.CreateAuthUser(user)

	key, _, _ := auth.NewAPIKey(user.ID, "revoke me")
	_ = store.CreateAPIKey(key)

	// Revoke the key.
	key.Active = false
	now := time.Now().UTC()
	key.LastUsedAt = &now

	if err := store.UpdateAPIKey(key); err != nil {
		t.Fatalf("UpdateAPIKey() error = %v", err)
	}

	got, _ := store.GetAPIKeyByHash(key.KeyHash)
	if got.Active {
		t.Error("Active should be false after revocation")
	}
	if got.LastUsedAt == nil {
		t.Error("LastUsedAt should be set")
	}
}

func TestDeleteAPIKey(t *testing.T) {
	store := mustOpenStore(t)

	user, _ := auth.NewUser("deleter", "password-123456", auth.RoleUser)
	_ = store.CreateAuthUser(user)

	key, _, _ := auth.NewAPIKey(user.ID, "delete me")
	_ = store.CreateAPIKey(key)

	if err := store.DeleteAPIKey(key.ID); err != nil {
		t.Fatalf("DeleteAPIKey() error = %v", err)
	}

	_, err := store.GetAPIKeyByHash(key.KeyHash)
	if !errors.Is(err, auth.ErrAPIKeyNotFound) {
		t.Fatalf("after delete, GetAPIKeyByHash() error = %v, want ErrAPIKeyNotFound", err)
	}
}

func TestDeleteAPIKey_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	err := store.DeleteAPIKey("nonexistent")
	if !errors.Is(err, auth.ErrAPIKeyNotFound) {
		t.Fatalf("DeleteAPIKey() error = %v, want ErrAPIKeyNotFound", err)
	}
}

// ── UpdateRemoteURL Tests ──

func TestUpdateRemoteURL_Success(t *testing.T) {
	store := mustOpenStore(t)
	sess := testSession("remote-1")
	sess.ProjectPath = "/Users/test/dev/myproject"
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	err := store.UpdateRemoteURL("remote-1", "github.com/org/repo")
	if err != nil {
		t.Fatalf("UpdateRemoteURL() error = %v", err)
	}

	got, err := store.Get("remote-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.RemoteURL != "github.com/org/repo" {
		t.Errorf("RemoteURL = %q, want %q", got.RemoteURL, "github.com/org/repo")
	}
}

func TestUpdateRemoteURL_NotFound(t *testing.T) {
	store := mustOpenStore(t)
	err := store.UpdateRemoteURL("nonexistent", "github.com/org/repo")
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("UpdateRemoteURL() error = %v, want ErrSessionNotFound", err)
	}
}

func TestListSessionsWithEmptyRemoteURL(t *testing.T) {
	store := mustOpenStore(t)

	// Session WITH remote_url — should NOT be returned.
	s1 := testSession("has-remote")
	s1.RemoteURL = "github.com/org/repo"
	s1.ProjectPath = "/path/a"
	if err := store.Save(s1); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Session WITHOUT remote_url — should be returned.
	s2 := testSession("no-remote")
	s2.RemoteURL = ""
	s2.ProjectPath = "/path/b"
	if err := store.Save(s2); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Session without project_path — should NOT be returned (can't resolve git).
	s3 := testSession("no-path")
	s3.RemoteURL = ""
	s3.ProjectPath = ""
	if err := store.Save(s3); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	candidates, err := store.ListSessionsWithEmptyRemoteURL(0)
	if err != nil {
		t.Fatalf("ListSessionsWithEmptyRemoteURL() error = %v", err)
	}

	if len(candidates) != 1 {
		t.Fatalf("got %d candidates, want 1", len(candidates))
	}
	if candidates[0].ID != "no-remote" {
		t.Errorf("candidate ID = %q, want %q", candidates[0].ID, "no-remote")
	}
	if candidates[0].ProjectPath != "/path/b" {
		t.Errorf("candidate ProjectPath = %q, want %q", candidates[0].ProjectPath, "/path/b")
	}
}

func TestListSessionsWithEmptyRemoteURL_Limit(t *testing.T) {
	store := mustOpenStore(t)

	for i := 0; i < 5; i++ {
		s := testSession(fmt.Sprintf("empty-%d", i))
		s.RemoteURL = ""
		s.ProjectPath = fmt.Sprintf("/path/%d", i)
		if err := store.Save(s); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	candidates, err := store.ListSessionsWithEmptyRemoteURL(3)
	if err != nil {
		t.Fatalf("ListSessionsWithEmptyRemoteURL() error = %v", err)
	}
	if len(candidates) != 3 {
		t.Fatalf("got %d candidates, want 3", len(candidates))
	}
}

// ── User enrichment tests (Phase 1 Slack Integration) ──

func TestSaveUser_WithKindAndRole(t *testing.T) {
	store := mustOpenStore(t)

	user := &session.User{
		ID:     session.ID("uk-1"),
		Name:   "Bot Account",
		Email:  "bot@ci.dev",
		Source: "git",
		Kind:   session.UserKindMachine,
		Role:   session.UserRoleMember,
	}
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser() error = %v", err)
	}

	got, err := store.GetUser(session.ID("uk-1"))
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetUser() returned nil")
	}
	if got.Kind != session.UserKindMachine {
		t.Errorf("Kind = %q, want %q", got.Kind, session.UserKindMachine)
	}
	if got.Role != session.UserRoleMember {
		t.Errorf("Role = %q, want %q", got.Role, session.UserRoleMember)
	}
}

func TestSaveUser_DefaultsKindAndRole(t *testing.T) {
	store := mustOpenStore(t)

	user := &session.User{
		ID:     session.ID("uk-2"),
		Name:   "No Kind",
		Email:  "nokind@example.com",
		Source: "git",
		// Kind and Role left as zero values
	}
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser() error = %v", err)
	}

	got, err := store.GetUser(session.ID("uk-2"))
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetUser() returned nil")
	}
	if got.Kind != session.UserKindUnknown {
		t.Errorf("Kind = %q, want %q (default)", got.Kind, session.UserKindUnknown)
	}
	if got.Role != session.UserRoleMember {
		t.Errorf("Role = %q, want %q (default)", got.Role, session.UserRoleMember)
	}
}

func TestListUsers(t *testing.T) {
	store := mustOpenStore(t)

	users := []*session.User{
		{ID: "lu-1", Name: "Alice", Email: "alice@test.com", Source: "git", Kind: session.UserKindHuman, Role: session.UserRoleMember},
		{ID: "lu-2", Name: "Bot", Email: "bot@test.com", Source: "git", Kind: session.UserKindMachine, Role: session.UserRoleMember},
		{ID: "lu-3", Name: "Charlie", Email: "charlie@test.com", Source: "git", Kind: session.UserKindHuman, Role: session.UserRoleAdmin},
	}
	for _, u := range users {
		if err := store.SaveUser(u); err != nil {
			t.Fatalf("SaveUser(%s) error = %v", u.ID, err)
		}
	}

	got, err := store.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListUsers() returned %d users, want 3", len(got))
	}
	// Should be ordered by name: Alice, Bot, Charlie
	if got[0].Name != "Alice" {
		t.Errorf("first user = %q, want Alice", got[0].Name)
	}
	if got[1].Name != "Bot" {
		t.Errorf("second user = %q, want Bot", got[1].Name)
	}
}

func TestListUsersByKind(t *testing.T) {
	store := mustOpenStore(t)

	users := []*session.User{
		{ID: "lk-1", Name: "Human1", Email: "h1@test.com", Source: "git", Kind: session.UserKindHuman},
		{ID: "lk-2", Name: "Human2", Email: "h2@test.com", Source: "git", Kind: session.UserKindHuman},
		{ID: "lk-3", Name: "Machine", Email: "m@test.com", Source: "git", Kind: session.UserKindMachine},
		{ID: "lk-4", Name: "Unknown", Email: "u@test.com", Source: "git", Kind: session.UserKindUnknown},
	}
	for _, u := range users {
		if err := store.SaveUser(u); err != nil {
			t.Fatalf("SaveUser(%s) error = %v", u.ID, err)
		}
	}

	humans, err := store.ListUsersByKind("human")
	if err != nil {
		t.Fatalf("ListUsersByKind(human) error = %v", err)
	}
	if len(humans) != 2 {
		t.Errorf("ListUsersByKind(human) = %d, want 2", len(humans))
	}

	machines, err := store.ListUsersByKind("machine")
	if err != nil {
		t.Fatalf("ListUsersByKind(machine) error = %v", err)
	}
	if len(machines) != 1 {
		t.Errorf("ListUsersByKind(machine) = %d, want 1", len(machines))
	}
}

func TestUpdateUserSlack(t *testing.T) {
	store := mustOpenStore(t)

	user := &session.User{
		ID:    "sl-1",
		Name:  "Slack User",
		Email: "slack@test.com",
		Kind:  session.UserKindHuman,
	}
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser() error = %v", err)
	}

	if err := store.UpdateUserSlack("sl-1", "U0123ABCDEF", "slack_user"); err != nil {
		t.Fatalf("UpdateUserSlack() error = %v", err)
	}

	got, err := store.GetUser("sl-1")
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if got.SlackID != "U0123ABCDEF" {
		t.Errorf("SlackID = %q, want %q", got.SlackID, "U0123ABCDEF")
	}
	if got.SlackName != "slack_user" {
		t.Errorf("SlackName = %q, want %q", got.SlackName, "slack_user")
	}
}

func TestUpdateUserSlack_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	err := store.UpdateUserSlack("nonexistent", "U123", "name")
	if err == nil {
		t.Fatal("expected error for nonexistent user")
	}
}

func TestUpdateUserKind(t *testing.T) {
	store := mustOpenStore(t)

	user := &session.User{
		ID:    "uk-k1",
		Name:  "Kind Change",
		Email: "kind@test.com",
		Kind:  session.UserKindUnknown,
	}
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser() error = %v", err)
	}

	if err := store.UpdateUserKind("uk-k1", "machine"); err != nil {
		t.Fatalf("UpdateUserKind() error = %v", err)
	}

	got, err := store.GetUser("uk-k1")
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if got.Kind != session.UserKindMachine {
		t.Errorf("Kind = %q, want %q", got.Kind, session.UserKindMachine)
	}
}

func TestUpdateUserRole(t *testing.T) {
	store := mustOpenStore(t)

	user := &session.User{
		ID:    "ur-1",
		Name:  "Role Change",
		Email: "role@test.com",
		Kind:  session.UserKindHuman,
		Role:  session.UserRoleMember,
	}
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser() error = %v", err)
	}

	if err := store.UpdateUserRole("ur-1", "admin"); err != nil {
		t.Fatalf("UpdateUserRole() error = %v", err)
	}

	got, err := store.GetUser("ur-1")
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if got.Role != session.UserRoleAdmin {
		t.Errorf("Role = %q, want %q", got.Role, session.UserRoleAdmin)
	}
}

func TestOwnerStats(t *testing.T) {
	store := mustOpenStore(t)

	// Create users
	u1 := &session.User{ID: "os-u1", Name: "Alice", Email: "alice@os.com", Kind: session.UserKindHuman}
	u2 := &session.User{ID: "os-u2", Name: "Bot", Email: "bot@os.com", Kind: session.UserKindMachine}
	for _, u := range []*session.User{u1, u2} {
		if err := store.SaveUser(u); err != nil {
			t.Fatalf("SaveUser(%s) error = %v", u.ID, err)
		}
	}

	// Create sessions with different owners
	s1 := testSession("os-s1")
	s1.OwnerID = "os-u1"
	s1.TokenUsage.TotalTokens = 100
	s1.ProjectPath = "/proj/a"
	s2 := testSession("os-s2")
	s2.OwnerID = "os-u1"
	s2.TokenUsage.TotalTokens = 200
	s2.ProjectPath = "/proj/a"
	s3 := testSession("os-s3")
	s3.OwnerID = "os-u2"
	s3.TokenUsage.TotalTokens = 50
	s3.ProjectPath = "/proj/a"

	for _, s := range []*session.Session{s1, s2, s3} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) error = %v", s.ID, err)
		}
	}

	// Query all
	since := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	stats, err := store.OwnerStats("", since, until)
	if err != nil {
		t.Fatalf("OwnerStats() error = %v", err)
	}
	if len(stats) < 2 {
		t.Fatalf("OwnerStats() returned %d rows, want at least 2", len(stats))
	}

	// Check Alice's stats (should have 2 sessions, 300 tokens)
	var aliceStat *session.OwnerStat
	for i := range stats {
		if stats[i].OwnerID == "os-u1" {
			aliceStat = &stats[i]
			break
		}
	}
	if aliceStat == nil {
		t.Fatal("OwnerStats() missing Alice")
	}
	if aliceStat.SessionCount != 2 {
		t.Errorf("Alice sessions = %d, want 2", aliceStat.SessionCount)
	}
	if aliceStat.TotalTokens != 300 {
		t.Errorf("Alice tokens = %d, want 300", aliceStat.TotalTokens)
	}
	if aliceStat.OwnerKind != "human" {
		t.Errorf("Alice kind = %q, want human", aliceStat.OwnerKind)
	}

	// Query with project filter
	statsProj, err := store.OwnerStats("/proj/a", since, until)
	if err != nil {
		t.Fatalf("OwnerStats(project) error = %v", err)
	}
	if len(statsProj) < 2 {
		t.Fatalf("OwnerStats(project) returned %d rows, want at least 2", len(statsProj))
	}
}

func TestOwnerStats_Empty(t *testing.T) {
	store := mustOpenStore(t)

	since := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)

	stats, err := store.OwnerStats("", since, until)
	if err != nil {
		t.Fatalf("OwnerStats() error = %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("OwnerStats() = %d, want 0 for empty range", len(stats))
	}
}

func TestGetUserByEmail_WithNewFields(t *testing.T) {
	store := mustOpenStore(t)

	user := &session.User{
		ID:        "nf-1",
		Name:      "Full Fields",
		Email:     "full@test.com",
		Source:    "git",
		Kind:      session.UserKindHuman,
		SlackID:   "U9999",
		SlackName: "full_user",
		Role:      session.UserRoleAdmin,
	}
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser() error = %v", err)
	}

	got, err := store.GetUserByEmail("full@test.com")
	if err != nil {
		t.Fatalf("GetUserByEmail() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetUserByEmail() returned nil")
	}
	if got.Kind != session.UserKindHuman {
		t.Errorf("Kind = %q, want human", got.Kind)
	}
	if got.SlackID != "U9999" {
		t.Errorf("SlackID = %q, want U9999", got.SlackID)
	}
	if got.SlackName != "full_user" {
		t.Errorf("SlackName = %q, want full_user", got.SlackName)
	}
	if got.Role != session.UserRoleAdmin {
		t.Errorf("Role = %q, want admin", got.Role)
	}
}

// ── Benchmarks ──

// seedBenchSessions creates n sessions with realistic-looking payloads
// (some messages, some tool calls) and returns their IDs.
func seedBenchSessions(b *testing.B, store *Store, n int) []session.ID {
	b.Helper()
	ids := make([]session.ID, 0, n)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		id := session.ID(fmt.Sprintf("bench-sess-%04d", i))
		sess := &session.Session{
			ID:          id,
			Version:     1,
			Provider:    session.ProviderClaudeCode,
			Agent:       "claude",
			Branch:      "main",
			CommitSHA:   "abc1234",
			ProjectPath: "/home/chris/bench-project",
			CreatedAt:   base.Add(time.Duration(i) * time.Minute),
			Summary:     fmt.Sprintf("Benchmark session %d — some work on feature X", i),
			StorageMode: session.StorageModeCompact,
			TokenUsage: session.TokenUsage{
				InputTokens:  1000 + i,
				OutputTokens: 500 + i,
				TotalTokens:  1500 + 2*i,
			},
			Messages: []session.Message{
				{ID: "m1", Role: session.RoleUser, Content: "Hello world, do X", Timestamp: base},
				{ID: "m2", Role: session.RoleAssistant, Content: "Working on X now", Timestamp: base},
				{ID: "m3", Role: session.RoleUser, Content: "Now also do Y please", Timestamp: base},
				{ID: "m4", Role: session.RoleAssistant, Content: "Y is done as well", Timestamp: base},
			},
			FileChanges: []session.FileChange{
				{FilePath: "src/foo.go", ChangeType: session.ChangeCreated},
				{FilePath: "src/bar.go", ChangeType: session.ChangeModified},
			},
			Links: []session.Link{
				{LinkType: session.LinkBranch, Ref: "main"},
			},
		}
		if err := store.Save(sess); err != nil {
			b.Fatalf("seed Save(%s) error = %v", id, err)
		}
		ids = append(ids, id)
	}
	return ids
}

// BenchmarkGet_Loop baselines the legacy N+1 pattern: call Get() for each
// session ID in a loop. Each Get() costs 3 SQL queries (payload + links + file_changes).
func BenchmarkGet_Loop(b *testing.B) {
	const n = 500
	dbPath := filepath.Join(b.TempDir(), "bench.db")
	store, err := New(dbPath)
	if err != nil {
		b.Fatalf("New error = %v", err)
	}
	defer func() { _ = store.Close() }()

	ids := seedBenchSessions(b, store, n)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, id := range ids {
			if _, err := store.Get(id); err != nil {
				b.Fatalf("Get error = %v", err)
			}
		}
	}
}

// BenchmarkGetBatch measures the batch path: one call loads everything with
// 3 SQL queries total regardless of len(ids).
func BenchmarkGetBatch(b *testing.B) {
	const n = 500
	dbPath := filepath.Join(b.TempDir(), "bench.db")
	store, err := New(dbPath)
	if err != nil {
		b.Fatalf("New error = %v", err)
	}
	defer func() { _ = store.Close() }()

	ids := seedBenchSessions(b, store, n)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := store.GetBatch(ids); err != nil {
			b.Fatalf("GetBatch error = %v", err)
		}
	}
}

// ── Session Analytics (CQRS read model) round-trip tests ──

// makeAnalyticsFixture builds a rich Analytics value with all 6 JSON blob
// pointer fields non-nil and a 2-row AgentUsage slice. Used by the
// round-trip tests below to verify that every column and blob field
// survives an upsert → read cycle through SQLite.
func makeAnalyticsFixture(sessionID string) session.Analytics {
	now := time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)
	return session.Analytics{
		SessionID: session.ID(sessionID),

		// ContextSaturation
		PeakInputTokens:        95_000,
		DominantModel:          "claude-sonnet-4-20250514",
		MaxContextWindow:       200_000,
		PeakSaturationPct:      47.5,
		HasCompaction:          true,
		CompactionCount:        3,
		CompactionDropPct:      22.5,
		CompactionWastedTokens: 18_000,

		// CacheEfficiency
		CacheReadTokens:   50_000,
		CacheWriteTokens:  12_000,
		InputTokens:       120_000,
		CacheMissCount:    2,
		CacheWastedTokens: 8_000,
		LongestGapMins:    45,
		SessionAvgGapMins: 12.7,

		// Forecast
		Backend:          "claude",
		EstimatedCost:    1.23,
		ActualCost:       0.98,
		ForkOffset:       5,
		DeduplicatedCost: 0.85,

		// Agent rollups
		TotalAgentInvocations: 7,
		UniqueAgentsUsed:      2,
		AgentTokens:           30_000,
		AgentCost:             0.15,
		TotalWastedTokens:     26_000,

		// Per-agent breakdown
		AgentUsage: []session.AgentUsage{
			{AgentName: "coder", Invocations: 5, Tokens: 25_000, Cost: 0.12, Errors: 1},
			{AgentName: "reviewer", Invocations: 2, Tokens: 5_000, Cost: 0.03, Errors: 0},
		},

		// JSON blobs
		WasteBreakdown: &session.TokenWasteBreakdown{
			TotalTokens:   120_000,
			ProductivePct: 78.3,
			WastePct:      21.7,
		},
		Freshness: &session.SessionFreshness{
			TotalMessages:     42,
			CompactionCount:   3,
			Recommendation:    "consider splitting after message 30",
			OptimalMessageIdx: 30,
		},
		Overload: &session.OverloadAnalysis{
			IsOverloaded:  true,
			Verdict:       "overloaded",
			InflectionAt:  28,
			HealthScore:   45,
			TotalMessages: 42,
		},
		PromptData: &session.SessionPromptData{
			PromptTokens: 12_000,
			TotalInput:   120_000,
			ErrorRate:    0.05,
			RetryRate:    0.02,
		},
		FitnessData: &session.SessionFitnessData{
			Model:         "claude-sonnet-4-20250514",
			SessionType:   "feature",
			TotalTokens:   120_000,
			OutputTokens:  30_000,
			MessageCount:  42,
			ToolCalls:     15,
			ToolErrors:    1,
			EstimatedCost: 1.23,
			HasRetries:    true,
		},
		ForecastInput: &session.SessionForecastInput{
			Model:                "claude-sonnet-4-20250514",
			MaxInputTokens:       200_000,
			MessageCount:         42,
			PeakInputTokens:      95_000,
			MsgAtFirstCompaction: 25,
			TokenGrowthPerMsg:    2_100,
		},

		SchemaVersion: 1,
		ComputedAt:    now,
	}
}

func TestUpsertAndGetSessionAnalytics(t *testing.T) {
	store := mustOpenStore(t)

	// The session_analytics table has a FK to sessions, so we need a parent row.
	sess := testSession("analytics-sess-1")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save session: %v", err)
	}

	want := makeAnalyticsFixture("analytics-sess-1")

	// Upsert
	if err := store.UpsertSessionAnalytics(want); err != nil {
		t.Fatalf("UpsertSessionAnalytics: %v", err)
	}

	// Read back
	got, err := store.GetSessionAnalytics(session.ID("analytics-sess-1"))
	if err != nil {
		t.Fatalf("GetSessionAnalytics: %v", err)
	}
	if got == nil {
		t.Fatal("GetSessionAnalytics returned nil, expected non-nil")
	}

	// Scalar fields
	if got.SessionID != want.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, want.SessionID)
	}
	if got.PeakInputTokens != want.PeakInputTokens {
		t.Errorf("PeakInputTokens = %d, want %d", got.PeakInputTokens, want.PeakInputTokens)
	}
	if got.DominantModel != want.DominantModel {
		t.Errorf("DominantModel = %q, want %q", got.DominantModel, want.DominantModel)
	}
	if got.MaxContextWindow != want.MaxContextWindow {
		t.Errorf("MaxContextWindow = %d, want %d", got.MaxContextWindow, want.MaxContextWindow)
	}
	if got.PeakSaturationPct != want.PeakSaturationPct {
		t.Errorf("PeakSaturationPct = %f, want %f", got.PeakSaturationPct, want.PeakSaturationPct)
	}
	if got.HasCompaction != want.HasCompaction {
		t.Errorf("HasCompaction = %v, want %v", got.HasCompaction, want.HasCompaction)
	}
	if got.CompactionCount != want.CompactionCount {
		t.Errorf("CompactionCount = %d, want %d", got.CompactionCount, want.CompactionCount)
	}
	if got.CompactionDropPct != want.CompactionDropPct {
		t.Errorf("CompactionDropPct = %f, want %f", got.CompactionDropPct, want.CompactionDropPct)
	}
	if got.CompactionWastedTokens != want.CompactionWastedTokens {
		t.Errorf("CompactionWastedTokens = %d, want %d", got.CompactionWastedTokens, want.CompactionWastedTokens)
	}
	if got.CacheReadTokens != want.CacheReadTokens {
		t.Errorf("CacheReadTokens = %d, want %d", got.CacheReadTokens, want.CacheReadTokens)
	}
	if got.CacheWriteTokens != want.CacheWriteTokens {
		t.Errorf("CacheWriteTokens = %d, want %d", got.CacheWriteTokens, want.CacheWriteTokens)
	}
	if got.InputTokens != want.InputTokens {
		t.Errorf("InputTokens = %d, want %d", got.InputTokens, want.InputTokens)
	}
	if got.CacheMissCount != want.CacheMissCount {
		t.Errorf("CacheMissCount = %d, want %d", got.CacheMissCount, want.CacheMissCount)
	}
	if got.CacheWastedTokens != want.CacheWastedTokens {
		t.Errorf("CacheWastedTokens = %d, want %d", got.CacheWastedTokens, want.CacheWastedTokens)
	}
	if got.LongestGapMins != want.LongestGapMins {
		t.Errorf("LongestGapMins = %d, want %d", got.LongestGapMins, want.LongestGapMins)
	}
	if got.SessionAvgGapMins != want.SessionAvgGapMins {
		t.Errorf("SessionAvgGapMins = %f, want %f", got.SessionAvgGapMins, want.SessionAvgGapMins)
	}
	if got.Backend != want.Backend {
		t.Errorf("Backend = %q, want %q", got.Backend, want.Backend)
	}
	if got.EstimatedCost != want.EstimatedCost {
		t.Errorf("EstimatedCost = %f, want %f", got.EstimatedCost, want.EstimatedCost)
	}
	if got.ActualCost != want.ActualCost {
		t.Errorf("ActualCost = %f, want %f", got.ActualCost, want.ActualCost)
	}
	if got.ForkOffset != want.ForkOffset {
		t.Errorf("ForkOffset = %d, want %d", got.ForkOffset, want.ForkOffset)
	}
	if got.DeduplicatedCost != want.DeduplicatedCost {
		t.Errorf("DeduplicatedCost = %f, want %f", got.DeduplicatedCost, want.DeduplicatedCost)
	}
	if got.TotalAgentInvocations != want.TotalAgentInvocations {
		t.Errorf("TotalAgentInvocations = %d, want %d", got.TotalAgentInvocations, want.TotalAgentInvocations)
	}
	if got.UniqueAgentsUsed != want.UniqueAgentsUsed {
		t.Errorf("UniqueAgentsUsed = %d, want %d", got.UniqueAgentsUsed, want.UniqueAgentsUsed)
	}
	if got.AgentTokens != want.AgentTokens {
		t.Errorf("AgentTokens = %d, want %d", got.AgentTokens, want.AgentTokens)
	}
	if got.AgentCost != want.AgentCost {
		t.Errorf("AgentCost = %f, want %f", got.AgentCost, want.AgentCost)
	}
	if got.TotalWastedTokens != want.TotalWastedTokens {
		t.Errorf("TotalWastedTokens = %d, want %d", got.TotalWastedTokens, want.TotalWastedTokens)
	}
	if got.SchemaVersion != want.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, want.SchemaVersion)
	}

	// Agent usage (ordered by agent_name)
	if len(got.AgentUsage) != 2 {
		t.Fatalf("AgentUsage len = %d, want 2", len(got.AgentUsage))
	}
	if got.AgentUsage[0].AgentName != "coder" {
		t.Errorf("AgentUsage[0].AgentName = %q, want %q", got.AgentUsage[0].AgentName, "coder")
	}
	if got.AgentUsage[0].Invocations != 5 {
		t.Errorf("AgentUsage[0].Invocations = %d, want 5", got.AgentUsage[0].Invocations)
	}
	if got.AgentUsage[0].Errors != 1 {
		t.Errorf("AgentUsage[0].Errors = %d, want 1", got.AgentUsage[0].Errors)
	}
	if got.AgentUsage[1].AgentName != "reviewer" {
		t.Errorf("AgentUsage[1].AgentName = %q, want %q", got.AgentUsage[1].AgentName, "reviewer")
	}

	// JSON blobs — verify non-nil and spot-check key fields
	if got.WasteBreakdown == nil {
		t.Fatal("WasteBreakdown is nil")
	}
	if got.WasteBreakdown.TotalTokens != 120_000 {
		t.Errorf("WasteBreakdown.TotalTokens = %d, want 120000", got.WasteBreakdown.TotalTokens)
	}
	if got.WasteBreakdown.ProductivePct != 78.3 {
		t.Errorf("WasteBreakdown.ProductivePct = %f, want 78.3", got.WasteBreakdown.ProductivePct)
	}

	if got.Freshness == nil {
		t.Fatal("Freshness is nil")
	}
	if got.Freshness.TotalMessages != 42 {
		t.Errorf("Freshness.TotalMessages = %d, want 42", got.Freshness.TotalMessages)
	}
	if got.Freshness.Recommendation != "consider splitting after message 30" {
		t.Errorf("Freshness.Recommendation = %q", got.Freshness.Recommendation)
	}

	if got.Overload == nil {
		t.Fatal("Overload is nil")
	}
	if !got.Overload.IsOverloaded {
		t.Error("Overload.IsOverloaded = false, want true")
	}
	if got.Overload.HealthScore != 45 {
		t.Errorf("Overload.HealthScore = %d, want 45", got.Overload.HealthScore)
	}

	if got.PromptData == nil {
		t.Fatal("PromptData is nil")
	}
	if got.PromptData.PromptTokens != 12_000 {
		t.Errorf("PromptData.PromptTokens = %d, want 12000", got.PromptData.PromptTokens)
	}

	if got.FitnessData == nil {
		t.Fatal("FitnessData is nil")
	}
	if got.FitnessData.SessionType != "feature" {
		t.Errorf("FitnessData.SessionType = %q, want %q", got.FitnessData.SessionType, "feature")
	}
	if !got.FitnessData.HasRetries {
		t.Error("FitnessData.HasRetries = false, want true")
	}

	if got.ForecastInput == nil {
		t.Fatal("ForecastInput is nil")
	}
	if got.ForecastInput.TokenGrowthPerMsg != 2_100 {
		t.Errorf("ForecastInput.TokenGrowthPerMsg = %d, want 2100", got.ForecastInput.TokenGrowthPerMsg)
	}
	if got.ForecastInput.MsgAtFirstCompaction != 25 {
		t.Errorf("ForecastInput.MsgAtFirstCompaction = %d, want 25", got.ForecastInput.MsgAtFirstCompaction)
	}
}

func TestGetSessionAnalytics_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	got, err := store.GetSessionAnalytics(session.ID("nonexistent"))
	if err != nil {
		t.Fatalf("GetSessionAnalytics error = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("GetSessionAnalytics = %+v, want nil", got)
	}
}

func TestUpsertSessionAnalytics_Update(t *testing.T) {
	store := mustOpenStore(t)
	sess := testSession("analytics-update-1")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Insert initial values
	initial := session.Analytics{
		SessionID:       session.ID("analytics-update-1"),
		PeakInputTokens: 50_000,
		DominantModel:   "gpt-4o",
		Backend:         "openai",
		EstimatedCost:   0.50,
		SchemaVersion:   1,
		ComputedAt:      time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC),
		AgentUsage: []session.AgentUsage{
			{AgentName: "old-agent", Invocations: 1, Tokens: 1000},
		},
	}
	if err := store.UpsertSessionAnalytics(initial); err != nil {
		t.Fatalf("UpsertSessionAnalytics (initial): %v", err)
	}

	// Overwrite with updated values
	updated := session.Analytics{
		SessionID:       session.ID("analytics-update-1"),
		PeakInputTokens: 99_000,
		DominantModel:   "claude-sonnet-4-20250514",
		Backend:         "claude",
		EstimatedCost:   2.50,
		SchemaVersion:   2,
		ComputedAt:      time.Date(2026, 4, 6, 14, 0, 0, 0, time.UTC),
		AgentUsage: []session.AgentUsage{
			{AgentName: "new-agent-a", Invocations: 3, Tokens: 15_000},
			{AgentName: "new-agent-b", Invocations: 1, Tokens: 5_000},
		},
	}
	if err := store.UpsertSessionAnalytics(updated); err != nil {
		t.Fatalf("UpsertSessionAnalytics (update): %v", err)
	}

	got, err := store.GetSessionAnalytics(session.ID("analytics-update-1"))
	if err != nil {
		t.Fatalf("GetSessionAnalytics: %v", err)
	}
	if got == nil {
		t.Fatal("GetSessionAnalytics returned nil")
	}

	// Verify updated values replaced initial
	if got.PeakInputTokens != 99_000 {
		t.Errorf("PeakInputTokens = %d, want 99000", got.PeakInputTokens)
	}
	if got.DominantModel != "claude-sonnet-4-20250514" {
		t.Errorf("DominantModel = %q, want claude-sonnet-4-20250514", got.DominantModel)
	}
	if got.Backend != "claude" {
		t.Errorf("Backend = %q, want claude", got.Backend)
	}
	if got.EstimatedCost != 2.50 {
		t.Errorf("EstimatedCost = %f, want 2.50", got.EstimatedCost)
	}
	if got.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", got.SchemaVersion)
	}

	// Agent usage should be fully replaced (old-agent gone)
	if len(got.AgentUsage) != 2 {
		t.Fatalf("AgentUsage len = %d, want 2", len(got.AgentUsage))
	}
	if got.AgentUsage[0].AgentName != "new-agent-a" {
		t.Errorf("AgentUsage[0].AgentName = %q, want new-agent-a", got.AgentUsage[0].AgentName)
	}
	if got.AgentUsage[1].AgentName != "new-agent-b" {
		t.Errorf("AgentUsage[1].AgentName = %q, want new-agent-b", got.AgentUsage[1].AgentName)
	}
}

func TestUpsertSessionAnalytics_NilBlobs(t *testing.T) {
	store := mustOpenStore(t)
	sess := testSession("analytics-nilblobs")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Upsert with all blob pointers nil
	a := session.Analytics{
		SessionID:     session.ID("analytics-nilblobs"),
		Backend:       "claude",
		SchemaVersion: 1,
		ComputedAt:    time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC),
	}
	if err := store.UpsertSessionAnalytics(a); err != nil {
		t.Fatalf("UpsertSessionAnalytics: %v", err)
	}

	got, err := store.GetSessionAnalytics(session.ID("analytics-nilblobs"))
	if err != nil {
		t.Fatalf("GetSessionAnalytics: %v", err)
	}
	if got == nil {
		t.Fatal("GetSessionAnalytics returned nil")
	}

	// All pointers should remain nil (empty JSON strings → nil)
	if got.WasteBreakdown != nil {
		t.Error("WasteBreakdown should be nil")
	}
	if got.Freshness != nil {
		t.Error("Freshness should be nil")
	}
	if got.Overload != nil {
		t.Error("Overload should be nil")
	}
	if got.PromptData != nil {
		t.Error("PromptData should be nil")
	}
	if got.FitnessData != nil {
		t.Error("FitnessData should be nil")
	}
	if got.ForecastInput != nil {
		t.Error("ForecastInput should be nil")
	}
	if len(got.AgentUsage) != 0 {
		t.Errorf("AgentUsage len = %d, want 0", len(got.AgentUsage))
	}
}

func TestQueryAnalytics_Filters(t *testing.T) {
	store := mustOpenStore(t)

	// Seed 3 sessions with different project paths, dates, and backends.
	now := time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)
	sessions := []struct {
		id          string
		projectPath string
		branch      string
		createdAt   time.Time
		backend     string
		schema      int
	}{
		{"qa-1", "/proj/alpha", "main", now.Add(-48 * time.Hour), "claude", 1},
		{"qa-2", "/proj/alpha", "feat", now.Add(-24 * time.Hour), "openai", 1},
		{"qa-3", "/proj/beta", "main", now, "claude", 2},
	}

	for _, tc := range sessions {
		s := testSession(tc.id)
		s.ProjectPath = tc.projectPath
		s.Branch = tc.branch
		s.CreatedAt = tc.createdAt
		if err := store.Save(s); err != nil {
			t.Fatalf("Save %s: %v", tc.id, err)
		}
		a := session.Analytics{
			SessionID:     session.ID(tc.id),
			Backend:       tc.backend,
			SchemaVersion: tc.schema,
			ComputedAt:    now,
		}
		if err := store.UpsertSessionAnalytics(a); err != nil {
			t.Fatalf("Upsert %s: %v", tc.id, err)
		}
	}

	t.Run("no filter returns all ordered by created_at DESC", func(t *testing.T) {
		rows, err := store.QueryAnalytics(session.AnalyticsFilter{})
		if err != nil {
			t.Fatalf("QueryAnalytics: %v", err)
		}
		if len(rows) != 3 {
			t.Fatalf("len = %d, want 3", len(rows))
		}
		// Newest first
		if rows[0].SessionID != "qa-3" {
			t.Errorf("[0].SessionID = %q, want qa-3", rows[0].SessionID)
		}
		if rows[1].SessionID != "qa-2" {
			t.Errorf("[1].SessionID = %q, want qa-2", rows[1].SessionID)
		}
		if rows[2].SessionID != "qa-1" {
			t.Errorf("[2].SessionID = %q, want qa-1", rows[2].SessionID)
		}
		// Verify hydrated metadata
		if rows[0].ProjectPath != "/proj/beta" {
			t.Errorf("[0].ProjectPath = %q, want /proj/beta", rows[0].ProjectPath)
		}
		if rows[0].Branch != "main" {
			t.Errorf("[0].Branch = %q, want main", rows[0].Branch)
		}
	})

	t.Run("filter by ProjectPath", func(t *testing.T) {
		rows, err := store.QueryAnalytics(session.AnalyticsFilter{ProjectPath: "/proj/alpha"})
		if err != nil {
			t.Fatalf("QueryAnalytics: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("len = %d, want 2", len(rows))
		}
		// Both should be alpha, newest first
		if rows[0].SessionID != "qa-2" {
			t.Errorf("[0].SessionID = %q, want qa-2", rows[0].SessionID)
		}
		if rows[1].SessionID != "qa-1" {
			t.Errorf("[1].SessionID = %q, want qa-1", rows[1].SessionID)
		}
	})

	t.Run("filter by Backend", func(t *testing.T) {
		rows, err := store.QueryAnalytics(session.AnalyticsFilter{Backend: "openai"})
		if err != nil {
			t.Fatalf("QueryAnalytics: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("len = %d, want 1", len(rows))
		}
		if rows[0].SessionID != "qa-2" {
			t.Errorf("[0].SessionID = %q, want qa-2", rows[0].SessionID)
		}
	})

	t.Run("filter by Since", func(t *testing.T) {
		rows, err := store.QueryAnalytics(session.AnalyticsFilter{
			Since: now.Add(-25 * time.Hour),
		})
		if err != nil {
			t.Fatalf("QueryAnalytics: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("len = %d, want 2 (qa-2 and qa-3)", len(rows))
		}
	})

	t.Run("filter by Until", func(t *testing.T) {
		rows, err := store.QueryAnalytics(session.AnalyticsFilter{
			Until: now.Add(-25 * time.Hour),
		})
		if err != nil {
			t.Fatalf("QueryAnalytics: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("len = %d, want 1 (qa-1)", len(rows))
		}
		if rows[0].SessionID != "qa-1" {
			t.Errorf("[0].SessionID = %q, want qa-1", rows[0].SessionID)
		}
	})

	t.Run("filter by MinSchemaVersion", func(t *testing.T) {
		rows, err := store.QueryAnalytics(session.AnalyticsFilter{MinSchemaVersion: 2})
		if err != nil {
			t.Fatalf("QueryAnalytics: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("len = %d, want 1 (qa-3 with schema=2)", len(rows))
		}
		if rows[0].SessionID != "qa-3" {
			t.Errorf("[0].SessionID = %q, want qa-3", rows[0].SessionID)
		}
	})

	t.Run("combined filters", func(t *testing.T) {
		rows, err := store.QueryAnalytics(session.AnalyticsFilter{
			ProjectPath: "/proj/alpha",
			Backend:     "claude",
		})
		if err != nil {
			t.Fatalf("QueryAnalytics: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("len = %d, want 1 (qa-1: alpha+claude)", len(rows))
		}
		if rows[0].SessionID != "qa-1" {
			t.Errorf("[0].SessionID = %q, want qa-1", rows[0].SessionID)
		}
	})

	t.Run("no match returns empty", func(t *testing.T) {
		rows, err := store.QueryAnalytics(session.AnalyticsFilter{Backend: "gemini"})
		if err != nil {
			t.Fatalf("QueryAnalytics: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("len = %d, want 0", len(rows))
		}
	})
}

func TestQueryAnalytics_BlobsHydrated(t *testing.T) {
	store := mustOpenStore(t)
	sess := testSession("qa-blobs")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	a := makeAnalyticsFixture("qa-blobs")
	if err := store.UpsertSessionAnalytics(a); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	rows, err := store.QueryAnalytics(session.AnalyticsFilter{})
	if err != nil {
		t.Fatalf("QueryAnalytics: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len = %d, want 1", len(rows))
	}

	got := rows[0]
	if got.WasteBreakdown == nil {
		t.Error("WasteBreakdown is nil in QueryAnalytics result")
	}
	if got.Freshness == nil {
		t.Error("Freshness is nil in QueryAnalytics result")
	}
	if got.Overload == nil {
		t.Error("Overload is nil in QueryAnalytics result")
	}
	if got.PromptData == nil {
		t.Error("PromptData is nil in QueryAnalytics result")
	}
	if got.FitnessData == nil {
		t.Error("FitnessData is nil in QueryAnalytics result")
	}
	if got.ForecastInput == nil {
		t.Error("ForecastInput is nil in QueryAnalytics result")
	}
}
