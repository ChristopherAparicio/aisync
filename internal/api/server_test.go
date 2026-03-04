package api_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/api"
	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
)

// newTestServer creates an API server backed by a temporary SQLite store
// with only a SessionService (no SyncService, no providers).
func newTestServer(t *testing.T) (http.Handler, *service.SessionService) {
	t.Helper()
	store := testutil.MustOpenStore(t)

	sessionSvc := service.NewSessionService(service.SessionServiceConfig{
		Store:     store,
		Registry:  provider.NewRegistry(),
		Converter: converter.New(),
	})

	srv := api.New(api.Config{
		SessionService: sessionSvc,
		Addr:           "127.0.0.1:0",
	})

	return srv.Handler(), sessionSvc
}

// seedSession saves a test session directly via the service and returns it.
func seedSession(t *testing.T, store *service.SessionService, id string) *session.Session {
	t.Helper()
	// We can't call Capture without a provider, so we build a session manually.
	// We'll use the testutil helper and store it.
	// Since SessionService doesn't expose a direct Save, we need to go through import.
	sess := testutil.NewSession(id)
	data, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}

	_, err = store.Import(service.ImportRequest{
		Data:         data,
		SourceFormat: "aisync",
		IntoTarget:   "aisync",
	})
	if err != nil {
		t.Fatalf("import session: %v", err)
	}
	return sess
}

func doRequest(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req := httptest.NewRequest(method, path, reqBody)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func decodeResponse(t *testing.T, resp *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response body: %v\nbody: %s", err, resp.Body.String())
	}
}

// ── Health ──

func TestHealthEndpoint(t *testing.T) {
	handler, _ := newTestServer(t)
	resp := doRequest(t, handler, "GET", "/api/v1/health", nil)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result map[string]string
	decodeResponse(t, resp, &result)
	if result["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", result["status"])
	}
}

// ── Get / List / Delete ──

