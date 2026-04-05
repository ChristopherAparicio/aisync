// Package elastic implements a full-text search engine using Elasticsearch 8.x.
//
// Elasticsearch provides:
//   - Full-text search with BM25 relevance ranking
//   - Highlighted snippets
//   - Fuzzy matching (tolerates typos)
//   - Faceted aggregations (group by project, branch, etc.)
//   - Horizontal scaling for large deployments
//
// The adapter auto-creates the index with an appropriate mapping on first use.
// It stays in sync via explicit Index/Delete calls from the post-capture hook.
package elastic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/search"
)

// Config holds Elasticsearch connection settings.
type Config struct {
	// URL is the Elasticsearch base URL (e.g. "http://localhost:9200").
	URL string

	// IndexName is the index to use (e.g. "aisync-sessions").
	IndexName string

	// HTTPClient is the HTTP client to use. If nil, http.DefaultClient is used.
	HTTPClient *http.Client
}

// Engine implements search.Engine using Elasticsearch.
type Engine struct {
	baseURL   string
	indexName string
	client    *http.Client
}

// New creates an Elasticsearch search engine and ensures the index exists.
func New(cfg Config) (*Engine, error) {
	if cfg.URL == "" {
		cfg.URL = "http://localhost:9200"
	}
	cfg.URL = strings.TrimRight(cfg.URL, "/")

	if cfg.IndexName == "" {
		cfg.IndexName = "aisync-sessions"
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	e := &Engine{
		baseURL:   cfg.URL,
		indexName: cfg.IndexName,
		client:    client,
	}

	// Ensure the index exists with the appropriate mapping.
	if err := e.ensureIndex(context.Background()); err != nil {
		return nil, fmt.Errorf("ensure index: %w", err)
	}

	return e, nil
}

func (e *Engine) Name() string { return "elasticsearch" }

func (e *Engine) Capabilities() search.Capabilities {
	return search.Capabilities{
		FullText:   true,
		Semantic:   false,
		Facets:     true,
		Highlights: true,
		FuzzyMatch: true,
		Ranking:    true,
	}
}

func (e *Engine) Close() error {
	return nil // HTTP client is stateless
}

// ── Index Management ──

// indexMapping is the Elasticsearch mapping for the aisync-sessions index.
const indexMapping = `{
  "settings": {
    "number_of_shards": 1,
    "number_of_replicas": 0,
    "analysis": {
      "analyzer": {
        "code_analyzer": {
          "type": "custom",
          "tokenizer": "standard",
          "filter": ["lowercase", "asciifolding"]
        }
      }
    }
  },
  "mappings": {
    "properties": {
      "summary":          {"type": "text", "analyzer": "code_analyzer"},
      "content":          {"type": "text", "analyzer": "code_analyzer"},
      "tool_names":       {"type": "text", "analyzer": "code_analyzer"},
      "project_path":     {"type": "keyword"},
      "remote_url":       {"type": "keyword"},
      "branch":           {"type": "keyword"},
      "agent":            {"type": "keyword"},
      "provider":         {"type": "keyword"},
      "session_type":     {"type": "keyword"},
      "project_category": {"type": "keyword"},
      "created_at":       {"type": "date"},
      "total_tokens":     {"type": "integer"},
      "message_count":    {"type": "integer"},
      "error_count":      {"type": "integer"}
    }
  }
}`

func (e *Engine) ensureIndex(ctx context.Context) error {
	// Check if index exists.
	resp, err := e.doRequest(ctx, http.MethodHead, "/"+e.indexName, nil)
	if err != nil {
		return fmt.Errorf("check index: %w", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil // Index already exists
	}

	// Create the index.
	resp, err = e.doRequest(ctx, http.MethodPut, "/"+e.indexName, strings.NewReader(indexMapping))
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("create index: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// ── Search ──

func (e *Engine) Search(ctx context.Context, query search.Query) (*search.Result, error) {
	start := time.Now()

	if query.Text == "" {
		return &search.Result{Engine: "elasticsearch", Took: time.Since(start)}, nil
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}

	esQuery := e.buildSearchQuery(query, limit)

	body, err := json.Marshal(esQuery)
	if err != nil {
		return nil, fmt.Errorf("marshal query: %w", err)
	}

	resp, err := e.doRequest(ctx, http.MethodPost, "/"+e.indexName+"/_search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("search request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("search: status %d: %s", resp.StatusCode, string(respBody))
	}

	var esResult esSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&esResult); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return e.parseSearchResponse(esResult, start), nil
}

func (e *Engine) buildSearchQuery(query search.Query, limit int) map[string]interface{} {
	// Multi-match query: search across summary, content, tool_names, branch.
	multiMatch := map[string]interface{}{
		"multi_match": map[string]interface{}{
			"query":     query.Text,
			"fields":    []string{"summary^3", "content", "tool_names", "branch^2"},
			"type":      "best_fields",
			"fuzziness": "AUTO",
		},
	}

	// Build filter clauses.
	var filters []map[string]interface{}
	if f := query.Filters; f.ProjectPath != "" {
		filters = append(filters, map[string]interface{}{"term": map[string]interface{}{"project_path": f.ProjectPath}})
	}
	if f := query.Filters; f.RemoteURL != "" {
		filters = append(filters, map[string]interface{}{"term": map[string]interface{}{"remote_url": f.RemoteURL}})
	}
	if f := query.Filters; f.Branch != "" {
		filters = append(filters, map[string]interface{}{"term": map[string]interface{}{"branch": f.Branch}})
	}
	if f := query.Filters; f.Provider != "" {
		filters = append(filters, map[string]interface{}{"term": map[string]interface{}{"provider": f.Provider}})
	}
	if f := query.Filters; f.Agent != "" {
		filters = append(filters, map[string]interface{}{"term": map[string]interface{}{"agent": f.Agent}})
	}
	if f := query.Filters; f.SessionType != "" {
		filters = append(filters, map[string]interface{}{"term": map[string]interface{}{"session_type": f.SessionType}})
	}
	if f := query.Filters; f.ProjectCategory != "" {
		filters = append(filters, map[string]interface{}{"term": map[string]interface{}{"project_category": f.ProjectCategory}})
	}
	if f := query.Filters; !f.Since.IsZero() || !f.Until.IsZero() {
		rangeQuery := map[string]interface{}{}
		if !f.Since.IsZero() {
			rangeQuery["gte"] = f.Since.Format(time.RFC3339)
		}
		if !f.Until.IsZero() {
			rangeQuery["lte"] = f.Until.Format(time.RFC3339)
		}
		filters = append(filters, map[string]interface{}{"range": map[string]interface{}{"created_at": rangeQuery}})
	}
	if f := query.Filters; f.HasErrors != nil {
		if *f.HasErrors {
			filters = append(filters, map[string]interface{}{"range": map[string]interface{}{"error_count": map[string]interface{}{"gt": 0}}})
		} else {
			filters = append(filters, map[string]interface{}{"term": map[string]interface{}{"error_count": 0}})
		}
	}

	// Combine into a bool query.
	boolQuery := map[string]interface{}{
		"must": multiMatch,
	}
	if len(filters) > 0 {
		boolQuery["filter"] = filters
	}

	esQuery := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": boolQuery,
		},
		"size": limit,
		"from": query.Offset,
		"highlight": map[string]interface{}{
			"fields": map[string]interface{}{
				"summary": map[string]interface{}{
					"pre_tags":  []string{"<mark>"},
					"post_tags": []string{"</mark>"},
				},
				"content": map[string]interface{}{
					"pre_tags":            []string{"<mark>"},
					"post_tags":           []string{"</mark>"},
					"fragment_size":       200,
					"number_of_fragments": 2,
				},
			},
		},
		"_source": true,
	}

	// Add aggregations if facets requested.
	if len(query.FacetFields) > 0 {
		aggs := make(map[string]interface{})
		for _, field := range query.FacetFields {
			aggs[field] = map[string]interface{}{
				"terms": map[string]interface{}{
					"field": field,
					"size":  20,
				},
			}
		}
		esQuery["aggs"] = aggs
	}

	return esQuery
}

// ── ES Response Types ──

type esSearchResponse struct {
	Took int `json:"took"` // milliseconds
	Hits struct {
		Total struct {
			Value int `json:"value"`
		} `json:"total"`
		Hits []esHit `json:"hits"`
	} `json:"hits"`
	Aggregations map[string]esAggBucket `json:"aggregations,omitempty"`
}

type esHit struct {
	ID        string              `json:"_id"`
	Score     float64             `json:"_score"`
	Source    json.RawMessage     `json:"_source"`
	Highlight map[string][]string `json:"highlight,omitempty"`
}

type esAggBucket struct {
	Buckets []struct {
		Key      string `json:"key"`
		DocCount int    `json:"doc_count"`
	} `json:"buckets"`
}

type esDocument struct {
	Summary         string    `json:"summary"`
	Content         string    `json:"content"`
	ToolNames       string    `json:"tool_names"`
	ProjectPath     string    `json:"project_path"`
	RemoteURL       string    `json:"remote_url"`
	Branch          string    `json:"branch"`
	Agent           string    `json:"agent"`
	Provider        string    `json:"provider"`
	SessionType     string    `json:"session_type"`
	ProjectCategory string    `json:"project_category"`
	CreatedAt       time.Time `json:"created_at"`
	TotalTokens     int       `json:"total_tokens"`
	MessageCount    int       `json:"message_count"`
	ErrorCount      int       `json:"error_count"`
}

func (e *Engine) parseSearchResponse(esResult esSearchResponse, start time.Time) *search.Result {
	result := &search.Result{
		TotalCount: esResult.Hits.Total.Value,
		Engine:     "elasticsearch",
		Took:       time.Since(start),
	}

	for _, hit := range esResult.Hits.Hits {
		var doc esDocument
		if err := json.Unmarshal(hit.Source, &doc); err != nil {
			continue
		}

		searchHit := search.Hit{
			SessionID:   hit.ID,
			Score:       hit.Score,
			Summary:     doc.Summary,
			ProjectPath: doc.ProjectPath,
			RemoteURL:   doc.RemoteURL,
			Branch:      doc.Branch,
			Agent:       doc.Agent,
			Provider:    doc.Provider,
			CreatedAt:   doc.CreatedAt,
			Tokens:      doc.TotalTokens,
			Messages:    doc.MessageCount,
			Errors:      doc.ErrorCount,
		}

		// Parse highlights.
		if len(hit.Highlight) > 0 {
			searchHit.Highlights = make(map[string]string)
			if summaryHL, ok := hit.Highlight["summary"]; ok && len(summaryHL) > 0 {
				searchHit.Highlights["summary"] = strings.Join(summaryHL, " … ")
			}
			if contentHL, ok := hit.Highlight["content"]; ok && len(contentHL) > 0 {
				searchHit.Highlights["content"] = strings.Join(contentHL, " … ")
			}
		}

		result.Hits = append(result.Hits, searchHit)
	}

	// Parse facets.
	if len(esResult.Aggregations) > 0 {
		result.Facets = make(map[string][]search.Facet)
		for name, agg := range esResult.Aggregations {
			var facets []search.Facet
			for _, bucket := range agg.Buckets {
				facets = append(facets, search.Facet{
					Value: bucket.Key,
					Count: bucket.DocCount,
				})
			}
			result.Facets[name] = facets
		}
	}

	return result
}

// ── Index / Delete ──

func (e *Engine) Index(ctx context.Context, doc search.Document) error {
	esDoc := esDocument{
		Summary:         doc.Summary,
		Content:         doc.Content,
		ToolNames:       doc.ToolNames,
		ProjectPath:     doc.ProjectPath,
		RemoteURL:       doc.RemoteURL,
		Branch:          doc.Branch,
		Agent:           doc.Agent,
		Provider:        doc.Provider,
		SessionType:     doc.SessionType,
		ProjectCategory: doc.ProjectCategory,
		CreatedAt:       doc.CreatedAt,
		TotalTokens:     doc.TotalTokens,
		MessageCount:    doc.MessageCount,
		ErrorCount:      doc.ErrorCount,
	}

	body, err := json.Marshal(esDoc)
	if err != nil {
		return fmt.Errorf("marshal document: %w", err)
	}

	// Use PUT with document ID for upsert.
	resp, err := e.doRequest(ctx, http.MethodPut, "/"+e.indexName+"/_doc/"+doc.ID, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("index document: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("index: status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (e *Engine) Delete(ctx context.Context, id string) error {
	resp, err := e.doRequest(ctx, http.MethodDelete, "/"+e.indexName+"/_doc/"+id, nil)
	if err != nil {
		return fmt.Errorf("delete document: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 404 is OK — document may not exist.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("delete: status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// ── Incremental Indexing ──

// IndexedSessionIDs returns the set of session IDs currently in the ES index.
// Uses a scroll query to efficiently enumerate all document IDs.
func (e *Engine) IndexedSessionIDs() (map[string]bool, error) {
	ctx := context.Background()

	// Use _search with stored_fields=[] to only get _id fields (no source).
	body := `{"query":{"match_all":{}},"stored_fields":[],"size":10000}`
	resp, err := e.doRequest(ctx, http.MethodPost, "/"+e.indexName+"/_search?scroll=1m", strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("scroll search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("scroll search: status %d: %s", resp.StatusCode, string(respBody))
	}

	var scrollResp struct {
		ScrollID string `json:"_scroll_id"`
		Hits     struct {
			Hits []struct {
				ID string `json:"_id"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&scrollResp); err != nil {
		return nil, fmt.Errorf("decode scroll: %w", err)
	}

	ids := make(map[string]bool)
	for _, hit := range scrollResp.Hits.Hits {
		ids[hit.ID] = true
	}

	// Continue scrolling if there are more documents.
	for len(scrollResp.Hits.Hits) > 0 {
		scrollBody := fmt.Sprintf(`{"scroll":"1m","scroll_id":"%s"}`, scrollResp.ScrollID)
		scrollResp.Hits.Hits = nil

		resp, err := e.doRequest(ctx, http.MethodPost, "/_search/scroll", strings.NewReader(scrollBody))
		if err != nil {
			break
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			break
		}
		if err := json.NewDecoder(resp.Body).Decode(&scrollResp); err != nil {
			_ = resp.Body.Close()
			break
		}
		_ = resp.Body.Close()

		for _, hit := range scrollResp.Hits.Hits {
			ids[hit.ID] = true
		}
	}

	// Clean up the scroll context.
	if scrollResp.ScrollID != "" {
		clearBody := fmt.Sprintf(`{"scroll_id":["%s"]}`, scrollResp.ScrollID)
		resp, err := e.doRequest(ctx, http.MethodDelete, "/_search/scroll", strings.NewReader(clearBody))
		if err == nil {
			_ = resp.Body.Close()
		}
	}

	return ids, nil
}

// ── HTTP Helper ──

func (e *Engine) doRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	url := e.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return e.client.Do(req)
}
