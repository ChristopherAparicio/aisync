package search_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ChristopherAparicio/aisync/internal/search"
	"github.com/ChristopherAparicio/aisync/internal/search/fts5"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// DocumentBuilder converts a session into a searchable document.
// Two implementations are injected into the eval harness:
//   - noisyBuilder: wraps DocumentFromSession (raw message content, tool echoes included)
//   - cleanBuilder: stub now; T6 will replace with a version that strips tool-echo noise
type DocumentBuilder func(sess *session.Session, maxLen int) search.Document

// noisyBuilder wraps the production DocumentFromSession for use as a DocumentBuilder.
func noisyBuilder(sess *session.Session, maxLen int) search.Document {
	return search.DocumentFromSession(sess, maxLen)
}

// stubCleanBuilder is a placeholder that mirrors noisyBuilder.
// T6 will replace this with a real filter that removes tool-echo inflation.
// T7 will plug the real version in by replacing this function reference.
func stubCleanBuilder(sess *session.Session, maxLen int) search.Document {
	return search.DocumentFromSession(sess, maxLen)
}

// precisionAtK returns the fraction of expected IDs found in the first k results.
//
// results is the ordered list of session IDs returned by the search engine.
// expected is the ground-truth set of relevant session IDs.
// k caps how many results are examined.
//
// Returns len(results[:k] ∩ expected) / len(expected).
// Returns 0.0 when expected is empty to avoid division by zero.
func precisionAtK(results []string, expected []string, k int) float64 {
	if len(expected) == 0 {
		return 0.0
	}
	want := make(map[string]bool, len(expected))
	for _, id := range expected {
		want[id] = true
	}
	limit := k
	if limit > len(results) {
		limit = len(results)
	}
	found := 0
	for _, id := range results[:limit] {
		if want[id] {
			found++
		}
	}
	return float64(found) / float64(len(expected))
}

// evalQuery is one entry in the fixture file testdata/eval_queries.json.
type evalQuery struct {
	Query       string   `json:"query"`
	ExpectedIDs []string `json:"expected_ids"`
}

// evalHarness indexes a corpus with a given DocumentBuilder and scores eval queries.
// Construct via newEvalHarness; the underlying FTS5 engine is in-memory per test.
type evalHarness struct {
	engine       *fts5.Engine
	noisyBuilder DocumentBuilder
	cleanBuilder DocumentBuilder
}

func newEvalHarness(t *testing.T, noisy, clean DocumentBuilder) *evalHarness {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	eng, err := fts5.New(db)
	if err != nil {
		t.Fatal(err)
	}
	return &evalHarness{engine: eng, noisyBuilder: noisy, cleanBuilder: clean}
}

// indexCorpus indexes every session in corpus using the supplied builder.
func (h *evalHarness) indexCorpus(ctx context.Context, corpus []*session.Session, builder DocumentBuilder) error {
	const maxContentLen = 50000
	for _, sess := range corpus {
		doc := builder(sess, maxContentLen)
		if err := h.engine.Index(ctx, doc); err != nil {
			return err
		}
	}
	return nil
}

// scoreQuery runs a single eval query and returns precision@k.
func (h *evalHarness) scoreQuery(ctx context.Context, q evalQuery, k int) (float64, error) {
	result, err := h.engine.Search(ctx, search.Query{Text: q.Query, Limit: k})
	if err != nil {
		return 0, err
	}
	ids := make([]string, len(result.Hits))
	for i, hit := range result.Hits {
		ids[i] = hit.SessionID
	}
	return precisionAtK(ids, q.ExpectedIDs, k), nil
}

// ── Unit tests for precisionAtK ──────────────────────────────────────────────

func TestPrecisionAtK(t *testing.T) {
	ids := func(prefix string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = prefix + string(rune('a'+i))
		}
		return out
	}

	tests := []struct {
		name     string
		results  []string
		expected []string
		k        int
		want     float64
	}{
		{
			// 3 of the 10 expected IDs appear in the first k results → 0.3
			name:     "ThreeOfTen",
			results:  append(ids("e", 3), ids("x", 7)...),
			expected: ids("e", 10),
			k:        10,
			want:     0.3,
		},
		{
			// None of the expected IDs appear in the first k results → 0.0
			name:     "ZeroOfTen",
			results:  ids("x", 10),
			expected: ids("e", 10),
			k:        10,
			want:     0.0,
		},
		{
			// All 10 expected IDs appear in the first k results → 1.0
			name:     "TenOfTen",
			results:  ids("e", 10),
			expected: ids("e", 10),
			k:        10,
			want:     1.0,
		},
		{
			// k smaller than results: only examine first 5 of 10 results
			name:     "KCapsLookup",
			results:  append(ids("e", 5), ids("x", 5)...),
			expected: ids("e", 10),
			k:        5,
			want:     0.5,
		},
		{
			// k larger than results slice: clamp to len(results)
			name:     "KExceedsResults",
			results:  ids("e", 3),
			expected: ids("e", 3),
			k:        100,
			want:     1.0,
		},
		{
			// Empty expected → always 0.0 (no division by zero)
			name:     "EmptyExpected",
			results:  ids("e", 5),
			expected: []string{},
			k:        10,
			want:     0.0,
		},
		{
			// Empty results, non-empty expected → 0.0
			name:     "EmptyResults",
			results:  []string{},
			expected: ids("e", 5),
			k:        10,
			want:     0.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := precisionAtK(tc.results, tc.expected, tc.k)
			if got != tc.want {
				t.Errorf("precisionAtK(results, expected, k=%d) = %.4f, want %.4f",
					tc.k, got, tc.want)
			}
		})
	}
}

