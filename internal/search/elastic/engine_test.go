package elastic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/search"
)

// newMockES creates a mock Elasticsearch server and returns the engine.
// The handler map routes method+path to handler functions.
func newMockES(t *testing.T, handlers map[string]http.HandlerFunc) *Engine {
	t.Helper()

	mux := http.NewServeMux()
	for pattern, handler := range handlers {
		mux.HandleFunc(pattern, handler)
	}

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	// Create engine with the mock server (skip ensureIndex by pre-registering HEAD handler).
	e := &Engine{
		baseURL:   ts.URL,
		indexName: "test-sessions",
		client:    ts.Client(),
	}
	return e
}

// newMockESWithIndex creates a mock ES and initializes via New() (tests ensureIndex).
func newMockESWithIndex(t *testing.T, indexExists bool) (*Engine, *httptest.Server) {
	t.Helper()

	mux := http.NewServeMux()

	// HEAD /test-sessions — check if index exists.
	mux.HandleFunc("HEAD /test-sessions", func(w http.ResponseWriter, r *http.Request) {
		if indexExists {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	})

	// PUT /test-sessions — create index.
	mux.HandleFunc("PUT /test-sessions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"acknowledged":true}`))
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	e, err := New(Config{
		URL:        ts.URL,
		IndexName:  "test-sessions",
		HTTPClient: ts.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return e, ts
}

// ── Constructor Tests ──

func TestNew_createsIndex(t *testing.T) {
	created := false

	mux := http.NewServeMux()
	mux.HandleFunc("HEAD /test-sessions", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound) // Index doesn't exist
	})
	mux.HandleFunc("PUT /test-sessions", func(w http.ResponseWriter, r *http.Request) {
		created = true
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "code_analyzer") {
			t.Error("expected mapping to contain code_analyzer")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"acknowledged":true}`))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	e, err := New(Config{
		URL:        ts.URL,
		IndexName:  "test-sessions",
		HTTPClient: ts.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if !created {
		t.Error("expected index to be created")
	}
	if e.Name() != "elasticsearch" {
		t.Errorf("Name() = %q, want elasticsearch", e.Name())
	}
}

func TestNew_indexAlreadyExists(t *testing.T) {
	e, _ := newMockESWithIndex(t, true)
	if e == nil {
		t.Fatal("expected engine to be created")
	}
}

func TestNew_defaultConfig(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// HEAD /aisync-sessions — default index name
		if r.Method == http.MethodHead && r.URL.Path == "/aisync-sessions" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	e, err := New(Config{URL: ts.URL, HTTPClient: ts.Client()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if e.indexName != "aisync-sessions" {
		t.Errorf("indexName = %q, want aisync-sessions", e.indexName)
	}
}

// ── Capabilities ──

func TestCapabilities(t *testing.T) {
	e, _ := newMockESWithIndex(t, true)
	caps := e.Capabilities()
	if !caps.FullText {
		t.Error("expected FullText=true")
	}
	if !caps.Facets {
		t.Error("expected Facets=true")
	}
	if !caps.Highlights {
		t.Error("expected Highlights=true")
	}
	if !caps.FuzzyMatch {
		t.Error("expected FuzzyMatch=true")
	}
	if !caps.Ranking {
		t.Error("expected Ranking=true")
	}
	if caps.Semantic {
		t.Error("expected Semantic=false")
	}
}

// ── Search ──

func TestSearch_emptyQuery(t *testing.T) {
	e, _ := newMockESWithIndex(t, true)

	result, err := e.Search(context.Background(), search.Query{Text: ""})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Engine != "elasticsearch" {
		t.Errorf("Engine = %q, want elasticsearch", result.Engine)
	}
	if len(result.Hits) != 0 {
		t.Errorf("expected 0 hits for empty query, got %d", len(result.Hits))
	}
}

func TestSearch_withResults(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("HEAD /test-sessions", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /test-sessions/_search", func(w http.ResponseWriter, r *http.Request) {
		// Verify the request body contains expected fields.
		body, _ := io.ReadAll(r.Body)
		var reqBody map[string]interface{}
		_ = json.Unmarshal(body, &reqBody)

		if _, ok := reqBody["highlight"]; !ok {
			t.Error("expected highlight in search request")
		}

		response := `{
			"took": 5,
			"hits": {
				"total": {"value": 1},
				"hits": [{
					"_id": "sess-123",
					"_score": 12.5,
					"_source": {
						"summary": "Fix auth flow",
						"content": "implemented OAuth login",
						"project_path": "/tmp/proj",
						"remote_url": "git@github.com:org/repo.git",
						"branch": "feature/auth",
						"agent": "coder",
						"provider": "anthropic",
						"created_at": "2026-03-15T10:00:00Z",
						"total_tokens": 5000,
						"message_count": 20,
						"error_count": 1
					},
					"highlight": {
						"summary": ["Fix <mark>auth</mark> flow"],
						"content": ["implemented <mark>OAuth</mark> login"]
					}
				}]
			}
		}`
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	e := &Engine{baseURL: ts.URL, indexName: "test-sessions", client: ts.Client()}

	result, err := e.Search(context.Background(), search.Query{Text: "auth"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.TotalCount != 1 {
		t.Errorf("TotalCount = %d, want 1", result.TotalCount)
	}
	if len(result.Hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(result.Hits))
	}
	hit := result.Hits[0]
	if hit.SessionID != "sess-123" {
		t.Errorf("SessionID = %q, want sess-123", hit.SessionID)
	}
	if hit.Score != 12.5 {
		t.Errorf("Score = %f, want 12.5", hit.Score)
	}
	if hit.Summary != "Fix auth flow" {
		t.Errorf("Summary = %q, want 'Fix auth flow'", hit.Summary)
	}
	if hit.Branch != "feature/auth" {
		t.Errorf("Branch = %q, want feature/auth", hit.Branch)
	}
	if hit.Tokens != 5000 {
		t.Errorf("Tokens = %d, want 5000", hit.Tokens)
	}
	if hit.Errors != 1 {
		t.Errorf("Errors = %d, want 1", hit.Errors)
	}
	if hl, ok := hit.Highlights["summary"]; !ok || !strings.Contains(hl, "<mark>auth</mark>") {
		t.Errorf("summary highlight = %q, want mark around auth", hl)
	}
	if hl, ok := hit.Highlights["content"]; !ok || !strings.Contains(hl, "<mark>OAuth</mark>") {
		t.Errorf("content highlight = %q, want mark around OAuth", hl)
	}
}

func TestSearch_withFilters(t *testing.T) {
	var receivedQuery map[string]interface{}

	mux := http.NewServeMux()
	mux.HandleFunc("HEAD /test-sessions", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /test-sessions/_search", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedQuery)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"took":1,"hits":{"total":{"value":0},"hits":[]}}`))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	e := &Engine{baseURL: ts.URL, indexName: "test-sessions", client: ts.Client()}

	_, err := e.Search(context.Background(), search.Query{
		Text: "test",
		Filters: search.Filters{
			Branch:   "main",
			Provider: "anthropic",
			Agent:    "coder",
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	// Verify the bool query has filter clauses.
	queryObj, _ := receivedQuery["query"].(map[string]interface{})
	boolObj, _ := queryObj["bool"].(map[string]interface{})
	filters, _ := boolObj["filter"].([]interface{})
	if len(filters) != 3 {
		t.Errorf("expected 3 filter clauses, got %d", len(filters))
	}

	// Verify size.
	if size, ok := receivedQuery["size"].(float64); !ok || int(size) != 10 {
		t.Errorf("size = %v, want 10", receivedQuery["size"])
	}
}

func TestSearch_withFacets(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("HEAD /test-sessions", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /test-sessions/_search", func(w http.ResponseWriter, _ *http.Request) {
		response := `{
			"took": 2,
			"hits": {"total": {"value": 0}, "hits": []},
			"aggregations": {
				"branch": {
					"buckets": [
						{"key": "main", "doc_count": 50},
						{"key": "feature/auth", "doc_count": 10}
					]
				}
			}
		}`
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	e := &Engine{baseURL: ts.URL, indexName: "test-sessions", client: ts.Client()}

	result, err := e.Search(context.Background(), search.Query{
		Text:        "test",
		FacetFields: []string{"branch"},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Facets == nil {
		t.Fatal("expected facets")
	}
	branchFacets, ok := result.Facets["branch"]
	if !ok {
		t.Fatal("expected branch facets")
	}
	if len(branchFacets) != 2 {
		t.Errorf("branch facets = %d, want 2", len(branchFacets))
	}
	if branchFacets[0].Value != "main" || branchFacets[0].Count != 50 {
		t.Errorf("first facet = %+v, want {main, 50}", branchFacets[0])
	}
}

func TestSearch_serverError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("HEAD /test-sessions", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /test-sessions/_search", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"cluster unavailable"}`))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	e := &Engine{baseURL: ts.URL, indexName: "test-sessions", client: ts.Client()}

	_, err := e.Search(context.Background(), search.Query{Text: "test"})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, want status 500", err.Error())
	}
}

// ── Index ──

func TestIndex_document(t *testing.T) {
	var indexedDoc esDocument
	var indexedID string

	mux := http.NewServeMux()
	mux.HandleFunc("HEAD /test-sessions", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("PUT /test-sessions/_doc/", func(w http.ResponseWriter, r *http.Request) {
		indexedID = strings.TrimPrefix(r.URL.Path, "/test-sessions/_doc/")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &indexedDoc)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"result":"created"}`))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	e := &Engine{baseURL: ts.URL, indexName: "test-sessions", client: ts.Client()}

	doc := search.Document{
		ID:           "sess-abc",
		Summary:      "Test session",
		Content:      "some message content",
		ToolNames:    "bash edit",
		ProjectPath:  "/tmp/proj",
		RemoteURL:    "git@github.com:org/repo.git",
		Branch:       "main",
		Agent:        "coder",
		Provider:     "anthropic",
		SessionType:  "feature",
		CreatedAt:    time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC),
		TotalTokens:  5000,
		MessageCount: 20,
		ErrorCount:   0,
	}

	err := e.Index(context.Background(), doc)
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if indexedID != "sess-abc" {
		t.Errorf("indexed ID = %q, want sess-abc", indexedID)
	}
	if indexedDoc.Summary != "Test session" {
		t.Errorf("summary = %q, want 'Test session'", indexedDoc.Summary)
	}
	if indexedDoc.Branch != "main" {
		t.Errorf("branch = %q, want main", indexedDoc.Branch)
	}
	if indexedDoc.TotalTokens != 5000 {
		t.Errorf("total_tokens = %d, want 5000", indexedDoc.TotalTokens)
	}
}

func TestIndex_serverError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"disk full"}`))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	e := &Engine{baseURL: ts.URL, indexName: "test-sessions", client: ts.Client()}

	err := e.Index(context.Background(), search.Document{ID: "test"})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

// ── Delete ──

func TestDelete_existing(t *testing.T) {
	var deletedID string
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /test-sessions/_doc/", func(w http.ResponseWriter, r *http.Request) {
		deletedID = strings.TrimPrefix(r.URL.Path, "/test-sessions/_doc/")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"deleted"}`))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	e := &Engine{baseURL: ts.URL, indexName: "test-sessions", client: ts.Client()}

	err := e.Delete(context.Background(), "sess-abc")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deletedID != "sess-abc" {
		t.Errorf("deleted ID = %q, want sess-abc", deletedID)
	}
}

func TestDelete_notFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"result":"not_found"}`))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	e := &Engine{baseURL: ts.URL, indexName: "test-sessions", client: ts.Client()}

	// 404 should not be an error.
	err := e.Delete(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("Delete() error = %v, want nil for 404", err)
	}
}

// ── IndexedSessionIDs ──

func TestIndexedSessionIDs(t *testing.T) {
	callCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("POST /test-sessions/_search", func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		response := `{
			"_scroll_id": "scroll123",
			"hits": {
				"hits": [
					{"_id": "sess-1"},
					{"_id": "sess-2"},
					{"_id": "sess-3"}
				]
			}
		}`
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	})
	mux.HandleFunc("POST /_search/scroll", func(w http.ResponseWriter, _ *http.Request) {
		// Second scroll returns empty → stop.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"_scroll_id":"scroll123","hits":{"hits":[]}}`))
	})
	mux.HandleFunc("DELETE /_search/scroll", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	e := &Engine{baseURL: ts.URL, indexName: "test-sessions", client: ts.Client()}

	ids, err := e.IndexedSessionIDs()
	if err != nil {
		t.Fatalf("IndexedSessionIDs() error = %v", err)
	}
	if len(ids) != 3 {
		t.Errorf("got %d IDs, want 3", len(ids))
	}
	for _, id := range []string{"sess-1", "sess-2", "sess-3"} {
		if !ids[id] {
			t.Errorf("expected %s in indexed IDs", id)
		}
	}
}

// ── Build Query Tests ──

func TestBuildSearchQuery_allFilters(t *testing.T) {
	e := &Engine{indexName: "test"}
	hasErrors := true

	q := search.Query{
		Text: "auth flow",
		Filters: search.Filters{
			ProjectPath:     "/tmp/proj",
			RemoteURL:       "git@github.com:org/repo.git",
			Branch:          "main",
			Provider:        "anthropic",
			Agent:           "coder",
			SessionType:     "feature",
			ProjectCategory: "web",
			Since:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Until:           time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
			HasErrors:       &hasErrors,
		},
		FacetFields: []string{"branch", "agent"},
		Limit:       25,
		Offset:      10,
	}

	esQuery := e.buildSearchQuery(q, 25)

	// Check that query has aggs.
	if _, ok := esQuery["aggs"]; !ok {
		t.Error("expected aggs in query")
	}

	// Check size and from.
	if size, ok := esQuery["size"].(int); !ok || size != 25 {
		t.Errorf("size = %v, want 25", esQuery["size"])
	}
	if from, ok := esQuery["from"].(int); !ok || from != 10 {
		t.Errorf("from = %v, want 10", esQuery["from"])
	}

	// Verify the bool query exists.
	queryObj, _ := esQuery["query"].(map[string]interface{})
	boolObj, _ := queryObj["bool"].(map[string]interface{})
	if boolObj == nil {
		t.Fatal("expected bool query")
	}

	// Verify filters — 10 filters: 7 terms + 1 range(date) + 1 range(errors)
	filters, _ := boolObj["filter"].([]map[string]interface{})
	if len(filters) != 9 {
		t.Errorf("expected 9 filter clauses, got %d", len(filters))
	}
}

func TestClose(t *testing.T) {
	e, _ := newMockESWithIndex(t, true)
	if err := e.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}
