package fts5_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ChristopherAparicio/aisync/internal/search"
	"github.com/ChristopherAparicio/aisync/internal/search/fts5"
)

func TestFTS5_CreateAndSearch(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	engine, err := fts5.New(db)
	if err != nil {
		t.Fatalf("FTS5 New() failed: %v", err)
	}

	// Verify capabilities.
	caps := engine.Capabilities()
	if !caps.FullText {
		t.Error("expected FullText capability")
	}
	if !caps.Ranking {
		t.Error("expected Ranking capability")
	}
	if !caps.Highlights {
		t.Error("expected Highlights capability")
	}

	// Index a document.
	doc := search.Document{
		ID:          "ses_test_123",
		Summary:     "Fix authentication bug in login flow",
		Content:     "The user reported that OAuth tokens were expiring too quickly. We fixed the refresh token logic.",
		ToolNames:   "bash edit read",
		ProjectPath: "/test/project",
		Branch:      "fix/auth-bug",
		Agent:       "build",
		Provider:    "opencode",
	}
	if err := engine.Index(context.Background(), doc); err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	// Verify count.
	count, err := engine.IndexedCount()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 indexed doc, got %d", count)
	}

	// Search by summary keyword.
	result, err := engine.Search(context.Background(), search.Query{
		Text:  "authentication",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(result.Hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(result.Hits))
	}
	if result.Hits[0].SessionID != "ses_test_123" {
		t.Errorf("expected ses_test_123, got %s", result.Hits[0].SessionID)
	}
	if result.Engine != "fts5" {
		t.Errorf("expected engine=fts5, got %s", result.Engine)
	}

	// Search by content keyword (not in summary).
	result2, err := engine.Search(context.Background(), search.Query{
		Text:  "OAuth tokens",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Content search failed: %v", err)
	}
	if len(result2.Hits) != 1 {
		t.Fatalf("expected 1 hit for content search, got %d", len(result2.Hits))
	}

	// Search by tool name.
	result3, err := engine.Search(context.Background(), search.Query{
		Text:  "bash",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Tool search failed: %v", err)
	}
	if len(result3.Hits) != 1 {
		t.Fatalf("expected 1 hit for tool search, got %d", len(result3.Hits))
	}

	// Search with no results.
	result4, err := engine.Search(context.Background(), search.Query{
		Text:  "nonexistent_word_xyz",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("No-result search failed: %v", err)
	}
	if len(result4.Hits) != 0 {
		t.Fatalf("expected 0 hits, got %d", len(result4.Hits))
	}
}

func TestFTS5_SearchByProjectPath(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	engine, err := fts5.New(db)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	docs := []search.Document{
		{
			ID:          "ses_swarm",
			Summary:     "Refactor scheduler",
			Content:     "Reworked the task scheduler loop and retry backoff.",
			ProjectPath: "/Users/dev/opencode-agent-swarm",
			RemoteURL:   "https://github.com/dev/opencode-agent-swarm",
		},
		{
			ID:          "ses_other",
			Summary:     "Update docs",
			Content:     "Tidied the contributing guide.",
			ProjectPath: "/Users/dev/unrelated-project",
		},
	}
	for _, d := range docs {
		if err := engine.Index(ctx, d); err != nil {
			t.Fatalf("Index %s: %v", d.ID, err)
		}
	}

	result, err := engine.Search(ctx, search.Query{Text: "opencode agent swarm", Limit: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(result.Hits) == 0 {
		t.Fatal("expected at least 1 hit for project-path tokens, got 0")
	}
	if result.Hits[0].SessionID != "ses_swarm" {
		t.Errorf("expected ses_swarm ranked first, got %s", result.Hits[0].SessionID)
	}
}

func TestFTS5_SummaryOutranksLongContent(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	engine, err := fts5.New(db)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	filler := strings.Repeat("lorem ipsum dolor sit amet ", 2000)

	if err := engine.Index(ctx, search.Document{
		ID:      "ses_summary_hit",
		Summary: "Hermes agent coding session",
		Content: "Short notes about the run.",
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Index(ctx, search.Document{
		ID:      "ses_content_hit",
		Summary: "Unrelated maintenance",
		Content: filler + " hermes agent coding " + filler,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := engine.Search(ctx, search.Query{Text: "hermes agent coding", Limit: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(result.Hits) < 1 {
		t.Fatal("expected hits, got none")
	}
	if result.Hits[0].SessionID != "ses_summary_hit" {
		t.Errorf("expected summary match ranked first via BM25 weighting, got %s", result.Hits[0].SessionID)
	}
}

func TestFTS5_RerankPathMatchBeatsContentMention(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	engine, err := fts5.New(db)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	mention := strings.Repeat("opencode agent swarm ", 500)

	if err := engine.Index(ctx, search.Document{
		ID:          "ses_mention",
		Summary:     "House automation notes",
		Content:     mention,
		ProjectPath: "/Users/dev/house",
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Index(ctx, search.Document{
		ID:          "ses_real",
		Summary:     "Refactor scheduler",
		Content:     "Reworked the task scheduler loop.",
		ProjectPath: "/Users/dev/opencode-agent-swarm",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := engine.Search(ctx, search.Query{Text: "opencode agent swarm", Limit: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(result.Hits) < 2 {
		t.Fatalf("expected both docs as hits, got %d", len(result.Hits))
	}
	if result.Hits[0].SessionID != "ses_real" {
		t.Errorf("expected path-match ses_real to outrank high-TF content mention, got %s", result.Hits[0].SessionID)
	}
}

func TestFTS5_RerankSummaryAllTokensBoost(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	engine, err := fts5.New(db)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	mention := strings.Repeat("hermes agent coding ", 500)

	if err := engine.Index(ctx, search.Document{
		ID:      "ses_mention",
		Summary: "Unrelated maintenance",
		Content: mention,
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Index(ctx, search.Document{
		ID:      "ses_titled",
		Summary: "Hermes Agent - Coding (fork #1)",
		Content: "Short run notes.",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := engine.Search(ctx, search.Query{Text: "Hermes Agent Coding", Limit: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(result.Hits) < 2 {
		t.Fatalf("expected both docs as hits, got %d", len(result.Hits))
	}
	if result.Hits[0].SessionID != "ses_titled" {
		t.Errorf("expected summary all-tokens match ses_titled first, got %s", result.Hits[0].SessionID)
	}
}

func TestFTS5_MigratesLegacyUnindexedSchema(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	legacySchema := `CREATE VIRTUAL TABLE sessions_fts USING fts5(
		session_id UNINDEXED,
		summary,
		content,
		tool_names,
		project_path UNINDEXED,
		remote_url UNINDEXED,
		branch,
		agent,
		provider UNINDEXED,
		session_type UNINDEXED,
		created_at UNINDEXED,
		total_tokens UNINDEXED,
		message_count UNINDEXED,
		error_count UNINDEXED,
		tokenize='unicode61 remove_diacritics 2'
	)`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions_fts (
		session_id, summary, content, tool_names,
		project_path, remote_url, branch, agent, provider, session_type,
		created_at, total_tokens, message_count, error_count
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"ses_legacy", "Legacy summary", "Legacy content body", "bash",
		"/Users/dev/opencode-agent-swarm", "https://github.com/dev/opencode-agent-swarm",
		"main", "build", "opencode", "regular",
		time.Now().Format(time.RFC3339), 100, 5, 0,
	); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	engine, err := fts5.New(db)
	if err != nil {
		t.Fatalf("New() with legacy schema: %v", err)
	}

	count, err := engine.IndexedCount()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row preserved after migration, got %d", count)
	}

	result, err := engine.Search(context.Background(), search.Query{Text: "opencode agent swarm", Limit: 10})
	if err != nil {
		t.Fatalf("post-migration search: %v", err)
	}
	if len(result.Hits) != 1 || result.Hits[0].SessionID != "ses_legacy" {
		t.Fatalf("expected ses_legacy searchable by project path after migration, got %+v", result.Hits)
	}

	var schemaSQL string
	if err := db.QueryRow(
		"SELECT sql FROM sqlite_master WHERE type='table' AND name='sessions_fts'",
	).Scan(&schemaSQL); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(schemaSQL, "project_path UNINDEXED") {
		t.Error("expected project_path to be indexed after migration")
	}
}

func TestFTS5_ImplementsIncrementalIndexer(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	engine, err := fts5.New(db)
	if err != nil {
		t.Fatal(err)
	}

	// Verify FTS5 implements search.IncrementalIndexer.
	var _ search.IncrementalIndexer = engine
}

func TestFTS5_IndexedSessionIDs_Empty(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	engine, err := fts5.New(db)
	if err != nil {
		t.Fatal(err)
	}

	ids, err := engine.IndexedSessionIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 indexed IDs, got %d", len(ids))
	}
}

func TestFTS5_IndexedSessionIDs_WithDocs(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	engine, err := fts5.New(db)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	now := time.Now()

	// Index 3 documents.
	for _, id := range []string{"ses_aaa", "ses_bbb", "ses_ccc"} {
		if err := engine.Index(ctx, search.Document{
			ID:        id,
			Summary:   "Test session " + id,
			CreatedAt: now,
		}); err != nil {
			t.Fatalf("Index %s: %v", id, err)
		}
	}

	ids, err := engine.IndexedSessionIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 indexed IDs, got %d", len(ids))
	}
	for _, id := range []string{"ses_aaa", "ses_bbb", "ses_ccc"} {
		if !ids[id] {
			t.Errorf("expected %s in indexed set", id)
		}
	}

	// Delete one and verify.
	if err := engine.Delete(ctx, "ses_bbb"); err != nil {
		t.Fatal(err)
	}
	ids2, err := engine.IndexedSessionIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids2) != 2 {
		t.Fatalf("expected 2 indexed IDs after delete, got %d", len(ids2))
	}
	if ids2["ses_bbb"] {
		t.Error("ses_bbb should not be in indexed set after delete")
	}
}