// ── Fixture loading ───────────────────────────────────────────────────────────

// TestEvalHarness_LoadFixture validates the fixture schema.
// It skips gracefully when the fixture file has not been created yet (T2 runs in parallel).
func TestEvalHarness_LoadFixture(t *testing.T) {
	if _, err := os.Stat("testdata/eval_queries.json"); os.IsNotExist(err) {
		t.Skip("fixture not yet created")
	}

	data, err := os.ReadFile("testdata/eval_queries.json")
	if err != nil {
		t.Fatal(err)
	}

	var queries []evalQuery
	if err := json.Unmarshal(data, &queries); err != nil {
		t.Fatalf("malformed fixture JSON: %v", err)
	}
	if len(queries) == 0 {
		t.Fatal("fixture contains no queries")
	}

	for i, q := range queries {
		if q.Query == "" {
			t.Errorf("fixture[%d]: empty query field", i)
		}
		if len(q.ExpectedIDs) == 0 {
			t.Errorf("fixture[%d] %q: expected_ids is empty", i, q.Query)
		}
	}

	t.Logf("loaded %d eval queries from fixture", len(queries))
}

// ── End-to-end harness test ───────────────────────────────────────────────────

// TestEvalHarness indexes a small deterministic corpus, runs a query through
// both noisy and clean builders, and asserts that a numeric score is produced.
// This is the smoke test: it validates the harness pipeline, not the ranking quality.
func TestEvalHarness(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	corpus := []*session.Session{
		{
			ID:          "ses_eval_auth",
			Provider:    "opencode",
			Agent:       "build",
			Branch:      "fix/auth-token",
			ProjectPath: "/projects/backend",
			Summary:     "Fix authentication token refresh bug in OAuth flow",
			CreatedAt:   now.Add(-3 * time.Hour),
			Messages: []session.Message{
				{Role: "user", Content: "The OAuth token refresh is failing with a 401."},
				{Role: "assistant", Content: "I'll fix the token refresh TTL in the auth service."},
			},
			TokenUsage: session.TokenUsage{TotalTokens: 12000},
		},
		{
			ID:          "ses_eval_db",
			Provider:    "opencode",
			Agent:       "build",
			Branch:      "feat/db-migration",
			ProjectPath: "/projects/backend",
			Summary:     "Add database migration for user table schema update",
			CreatedAt:   now.Add(-2 * time.Hour),
			Messages: []session.Message{
				{Role: "user", Content: "Add a migration to add the new columns to the user table."},
				{Role: "assistant", Content: "Creating the Alembic migration file for the user schema."},
			},
			TokenUsage: session.TokenUsage{TotalTokens: 9000},
		},
		{
			ID:          "ses_eval_ui",
			Provider:    "opencode",
			Agent:       "explore",
			Branch:      "feat/dark-mode",
			ProjectPath: "/projects/frontend",
			Summary:     "Implement dark mode toggle in the settings page",
			CreatedAt:   now.Add(-1 * time.Hour),
			Messages: []session.Message{
				{Role: "user", Content: "Add a dark mode toggle to the settings page."},
				{Role: "assistant", Content: "I'll use CSS custom properties and localStorage for persistence."},
			},
			TokenUsage: session.TokenUsage{TotalTokens: 7000},
		},
		{
			ID:          "ses_eval_cicd",
			Provider:    "opencode",
			Agent:       "build",
			Branch:      "feat/ci-pipeline",
			ProjectPath: "/projects/infra",
			Summary:     "Set up GitHub Actions CI pipeline with test and lint steps",
			CreatedAt:   now,
			Messages: []session.Message{
				{Role: "user", Content: "Configure the GitHub Actions workflow for CI."},
				{Role: "assistant", Content: "Adding .github/workflows/ci.yml with test and lint jobs."},
			},
			TokenUsage: session.TokenUsage{TotalTokens: 5000},
		},
	}

	t.Run("NoisyBuilder", func(t *testing.T) {
		h := newEvalHarness(t, noisyBuilder, stubCleanBuilder)
		if err := h.indexCorpus(ctx, corpus, h.noisyBuilder); err != nil {
			t.Fatalf("indexCorpus (noisy): %v", err)
		}

		q := evalQuery{
			Query:       "authentication token OAuth",
			ExpectedIDs: []string{"ses_eval_auth"},
		}

		score, err := h.scoreQuery(ctx, q, 10)
		if err != nil {
			t.Fatalf("scoreQuery: %v", err)
		}
		if score < 0.0 || score > 1.0 {
			t.Errorf("score out of [0,1]: %f", score)
		}
		t.Logf("noisy builder precision@10 for %q = %.2f", q.Query, score)
	})

	t.Run("CleanBuilder", func(t *testing.T) {
		h := newEvalHarness(t, noisyBuilder, stubCleanBuilder)
		if err := h.indexCorpus(ctx, corpus, h.cleanBuilder); err != nil {
			t.Fatalf("indexCorpus (clean): %v", err)
		}

		q := evalQuery{
			Query:       "database migration schema",
			ExpectedIDs: []string{"ses_eval_db"},
		}

		score, err := h.scoreQuery(ctx, q, 10)
		if err != nil {
			t.Fatalf("scoreQuery: %v", err)
		}
		if score < 0.0 || score > 1.0 {
			t.Errorf("score out of [0,1]: %f", score)
		}
		t.Logf("clean builder precision@10 for %q = %.2f", q.Query, score)
	})
}
