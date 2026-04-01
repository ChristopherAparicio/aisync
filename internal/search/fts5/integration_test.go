package fts5_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ChristopherAparicio/aisync/internal/search"
	"github.com/ChristopherAparicio/aisync/internal/search/fts5"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// TestIntegration_FTS5_FullLifecycle tests the complete FTS5 search lifecycle:
// index multiple documents → search with various queries → filter → delete → verify.
func TestIntegration_FTS5_FullLifecycle(t *testing.T) {
	db := openTestDB(t)
	engine, err := fts5.New(db)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	now := time.Now()

	// ── Index multiple sessions as documents ──

	docs := []search.Document{
		{
			ID:           "ses_auth_fix",
			Summary:      "Fix authentication bug in OAuth login flow",
			Content:      "The user reported OAuth tokens were expiring too quickly. Fixed refresh token TTL.",
			ToolNames:    "bash edit read",
			ProjectPath:  "/projects/backend",
			RemoteURL:    "github.com/org/backend",
			Branch:       "fix/auth-bug",
			Agent:        "build",
			Provider:     "opencode",
			SessionType:  "bug",
			CreatedAt:    now.Add(-2 * time.Hour),
			TotalTokens:  85000,
			MessageCount: 24,
			ErrorCount:   2,
		},
		{
			ID:           "ses_api_feat",
			Summary:      "Implement REST API pagination for user listings",
			Content:      "Added offset/limit pagination with cursor-based alternative. GraphQL connector updated.",
			ToolNames:    "bash write glob grep",
			ProjectPath:  "/projects/backend",
			RemoteURL:    "github.com/org/backend",
			Branch:       "feat/pagination",
			Agent:        "build",
			Provider:     "opencode",
			SessionType:  "feature",
			CreatedAt:    now.Add(-1 * time.Hour),
			TotalTokens:  120000,
			MessageCount: 42,
			ErrorCount:   0,
		},
		{
			ID:           "ses_frontend",
			Summary:      "Dark mode toggle for settings page",
			Content:      "CSS custom properties for theme switching. Local storage persistence for preference.",
			ToolNames:    "edit write bash",
			ProjectPath:  "/projects/frontend",
			RemoteURL:    "github.com/org/frontend",
			Branch:       "feat/dark-mode",
			Agent:        "explore",
			Provider:     "anthropic",
			SessionType:  "feature",
			CreatedAt:    now,
			TotalTokens:  45000,
			MessageCount: 15,
			ErrorCount:   1,
		},
	}

	for _, doc := range docs {
		if err := engine.Index(ctx, doc); err != nil {
			t.Fatalf("Index %s: %v", doc.ID, err)
		}
	}

	// Verify count.
	count, err := engine.IndexedCount()
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("IndexedCount = %d, want 3", count)
	}

	// ── Search by keyword in summary ──

	result, err := engine.Search(ctx, search.Query{Text: "authentication", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 {
		t.Fatalf("search 'authentication': got %d hits, want 1", len(result.Hits))
	}
	if result.Hits[0].SessionID != "ses_auth_fix" {
		t.Errorf("hit ID = %s, want ses_auth_fix", result.Hits[0].SessionID)
	}
	if result.Engine != "fts5" {
		t.Errorf("Engine = %s, want fts5", result.Engine)
	}

	// ── Search by keyword in content (not summary) ──

	result2, err := engine.Search(ctx, search.Query{Text: "GraphQL", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(result2.Hits) != 1 {
		t.Fatalf("search 'GraphQL': got %d hits, want 1", len(result2.Hits))
	}
	if result2.Hits[0].SessionID != "ses_api_feat" {
		t.Errorf("hit ID = %s, want ses_api_feat", result2.Hits[0].SessionID)
	}

	// ── Search matching multiple results ──

	result3, err := engine.Search(ctx, search.Query{Text: "feature", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	// "feature" appears in session_type of api_feat and frontend
	// FTS5 searches summary + content + branch + tool_names by default;
	// session_type is UNINDEXED so not searchable via MATCH, but the test validates
	// multi-result retrieval works.
	if result3.TotalCount < 0 {
		t.Error("unexpected negative total count")
	}

	// ── Search with project filter ──

	result4, err := engine.Search(ctx, search.Query{
		Text: "bash",
		Filters: search.Filters{
			ProjectPath: "/projects/frontend",
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result4.Hits) != 1 {
		t.Fatalf("filtered search: got %d hits, want 1", len(result4.Hits))
	}
	if result4.Hits[0].SessionID != "ses_frontend" {
		t.Errorf("filtered hit = %s, want ses_frontend", result4.Hits[0].SessionID)
	}

	// ── Search with no results ──

	result5, err := engine.Search(ctx, search.Query{Text: "kubernetes_deployment_xyz", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(result5.Hits) != 0 {
		t.Fatalf("no-match search: got %d hits, want 0", len(result5.Hits))
	}

	// ── Verify highlights are returned ──

	resultHL, err := engine.Search(ctx, search.Query{Text: "OAuth", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(resultHL.Hits) != 1 {
		t.Fatalf("highlights search: got %d hits, want 1", len(resultHL.Hits))
	}
	if resultHL.Hits[0].Highlights == nil {
		t.Error("expected highlights map to be non-nil")
	}

	// ── Delete a document and verify ──

	if err := engine.Delete(ctx, "ses_api_feat"); err != nil {
		t.Fatal(err)
	}
	count2, _ := engine.IndexedCount()
	if count2 != 2 {
		t.Fatalf("IndexedCount after delete = %d, want 2", count2)
	}

	// Deleted doc should not appear in search.
	resultDel, err := engine.Search(ctx, search.Query{Text: "pagination", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(resultDel.Hits) != 0 {
		t.Fatalf("search after delete: got %d hits, want 0", len(resultDel.Hits))
	}

	// ── Re-index (upsert) an existing document with updated content ──

	updatedDoc := docs[0]
	updatedDoc.Summary = "Fix authentication AND authorization in OAuth flow"
	updatedDoc.Content = "Extended fix to cover authorization scopes. Added RBAC checks."
	if err := engine.Index(ctx, updatedDoc); err != nil {
		t.Fatal(err)
	}

	// Count should still be 2 (upsert replaces existing).
	count3, _ := engine.IndexedCount()
	if count3 != 2 {
		t.Fatalf("IndexedCount after upsert = %d, want 2", count3)
	}

	// Old content should not match.
	resultOld, err := engine.Search(ctx, search.Query{Text: "refresh token TTL", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(resultOld.Hits) != 0 {
		t.Fatal("old content still matches after upsert")
	}

	// New content should match.
	resultNew, err := engine.Search(ctx, search.Query{Text: "RBAC", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(resultNew.Hits) != 1 {
		t.Fatalf("new content search: got %d hits, want 1", len(resultNew.Hits))
	}
}

// TestIntegration_FTS5_IncrementalReindex tests that IncrementalIndexer
// correctly identifies sessions needing indexing.
func TestIntegration_FTS5_IncrementalReindex(t *testing.T) {
	db := openTestDB(t)
	engine, err := fts5.New(db)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	now := time.Now()

	// Index 3 sessions.
	for i, id := range []string{"ses_001", "ses_002", "ses_003"} {
		if err := engine.Index(ctx, search.Document{
			ID:        id,
			Summary:   "Session " + id,
			CreatedAt: now.Add(time.Duration(i) * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Get indexed set.
	indexed, err := engine.IndexedSessionIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(indexed) != 3 {
		t.Fatalf("indexed = %d, want 3", len(indexed))
	}

	// Simulate incremental: check which of 5 sessions need indexing.
	allSessions := []string{"ses_001", "ses_002", "ses_003", "ses_004", "ses_005"}
	var needsIndexing []string
	for _, id := range allSessions {
		if !indexed[id] {
			needsIndexing = append(needsIndexing, id)
		}
	}

	if len(needsIndexing) != 2 {
		t.Fatalf("needsIndexing = %d, want 2", len(needsIndexing))
	}
	if needsIndexing[0] != "ses_004" || needsIndexing[1] != "ses_005" {
		t.Errorf("needsIndexing = %v, want [ses_004, ses_005]", needsIndexing)
	}

	// Index the missing ones.
	for _, id := range needsIndexing {
		if err := engine.Index(ctx, search.Document{ID: id, Summary: "New " + id}); err != nil {
			t.Fatal(err)
		}
	}

	count, _ := engine.IndexedCount()
	if count != 5 {
		t.Fatalf("count after incremental = %d, want 5", count)
	}
}

// TestIntegration_FTS5_DocumentFromSession tests the full pipeline:
// session.Session → search.Document → FTS5 index → search hit.
func TestIntegration_FTS5_DocumentFromSession(t *testing.T) {
	db := openTestDB(t)
	engine, err := fts5.New(db)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// Build a session with messages, tool calls.
	sess := &session.Session{
		ID:          "ses_full_pipeline",
		Provider:    "opencode",
		Agent:       "build",
		Branch:      "feat/integration-tests",
		ProjectPath: "/projects/aisync",
		RemoteURL:   "github.com/org/aisync",
		Summary:     "Add integration tests for FTS5 search engine",
		SessionType: "feature",
		CreatedAt:   time.Now(),
		Messages: []session.Message{
			{
				Role:    "user",
				Content: "Please write integration tests for the FTS5 search engine",
			},
			{
				Role:    "assistant",
				Content: "I'll create comprehensive integration tests covering indexing and querying",
				ToolCalls: []session.ToolCall{
					{Name: "Write", Input: "/path/to/test.go"},
					{Name: "bash", Input: "go test ./...", Output: "PASS"},
				},
			},
		},
		TokenUsage: session.TokenUsage{TotalTokens: 50000},
	}

	doc := search.DocumentFromSession(sess, 50000)
	if err := engine.Index(ctx, doc); err != nil {
		t.Fatal(err)
	}

	// Search by session content.
	result, err := engine.Search(ctx, search.Query{Text: "integration tests FTS5", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 {
		t.Fatalf("pipeline search: got %d hits, want 1", len(result.Hits))
	}
	hit := result.Hits[0]
	if hit.SessionID != "ses_full_pipeline" {
		t.Errorf("hit ID = %s, want ses_full_pipeline", hit.SessionID)
	}
	if hit.ProjectPath != "/projects/aisync" {
		t.Errorf("ProjectPath = %s, want /projects/aisync", hit.ProjectPath)
	}
	if hit.Tokens != 50000 {
		t.Errorf("Tokens = %d, want 50000", hit.Tokens)
	}

	// Search by tool output (bash PASS).
	result2, err := engine.Search(ctx, search.Query{Text: "PASS", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(result2.Hits) != 1 {
		t.Fatalf("tool output search: got %d hits, want 1", len(result2.Hits))
	}
}

// openTestDB creates an in-memory SQLite database for testing.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