func TestGetSession(t *testing.T) {
	handler, svc := newTestServer(t)
	sess := seedSession(t, svc, "test-session-1")

	resp := doRequest(t, handler, "GET", "/api/v1/sessions/"+string(sess.ID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var got session.Session
	decodeResponse(t, resp, &got)
	if got.ID != sess.ID {
		t.Errorf("expected ID %s, got %s", sess.ID, got.ID)
	}
}

func TestGetSessionNotFound(t *testing.T) {
	handler, _ := newTestServer(t)
	resp := doRequest(t, handler, "GET", "/api/v1/sessions/nonexistent", nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestListSessions(t *testing.T) {
	handler, svc := newTestServer(t)
	seedSession(t, svc, "list-1")
	seedSession(t, svc, "list-2")

	resp := doRequest(t, handler, "GET", "/api/v1/sessions?all=true", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var summaries []session.Summary
	decodeResponse(t, resp, &summaries)
	if len(summaries) < 2 {
		t.Errorf("expected at least 2 sessions, got %d", len(summaries))
	}
}

func TestDeleteSession(t *testing.T) {
	handler, svc := newTestServer(t)
	sess := seedSession(t, svc, "delete-me")

	// Delete
	resp := doRequest(t, handler, "DELETE", "/api/v1/sessions/"+string(sess.ID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	// Verify it's gone
	resp = doRequest(t, handler, "GET", "/api/v1/sessions/"+string(sess.ID), nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d: %s", resp.Code, resp.Body.String())
	}
}

// ── Export ──

func TestExportSession(t *testing.T) {
	handler, svc := newTestServer(t)
	sess := seedSession(t, svc, "export-test")

	body := map[string]string{
		"session_id": string(sess.ID),
		"format":     "aisync",
	}

	resp := doRequest(t, handler, "POST", "/api/v1/sessions/export", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result struct {
		Data      string `json:"data"`
		Format    string `json:"format"`
		SessionID string `json:"session_id"`
	}
	decodeResponse(t, resp, &result)

	if result.Format != "aisync" {
		t.Errorf("expected format=aisync, got %q", result.Format)
	}
	if result.SessionID != string(sess.ID) {
		t.Errorf("expected session_id=%s, got %s", sess.ID, result.SessionID)
	}

	// Verify base64-encoded data decodes to valid JSON
	decoded, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	var exported session.Session
	if err := json.Unmarshal(decoded, &exported); err != nil {
		t.Fatalf("unmarshal exported session: %v", err)
	}
	if exported.ID != sess.ID {
		t.Errorf("exported session ID mismatch: %s vs %s", exported.ID, sess.ID)
	}
}

// ── Import ──

func TestImportSession(t *testing.T) {
	handler, _ := newTestServer(t)

	sess := testutil.NewSession("import-via-api")
	data, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	body := map[string]string{
		"data":          base64.StdEncoding.EncodeToString(data),
		"source_format": "aisync",
		"into_target":   "aisync",
	}

	resp := doRequest(t, handler, "POST", "/api/v1/sessions/import", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result struct {
		SessionID    string `json:"SessionID"`
		SourceFormat string `json:"SourceFormat"`
		Target       string `json:"Target"`
	}
	decodeResponse(t, resp, &result)

	if result.SessionID != string(sess.ID) {
		t.Errorf("expected session ID %s, got %s", sess.ID, result.SessionID)
	}

	// Verify it's retrievable
	resp = doRequest(t, handler, "GET", "/api/v1/sessions/"+string(sess.ID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("imported session not retrievable: %d %s", resp.Code, resp.Body.String())
	}
}

// ── Stats ──

func TestStats(t *testing.T) {
	handler, svc := newTestServer(t)
	seedSession(t, svc, "stats-1")
	seedSession(t, svc, "stats-2")

	resp := doRequest(t, handler, "GET", "/api/v1/stats?all=true", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result struct {
		TotalSessions int `json:"TotalSessions"`
		TotalMessages int `json:"TotalMessages"`
		TotalTokens   int `json:"TotalTokens"`
	}
	decodeResponse(t, resp, &result)

	if result.TotalSessions < 2 {
		t.Errorf("expected at least 2 sessions in stats, got %d", result.TotalSessions)
	}
}

// ── Sync without SyncService ──

func TestSyncUnavailable(t *testing.T) {
	handler, _ := newTestServer(t) // no SyncService wired

	tests := []struct {
		method string
		path   string
		body   any
	}{
		{"POST", "/api/v1/sync/push", map[string]bool{"remote": false}},
		{"POST", "/api/v1/sync/pull", map[string]bool{"remote": false}},
		{"POST", "/api/v1/sync/sync", map[string]bool{"remote": false}},
		{"GET", "/api/v1/sync/index", nil},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			resp := doRequest(t, handler, tt.method, tt.path, tt.body)
			if resp.Code != http.StatusServiceUnavailable {
				t.Errorf("expected 503, got %d: %s", resp.Code, resp.Body.String())
			}
		})
	}
}

// ── Error cases ──

func TestBadJSON(t *testing.T) {
	handler, _ := newTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/sessions/export", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeleteNotFound(t *testing.T) {
	handler, _ := newTestServer(t)
	resp := doRequest(t, handler, "DELETE", "/api/v1/sessions/does-not-exist", nil)
	if resp.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestImportBadBase64(t *testing.T) {
	handler, _ := newTestServer(t)
	body := map[string]string{
		"data": "not-valid-base64!!!",
	}
	resp := doRequest(t, handler, "POST", "/api/v1/sessions/import", body)
	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", resp.Code, resp.Body.String())
	}
}

// ── Search ──

func TestSearchSessions(t *testing.T) {
	handler, svc := newTestServer(t)
	seedSession(t, svc, "search-1")
	seedSession(t, svc, "search-2")

	resp := doRequest(t, handler, "GET", "/api/v1/sessions/search?keyword=Test+session", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result session.SearchResult
	decodeResponse(t, resp, &result)
	if result.TotalCount < 2 {
		t.Errorf("expected at least 2 results, got %d", result.TotalCount)
	}
	if len(result.Sessions) < 2 {
		t.Errorf("expected at least 2 sessions, got %d", len(result.Sessions))
	}
}

func TestSearchSessionsNoResults(t *testing.T) {
	handler, _ := newTestServer(t)

	resp := doRequest(t, handler, "GET", "/api/v1/sessions/search?keyword=nonexistent", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result session.SearchResult
	decodeResponse(t, resp, &result)
	if result.TotalCount != 0 {
		t.Errorf("expected 0 results, got %d", result.TotalCount)
	}
}

func TestSearchSessionsWithFilters(t *testing.T) {
	handler, svc := newTestServer(t)
	seedSession(t, svc, "filter-1")
	seedSession(t, svc, "filter-2")

	// Filter by branch from testutil.NewSession (uses "feature/test")
	resp := doRequest(t, handler, "GET", "/api/v1/sessions/search?branch=feature/test", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result session.SearchResult
	decodeResponse(t, resp, &result)
	if result.TotalCount < 2 {
		t.Errorf("expected at least 2 results for branch filter, got %d", result.TotalCount)
	}
}

func TestSearchSessionsPagination(t *testing.T) {
	handler, svc := newTestServer(t)
	seedSession(t, svc, "page-1")
	seedSession(t, svc, "page-2")
	seedSession(t, svc, "page-3")

	resp := doRequest(t, handler, "GET", "/api/v1/sessions/search?limit=2&offset=0", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result session.SearchResult
	decodeResponse(t, resp, &result)
	if result.TotalCount < 3 {
		t.Errorf("expected at least 3 total, got %d", result.TotalCount)
	}
	if len(result.Sessions) != 2 {
		t.Errorf("expected 2 sessions (limit=2), got %d", len(result.Sessions))
	}
	if result.Limit != 2 {
		t.Errorf("expected limit=2, got %d", result.Limit)
	}
}

func TestSearchSessionsBadProvider(t *testing.T) {
	handler, _ := newTestServer(t)

	resp := doRequest(t, handler, "GET", "/api/v1/sessions/search?provider=invalid", nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid provider, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestExportUnknownFormat(t *testing.T) {
	handler, svc := newTestServer(t)
	sess := seedSession(t, svc, "bad-format-test")

	body := map[string]string{
		"session_id": string(sess.ID),
		"format":     "xml",
	}
	resp := doRequest(t, handler, "POST", "/api/v1/sessions/export", body)
	if resp.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d: %s", resp.Code, resp.Body.String())
	}
}

// ── Sync Index handler with GET (no body) ──

func TestSyncIndexGET(t *testing.T) {
	handler, _ := newTestServer(t)
	resp := doRequest(t, handler, "GET", "/api/v1/sync/index", nil)

	// Should return 503 since no SyncService is configured
	if resp.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d: %s", resp.Code, resp.Body.String())
	}
}
