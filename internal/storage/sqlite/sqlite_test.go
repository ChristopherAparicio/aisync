package sqlite

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
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

func TestGetByBranch(t *testing.T) {
	store := mustOpenStore(t)

	s1 := testSession("sess-1")
	s1.Branch = "feature/auth"
	s1.ProjectPath = "/project"

	s2 := testSession("sess-2")
	s2.Branch = "feature/other"
	s2.ProjectPath = "/project"

	if err := store.Save(s1); err != nil {
		t.Fatalf("Save(s1) error = %v", err)
	}
	if err := store.Save(s2); err != nil {
		t.Fatalf("Save(s2) error = %v", err)
	}

	got, err := store.GetByBranch("/project", "feature/auth")
	if err != nil {
		t.Fatalf("GetByBranch() error = %v", err)
	}
	if got.ID != session.ID("sess-1") {
		t.Errorf("ID = %q, want %q", got.ID, "sess-1")
	}
}

func TestGetByBranch_NotFound(t *testing.T) {
	store := mustOpenStore(t)

	_, err := store.GetByBranch("/project", "nonexistent")
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("GetByBranch(nonexistent) error = %v, want ErrSessionNotFound", err)
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
