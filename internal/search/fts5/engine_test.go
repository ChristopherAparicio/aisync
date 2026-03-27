package fts5_test

import (
	"context"
	"database/sql"
	"testing"

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
